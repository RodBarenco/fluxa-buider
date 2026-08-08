package containersmoke

import (
	"context"
	_ "embed"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/executor"
)

const wineImageTag = "fluxa-builder-containersmoke-wine"

//go:embed docker/wine.Dockerfile
var wineDockerfile []byte

// wineSeccompUnconfined disables Docker's default seccomp filter for
// running the Wine image (both priming it and actually using it). Real
// testing found Docker's default profile makes Wine fail immediately
// with "could not load kernel32.dll, status c0000135" even though the
// file genuinely exists on disk — a known class of issue for Wine under
// a restricted syscall filter (it needs syscalls the default profile
// doesn't allow), not specific to this project or environment. Building
// the image itself (installing packages) needs no such relaxation.
var wineSeccompUnconfined = []string{"--security-opt", "seccomp=unconfined"}

// wineRunArgs is wineSeccompUnconfined plus a bounded memory limit
// (windowsMemoryLimitMB, FLUXA_BUILDER_WINDOWS_CONTAINER_MEMORY_MB) —
// Docker gives a container no memory limit by default, so an unbounded
// Wine invocation can contend for the host's entire RAM budget alongside
// everything else running there. Used for both priming and actually
// running the image, so neither can do this.
func wineRunArgs() []string {
	return append(append([]string{}, wineSeccompUnconfined...), memoryLimitArgs(windowsMemoryLimitMB())...)
}

// RunWindows runs executable (a produced Windows application) inside a
// pinned, network-isolated Wine container — internal/portable's
// ContainerRunner contract — letting any host with Docker verify a
// Windows build, not only real Windows or a host with Wine installed
// directly. directory must already be a disposable, isolated copy of the
// build's staged output; this package only runs what it is given.
//
// Wine's own startup, not just the self-test process itself, eats into
// timeout — a produced GUI-subsystem application (see ADR 0026's terminal
// mode) may still touch Win32 windowing internals even though the
// self-test contract forbids actually opening a visible window, so this
// always runs under xvfb-run (a headless X server) rather than assuming a
// console-only self-test never needs one.
//
// Real testing while building this package hit — and fixed — several
// genuine Wine-in-Docker issues (see this function's own internal
// comments and docs/adr/0028 for the full list: a missing
// /tmp/.X11-unix under a non-root Xvfb, a wineserver deadlock, WINEPREFIX
// ownership, stale-prefix reuse across image rebuilds). One further
// issue was found but not resolved: wineboot --init reliably succeeds,
// but the generic `wine <program>` launcher — which always bootstraps
// through the 32-bit WoW64 syswow64\start.exe regardless of the target
// program's own architecture — reproducibly fails to load its own
// kernel32.dll in this project's development environment, for a reason
// that survived extensive isolation testing (Docker security options,
// namespaces, disk/memory pressure, two different Wine versions, DLL
// presence and permissions all ruled out). wineInfrastructureFailure
// below exists specifically so this unresolved failure mode degrades a
// build to a warning rather than blocking it — see
// internal/app.smokeVerify.
func RunWindows(ctx context.Context, executable, directory string, timeout time.Duration) (executor.Result, error) {
	if !dockerAvailable(ctx) {
		return executor.Result{}, newError(ErrorUnsupported, "check prerequisites", "Docker is not installed or not reachable", nil)
	}
	containerPath, err := containerExecutablePath(directory, executable)
	if err != nil {
		return executor.Result{}, newError(ErrorRun, "resolve executable path", executable, err)
	}
	if err := ensureWineImageBuilt(ctx); err != nil {
		return executor.Result{}, err
	}
	imageID, err := dockerImageID(ctx, wineImageTag)
	if err != nil {
		return executor.Result{}, err
	}
	prefixDir, err := ensureWineprefixInitialized(ctx, imageID)
	if err != nil {
		return executor.Result{}, err
	}

	// wine leaves explorer.exe/services.exe resident after the requested
	// program exits — a real Windows session behaves the same way, it
	// never self-terminates. `wineserver -k` force-ends the session
	// immediately after the self-test's own exit code is captured, so
	// that code (not wineserver -k's) is what gets returned — but it
	// must run INSIDE the same xvfb-run session that started those
	// resident processes, not as a separate step chained after
	// xvfb-run itself returns: xvfb-run only returns once every client
	// connected to its virtual display disconnects, which explorer.exe
	// et al. never do on their own, so a `wineserver -k` chained after
	// xvfb-run (`xvfb-run -a wine ...; wineserver -k`) never actually
	// runs — a real, reproducible deadlock found while building this
	// package, not a hypothetical one. See docs/adr/0028.
	const sessionScript = `xvfb-run -a sh -c 'wine "$0" --fluxa-package-self-test; code=$?; wineserver -k >/dev/null 2>&1 || true; exit $code' "$0"`
	mounts := []mount{{host: directory, container: "/work"}, {host: prefixDir, container: "/wineprefix"}}
	result, err := dockerRunIsolated(ctx, wineImageTag, mounts, timeout,
		[]string{"sh", "-c", sessionScript, containerPath}, wineRunArgs()...)
	if err != nil && wineInfrastructureFailure(result.Stderr) {
		// A *containersmoke.Error here, not the raw *executor.Error
		// dockerRunIsolated's own doc comment says to pass through
		// unchanged: this specific stderr means Wine's own execution
		// environment broke, not that the self-test process itself
		// genuinely ran and failed — see wineInfrastructureSignatures.
		// Wrapping it lets internal/app's smokeVerify route it through
		// the same graceful-degradation path as any other
		// containersmoke.Error (Docker unavailable, image build
		// failure): publish with a warning instead of blocking the
		// build over something outside this project's control.
		return result, newError(ErrorRun, "run self-test inside Wine", result.Stderr, err)
	}
	return result, err
}

