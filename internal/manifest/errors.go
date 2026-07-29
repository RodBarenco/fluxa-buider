package manifest

import "fmt"

// ErrorKind classifies manifest construction, validation, and I/O failures.
type ErrorKind string

const (
	// ErrorCanceled means the caller canceled manifest construction.
	ErrorCanceled ErrorKind = "canceled"
	// ErrorInvalid means manifest data violates the schema.
	ErrorInvalid ErrorKind = "invalid"
	// ErrorUnknownVersion means the schema version is unsupported.
	ErrorUnknownVersion ErrorKind = "unknown_version"
	// ErrorTooLarge means encoded input exceeds the manifest size limit.
	ErrorTooLarge ErrorKind = "too_large"
	// ErrorIO means filesystem access failed.
	ErrorIO ErrorKind = "io"
)

// Error preserves a manifest failure's operation and cause.
type Error struct {
	Kind      ErrorKind
	Operation string
	Path      string
	Err       error
}

func (e *Error) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("manifest %s: %v", e.Operation, e.Err)
	}
	return fmt.Sprintf("manifest %s %q: %v", e.Operation, e.Path, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }
