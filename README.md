# Fluxa Builder

**Turn a tested [Fluxa](https://github.com/RodBarenco/fluxa-lang) project into
a standalone application your users can just open — no Fluxa installed, no
terminal, no idea any of this happened.**

Fluxa Builder is an independent CLI that orchestrates the official Fluxa
toolchain to package, sign, and distribute Fluxa projects. It does not
reimplement the Fluxa parser, resolver, bytecode engine, or runtime — Fluxa
projects still run on the real Fluxa language.

## What it does

- Validates and packages a tested Fluxa project into a signed, verified
  `.flxpkg`.
- Selects a compatible, verified Fluxa runtime and builds an integrated
  application launcher around it, compiled for the target platform rather
  than for whichever machine ran the build.
- Smoke-tests the resulting portable application before publishing anything.
- Emits a deterministic archive (`.zip` on Windows, `.tar.gz`/`.deb` on
  Linux, `.app`/`.tar.gz` on macOS) with an external checksum.
- Associates the configured icon with the file itself, not just a loose icon
  file beside it — embedded in the `.exe`'s own resources on Windows,
  registered as a desktop entry automatically on Linux.
- On Windows, bundles a software-rendering fallback so graphical projects
  still run on a machine with no real GPU driver (common inside VMs).
- Can acquire the Fluxa toolchain and runtime for you: `fluxa-builder init`
  offers to clone and build `fluxa-lang` automatically (Linux and Windows,
  the latter cross-compiled from Linux — no Windows machine required),
  inside a pinned Docker container that never touches your own system
  packages.
- Builds and verifies cross-platform: a Linux host can produce a genuinely
  tested Windows build (and vice versa), running the produced application
  for real inside a network-isolated Docker container instead of requiring
  the host to run that target natively. See
  [ADR 0028](docs/adr/0028-container-verified-cross-platform-builds.md)
  for exact status per platform pair.

## Requirements

- Go 1.24 or newer, to build Fluxa Builder itself.
- A tested Fluxa project and, eventually, a Fluxa toolchain/runtime —
  `fluxa-builder init` can fetch and build these for you (see above), or
  guide you through doing it by hand.
- Docker, only if you want automatic toolchain acquisition, the Windows
  fallback bundling above, or to build for a target your host can't run
  natively (Docker verifies the produced application in a container
  instead). Everything else works without it.
- `git`, only for automatic toolchain acquisition — it clones `fluxa-lang`.
- `golangci-lint` 2.x, only for the optional lint target during development.

### Disk space and time

Packaging itself is cheap; the Docker-backed paths are not. Budget by what
you actually use:

| What | Size | When |
|---|---|---|
| `bin/fluxa-builder` | ~36 MB | always — it embeds a launcher per target |
| `~/.fluxa-builder/runtimes` | ~13 MB per registered runtime | always |
| `~/.fluxa-builder/mesa-dist-win` | ~75 MB | first Windows build, then cached |
| `~/.fluxa-builder/toolchain-built` | ~26 MB | automatic acquisition |
| `~/.fluxa-builder/toolchain-src` | ~50 MB while building | automatic acquisition |
| Wine verification image | ~2.4 GB | building Windows from a non-Windows host |
| Toolchain build images | ~730 MB Linux, ~880 MB Windows | automatic acquisition |
| Mesa extraction image | ~85 MB | first Windows build |

A machine that uses every Docker path should have roughly **5 GB** free.
Without Docker — a host-native build against a runtime you registered
yourself — the footprint is the Builder plus the registry, well under
100 MB.

Everything above is cached and reused. Only the first run pays for it, and
`fluxa-builder init` offers to delete the `fluxa-lang` checkout and the
toolchain images once acquisition finishes (keeping them makes later builds
much faster).

Time is dominated by the same first run: building `fluxa-lang` inside a
container takes on the order of 15 minutes per target, and a Windows build
from Linux does it twice (the runtime that ships, plus a host-native
compiler — see the User Manual). Later builds of the same project take
seconds, plus the container self-test for a cross-target build.

Produced artifacts are modest by comparison: a graphical Windows portable
directory runs around 130 MB before compression, most of it the bundled
Mesa3D `libgallium_wgl.dll` and the private runtime.

## Quick start

```sh
# 1. Build Fluxa Builder
make build

# 2. Point it at your tested Fluxa project
./bin/fluxa-builder init
```

The interactive wizard detects your platform, fills in the missing pieces of
`fluxa.toml` (asking before every write), gets you a toolchain and runtime if
you don't have one yet, and runs the real build. Answering "no" anywhere just
falls back to guidance — nothing is ever silently assumed.

Prefer to drive it by hand instead? See the non-interactive CLI reference
below, or the full walkthrough in the [User Manual](docs/user-manual.md).

On Windows, build with:

```powershell
go build -trimpath -o bin/fluxa-builder.exe ./cmd/fluxa-builder
.\bin\fluxa-builder.exe init
```

## What ends up in the package

| Platform | Format | Contains |
|---|---|---|
| Windows | `.zip` | launcher `.exe` (icon embedded), private runtime, FLXPKG, Mesa3D fallback DLLs, build metadata |
| Linux | `.tar.gz` and `.deb` | launcher, private runtime relay + interpreter, FLXPKG, desktop-entry install script, build metadata |
| macOS | `.app` bundle + `.tar.gz` | launcher, private runtime, FLXPKG under `Contents/Resources`, `Info.plist`, optional ICNS icon |

The visible executable is always the launcher — end users never see Fluxa,
FLXPKG, or a terminal. Full per-platform detail (exact file layout, icon
association, the Mesa fallback, the Linux runtime relay, macOS signing
caveats) is in [`docs/distribution.md`](docs/distribution.md) and the
relevant ADRs under [`docs/adr/`](docs/adr/).

## Before you package: test the project

Automatic Fluxa preflight is intentionally disabled — the current preflight
command is experimental and may execute the program. Test your project
yourself first:

```sh
fluxa run main.flx -proj .
```

Fluxa Builder assumes this already passed; it will not run it for you.
Project configuration is documented in
[`docs/configuration.md`](docs/configuration.md).

## Non-interactive CLI reference

Everything the wizard does can also be driven directly, e.g. for CI:

```sh
# Fluxa does not yet export protected bytecode, so a working build today
# requires staging readable .flx source explicitly:
fluxa-builder build . --include-source
```

Other useful flags and commands:

```sh
# Override the output directory for one run, without editing fluxa.toml
fluxa-builder build . --include-source --output build-output

# Build for a target other than the host machine, for one run
fluxa-builder build . --include-source --target windows-x64

# Sign the package (raw 64-byte Ed25519 key from `fluxa keygen`)
fluxa-builder build . --sign-key /secure/signing.key
fluxa-builder verify application.flxpkg --public-key /trusted/signing.pub

# Inspect a package without a full build
fluxa-builder inspect application.flxpkg

# Single-file build, for runtimes with the embedded-footer contract
# (mutually exclusive with --sign-key)
fluxa-builder build . --embed

# Manage the local, verified runtime registry
fluxa-builder runtime list
fluxa-builder runtime add ./fluxa-runtime --metadata ./runtime.json
```

`--keep-work` retains `.fluxa-builder/work/<build-id>` for debugging and
should not be used in routine builds.

Stateful applications declare runtime-generated data explicitly so it
survives restarts without being shipped as an asset:

```toml
[build]
terminal = false
persistent = ["application.db", "cards/**"]
export = ["cards/**"]
```

`export` entries are also copied to a user-visible location beside the
application. See [`docs/distribution.md`](docs/distribution.md) for the full
persistence and export model.

## Development

```sh
go test ./...
go test -race ./...
go vet ./...
golangci-lint run
```

CI runs tests, the race detector, vet, build, and a version smoke test on
Linux, Windows, and macOS. Native release gates additionally run the official
Windows x64 and Linux x64 portable pipelines end to end.

## Learn more

- [User Manual](docs/user-manual.md) ([Manual do Usuário](docs/manual-do-usuario.md) em português) — the full packaging workflow.
- [`docs/distribution.md`](docs/distribution.md) — exact per-platform output layout and mechanics.
- [`docs/configuration.md`](docs/configuration.md) — `fluxa.toml` reference.
- [`docs/architecture.md`](docs/architecture.md) — how the pipeline fits together internally.
- [`docs/adr/`](docs/adr/) — the design decisions behind all of the above, in detail.

## Current scope

The verified, working build mode is development/source-exposed
(`--include-source`): Fluxa does not yet expose the stable bytecode-export
command a protected release needs, so `.flx` source is staged readable and
the build prints an explicit warning. Without `--include-source`, compilation
fails closed rather than silently producing a broken protected build.

A distributed source package still requires a runtime registered with
`"packaged": true`, which refuses direct commands like `run arbitrary.flx`
— only the verified application launcher can use its private execution
entry. How that binary is produced differs by target: Windows needs
`fluxa-lang`'s own restricted build (`make build-windows-packaged`), while
Linux and macOS use the plain `make build` interpreter with the private
entry supplied by a small relay Fluxa Builder assembles itself
([ADR 0025](docs/adr/0025-linux-adapted-runtime-wrapper.md)). Either can be
acquired automatically, see above.

## Versioning

Fluxa Builder follows Semantic Versioning. While the version is below
`1.0.0`, minor releases may change unstable CLI and package contracts. The
`.flxpkg` format, runtime compatibility identity, and manifest schema each
carry their own explicit version.

## License

Fluxa Builder is released under the [MIT License](LICENSE).

This covers Fluxa Builder itself. The artifacts it produces bundle
third-party components under their own terms — notably the Fluxa runtime
you register, and, on Windows, the Mesa3D DLLs from
[`mesa-dist-win`](https://github.com/pal1000/mesa-dist-win). Check those
projects' licenses before redistributing a packaged application.
