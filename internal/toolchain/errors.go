// Package toolchain locates and identifies the external Fluxa toolchain.
package toolchain

import "fmt"

// ErrorKind classifies toolchain discovery and probing failures.
type ErrorKind string

const (
	// ErrorNotFound means no Fluxa executable was found in any discovery layer.
	ErrorNotFound ErrorKind = "not_found"
	// ErrorInvalidExecutable means a configured candidate cannot be executed.
	ErrorInvalidExecutable ErrorKind = "invalid_executable"
	// ErrorProbe means the runtime-info process or executable hashing failed.
	ErrorProbe ErrorKind = "probe"
	// ErrorTimeout means runtime-info exceeded its deadline.
	ErrorTimeout ErrorKind = "timeout"
	// ErrorCanceled means the caller canceled runtime-info.
	ErrorCanceled ErrorKind = "canceled"
	// ErrorInvalidOutput means a process did not return the Fluxa signature.
	ErrorInvalidOutput ErrorKind = "invalid_output"
	// ErrorCompatibility means a required Fluxa version could not be satisfied.
	ErrorCompatibility ErrorKind = "compatibility"
)

// Error preserves context and the original cause of a toolchain failure.
type Error struct {
	Kind      ErrorKind
	Operation string
	Path      string
	Detail    string
	Err       error
}

func (e *Error) Error() string {
	message := fmt.Sprintf("%s Fluxa toolchain", e.Operation)
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

// Unwrap exposes the process or filesystem error.
func (e *Error) Unwrap() error {
	return e.Err
}
