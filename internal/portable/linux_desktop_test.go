package portable_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/executor"
	"github.com/RodBarenco/fluxa-builder/internal/portable"
)

// TestInstallDesktopShortcutScript proves the generated script never bakes
// in this build's own absolute path (which would break the moment the
// portable directory is moved — see docs/adr/0026-file-manager-icon-association.md)
// and, when actually run against a fresh $HOME, correctly registers a
// .desktop entry pointing at wherever the directory happens to live at run
// time.
func TestInstallDesktopShortcutScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the generated script and /bin/sh are POSIX-only")
	}

	fixture := newFixture(t, "Instalação Rápida", true)
	launcherPath := filepath.Join(t.TempDir(), "launcher")
	if err := os.WriteFile(launcherPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { // #nosec G306 -- test fixture launcher, not sensitive.
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

	scriptPath := filepath.Join(result.Directory, "install-desktop-shortcut.sh")
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("install-desktop-shortcut.sh missing: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("install-desktop-shortcut.sh mode = %v, want executable", info.Mode())
	}

	script, err := os.ReadFile(scriptPath) // #nosec G304 -- test-owned result directory.
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), result.Directory) {
		t.Fatalf("script embeds this build's own absolute path %q; it must resolve its location at run time instead", result.Directory)
	}
	if !strings.Contains(string(script), `dirname -- "$0"`) {
		t.Fatalf("script = %q, want it to resolve its own location via $0", string(script))
	}

	// Actually run it against a fresh, isolated $HOME and confirm it
	// produces a correctly-pathed .desktop entry — this is the real proof,
	// not just a source-text check.
	fakeHome := t.TempDir()
	execution, err := executor.Run(context.Background(), executor.Request{
		Path: scriptPath, Env: []string{"HOME=" + fakeHome}, Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("run install-desktop-shortcut.sh: %v; stdout=%q stderr=%q", err, execution.Stdout, execution.Stderr)
	}

	entries, err := os.ReadDir(filepath.Join(fakeHome, ".local", "share", "applications"))
	if err != nil {
		t.Fatalf("read applications directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("applications directory has %d entries, want 1", len(entries))
	}
	desktopPath := filepath.Join(fakeHome, ".local", "share", "applications", entries[0].Name())
	desktop, err := os.ReadFile(desktopPath) // #nosec G304 -- test-owned fake $HOME.
	if err != nil {
		t.Fatal(err)
	}
	wantExec := "Exec=" + filepath.Join(result.Directory, filepath.Base(result.Executable))
	wantIcon := "Icon=" + filepath.Join(result.Directory, "instalação-rápida.png")
	for _, want := range []string{
		"[Desktop Entry]", "Type=Application", "Name=Instalação Rápida",
		wantExec, wantIcon, "Terminal=true", "Categories=Utility;",
	} {
		if !strings.Contains(string(desktop), want) {
			t.Errorf(".desktop = %q, want it to contain %q", string(desktop), want)
		}
	}
}
