package portable_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/RodBarenco/fluxa-builder/internal/mesafallback"
	"github.com/RodBarenco/fluxa-builder/internal/portable"
)

// TestWindowsMesaFallbackBundling proves portable.Build's wiring of
// WindowsMesaFallbackDir: when set, every name in mesafallback.DLLNames
// is copied into the portable directory alongside a
// "<executable>.local" marker enabling application-local DLL
// redirection (docs/adr/0027-automatic-toolchain-acquisition.md), and
// when unset (the default), none of this happens — existing behavior is
// unaffected. internal/mesafallback's own tests cover the real
// download/verify/extract path; this only proves portable.Build copies
// from an already-populated local directory correctly, using dummy
// content instead of the real ~78 MB of DLLs.
func TestWindowsMesaFallbackBundling(t *testing.T) {
	sourceDir := t.TempDir()
	for _, name := range mesafallback.DLLNames {
		if err := os.WriteFile(filepath.Join(sourceDir, name), []byte("dummy-"+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	fixture := newFixture(t, "Mesa Fallback Game", true)
	fixture.request.TargetOS = "windows"
	fixture.request.Runtime.Metadata.OS = "windows"
	fixture.request.WindowsMesaFallbackDir = sourceDir

	result, err := portable.Build(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("Warnings = %#v, want none", result.Warnings)
	}

	for _, name := range mesafallback.DLLNames {
		data, err := os.ReadFile(filepath.Join(result.Directory, name)) // #nosec G304 -- test-owned result directory.
		if err != nil {
			t.Fatalf("%s missing from portable directory: %v", name, err)
		}
		if string(data) != "dummy-"+name {
			t.Fatalf("%s content = %q, want the copied dummy content", name, string(data))
		}
	}
	localMarker := filepath.Join(result.Directory, filepath.Base(result.Executable)+".local")
	if _, err := os.Stat(localMarker); err != nil {
		t.Fatalf("DLL redirection marker missing: %v", err)
	}
}

// TestWindowsBuildSkipsMesaFallbackWhenUnset proves the default,
// unset-field behavior is unchanged: no Mesa files, no marker, no
// warnings, for a Windows build that never asked for the fallback.
func TestWindowsBuildSkipsMesaFallbackWhenUnset(t *testing.T) {
	fixture := newFixture(t, "No Mesa Game", true)
	fixture.request.TargetOS = "windows"
	fixture.request.Runtime.Metadata.OS = "windows"

	result, err := portable.Build(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("Warnings = %#v, want none", result.Warnings)
	}
	for _, name := range mesafallback.DLLNames {
		if _, err := os.Stat(filepath.Join(result.Directory, name)); err == nil {
			t.Fatalf("%s unexpectedly present when WindowsMesaFallbackDir was never set", name)
		}
	}
}
