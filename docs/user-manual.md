# User Manual — Fluxa Builder

*[Também disponível em português: manual-do-usuario.md](manual-do-usuario.md)*

This manual shows the current path from an already-tested Fluxa project to a
portable application the end user can open directly.

> **Current state:** Linux and Windows produce working portable packages.
> macOS produces a working `.app` for testing, but public distribution still
> needs Developer ID signing and Apple notarization.

## Guided path: `fluxa-builder init`

After compiling the Builder (section 2) and manually testing the Fluxa
project (section 3), the interactive command below helps with sections 4, 5,
and 6 without requiring you to already know every command and field by
heart:

```sh
fluxa-builder init
```

It detects the host system automatically and, in order, asks:

1. the project directory (full path). If `fluxa.toml` is missing or is
   missing `name`, `id`, `version`, or `entry`, the wizard offers to fill in
   each field — always showing the exact line before writing it and asking
   for confirmation;
2. which platform to build for. Choosing your own machine's platform works
   as always. Choosing `windows-x64` or `linux-x64` on a different host now
   also works for real: the smoke test that must pass before publishing
   runs inside a network-isolated Docker container instead of natively —
   see "Building for another platform" in section 6. Choosing `macos` on a
   non-Mac host, or "all three platforms" in one run, still isn't
   supported; the wizard explains why and asks again, and an answer that
   matches nothing on the menu is rejected rather than quietly becoming a
   build for your own machine. Whatever you pick wins over any
   `build.target` already pinned in `fluxa.toml`;
3. the most common optional settings described in section 4 —
   `build.assets`, `build.exclude`, `build.persistent`/`build.export`,
   `package.include_source`, and the icon/`bundle_id` **for the platform
   you chose above**, not for the machine you are sitting at — silently
   skipping any field already set in `fluxa.toml`. This is why the target
   question comes first: a build only ever reads its own target's icon, so
   a Windows build asks for the `.ico` it will actually embed;
4. where to save the output, with the option to record that choice into
   `build.output`;
5. whether to try downloading and compiling the Fluxa toolchain
   automatically. On Linux and Windows, answering "yes" actually clones
   `fluxa-lang` and builds it for real inside a pinned Docker container
   (including real MinGW cross-compilation for Windows, no Windows machine
   needed) — it never touches the host's own package manager. Building for
   Windows from a Linux machine compiles that one download twice over: the
   Windows runtime you ship, plus a compiler that runs on *your* machine,
   since a cross-compiled `.exe` cannot compile anything here. Declining, or
   a condition automatic acquisition does not support, falls back to the
   manual guide (equivalent to sections 5 and 6 below), including a
   starting `runtime.json` template already filled with whatever can be
   computed. macOS automatic acquisition is on hold and always falls
   straight through to the manual guide. See
   `docs/adr/0027-automatic-toolchain-acquisition.md`.

The wizard skips straight to actually building the application (equivalent
to section 6) only when a `fluxa` toolchain is available **and** a
registered runtime genuinely resolves for this build — the same selection
`build` itself performs, not merely a runtime for the same platform. A
registered runtime is only usable with the exact toolchain, `fluxa.libs`,
and terminal mode it was built against, so a `windows-x64` runtime left
over from another project does not count as ready: the wizard says so and
offers to build a matching pair instead of starting a build that would
fail at runtime selection.

Because registry slots are keyed on the Fluxa version a toolchain reports,
and `fluxa-lang` reports none, every runtime built today lands in the same
slot for a given target and terminal mode. When acquisition produces a
runtime whose slot is already taken by the incompatible one, the wizard
asks before replacing it; declining leaves the registry untouched.

## 1. Prerequisites

On the machine for the system being packaged, install or prepare:

- the Fluxa toolchain compatible with the project;
- Go, to compile Fluxa Builder;
- the Fluxa Builder source code;
- a packaged Fluxa runtime, compiled for the same system, architecture, and
  terminal mode as the application.

The Builder always runs the produced application for real before
publishing. When the host can execute the target natively that test runs
directly; when it cannot — a `windows-x64` build on Linux, or the reverse
— it runs inside a network-isolated Docker container instead, so those two
targets can be built from either host. macOS is the exception: it must be
built on macOS, because no container can run it. See "Building for another
platform" in section 6.

## 2. Compile the Builder

Linux or macOS:

```sh
go build -trimpath -o bin/fluxa-builder ./cmd/fluxa-builder
./bin/fluxa-builder version
```

Windows PowerShell:

```powershell
go build -trimpath -o bin/fluxa-builder.exe ./cmd/fluxa-builder
.\bin\fluxa-builder.exe version
```

## 3. Test the Fluxa project

The Builder does not run preflight, because that feature can still reject
valid Fluxa programs. Before packaging, enter the project directory and run:

```sh
fluxa run main.flx -proj .
```

Test the screens, files, database, audio, images, and every other feature
the program uses. The Builder assumes the project already passed this test.

## 4. Configure `fluxa.toml`

Example for a graphical game:

