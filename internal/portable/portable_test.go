package portable_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/manifest"
	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
	"github.com/RodBarenco/fluxa-builder/internal/portable"
	runtimepkg "github.com/RodBarenco/fluxa-builder/internal/runtime"
)

func TestBuildPortableNamesWithSpacesAndUnicode(t *testing.T) {
	fixture := newFixture(t, "Minha Ação Espacial", true)
	signatureData := []byte("{\"signature\":\"fixture\"}\n")
	signaturePath := filepath.Join(filepath.Dir(fixture.request.PackagePath), "package.sig")
	if err := os.WriteFile(signaturePath, signatureData, 0o600); err != nil {
		t.Fatal(err)
	}
	signatureDigest := sha256.Sum256(signatureData)
	fixture.request.SignaturePath = signaturePath
	fixture.request.SignatureHash = hex.EncodeToString(signatureDigest[:])
	fixture.request.SigningKeyID = strings.Repeat("b", 64)
	result, err := portable.Build(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if result.Name != "minha-ação-espacial" {
		t.Fatalf("Name = %q", result.Name)
	}
	if filepath.Base(result.Executable) != result.Name || filepath.Base(result.Package) != result.Name+".flxpkg" {
		t.Fatalf("result paths = %#v", result)
	}
	if filepath.Base(result.Signature) != result.Name+".flxpkg.sig" {
		t.Fatalf("Signature = %q", result.Signature)
	}
	if _, err := flxpkg.Verify(result.Package); err != nil {
		t.Fatalf("copied package invalid: %v", err)
	}
	data, err := os.ReadFile(result.BuildInfo) // #nosec G304 -- test-controlled result.
	if err != nil {
		t.Fatal(err)
	}
	var info map[string]any
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	if info["name"] != "Minha Ação Espacial" || info["source_exposed"] != true ||
		info["signing_key_id"] != fixture.request.SigningKeyID {
		t.Fatalf("build-info = %#v", info)
	}
}

func TestBuildPortableRejectsMissingInputsPermissionAndExistingOutput(t *testing.T) {
	fixture := newFixture(t, "Game", true)
	request := fixture.request
	request.PackagePath = filepath.Join(t.TempDir(), "missing.flxpkg")
	_, err := portable.Build(context.Background(), request)
	assertPortableError(t, err)

	request = fixture.request
	request.Runtime.BinaryPath = filepath.Join(t.TempDir(), "missing-runtime")
	_, err = portable.Build(context.Background(), request)
	assertPortableError(t, err)

	if goruntime.GOOS != "windows" {
		request = fixture.request
		if err := os.Chmod(request.Runtime.BinaryPath, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = portable.Build(context.Background(), request)
		assertPortableKind(t, err, portable.ErrorPermission)
	}

	fixture = newFixture(t, "Existing", true)
	existing := filepath.Join(fixture.request.OutputRoot, "existing")
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = portable.Build(context.Background(), fixture.request)
	assertPortableError(t, err)
	if _, statErr := os.Stat(existing); statErr != nil {
		t.Fatalf("existing output was removed: %v", statErr)
	}
}

func TestSmokeStartsApplicationAndRejectsFailureOrTamperedPackage(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("portable shell runtime fixture is Unix-only")
	}
	fixture := newFixture(t, "Smoke", true)
	result, err := portable.Build(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	report, err := portable.SmokeDetailed(context.Background(), result, 2*time.Second)
	if err != nil {
		t.Fatalf("Smoke() error = %v", err)
	}
	if !report.PackageOpened || !report.VMCompatible || report.UIOpened || report.PackageSHA256 == "" {
		t.Fatalf("SmokeDetailed() report = %#v", report)
	}

	failing := newFixture(t, "Failing", false)
	failingResult, err := portable.Build(context.Background(), failing.request)
	if err != nil {
		t.Fatal(err)
	}
	err = portable.Smoke(context.Background(), failingResult, 2*time.Second)
	assertPortableKind(t, err, portable.ErrorSmoke)

	if err := os.WriteFile(result.Package, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = portable.Smoke(context.Background(), result, 2*time.Second)
	assertPortableKind(t, err, portable.ErrorIntegrity)
}

func TestSmokeRejectsIncompatibleRuntimeTimeoutCrashAndInvalidProtocol(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("portable shell runtime fixture is Unix-only")
	}
	tests := []struct {
		name    string
		body    func(string) string
		timeout time.Duration
		kind    portable.ErrorKind
	}{
		{
			name: "incompatible",
			body: func(hash string) string {
				return smokeScript(hash, true, false, false)
			},
			timeout: time.Second,
			kind:    portable.ErrorSmokeIncompatible,
		},
		{
			name: "timeout",
			body: func(string) string {
				return "#!/bin/sh\nwhile :; do :; done\n"
			},
			timeout: 30 * time.Millisecond,
			kind:    portable.ErrorSmokeTimeout,
		},
		{
			name: "crash",
			body: func(string) string {
				return "#!/bin/sh\nkill -SEGV $$\n"
			},
			timeout: time.Second,
			kind:    portable.ErrorSmokeCrash,
		},
		{
			name: "invalid protocol",
			body: func(string) string {
				return "#!/bin/sh\nprintf '%s\\n' '{\"protocol\":\"wrong\"}'\n"
			},
			timeout: time.Second,
			kind:    portable.ErrorSmokeProtocol,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixtureWithScript(t, "Smoke "+test.name, test.body)
			result, err := portable.Build(context.Background(), fixture.request)
			if err != nil {
				t.Fatal(err)
			}
			report, err := portable.SmokeDetailed(context.Background(), result, test.timeout)
			assertPortableKind(t, err, test.kind)
			if report.ExitCode == 0 && test.kind == portable.ErrorSmokeCrash {
				t.Fatalf("crash report = %#v", report)
			}
		})
	}
}

type fixture struct {
	request portable.Request
}

func newFixture(t *testing.T, name string, successfulRuntime bool) fixture {
	t.Helper()
	if !successfulRuntime {
		return newFixtureWithScript(t, name, func(string) string {
			return "#!/bin/sh\nprintf '%s\\n' 'runtime rejected package' >&2\nexit 7\n"
		})
	}
	return newFixtureWithScript(t, name, func(hash string) string {
		return smokeScript(hash, true, true, false)
	})
}

func newFixtureWithScript(t *testing.T, name string, makeScript func(string) string) fixture {
	t.Helper()
	root := t.TempDir()
	packagePath := makePackage(t, root)
	packageInfo, err := flxpkg.Verify(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "fluxa-runtime")
	script := makeScript(packageInfo.SHA256)
	if err := os.WriteFile(runtimePath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(runtimePath, 0o700); err != nil { // #nosec G302 -- executable runtime fixture.
		t.Fatal(err)
	}
	runtimeHash := sha256.Sum256([]byte(script))
	return fixture{request: portable.Request{
		OutputRoot:    filepath.Join(root, "output"),
		ProjectName:   name,
		ProjectID:     "com.example.game",
		Version:       "1.0.0",
		TargetOS:      "linux",
		TargetArch:    "amd64",
		Terminal:      true,
		PackagePath:   packagePath,
		PackageSHA256: packageInfo.SHA256,
		Runtime: runtimepkg.Runtime{
			BinaryPath: runtimePath,
			Metadata: runtimepkg.Metadata{
				OS: "linux", Arch: "amd64", Terminal: true,
				BinarySHA256: hex.EncodeToString(runtimeHash[:]),
			},
		},
		SourceExposed: true,
	}}
}

func smokeScript(hash string, opened, compatible, uiOpened bool) string {
	response := map[string]any{
		"protocol":       "fluxa-package-self-test-v1",
		"package_sha256": hash,
		"package_opened": opened,
		"vm_compatible":  compatible,
		"ui_opened":      uiOpened,
	}
	data, err := json.Marshal(response)
	if err != nil {
		panic(err)
	}
	return "#!/bin/sh\nif [ \"$1\" = \"--fluxa-package-self-test\" ] && [ -f \"$0.flxpkg\" ]; then\nprintf '%s\\n' '" +
		string(data) + "'\nexit 0\nfi\nexit 9\n"
}

func makePackage(t *testing.T, root string) string {
	t.Helper()
	outputRoot := filepath.Join(root, "output")
	if err := os.Mkdir(outputRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "main.flx")
	data := []byte("main")
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	file := manifest.File{
		Path: "program/source/main.flx", LogicalPath: "main.flx", Kind: "program",
		Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]),
	}
	hash := strings.Repeat("a", 64)
	value := manifest.Manifest{
		FormatVersion: manifest.CurrentFormatVersion,
		Project: manifest.Project{
			Name: "Game", ID: "com.example.game", Version: "1.0.0", Entry: "main.flx", Type: "desktop",
		},
		Toolchain: manifest.Toolchain{
			Protocol: "runtime-info-v1", FluxaSHA256: hash, LibrariesSHA256: hash,
		},
		Target: manifest.Target{OS: "linux", Arch: "amd64", Terminal: true},
		Build: manifest.Build{
			Preflight: "not_run", ProgramFormat: "fluxa-source", Debug: true, SourceExposed: true,
		},
		Files: []manifest.File{file},
	}
	packagePath := filepath.Join(root, "input.flxpkg")
	if _, err := flxpkg.Write(context.Background(), flxpkg.Request{
		OutputPath: packagePath,
		Manifest:   value,
		Sources:    map[string]string{file.Path: source},
	}); err != nil {
		t.Fatal(err)
	}
	return packagePath
}

func assertPortableError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want portable error")
	}
	var portableError *portable.Error
	if !errors.As(err, &portableError) {
		t.Fatalf("error type = %T, want *portable.Error: %v", err, err)
	}
}

func assertPortableKind(t *testing.T, err error, want portable.ErrorKind) {
	t.Helper()
	assertPortableError(t, err)
	var portableError *portable.Error
	if !errors.As(err, &portableError) || portableError.Kind != want {
		t.Fatalf("error kind = %q, want %q: %v", portableError.Kind, want, err)
	}
}
