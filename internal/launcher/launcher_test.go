package launcher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/RodBarenco/fluxa-builder/internal/manifest"
	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
	"github.com/RodBarenco/fluxa-builder/internal/portable"
)

func TestInvocationDetection(t *testing.T) {
	t.Parallel()

	for _, executable := range []string{"/opt/bin/fluxa-builder", `C:\tools\fluxa-builder.exe`} {
		if IsInvocation(executable) {
			t.Fatalf("IsInvocation(%q) = true, want false", executable)
		}
	}
	for _, executable := range []string{"/opt/games/starfight", `C:\Games\Starfight.exe`} {
		if !IsInvocation(executable) {
			t.Fatalf("IsInvocation(%q) = false, want true", executable)
		}
	}
}

func TestLayoutForMacOSBundle(t *testing.T) {
	t.Parallel()

	executable := filepath.Join(
		string(filepath.Separator), "Applications", "Starfight.app",
		"Contents", "MacOS", "starfight",
	)
	layout := layoutFor(executable)
	if want := filepath.Join(string(filepath.Separator), "Applications", "Starfight.app", "Contents", "Resources"); layout.packageDirectory != want {
		t.Errorf("package directory = %q, want %q", layout.packageDirectory, want)
	}
	if want := filepath.Join(string(filepath.Separator), "Applications", "Starfight.app", "Contents", "MacOS"); layout.runtimeDirectory != want {
		t.Errorf("runtime directory = %q, want %q", layout.runtimeDirectory, want)
	}
	if want := filepath.Join(string(filepath.Separator), "Applications"); layout.distributionDirectory != want {
		t.Errorf("distribution directory = %q, want %q", layout.distributionDirectory, want)
	}
}

// TestRunResolvesRelativeExecutablePath is a regression test for a
// real bug this project's own end-to-end verification caught: os.Args[0] is
// frequently relative (a user launching "./app" from its own directory).
// filepath.Join(filepath.Dir("./app"), ".fluxa-runtime") produces
// ".fluxa-runtime" — a path with no separator, which os/exec treats as a
// bare command name to search $PATH for, not a path relative to the
// current directory, and fails with "executable file not found in $PATH".
// This never surfaced in any automated pipeline test because none of them
// actually launched a produced application the way a real user does
// (relative to its own directory); they only ever exercised
// --fluxa-package-self-test, which RunInstalled answers before ever
// touching RuntimePath.
func TestRunResolvesRelativeExecutablePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell runtime fixture is POSIX-only")
	}

	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	runtimePath := filepath.Join(root, RuntimeName)
	if err := os.WriteFile(runtimePath, []byte("#!/bin/sh\necho relay-ran\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- executable test fixture.
		t.Fatal(err)
	}

	sourcePath := filepath.Join(root, "main.flx.src")
	if err := os.WriteFile(sourcePath, []byte("main"), 0o600); err != nil {
		t.Fatal(err)
	}
	data := []byte("main")
	sum := sha256.Sum256(data)
	value := manifest.Manifest{
		FormatVersion: manifest.CurrentFormatVersion,
		Project: manifest.Project{
			Name: "Relative Path Test", ID: "com.example.relative-path-test",
			Version: "1.0.0", Entry: "main.flx", Type: "desktop",
		},
		Toolchain: manifest.Toolchain{
			Protocol: "runtime-info-v1", FluxaSHA256: strings.Repeat("a", 64), LibrariesSHA256: strings.Repeat("b", 64),
		},
		Target: manifest.Target{OS: runtime.GOOS, Arch: runtime.GOARCH},
		Build: manifest.Build{
			Preflight: "not_run", ProgramFormat: "fluxa-source",
			Debug: true, SourceExposed: true,
		},
		Files: []manifest.File{{
			Path: "program/source/main.flx", LogicalPath: "main.flx", Kind: "program",
			Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]),
		}},
	}
	packagePath := filepath.Join(root, "app.flxpkg")
	if _, err := flxpkg.Write(context.Background(), flxpkg.Request{
		OutputPath: packagePath, Manifest: value,
		Sources: map[string]string{"program/source/main.flx": sourcePath}, Compress: true,
	}); err != nil {
		t.Fatal(err)
	}

	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run("./app", nil, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run() code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "relay-ran") {
		t.Errorf("stdout = %q, want evidence the runtime fixture actually ran", stdout.String())
	}
}

// TestRunRegistersDesktopEntry proves RunInstalled itself
// (re)registers a Linux desktop entry on every launch, not just when a user
// manually runs install-desktop-shortcut.sh. Real end-user testing of the
// script-only design found that a non-technical player never discovers or
// runs that script, so the launcher — which the game's own executable
// literally is, per IsInvocation — now does the equivalent itself.
// See docs/adr/0026-file-manager-icon-association.md.
func TestRunRegistersDesktopEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell runtime fixture and XDG paths are POSIX-only")
	}

	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	fakeHome := filepath.Join(root, "home")
	t.Setenv("HOME", fakeHome)
	runtimePath := filepath.Join(root, RuntimeName)
	if err := os.WriteFile(runtimePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- executable test fixture.
		t.Fatal(err)
	}

	sourcePath := filepath.Join(root, "main.flx.src")
	if err := os.WriteFile(sourcePath, []byte("main"), 0o600); err != nil {
		t.Fatal(err)
	}
	data := []byte("main")
	sum := sha256.Sum256(data)
	value := manifest.Manifest{
		FormatVersion: manifest.CurrentFormatVersion,
		Project: manifest.Project{
			Name: "Desktop Entry Test", ID: "com.example.desktop-entry-test",
			Version: "1.0.0", Entry: "main.flx", Type: "desktop",
		},
		Toolchain: manifest.Toolchain{
			Protocol: "runtime-info-v1", FluxaSHA256: strings.Repeat("a", 64), LibrariesSHA256: strings.Repeat("b", 64),
		},
		Target: manifest.Target{OS: runtime.GOOS, Arch: runtime.GOARCH},
		Build: manifest.Build{
			Preflight: "not_run", ProgramFormat: "fluxa-source",
			Debug: true, SourceExposed: true,
		},
		Files: []manifest.File{{
			Path: "program/source/main.flx", LogicalPath: "main.flx", Kind: "program",
			Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]),
		}},
	}
	packagePath := filepath.Join(root, "app.flxpkg")
	if _, err := flxpkg.Write(context.Background(), flxpkg.Request{
		OutputPath: packagePath, Manifest: value,
		Sources: map[string]string{"program/source/main.flx": sourcePath}, Compress: true,
	}); err != nil {
		t.Fatal(err)
	}

	// ensureDesktopEntry never opens the icon file itself — it only needs
	// LinuxInfo.Icon to be a non-empty filename to reference in the
	// generated Icon= line — so an arbitrary placeholder is enough here.
	if err := os.WriteFile(filepath.Join(root, "app.png"), []byte("not a real png"), 0o600); err != nil {
		t.Fatal(err)
	}

	linuxInfo := portable.LinuxInfo{
		FormatVersion: 1, ProductName: "Desktop Entry Test", ProjectID: "com.example.desktop-entry-test",
		Version: "1.0.0", Architecture: runtime.GOARCH, Executable: "app", Package: "app.flxpkg",
		RuntimeHash: "irrelevant", PackageHash: "irrelevant", Icon: "app.png", IconHash: "irrelevant",
		DataPolicy: "xdg", LibcPolicy: "runtime-defined", Terminal: true,
	}
	linuxInfoData, err := json.Marshal(linuxInfo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "linux-runtime.json"), linuxInfoData, 0o600); err != nil {
		t.Fatal(err)
	}

	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run("./app", nil, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run() code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	desktopPath := filepath.Join(fakeHome, ".local", "share", "applications", "com.example.desktop-entry-test.desktop")
	desktop, err := os.ReadFile(desktopPath) // #nosec G304 -- test-owned fake $HOME.
	if err != nil {
		t.Fatalf("read registered desktop entry: %v", err)
	}
	executable, err := filepath.Abs("app")
	if err != nil {
		t.Fatal(err)
	}
	want := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=Desktop Entry Test\n" +
		"Exec=" + executable + "\n" +
		"Icon=" + filepath.Join(root, "app.png") + "\n" +
		"Terminal=true\n" +
		"Categories=Utility;\n"
	if string(desktop) != want {
		t.Fatalf("desktop entry = %q, want %q", string(desktop), want)
	}
}