```toml
[project]
name = "My Game"
id = "com.example.my-game"
version = "1.0.0"
entry = "main.flx"
type = "desktop"

[toolchain]
path = "fluxa"

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
persistent = ["game.db", "cards/**"]
export = ["cards/**"]

[package]
format = "portable"
compress = true
include_source = true

[targets.windows]
icon = "assets/app.ico"

[targets.linux]
icon = "assets/app.png"

[targets.macos]
icon = "assets/AppIcon.icns"
bundle_id = "com.example.my-game"
```

Main options:

- `terminal = false`: graphical application with no terminal window on
  Windows;
- `terminal = true`: application that uses a console;
- `assets`: files shipped alongside the program;
- `persistent`: database, settings, and generated files that must survive
  future runs;
- `export`: subset of `persistent` that is also made visible to the user;
- `include_source = true`: required until Fluxa exports a stable
  distributable bytecode.

Every `export` pattern must also appear exactly in `persistent`.

An SQLite database created by the program does not need to exist before the
build. If the program creates the file and its tables on first run, do not
ship an already-played database in `assets`.

## 5. Prepare the packaged runtime

How the runtime is prepared depends on the system, because only Windows
needs a differently-compiled binary:

- **Linux and macOS**: in the `fluxa-lang` repository, compile the regular
  native interpreter with `make build`, using whatever backends the project
  needs (see `fluxa.libs` in that repository) — both systems share the same
  `src/main.c`. Neither native entrypoint has a private mode of its own —
  Fluxa Builder assembles, at `build` time, a small embedded relay
  (compiled per architecture) as `.fluxa-runtime` that speaks the launcher's
  private protocol and executes that registered binary (placed beside it as
  `.fluxa-runtime.interpreter`) with the already-existing
  `run <entry> -proj .` command. See ADR 0025 (`docs/adr/`). The macOS relay
  was cross-compiled and hash-verified but not yet confirmed end to end on
  real macOS hardware — only Linux has passed that test so far.
- **Windows**: compile the actually-packaged variant with
  `make build-windows-packaged` (see `docs/WINDOWS.md` in the `fluxa-lang`
  repository). That binary is compiled with `FLUXA_PACKAGED_RUNTIME=1` and
  already refuses public commands on its own, such as:

  ```powershell
  fluxa-runtime.exe run arbitrary-file.flx
  ```

In both cases, the final result refuses direct/public use with exit code
126 — on Linux because the Builder's relay refuses, on Windows because the
binary itself refuses.

The runtime must match the target:

| System | Architecture | Runtime name |
|---|---|---|
| Linux x64 | `amd64` | `fluxa-runtime` |
| Windows x64 | `amd64` | `fluxa-runtime.exe` |
| macOS Intel | `amd64` | `fluxa-runtime` |
| macOS Apple Silicon | `arm64` | `fluxa-runtime` |

