# ADR 0027: Automatic toolchain/runtime acquisition and Windows Mesa3D fallback

- Status: Accepted
- Date: 2026-08-04

## Context

`fluxa-builder init`'s step 5 asks whether to download and build the Fluxa
toolchain automatically, but until now answering "yes" only explained that
this was not implemented and fell back to a manual guide (cloning
`fluxa-lang`, running `make build` by hand, and finishing a partially-filled
`runtime.json` template). The project owner asked for this to actually be
implemented — "o builder tem que funcionar" (the Builder has to work) even
when a user's machine is missing `make`, a compiler, or the native libraries
`fluxa-lang` needs.

The first design considered installing missing build dependencies directly
onto the host via `apt`/`pacman`/`brew`. The project owner's own objection to
that approach was the right one: a user's already-installed library
*versions* can differ subtly from machine to machine, undermining the
portable pipeline's deterministic, reproducible-artifact goal (ADR 0010).
Building inside a pinned Docker container instead removes that class of
problem entirely, and — a direct side benefit — turns Windows from "coded
against upstream docs, never actually run" into something buildable and
structurally verifiable on a Linux machine via MinGW cross-compilation,
which real testing during this work exploited immediately.

## Decision

### `internal/toolchainbuild`: Docker-based acquisition

`Acquire(ctx, Request, Confirmer) (Result, error)` clones
`https://github.com/RodBarenco/fluxa-lang` into a persistent host cache
(`~/.fluxa-builder/toolchain-src`, mirroring the existing runtime registry's
own persistence) and builds it inside a pinned image, confirming every
consequential step (building/running a container, later removing the cache)
through the wizard's existing `askYesNo`. Every container invocation runs as
`--user <uid>:<gid>` (the invoking host user, not the container's default
root) — a bind mount shares the host filesystem directly rather than
translating ownership, so a root-owned write would otherwise leave the host
cache undeletable by its own owner without `sudo`.

**Linux**: `make build` in the checkout, inside a Debian-based image with
the native library dev packages fluxa-lang's own `fluxa.libs` documents
needing (`libsqlite3-dev libsodium-dev libcurl4-openssl-dev zlib1g-dev
libserialport-dev`, `libpq-dev`/`libmicrohttpd-dev` deliberately omitted —
both gracefully stub when absent, per fluxa-lang's own documented
behavior — `libmosquitto-dev` omitted because Ubuntu ships no static
archive for it at all). Raylib and miniaudio have no Debian packages, so
raylib is built from the exact commit fluxa-lang's own Windows build script
already pins, and miniaudio's single vendored header is fetched at a pinned
release tag; both are baked into the image, not the per-project checkout.
The resulting `./fluxa` serves as both the registered toolchain and the
runtime source Fluxa Builder's own relay (ADR 0025) wraps — Linux has no
separate packaged build.

**Windows**: real MinGW cross-compilation from this Linux host, not a
Windows machine — the real `fluxa-lang` Makefile already supports this
(`CC_WINDOWS ?= x86_64-w64-mingw32-gcc`, `WINDOWS_DEPS_PREFIX ?=
/usr/x86_64-w64-mingw32` in its non-Windows-host branch). The image is
Fedora-based specifically for its MinGW packaging project's static
archives (`mingw64-sqlite-static`, `mingw64-curl-static`,
`mingw64-zlib-static`, `mingw64-openssl-static`, `mingw64-libssh2-static`,
`mingw64-libidn2-static` — confirmed to exist; the plain, non-`-static`
mingw64 packages only ship DLL import libraries, `libX.dll.a`, not `libX.a`,
which a real build attempt here failed to link against). libsodium has no
Fedora static package either and is cross-compiled from its own pinned
upstream release via a standard autotools `--host=x86_64-w64-mingw32`
build. Two more real, non-obvious fixes real build attempts here found and
document in `docker/windows.Dockerfile`'s own comments:

