package project

import "fmt"

// ErrorKind classifies project configuration failures.
type ErrorKind string

const (
	// ErrorNotFound means a required project file or path was not found.
	ErrorNotFound ErrorKind = "not_found"
	// ErrorParse means fluxa.toml could not be decoded.
	ErrorParse ErrorKind = "parse"
	// ErrorValidation means decoded configuration violates a Builder contract.
	ErrorValidation ErrorKind = "validation"
	// ErrorIO means a filesystem operation failed.
	ErrorIO ErrorKind = "io"
)

// Error is a structured project loading error.
type Error struct {
	Kind      ErrorKind
	Operation string
	Path      string
	Field     string
	Value     string
	Err       error
}

func (e *Error) Error() string {
	message := fmt.Sprintf("%s project configuration", e.Operation)
	if e.Field != "" {
		message += fmt.Sprintf(": field %s", e.Field)
	}
	if e.Value != "" {
		message += fmt.Sprintf(" (value %q)", e.Value)
	}
	if e.Path != "" {
		message += fmt.Sprintf(" at %s", e.Path)
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

// Unwrap preserves the underlying filesystem or parser error.
func (e *Error) Unwrap() error {
	return e.Err
}
