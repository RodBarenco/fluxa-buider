package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	buildpkg "github.com/RodBarenco/fluxa-builder/internal/build"
	"github.com/RodBarenco/fluxa-builder/internal/collector"
	"github.com/RodBarenco/fluxa-builder/internal/compiler"
	"github.com/RodBarenco/fluxa-builder/internal/manifest"
	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
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

func TestRunBuildLoadsProjectThenStops(t *testing.T) {
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
	}
	code := runBuild([]string{root, "--fluxa", "/opt/fluxa/bin/fluxa", "--include-source"}, &stdout, &stderr, dependencies)

	if code != 1 {
		t.Fatalf("Run(build) code = %d, want 1", code)
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
	if !strings.Contains(stderr.String(), "runtime selection is not implemented yet") {
		t.Fatalf("Run(build) stderr = %q, want phase boundary", stderr.String())
	}
	workDir := filepath.Join(root, ".fluxa-builder", "work")
	entries, err := os.ReadDir(workDir)
	if err != nil {
		t.Fatalf("ReadDir(work) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("workspace was not cleaned: %v", entries)
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
