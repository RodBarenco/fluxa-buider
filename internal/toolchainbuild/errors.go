// Package toolchainbuild acquires a Fluxa toolchain and packaged runtime
// automatically: cloning fluxa-lang, building it inside a pinned Docker
// container (avoiding host library version drift), and handing back
// binaries internal/app can register with the existing toolchain/runtime
// packages. See docs/adr/0027-automatic-toolchain-acquisition.md.
package toolchainbuild

import "fmt"

// ErrorKind classifies acquisition failures.
type ErrorKind string

const (
	// ErrorUnsupported means a known, expected condition prevents
	// automatic acquisition (Docker missing, an unsupported library, an
	// unsupported host) — callers should fall back to the manual guide,
	// not report a crash.
	ErrorUnsupported ErrorKind = "unsupported"
	// ErrorDeclined means the user did not confirm a required step.
	ErrorDeclined ErrorKind = "declined"
	// ErrorIO means a filesystem or network operation failed.
	ErrorIO ErrorKind = "io"
	// ErrorBuild means the containerized build itself failed.
	ErrorBuild ErrorKind = "build"
)

// Error preserves operation context for acquisition failures.
type Error struct {
	Kind      ErrorKind
	Operation string
	Detail    string
	Err       error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("toolchainbuild %s: %s: %v", e.Operation, e.Detail, e.Err)
	}
	return fmt.Sprintf("toolchainbuild %s: %s", e.Operation, e.Detail)
}

func (e *Error) Unwrap() error { return e.Err }

func newError(kind ErrorKind, operation, detail string, err error) *Error {
	return &Error{Kind: kind, Operation: operation, Detail: detail, Err: err}
}
