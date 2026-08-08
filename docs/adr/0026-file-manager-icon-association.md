# ADR 0026: File-manager icon association (Windows PE + Linux desktop entry)

- Status: Accepted
- Date: 2026-08-04

## Context

While validating the full pipeline end to end against a real, published
Fluxa game, the project owner flagged a real gap: `targets.windows.icon` /
`targets.linux.icon` already ships a loose `.ico`/`.png` file next to the
executable (per ADR 0019/0020), but nothing associates that icon with the
executable itself — Explorer, the taskbar, and Linux file managers all show
a generic executable icon instead. This is explicitly scoped to the
*file-manager* icon only: the in-game window icon is `std.graph`/raylib
territory inside the user's own `.flx` code, outside anything Builder
controls once the interpreter starts running, and out of scope here.

Two very different platforms, two very different mechanisms:

- **Windows** identifies an executable's icon by a `RT_GROUP_ICON`/`RT_ICON`
  resource embedded inside the PE itself. There is no external-file
  convention Explorer honors.
- **Linux** desktop environments identify an application's icon (and its
  presence in an application menu at all) via a `.desktop` entry
  (freedesktop.org Desktop Entry Specification) with absolute `Exec=` and
  `Icon=` paths — the spec has no relative-to-file resolution.

## Decision

### Linux: automatic desktop-entry registration on launch, plus an install script for scripted deployment

The first version of this ADR shipped only a generated
`install-desktop-shortcut.sh` at the root of the Linux portable directory,
requiring the user to run it manually once. Real testing against the
project owner's own published game (Starfight) found this does not work in
practice: "o usuário não técnico não vai saber rodar o sh" (a non-technical
user will not know to run the `.sh`) — the game already ran fine without
it, so there was no prompt or friction pushing anyone to discover the
script at all.

The fix: `internal/app.RunInstalled` — the code path every produced
application's launcher actually runs, since the launcher is a renamed copy
of Fluxa Builder itself — now calls `ensureDesktopEntry` on every launch,
before invoking the runtime. It reads back its own `linux-runtime.json`
(now exported as `portable.LinuxInfo`, with a new `Terminal` field added
for this), computes the same `.desktop` content the install script would,
and writes it to `$HOME/.local/share/applications/<project.id>.desktop` —
silently and unconditionally overwriting any previous content there, so a
directory moved after the first launch re-syncs automatically on the next
one. Any failure (no `$HOME`, unwritable directory, missing metadata) is
silently ignored: this runs ahead of an audience that frequently has no
visible terminal (`terminal = false`) to report a warning to, and a missing
shortcut must never be allowed to stop the game itself from starting.

The project's determinism rule still applies — no absolute or
machine-specific path in any *generated build artifact* (`build-info.json`,
`linux-runtime.json`, the manifest, ADR 0010) — but this write happens at
**run time**, on the end user's own machine, not at build time; there is
nothing non-deterministic about the executable Builder ships, only about a
side effect it causes once actually run there, exactly like exported
persistent files (ADR 0023) already do.

`install-desktop-shortcut.sh` is kept as a second, explicit mechanism:
useful for provisioning scripts and mass-deployment scenarios that install
files without ever launching the GUI once, and as an inspectable,
non-magic option for technical users. It is otherwise redundant with
`ensureDesktopEntry` for the common case of a user just running the game.
Both write byte-identical `.desktop` content (same fields, computed the
same way, from the same `linux-runtime.json`) and are each independently
tested; they are not implemented as a shared code path because one produces
a shell script's *text* and the other computes the file's content directly
in Go — there is no meaningful line-level logic to share between "generate
a script that generates a file" and "generate the file."

### Windows: real PE `.rsrc` icon embedding, gracefully degraded

`internal/windows.EmbedIcon(exePath, icoPath string) error` appends a new
`.rsrc` section to the launcher PE containing one `RT_ICON` resource per
image already validated by `ValidateICO`, plus one `RT_GROUP_ICON` resource
referencing them, and points the PE's resource data directory at it. This is
hand-written PE surgery (no third-party PE library) purely additive to the
existing file:

- Every existing byte of every existing section is left untouched.
- Only three existing fields are patched in place: `NumberOfSections`,
  `SizeOfImage`, and the resource data directory entry.
- The new `IMAGE_SECTION_HEADER` is written into unused header padding that
  already existed in the file (`SizeOfHeaders` minus the file-aligned end of
  the current section table) — never into space that held other data.

Two preconditions are treated as **expected, not exceptional**, and reported
via a new `ErrorUnsupported` error kind rather than failing the build:

