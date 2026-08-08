package toolchainbuild

import (
	"context"
	_ "embed"
	"os"
	"path/filepath"
	"runtime"
)

//go:embed docker/linux.Dockerfile
var linuxDockerfile []byte

const linuxImageTag = "fluxa-builder-toolchain-linux"

// acquireLinux clones fluxa-lang and builds it inside the pinned Linux
// image with `make build FLUXA_GRAPH_RAYLIB=1 FLUXA_SOUND_MINIAUDIO=1`,
// returning the resulting ./fluxa binary as both the toolchain and the
// runtime: Linux's native entrypoint has no separate packaged build,
// unlike Windows (ADR 0025 already covers how Fluxa Builder itself
// supplies the missing packaged-runtime relay for Linux at
// portable-assembly time).
//
// Unlike Windows, Linux's `make build` has no fixed library profile to
// validate against — it compiles whatever the project's fluxa.libs
// actually declares, and the image installs the full set of native
// library dependencies fluxa-lang's own stdlib modules document (see
// docker/linux.Dockerfile) so that any reasonable combination builds
// without per-project image customization.
func acquireLinux(ctx context.Context, request Request, confirm Confirmer) (Result, error) {
	cacheDir := sourceCacheDir(request.CacheRoot)
	if err := ensureLinuxImage(ctx, confirm, "Build the Linux toolchain image (first run only; cached afterward)"); err != nil {
		return Result{}, err
	}

	if err := confirmOrDecline(confirm, "Download fluxa-lang and build it inside the container"); err != nil {
		return Result{}, err
	}
	if err := ensureSource(ctx, cacheDir); err != nil {
		return Result{}, err
	}

	if err := os.MkdirAll(request.OutputDir, 0o700); err != nil { // #nosec G301 -- host-owned build output directory.
		return Result{}, newError(ErrorIO, "create output directory", request.OutputDir, err)
	}

	binaryPath, err := runLinuxNativeBuild(ctx, cacheDir, request.OutputDir, "fluxa")
	if err != nil {
		return Result{}, err
	}

	result := Result{
		ToolchainPath:  binaryPath,
		RuntimePath:    binaryPath,
		SourceCacheDir: cacheDir,
		ImageTags:      []string{linuxImageTag},
	}
	// An ELF binary only executes on a Linux host; building a Linux target
	// from Windows or macOS leaves the caller without a usable local
	// compiler exactly as a Windows target does — see Result's own
	// HostToolchainPath.
	if runtime.GOOS == "linux" {
		result.HostToolchainPath = binaryPath
	}
	return result, nil
}

// ensureLinuxImage builds linuxImageTag behind a confirmation worded for
// whichever flow needs it: acquireLinux builds it as the target's own
// toolchain image, while acquireWindows builds the same image only to
// produce the host-executable compiler a cross-compiled Windows PE cannot
// be.
func ensureLinuxImage(ctx context.Context, confirm Confirmer, action string) error {
	dockerfilePath, contextDir, err := writeEmbeddedDockerfile(linuxDockerfile, "linux.Dockerfile")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(contextDir) }()

	if err := confirmOrDecline(confirm, action); err != nil {
		return err
	}
	if _, err := dockerBuildImage(ctx, dockerfilePath, contextDir, linuxImageTag); err != nil {
		return err
	}
	return nil
}

// runLinuxNativeBuild runs fluxa-lang's plain native `make build` against
// an already-prepared checkout at cacheDir, copying the resulting ./fluxa
// out as outputDir/binaryName. binaryName is caller-controlled only from
// this package's own fixed constants, never from user input.
//
// fluxa-lang's `build` target compiles every source in a single compiler
// invocation and writes no intermediate object files (confirmed in its own
// Makefile: `$(CC) $(CFLAGS) $(SRCS) -o $(TARGET)`), and so shares nothing
// with the Windows profile targets beyond the checkout itself — which is
// what makes running both against one checkout safe.
func runLinuxNativeBuild(ctx context.Context, cacheDir, outputDir, binaryName string) (string, error) {
	mounts := []mount{
		{host: cacheDir, container: "/src"},
		{host: outputDir, container: "/out"},
	}
	buildScript := "mkdir -p vendor && cp /opt/fluxa-vendor/miniaudio.h vendor/miniaudio.h && " +
		"make build FLUXA_GRAPH_RAYLIB=1 FLUXA_SOUND_MINIAUDIO=1 && " +
		"cp fluxa /out/" + binaryName + " && chmod +x /out/" + binaryName
	result, err := dockerRunBuild(ctx, linuxImageTag, mounts, "/src", []string{"sh", "-c", buildScript})
	if err != nil {
		return "", newError(ErrorBuild, "build fluxa-lang", result.Stderr, err)
	}

	binaryPath := filepath.Join(outputDir, binaryName)
	if _, err := os.Stat(binaryPath); err != nil {
		return "", newError(ErrorBuild, "locate built binary", binaryPath, err)
	}
	return binaryPath, nil
}

// writeEmbeddedDockerfile writes an embedded Dockerfile to a fresh
// temporary build context directory, since `docker build` needs a real
// file on disk, not an in-memory byte slice.
func writeEmbeddedDockerfile(content []byte, name string) (dockerfilePath, contextDir string, err error) {
	contextDir, err = os.MkdirTemp("", "fluxa-builder-docker-*")
	if err != nil {
		return "", "", newError(ErrorIO, "create Docker build context", "", err)
	}
	dockerfilePath = filepath.Join(contextDir, name)
	if err := os.WriteFile(dockerfilePath, content, 0o600); err != nil {
		_ = os.RemoveAll(contextDir)
		return "", "", newError(ErrorIO, "write Dockerfile", dockerfilePath, err)
	}
	return dockerfilePath, contextDir, nil
}
