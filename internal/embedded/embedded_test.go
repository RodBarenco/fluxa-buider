package embedded_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/embedded"
	"github.com/RodBarenco/fluxa-builder/internal/manifest"
	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
	"github.com/RodBarenco/fluxa-builder/internal/portable"
)

func TestBuildVerifyAndExecuteEmbeddedApplication(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("embedded shell runtime fixture is Unix-only")
	}
	fixture := newFixture(t)
	info, err := embedded.Build(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := embedded.Verify(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	if verified.RuntimeSize < 0 || verified.PackageSHA256 != fixture.request.PackageHash ||
		verified.PackageOffset != uint64(verified.RuntimeSize) || // #nosec G115 -- negativity checked first.
		verified.Package.Manifest.Project.ID != "com.example.embedded" {
		t.Fatalf("Verify() = %#v", verified)
	}
	if _, err := portable.SmokeExecutable(
		context.Background(), info.Path, filepath.Dir(info.Path), info.PackageSHA256, time.Second,
	); err != nil {
		t.Fatalf("embedded executable self-test failed: %v", err)
	}
}

func TestVerifyRejectsMissingCorruptOrInconsistentFooter(t *testing.T) {
	fixture := newFixture(t)
	info, err := embedded.Build(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(info.Path) // #nosec G304 -- test fixture.
	if err != nil {
		t.Fatal(err)
	}
	footer := len(original) - int(embedded.FooterSize)
	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"footer absent", func(data []byte) []byte { return append([]byte(nil), data[:footer]...) }},
		{"footer corrupt", func(data []byte) []byte { data[footer] ^= 1; return data }},
		{"offset invalid", func(data []byte) []byte {
			binary.LittleEndian.PutUint64(data[footer+12:footer+20], 0)
			return data
		}},
		{"size invalid", func(data []byte) []byte {
			size := binary.LittleEndian.Uint64(data[footer+20 : footer+28])
			binary.LittleEndian.PutUint64(data[footer+20:footer+28], size-1)
			return data
		}},
		{"executable truncated", func(data []byte) []byte { return data[:len(data)-1] }},
		{"package altered", func(data []byte) []byte {
			offset := binary.LittleEndian.Uint64(data[footer+12 : footer+20])
			if offset >= uint64(len(data)) {
				t.Fatal("fixture package offset is outside file")
			}
			data[int(offset)] ^= 1 // #nosec G115 -- checked against slice length.
			return data
		}},
		{"bytes extra", func(data []byte) []byte { return append(data, 0) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mutated")
			data := test.mutate(append([]byte(nil), original...))
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := embedded.Verify(path); err == nil {
				t.Fatal("Verify() accepted mutated executable")
			}
		})
	}
}

func TestVerifyRejectsFileAboveSizeLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- test-controlled temporary path.
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(file.Truncate(4<<30), file.Close()); err != nil {
		t.Fatal(err)
	}
	_, err = embedded.Verify(path)
	assertEmbeddedKind(t, err, embedded.ErrorLimit)
}

type fixture struct {
	request embedded.Request
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	packagePath := makePackage(t, root)
	packageInfo, err := flxpkg.Verify(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	response := `{"protocol":"fluxa-package-self-test-v1","package_sha256":"` +
		packageInfo.SHA256 + `","package_opened":true,"vm_compatible":true,"ui_opened":false}`
	runtimePath := filepath.Join(root, "runtime")
	script := "#!/bin/sh\nif [ \"$1\" = \"--fluxa-package-self-test\" ]; then\nprintf '%s\\n' '" +
		response + "'\nexit 0\nfi\nexit 9\n"
	if err := os.WriteFile(runtimePath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(runtimePath, 0o700); err != nil { // #nosec G302 -- executable runtime fixture.
		t.Fatal(err)
	}
	return fixture{request: embedded.Request{
		RuntimePath: runtimePath, PackagePath: packagePath,
		OutputPath:  filepath.Join(root, "application"),
		PackageHash: packageInfo.SHA256, ExecutableOS: "linux",
	}}
}

func makePackage(t *testing.T, root string) string {
	t.Helper()
	source := filepath.Join(root, "main.flx")
	data := []byte(`print("embedded")`)
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
			Name: "Embedded", ID: "com.example.embedded", Version: "1.0.0", Entry: "main.flx", Type: "desktop",
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
	output := filepath.Join(root, "application.flxpkg")
	if _, err := flxpkg.Write(context.Background(), flxpkg.Request{
		OutputPath: output, Manifest: value, Sources: map[string]string{file.Path: source},
	}); err != nil {
		t.Fatal(err)
	}
	return output
}

func assertEmbeddedKind(t *testing.T, err error, want embedded.ErrorKind) {
	t.Helper()
	var embeddedError *embedded.Error
	if !errors.As(err, &embeddedError) || embeddedError.Kind != want {
		t.Fatalf("error = %v, want embedded kind %s", err, want)
	}
}
