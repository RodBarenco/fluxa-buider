package containersmoke

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/executor"
)

// dockerBuildTimeout and dockerProbeTimeout mirror internal/toolchainbuild's
// own: generous because a first-time image build (installing Wine from
// WineHQ's own apt repo) genuinely takes minutes, while a cached rebuild or
// a real self-test run is fast.
const (
	dockerBuildTimeout = 15 * time.Minute
	dockerProbeTimeout = 5 * time.Second

	maxContainerStdout = 64 * 1024
	maxContainerStderr = 1024 * 1024
)

// mount is one bind mount passed to `docker run -v host:container`,
// mirroring internal/toolchainbuild's own identically-shaped helper.
type mount struct {
	host      string
	container string
}

// hostUserFlags is the docker run --user flag for the invoking host user.
// dockerRunIsolated always runs as this, not the container's default
// root — a bind mount shares the host filesystem directly, so a
// root-owned write would leave it unusable/undeletable by its actual
// owner without extra privileges (the same reasoning
// docs/adr/0027-automatic-toolchain-acquisition.md already applies to
// internal/toolchainbuild's own container invocations).
func hostUserFlags() []string {
	return []string{"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())}
}

// dockerAvailable reports whether the docker CLI is present and the daemon
// is reachable, without ever calling exec directly — internal/executor is
// this project's one permitted os/exec call site (ADR 0005).
func dockerAvailable(ctx context.Context) bool {
	_, err := executor.Run(ctx, executor.Request{
		Path: "docker", Args: []string{"info"}, Timeout: dockerProbeTimeout,
	})
	return err == nil
}

// dockerBuildImage builds dockerfilePath (using contextDir as the build
// context) and tags the result as tag. It is a no-op cost on every call
// after the first (Docker's own layer cache), so callers can call it on
// every run rather than tracking whether the image already exists. A
// failure here is always a containersmoke-level *Error (ErrorBuild): it
// means verification could not even start, distinct from the self-test
// process itself later failing.
func dockerBuildImage(ctx context.Context, dockerfilePath, contextDir, tag string) error {
	result, err := executor.Run(ctx, executor.Request{
		Path:      "docker",
		Args:      []string{"build", "-f", dockerfilePath, "-t", tag, contextDir},
		Timeout:   dockerBuildTimeout,
		MaxStdout: 8 * 1024 * 1024,
		MaxStderr: 8 * 1024 * 1024,
	})
	if err != nil {
		return newError(ErrorBuild, "build Docker image", result.Stderr, err)
	}
	return nil
}

// dockerImageID returns tag's content-addressed image ID (e.g.
// "sha256:abcdef..."). RunWindows uses this to key its cached
// WINEPREFIX per image build: real testing found a WINEPREFIX
// initialized by one Wine build and reused by a different one — this
// project cycled through several pinned Wine versions while diagnosing
// the "could not load kernel32.dll" failure documented in
// docs/adr/0028 — fails exactly that way: wineboot --init still
// silently "succeeds" and writes a system.reg, but any later real
// invocation against that same now-mismatched prefix then fails.
// Keying the cache by the exact image that created it makes this a
// non-issue: any image change (a version bump, a Dockerfile edit,
// anything) gets a fresh prefix automatically, with nothing to
// remember to invalidate by hand.
func dockerImageID(ctx context.Context, tag string) (string, error) {
	result, err := executor.Run(ctx, executor.Request{
		Path: "docker", Args: []string{"image", "inspect", "--format", "{{.Id}}", tag}, Timeout: dockerProbeTimeout,
	})
	if err != nil {
		return "", newError(ErrorBuild, "resolve Docker image id", result.Stderr, err)
	}
	return strings.TrimSpace(result.Stdout), nil
}

// dockerRunIsolated runs command inside image with mounts bind-mounted
// read-write, as the invoking host user (hostUserFlags) rather than the
// container's default root, and with networking fully disabled: a
// self-test must never be able to phone home. extraArgs are inserted
// right after the fixed flags — RunWindows uses this for --security-opt
// seccomp=unconfined: real testing found Docker's default seccomp
// profile makes Wine fail immediately (`could not load kernel32.dll,
// status c0000135`) even though the file genuinely exists on disk, a
// known class of issue for Wine under a restricted syscall filter, not
// specific to this project. RunLinux needs no such relaxation; a plain
// Linux binary runs fine under the default profile.
//
// Its return value is deliberately passed through exactly as
// internal/executor.Run produced it, unwrapped into any containersmoke
// error type: a self-test that legitimately exits non-zero, times out, or
// crashes is not this package's concern to interpret, only
// internal/portable.ValidateSmokeExecution's — and that function expects
// to unwrap an *executor.Error the same way for both a native and a
// containerized run, per ContainerRunner's documented contract.
func dockerRunIsolated(ctx context.Context, image string, mounts []mount, timeout time.Duration, command []string, extraArgs ...string) (executor.Result, error) {
	args := []string{"run", "--rm", "--network", "none"}
	args = append(args, hostUserFlags()...)
	// The container's own /etc/passwd has no entry for an arbitrary host
	// uid, so $HOME would otherwise be unset — Wine in particular gets
	// confused without one. /tmp is writable by any uid in every image
	// this package uses.
	args = append(args, "-e", "HOME=/tmp")
	args = append(args, extraArgs...)
	for _, m := range mounts {
		args = append(args, "-v", m.host+":"+m.container)
	}
	args = append(args, "-w", "/work", image)
	args = append(args, command...)

	return executor.Run(ctx, executor.Request{
		Path: "docker", Args: args,
		Timeout:   timeout,
		MaxStdout: maxContainerStdout,
		MaxStderr: maxContainerStderr,
	})
}
