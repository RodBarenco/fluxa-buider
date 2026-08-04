// Package runtimeprotocol holds the private launcher-to-runtime protocol
// constants shared between internal/runner (the caller) and
// cmd/fluxa-runtime-wrapper (the Linux relay that implements the callee
// side of this same protocol). Keeping one source of truth avoids two
// independently-typed string literals drifting apart.
package runtimeprotocol

const (
	// Command is the private subcommand a packaged runtime must accept.
	Command = "__fluxa_builder_run_v1"

	// AuthEnvVar is the environment variable carrying AuthValue, set by the
	// launcher on every private invocation.
	AuthEnvVar = "FLUXA_BUILDER_RUNTIME_AUTH"

	// AuthValue is the fixed shared secret identifying a genuine launcher
	// invocation. It is not a capability boundary against a determined
	// attacker who reads this source file; it exists to keep a packaged
	// runtime from being triggered by an unrelated process that merely
	// guesses the private command name.
	AuthValue = "fluxa-builder-packaged-runtime-v1:4f726967696e2d6c6f636b65642d72756e74696d65"
)
