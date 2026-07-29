// Package embedded builds and verifies single-file Fluxa applications.
package embedded

import "fmt"

// ErrorKind classifies embedded executable failures.
type ErrorKind string

const (
	// ErrorInvalid means the request or footer is malformed.
	ErrorInvalid ErrorKind = "invalid"
	// ErrorLimit means an input or output exceeds the supported size.
	ErrorLimit ErrorKind = "limit"
	// ErrorIntegrity means package or executable bytes failed hashing.
	ErrorIntegrity ErrorKind = "integrity"
	// ErrorIO means file access failed.
	ErrorIO ErrorKind = "io"
	// ErrorCanceled means the operation was canceled.
	ErrorCanceled ErrorKind = "canceled"
	// ErrorSmoke means the embedded runtime self-test failed.
	ErrorSmoke ErrorKind = "smoke"
)

// Error preserves safe operation and path context.
type Error struct {
	Kind      ErrorKind
	Operation string
	Path      string
	Err       error
}

func (e *Error) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("embedded %s: %v", e.Operation, e.Err)
	}
	return fmt.Sprintf("embedded %s %q: %v", e.Operation, e.Path, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

func embeddedError(kind ErrorKind, operation, path string, err error) *Error {
	return &Error{Kind: kind, Operation: operation, Path: path, Err: err}
}
