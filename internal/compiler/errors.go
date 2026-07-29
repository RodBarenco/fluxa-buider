package compiler

import "fmt"

// ErrorKind classifies compilation and source-staging failures.
type ErrorKind string

const (
	// ErrorUnavailable means the Fluxa toolchain has no stable compile contract.
	ErrorUnavailable ErrorKind = "unavailable"
	// ErrorCanceled means the caller canceled the operation.
	ErrorCanceled ErrorKind = "canceled"
	// ErrorInvalidInput means the compilation request is inconsistent.
	ErrorInvalidInput ErrorKind = "invalid_input"
	// ErrorUnsafePath means an output path escaped or used an unsafe file type.
	ErrorUnsafePath ErrorKind = "unsafe_path"
	// ErrorIO means reading or writing an artifact failed.
	ErrorIO ErrorKind = "io"
)

// Error preserves the underlying failure with compiler context.
type Error struct {
	Kind      ErrorKind
	Operation string
	Path      string
	Err       error
}

func (e *Error) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("compiler %s: %v", e.Operation, e.Err)
	}
	return fmt.Sprintf("compiler %s %q: %v", e.Operation, e.Path, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }
