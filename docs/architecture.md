# Architecture

Fluxa Builder is a Go command-line application separated from the Fluxa
language repository and runtime.

The Builder is responsible for project validation, toolchain orchestration,
file collection, package construction, runtime selection, target formatting,
verification, and build reporting. Parsing, resolving, executing, and compiling
Fluxa code remain responsibilities of the official Fluxa toolchain.

The planned pipeline is:

```text
config → validation → toolchain → preflight → collection/compilation
       → manifest → package → verification → runtime → output → smoke test
```

Builds use an isolated workspace and publish final artifacts atomically.

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
program representation, terminal mode, and raw `fluxa.libs` hash. Resolution
rehashes every candidate and fails closed if no single exact match exists.

## Portable output

Portable output contains a renamed runtime, a same-basename `.flxpkg`, and
`build-info.json`. Assembly and hash verification happen inside the
transactional workspace. A host runtime must then pass the non-interactive
`--fluxa-package-self-test` contract before the directory is atomically
published under `dist/<target>/`. Cross-target output remains unpublished until
an equivalent trusted smoke mechanism exists.

## Terminal mode

Project configuration will include an explicit terminal preference, planned as
`build.terminal` with a conservative default of `true`.

- `true`: the application is allowed or expected to run attached to a terminal.
- `false`: targets that support the distinction should build a GUI/background
  application without opening a terminal window.

The setting is metadata until target-specific packaging is implemented. Windows
will use it to choose the executable subsystem. On systems where it has no
meaning, validation and documentation will define the behavior explicitly.
