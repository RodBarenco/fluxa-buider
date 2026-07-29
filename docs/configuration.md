# Project Configuration

Fluxa Builder reads `fluxa.toml` from the project root. Builder-specific tables
coexist with Fluxa's existing `[runtime]`, `[libs]`, `[ffi]`, and `[security]`
tables. The current Fluxa loader ignores Builder tables and additional project
metadata it does not use.

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

`build.terminal = false` requests an artifact that does not open or require a
terminal on targets supporting that distinction. The setting becomes effective
when target-specific packaging is implemented.

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

The initial package formats accepted by configuration are `portable` and `zip`.
Only `portable` is part of the first functional milestone.

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
