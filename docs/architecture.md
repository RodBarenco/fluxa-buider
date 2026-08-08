# Architecture

Fluxa Builder is a Go command-line application separated from the Fluxa
language repository and runtime.

The Builder is responsible for project validation, toolchain orchestration,
file collection, package construction, runtime selection, target formatting,
verification, and build reporting. Parsing, resolving, executing, and compiling
Fluxa code remain responsibilities of the official Fluxa toolchain.

The implemented source-package pipeline is:

```text
config → validation → toolchain → collection/source staging
       → manifest → FLXPKG → runtime selection → launcher assembly
       → self-test → archive/installer → atomic publication
```

Builds use an isolated workspace and publish final artifacts atomically.

## Interactive setup layer

`fluxa-builder init` (`internal/app/init.go`) is a thin interactive layer in
front of this pipeline, not a second implementation of it: once a toolchain
and a usable registered runtime are found, it constructs the equivalent
`build` arguments and calls the same `runBuild` entry point used by the
non-interactive CLI. Its own independent logic is guidance — detecting the
host, walking the user through required and optional `fluxa.toml` fields,
and, when nothing is ready yet, either automatically acquiring a
toolchain/runtime (Linux and Windows, via `internal/toolchainbuild` — see
ADR 0027) or printing manual setup steps (macOS, and any Linux/Windows
condition automatic acquisition does not support).

Two ordering and precedence rules make the wizard's answers binding rather
than advisory, and both exist because breaking them produced builds that
contradicted what the wizard had printed:

- The target is chosen **before** any per-platform question, because every
  build reads exactly one platform's icon/bundle metadata — the one it is
  producing. Asking about the host's platform on a cross-target run
  collected settings that build could not use and never asked for the ones
  it needed.
- The menu answer is always passed through as an explicit `--target`,
  including the `host` sentinel for "this machine". Omitting the flag does
  not mean the host; it defers to whatever `build.target` `fluxa.toml`
  carries, which silently overrode the choice for any pinned project.

Whether setup is complete is likewise decided by the *same* runtime
resolution `runBuild` performs (`plannedRuntimeRequirement`), not by an
OS/arch approximation of it — see "Runtime compatibility" below.

All of its `fluxa.toml` edits go through
`internal/project`'s additive editor
(`EnsureStringField`/`EnsureBoolField`/`EnsureStringArrayField`), which never
modifies or removes an existing key and rewrites the file atomically. See
ADR 0024 for the wizard's overall design.

## Transactional workspace

Each build uses `.fluxa-builder/work/<random-id>` below the canonical project
root, with separate compiled, package, runtime, output, and report directories.
The tree is private, symlink-guarded, and removed on completion unless
`--keep-work` was explicitly requested. Only a completed tree under workspace
`output/` may be atomically renamed into the project output directory.

## File collection and release paths

Collection preserves normalized project-relative paths and produces a stable
lexicographically sorted inventory. Entry files, Fluxa modules, and configured
assets carry logical kind metadata; this classification does not move source
files or alter paths used by the runtime.

The final ZIP is intended to separate the executable, Fluxa package, categorized
assets, and `build-info.json`. Categorized physical asset paths require a
runtime-supported mapping in the package manifest. Until that contract exists,
release generation must preserve each asset's logical path.

## Deterministic manifest

Manifest schema v1 separates package paths from project logical paths. Program
artifacts live in the `program/` namespace and assets in `resources/`. It
records hashes and all security-relevant build state, including
`preflight: not_run` and source exposure, while excluding timestamps, absolute
paths, workspace IDs, and other build-machine data.

## Fluxa package

`.flxpkg` format v1 is a deterministic little-endian container with a fixed
header, canonical manifest, sorted binary file table, and contiguous payload.
Each entry hashes its original bytes; the header hashes the manifest, table,
and stored payload. Optional zlib compression is per entry. Package publication
occurs only after the temporary file is synced, reopened, and fully verified.

The package reader treats every input as hostile. It validates bounded structure
before streaming payload data, limits zlib expansion, rejects trailing or
concatenated compressed streams, and only reports success after entry and global
integrity checks. The parser works over `io.ReaderAt`, so file verification and
in-memory fuzzing share exactly the same implementation.

## External processes

All process execution is centralized in `internal/executor`. Callers pass an
executable and literal argument vector; shell parsing is never involved. The
executor applies deadlines, bounded stdout/stderr capture, cancellation, and
structured exit diagnostics. Direct use of `os/exec` outside that package is an
architectural violation.

## Fluxa preflight

Automatic language preflight is deferred by ADR 0006. The current Fluxa
command is experimental and may execute user code after validation. The Builder
therefore treats a tested Fluxa project as an external prerequisite and must
never label the skipped check as passed.

## Compilation boundary

Release compilation is blocked until Fluxa exposes a stable, non-executing
compile command with bytecode version and ABI metadata. The explicit
`--include-source` fallback stages program sources only for development and
marks the result as source-exposed. Assets remain collector inputs for later
packaging and are not copied into the compiler output.

## Toolchain contract

The current Fluxa CLI has no public `--version` command. Discovery uses the
transitional `runtime-info-v1` protocol documented in ADR 0004: execute
`fluxa runtime info`, validate its signature, and hash the executable. A stable
machine-readable release and ABI identity is still required before standalone
artifacts are released.

## Runtime compatibility

A runtime identity must include more than operating system, architecture, and
Fluxa release. It must also identify the build-time `fluxa.libs` selection,
relevant compile flags, optional library backends, and bytecode/package ABI.

