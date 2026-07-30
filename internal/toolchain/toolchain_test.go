package toolchain_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/toolchain"
)

func TestMain(m *testing.M) {
	if os.Getenv("GO_WANT_FLUXA_HELPER") == "1" {
		runFluxaHelper()
		return
	}
	os.Exit(m.Run())
}

func TestResolveOrder(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	explicit := fakeExecutable(t, root, "explicit")
	configured := fakeExecutable(t, root, "configured")
	homeDir := filepath.Join(root, "home")
	home := fakeExecutable(t, homeDir, executableName())
	pathDir := filepath.Join(root, "path")
	fromPath := fakeExecutable(t, pathDir, executableName())

	tests := []struct {
		name string
		opts toolchain.ResolveOptions
		want string
	}{
		{
			name: "explicit wins",
			opts: toolchain.ResolveOptions{
				ExplicitPath: explicit,
				ConfigPath:   configured,
				FluxaHome:    homeDir,
				PathEnv:      pathDir,
				ProjectRoot:  root,
			},
			want: explicit,
		},
		{
			name: "config wins",
			opts: toolchain.ResolveOptions{
				ConfigPath:  configured,
				FluxaHome:   homeDir,
				PathEnv:     pathDir,
				ProjectRoot: root,
			},
			want: configured,
		},
		{
			name: "FLUXA_HOME directory wins",
			opts: toolchain.ResolveOptions{
				FluxaHome:   homeDir,
				PathEnv:     pathDir,
				ProjectRoot: root,
			},
			want: home,
		},
		{
			name: "PATH fallback",
			opts: toolchain.ResolveOptions{
				PathEnv:     pathDir,
				ProjectRoot: root,
			},
			want: fromPath,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := toolchain.Resolve(tt.opts)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			want := canonicalPath(t, tt.want)
			if got.Path != want {
				t.Errorf("Resolve() path = %q, want %q", got.Path, want)
			}
		})
	}
}

func TestResolveRelativeConfigPathAgainstProject(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	want := fakeExecutable(t, filepath.Join(root, "tools"), executableName())

	got, err := toolchain.Resolve(toolchain.ResolveOptions{
		ConfigPath:  filepath.Join("tools", executableName()),
		ProjectRoot: root,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want = canonicalPath(t, want)
	if got.Path != want {
		t.Errorf("Resolve() path = %q, want %q", got.Path, want)
	}
}

func TestResolvePathWithSpaces(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "toolchain with spaces")
	want := fakeExecutable(t, root, executableName())

	got, err := toolchain.Resolve(toolchain.ResolveOptions{ExplicitPath: want})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want = canonicalPath(t, want)
	if got.Path != want {
		t.Errorf("Resolve() path = %q, want %q", got.Path, want)
	}
}

func TestResolveFluxaHomeExecutable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	want := fakeExecutable(t, root, "custom-fluxa")

	got, err := toolchain.Resolve(toolchain.ResolveOptions{FluxaHome: want})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want = canonicalPath(t, want)
	if got.Path != want {
		t.Errorf("Resolve() path = %q, want %q", got.Path, want)
	}
}

func TestResolveErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts toolchain.ResolveOptions
		kind toolchain.ErrorKind
	}{
		{
			name: "not found",
			opts: toolchain.ResolveOptions{PathEnv: t.TempDir()},
			kind: toolchain.ErrorNotFound,
		},
		{
			name: "configured file missing",
			opts: toolchain.ResolveOptions{
				ConfigPath:  "missing-fluxa",
				ProjectRoot: t.TempDir(),
			},
			kind: toolchain.ErrorInvalidExecutable,
		},
		{
			name: "configured path is directory",
			opts: toolchain.ResolveOptions{
				ExplicitPath: t.TempDir(),
			},
			kind: toolchain.ErrorInvalidExecutable,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := toolchain.Resolve(tt.opts)
			if err == nil {
				t.Fatal("Resolve() error = nil, want error")
			}
			var toolchainErr *toolchain.Error
			if !errors.As(err, &toolchainErr) {
				t.Fatalf("Resolve() error type = %T, want *toolchain.Error", err)
			}
			if toolchainErr.Kind != tt.kind {
				t.Errorf("error kind = %q, want %q", toolchainErr.Kind, tt.kind)
			}
		})
	}
}

func TestProbeRecognizesFluxaAndHashesBinary(t *testing.T) {
	t.Setenv("GO_WANT_FLUXA_HELPER", "1")
	t.Setenv("FLUXA_HELPER_OUTPUT", "Fluxa Runtime\n[runtime]\n  gc_cap : 1024\n")
	t.Setenv("FLUXA_HELPER_EXIT", "0")
	t.Setenv("FLUXA_HELPER_DELAY", "0")

	identity, err := toolchain.Probe(context.Background(), os.Args[0], 10*time.Second)
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if identity.Version != "" {
		t.Errorf("Version = %q, want unavailable", identity.Version)
	}
	if identity.Protocol != "runtime-info-v1" {
		t.Errorf("Protocol = %q", identity.Protocol)
	}
	if len(identity.SHA256) != 64 {
		t.Errorf("SHA256 length = %d, want 64", len(identity.SHA256))
	}
}

