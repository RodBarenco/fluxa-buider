package linux

import "fmt"

// Error describes an invalid Linux release input.
type Error struct {
	Operation string
	Path      string
	Err       error
}

func (e *Error) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("linux %s: %v", e.Operation, e.Err)
	}
	return fmt.Sprintf("linux %s %q: %v", e.Operation, e.Path, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }
