// Package runtimepkg manages verified Fluxa runtime binaries.
package runtimepkg

import "fmt"

// ErrorKind classifies runtime registry failures.
type ErrorKind string

const (
	// ErrorNotFound means no compatible runtime exists.
	ErrorNotFound ErrorKind = "not_found"
	// ErrorInvalid means metadata or registry structure is invalid.
	ErrorInvalid ErrorKind = "invalid"
	// ErrorIncompatible means available runtime metadata does not match.
	ErrorIncompatible ErrorKind = "incompatible"
	// ErrorIntegrity means a runtime binary hash does not match metadata.
	ErrorIntegrity ErrorKind = "integrity"
	// ErrorPermission means a runtime cannot be executed safely.
	ErrorPermission ErrorKind = "permission"
	// ErrorExists means the registry slot is already occupied.
	ErrorExists ErrorKind = "exists"
	// ErrorIO means registry filesystem access failed.
	ErrorIO ErrorKind = "io"
)

// Error preserves registry operation and path context.
type Error struct {
	Kind      ErrorKind
	Operation string
	Path      string
	Err       error
}

func (e *Error) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("runtime registry %s: %v", e.Operation, e.Err)
	}
	return fmt.Sprintf("runtime registry %s %q: %v", e.Operation, e.Path, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }
