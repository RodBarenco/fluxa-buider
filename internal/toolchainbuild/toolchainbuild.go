package toolchainbuild

import (
	"context"
	"path/filepath"
)

// Request describes the project and target Acquire should build a
// toolchain and packaged runtime for.
type Request struct {
	// ProjectRoot is the project directory containing fluxa.toml and, if
	// present, fluxa.libs.
	ProjectRoot string
	// Terminal mirrors the project's build.terminal setting.
	Terminal bool
	// TargetOS is "linux" or "windows" (the only two Acquire supports in
	// this pass; macOS is on hold — see docs/adr/0027).
	TargetOS string
	// OutputDir receives the built toolchain/runtime binaries.
	OutputDir string
	// CacheRoot is the persistent host directory Acquire clones and
	// builds under, normally ~/.fluxa-builder/toolchain-src.
	CacheRoot string
}

// Confirmer gates every consequential step (building/running a container,
// deleting a cache) behind explicit user confirmation. The wizard
// implements this over its existing askYesNo.
type Confirmer interface {
	Confirm(action string) (bool, error)
}

// Result carries the binaries Acquire produced, ready for
// toolchain.Probe/runtime.Add, plus enough state for an optional cleanup
// step.
type Result struct {
	// ToolchainPath is the binary to use as fluxa.toml's [toolchain] path
	// (also the file Acquire registers as the runtime on Linux, which has
	// no separate packaged build).
	ToolchainPath string
	// RuntimePath is the binary to register with runtime.Add. On Linux
	// this equals ToolchainPath; on Windows it is the separate
	// FLUXA_PACKAGED_RUNTIME=1 build.
	RuntimePath string
	// HostToolchainPath is a compiler that actually executes on the
	// machine running Fluxa Builder, which ToolchainPath is not when
	// TargetOS differs from the host OS: a cross-compiled
	// fluxa-toolchain.exe is a Windows PE, unrunnable on a Linux host, so
	// it can neither compile this project nor supply the toolchain
	// identity `build` records in a package manifest. Acquire builds one
	// from the same fluxa-lang checkout whenever it can, and leaves this
	// empty when it cannot (a macOS host, whose native path is still on
	// hold — see docs/adr/0027-automatic-toolchain-acquisition.md).
	HostToolchainPath string
	// SourceCacheDir is the persistent fluxa-lang checkout Acquire used,
	// offered for deletion by the wizard's cleanup step.
	SourceCacheDir string
	// ImageTags lists every Docker image Acquire built, offered for
	// removal by the same cleanup step.
	ImageTags []string
}

// declined is returned by acquire steps when the user does not confirm a
// required action; Acquire surfaces it as ErrorDeclined so the wizard can
// fall back to the manual guide without treating it as a crash.
func declined(action string) error {
	return newError(ErrorDeclined, "confirm", action, nil)
}

func confirmOrDecline(confirm Confirmer, action string) error {
	ok, err := confirm.Confirm(action)
	if err != nil {
		return newError(ErrorIO, "confirm", action, err)
	}
	if !ok {
		return declined(action)
	}
	return nil
}

// Acquire clones fluxa-lang, builds it inside a pinned Docker container,
// and returns the resulting toolchain/runtime binaries. It never mutates
// the host's own package manager state.
func Acquire(ctx context.Context, request Request, confirm Confirmer) (Result, error) {
	if !dockerAvailable(ctx) {
		return Result{}, newError(ErrorUnsupported, "check prerequisites", "Docker is not installed or not reachable", nil)
	}
	switch request.TargetOS {
	case "linux":
		return acquireLinux(ctx, request, confirm)
	case "windows":
		return acquireWindows(ctx, request, confirm)
	default:
		return Result{}, newError(ErrorUnsupported, "select platform", "automatic acquisition supports linux and windows only in this version", nil)
	}
}

func sourceCacheDir(cacheRoot string) string {
	return filepath.Join(cacheRoot, "fluxa-lang")
}

// RemoveImage deletes a Docker image Acquire built, for the wizard's
// optional post-build cleanup step. A missing image is not an error.
func RemoveImage(ctx context.Context, tag string) error {
	return dockerRemoveImage(ctx, tag)
}