// wineInfrastructureSignatures are stderr substrings that mean Wine's own
// execution environment is broken, not that the self-test process itself
// genuinely ran and failed:
//
//   - "could not load kernel32.dll" — the unresolved failure this
//     package's own doc comments and docs/adr/0028 document in full:
//     wineboot --init (the 64-bit path) reliably succeeds, but the
//     generic `wine <program>` launcher (which always bootstraps through
//     the 32-bit WoW64 syswow64\start.exe) reproducibly fails to load its
//     own kernel32.dll here. Extensive isolation testing ruled out this
//     project's own request construction, Docker security options,
//     namespaces, disk/memory pressure, and a Wine version regression
//     (reproduced identically on 9.0 and 11.0) without finding the actual
//     cause.
//   - "is not owned by you" — the WINEPREFIX ownership check, in case
//     some future environment defeats ensureWineprefixInitialized's own
//     fix for it (a bind-mounted, host-owned directory).
var wineInfrastructureSignatures = []string{
	"could not load kernel32.dll",
	"is not owned by you",
}

func wineInfrastructureFailure(stderr string) bool {
	for _, signature := range wineInfrastructureSignatures {
		if strings.Contains(stderr, signature) {
			return true
		}
	}
	return false
}

func ensureWineImageBuilt(ctx context.Context) error {
	dockerfilePath, contextDir, err := writeEmbeddedDockerfile(wineDockerfile, "wine.Dockerfile")
	if err != nil {
		return newError(ErrorBuild, "prepare Wine image build context", "", err)
	}
	defer func() { _ = os.RemoveAll(contextDir) }()
	return dockerBuildImage(ctx, dockerfilePath, contextDir, wineImageTag)
}

// ensureWineprefixInitialized returns a persistent, host-owned directory
// (under the current user's home, keyed only by this package — safe to
// delete at any time, it is fully rebuilt on next use) populated with a
// real WINEPREFIX, initializing it via a real `wineboot --init` run on
// first use.
//
// This is a bind mount, not anything baked into the image or committed
// as a new one: real testing found Wine's own ownership check on
// WINEPREFIX ("wine: '/wineprefix' is not owned by you") rejects
// anything not created by the exact uid using it, and every path that
// pre-creates the directory as part of the image itself (a Dockerfile
// RUN step, `docker commit` after priming as a different uid, even a
// pre-created empty directory with permissive chmod bits) leaves it
// owned by whichever uid happened to create it at build/commit time —
// not the arbitrary host uid dockerRunIsolated actually runs as. A bind
// mount sidesteps the problem entirely: its ownership is whatever the
// host-side directory's owner already is, which is exactly the host uid
// running Fluxa Builder itself, on both the priming call below and every
// real one afterward.
//
// The cache path is keyed by imageID (see dockerImageID's own doc
// comment): a prefix initialized by one Wine build and reused by a
// different one is a real, reproducible failure mode, not a
// hypothetical one — this project hit it directly while diagnosing
// docs/adr/0028's "could not load kernel32.dll" investigation. Keying by
// the exact image, not just a version string that could be forgotten,
// makes any image change self-heal automatically.
func ensureWineprefixInitialized(ctx context.Context, imageID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", newError(ErrorBuild, "resolve Wine prefix cache directory", "", err)
	}
	prefixDir := filepath.Join(home, ".fluxa-builder", "containersmoke", "wineprefix-"+shortImageID(imageID))
	if err := os.MkdirAll(prefixDir, 0o700); err != nil {
		return "", newError(ErrorBuild, "create Wine prefix cache directory", prefixDir, err)
	}
	// system.reg only exists once wineboot --init has actually completed
	// successfully — a safe, cheap "already primed" check that also
	// self-heals a previous partial/failed attempt (no marker written,
	// so the next call retries init instead of reusing a broken prefix).
	if _, err := os.Stat(filepath.Join(prefixDir, "system.reg")); err == nil {
		return prefixDir, nil
	}

	const primeScript = `xvfb-run -a sh -c 'wineboot --init; wineserver -k >/dev/null 2>&1 || true'`
	result, err := dockerRunIsolated(ctx, wineImageTag, []mount{{host: prefixDir, container: "/wineprefix"}}, dockerBuildTimeout,
		[]string{"sh", "-c", primeScript}, wineRunArgs()...)
	if err != nil {
		return "", newError(ErrorBuild, "initialize Wine prefix", result.Stderr, err)
	}
	if _, err := os.Stat(filepath.Join(prefixDir, "system.reg")); err != nil {
		return "", newError(ErrorBuild, "initialize Wine prefix", prefixDir, err)
	}
	return prefixDir, nil
}

// shortImageID trims a "sha256:" prefix and takes the leading 12 hex
// characters, matching `docker images`' own short-ID convention — just
// short enough to keep the cache directory name readable.
func shortImageID(imageID string) string {
	id := strings.TrimPrefix(imageID, "sha256:")
	if len(id) > 12 {
		id = id[:12]
	}
	return id
}

func writeEmbeddedDockerfile(content []byte, name string) (dockerfilePath, contextDir string, err error) {
	contextDir, err = os.MkdirTemp("", "fluxa-builder-containersmoke-*")
	if err != nil {
		return "", "", err
	}
	dockerfilePath = filepath.Join(contextDir, name)
	if err := os.WriteFile(dockerfilePath, content, 0o600); err != nil {
		_ = os.RemoveAll(contextDir)
		return "", "", err
	}
	return dockerfilePath, contextDir, nil
}
