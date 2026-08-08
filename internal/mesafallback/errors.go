// Package mesafallback fetches and caches the Mesa3D Windows software
// OpenGL fallback (github.com/pal1000/mesa-dist-win) so every Windows
// portable build can ship it beside the application, letting std.graph
// work even on a machine with no usable OpenGL driver (common inside
// VMs) — see docs/adr/0027-automatic-toolchain-acquisition.md and
// fluxa-lang's own docs/WINDOWS.md "Virtual machines and Mesa3D" section,
// which documents this exact mechanism and says Fluxa Builder is
// responsible for bundling it.
package mesafallback

import "fmt"

// ErrorKind classifies acquisition failures.
type ErrorKind string

const (
	// ErrorUnsupported means a known, expected condition prevents
	// caching Mesa (Docker missing, download/checksum/extraction
	// failure) — callers should treat this as a skippable, best-effort
	// enhancement, not a build failure. Mesa is optional companion
	// distribution, not a functional requirement.
	ErrorUnsupported ErrorKind = "unsupported"
	// ErrorIO means a filesystem operation failed.
	ErrorIO ErrorKind = "io"
)

// Error preserves operation context for acquisition failures.
type Error struct {
	Kind      ErrorKind
	Operation string
	Detail    string
	Err       error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("mesafallback %s: %s: %v", e.Operation, e.Detail, e.Err)
	}
	return fmt.Sprintf("mesafallback %s: %s", e.Operation, e.Detail)
}

func (e *Error) Unwrap() error { return e.Err }

func newError(kind ErrorKind, operation, detail string, err error) *Error {
	return &Error{Kind: kind, Operation: operation, Detail: detail, Err: err}
}
