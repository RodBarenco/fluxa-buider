package containersmoke

import (
	"context"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/executor"
)

// linuxImage is pinned by digest (not just the "bookworm-slim" tag) for the
// same determinism reason every other pinned dependency in this project is
// — resolved for real via `docker pull debian:bookworm-slim` while writing
// this package. No custom Dockerfile is needed for this path: a produced
// Linux binary is already a native ELF executable for this same image
// family, so it runs directly, unlike the Windows target's Wine image.
const linuxImage = "debian@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241"

// RunLinux runs executable (a produced Linux application) inside a pinned,
// network-isolated Linux container — internal/portable's ContainerRunner
// contract — for hosts that cannot execute a Linux target natively (e.g.
// building a Linux standalone from a Windows or macOS host). directory
// must already be a disposable, isolated copy of the build's staged
// output; this package only runs what it is given.
func RunLinux(ctx context.Context, executable, directory string, timeout time.Duration) (executor.Result, error) {
	if !dockerAvailable(ctx) {
		return executor.Result{}, newError(ErrorUnsupported, "check prerequisites", "Docker is not installed or not reachable", nil)
	}
	containerPath, err := containerExecutablePath(directory, executable)
	if err != nil {
		return executor.Result{}, newError(ErrorRun, "resolve executable path", executable, err)
	}
	// Docker gives a container no memory limit by default, so an
	// unbounded self-test could otherwise contend for the host's entire
	// RAM budget — see wine.go's counterpart, wineRunArgs.
	return dockerRunIsolated(ctx, linuxImage, []mount{{host: directory, container: "/work"}}, timeout,
		[]string{containerPath, "--fluxa-package-self-test"}, memoryLimitArgs(linuxMemoryLimitMB())...)
}
