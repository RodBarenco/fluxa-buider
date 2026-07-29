// Package signing signs and verifies Fluxa package identities with Ed25519.
package signing

import "fmt"

// ErrorKind classifies package signing failures.
type ErrorKind string

const (
	// ErrorInvalidInput means required paths or values are invalid.
	ErrorInvalidInput ErrorKind = "invalid_input"
	// ErrorKey means a key is missing, unsafe, or malformed.
	ErrorKey ErrorKind = "key"
	// ErrorIO means signature file access failed.
	ErrorIO ErrorKind = "io"
	// ErrorSignature means signature metadata or verification is invalid.
	ErrorSignature ErrorKind = "signature"
	// ErrorIntegrity means package integrity or identity validation failed.
	ErrorIntegrity ErrorKind = "integrity"
)

// Error provides safe operation context without including key material.
type Error struct {
	Kind      ErrorKind
	Operation string
	Path      string
	Err       error
}

func (e *Error) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("signing %s: %v", e.Operation, e.Err)
	}
	return fmt.Sprintf("signing %s %q: %v", e.Operation, e.Path, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

func signingError(kind ErrorKind, operation, path string, err error) *Error {
	return &Error{Kind: kind, Operation: operation, Path: path, Err: err}
}
