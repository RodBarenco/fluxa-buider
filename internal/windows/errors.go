// Package windows validates Windows-specific portable application inputs.
package windows

import "fmt"

// ErrorKind classifies Windows artifact validation failures.
type ErrorKind string

const (
	// ErrorInvalid means a PE or ICO structure is malformed or unsupported.
	ErrorInvalid ErrorKind = "invalid"
	// ErrorIO means a Windows artifact could not be read.
	ErrorIO ErrorKind = "io"
	// ErrorLimit means a Windows artifact exceeds a safety limit.
	ErrorLimit ErrorKind = "limit"
)

// Error preserves operation and path context.
type Error struct {
	Kind      ErrorKind
	Operation string
	Path      string
	Err       error
}

func (e *Error) Error() string {
	return fmt.Sprintf("windows %s %q: %v", e.Operation, e.Path, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

func windowsError(kind ErrorKind, operation, path string, err error) *Error {
	return &Error{Kind: kind, Operation: operation, Path: path, Err: err}
}
