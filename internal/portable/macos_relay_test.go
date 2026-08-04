package portable_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/RodBarenco/fluxa-builder/internal/portable"
	"github.com/RodBarenco/fluxa-builder/internal/wrapper"
)

// TestMacOSRuntimeRelayAssembly proves portable.Build assembles the same
// two-file "adapted runtime" layout for macOS as it does for Linux (embedded
// relay as .fluxa-runtime, verified interpreter beside it as
// .fluxa-runtime.interpreter — see
// docs/adr/0025-linux-adapted-runtime-wrapper.md), and that it selects the
// relay binary matching the requested architecture. This test deliberately
// has no build tag: validateRuntime's real Mach-O parsing only runs when
// GOOS is actually darwin, so a plain script fixture with FormatVersion left
// at its zero value exercises the bundle-assembly logic on any host,
// including this one. What it cannot prove — because only real macOS can
// run a Mach-O binary — is that the relay actually executes and relays
// correctly there; that is only established by the wrapper's own
// cross-compiled-and-hash-verified drift test
// (internal/wrapper/wrapper_test.go) plus the pure-Go relay logic already
// covered without any OS-specific behavior by
// cmd/fluxa-runtime-wrapper/main_test.go.
func TestMacOSRuntimeRelayAssembly(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			t.Parallel()

			fixture := newFixtureWithScript(t, "macOS Relay Test", func(string) string {
				return "#!/bin/sh\nexit 0\n"
			})
			fixture.request.TargetOS = "macos"
			fixture.request.TargetArch = arch
			fixture.request.Runtime.Metadata.OS = "macos"
			fixture.request.Runtime.Metadata.Arch = arch
			fixture.request.BundleID = "com.example.macos-relay-test"

			launcherPath := filepath.Join(t.TempDir(), "launcher")
			if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- test fixture launcher, not sensitive.
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

			macOSDir := filepath.Join(result.Directory, "Contents", "MacOS")
			relayPath := filepath.Join(macOSDir, ".fluxa-runtime")
			interpreterPath := filepath.Join(macOSDir, ".fluxa-runtime.interpreter")

			relayData, err := os.ReadFile(relayPath) // #nosec G304 -- test-owned result directory.
			if err != nil {
				t.Fatalf("read assembled relay: %v", err)
			}
			want := wrapper.DarwinAMD64
			if arch == "arm64" {
				want = wrapper.DarwinARM64
			}
			if string(relayData) != string(want) {
				t.Fatalf(".fluxa-runtime does not match the embedded darwin/%s wrapper binary", arch)
			}
			if _, err := os.Stat(interpreterPath); err != nil {
				t.Fatalf(".fluxa-runtime.interpreter missing: %v", err)
			}
		})
	}
}

// TestMacOSWrapperBinaryRejectsUnsupportedArch proves an unknown target
// architecture fails closed instead of silently shipping the wrong relay.
func TestMacOSWrapperBinaryRejectsUnsupportedArch(t *testing.T) {
	t.Parallel()

	fixture := newFixtureWithScript(t, "macOS Relay Arch Test", func(string) string {
		return "#!/bin/sh\nexit 0\n"
	})
	fixture.request.TargetOS = "macos"
	fixture.request.TargetArch = "riscv64"
	fixture.request.Runtime.Metadata.OS = "macos"
	fixture.request.Runtime.Metadata.Arch = "riscv64"
	fixture.request.BundleID = "com.example.macos-relay-arch-test"

	launcherPath := filepath.Join(t.TempDir(), "launcher")
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- test fixture launcher, not sensitive.
		t.Fatal(err)
	}
	fixture.request.LauncherPath = launcherPath
	if err := os.MkdirAll(fixture.request.OutputRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := portable.Build(context.Background(), fixture.request); err == nil {
		t.Fatal("Build() error = nil, want failure for an unsupported macOS architecture")
	}
}
