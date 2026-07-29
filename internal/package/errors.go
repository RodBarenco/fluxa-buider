// Package flxpkg writes and verifies deterministic Fluxa package files.
package flxpkg

import "fmt"

// ErrorKind classifies package writing and verification failures.
type ErrorKind string

const (
	// ErrorCanceled means the caller canceled package construction.
	ErrorCanceled ErrorKind = "canceled"
	// ErrorInvalid means package metadata or bytes violate the format.
	ErrorInvalid ErrorKind = "invalid"
	// ErrorIntegrity means a cryptographic digest did not match.
	ErrorIntegrity ErrorKind = "integrity"
	// ErrorLimit means a package safety bound was exceeded.
	ErrorLimit ErrorKind = "limit"
	// ErrorIO means filesystem access failed.
	ErrorIO ErrorKind = "io"
)

// Error preserves package operation and path context.
type Error struct {
	Kind      ErrorKind
	Operation string
	Path      string
	Err       error
}

func (e *Error) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("flxpkg %s: %v", e.Operation, e.Err)
	}
	return fmt.Sprintf("flxpkg %s %q: %v", e.Operation, e.Path, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }
