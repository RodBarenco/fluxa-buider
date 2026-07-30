package installer

import "fmt"

// Error describes an installer generation failure.
type Error struct {
	Kind      string
	Operation string
	Path      string
	Err       error
}

func (e *Error) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("installer %s: %v", e.Operation, e.Err)
	}
	return fmt.Sprintf("installer %s %q: %v", e.Operation, e.Path, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }
