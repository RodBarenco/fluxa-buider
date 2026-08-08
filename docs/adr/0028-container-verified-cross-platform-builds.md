# ADR 0028: Container-verified cross-platform builds

- Status: Accepted (Windows verification: partially working, known open issue — see Consequences)
- Date: 2026-08-06

## Context

`fluxa-builder build` has always run the produced application for real
(`--fluxa-package-self-test`) before publishing it — a deliberate safety
guarantee, not an artificial restriction: every published artifact was
actually launched and validated. That guarantee was enforced by
`hostCanExecuteTarget`, which hard-failed whenever the build target's
OS/architecture didn't match the host running Fluxa Builder. The
`fluxa-builder init` wizard's `chooseTarget` step already let a user *pick*
a non-host target, but its own doc comment admitted the choice was
discarded — it always built for the host OS regardless of the answer,
because there was nowhere else for a non-host target to be verified. The
project owner flagged this directly: on a Linux machine, Fluxa Builder
would not build a Windows variant, even though it should — "o ideal é
sempre construir em container" (the ideal is to always build inside a
container).

ADR 0027 already proved the underlying idea works: Windows can be *built*
from a Linux host entirely inside a pinned Docker container (MinGW
cross-compilation). This ADR extends the same idea to *verifying* a user's
packaged application: a produced Windows `.exe` can be smoke-tested for
real inside a container running Wine, and a produced Linux binary can be
smoke-tested inside a plain Linux container, from any host with Docker —
without that host needing to execute the target binary natively. macOS
stays host-only in both directions, unchanged from ADR 0027's own stance:
Apple does not license macOS for containers, so there is no container path
for it at all, only real Apple hardware.

A second, related requirement came directly from the project owner while
this was being designed: "as vezes fazer o teste pode gerar chaves... é
importante que o arquivo final seja algo zerado, e teoricamente que não
envie mensagens para um site" (sometimes running the test can generate
keys; the final artifact needs to end up clean, and the test must not be
able to reach out to a site). Investigating that turned up a real,
pre-existing gap, independent of anything containerized: the smoke test
already ran *in place*, directly inside the exact staged directory that
gets archived and published moments later. Anything a self-test happened
to write on first run — a generated key, a config file, a save slot —
would already have been baked into the shipped artifact, even without any
container involved at all.

A third requirement, also from the project owner, shaped the container
resource footprint: containers must run with an explicit, bounded memory
limit rather than Docker's unbounded default, since an unbounded container
can contend for a host's entire RAM budget alongside everything else
running there.

## Decision

### Isolated smoke testing, for every build (`internal/portable/smoke.go`)

`SmokeExecutable` now always runs the self-test against a disposable copy
of its `directory` argument, never `directory` itself. `isolateForSmoke`
creates that copy as a sibling of `directory` (guaranteeing the same
filesystem — needed for the fast path below, and a bonus safety net: if
the returned `cleanup()` somehow never runs, the copy still lives under
the workspace's own tree and is swept up by its eventual full cleanup).
Every entry is hard-linked when possible — `directory` can contain a
multi-hundred-MB `.flxpkg`, and only files the self-test itself creates
need a distinct inode — falling back to a real, permission-preserving copy
only when hard-linking fails (crossing a filesystem boundary). Symlinks
(macOS `.app` bundles can contain these) are always recreated as symlinks,
never linked or followed. This applies transparently to every existing
caller, native or not: no public signature changed.

The validation half of the old `SmokeExecutable` — interpreting a
finished process execution into a validated `SmokeReport` (protocol,
package hash, VM-compatibility, UI-not-opened) — is now its own exported
`ValidateSmokeExecution(execution executor.Result, runErr error,
executablePath, expectedPackageHash string) (SmokeReport, error)`, so
there is exactly one implementation of the self-test contract shared by
every execution mechanism.

### Container-based verification (`internal/containersmoke`, new package)

`internal/portable` gains a `ContainerRunner` function type — "execute
`executable` inside `directory`, return the same bounded-output shape
`internal/executor` already produces" — and `SmokeContainer` /
`SmokeContainerDetailed` / `SmokeExecutableContainer`, the containerized
counterparts of `Smoke`/`SmokeDetailed`/`SmokeExecutable`: same isolation,
same `ValidateSmokeExecution`, only *what* runs the process differs. This
keeps `internal/portable` free of any Docker or network import at all,
consistent with ADR 0027's own principle that this package "stays a pure,
no-network function" — the caller supplies how to run.

The new `internal/containersmoke` package supplies two such runners,
mirroring `internal/toolchainbuild`/`internal/mesafallback`'s existing
shape (own `docker.go`, own embedded Dockerfile, own error kind):

- **`RunLinux`** — a pinned base image, resolved and pinned by digest
  exactly like every other pinned dependency in this project
  (`debian@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241`,
  the real `bookworm-slim` digest at the time this was written). No custom
  Dockerfile needed — a produced Linux binary is already native to it.
  **This path works end-to-end, verified for real**, including that
  `--network none` genuinely blocks DNS resolution
  (`TestRunLinuxExecutesRealBinaryNetworkIsolated`).
- **`RunWindows`** — a pinned Debian image with Wine 11.0 installed from
  WineHQ's own apt repo (all four related sub-packages —
  `winehq-stable`, `wine-stable`, `wine-stable-amd64`,
  `wine-stable-i386` — pinned to the identical exact version together;
  apt does not cascade a version pin through its own dependencies, and
  mismatched versions fail outright), plus Xvfb for headless execution.
  **This path is partially working: `wineboot --init` succeeds
  reliably; actually running a program via the generic `wine <program>`
  launcher does not, for a reason not identified despite extensive
  investigation.** See "Windows verification: what works, what doesn't,
  and what was tried" below for the full account — this is deliberately
  detailed, since the project owner asked for it to be preserved for a
  future attempt.

Every container run — Wine or plain Linux — uses `docker run --rm
--network none --user <host-uid>:<host-gid> --memory <N>m --memory-swap
<N>m -v <directory>:/work -w /work`. `--network none` is what makes the
project owner's "must not phone home" requirement a real guarantee rather
than a hope: the self-test process has no network device at all, not
merely an unconfigured one. `--memory`/`--memory-swap` (both set to the
same value, so a container can never spill into extra swap instead of
failing cleanly) answer the project owner's resource-boundedness
requirement — `internal/containersmoke/limits.go` defaults to 4096MB for
Wine and 2048MB for the plain Linux runner (Wine needing more headroom is
a real, tested minimum from this investigation, not a guess), each
overridable via `FLUXA_BUILDER_WINDOWS_CONTAINER_MEMORY_MB` /
`FLUXA_BUILDER_LINUX_CONTAINER_MEMORY_MB`. Running as the host's own uid
rather than the container's default root matches ADR 0027's own reasoning
(a bind mount shares the host filesystem directly, so a root-owned write
would be a problem) and needed one more fix specific to Wine: an arbitrary
host uid has no `/etc/passwd` entry inside the image and therefore no
`$HOME`, which confuses Wine. `dockerRunIsolated` sets `-e HOME=/tmp`
unconditionally to route around this — harmless for the plain Linux
runner too.

`internal/app.resolveSmokeStrategy(targetOS, targetArch)` decides, per
build, whether verification is `smokeNative` (host already matches — the
only path that existed before this ADR), `smokeWineContainer`,
`smokeLinuxContainer`, or `smokeUnsupported` (macOS cross-building, or an
architecture combination this project doesn't otherwise support). Both of
`runBuild`'s smoke-test call sites (the `--embed` path and the portable
path) now switch on this instead of hard-failing on any host/target
mismatch; the container path is wrapped with the same spinner already
introduced for Docker-based toolchain acquisition and the Mesa fallback
download, since a first-run image build can take a while with no other
output otherwise.

### Graceful degradation when container verification cannot run at all

A real, deliberate trade-off the project owner chose after this ADR's own
Windows investigation kept hitting genuine, hard-to-diagnose Wine
failures: `internal/app.smokeVerify` treats a `*containersmoke.Error`
(Docker unavailable, an image build failure, or — via
`wine.go`'s `wineInfrastructureFailure` — a recognized Wine-infrastructure
stderr signature) as reason to publish the build anyway, with a
`WARNING:` line, rather than blocking it. This is the same shape as the
existing Mesa3D fallback's own behavior for the same class of failure.
It is a genuine trade-off, not a free win: a Windows artifact published
this way was never actually executed and validated, weakening this
project's own core guarantee for exactly that build. The project owner
chose availability over always requiring verification specifically for
container-infrastructure failures — a *portable.Error* (meaning the
self-test process actually ran inside the container and genuinely failed,
timed out, or crashed) is not covered by this and still fails the build;
only "verification could not even run" degrades gracefully.

### The wizard's target choice now actually takes effect

`chooseTarget` returns a target override string instead of silently
discarding a non-host answer. Making this real required a fix beyond just
this ADR's own scope: `runBuild` reloads the project fresh from disk (it
takes CLI-style arguments, not the wizard's in-memory `*project.Config`),
so an in-memory `cfg.Build.Target` mutation would have been lost the
moment `setupOrBuild` called `runBuild` — exactly the reason `--output`
already exists as a real CLI flag rather than an in-memory field.
`fluxa-builder build` gained a matching `--target <os>-<arch>` flag for
the same reason, and the wizard now threads its target choice through
`setupOrBuild`/`manualGuide`/`autoAcquire` into that flag exactly like
`--output` already threads through as `outputOverride` — including through
the automatic-toolchain-acquisition detour (ADR 0027), which a Linux host
choosing `windows-x64` with no toolchain registered yet will commonly hit
in the very first real use of this feature.

macOS non-host and "all three platforms in one run" keep an honest
message instead of silently building the host target anyway: macOS
cross-building has no container path (documented above), and building all
three in a single wizard run is simply not implemented yet.

## Windows verification: what works, what doesn't, and what was tried

This section exists specifically so a future attempt at the open problem
below doesn't have to re-derive any of this from scratch.

**What was fixed, and is real, tested, and working:**

1. **`xauth` missing.** `xvfb-run` needs the `xauth` package to create its
   per-session `Xauthority` file; without it, `xvfb-run` fails immediately
   with `xauth command not found`. Fixed by adding `xauth` to the image's
   package list.
2. **Docker's default seccomp profile.** Without `--security-opt
   seccomp=unconfined`, Wine failed immediately with `could not load
   kernel32.dll, status c0000135` even on a brand-new prefix — a known
   class of issue for Wine under a restricted syscall filter, not
   specific to this project. Fixed by relaxing seccomp for the Wine image
   specifically (`wineRunArgs` in `wine.go`); the plain Linux runner needs
   no such relaxation and keeps the default profile.
3. **Missing `/tmp/.X11-unix`.** X11 clients (Xvfb included) expect this
   directory to already exist with the standard world-writable-plus-
   sticky-bit mode a real desktop's own init/X infrastructure normally
   creates ahead of time — a minimal container image has none of that.
   Under the arbitrary non-root uid every container in this package runs
   as, Xvfb was silently failing to even start
   (`_XSERVTransmkdir: ERROR: euid != 0, directory /tmp/.X11-unix will
   not be created` — visible only via `xvfb-run --error-file`, since
   `xvfb-run`'s own default behavior discards Xvfb's stderr entirely), so
   Wine had no display to attach to and failed deep inside its own module
   loader with a message that never mentioned X11 or Xvfb at all. Fixed
   by pre-creating `/tmp/.X11-unix` with mode `1777` as root at image
   build time (`wine.Dockerfile`). This is what made `wineboot --init`
   start succeeding reliably.
4. **WINEPREFIX ownership.** Wine refuses to use a prefix it does not
   itself own (`wine: '/wineprefix' is not owned by you`). Any approach
   that pre-creates or bakes the prefix into the image — a plain
   Dockerfile `RUN` step, `docker commit` after priming as a different
   uid, even a pre-created empty directory with permissive `chmod` bits —
   leaves it owned by whichever uid touched it at build/commit time, not
   the arbitrary host uid `dockerRunIsolated` actually runs as. Fixed by
   never baking WINEPREFIX into the image at all: `wine.go`'s
   `ensureWineprefixInitialized` bind-mounts a persistent, host-owned
   directory (under `~/.fluxa-builder/containersmoke/`) there instead,
   for both priming and every real run — a bind mount's ownership is
   simply whatever the host-side directory's owner already is.
5. **Stale prefix reused across a different Wine build.** A WINEPREFIX
   initialized by one Wine image and reused by a different one (this
   project cycled through several pinned Wine versions while diagnosing
   the open problem below) fails in exactly the same
   `could not load kernel32.dll` way: `wineboot --init` still silently
   "succeeds" and writes a `system.reg`, but any later real invocation
   against that now-mismatched prefix then fails. Fixed by keying the
   cached prefix directory's name by the exact Docker image ID
   (`dockerImageID`, `shortImageID`), not a version string that could be
   forgotten to bump — any image change (a version bump, a Dockerfile
   edit, anything) gets a fresh prefix automatically.
6. **`wineserver -k`/`-w` chained *after* `xvfb-run` deadlocks.**
   `wineboot --init` (and any `wine <program>` run) deliberately leaves
   `explorer.exe`/`services.exe` resident afterward, the same as a real
   Windows session — they never exit on their own. `xvfb-run` itself only
   returns once every client connected to its virtual display
   disconnects, which those resident processes never do on their own. A
   script of the shape `xvfb-run -a wineboot --init; wineserver -k` was
   found to deadlock: `wineserver -k` never actually runs, because
   `xvfb-run` (the thing that would need to return first) never returns.
   Fixed by nesting the kill *inside* the same `xvfb-run`-wrapped shell
   invocation instead: `xvfb-run -a sh -c 'wineboot --init; wineserver -k
   ...'` — confirmed to work reliably by direct process inspection inside
   a running container (`explorer.exe`, `control.exe` etc. actually
   present and then actually gone).
7. **Resource boundedness.** `--memory`/`--memory-swap`, per the Decision
   section above.

**What remains open, unfixed:**

`wineboot --init` — which does not go through Wine's generic `wine
<program>` launcher at all — reliably succeeds against a correctly-primed
prefix. The generic `wine <program>` launcher (used for the actual
self-test run, and for anything beyond `wineboot` itself) *always*
bootstraps through the 32-bit WoW64 `syswow64\start.exe`, regardless of
the target program's own architecture — verbose `WINEDEBUG=+loaddll,
+module` tracing confirms this exactly: `start.exe` loads, `ntdll.dll`
loads (both `builtin`), then `looking for L"kernel32.dll" in (null)`
immediately fails with `status=c0000135`. This reproduces even running a
trivial cross-compiled console program (`argc/argv` check, `printf`,
`return`) that touches no Win32 GUI/windowing API at all, and even
running Wine's own bundled `cmd.exe` — so it is not specific to this
project's own package or self-test protocol.

The following were investigated and **ruled out** as the cause, each with
a real, reproducible test, not a guess:

- WINEPREFIX ownership (fixed independently per above, but the failure
  persists after that fix with a correctly-owned prefix).
- Docker's default seccomp profile (already relaxed, per above).
- Docker's default AppArmor profile (`--security-opt apparmor=unconfined`
  tested directly — no change).
- Missing Linux shared library dependencies (`ldd $(which wine)` and
  `ldd $(which wineserver)` inside the image — no "not found" entries).
- Unset `WINEARCH` (explicitly set to `win64` — no change).
- Host disk/build-cache pressure (reproduced identically after freeing
  68GB of accumulated Docker build cache/images and rebuilding the image
  completely from scratch).
- A Wine version regression (reproduced identically pinning to both 11.0
  and 9.0 — ruling out a recent WoW64 architecture change in Wine itself
  as the cause).
- `--privileged`, `--pid=host`, and `--ipc=host` (tested individually and
  combined — no change; rules out container capability/device
  restrictions, PID namespace isolation, and IPC/shared-memory namespace
  isolation).
- `--network none` specifically (tested with plain `--network` default —
  no change; rules out network namespace/loopback availability).
- The Wine binaries/libraries themselves being read through Docker's
  overlay2 storage driver (tested bind-mounting a copy of `wine-stable`
  extracted straight onto the host filesystem into the container,
  overriding the image's own copy at the same path — no change).
- Missing 32-bit kernel32.dll/ntdll.dll files (both present, in both
  `i386-windows/` and `x86_64-windows/` under `/opt/wine-stable/lib/wine`,
  world-readable, confirmed via direct `find`/`cat`).
- Inconsistent package versions (`winehq-stable`, `wine-stable`,
  `wine-stable-amd64`, `wine-stable-i386` all report the identical pinned
  version via `dpkg -l`).
- `wine`/`wineboot`/`wineserver`/`winecfg` resolving to inconsistent
  installations (`wineboot` and `winecfg` are — correctly, by Wine's own
  multi-call-binary design — symlinks to the same `wine` binary; this is
  expected, not a bug).

**One genuinely informative, unresolved data point:** the exact same Wine
binaries (`wine-stable` 9.0, extracted from this project's own built
image via `docker cp`) run **natively on the host** (no Docker/container
at all — a plain `LD_LIBRARY_PATH`/`WINEPREFIX` invocation) without this
failure: `wineboot --init` and `wine cmd /c echo hello` both succeed. This
strongly suggests something specific to running Wine's 32-bit WoW64
bootstrap *inside a container* (as opposed to Wine itself, or this host's
CPU/kernel in general) is the actual cause — but which specific
container-related factor, after ruling out every one listed above, was
not identified.

**Recommended starting points for a future attempt**, roughly in order of
promise:

1. Compare against a real, working Docker+Wine reference image (e.g.
   `scottyhardy/docker-wine`, `webcomics/wine-docker`) line by line,
   rather than building up from a minimal image — something in how such
   images differ (their `tini`/init handling, their exact package set, an
   `Xvfb` invocation detail) may matter in a way not yet identified here.
2. `strace`/`ltrace` the failing `wine cmd /c ver` invocation directly
   (not available in the minimal image used here; would need adding
   `strace` to the Dockerfile) to see the actual syscalls/file lookups
   `start.exe`'s module loader performs immediately before failing,
   rather than relying on Wine's own `WINEDEBUG` tracing.
3. Try Debian's own native `wine`/`wine32`/`libwine`/`libwine:i386`
   packages instead of WineHQ's `wine-stable`/`wine-stable-i386` — not
   yet tried; this project switched to WineHQ's repo specifically because
   Debian's bundled Wine is "stale/inconsistently available across
   suites," but that tradeoff was never actually re-examined against this
   specific failure.
4. As the project owner independently suggested (and the native-Wine test
   above supports as a real, working alternative): detect a native `wine`
   already installed on the host `PATH` and prefer it over the
   container path when present, asking the user which to use in the
   wizard (native: faster, no ~2.4GB image download, but requires Wine
   pre-installed; container: always works, self-contained, heavier).
   Deliberately **not implemented in this pass** — noted here as a
   concrete, validated-as-working future direction, not a hypothetical
   one.

## Consequences

A Linux host can now build and publish a real, verified Linux-hosted
Windows artifact, and a Windows host can do the equivalent for Linux
(`RunLinux`, fully working) — without Docker's toolchain-acquisition path
(ADR 0027) being a prerequisite; the two features are independent. Docker
becomes a second, separate reason a build can now depend on it, on top of
automatic toolchain acquisition and the Windows Mesa fallback.

Every build, not only cross-target ones, gained a real correctness fix:
the self-test can no longer leave any trace in the artifact that actually
gets archived and published, verified directly by
`TestSmokeNeverLeavesSelfTestSideEffectsInTheOriginalDirectory`. The
accepted cost is a bit more build-time I/O when hard-linking isn't
possible (crossing a filesystem boundary) — the common case, hard-linking
a same-filesystem copy, is effectively free.

**Windows container verification does not currently work reliably in this
project's own development environment** — see the dedicated section
above. This is not hidden or silently assumed: `resolveSmokeStrategy`
still routes a Windows cross-target build through `RunWindows` (the
architecture is correct and `RunLinux`'s equivalent path is proven
working), but a real user hitting the same unresolved failure will see
their build publish with a `WARNING:` instead of being blocked, per the
graceful-degradation trade-off above — never a silent, unverified success
and never a hard block over infrastructure outside this project's
control. `TestRunWindowsExecutesRealPEBinary` is left in place, failing,
specifically so a future fix has an immediate, unambiguous pass/fail
signal.

What is explicitly **not** verified by anything in this pass, and is not
hidden: `RunLinux`'s minimal base image can have a different dynamic-
linking environment than a real end-user machine (the same class of risk
that already exists for native smoke testing on a Linux distribution
different from the one that produced the build — not a new risk this ADR
introduces, just one now also reachable via a second path). Cross-
architecture container execution (an arm64 host running an amd64-only
Windows/Linux target) depends entirely on the host's own Docker
installation supporting emulation (binfmt_misc) — this project makes no
attempt to set that up itself, the same best-effort stance already taken
toward Docker availability in general. macOS cross-building remains an
explicit, undone gap, not a silently assumed one, matching ADR 0027's own
honesty about the same platform.
