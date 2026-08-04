// Command fluxa-runtime-wrapper is the Linux "adapted runtime" Fluxa Builder
// generates and embeds. The native Linux Fluxa interpreter has no private
// launcher protocol (unlike the Windows entrypoint's FLUXA_PACKAGED_RUNTIME
// mode), so this relay provides it: it accepts only the private
// launcher-to-runtime call internal/runner.go makes, and translates it into
// the interpreter's already-working `run <entry> -proj .` command. Anything
// else — wrong command, missing or wrong authorization, or direct execution
// with no arguments at all — is refused with exit code 126, matching the
// documented public-CLI refusal contract.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/RodBarenco/fluxa-builder/internal/runtimeprotocol"
)

// interpreterName is the fixed sibling filename Fluxa Builder places the
// verified interpreter binary at, next to this wrapper, when it assembles a
// Linux portable application (see internal/portable's Linux assembly).
const interpreterName = ".fluxa-runtime.interpreter"

func main() {
	os.Exit(run())
}

func run() int {
	if err := validateInvocation(); err != nil {
		fmt.Fprintln(os.Stderr, "fluxa-runtime-wrapper:", err)
		return 126
	}

	entry := os.Args[2]
	interpreter, err := interpreterPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fluxa-runtime-wrapper: locate interpreter:", err)
		return 126
	}

	// interpreter is a fixed sibling path resolved from this binary's own
	// location, not attacker-controlled input; entry is passed as a literal
	// argv element (no shell involved), and the private command name and
	// authorization were already validated above.
	command := exec.Command(interpreter, "run", entry, "-proj", ".") // #nosec G204 G702
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "fluxa-runtime-wrapper: run interpreter:", err)
		return 126
	}
	return 0
}

// validateInvocation requires exactly the private protocol
// runtimeprotocol.Command carries: this program is not a general-purpose
// Fluxa CLI and must refuse any other shape, including no arguments at all.
func validateInvocation() error {
	if len(os.Args) != 4 {
		return errors.New("this is a private Fluxa Builder runtime; open the generated application instead")
	}
	if os.Args[1] != runtimeprotocol.Command {
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
	if os.Getenv(runtimeprotocol.AuthEnvVar) != runtimeprotocol.AuthValue {
		return errors.New("missing or invalid launcher authorization")
	}
	if os.Args[2] == "" || os.Args[3] == "" {
		return errors.New("entry and project root are required")
	}
	return nil
}

func interpreterPath() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(self)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(resolved), interpreterName), nil
}
