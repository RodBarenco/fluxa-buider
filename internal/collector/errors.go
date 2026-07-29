package collector

import "fmt"

// ErrorKind classifies collection failures.
type ErrorKind string

const (
	// ErrorCanceled means the caller canceled collection.
	ErrorCanceled ErrorKind = "canceled"
	// ErrorInvalidInput means collection options are invalid.
	ErrorInvalidInput ErrorKind = "invalid_input"
	// ErrorUnsafePath means a selected path or file type is unsafe.
	ErrorUnsafePath ErrorKind = "unsafe_path"
	// ErrorCollision means two logical paths are not portable together.
	ErrorCollision ErrorKind = "collision"
	// ErrorLimit means a configured resource bound was exceeded.
	ErrorLimit ErrorKind = "limit"
	// ErrorIO means filesystem inspection failed.
	ErrorIO ErrorKind = "io"
)

// Error adds collection context while preserving the underlying cause.
type Error struct {
	Kind      ErrorKind
	Operation string
	Path      string
	Err       error
}

func (e *Error) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("collector %s: %v", e.Operation, e.Err)
	}
	return fmt.Sprintf("collector %s %q: %v", e.Operation, e.Path, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }
