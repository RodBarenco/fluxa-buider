package macos

import "fmt"

// Error describes an invalid macOS release input.
type Error struct {
	Operation string
	Path      string
	Err       error
}

func (e *Error) Error() string {
	return fmt.Sprintf("macOS %s %q: %v", e.Operation, e.Path, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }
