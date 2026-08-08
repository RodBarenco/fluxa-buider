# Distribution Guide

This guide describes the currently verified path from a tested Fluxa project
to an application a non-technical user can open directly. `fluxa-builder init`
can walk through steps 2 and 3 interactively and run step 4 automatically once
setup is complete; see `docs/adr/0024-interactive-init-wizard.md`. This guide
remains the reference for what that command does under the hood, and for
doing any of it by hand.

## 1. Test the project first

Fluxa Builder deliberately does not run preflight. The current Fluxa preflight
can reject valid programs and may execute code. Test the real project manually:

```sh
fluxa run main.flx -proj .
```

The Builder assumes this test already passed.

## 2. Configure the project

A desktop game with internal SQLite state and user-visible earned cards can use:

```toml
[project]
name = "Starfight"
id = "com.rodbarenco.starfight"
version = "0.1.0"
entry = "main.flx"
type = "desktop"

[toolchain]
path = "fluxa"

[runtime]
scope_cap = 1024

[libs]
std.graph = "1.0"
std.image = "1.0"
std.strings = "1.0"
std.sqlite = "1.0"
std.sound = "1.0"
std.crypto = "1.0"
std.json2 = "1.0"
std.fs = "1.0"
std.httpc = "1.0"
std.https = "1.0"

[build]
output = "dist"
target = "host"
terminal = false
assets = [
  "fluxa.toml",
  "fluxa.libs",
  "assets/**",
  "languages/**",
]
persistent = ["nave.db", "cards/**"]
export = ["cards/**"]

[package]
format = "portable"
compress = true
include_source = true
```

Do not ship a played database merely because it appears in `persistent`.
Persistence is a runtime policy, not an asset requirement. SQLite
`sqlite.open("nave.db")` creates a missing database, and the application can
create its schema with `CREATE TABLE IF NOT EXISTS`.

Likewise, a generated directory does not need to be shipped if the application
creates it before writing. Starfight creates `cards/` with `fs.mkdir`.

## 3. Register a compatible runtime

The runtime registry is an explicit local trust store. Create strict schema-v2 metadata
for each OS, architecture, terminal mode, toolchain hash, `fluxa.libs` hash,
supported program format, and packaged-runtime mode, then add the binary:

```sh
fluxa-builder runtime add ./fluxa-runtime \
  --metadata ./runtime.json
```

List verified candidates with:

```sh
fluxa-builder runtime list
```

Use `--registry` and `--runtime-registry` to select a non-default registry.
The current development pipeline uses `program_formats: ["fluxa-source"]`.
It also requires `"packaged": true`.

How the interpreter binary itself is built differs by target, because only
Windows needs a separate, restricted entrypoint:

- **Linux and macOS**: build the plain native interpreter — `make build` in
  the `fluxa-lang` checkout, with whatever optional backend flags the
  project's libraries need (see that repository's `fluxa.libs`). Neither
  native entrypoint has a private-launcher mode of its own; Fluxa Builder
  supplies it. `fluxa-builder build` assembles a small embedded relay,
  cross-compiled per architecture, as `.fluxa-runtime` that speaks the
  private protocol and execs this registered binary (placed beside it as
  `.fluxa-runtime.interpreter`) with its already-working
  `run <entry> -proj .` command. Direct execution of `.fluxa-runtime` itself
  — the relay, not the interpreter — refuses with exit code 126. See
  ADR 0025. (The macOS relay is cross-compiled and hash-verified but not
  yet confirmed on real macOS hardware.)
- **Windows**: build the real packaged variant —
  `make build-windows-packaged` in the `fluxa-lang` checkout (see
  `docs/WINDOWS.md` there). That binary is compiled with
  `FLUXA_PACKAGED_RUNTIME=1` and already refuses public commands such as
  `fluxa-runtime.exe run arbitrary.flx` with exit code 126 on its own; Fluxa
  Builder registers and ships it unmodified as `.fluxa-runtime.exe`.

Doing this by hand is what `fluxa-builder init` automates on Linux and
Windows: cloning `fluxa-lang` and building it (or, for Windows,
cross-compiling both `build-windows-essential-static` and
`build-windows-packaged`) inside a pinned Docker container, never touching
the host's own package manager. See
`docs/adr/0027-automatic-toolchain-acquisition.md`.

## 4. Build

Until Fluxa exports stable distributable bytecode, source staging must be
explicit:

```sh
fluxa-builder build . --include-source
```

The warning about `source-exposed` is expected and must not be suppressed or
reinterpreted as a protected release.

A successful Linux x64 build currently emits:

```text
dist/linux-x64/
├── <application>/
│   ├── <application>
│   ├── .fluxa-runtime
│   ├── .fluxa-runtime.interpreter
│   ├── <application>.flxpkg
│   ├── <application>.png                  (only if targets.linux.icon is set)
│   ├── install-desktop-shortcut.sh
│   ├── build-info.json
│   └── linux-runtime.json
├── <application>.tar.gz
├── <application>.tar.gz.sha256
├── <project.id>_<version>_amd64.deb
└── <project.id>_<version>_amd64.deb.sha256
```

A `windows-x64` build emits:

```text
dist/windows-x64/
├── <application>/
│   ├── <application>.exe                   (icon embedded in its resources)
│   ├── <application>.exe.local
│   ├── .fluxa-runtime.exe
│   ├── <application>.flxpkg
│   ├── <application>.ico                   (only if targets.windows.icon is set)
│   ├── dxil.dll
│   ├── libgallium_wgl.dll
│   ├── opengl32.dll
│   ├── opengl32sw.dll
│   ├── build-info.json
│   └── windows-version.json
├── <application>.zip
└── <application>.zip.sha256
```

Unlike Linux, there is no `.fluxa-runtime.interpreter` beside the runtime:
the Windows interpreter has a private-entry mode of its own, so it is
shipped directly rather than behind a relay (see step 3 above). The four
DLLs and `<application>.exe.local` are the Mesa3D fallback described below.

The visible application executable is the launcher — a small program of its
own (`cmd/fluxa-launcher`), built for the target platform rather than for
whichever machine ran the build, so a Windows application assembled on
Linux gets a real PE. A user does not install Fluxa, run Fluxa Builder,
open a terminal, or know about FLXPKG.

glibc compatibility follows whichever runtime binary was registered — Fluxa
Builder copies it unmodified and never relinks or rewrites it. Building
against an older glibc (an older distro, or a container pinned to one)
produces a more widely compatible binary; this is a property of the
registered runtime, not something a build flag controls.

### File-manager icon association

A configured icon (`targets.windows.icon` / `targets.linux.icon`) always
ships as a loose file beside the executable. See
`docs/adr/0026-file-manager-icon-association.md` for the full design; in
short:

- **Linux**: the launcher registers a `.desktop` entry under
  `~/.local/share/applications` automatically the first time the
  application runs — no extra step for the end user — and re-registers it
  on every later launch, so moving the extracted directory afterward does
  not leave a stale shortcut. `install-desktop-shortcut.sh` is also shipped
  at the root of the portable directory for provisioning scripts or mass
  deployment that installs files without ever launching the GUI once; it
  registers the same entry and is likewise safe to re-run.
- **Windows**: the icon is embedded directly into the executable's PE
  resources at build time — no extra step. This is best-effort: if the
  launcher's PE has no header room for another section, or already carries
  embedded resources, `fluxa-builder build` prints a `WARNING:` line, the
  build still succeeds, and the loose `.ico` file is still shipped as
  before.

### Windows Mesa3D software-rendering fallback

