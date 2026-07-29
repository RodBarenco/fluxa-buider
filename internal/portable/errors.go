// Package portable assembles and validates standalone portable directories.
package portable

import "fmt"

// ErrorKind classifies portable artifact failures.
type ErrorKind string

const (
	// ErrorCanceled means assembly or smoke testing was canceled.
	ErrorCanceled ErrorKind = "canceled"
	// ErrorInvalid means requested metadata or input artifacts are inconsistent.
	ErrorInvalid ErrorKind = "invalid"
	// ErrorIntegrity means a copied runtime or package failed verification.
	ErrorIntegrity ErrorKind = "integrity"
	// ErrorPermission means executable permissions could not be established.
	ErrorPermission ErrorKind = "permission"
	// ErrorIO means filesystem access failed.
	ErrorIO ErrorKind = "io"
	// ErrorSmoke means the staged runtime rejected its package.
	ErrorSmoke ErrorKind = "smoke"
)

// Error preserves operation and path context.
type Error struct {
	Kind      ErrorKind
	Operation string
	Path      string
	Err       error
}

func (e *Error) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("portable %s: %v", e.Operation, e.Err)
	}
	return fmt.Sprintf("portable %s %q: %v", e.Operation, e.Path, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }
