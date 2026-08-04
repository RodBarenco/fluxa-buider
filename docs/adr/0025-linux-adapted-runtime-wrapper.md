# ADR 0025: Linux and macOS "adapted runtime" wrapper

- Status: Accepted
- Date: 2026-08-04

## Context

`internal/runner.go` runs every real, installed application through one
unconditional path (`RunInstalled` in `internal/app/installed.go` always
sets `PackagedRuntime: true`):

```go
command = exec.CommandContext(ctx, request.RuntimePath,
    "__fluxa_builder_run_v1", entry, projectRoot)
command.Env = append(os.Environ(), "FLUXA_BUILDER_RUNTIME_AUTH=...")
```

This session's investigation (git checkout of
`github.com/RodBarenco/fluxa-lang` at both current `main` and the exact
commit `docs/distribution.md`/`HANDOFF.md` already cite, plus compiling the
real binary with `make build` and running it) confirmed
`__fluxa_builder_run_v1` is implemented only in `platform/windows/main.c`.
The native Linux entrypoint (`src/main.c`) has no such command — and per the
Makefile's own header comment ("v0.10 | C99 | targets: native Linux/macOS,
RP2040, ESP32, Cortex-M"), macOS's native interpreter is built from that
exact same `src/main.c`, so it has the identical gap:

```text
$ ./fluxa __fluxa_builder_run_v1 main.flx /tmp/project
[fluxa] unknown command: __fluxa_builder_run_v1
```

There is also no Linux `build-packaged` Make target at either commit — only
`build-windows-packaged`. This is not a regression or an oversight to fix
upstream: Windows needed its own separate entrypoint specifically because of
that platform's own constraints (`docs/WINDOWS.md`: no POSIX — "deliberately
excludes Unix IPC, atomic handover, live runtime replacement, native Fluxa
threads, and C FFI" — plus static-linking every third-party dependency so
the result has zero MSYS2 DLL dependencies). Linux's native `src/main.c`
never needed a separate entrypoint or a static-link dance, so it never grew
a packaged-mode command either — for Linux or for macOS, which shares that
same source. Per the project owner, this is by design: Fluxa Builder itself,
not fluxa-lang, is responsible for producing the adapted runtime on both
platforms.

## Decision

Fluxa Builder generates and embeds a small, dependency-free Go program
(`cmd/fluxa-runtime-wrapper`) that speaks the private protocol and relays it
into the interpreter's already-working, already-documented
`run <entry> -proj .` command:

- Accepts only `__fluxa_builder_run_v1 <entry> <projectRoot>` with the exact
  `FLUXA_BUILDER_RUNTIME_AUTH` value `internal/runner.go` sends (both sides
  now reference one source of truth, `internal/runtimeprotocol`, instead of
  independently-typed literals).
- On a valid call, execs a fixed sibling binary,
  `.fluxa-runtime.interpreter`, as `run <entry> -proj .`, inheriting the
  process's working directory (already the verified, FLXPKG-extracted
  project root — `internal/runner.go` sets `command.Dir = projectRoot`
  before the relay ever runs) and stdio.
- Refuses everything else — wrong command, missing/wrong authorization,
  wrong argument count, or direct execution with no arguments at all — by
  printing one line to stderr and exiting **126**, matching the exit code
  `docs/distribution.md` already documented before this binary existed to
  produce it.

The wrapper never sees an unverified file: `internal/runner.go` already
extracts and hash-verifies the FLXPKG (`internal/package`'s per-entry and
global SHA-256 checks) into the private, per-`project.id` data directory
*before* invoking the runtime at all, and only that directory's path is ever
passed through. The relay adds no new trust boundary — it only reaches
parity with what Windows's real `FLUXA_PACKAGED_RUNTIME` mode already does.

`internal/portable.Build` assembles this for Linux (`linux-x64`) and macOS
(`macos-x64`/`macos-arm64`); Windows keeps its existing single-binary copy —
it already has a real `FLUXA_PACKAGED_RUNTIME` binary and doesn't need a
relay:

```text
<application>/                       Linux (linux-x64)
├── <application>                    the launcher (a renamed Fluxa Builder)
├── .fluxa-runtime                   the embedded relay (this ADR)
├── .fluxa-runtime.interpreter       the verified, registered fluxa binary
├── <application>.flxpkg
├── build-info.json
└── linux-runtime.json

<application>.app/Contents/MacOS/    macOS (macos-x64 / macos-arm64)
├── <application>                    the launcher
├── .fluxa-runtime                   the embedded relay, arch-matched
└── .fluxa-runtime.interpreter       the verified, registered fluxa binary
```

The registered runtime binary (`runtime add`) is unchanged in every other
respect: still validated as a real ELF64 x86-64 (Linux) or Mach-O (macOS)
executable (`internal/linux.ValidateELFAMD64`,
`internal/macos.ValidateMachO`), still hash-verified against `runtime.json`'s
`binary_sha256`. It becomes the interpreter rather than `.fluxa-runtime`
directly.

### Embedding

The compiled relays — `linux/amd64`, `darwin/amd64`, `darwin/arm64` — are
committed to the repository under `internal/wrapper/bin/` and embedded via
`//go:embed`. `make wrapper` regenerates all three
(`GOOS=<os> GOARCH=<arch> CGO_ENABLED=0 go build -trimpath`, one invocation
per target); `build` and `check` depend on it. Because CI (`ci.yml`) runs
`go build`/`go test` directly rather than through `make`, the committed
binaries must already be current — `internal/wrapper`'s own test rebuilds
each from source with the same flags and asserts a byte-for-byte SHA-256
match, so an edit to `cmd/fluxa-runtime-wrapper` without regenerating and
committing all three binaries fails loudly in CI instead of silently
shipping stale code.

The relay's source has no platform-specific code (plain `os/exec`, `os.Args`,
`os.Getenv`, `filepath`), so its logic is proven once, on Linux, by
`cmd/fluxa-runtime-wrapper/main_test.go`. What is specific to macOS —
cross-compilation succeeding, the correct architecture-matched binary being
selected, and the two-file bundle layout being assembled correctly — is
covered by `internal/portable/macos_relay_test.go`, which deliberately has
no `darwin`-only build tag so it runs on every CI host. What none of this
proves, because it requires real macOS hardware this project does not
currently have access to, is that the relay actually executes and correctly
relays there. That gap is explicit, not hidden: `docs/manual-do-usuario.md`
and this ADR both say so, and closing it is future work for whoever next
has a real macOS machine to run the same end-to-end proof this ADR's Linux
half already passed (see `HANDOFF.md`).

## Consequences

A registered Linux runtime now actually works when a real end user opens a
shipped application — previously, every "official Linux pipeline" test
passed while never exercising `__fluxa_builder_run_v1` at all (its fixture
never sets `LauncherPath`, so it always took the older, simpler
direct-executable branch); `internal/portable`'s new
`TestLinuxRuntimeRelayAssemblesAndExecutes` closes that gap by actually
invoking the assembled `.fluxa-runtime` with the real protocol end to end.

The portable directory gains one file on Linux and macOS
(`.fluxa-runtime.interpreter` alongside `.fluxa-runtime`); every place that
documents or counts that layout (`README.md`, `docs/distribution.md`,
`docs/manual-do-usuario.md`, `docs/architecture.md`, and
`internal/portable/macos_integration_test.go`'s archive entry-count
assertions) is updated accordingly.

Committing a compiled binary is a deliberate, narrow exception to
"generated artifacts don't belong in git" — `go:embed` requires the file to
exist at compile time, CI never runs a generation step before `go build`,
and the drift test makes staleness a hard CI failure rather than a
silent risk.
