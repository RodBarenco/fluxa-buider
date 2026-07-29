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

## Terminal mode

Project configuration will include an explicit terminal preference, planned as
`build.terminal` with a conservative default of `true`.

- `true`: the application is allowed or expected to run attached to a terminal.
- `false`: targets that support the distinction should build a GUI/background
  application without opening a terminal window.

The setting is metadata until target-specific packaging is implemented. Windows
will use it to choose the executable subsystem. On systems where it has no
meaning, validation and documentation will define the behavior explicitly.
