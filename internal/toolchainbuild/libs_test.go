package toolchainbuild

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// realisticFluxaLibs mirrors the real fluxa-lang fluxa.libs format
// (confirmed against a real checkout of github.com/RodBarenco/fluxa-lang):
// a [libs.build] table whose unquoted dotted keys (std.math = true) decode
// as a nested "std" table, not a flat "std.math" key.
const realisticFluxaLibs = `# fluxa.libs — Build-time library configuration
[libs.build]
std.math      = true
std.csv       = true
std.graph     = true
std.image     = true
std.sound     = true
std.sqlite    = true
std.crypto    = true
std.json2     = true
std.fs        = true
std.httpc     = true
std.https     = true
std.strings   = true
std.pg        = false
std.mqtt      = true
`

func TestReadEnabledLibsParsesRealFluxaLibsFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fluxa.libs")
	if err := os.WriteFile(path, []byte(realisticFluxaLibs), 0o600); err != nil {
		t.Fatal(err)
	}

	enabled, err := readEnabledLibs(path)
	if err != nil {
		t.Fatalf("readEnabledLibs() error = %v", err)
	}
	sort.Strings(enabled)
	want := []string{"crypto", "csv", "fs", "graph", "httpc", "https", "image", "json2", "math", "mqtt", "sound", "sqlite", "strings"}
	if len(enabled) != len(want) {
		t.Fatalf("enabled = %v, want %v", enabled, want)
	}
	for i := range want {
		if enabled[i] != want[i] {
			t.Fatalf("enabled = %v, want %v", enabled, want)
		}
	}
}

func TestReadEnabledLibsMissingFileIsNotAnError(t *testing.T) {
	enabled, err := readEnabledLibs(filepath.Join(t.TempDir(), "does-not-exist.libs"))
	if err != nil {
		t.Fatalf("readEnabledLibs() error = %v, want nil for a missing file", err)
	}
	if len(enabled) != 0 {
		t.Fatalf("enabled = %v, want empty", enabled)
	}
}

func TestUnsupportedForWindowsDetectsLibraryOutsideEssentialProfile(t *testing.T) {
	unsupported := unsupportedForWindows([]string{"graph", "sqlite", "mqtt", "pg"})
	sort.Strings(unsupported)
	want := []string{"mqtt", "pg"}
	if len(unsupported) != len(want) || unsupported[0] != want[0] || unsupported[1] != want[1] {
		t.Fatalf("unsupportedForWindows() = %v, want %v", unsupported, want)
	}
}

func TestUnsupportedForWindowsAcceptsEssentialProfile(t *testing.T) {
	enabled := []string{
		"graph", "image", "strings", "sqlite", "sound", "crypto",
		"json2", "fs", "httpc", "https", "math", "csv", "json", "pid", "libdsp",
	}
	if unsupported := unsupportedForWindows(enabled); len(unsupported) != 0 {
		t.Fatalf("unsupportedForWindows() = %v, want none", unsupported)
	}
}
