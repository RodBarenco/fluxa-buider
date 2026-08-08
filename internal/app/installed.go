package app

import (
	"io"

	"github.com/RodBarenco/fluxa-builder/internal/launcher"
)

// IsInstalledInvocation and RunInstalled keep working for applications
// assembled before cmd/fluxa-launcher existed, which were distributed as a
// renamed copy of the Fluxa Builder executable itself and therefore still
// enter through cmd/fluxa-builder's own main. Newly assembled applications
// carry a real cmd/fluxa-launcher binary instead — see
// docs/adr/0029-cross-target-application-launcher.md — so this path is
// backwards compatibility, not the current one.

// IsInstalledInvocation reports whether the Builder executable was renamed to
// become an application launcher.
func IsInstalledInvocation(executable string) bool {
	return launcher.IsInvocation(executable)
}

// RunInstalled executes a portable application assembled by Fluxa Builder.
func RunInstalled(executable string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return launcher.Run(executable, args, stdin, stdout, stderr)
}
