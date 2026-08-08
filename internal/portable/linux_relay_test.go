//go:build linux

package portable_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/executor"
	"github.com/RodBarenco/fluxa-builder/internal/portable"
	"github.com/RodBarenco/fluxa-builder/internal/runtimeprotocol"
	"github.com/RodBarenco/fluxa-builder/internal/wrapper"
)

// TestLinuxRuntimeRelayAssemblesAndExecutes proves the two-file Linux
// runtime portable.Build assembles when LauncherPath is set (the real
// fluxa-builder build path, per internal/app's runBuild) — the embedded
// relay as .fluxa-runtime, the verified interpreter beside it as
// .fluxa-runtime.interpreter — actually works end to end: it invokes the
// assembled .fluxa-runtime with the exact private protocol
// internal/runner.go sends a real installed application, and confirms the
// relay translates it into the interpreter's `run <entry> -proj .` call.
// This is the coverage the pre-existing "official" Linux pipeline test
// never provided: that test never sets LauncherPath, so it always exercised
// the single-binary branch instead, and never invoked the private protocol
// at all.
func TestLinuxRuntimeRelayAssemblesAndExecutes(t *testing.T) {
	interpreterScript := "#!/bin/sh\n" +
		"echo \"argv:$*\"\n" +
		"echo \"cwd:$(pwd)\"\n" +
		"exit ${FAKE_INTERPRETER_EXIT_CODE:-0}\n"
	fixture := newFixtureWithScript(t, "Relay Test", func(string) string { return interpreterScript })

	launcherPath := filepath.Join(t.TempDir(), "launcher")
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- test fixture launcher needs the execute bit; not external input.
		t.Fatal(err)
	}
	fixture.request.LauncherPath = launcherPath
	if err := os.MkdirAll(fixture.request.OutputRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := portable.Build(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	relayPath := filepath.Join(result.Directory, ".fluxa-runtime")
	interpreterPath := filepath.Join(result.Directory, ".fluxa-runtime.interpreter")
	relayData, err := os.ReadFile(relayPath) // #nosec G304 -- test-owned result directory.
	if err != nil {
		t.Fatalf("read assembled relay: %v", err)
	}
	if string(relayData) != string(wrapper.LinuxAMD64) {
		t.Fatal(".fluxa-runtime does not match the embedded wrapper binary")
	}
	if _, err := os.Stat(interpreterPath); err != nil {
		t.Fatalf(".fluxa-runtime.interpreter missing: %v", err)
	}

	projectDir := t.TempDir()
	execution, err := executor.Run(context.Background(), executor.Request{
		Path:    relayPath,
		Args:    []string{runtimeprotocol.Command, "main.flx", projectDir},
		Dir:     projectDir,
		Env:     append(os.Environ(), runtimeprotocol.AuthEnvVar+"="+runtimeprotocol.AuthValue),
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("relay exec error = %v; stdout=%q stderr=%q", err, execution.Stdout, execution.Stderr)
	}
	if execution.ExitCode != 0 {
		t.Fatalf("relay exit code = %d, want 0; stdout=%q stderr=%q", execution.ExitCode, execution.Stdout, execution.Stderr)
	}
	if !strings.Contains(execution.Stdout, "argv:run main.flx -proj .") {
		t.Errorf("stdout = %q, want relayed run/-proj argv", execution.Stdout)
	}
	if !strings.Contains(execution.Stdout, "cwd:"+projectDir) {
		t.Errorf("stdout = %q, want cwd %q", execution.Stdout, projectDir)
	}
}

// TestLinuxRuntimeRelaySurvivesTarGZRoundTrip proves the assembled relay,
// interpreter, and install script all keep their executable bit through a
// real portable.Archive tar.gz round trip, and that the relay still runs
// correctly from the extracted location. archiveEntries used to grant the
// execute bit only to portable.Executable (the launcher) — real testing
// against a real end-user extraction of a real game's .tar.gz found that
// .fluxa-runtime, .fluxa-runtime.interpreter, and
// install-desktop-shortcut.sh all came out non-executable, silently
// breaking every packaged Linux application the moment a user extracted it,
// with no automated test catching it because none of them archived a
// LauncherPath-based build. See docs/adr/0026-file-manager-icon-association.md.
func TestLinuxRuntimeRelaySurvivesTarGZRoundTrip(t *testing.T) {
	interpreterScript := "#!/bin/sh\n" +
		"echo \"argv:$*\"\n" +
		"echo \"cwd:$(pwd)\"\n" +
		"exit 0\n"
	fixture := newFixtureWithScript(t, "Round Trip Test", func(string) string { return interpreterScript })

	launcherPath := filepath.Join(t.TempDir(), "launcher")
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- test fixture launcher needs the execute bit; not external input.
		t.Fatal(err)
	}
	fixture.request.LauncherPath = launcherPath
	iconPath := filepath.Join(t.TempDir(), "icone.png")
	writeLinuxPNG(t, iconPath)
	fixture.request.LinuxIcon = iconPath
	if err := os.MkdirAll(fixture.request.OutputRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := portable.Build(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	archive, err := portable.Archive(context.Background(), result, "linux")
	if err != nil {
		t.Fatalf("Archive() error = %v", err)
	}

	extracted := t.TempDir()
	extractTarGZ(t, archive.Path, extracted)
	extractedDir := filepath.Join(extracted, result.Name)

	for _, name := range []string{".fluxa-runtime", ".fluxa-runtime.interpreter", "install-desktop-shortcut.sh"} {
		info, err := os.Stat(filepath.Join(extractedDir, name))
		if err != nil {
			t.Fatalf("extracted %s missing: %v", name, err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("extracted %s mode = %v, want executable", name, info.Mode())
		}
	}

	projectDir := t.TempDir()
	execution, err := executor.Run(context.Background(), executor.Request{
		Path:    filepath.Join(extractedDir, ".fluxa-runtime"),
		Args:    []string{runtimeprotocol.Command, "main.flx", projectDir},
		Dir:     projectDir,
		Env:     append(os.Environ(), runtimeprotocol.AuthEnvVar+"="+runtimeprotocol.AuthValue),
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("extracted relay exec error = %v; stdout=%q stderr=%q", err, execution.Stdout, execution.Stderr)
	}
	if execution.ExitCode != 0 {
		t.Fatalf("extracted relay exit code = %d, want 0; stdout=%q stderr=%q", execution.ExitCode, execution.Stdout, execution.Stderr)
	}
	if !strings.Contains(execution.Stdout, "argv:run main.flx -proj .") {
		t.Errorf("stdout = %q, want relayed run/-proj argv", execution.Stdout)
	}
}

// TestLinuxRuntimeRelayRefusesDirectExecution proves the assembled
// .fluxa-runtime keeps refusing arbitrary use even after being copied into
// a real portable directory, not just when built standalone.
func TestLinuxRuntimeRelayRefusesDirectExecution(t *testing.T) {
	fixture := newFixtureWithScript(t, "Relay Refusal Test", func(string) string {
		return "#!/bin/sh\necho should-not-run\nexit 0\n"
	})
	launcherPath := filepath.Join(t.TempDir(), "launcher")
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- test fixture launcher needs the execute bit; not external input.
		t.Fatal(err)
	}
	fixture.request.LauncherPath = launcherPath
	if err := os.MkdirAll(fixture.request.OutputRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := portable.Build(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	relayPath := filepath.Join(result.Directory, ".fluxa-runtime")
	execution, err := executor.Run(context.Background(), executor.Request{
		Path: relayPath, Args: []string{"run", "arbitrary.flx"}, Timeout: 10 * time.Second,
	})
	if execution.ExitCode != 126 {
		t.Fatalf("relay exit code = %d, want 126; err=%v stdout=%q stderr=%q", execution.ExitCode, err, execution.Stdout, execution.Stderr)
	}
	if strings.Contains(execution.Stdout, "should-not-run") {
		t.Error("interpreter was invoked despite an unauthorized direct call")
	}
}