func TestProbeParsesFutureVersionLine(t *testing.T) {
	t.Setenv("GO_WANT_FLUXA_HELPER", "1")
	t.Setenv("FLUXA_HELPER_OUTPUT", "Fluxa Runtime\nVersion: 0.24.1\n")
	t.Setenv("FLUXA_HELPER_EXIT", "0")
	t.Setenv("FLUXA_HELPER_DELAY", "0")

	identity, err := toolchain.Probe(context.Background(), os.Args[0], 10*time.Second)
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if identity.Version != "0.24.1" {
		t.Errorf("Version = %q, want 0.24.1", identity.Version)
	}
}

func TestProbeRejectsInvalidOutput(t *testing.T) {
	t.Setenv("GO_WANT_FLUXA_HELPER", "1")
	t.Setenv("FLUXA_HELPER_OUTPUT", "not fluxa\n")
	t.Setenv("FLUXA_HELPER_EXIT", "0")
	t.Setenv("FLUXA_HELPER_DELAY", "0")

	_, err := toolchain.Probe(context.Background(), os.Args[0], 10*time.Second)
	assertToolchainErrorKind(t, err, toolchain.ErrorInvalidOutput)
}

func TestProbeReportsProcessFailure(t *testing.T) {
	t.Setenv("GO_WANT_FLUXA_HELPER", "1")
	t.Setenv("FLUXA_HELPER_OUTPUT", "probe failed\n")
	t.Setenv("FLUXA_HELPER_EXIT", "7")
	t.Setenv("FLUXA_HELPER_DELAY", "0")

	_, err := toolchain.Probe(context.Background(), os.Args[0], 10*time.Second)
	assertToolchainErrorKind(t, err, toolchain.ErrorProbe)
}

func TestProbeTimeout(t *testing.T) {
	t.Setenv("GO_WANT_FLUXA_HELPER", "1")
	t.Setenv("FLUXA_HELPER_OUTPUT", "")
	t.Setenv("FLUXA_HELPER_EXIT", "0")
	t.Setenv("FLUXA_HELPER_DELAY", "250ms")

	_, err := toolchain.Probe(context.Background(), os.Args[0], 20*time.Millisecond)
	assertToolchainErrorKind(t, err, toolchain.ErrorTimeout)
}

func TestCheckCompatibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		required string
		actual   string
		wantErr  bool
	}{
		{name: "no requirement"},
		{name: "exact match", required: "0.24.1", actual: "0.24.1"},
		{name: "unknown actual", required: "0.24.1", wantErr: true},
		{name: "mismatch", required: "0.24.1", actual: "0.23.0", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := toolchain.CheckCompatibility(tt.required, toolchain.Identity{Version: tt.actual})
			if (err != nil) != tt.wantErr {
				t.Fatalf("CheckCompatibility() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func assertToolchainErrorKind(t *testing.T, err error, want toolchain.ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want error")
	}
	var toolchainErr *toolchain.Error
	if !errors.As(err, &toolchainErr) {
		t.Fatalf("error type = %T, want *toolchain.Error", err)
	}
	if toolchainErr.Kind != want {
		t.Errorf("error kind = %q, want %q; error = %v", toolchainErr.Kind, want, err)
	}
}

func fakeExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	if filepath.Separator == '\\' && !strings.EqualFold(filepath.Ext(name), ".exe") {
		name += ".exe"
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The fixture intentionally models an executable selected by the locator.
	if err := os.Chmod(path, 0o700); err != nil { // #nosec G302
		t.Fatal(err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return absolute
}

func canonicalPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(resolved)
}

func executableName() string {
	if filepath.Separator == '\\' {
		return "fluxa.exe"
	}
	return "fluxa"
}

func runFluxaHelper() {
	delay, _ := time.ParseDuration(os.Getenv("FLUXA_HELPER_DELAY"))
	if delay > 0 {
		time.Sleep(delay)
	}
	fmt.Print(os.Getenv("FLUXA_HELPER_OUTPUT"))
	exitCode := 0
	if _, err := fmt.Sscanf(os.Getenv("FLUXA_HELPER_EXIT"), "%d", &exitCode); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
	if !containsRuntimeInfo(os.Args) {
		fmt.Fprintln(os.Stderr, "unexpected helper arguments")
		os.Exit(126)
	}
	os.Exit(exitCode)
}

func containsRuntimeInfo(args []string) bool {
	return strings.Contains(strings.Join(args, " "), "runtime info")
}
