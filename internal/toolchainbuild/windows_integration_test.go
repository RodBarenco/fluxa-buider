//go:build linux

package toolchainbuild

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/toolchain"
	windowspkg "github.com/RodBarenco/fluxa-builder/internal/windows"
)

// TestAcquireWindowsCrossCompilesRealBinaries is a real, opt-in,
// end-to-end integration test: it builds the Fedora/MinGW cross-compile
// image, clones the real fluxa-lang repository, cross-compiles both
// Windows static targets inside the container — entirely from this Linux
// host, no Windows machine involved — and structurally validates both
// resulting PE binaries. It does not (and cannot, without real Windows or
// Wine) prove the binaries actually run; see
// docs/adr/0027-automatic-toolchain-acquisition.md for that documented
// scope boundary.
//
// Skipped by default: a full build compiles raylib and the entire
// fluxa-lang C codebase twice (once per static target) from scratch on
// first run. Run explicitly with:
//
//	FLUXA_BUILDER_DOCKER_TESTS=1 go test ./internal/toolchainbuild/... -run TestAcquireWindowsCrossCompilesRealBinaries -v -timeout 30m
func TestAcquireWindowsCrossCompilesRealBinaries(t *testing.T) {
	if os.Getenv("FLUXA_BUILDER_DOCKER_TESTS") != "1" {
		t.Skip("set FLUXA_BUILDER_DOCKER_TESTS=1 to run the real Docker build (slow, needs network)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 28*time.Minute)
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
		TargetOS:    "windows",
		OutputDir:   outputDir,
		CacheRoot:   cacheRoot,
	}, alwaysConfirm{})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	if result.ToolchainPath == result.RuntimePath {
		t.Fatalf("toolchain and runtime paths must differ on Windows: both are %q", result.ToolchainPath)
	}
	// The host toolchain is the half that makes a Windows target usable at
	// all from here: `build` probes a host-native compiler and records its
	// identity, so without one nothing this test built could ever be
	// selected later. Probing it is the assertion — a PE would fail with
	// "exec format error", which is precisely the old, broken behavior.
	if result.HostToolchainPath == "" {
		t.Fatal("HostToolchainPath is empty: a Linux host cannot execute either cross-compiled PE, so a Windows build would have no toolchain identity to match")
	}
	identity, err := toolchain.Probe(ctx, result.HostToolchainPath, 30*time.Second)
	if err != nil {
		t.Fatalf("Probe(%s) error = %v", result.HostToolchainPath, err)
	}
	if identity.SHA256 == "" {
		t.Errorf("host toolchain identity has no SHA256: %+v", identity)
	}
	for _, path := range []string{result.ToolchainPath, result.RuntimePath} {
		if err := windowspkg.ValidatePEAMD64(path); err != nil {
			t.Fatalf("ValidatePEAMD64(%s) error = %v", path, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() == 0 {
			t.Fatalf("%s is empty", path)
		}
	}
	t.Logf("cross-compiled real Windows PE binaries: toolchain=%s runtime=%s", result.ToolchainPath, result.RuntimePath)
}
