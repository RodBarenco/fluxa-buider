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

`runtime.json` schema v1:

```json
{
  "format_version": 1,
  "fluxa_version": "unreported",
  "toolchain_sha256": "<64 lowercase hex>",
  "package_format_version": 1,
  "bytecode_version": "",
  "bytecode_abi": "",
  "libraries_sha256": "<SHA-256 of fluxa.libs, or empty content>",
  "program_formats": ["fluxa-source"],
  "os": "linux",
  "arch": "amd64",
  "terminal": true,
  "binary_name": "fluxa-runtime",
  "binary_sha256": "<64 lowercase hex>"
}
```

`runtime add` is an explicit local trust decision. It verifies paths,
permissions, metadata, and hashes, but signed runtime provenance is not yet
implemented.
