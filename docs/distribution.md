# Distribution Guide

This guide describes the currently verified path from a tested Fluxa project
to an application a non-technical user can open directly.

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

Build the distribution variant of Fluxa with:

```sh
make FLUXA_GRAPH_RAYLIB=1 build-packaged
```

Choose the target-specific backend flags required by the application. The
resulting `fluxa_runtime` is not a development CLI. Direct commands such as
`fluxa_runtime run arbitrary.flx` exit with code 126.

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
│   ├── <application>.flxpkg
│   ├── build-info.json
│   └── linux-runtime.json
├── <application>.tar.gz
├── <application>.tar.gz.sha256
├── <project.id>_<version>_amd64.deb
└── <project.id>_<version>_amd64.deb.sha256
```

The visible application executable is the launcher. A user does not install
Fluxa, run Fluxa Builder, open a terminal, or know about FLXPKG.

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
