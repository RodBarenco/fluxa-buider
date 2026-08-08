//go:build linux

package containersmoke

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/executor"
)

// TestRunWindowsExecutesRealPEBinary is a real, opt-in, end-to-end test:
// it cross-compiles a minimal real Windows PE executable with MinGW (a
// lightweight fixture, not a full fluxa-lang toolchain build — see
// internal/toolchainbuild's own windows_integration_test.go for "does a
// real fluxa-lang Windows build even compile," a much slower, separate
// question), primes the pinned Wine image for real, and runs the
// resulting .exe through it.
//
// As of this writing this test fails in this project's own development
// environment, and is left in place — not skipped — as exactly the
// regression/fix-confirmation signal a future attempt at the open
// problem needs: wineboot --init (see ensureWineprefixInitialized)
// reliably succeeds, but the generic `wine <program>` launcher this test
// actually exercises reproducibly fails to load its own kernel32.dll for
// a reason that survived extensive isolation testing. See wine.go's own
// doc comments and docs/adr/0028 for the full investigation — what was
// ruled out, what was fixed along the way (a missing /tmp/.X11-unix
// under non-root Xvfb, a wineserver/xvfb-run deadlock, WINEPREFIX
// ownership, stale-prefix reuse across image rebuilds) and what remains
// open. RunLinux's own equivalent test,
// TestRunLinuxExecutesRealBinaryNetworkIsolated, passes reliably — this
// gap is specific to the Wine/WoW64 path.
//
// Skipped by default like this project's other Docker integration tests
// (opt-in only, never part of normal CI). Run explicitly with:
//
//	FLUXA_BUILDER_DOCKER_TESTS=1 go test ./internal/containersmoke/... -run TestRunWindowsExecutesRealPEBinary -v -timeout 10m
func TestRunWindowsExecutesRealPEBinary(t *testing.T) {
	if os.Getenv("FLUXA_BUILDER_DOCKER_TESTS") != "1" {
		t.Skip("set FLUXA_BUILDER_DOCKER_TESTS=1 to run the real Docker/Wine build (slow, needs network)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	if !dockerAvailable(ctx) {
		t.Skip("docker is not installed or not reachable")
	}

	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "game.c")
	source := `#include <stdio.h>
#include <string.h>
int main(int argc, char **argv) {
    if (argc >= 2 && strcmp(argv[1], "--fluxa-package-self-test") == 0) {
        printf("SELF_TEST_OK\n");
        return 0;
    }
    return 9;
}
`
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(directory, "game.exe")
	// internal/executor is this project's one permitted os/exec call
	// site (ADR 0005) — used here too, in test code, for consistency.
	compile, err := executor.Run(ctx, executor.Request{
		Path:    "x86_64-w64-mingw32-gcc",
		Args:    []string{"-O0", "-o", executable, sourcePath},
		Timeout: time.Minute,
	})
	if err != nil {
		t.Fatalf("cross-compiling the test fixture failed: %v, stderr=%q", err, compile.Stderr)
	}

	result, err := RunWindows(ctx, executable, directory, 3*time.Minute)
	if err != nil {
		t.Fatalf("RunWindows() error = %v, stdout=%q stderr=%q", err, result.Stdout, result.Stderr)
	}
	if result.ExitCode != 0 {
		t.Fatalf("RunWindows() exit code = %d, stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "SELF_TEST_OK") {
		t.Fatalf("RunWindows() stdout = %q, want SELF_TEST_OK from the real cross-compiled .exe", result.Stdout)
	}
	t.Logf("real Wine execution succeeded: stdout=%q duration=%s", result.Stdout, result.Duration)
}
