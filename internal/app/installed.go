package app

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
	"github.com/RodBarenco/fluxa-builder/internal/runner"
)

const installedRuntimeName = ".fluxa-runtime"

// IsInstalledInvocation reports whether the Builder executable was renamed to
// become an application launcher.
func IsInstalledInvocation(executable string) bool {
	portablePath := strings.ReplaceAll(executable, `\`, "/")
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(portablePath)), ".exe")
	return name != Name
}

// RunInstalled executes a portable application assembled by Fluxa Builder.
func RunInstalled(executable string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	directory := filepath.Dir(executable)
	packagePath, err := installedPackage(directory)
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
	runtimeName := installedRuntimeName
	if runtime.GOOS == "windows" {
		runtimeName += ".exe"
	}
	if err := runner.Run(context.Background(), runner.Request{
		PackagePath:     packagePath,
		RuntimePath:     filepath.Join(directory, runtimeName),
		DistributionDir: directory,
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

func installedPackage(directory string) (string, error) {
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
