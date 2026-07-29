# Fluxa Builder

Fluxa Builder is an independent CLI that will turn Fluxa projects into
standalone, distributable artifacts. It orchestrates the official Fluxa
toolchain; it does not reimplement the Fluxa parser, resolver, bytecode engine,
or runtime.

The project is in its initial implementation phase. The only command currently
available for a complete operation is:

```text
fluxa-builder version
```

`fluxa-builder build [project] [--fluxa <path>] [--keep-work]` currently loads
and validates `fluxa.toml`, locates and probes the Fluxa toolchain, creates and
cleans an isolated transactional workspace, then stops before file collection.

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

Project configuration is documented in `docs/configuration.md`.

## Requirements

- Go 1.24 or newer
- `golangci-lint` 2.x for the optional lint target

## Build

```sh
make build
./bin/fluxa-builder version
```

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
Linux, Windows, and macOS.

## Scope

The first functional milestone will run:

```text
fluxa-builder build . --release --target host
```

and produce a portable application containing a compatible Fluxa runtime,
package, and build report. Implementation proceeds one reviewed phase at a
time; see `PLANO_COMPLETO_FLUXA_BUILDER_AGENT.md`.

Fluxa does not yet expose the stable compile command required for a protected
release. For development of the remaining packaging pipeline only,
`--include-source` stages `.flx` program files and prints a source-exposure
warning. Without that explicit option, compilation fails closed.

Generated `.flxpkg` files can be checked independently:

```sh
fluxa-builder inspect application.flxpkg
fluxa-builder verify application.flxpkg
```

Verified runtime binaries are managed locally:

```sh
fluxa-builder runtime list
fluxa-builder runtime add ./fluxa-runtime --metadata ./runtime.json
```

## Versioning

Fluxa Builder follows Semantic Versioning. While the version is below `1.0.0`,
minor releases may change unstable CLI and package contracts. The `.flxpkg`
format, runtime compatibility identity, and manifest schema will each carry
their own explicit version.
