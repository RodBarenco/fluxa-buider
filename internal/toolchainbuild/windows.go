package toolchainbuild

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	windowspkg "github.com/RodBarenco/fluxa-builder/internal/windows"
)

//go:embed docker/windows.Dockerfile
var windowsDockerfile []byte

const windowsImageTag = "fluxa-builder-toolchain-windows"

// fedoraMingwSysroot mirrors docker/windows.Dockerfile's own
// FEDORA_MINGW_SYSROOT: the Fedora MinGW packaging project's documented
// install layout for its mingw64-* cross-compiled libraries, which
// differs from the /usr/x86_64-w64-mingw32 a Debian-style layout
// (fluxa-lang's own WINDOWS_DEPS_PREFIX default) would assume.
const fedoraMingwSysroot = "/usr/x86_64-w64-mingw32/sys-root/mingw"

// acquireWindows clones fluxa-lang and cross-compiles it for Windows
// inside the pinned Fedora/MinGW image — real MinGW cross-compilation
// from this Linux host, not a Windows machine, per the Makefile's own
// documented non-Windows-host branch (CC_WINDOWS=x86_64-w64-mingw32-gcc).
// It builds the two distinct static targets fluxa-lang's Windows profile
// needs (see docs/adr/0027-automatic-toolchain-acquisition.md): the
// public standalone runtime (used as the toolchain) and the private
// FLUXA_PACKAGED_RUNTIME=1 build (registered as the runtime) — both emit
// a same-named fluxa-runtime.exe, so each must be copied out before the
// other runs. From a non-Windows host it then builds one more binary from
// that same checkout — a compiler that runs *here* — see
// needsLinuxHostToolchain for why the flow is broken without it. That one
// runs last, so the Windows cross-compile still sees a checkout no other
// build has touched, exactly as before this fix.
func acquireWindows(ctx context.Context, request Request, confirm Confirmer) (Result, error) {
	enabled, err := readEnabledLibs(filepath.Join(request.ProjectRoot, "fluxa.libs"))
	if err != nil {
		return Result{}, err
	}
	if unsupported := unsupportedForWindows(enabled); len(unsupported) > 0 {
		return Result{}, newError(ErrorUnsupported, "validate fluxa.libs",
			fmt.Sprintf("this project needs %v, which fluxa-lang's Windows static profile does not support", unsupported), nil)
	}

	cacheDir := sourceCacheDir(request.CacheRoot)
	dockerfilePath, contextDir, err := writeEmbeddedDockerfile(windowsDockerfile, "windows.Dockerfile")
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = os.RemoveAll(contextDir) }()

	if err := confirmOrDecline(confirm, "Build the Windows cross-compilation image (first run only; cached afterward)"); err != nil {
		return Result{}, err
	}
	if _, err := dockerBuildImage(ctx, dockerfilePath, contextDir, windowsImageTag); err != nil {
		return Result{}, err
	}

	// Both images are prepared up front, before any compilation, because
	// image building is the network-heavy and therefore flakiest part of
	// this whole flow — a real run here had the Linux image's raylib
	// `git clone` fail transiently and discard an already-finished
	// 14-minute Windows cross-compile with it. The host toolchain's own
	// `make build` still runs last (see below), so this reordering costs
	// the Windows build nothing.
	needsHostToolchain := needsLinuxHostToolchain()
	if needsHostToolchain {
		if err := ensureLinuxImage(ctx, confirm,
			"Build the Linux toolchain image, needed to compile this project on this machine (a cross-compiled .exe cannot)"); err != nil {
			return Result{}, err
		}
	}

	if err := confirmOrDecline(confirm, "Download fluxa-lang and cross-compile it for Windows inside the container"); err != nil {
		return Result{}, err
	}
	if err := ensureSource(ctx, cacheDir); err != nil {
		return Result{}, err
	}

	if err := os.MkdirAll(request.OutputDir, 0o700); err != nil { // #nosec G301 -- host-owned build output directory.
		return Result{}, newError(ErrorIO, "create output directory", request.OutputDir, err)
	}

	mounts := []mount{
		{host: cacheDir, container: "/src"},
		{host: request.OutputDir, container: "/out"},
	}
	prefixArgs := fmt.Sprintf(
		"WINDOWS_SQLITE_PREFIX=%[1]s WINDOWS_SODIUM_PREFIX=%[1]s WINDOWS_CURL_PREFIX=%[1]s WINDOWS_ZLIB_PREFIX=%[1]s",
		fedoraMingwSysroot,
	)
	// build-raylib-static.sh invokes raylib's own Makefile assuming it
	// runs inside a real MSYS2 MinGW64 shell, where plain `gcc` already
	// targets Windows and raylib's own uname-based PLATFORM_OS detection
	// sees Windows-like output. Neither holds on a genuine Linux host,
	// and raylib's Makefile sets both `CC = gcc` and `PLATFORM_OS = ...`
	// (from `uname -s`) with plain `=`, not `?=` — meaning environment
	// variables for either are silently ignored (confirmed by a real
	// build attempt here: exporting CC alone still compiled with native
	// gcc and picked the Linux/X11 GLFW backend). MAKEFLAGS is the one
	// channel that reaches both with command-line-argument precedence,
	// the only origin a plain `=` reassignment cannot override — scoped
	// to a subshell so it does not also leak into fluxa-lang's own,
	// already-correct non-Windows-host branch in the make invocations
	// that follow.
	buildScript := "(export MAKEFLAGS='CC=x86_64-w64-mingw32-gcc PLATFORM_OS=WINDOWS'; " +
		"bash platform/windows/build-raylib-static.sh) && " +
		"make build-windows-essential-static " + prefixArgs + " && " +
		"cp fluxa-runtime.exe /out/fluxa-toolchain.exe && " +
		"make build-windows-packaged " + prefixArgs + " && " +
		"cp fluxa-runtime.exe /out/fluxa-runtime.exe"
	buildResult, err := dockerRunBuild(ctx, windowsImageTag, mounts, "/src", []string{"sh", "-c", buildScript})
	if err != nil {
		return Result{}, newError(ErrorBuild, "build fluxa-lang for Windows", buildResult.Stderr, err)
	}

	toolchainPath := filepath.Join(request.OutputDir, "fluxa-toolchain.exe")
	runtimePath := filepath.Join(request.OutputDir, "fluxa-runtime.exe")
	for _, path := range []string{toolchainPath, runtimePath} {
		if err := windowspkg.ValidatePEAMD64(path); err != nil {
			return Result{}, newError(ErrorBuild, "validate built Windows binary", path, err)
		}
	}

	acquired := Result{
		ToolchainPath:  toolchainPath,
		RuntimePath:    runtimePath,
		SourceCacheDir: cacheDir,
		ImageTags:      []string{windowsImageTag},
	}
	if !needsHostToolchain {
		// A Windows host already has its native compiler:
		// build-windows-essential-static's own output is exactly that.
		// A macOS host gets nothing — Docker only ever yields a Linux ELF
		// and fluxa-lang's native macOS build is still on hold
		// (docs/adr/0027) — so HostToolchainPath stays empty rather than
		// failing: the two Windows binaries above are genuinely built and
		// valid, and the wizard still has its manual-guide fallback.
		if runtime.GOOS == "windows" {
			acquired.HostToolchainPath = toolchainPath
		}
		return acquired, nil
	}

	hostToolchainPath, err := runLinuxNativeBuild(ctx, cacheDir, request.OutputDir, hostToolchainName)
	if err != nil {
		return Result{}, err
	}
	acquired.HostToolchainPath = hostToolchainPath
	acquired.ImageTags = append(acquired.ImageTags, linuxImageTag)
	return acquired, nil
}

// hostToolchainName is the host-executable compiler acquireWindows builds
// beside the two Windows PEs. It deliberately does not collide with
// acquireLinux's own "fluxa": that one is a *linux* target's registered
// runtime, while this one only ever serves as this host's local compiler,
// and both can land in the same ~/.fluxa-builder/toolchain-built tree.
const hostToolchainName = "fluxa-host-toolchain"

// needsLinuxHostToolchain reports whether a Windows acquisition must build
// one more binary — a compiler that runs on *this* machine, which neither
// of the two Windows PEs can be from here.
//
// This is the gap that made the whole Windows flow unusable from a Linux
// host: `build` resolves and probes a host-native toolchain and records
// its SHA-256 in the package manifest, and internal/runtime then requires
// the registered runtime's own ToolchainSHA256 to match it exactly. With
// only cross-compiled PEs to offer, init could record nothing that would
// ever match, and every subsequent Windows build failed with "no verified
// runtime matches windows/amd64" — reproduced end to end here before this
// fix. Building the native compiler from the same already-cloned
// fluxa-lang checkout is what closes it: one download, both halves.
func needsLinuxHostToolchain() bool {
	return runtime.GOOS == "linux"
}
