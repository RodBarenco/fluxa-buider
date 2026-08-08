package toolchainbuild

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/executor"
)

// dockerBuildTimeout and dockerRunTimeout are generous: the first build of
// an image compiles a full C toolchain's worth of native dependencies
// (raylib from source, on Windows), which can genuinely take minutes; a
// cache hit on a later run is fast regardless.
const (
	dockerBuildTimeout = 30 * time.Minute
	dockerRunTimeout   = 30 * time.Minute
	dockerProbeTimeout = 5 * time.Second
)

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
// context) and tags the result as tag. It is a no-op cost the second time
// (Docker's own layer cache), so callers can call it on every run rather
// than tracking whether the image already exists.
func dockerBuildImage(ctx context.Context, dockerfilePath, contextDir, tag string) (executor.Result, error) {
	result, err := executor.Run(ctx, executor.Request{
		Path:      "docker",
		Args:      []string{"build", "-f", dockerfilePath, "-t", tag, contextDir},
		Timeout:   dockerBuildTimeout,
		MaxStdout: 8 * 1024 * 1024,
		MaxStderr: 8 * 1024 * 1024,
	})
	if err != nil {
		return result, newError(ErrorBuild, "build Docker image", tag, err)
	}
	return result, nil
}

// mount is one bind mount passed to `docker run -v host:container`.
type mount struct {
	host      string
	container string
}

// dockerRunBuild runs image with the given bind mounts and command,
// executing as the current host user (uid:gid) rather than the
// container's default root — a bind mount shares the host filesystem
// directly, so a root-owned write here would leave the host's own
// ~/.fluxa-builder cache undeletable by its actual owner without sudo.
// See docs/adr/0027-automatic-toolchain-acquisition.md.
func dockerRunBuild(ctx context.Context, image string, mounts []mount, workdir string, command []string) (executor.Result, error) {
	args := []string{
		"run", "--rm",
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
	}
	for _, m := range mounts {
		args = append(args, "-v", m.host+":"+m.container)
	}
	if workdir != "" {
		args = append(args, "-w", workdir)
	}
	args = append(args, image)
	args = append(args, command...)

	result, err := executor.Run(ctx, executor.Request{
		Path: "docker", Args: args,
		Timeout:   dockerRunTimeout,
		MaxStdout: 8 * 1024 * 1024,
		MaxStderr: 8 * 1024 * 1024,
	})
	if err != nil {
		return result, newError(ErrorBuild, "run Docker container", image, err)
	}
	return result, nil
}

// dockerRemoveImage removes a locally built image, used by the optional
// cleanup step. Missing-image is not treated as an error: the user may
// already have removed it by hand.
func dockerRemoveImage(ctx context.Context, tag string) error {
	_, err := executor.Run(ctx, executor.Request{
		Path: "docker", Args: []string{"rmi", tag}, Timeout: dockerProbeTimeout,
	})
	return err
}
