# Fluxa Builder

Fluxa Builder is an independent CLI that will turn Fluxa projects into
standalone, distributable artifacts. It orchestrates the official Fluxa
toolchain; it does not reimplement the Fluxa parser, resolver, bytecode engine,
or runtime.

The Builder currently validates and packages a tested Fluxa project, selects a
verified compatible runtime, builds an integrated application launcher,
smoke-tests the portable application, and emits a deterministic ZIP or tar.gz
with an external checksum.

`--keep-work` retains `.fluxa-builder/work/<build-id>` for debugging. It should
not be used in routine builds.

## Fluxa project validation

Automatic Fluxa preflight is intentionally disabled. The current Fluxa
preflight command is experimental and may execute the program after successful
validation. Users must test their Fluxa project before running the Builder.
Future reports will record this as `preflight: not_run`, not as a successful
Builder validation.

Run the project explicitly before packaging:

```sh
fluxa run main.flx -proj .
```

Replace `main.flx` with `project.entry` and `.` with the project root when
needed. The Builder does not run this command automatically because it executes
the application with its real side effects and may be long-running.

Project configuration is documented in `docs/configuration.md`. The complete
distribution workflow, including persistent databases and user-visible exports,
is in `docs/distribution.md`.

## Requirements

- Go 1.24 or newer
- `golangci-lint` 2.x for the optional lint target

## Build

```sh
make build
./bin/fluxa-builder version
```

## Guided setup

```sh
fluxa-builder init
```

Detects the host platform, asks for the project directory, and helps fill in
`fluxa.toml` before writing anything — every write is shown and confirmed
first. It covers the required `[project]` fields (`name`, `id`, `version`,
`entry`) and, once the project loads, the rest of the Builder-specific
surface a project usually wants: `build.assets`, `build.exclude`,
`build.persistent`/`build.export`, `package.include_source`, the host
platform's `targets.<os>.icon` (and `bundle_id` on macOS), and optionally
`toolchain.path`. Every field already set in `fluxa.toml` is left untouched
and simply skipped. It also previews the output location (defaulting to the
project's configured `dist`). If a Fluxa toolchain and a registered runtime
are already available it runs the real build; otherwise it prints the manual
setup steps (cloning `fluxa-lang`, `make build-packaged`, `runtime add`) and a
starting `runtime.json` template. Automatic download-and-build of the
toolchain is intentionally not implemented yet; see
`docs/adr/0024-interactive-init-wizard.md`.

On Windows:

```powershell
go build -trimpath -o bin/fluxa-builder.exe ./cmd/fluxa-builder
.\bin\fluxa-builder.exe version
```

## Validation

```sh
go test ./...
go test -race ./...
go vet ./...
golangci-lint run
```

The CI runs tests, the race detector, vet, build, and a version smoke test on
Linux, Windows, and macOS. Native release gates additionally execute the
official Windows x64 and Linux x64 portable pipelines.

For the complete packaging workflow in Portuguese, see
[Manual do Usuário](docs/manual-do-usuario.md).

The Linux portable layout is distributed as a deterministic tar.gz. It contains
the named launcher, a private Fluxa runtime, and the verified FLXPKG. Internal
persistent data uses XDG; explicitly exported user files appear beside a
writable portable application or under Documents for read-only installations.
glibc compatibility follows the selected runtime binary; the Builder does not
relink or rewrite it. That private runtime is itself two files: a small
Builder-generated relay (`.fluxa-runtime`) that speaks the private launcher
protocol, and the verified, registered interpreter beside it
(`.fluxa-runtime.interpreter`) — the native Linux interpreter has no private
mode of its own, unlike Windows. macOS uses the identical relay (it shares
the same native interpreter source as Linux), cross-compiled per
architecture and placed the same way inside `Contents/MacOS/`; unlike
Linux, this has not yet been verified end to end on real macOS hardware.
See `docs/adr/0025-linux-adapted-runtime-wrapper.md`.

macOS builds produce a conventional `.app` containing a named launcher and
private thin x64 or arm64 Mach-O runtime under `Contents/MacOS`, the FLXPKG
under `Contents/Resources`, deterministic `Info.plist`, and an optional
validated ICNS icon. The unsigned bundle is a development artifact; public
distribution still requires Developer ID signing, hardened runtime,
notarization, and ticket stapling.

Linux x64 builds also emit a deterministic `.deb`. It installs immutable
application files under `/opt/<project.id>`, a launcher under `/usr/bin`, a
desktop entry, and the optional PNG under `/usr/share/pixmaps`. The package has
no install/remove scripts and never deletes XDG user data.

## Current scope

The verified development build is:

```sh
fluxa-builder build . --include-source
```

It produces a directly launchable portable application containing the
integrated launcher, compatible private Fluxa runtime, verified package, build
report, deterministic archive, and the native installer currently implemented
for the host target. The protected release path remains blocked on stable Fluxa
bytecode export.

Fluxa does not yet expose the stable compile command required for a protected
release. For development of the remaining packaging pipeline only,
`--include-source` stages `.flx` program files and prints a source-exposure
warning. Without that explicit option, compilation fails closed.

Generated `.flxpkg` files can be checked independently:

```sh
fluxa-builder inspect application.flxpkg
fluxa-builder verify application.flxpkg
```

Packages may be signed with the raw 64-byte Ed25519 private key generated by
`fluxa keygen`:

```sh
fluxa-builder build . --sign-key /secure/signing.key
```

`FLUXA_SIGN_KEY` may contain the key path instead. The explicit flag takes
precedence. An explicit output directory can also override `build.output` for
one run without editing `fluxa.toml`, subject to the same relative,
no-traversal, stays-inside-the-project rule:

```sh
fluxa-builder build . --output build-output
```

Verify a signed package with the trusted raw 32-byte public key:

```sh
fluxa-builder verify application.flxpkg --public-key /trusted/signing.pub
```

The detached signature defaults to `application.flxpkg.sig`; use
`--signature <path>` to select another file.

For runtimes that implement the embedded footer contract, a single-file build
can be requested explicitly:

```sh
fluxa-builder build . --embed
```

The Builder appends the verified FLXPKG and a versioned footer to the runtime,
reopens and verifies the final executable, then runs the same non-interactive
self-test before publication. Until a future signed-footer format exists,
`--embed` and `--sign-key` are intentionally mutually exclusive.

Verified runtime binaries are managed locally:

```sh
fluxa-builder runtime list
fluxa-builder runtime add ./fluxa-runtime --metadata ./runtime.json
```

With a compatible registered runtime, `fluxa-builder build . --include-source`
publishes a portable directory whose named executable can be opened directly.
The source option remains development-only. The integrated launcher supports
FLXPKG v1 and `--fluxa-package-self-test`; the Fluxa language runtime remains a
normal script runtime and is invoked with `run <entry> -proj .`.

Stateful applications declare runtime-generated data explicitly:

```toml
[build]
terminal = false
persistent = ["application.db", "cards/**"]
export = ["cards/**"]
```

The database survives restarts without being shipped as an asset. Exported
cards remain persistent and are also copied to a user-visible `cards/`
directory.

Distributed source packages require a Fluxa runtime built with
`make build-packaged` and registered with `"packaged": true`. The shipped
private runtime refuses direct commands such as `run arbitrary.flx`; only the
verified application launcher can use its private execution entry.

## Versioning

Fluxa Builder follows Semantic Versioning. While the version is below `1.0.0`,
minor releases may change unstable CLI and package contracts. The `.flxpkg`
format, runtime compatibility identity, and manifest schema will each carry
their own explicit version.
