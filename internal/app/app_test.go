package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	buildpkg "github.com/RodBarenco/fluxa-builder/internal/build"
	"github.com/RodBarenco/fluxa-builder/internal/collector"
	"github.com/RodBarenco/fluxa-builder/internal/compiler"
	"github.com/RodBarenco/fluxa-builder/internal/manifest"
	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
	"github.com/RodBarenco/fluxa-builder/internal/portable"
	runtimepkg "github.com/RodBarenco/fluxa-builder/internal/runtime"
	"github.com/RodBarenco/fluxa-builder/internal/toolchain"
)

func TestRunVersion(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run(version) code = %d, want 0; stderr = %q", code, stderr.String())
	}
	want := Name + " " + Version + "\n"
	if stdout.String() != want {
		t.Fatalf("Run(version) stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("Run(version) stderr = %q, want empty", stderr.String())
	}
}

func TestRunHelp(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run(help) code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "fluxa-builder version") {
		t.Fatalf("Run(help) stdout = %q, want version usage", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("Run(help) stderr = %q, want empty", stderr.String())
	}
}

func TestRunWithoutCommand(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run(nil, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("Run(nil) code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("Run(nil) stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("Run(nil) stderr = %q, want usage", stderr.String())
	}
}

func TestRunUnknownCommand(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"unknown"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("Run(unknown) code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("Run(unknown) stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), `unknown command "unknown"`) {
		t.Fatalf("Run(unknown) stderr = %q, want contextual error", stderr.String())
	}
}

func TestRunRejectsVersionArguments(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"version", "extra"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("Run(version extra) code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "does not accept arguments") {
		t.Fatalf("Run(version extra) stderr = %q, want argument error", stderr.String())
	}
}

func TestRunBuildPublishesPortableProject(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	config := `[project]
name = "CLI Test"
id = "com.example.cli-test"
version = "1.0.0"
entry = "main.flx"

[build]
terminal = false
`
	if err := os.WriteFile(filepath.Join(root, "fluxa.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.flx"), []byte(`print("ok")`), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "runtime-fixture")
	runtimeBytes := []byte("runtime fixture")
	if err := os.WriteFile(runtimePath, runtimeBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(runtimePath, 0o700); err != nil { // #nosec G302 -- executable runtime fixture.
		t.Fatal(err)
	}
	runtimeHash := sha256.Sum256(runtimeBytes)
	targetOS := runtime.GOOS
	if targetOS == "darwin" {
		targetOS = "macos"
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	dependencies := buildDependencies{
		resolve: func(options toolchain.ResolveOptions) (toolchain.Candidate, error) {
			if options.ExplicitPath != "/opt/fluxa/bin/fluxa" {
				t.Errorf("ExplicitPath = %q", options.ExplicitPath)
			}
			return toolchain.Candidate{
				Path:   "/opt/fluxa/bin/fluxa",
				Source: toolchain.SourceExplicit,
			}, nil
		},
		probe: func(context.Context, string, time.Duration) (toolchain.Identity, error) {
			return toolchain.Identity{
				Protocol: "runtime-info-v1",
				SHA256:   strings.Repeat("a", 64),
			}, nil
		},
		newWorkspace:  buildpkg.NewWorkspace,
		collect:       collector.CollectProject,
		compile:       compiler.Compile,
		newManifest:   manifest.New,
		writeManifest: manifest.WriteFile,
		writePackage:  flxpkg.Write,
		resolveRuntime: func(string, runtimepkg.Requirement) (runtimepkg.Runtime, error) {
			return runtimepkg.Runtime{
				BinaryPath: runtimePath,
				Metadata: runtimepkg.Metadata{
					OS: targetOS, Arch: runtime.GOARCH, Terminal: false,
					BinarySHA256: hex.EncodeToString(runtimeHash[:]),
				},
			}, nil
		},
		buildPortable: portable.Build,
		smokePortable: func(context.Context, portable.Result, time.Duration) error {
			return nil
		},
		archivePortable: portable.Archive,
	}
	code := runBuild([]string{root, "--fluxa", "/opt/fluxa/bin/fluxa", "--include-source"}, &stdout, &stderr, dependencies)

	if code != 0 {
		t.Fatalf("Run(build) code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Project configuration valid") ||
		!strings.Contains(stdout.String(), "Terminal: false") ||
		!strings.Contains(stdout.String(), "Fluxa toolchain selected") {
		t.Fatalf("Run(build) stdout = %q, want loaded project summary", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Files collected: 1") {
		t.Fatalf("Run(build) stdout = %q, want collection summary", stdout.String())
	}
	if !strings.Contains(stdout.String(), "development/source-exposed") ||
		!strings.Contains(stdout.String(), "not a secure release") {
		t.Fatalf("Run(build) stdout = %q, want exposed-source warning", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Manifest schema: 1") {
		t.Fatalf("Run(build) stdout = %q, want manifest summary", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Fluxa package: com.example.cli-test-1.0.0.flxpkg") {
		t.Fatalf("Run(build) stdout = %q, want package summary", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Runtime selected:") {
		t.Fatalf("Run(build) stdout = %q, want runtime summary", stdout.String())
	}
	archiveExtension := ".tar.gz"
	if targetOS == "windows" {
		archiveExtension = ".zip"
	}
	if !strings.Contains(stdout.String(), "Distribution archive:") ||
		!strings.Contains(stdout.String(), "Archive SHA-256:") {
		t.Fatalf("Run(build) stdout = %q, want archive summary", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("Run(build) stderr = %q, want empty", stderr.String())
	}
	targetOutput := filepath.Join(root, "dist", targetDirectoryName(targetOS, runtime.GOARCH))
	if _, err := os.Stat(filepath.Join(targetOutput, "cli-test")); err != nil {
		t.Fatalf("portable output missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetOutput, "cli-test"+archiveExtension)); err != nil {
		t.Fatalf("distribution archive missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetOutput, "cli-test"+archiveExtension+".sha256")); err != nil {
		t.Fatalf("archive checksum missing: %v", err)
	}
	workDir := filepath.Join(root, ".fluxa-builder", "work")
	entries, err := os.ReadDir(workDir)
	if err != nil {
		t.Fatalf("ReadDir(work) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("workspace was not cleaned: %v", entries)
	}

	if err := os.RemoveAll(filepath.Join(root, "dist")); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	dependencies.smokePortable = func(context.Context, portable.Result, time.Duration) error {
		return errors.New("runtime rejected package")
	}
	code = runBuild([]string{root, "--fluxa", "/opt/fluxa/bin/fluxa", "--include-source"}, &stdout, &stderr, dependencies)
	if code != 1 || !strings.Contains(stderr.String(), "smoke test failed") {
		t.Fatalf("failed smoke code=%d stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "dist")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed smoke published dist: %v", err)
	}
}

func TestResolveManifestTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		wantOS   string
		wantArch string
		wantErr  bool
	}{
		{input: "linux-x64", wantOS: "linux", wantArch: "amd64"},
		{input: "windows-arm64", wantOS: "windows", wantArch: "arm64"},
		{input: "macos-x64", wantOS: "macos", wantArch: "amd64"},
		{input: "freebsd-x64", wantErr: true},
		{input: "linux-386", wantErr: true},
		{input: "invalid", wantErr: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			gotOS, gotArch, err := resolveManifestTarget(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("resolveManifestTarget() error = nil")
				}
				return
			}
			if err != nil || gotOS != tt.wantOS || gotArch != tt.wantArch {
				t.Fatalf("resolveManifestTarget() = %q, %q, %v", gotOS, gotArch, err)
			}
		})
	}
}

func TestRunBuildReportsConfigError(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"build", t.TempDir()}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("Run(build missing config) code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "failed to load project") ||
		!strings.Contains(stderr.String(), "fluxa.toml") {
		t.Fatalf("Run(build missing config) stderr = %q, want contextual error", stderr.String())
	}
}

func TestRuntimeAddAndListCommands(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	registry := filepath.Join(root, "registry")
	binary := filepath.Join(root, "fluxa-runtime")
	binaryData := []byte("runtime")
	if err := os.WriteFile(binary, binaryData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(binary, 0o700); err != nil { // #nosec G302 -- executable fixture for runtime registry.
		t.Fatal(err)
	}
	binaryHash := sha256.Sum256(binaryData)
	emptyHash := sha256.Sum256(nil)
	metadata := filepath.Join(root, "runtime.json")
	metadataJSON := fmt.Sprintf(`{
  "format_version": 1,
  "fluxa_version": "unreported",
  "toolchain_sha256": "%s",
  "package_format_version": 1,
  "bytecode_version": "",
  "bytecode_abi": "",
  "libraries_sha256": "%s",
  "program_formats": ["fluxa-source"],
  "os": "linux",
  "arch": "amd64",
  "terminal": true,
  "binary_name": "fluxa-runtime",
  "binary_sha256": "%s"
}`, strings.Repeat("a", 64), hex.EncodeToString(emptyHash[:]), hex.EncodeToString(binaryHash[:]))
	if err := os.WriteFile(metadata, []byte(metadataJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"runtime", "add", binary, "--metadata", metadata, "--registry", registry}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runtime add code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"runtime", "list", "--registry", registry}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "unreported  linux/amd64") {
		t.Fatalf("runtime list code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestParseBuildOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		args      []string
		want      buildOptions
		wantError string
	}{
		{
			name: "defaults",
			want: buildOptions{projectPath: "."},
		},
		{
			name: "project then flag",
			args: []string{"my project", "--fluxa", "/opt/fluxa"},
			want: buildOptions{projectPath: "my project", fluxaPath: "/opt/fluxa"},
		},
		{
			name: "flag then project",
			args: []string{"--fluxa", "/opt/fluxa", "my project"},
			want: buildOptions{projectPath: "my project", fluxaPath: "/opt/fluxa"},
		},
		{
			name: "keep workspace",
			args: []string{"my project", "--keep-work"},
			want: buildOptions{projectPath: "my project", keepWork: true},
		},
		{
			name: "explicit development source fallback",
			args: []string{"my project", "--include-source"},
			want: buildOptions{projectPath: "my project", includeSource: true},
		},
		{
			name: "runtime registry",
			args: []string{"my project", "--runtime-registry", "/tmp/runtimes"},
			want: buildOptions{projectPath: "my project", runtimeRegistry: "/tmp/runtimes"},
		},
		{
			name:      "missing flag value",
			args:      []string{"--fluxa"},
			wantError: "requires",
		},
		{
			name:      "unknown flag",
			args:      []string{"--unknown"},
			wantError: "unknown",
		},
		{
			name:      "two projects",
			args:      []string{"one", "two"},
			wantError: "at most one",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseBuildOptions(tt.args)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("parseBuildOptions() error = %v, want %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseBuildOptions() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("parseBuildOptions() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
