// Package containersmoke runs a produced application's package self-test
// inside a Docker container instead of natively, so a build can be
// verified — not just assembled — on a host that cannot execute the
// target OS/architecture directly: a real Windows .exe smoke-tested via
// Wine, or a real Linux binary smoke-tested in a plain Linux container.
// Every run is network-isolated (`docker run --network none`) and only
// ever touches whatever directory the caller mounts in — this package
// has no opinion on isolating that directory from a build's own staged
// output; internal/portable already does that (see its own
// ContainerRunner and docs/adr/0028). See
// docs/adr/0028-container-verified-cross-platform-builds.md.
package containersmoke

import "fmt"

// ErrorKind classifies containerized self-test failures.
type ErrorKind string

const (
	// ErrorUnsupported means a known, expected condition prevents
	// container-based verification (Docker missing or unreachable) —
	// callers should treat this as "cannot verify this target here",
	// not a crash.
	ErrorUnsupported ErrorKind = "unsupported"
	// ErrorBuild means building the pinned container image itself
	// failed.
	ErrorBuild ErrorKind = "build"
	// ErrorRun means starting or running the container itself failed,
	// as opposed to the self-test process it ran reporting failure
	// (which internal/portable.ValidateSmokeExecution interprets, not
	// this package).
	ErrorRun ErrorKind = "run"
)

// Error preserves operation context for containerized self-test failures.
type Error struct {
	Kind      ErrorKind
	Operation string
	Detail    string
	Err       error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("containersmoke %s: %s: %v", e.Operation, e.Detail, e.Err)
	}
	return fmt.Sprintf("containersmoke %s: %s", e.Operation, e.Detail)
}

func (e *Error) Unwrap() error { return e.Err }

func newError(kind ErrorKind, operation, detail string, err error) *Error {
	return &Error{Kind: kind, Operation: operation, Detail: detail, Err: err}
}
