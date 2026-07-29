package executor

import "fmt"

// ErrorKind classifies command execution failures.
type ErrorKind string

const (
	// ErrorInvalidRequest means the request could not be executed safely.
	ErrorInvalidRequest ErrorKind = "invalid_request"
	// ErrorStart means the operating system could not start the process.
	ErrorStart ErrorKind = "start"
	// ErrorExit means the process returned a non-zero exit status.
	ErrorExit ErrorKind = "exit"
	// ErrorTimeout means the configured deadline expired.
	ErrorTimeout ErrorKind = "timeout"
	// ErrorCanceled means the caller canceled the context.
	ErrorCanceled ErrorKind = "canceled"
	// ErrorOutputLimit means stdout or stderr exceeded its capture bound.
	ErrorOutputLimit ErrorKind = "output_limit"
)

// Error preserves process context and the original error.
type Error struct {
	Kind      ErrorKind
	Operation string
	Path      string
	ExitCode  int
	Detail    string
	Err       error
}

func (e *Error) Error() string {
	message := fmt.Sprintf("%s command", e.Operation)
	if e.Path != "" {
		message += fmt.Sprintf(" %s", e.Path)
	}
	if e.ExitCode != 0 {
		message += fmt.Sprintf(" (exit code %d)", e.ExitCode)
	}
	if e.Detail != "" {
		message += ": " + e.Detail
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

// Unwrap exposes the underlying context or process error.
func (e *Error) Unwrap() error {
	return e.Err
}
