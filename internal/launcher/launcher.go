// Package launcher is the application launcher Fluxa Builder assembles
// into every portable application: the executable an end user actually
// double-clicks, which finds the FLXPKG beside it, verifies it, and hands
// it to the packaged runtime.
//
// It lives apart from internal/app so it can be built as its own small
// program (cmd/fluxa-launcher) and cross-compiled per target. A portable
// application built for Windows on a Linux machine needs a real PE
// launcher, which the running Linux Fluxa Builder executable can never be
// — see docs/adr/0029-cross-target-application-launcher.md. internal/app
// still re-exports Run/IsInvocation so a Fluxa Builder binary renamed the
// old way keeps working for already-distributed applications.
package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	flxpkg "github.com/RodBarenco/fluxa-builder/internal/package"
	"github.com/RodBarenco/fluxa-builder/internal/portable"
	"github.com/RodBarenco/fluxa-builder/internal/runner"
)

// RuntimeName is the private runtime copy an assembled application keeps
// beside itself, hidden by the leading dot.
const RuntimeName = ".fluxa-runtime"

// BuilderName is the Fluxa Builder executable's own name, the one name
// that does *not* mean "this is an application launcher" when a Builder
// binary is invoked directly.
const BuilderName = "fluxa-builder"

// IsInvocation reports whether a Fluxa Builder executable was renamed to
// become an application launcher. cmd/fluxa-launcher never needs this —
// it is only ever an application launcher — but a Fluxa Builder binary
// distributed as a launcher before cmd/fluxa-launcher existed still does.
func IsInvocation(executable string) bool {
	portablePath := strings.ReplaceAll(executable, `\`, "/")
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(portablePath)), ".exe")
	return name != BuilderName
}

// Run executes a portable application assembled by Fluxa Builder.
func Run(executable string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// executable is typically os.Args[0], which is frequently relative
	// (e.g. "./app" when launched from the same directory). A relative
	// RuntimePath with no path separator (".fluxa-runtime") is not treated
	// as cwd-relative by os/exec: it is looked up on $PATH like a bare
	// command name and fails closed with "executable file not found in
	// $PATH". Resolving to an absolute path here keeps every derived path
	// unambiguous regardless of how the application was invoked.
	if absolute, err := filepath.Abs(executable); err == nil {
		executable = absolute
	}
	layout := layoutFor(executable)
	packagePath, err := findPackage(layout.packageDirectory)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Fluxa application error: %v\n", err)
		return 1
	}
	info, err := flxpkg.Verify(packagePath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Fluxa application integrity error: %v\n", err)
		return 1
	}
	if len(args) == 1 && args[0] == "--fluxa-package-self-test" {
		_ = json.NewEncoder(stdout).Encode(map[string]any{
			"protocol":       "fluxa-package-self-test-v1",
			"package_sha256": info.SHA256,
			"package_opened": true,
			"vm_compatible":  info.Manifest.Build.ProgramFormat == "fluxa-source",
			"ui_opened":      false,
		})
		return 0
	}
	if len(args) != 0 {
		_, _ = fmt.Fprintln(stderr, "Fluxa application does not accept command-line arguments")
		return 2
	}
	if runtime.GOOS == "linux" {
		ensureDesktopEntry(executable, layout)
	}
	runtimeName := RuntimeName
	if runtime.GOOS == "windows" {
		runtimeName += ".exe"
	}
	if err := runner.Run(context.Background(), runner.Request{
		PackagePath:     packagePath,
		RuntimePath:     filepath.Join(layout.runtimeDirectory, runtimeName),
		DistributionDir: layout.distributionDirectory,
		PackagedRuntime: true,
		Stdin:           stdin,
		Stdout:          stdout,
		Stderr:          stderr,
	}); err != nil {
		_, _ = fmt.Fprintf(stderr, "Fluxa application error: %v\n", err)
		return 1
	}
	return 0
}

type appLayout struct {
	packageDirectory      string
	runtimeDirectory      string
	distributionDirectory string
}

func layoutFor(executable string) appLayout {
	executableDirectory := filepath.Dir(executable)
	layout := appLayout{
		packageDirectory:      executableDirectory,
		runtimeDirectory:      executableDirectory,
		distributionDirectory: executableDirectory,
	}
	// A macOS application launcher lives at:
	// <name>.app/Contents/MacOS/<name>. The immutable package belongs in
	// Resources, while user-visible exports should be written beside the app
	// (or fall back to Documents when that location is not writable).
	if filepath.Base(executableDirectory) == "MacOS" {
		contents := filepath.Dir(executableDirectory)
		if filepath.Base(contents) == "Contents" &&
			strings.HasSuffix(strings.ToLower(filepath.Base(filepath.Dir(contents))), ".app") {
			layout.packageDirectory = filepath.Join(contents, "Resources")
			layout.distributionDirectory = filepath.Dir(filepath.Dir(contents))
		}
	}
	return layout
}

func findPackage(directory string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(directory, "*.flxpkg"))
	if err != nil {
		return "", err
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("expected exactly one FLXPKG beside the application, found %d", len(matches))
	}
	info, err := os.Lstat(matches[0])
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("FLXPKG is missing or unsafe")
	}
	return matches[0], nil
}

// ensureDesktopEntry silently (re)registers this Linux launcher's desktop
// entry on every launch. A portable directory has no install step of its
// own for a file manager to hook into, and real end-user testing showed
// that expecting a non-technical player to discover and manually run
// install-desktop-shortcut.sh does not work in practice — so the launcher
// now does the equivalent itself, every launch. Re-running keeps the entry
// correct if the directory was later moved, the same "safe to re-run"
// contract install-desktop-shortcut.sh already has. Any failure here is
// silently ignored: a missing or stale desktop entry must never stop the
// application itself from starting, and this runs ahead of an audience
// (non-technical players, frequently with terminal = false and so no
// visible place to report a warning) different from the build-time
// warnings the Windows icon-embedding path prints for a developer.
// See docs/adr/0026-file-manager-icon-association.md.
func ensureDesktopEntry(executable string, layout appLayout) {
	infoPath := filepath.Join(layout.distributionDirectory, "linux-runtime.json")
	info, err := portable.ReadLinuxInfo(infoPath)
	if err != nil {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	desktopName := info.ProjectID
	if desktopName == "" || strings.ContainsAny(desktopName, "/\\") {
		desktopName = filepath.Base(executable)
	}
	iconLine := ""
	if info.Icon != "" {
		iconLine = "Icon=" + filepath.Join(layout.distributionDirectory, info.Icon) + "\n"
	}
	content := fmt.Sprintf(
		"[Desktop Entry]\nType=Application\nName=%s\nExec=%s\n%sTerminal=%t\nCategories=Utility;\n",
		strings.ReplaceAll(info.ProductName, "\n", " "), executable, iconLine, info.Terminal,
	)
	target := filepath.Join(home, ".local", "share", "applications", desktopName+".desktop")
	if existing, err := os.ReadFile(target); err == nil && string(existing) == content { // #nosec G304 -- fixed, computed XDG applications path.
		return
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { // #nosec G301 -- standard XDG applications directory mode.
		return
	}
	_ = os.WriteFile(target, []byte(content), 0o600) // #nosec G306 -- desktop entry content has no secrets.
}
