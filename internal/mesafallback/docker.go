package mesafallback

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/RodBarenco/fluxa-builder/internal/executor"
)

const (
	dockerBuildTimeout = 5 * time.Minute
	dockerRunTimeout   = 5 * time.Minute
	dockerProbeTimeout = 5 * time.Second
	extractImageTag    = "fluxa-builder-mesa-extract"
)

// dockerAvailable reports whether the docker CLI is present and the
// daemon is reachable, without ever calling exec directly —
// internal/executor is this project's one permitted os/exec call site
// (ADR 0005).
func dockerAvailable(ctx context.Context) bool {
	_, err := executor.Run(ctx, executor.Request{
		Path: "docker", Args: []string{"info"}, Timeout: dockerProbeTimeout,
	})
	return err == nil
}

func dockerBuildExtractImage(ctx context.Context, dockerfilePath, contextDir string) error {
	_, err := executor.Run(ctx, executor.Request{
		Path:      "docker",
		Args:      []string{"build", "-f", dockerfilePath, "-t", extractImageTag, contextDir},
		Timeout:   dockerBuildTimeout,
		MaxStdout: 4 * 1024 * 1024,
		MaxStderr: 4 * 1024 * 1024,
	})
	if err != nil {
		return newError(ErrorUnsupported, "build Mesa extraction image", extractImageTag, err)
	}
	return nil
}

// extractArchive runs `7z x` inside the pinned container — p7zip lives
// only in the container, never required on the host running
// fluxa-builder — extracting archivePath (inside dataDir) into dataDir's
// "out" subdirectory. Both directories are the same bind mount so the
// archive is visible to read and the output is visible to write.
func extractArchive(ctx context.Context, dataDir, archiveName string) error {
	uid, gid := os.Getuid(), os.Getgid()
	result, err := executor.Run(ctx, executor.Request{
		Path: "docker",
		Args: []string{
			"run", "--rm",
			"--user", fmt.Sprintf("%d:%d", uid, gid),
			"-v", dataDir + ":/data",
			extractImageTag,
			"7z", "x", "/data/" + archiveName, "-o/data/out",
		},
		Timeout:   dockerRunTimeout,
		MaxStdout: 4 * 1024 * 1024,
		MaxStderr: 4 * 1024 * 1024,
	})
	if err != nil {
		return newError(ErrorUnsupported, "extract Mesa archive", result.Stderr, err)
	}
	return nil
}
