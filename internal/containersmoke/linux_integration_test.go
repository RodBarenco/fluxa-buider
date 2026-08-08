package containersmoke

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunLinuxExecutesRealBinaryNetworkIsolated is a real, opt-in,
// end-to-end test: it runs a real script inside the pinned, network-
// isolated Linux container and confirms both that execution genuinely
// happens there (stdout/exit code round-trip correctly) and that
// docs/adr/0028's "must not phone home" requirement actually holds —
// not just that --network none is passed, but that DNS resolution
// really does fail inside the container.
//
// Skipped by default like this project's other Docker integration tests.
// Run explicitly with:
//
//	FLUXA_BUILDER_DOCKER_TESTS=1 go test ./internal/containersmoke/... -run TestRunLinuxExecutesRealBinaryNetworkIsolated -v -timeout 5m
func TestRunLinuxExecutesRealBinaryNetworkIsolated(t *testing.T) {
	if os.Getenv("FLUXA_BUILDER_DOCKER_TESTS") != "1" {
		t.Skip("set FLUXA_BUILDER_DOCKER_TESTS=1 to run the real Docker container (slow, needs network for the image pull)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if !dockerAvailable(ctx) {
		t.Skip("docker is not installed or not reachable")
	}

	directory := t.TempDir()
	executable := filepath.Join(directory, "game")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--fluxa-package-self-test\" ]; then\n" +
		"  if getent hosts example.com >/dev/null 2>&1; then\n" +
		"    echo NETWORK_REACHABLE\n" +
		"  else\n" +
		"    echo NETWORK_BLOCKED\n" +
		"  fi\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 9\n"
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil { // #nosec G302 -- executable test fixture.
		t.Fatal(err)
	}

	result, err := RunLinux(ctx, executable, directory, time.Minute)
	if err != nil {
		t.Fatalf("RunLinux() error = %v, stderr = %q", err, result.Stderr)
	}
	if result.ExitCode != 0 {
		t.Fatalf("RunLinux() exit code = %d, stdout = %q, stderr = %q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "NETWORK_BLOCKED") {
		t.Fatalf("RunLinux() stdout = %q, want NETWORK_BLOCKED — --network none should make DNS resolution fail", result.Stdout)
	}
	if strings.Contains(result.Stdout, "NETWORK_REACHABLE") {
		t.Fatal("RunLinux() reached the network — --network none is not actually isolating this container")
	}
}