A `runtime.json` in the format described in
[Project Configuration](configuration.md#runtime-registry) is also
required. Its hashes must exactly identify the toolchain, `fluxa.libs`, and
runtime used.

Register the runtime:

```sh
fluxa-builder runtime add ./fluxa-runtime --metadata ./runtime.json
fluxa-builder runtime list
```

On Windows:

```powershell
.\fluxa-builder.exe runtime add .\fluxa-runtime.exe --metadata .\runtime.json
.\fluxa-builder.exe runtime list
```

By default, the registry lives at `~/.fluxa-builder/runtimes`. To use a
different one:

```sh
fluxa-builder runtime add ./fluxa-runtime \
  --metadata ./runtime.json \
  --registry ./my-runtimes
```

## 6. Build the application

Run from the project root:

```sh
fluxa-builder build . --include-source
```

If using an alternate registry:

```sh
fluxa-builder build . \
  --include-source \
  --runtime-registry ./my-runtimes
```

To write to a directory other than `build.output` without editing
`fluxa.toml`, use `--output` (subject to the same safety rule: a relative
path, no `..`, staying inside the project):

```sh
fluxa-builder build . --include-source --output build-output
```

### Building for another platform

`--target <os>-<arch>` builds for a platform other than the host machine
for a single run, without editing `fluxa.toml`:

```sh
# from a Linux host
fluxa-builder build . --include-source --target windows-x64
```

The smoke test still runs the produced application for real before
publishing — nothing about that safety guarantee is weakened — but when
the host can't run that target natively, it runs inside a network-isolated
Docker container instead. Building `linux-x64` this way is fully working.
Building `windows-x64` this way has a known, currently-unresolved
reliability limitation on some hosts: when container verification cannot
run at all (Docker missing, or this limitation), the build still
publishes, with a `WARNING:` line, instead of being blocked. See
`docs/adr/0028-container-verified-cross-platform-builds.md` for the exact
status and the reasoning behind that trade-off. `macos` targets always
need to be built on real Mac hardware; there is no container path for
macOS at all.

The Builder:

1. collects only the declared files;
2. creates and verifies the FLXPKG package;
3. selects a compatible packaged runtime;
4. assembles the launcher (built for the target platform, not for the
   machine running the build) and the private runtime;
5. runs the smoke test without opening the UI — natively, or inside a
   container when the host cannot execute the target;
6. creates the distribution archive and its SHA-256;
7. publishes the result only if every step passed.

The Builder does not overwrite an existing output. Before repeating a build,
consciously rename or remove the old target folder under `dist`.

## 7. Result per system

### Windows

The result normally lands at:

```text
dist/windows-x64/
├── my-game/
│   ├── my-game.exe
│   ├── .fluxa-runtime.exe
│   ├── my-game.flxpkg
│   ├── my-game.ico                 (only if targets.windows.icon is set)
│   ├── dxil.dll
│   ├── libgallium_wgl.dll
│   ├── opengl32.dll
│   ├── opengl32sw.dll
│   ├── my-game.exe.local
│   ├── windows-version.json
│   └── build-info.json
├── my-game.zip
└── my-game.zip.sha256
```

Ship the ZIP. The user extracts the folder and opens only `my-game.exe`. If
an icon was configured, it is already embedded into the `.exe`'s own
resources (see "Associating the icon with the executable", below) — the
loose `.ico` is still shipped either way.

The four `.dll` files and `my-game.exe.local` are the Mesa3D
software-rendering fallback — they keep `std.graph` working even on a
machine with no usable GPU driver, common inside VMs. They are downloaded
once, checksum-verified, and cached at `~/.fluxa-builder/mesa-dist-win`; a
failure here only prints a `WARNING:` and the build continues without them —
this is an optional compatibility enhancement, not a functional
requirement. See `docs/adr/0027-automatic-toolchain-acquisition.md`.

### Linux

```text
dist/linux-x64/
├── my-game/
│   ├── my-game
│   ├── .fluxa-runtime
│   ├── .fluxa-runtime.interpreter
│   ├── my-game.flxpkg
│   ├── my-game.png                 (only if targets.linux.icon is set)
│   ├── install-desktop-shortcut.sh
│   ├── build-info.json
│   └── linux-runtime.json
├── my-game.tar.gz
├── my-game.tar.gz.sha256
├── com.example.my-game_1.0.0_amd64.deb
└── com.example.my-game_1.0.0_amd64.deb.sha256
```

Either the portable `.tar.gz` or the `.deb` installer can be shipped. Both
already register an icon and a menu entry automatically — the `.deb` on
install, the portable `.tar.gz` the first time the game is opened.

### Associating the icon with the executable

A configured icon (`targets.windows.icon` / `targets.linux.icon`) is always
shipped as a loose file beside the executable. See
`docs/adr/0026-file-manager-icon-association.md` for the full design; in
short:

- **Windows**: the icon is already embedded directly into the `.exe`'s
  resources during `build` — no extra step is needed. This is done
  best-effort: if the launcher has no header room for another section, or
  already carries embedded resources, `fluxa-builder build` prints a
  `WARNING:` line, the build still succeeds and publishes normally, and the
  loose `.ico` is still shipped as before.
- **Linux**: the launcher itself registers its `.desktop` entry under
  `~/.local/share/applications` automatically on the first run — no extra
  step for someone who just wants to play — and refreshes that entry on
  every later run, so moving the folder afterward does not leave a stale
  shortcut. The `install-desktop-shortcut.sh` script is also still shipped
  at the root of the portable folder, useful for provisioning scripts or
  mass deployment that never opens the graphical interface; it registers
  the same entry and is likewise safe to run again.

### macOS

```text
dist/macos-x64/
├── my-game.app/
├── my-game.app.tar.gz
└── my-game.app.tar.gz.sha256
```

The `.app` contains the public launcher, the private runtime, and the
FLXPKG. The unsigned artifact is a development artifact. For public
distribution to non-technical users, every component still needs to be
signed with Developer ID, have Hardened Runtime enabled, be notarized, and
have Apple's ticket stapled.

## 8. Manual test before distributing

Test the application exactly as the end user would:

1. copy only the final ZIP, portable archive, or installer to another
   location;
2. extract or install it;
3. open only the executable with the application's name;
4. confirm no terminal appears when `terminal = false`;
5. test images, fonts, audio, video, languages, and the database;
6. create a save or exported file;
7. close the program normally;
8. reopen it and confirm the state was preserved;
9. check the exported files, such as the `cards` folder;
10. try running the private runtime with an arbitrary `.flx` file and
    confirm it refuses the operation.

Internal data lives at:

```text
Linux:   $XDG_DATA_HOME/fluxa/<project.id>/project
Windows: %AppData%\fluxa\<project.id>\project
macOS:   ~/Library/Application Support/fluxa/<project.id>/project
```

When the application folder is writable, `build.export` items appear beside
it. On protected installations, the fallback is the
`Documents/<project name>` folder.

## 9. Current limitations

- `.flx` files are still included as readable source.
- The packaged runtime prevents normal/accidental use as a CLI, but it is
  not DRM.
- FLXPKG's Ed25519 signature does not replace Authenticode on Windows or
  Developer ID/notarization on macOS.
- macOS does not yet produce a notarized public distribution.
- Windows does not yet have a native installer or Authenticode signing.

For every advanced option, see
[Project Configuration](configuration.md) and
[Distribution Guide](distribution.md).