- raylib's own build script assumes a real MSYS2 MinGW64 shell, where
  plain `gcc`/`uname` already resolve to Windows; on genuine Linux neither
  holds, and raylib's Makefile sets `CC`/`PLATFORM_OS` with plain `=`, not
  `?=`, so environment variables are silently ignored — `MAKEFLAGS` is the
  one channel with command-line-argument precedence that reaches both.
- `libcurl.pc`/`libcrypto.pc`'s plain `Libs:` line (the only one
  fluxa-lang's own lib.mk reads, via plain, non-`--static`
  `pkg-config --libs`) omits their own static transitive dependencies,
  which live only in `Libs.private:` — merged into `Libs:` directly, the
  same fix already needed for raylib's own `.pc` file.

Both static targets (`build-windows-essential-static` → the toolchain,
`build-windows-packaged` → the registered runtime) run in sequence, each
copied out before the other overwrites the same `fluxa-runtime.exe`
filename — this two-build requirement is what the project owner meant by
"os 2 runtimes que precisa." Both resulting PE binaries are validated
structurally with the existing `internal/windows.ValidatePEAMD64` before
being trusted.

**A third binary — the host's own compiler — is built too**, from that
same checkout, whenever the host is not itself Windows. Without it the
whole Windows flow was unusable from a Linux host, which real end-to-end
testing confirmed after the fact: `build` resolves and *probes* a
host-native toolchain and records its SHA-256 in the package manifest,
and `internal/runtime` then requires the registered runtime's own
`toolchain_sha256` to match it exactly. Two cross-compiled PEs cannot
satisfy that under any arrangement — a Linux host can neither execute
`fluxa-toolchain.exe` to compile the project nor probe it for an
identity, and hashing it unexecuted (the original behavior) registers a
runtime whose recorded toolchain is, by construction, a different file
from the one the manifest will name. Every Windows build after a
"successful" acquisition therefore failed with
`no verified runtime matches windows/amd64`. Running fluxa-lang's plain
native `make build` in the already-pinned Linux image closes the gap from
the one download that was already happening: `Result.HostToolchainPath`
carries it, `init` probes and persists it as `fluxa.toml`'s `[toolchain]`
path, and the registered Windows runtime records *that* identity. A macOS
host gets an empty `HostToolchainPath` rather than an error — its native
path is still on hold, and the manual guide remains its fallback.

Ordering is deliberate, and the first real run of this corrected flow is
what settled it: both Docker *images* are prepared up front, before any
compilation, while both *compilations* run afterwards with the Windows
one first. Image building is the network-heavy and so flakiest part —
the Linux image's raylib `git clone` failed transiently on that run and
took an already-finished 14-minute Windows cross-compile down with it —
while running the host toolchain's own `make build` last keeps the
Windows cross-compile looking at a checkout no other build has touched,
exactly as it did before this fix.

Running both against one checkout is safe because fluxa-lang's `build`
and `build-windows-profile` targets each compile every source in a single
compiler invocation and write no intermediate object files
(`$(CC) $(CFLAGS) $(SRCS) -o $(TARGET)`), so they share nothing but the
source tree. A project's `fluxa.libs` is validated against fluxa-lang's
fixed Windows essential-profile library set first (unlike Linux, which has
no such restriction — this fixed set is specific to Windows's two curated
static targets) and fails clearly, naming the unsupported library, rather
than attempting an incomplete build.

**macOS is on hold**, not attempted in this pass: Docker cannot help at
all (Apple does not license macOS for containers, and there is no
realistic cross-compilation path from Linux), so it would need its own
native `brew`-based path, only reachable when Fluxa Builder itself runs on
real macOS hardware. `fluxa-builder init` keeps today's existing manual
guide unchanged for macOS.

Registration reuses existing code rather than reimplementing it:
`toolchain.Probe` for the built toolchain's `Identity`, `runtime.Add` for
the runtime, with a fully computed `runtime.Metadata` — what the manual
guide previously left half-filled for the user to finish by hand.

### Windows Mesa3D software-rendering fallback

Separately, real end-to-end testing of a produced Windows build surfaced a
second, related gap: `std.graph` needs a working OpenGL driver, which is
often missing inside VMs. fluxa-lang's own `docs/WINDOWS.md` documents a
validated fix — bundling four files from the community
[`mesa-dist-win`](https://github.com/pal1000/mesa-dist-win) project
(`dxil.dll`, `libgallium_wgl.dll`, `opengl32.dll`, and `opengl32sw.dll`, a
second copy of `opengl32.dll` under a name that requests the
software-rendering path) plus a `<executable>.local` marker enabling
per-application DLL redirection — and says explicitly that "Fluxa Builder
must explicitly include these companion files." Per the project owner's
direction this is bundled **always** for every Windows build, not only as
an opt-in VM-compatibility mode.

The upstream archive is published only as `.7z`, with no `.zip`
alternative. Rather than add a third-party `.7z`-extraction Go dependency
(this project has exactly one external Go dependency today,
`BurntSushi/toml`) or require `7z` on the host running Fluxa Builder, a new
`internal/mesafallback` package downloads the pinned, checksum-verified
release directly (plain `net/http`, no new Go dependency) and extracts it
inside a minimal, pinned Debian+`p7zip-full` container — matching this
ADR's own "don't add a new host dependency, use a container instead"
reasoning. The four files are cached flat under
`~/.fluxa-builder/mesa-dist-win`, keyed only by the pinned version
constants, so every future Windows build across every project reuses the
same one-time download and extraction.

`internal/portable.Request` gained an optional `WindowsMesaFallbackDir`
field: `internal/portable.Build` stays a pure, no-network function — it
only copies from an already-populated local directory, exactly like
`WindowsIcon`/`LinuxIcon` already work. `internal/app`'s `runBuild` is
responsible for calling `mesafallback.EnsureCached` before a Windows
build and passing the result in; on any failure (Docker unavailable,
network failure, checksum mismatch), it prints a `WARNING:` line and
leaves the field unset, exactly like the existing Windows icon-embedding
graceful-degradation contract (ADR 0026) — Mesa is optional companion
distribution, never a build failure.

## Consequences

The Windows path is now genuinely testable on this Linux machine, not
purely theoretical: `internal/toolchainbuild/windows_integration_test.go`
cross-compiles both real static targets, structurally validates the
resulting PE binaries, and probes the host toolchain built beside them.
What remains explicitly unverified, and is not hidden — actually
launching or smoke-testing either `.exe`, which needs real Windows or
Wine, consistent with this project's existing `hostCanExecuteTarget`
gating elsewhere.

`Acquire` is now injected into `internal/app` through `buildDependencies`
rather than called directly, because the defect above lived entirely in
what the wizard *did* with a `Result` — ordinary logic that a
25-minute opt-in container test is the wrong instrument to guard.
`TestRunInitCrossAcquireRegistersHostToolchainIdentity` covers it in
milliseconds, with a fake probe that refuses any path but the host
toolchain, standing in for the real "exec format error" a Linux host
gives a PE.

A Windows target acquired from a Linux host now costs three fluxa-lang
compilations rather than two, and `~/.fluxa-builder/toolchain-built/
windows/` holds a `fluxa-host-toolchain` ELF beside the two `.exe`
files. macOS automatic acquisition
remains unimplemented, an explicit gap rather than a silently assumed one.

The `~/.fluxa-builder` cache directory gains two new persistent
subdirectories beyond the existing `runtimes/`: `toolchain-src/` (the
cloned `fluxa-lang` checkout and, when built, `toolchain-built/<os>/`
holding the produced binaries) and `mesa-dist-win/` (the cached fallback
DLLs). Both are safe to delete at any time — everything under them is
either re-clonable or re-downloadable and re-verified by hash on next use.

Docker is now a real, if optional, dependency: required for automatic
toolchain acquisition (only invoked when a user opts into it in
`fluxa-builder init`) and for the Windows Mesa3D fallback (invoked
automatically for every Windows build, but only on a soft-failure path if
missing — the build still proceeds without the fallback bundled). Neither
path requires Docker for Linux/macOS builds that don't use automatic
acquisition, nor for Windows builds where the Mesa cache is already
populated from a previous run.
