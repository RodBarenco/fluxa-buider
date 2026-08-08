//go:build linux

package toolchainbuild

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/toolchain"
)

// alwaysConfirm auto-approves every step, standing in for the wizard's
// real askYesNo during automated testing.
type alwaysConfirm struct{}

func (alwaysConfirm) Confirm(string) (bool, error) { return true, nil }

// TestAcquireLinuxBuildsRealFluxaBinary is a real, opt-in, end-to-end
// integration test: it builds the actual Docker image, clones the real
// fluxa-lang repository, compiles it inside the container, and proves the
// resulting binary is a genuine, working Fluxa toolchain — not just "a
// file exists" — by running toolchain.Probe against it (the same offline
// `runtime info` check internal/app/init.go's manual guide already uses).
// It is skipped by default: a full build compiles raylib and the entire
// fluxa-lang C codebase from scratch on first run, which can take several
// minutes and needs network access, both inappropriate for the normal
// `go test ./...` gate. Run explicitly with:
//
//	FLUXA_BUILDER_DOCKER_TESTS=1 go test ./internal/toolchainbuild/... -run TestAcquireLinux -v -timeout 30m
func TestAcquireLinuxBuildsRealFluxaBinary(t *testing.T) {
	if os.Getenv("FLUXA_BUILDER_DOCKER_TESTS") != "1" {
		t.Skip("set FLUXA_BUILDER_DOCKER_TESTS=1 to run the real Docker build (slow, needs network)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	if !dockerAvailable(ctx) {
		t.Skip("docker is not installed or not reachable")
	}

	const essentialOnlyFluxaLibs = `[libs.build]
std.graph   = true
std.image   = true
std.strings = true
std.sqlite  = true
std.sound   = true
std.crypto  = true
std.json2   = true
std.fs      = true
std.httpc   = true
std.https   = true
`
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fluxa.libs"), []byte(essentialOnlyFluxaLibs), 0o600); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(root, "out")
	cacheRoot := filepath.Join(root, "cache")

	result, err := Acquire(ctx, Request{
		ProjectRoot: root,
		TargetOS:    "linux",
		OutputDir:   outputDir,
		CacheRoot:   cacheRoot,
	}, alwaysConfirm{})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	info, err := os.Stat(result.ToolchainPath)
	if err != nil {
		t.Fatalf("built binary missing: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("built binary mode = %v, want executable", info.Mode())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("could not read Unix file ownership")
	}
	if wantUID := uint32(os.Getuid()); stat.Uid != wantUID { //nolint:gosec // os.Getuid() is non-negative on Linux.
		t.Fatalf("built binary owner UID = %d, want %d (the invoking user, not root)", stat.Uid, wantUID)
	}

	identity, err := toolchain.Probe(ctx, result.ToolchainPath, 10*time.Second)
	if err != nil {
		t.Fatalf("toolchain.Probe() on the built binary error = %v", err)
	}
	if identity.SHA256 == "" {
		t.Fatal("Probe() returned an empty SHA-256 for a binary that should exist")
	}
	t.Logf("built a real Fluxa toolchain: version=%q sha256=%s", identity.Version, identity.SHA256)
}
