// Package build manages transactional build state and publication.
package build

import "fmt"

// ErrorKind classifies workspace and publication failures.
type ErrorKind string

const (
	// ErrorCanceled means the caller canceled an operation.
	ErrorCanceled ErrorKind = "canceled"
	// ErrorCollision means unique workspace allocation exhausted its retries.
	ErrorCollision ErrorKind = "collision"
	// ErrorUnsafePath means an operation could escape approved roots.
	ErrorUnsafePath ErrorKind = "unsafe_path"
	// ErrorCreate means workspace creation failed.
	ErrorCreate ErrorKind = "create"
	// ErrorCleanup means workspace cleanup failed.
	ErrorCleanup ErrorKind = "cleanup"
	// ErrorOutputExists means publication would overwrite an artifact.
	ErrorOutputExists ErrorKind = "output_exists"
	// ErrorPublish means atomic rename publication failed.
	ErrorPublish ErrorKind = "publish"
)

// Error preserves workspace context and the original cause.
type Error struct {
	Kind      ErrorKind
	Operation string
	Path      string
	Detail    string
	Err       error
}

func (e *Error) Error() string {
	message := e.Operation + " build workspace"
	if e.Path != "" {
		message += fmt.Sprintf(" at %s", e.Path)
	}
	if e.Detail != "" {
		message += ": " + e.Detail
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

// Unwrap exposes the underlying filesystem or context error.
func (e *Error) Unwrap() error {
	return e.Err
}