Verified runtimes live in a local registry keyed by Fluxa version, target, and
terminal mode.
Metadata additionally binds the binary hash, toolchain hash, package format,
program representation, packaged-runtime mode, terminal mode, and raw
`fluxa.libs` hash. Resolution rehashes every candidate and fails closed if no
single exact match exists.

The registry's *slot* key is coarser than its *compatibility* key, and the
gap is load-bearing today: the slot is only version/target/terminal, while
compatibility also demands the exact toolchain and `fluxa.libs` hashes.
Because Fluxa reports no version at all (ADR 0004), every runtime built
today lands in the same `unreported` slot for a given target. Two runtimes
that are genuinely different — built for projects with different
`fluxa.libs`, or against different toolchains — therefore collide, and
`Add` refuses to write into an occupied slot. `runtime.Remove` is the
escape hatch, used only by `init` after resolution has already proven the
occupant unusable for this build, and only with explicit confirmation.
This disappears once Fluxa reports a real version.

## Build verification

Every build runs the produced application for real
(`--fluxa-package-self-test`) before publishing anything — a safety
guarantee, not an optional check. `internal/app.resolveSmokeStrategy`
picks how, per target: `smokeNative` when the host can already execute
the target directly (the only path that existed before ADR 0028), or a
container path (`smokeWineContainer`/`smokeLinuxContainer`,
`internal/containersmoke`) when it can't — running the application inside
a pinned, network-isolated, resource-bounded Docker container instead.
macOS targets have no container path (Apple does not license macOS for
containers) and stay host-only.

The self-test always runs against a disposable, isolated copy of the
staged build output, never the directory that actually gets archived and
published (`internal/portable.SmokeExecutable`'s own isolation step):
anything the application happens to write on first run — a generated
key, a config file, a save slot — can never leak into a published
artifact. `internal/containersmoke`'s Linux container path is fully
verified end to end, including that its network isolation genuinely
blocks outbound connections. Its Windows/Wine container path has a
known, currently-unresolved reliability limitation on some hosts; see
[ADR 0028](adr/0028-container-verified-cross-platform-builds.md) for the
full investigation and the deliberate trade-off that follows from it: a
container-infrastructure failure (as opposed to the self-test itself
genuinely failing) degrades a build to a `WARNING:` and still publishes,
rather than blocking it.

## Application launcher

Portable output contains an application launcher, a private
`.fluxa-runtime[.exe]`, a same-basename `.flxpkg`, and `build-info.json`.
Assembly and hash verification happen inside the transactional workspace.

The launcher is `cmd/fluxa-launcher`, cross-compiled per target and
embedded by `internal/launcherbin` (`make launcher`, guarded by the same
byte-for-byte drift test as the runtime relay below). It used to be the
Fluxa Builder executable itself, renamed — which silently assumed the
build target matched the host, and so produced an ELF named `<app>.exe`
for a Windows build on a Linux machine. `internal/app` still exposes the
renamed-binary entry point for applications distributed before this
change. See ADR 0029.

On Linux and macOS, `.fluxa-runtime` is not the registered runtime binary
directly: it is a small embedded relay (`cmd/fluxa-runtime-wrapper`) that
speaks the private launcher protocol and execs a sibling
`.fluxa-runtime.interpreter` (the registered, hash-verified binary) with the
interpreter's own `run <entry> -proj .` command. Neither platform's native
Fluxa interpreter has a private-protocol entrypoint of its own — unlike
Windows's `FLUXA_PACKAGED_RUNTIME` mode — so the Builder provides the relay
itself, cross-compiled per target (`linux/amd64`, `darwin/amd64`,
`darwin/arm64`). The macOS relay is verified by cross-compilation and a
byte-for-byte drift test against its committed binary, the same as Linux,
but has not yet been exercised end to end on real macOS hardware.
See ADR 0025.

The launcher, rather than the language runtime, owns the distribution contract:

1. locate and fully verify the sibling FLXPKG;
2. extract it into a private temporary directory;
3. refresh packaged `.flx` files in the application data project;
4. preserve only paths declared by `build.persistent`;
5. run the private runtime as `fluxa run <entry> -proj .`;
6. mirror `build.export` files to a user-visible directory.

The launcher also implements the non-interactive
`--fluxa-package-self-test` protocol. The registered Fluxa binary remains a
script runtime and does not need to parse FLXPKG. What provides the
private-entry restriction depends on the target, as above: on Windows,
distribution builds select the `FLUXA_PACKAGED_RUNTIME` variant, which
refuses the public CLI and accepts only identity probing plus the
launcher's private execution protocol; on Linux and macOS the plain
interpreter has no such variant, and the relay in front of it enforces the
same restriction instead.

## Runtime data

Each application has a stable project data root keyed by `project.id`. It uses
XDG data on Linux, AppData on Windows, and Application Support on macOS.
Packaged source is refreshed on every launch, while declared persistent files
remain untouched after their first seed or runtime creation.

Exports are necessarily copies, not the authoritative save location. They are
written beside a writable portable application or below
`~/Documents/<project.name>` when the installation directory is immutable.
This keeps databases internal while allowing user-owned output such as cards,
screenshots, and documents to remain discoverable.

## Terminal mode

Project configuration includes `build.terminal` with a conservative default of
`true`.

- `true`: the application is allowed or expected to run attached to a terminal.
- `false`: targets that support the distinction should build a GUI/background
  application without opening a terminal window.

Linux desktop entries copy the value, macOS applications use the bundle launch
model, and Windows launchers are patched to PE GUI subsystem for `false` or
Console subsystem for `true`.