- The PE already has a resource directory (Data Directory[2] is nonzero) —
  embedding would require merging into an existing resource tree, which this
  function does not attempt.
- The PE's header has less than 40 bytes of slack past the current section
  table — there is no room for one more `IMAGE_SECTION_HEADER` without
  relocating existing sections, which this function does not attempt either.

`internal/portable.Build`'s Windows path calls `EmbedIcon` only when
`request.LauncherPath != ""` — the same precondition `ConfigureTerminal`
(ADR 0003) already requires, since that is the only branch where
`executablePath` is guaranteed to be a genuine PE (`ConfigureTerminal`'s own
`ValidatePEAMD64` call already ran against it there). When there is no
launcher, `executablePath` is the raw runtime binary copied directly — a
simpler, launcher-less publication mode that already skips other
PE-specific finalization for the same reason, and was never intended to
carry a customized file-manager icon in the first place.

On `ErrorUnsupported`, the build still succeeds exactly as it did before
this ADR — the loose `.ico` is still shipped — but a new `Result.Warnings`
entry is surfaced through `internal/app`'s `runBuild`, printed to the user
as `WARNING: ...`. Any other error from `EmbedIcon` still fails the build:
this feature is cosmetic, but a genuine I/O or corruption failure is not.

## Consequences

Real testing against the same Starfight build also found a second, more
serious bug, unrelated to discoverability: `internal/portable/archive.go`'s
`archiveEntries` (shared by the Windows ZIP and Linux tar.gz writers) only
ever granted the executable bit to `portable.Executable` — the launcher.
`.fluxa-runtime`, `.fluxa-runtime.interpreter` (ADR 0025), and this ADR's
own `install-desktop-shortcut.sh` all came out of a real `tar -xzf` as mode
`0600`, silently breaking every packaged Linux application the moment a
real user extracted it (the launcher would try to exec a non-executable
relay and fail). The macOS archiver (`macOSArchiveEntries`) had the same
class of bug in miniature — it special-cased `.fluxa-runtime` by name but
never `.fluxa-runtime.interpreter`. No automated test caught either
instance, because none of them archived a `LauncherPath`-based build — the
gap this ADR's own `TestLinuxRuntimeRelaySurvivesTarGZRoundTrip` now closes.
Both archivers were fixed the same way: derive the archived mode from the
source file's own already-correct on-disk permission bit instead of a
hardcoded, drift-prone filename allowlist.

Both Linux mechanisms, and the archive fix, are covered by tests that do
not require the platform they target:

- `internal/app/app_test.go`'s `TestRunInstalledRegistersDesktopEntry`
  proves `RunInstalled` itself registers a correct `.desktop` entry against
  a fake `$HOME`, with no script involved.
- `internal/portable/linux_desktop_test.go` builds a fixture with a real
  executable and icon, asserts the generated script contains no baked
  absolute path, and actually **runs** it (via `internal/executor`, this
  project's only permitted `os/exec` call site — ADR 0005) against a fake
  `$HOME`, confirming the resulting `.desktop` file's content is correct.
- `internal/portable/linux_relay_test.go`'s
  `TestLinuxRuntimeRelaySurvivesTarGZRoundTrip` archives a
  `LauncherPath`-based build, extracts the real tar.gz, and confirms the
  relay, interpreter, and install script are all still executable and the
  relay still runs correctly from the extracted location — this is the
  regression test for the archive-mode bug above; it fails without the fix
  and passes with it.
- `internal/windows/pe_fixture_test.go` hand-builds a minimal, valid PE32+
  fixture (`buildSyntheticPE`) usable by any test in the package —
  previously, every PE test in this codebase required a real Windows host
  and `os.Executable()` as the fixture. `internal/windows/pe_icon_test.go`
  embeds an icon into it and independently re-parses the resulting resource
  tree (a from-scratch reader, not `EmbedIcon`'s own code) to prove the
  round trip is byte-correct, plus a SHA-256 comparison proving every
  existing section is untouched, plus both `ErrorUnsupported` paths.
- `internal/portable/windows_icon_test.go` proves `portable.Build`'s wiring
  specifically: a synthetic PE launcher with adequate header slack ends up
  with an extra `.rsrc` section and zero warnings; one with none ends up
  unchanged, one warning, and the loose `.ico` still present.

What is explicitly **not verified** by any of this, because it requires
hardware this project does not currently have access to: actual icon
embedding correctness opened in real Windows Explorer, and against a real
MinGW/MSVC-linked `.exe` rather than a synthetic fixture. This is the same
honesty bar ADR 0025 already set for its macOS half — documented here rather
than silently assumed, and flagged as follow-up work for whenever real
Windows access is available.
