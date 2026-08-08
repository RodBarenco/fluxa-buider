# Project Configuration

Fluxa Builder reads `fluxa.toml` from the project root. Builder-specific tables
coexist with Fluxa's existing `[runtime]`, `[libs]`, `[ffi]`, and `[security]`
tables. The current Fluxa loader ignores Builder tables and additional project
metadata it does not use.

`fluxa-builder init` can help write most of the Builder-specific fields
described below interactively — see "Quick start" in the
[README](../README.md), or the full walkthrough in the
[User Manual](user-manual.md). It never overwrites a field that is already
present;
every field it does offer to write is previewed and requires confirmation
first, and every other byte of the file (including Fluxa's own tables and any
comments) is left untouched.

## Minimal configuration

```toml
[project]
name = "My Application"
id = "com.example.my-application"
version = "1.0.0"
entry = "main.flx"
```

Required fields:

- `project.name`: display name, at most 256 UTF-8 bytes;
- `project.id`: lowercase reverse-domain identifier;
- `project.version`: semantic version;
- `project.entry`: existing Fluxa entry file relative to the project.

## Defaults

```toml
[project]
type = "desktop"

[build]
output = "dist"
target = "host"
terminal = true

[package]
format = "portable"
compress = true
sign = false
embed = false
include_source = false
```

`build.terminal = false` builds a graphical application. Linux desktop entries
use `Terminal=false`, macOS uses an application bundle, and Windows launchers
use the GUI PE subsystem so no console window is created. `true` retains the
console/terminal behavior required by command-line applications.

## Complete Builder configuration

```toml
[project]
name = "My Application"
id = "com.example.my-application"
version = "1.0.0"
entry = "main.flx"
type = "desktop"
module_root = "src"

[toolchain]
path = "tools/fluxa"
# fluxa = "0.24.1" # optional exact version requirement

[build]
output = "dist"
target = "host"
terminal = true
assets = ["assets/**", "data/**"]
exclude = [".git/**", "dist/**", "tests/**", "*.log"]
persistent = ["save.db", "captures/**"]
export = ["captures/**"]

[package]
format = "portable"
compress = true
sign = false
embed = false
include_source = false

[targets.windows]
icon = "assets/app.ico"

[targets.linux]
icon = "assets/app.png"

[targets.macos]
icon = "assets/app.icns"
bundle_id = "com.example.my-application"
```

All project content paths and patterns must be relative, must not contain `..`,
and must remain inside the project after symbolic links are resolved. Absolute,
drive-qualified, UNC, and NUL-containing paths are rejected.

Asset patterns select content for the build; they do not dictate how the source
project must be organized. `**` matches across directory boundaries. Selected
paths are normalized to `/`, sorted deterministically, and retained as logical
project paths. Overlapping patterns select a file only once. Case-only
collisions and selected symlinks that escape the project are rejected.

`build.persistent` declares runtime-generated files that survive application
restarts and package upgrades. These paths are stored under the platform user
data location, never in the temporary execution directory. A missing persistent
file does not need a seed asset: if the application creates it at runtime, it
will be retained. This is the recommended contract for SQLite databases:

```toml
[build]
assets = ["assets/**"]
persistent = ["application.db"]
```

`build.export` is the subset of persistent patterns whose real files should
also be visible to a non-technical user. Every export pattern must appear
exactly in `build.persistent`. For a portable application the files are copied
beside the executable; for an installer whose application directory is not
writable, they are copied to `~/Documents/<project.name>/`:

```toml
[build]
persistent = ["application.db", "cards/**"]
export = ["cards/**"]
```

Here the database stays internal while earned cards appear in a visible
`cards/` directory. Fluxa source paths cannot be persistent or exported.

The official Linux target is currently `linux-x64`. Its optional icon must be a
bounded, valid PNG. Linux uses
`$XDG_DATA_HOME/fluxa/<project.id>/project`, defaulting to
`~/.local/share/fluxa/<project.id>/project`. Windows uses
`%AppData%\fluxa\<project.id>\project`, and macOS uses
`~/Library/Application Support/fluxa/<project.id>/project`. Explicit exports may be mirrored to
the portable directory or Documents as described above. The runtime binary,
not the Builder, determines the glibc compatibility baseline.

macOS supports thin `macos-x64` and `macos-arm64` `.app` bundles. The optional
icon must be a valid ICNS container. `targets.macos.bundle_id` defaults to
`project.id`; universal binaries, Apple code signing, and notarization are
future distribution stages.

Every successful `linux-x64` build also emits a Debian `amd64` installer. Its
stable package identity is `project.id`; `project.version` becomes the Debian
version, and `build.terminal` is copied to the desktop entry. No additional
configuration switch is required.

The initial package formats accepted by configuration are `portable` and `zip`.
Only `portable` is part of the first functional milestone.

`package.include_source = true` is equivalent to the explicit
`--include-source` build option. It enables a development-only fallback while
Fluxa has no stable compile command. The resulting artifact contains readable
`.flx` files, keeps debug behavior, has no bytecode ABI, and is not a secure
release. The default `false` fails closed at compilation.

## Toolchain selection

The executable is selected in this order:

1. `fluxa-builder build --fluxa <path>`;
2. `toolchain.path`;
3. `FLUXA_HOME`;
4. `PATH`.

`toolchain.path` may be absolute or relative to the project root. `FLUXA_HOME`
may point either to the executable itself or to a directory containing
`fluxa[.exe]`.

The current Fluxa CLI does not report a language/runtime release number.
Therefore, setting `toolchain.fluxa` currently causes a clear compatibility
error after discovery: the Builder will not claim that an unreported version
matches. Once Fluxa exposes a machine-readable version, this field will require
an exact match.

## Output directory override

`build --output <dir>` overrides `build.output` for a single run without
editing `fluxa.toml`. The value is validated with the exact same rule as the
configured field: it must be relative, must not contain `..`, and must remain
inside the project root once resolved; anything else is rejected before any
work begins. `fluxa-builder init` uses this to preview an output directory and
optionally offers to persist the choice into `fluxa.toml` afterward.

## Build target override

`build.target` (default `"host"`) selects which platform a build targets;
an explicit value is `<os>-<arch>`, e.g. `"windows-x64"` or `"linux-x64"`.
`build --target <os>-<arch>` overrides it for a single run without editing
`fluxa.toml`, exactly like `--output` above; `fluxa-builder init`'s target
menu uses this to make a non-host choice actually take effect, since it
also reloads the project from disk before building.

Building for a target other than the host machine still runs the produced
application for real before publishing (this project's core safety
guarantee — see [`docs/architecture.md`](architecture.md#build-verification)):
when the host can't execute that target natively, verification runs
instead inside a network-isolated, resource-bounded Docker container. A
Linux host building `windows-x64` (or a Windows host building
`linux-x64`) needs Docker for this. Exact per-platform-pair status,
including a currently-unresolved Windows/Wine reliability limitation on
some hosts, is documented in
[ADR 0028](adr/0028-container-verified-cross-platform-builds.md). macOS
targets stay host-only in both directions — there is no container path
for macOS at all.

## Runtime registry

The default registry is `~/.fluxa-builder/runtimes`. It can be inspected and
populated with:

```sh
fluxa-builder runtime list
fluxa-builder runtime add ./fluxa-runtime --metadata ./runtime.json
```

Use `--registry <path>` on these commands or `--runtime-registry <path>` on
`build` for an exact alternate registry. `FLUXA_BUILDER_HOME` changes the
Builder home globally.

`runtime.json` schema v2:

```json
{
  "format_version": 2,
  "fluxa_version": "unreported",
  "toolchain_sha256": "<64 lowercase hex>",
  "package_format_version": 1,
  "bytecode_version": "",
  "bytecode_abi": "",
  "libraries_sha256": "<SHA-256 of fluxa.libs, or empty content>",
  "program_formats": ["fluxa-source"],
  "packaged": true,
  "os": "linux",
  "arch": "amd64",
  "terminal": true,
  "binary_name": "fluxa-runtime",
  "binary_sha256": "<64 lowercase hex>"
}
```

`packaged = true` marks a runtime source-package builds trust to receive the
launcher's private entry. On Windows this is a binary actually compiled with
`FLUXA_PACKAGED_RUNTIME=1`, which refuses public CLI commands such as `run`,
`dis`, `init`, and `apply` on its own. Neither native interpreter has such a
mode on Linux or macOS (they share the same source); Fluxa Builder assembles
a small embedded relay (`.fluxa-runtime`, see ADR 0025) that provides the
same private-entry-only behavior in front of the plain, registered
interpreter binary (`.fluxa-runtime.interpreter`).

`runtime add` is an explicit local trust decision. It verifies paths,
permissions, metadata, and hashes, but signed runtime provenance is not yet
implemented.

## Portable output

For `package.format = "portable"`, a successful host build is published as:

```text
<build.output>/<os>-<arch>/<application>/
├── <application>[.exe]
├── .fluxa-runtime[.exe]
├── <application>.flxpkg
└── build-info.json
```

On Linux and macOS, `.fluxa-runtime` is the embedded relay described above,
and a sibling `.fluxa-runtime.interpreter` (the actual registered binary) is
added alongside it.

The visible executable is the Fluxa Builder launcher. It verifies the sibling
package, restores packaged program files, preserves declared data, and invokes
the private runtime with `fluxa run <entry> -proj .`. The launcher implements
`--fluxa-package-self-test`; the registered language runtime does not need
native FLXPKG support. No output is published if assembly, package verification,
launcher self-test, or atomic publication fails. Existing output is never
overwritten.

For the official `windows-x64` target, the executable uses `.exe` and the
portable directory also contains deterministic `windows-version.json`. When
`targets.windows.icon` is configured, it must be a structurally valid ICO and
is copied as `<application>.ico`. Everything below is included in the
deterministic ZIP:

```text
<application>/
├── <application>.exe
├── <application>.exe.local   # DLL redirection marker for the Mesa fallback
├── .fluxa-runtime.exe
├── <application>.flxpkg
├── <application>.flxpkg.sig  # when signing is enabled
├── <application>.ico         # when configured
├── dxil.dll                  # Mesa3D software-rendering fallback
├── libgallium_wgl.dll        # Mesa3D software-rendering fallback
├── opengl32.dll              # Mesa3D software-rendering fallback
├── opengl32sw.dll            # Mesa3D software-rendering fallback
├── windows-version.json
└── build-info.json
```

The four DLLs and the `.local` marker are the Mesa3D software-rendering
fallback, bundled on every Windows build so `std.graph` still works on a
machine with no usable OpenGL driver. `<application>.exe.local` is what
makes them take effect: it enables Windows' per-application DLL
redirection, so the loader prefers these copies over the system
`opengl32.dll`. Without it the DLLs would ship and be ignored. See
[`distribution.md`](distribution.md#windows-mesa3d-software-rendering-fallback).

The Windows metadata records product name, project ID, semantic version,
architecture, terminal mode, filenames, and verified runtime/package/icon
hashes.

The Builder does rewrite the *launcher* PE it ships — patching its
subsystem for `build.terminal` and embedding the configured icon into its
resources ([ADR 0026](adr/0026-file-manager-icon-association.md)) — but
never the registered runtime PE, which is copied unmodified. Authenticode
code signing and Windows installers remain separate future formats.