Every Windows build also bundles `dxil.dll`, `libgallium_wgl.dll`,
`opengl32.dll`, `opengl32sw.dll`, and a `<executable>.local` redirection
marker beside the executable, so `std.graph` still works on a machine with
no usable OpenGL driver — common inside VMs. The `.local` file is what
makes the DLLs count: it enables Windows' per-application ("DotLocal") DLL
redirection, so the loader prefers these copies over the system
`opengl32.dll`. Its contents are irrelevant — only its existence is. The
files come from the
community [`mesa-dist-win`](https://github.com/pal1000/mesa-dist-win)
project, downloaded once, checksum-verified, and cached at
`~/.fluxa-builder/mesa-dist-win` for reuse across every future build. A
failure to acquire them only prints a `WARNING:` and the build proceeds
without the fallback bundled — this is an optional compatibility
enhancement fluxa-lang's own `docs/WINDOWS.md` documents, not a functional
requirement. See `docs/adr/0027-automatic-toolchain-acquisition.md`.

### Building for a target other than the host machine

`build.target`/`build --target <os>-<arch>` (see
`docs/configuration.md`) is not limited to the host's own platform. A
build for any other target still runs the produced application for real
before publishing — this project's core safety guarantee, unweakened —
but does so inside a network-isolated, resource-bounded Docker container
instead of natively, since the host itself often can't execute a
different target's binary directly:

```sh
# from a Linux host
fluxa-builder build . --include-source --target windows-x64
```

The Linux container path is fully working. The Windows/Wine container
path has a known, currently-unresolved reliability limitation on some
hosts: when container verification cannot even run (Docker unavailable,
or this specific limitation), the build still publishes, with a
`WARNING:` line, rather than being blocked — see
`docs/adr/0028-container-verified-cross-platform-builds.md` for the full
account and exact per-platform-pair status. macOS targets remain
host-only in both directions; there is no container path for macOS.

## 5. Runtime behavior

On launch:

- the FLXPKG is verified before any packaged file executes;
- `.flx` files are refreshed from the verified package;
- the private runtime is invoked in project mode;
- the private runtime refuses direct public CLI use;
- undeclared runtime changes are replaced by packaged content;
- persistent paths survive restarts and upgrades;
- exported paths are copied to a visible location.

For Linux portable output, exports appear beside the executable:

```text
<application>/
└── cards/
```

For an immutable installation such as `/opt`, the fallback is:

```text
~/Documents/<project.name>/cards/
```

The authoritative internal state uses the native per-user application-data
directory:

```text
Linux:   $XDG_DATA_HOME/fluxa/<project.id>/project/
Windows: %AppData%\fluxa\<project.id>\project\
macOS:   ~/Library/Application Support/fluxa/<project.id>/project/
```

On Linux, when `XDG_DATA_HOME` is unset, the fallback is:

```text
~/.local/share/fluxa/<project.id>/project/
```

## 6. Acceptance test

For a stateful graphical application:

1. open only the final application executable;
2. confirm no terminal appears when `terminal = false`;
3. exercise audio, fonts, images, language files, and database features;
4. create a save or earned item;
5. close normally and reopen;
6. confirm internal state was restored;
7. confirm exported files are visible;
8. verify the archive checksum;
9. install the native package and repeat;
10. remove the native package and confirm user data was not deleted.

Starfight passed the direct portable launch test on Pop!_OS: OpenGL initialized,
fonts and assets loaded, the application remained live, database state survived
two executions, and real card PNG files were exported beside the executable.

## Current security boundary

FLXPKG v1 provides deterministic structure, bounded hostile-input parsing,
per-file hashes, and a global package hash. It prevents unnoticed corruption
and modification.

It does not make source confidential. The current `fluxa-source` representation
can be extracted by a determined user. A protected release remains blocked on
a stable Fluxa bytecode compiler/export contract with explicit bytecode version
and ABI.

The packaged-runtime authorization is a strong product boundary against normal
or accidental reuse of the shipped runtime. It is not DRM against a determined
attacker who patches binaries. Package and file SHA-256 verification protects
integrity; bytecode or native compilation is still required for source
confidentiality.
