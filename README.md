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

`fluxa-builder build [project] [--fluxa <path>]` currently loads and validates
`fluxa.toml`, locates and probes the Fluxa toolchain through the secure command
executor, then stops before creation of the transactional build workspace.

## Fluxa project validation

Automatic Fluxa preflight is intentionally disabled. The current Fluxa
preflight command is experimental and may execute the program after successful
validation. Users must test their Fluxa project before running the Builder.
Future reports will record this as `preflight: not_run`, not as a successful
Builder validation.

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

## Versioning

Fluxa Builder follows Semantic Versioning. While the version is below `1.0.0`,
minor releases may change unstable CLI and package contracts. The `.flxpkg`
format, runtime compatibility identity, and manifest schema will each carry
their own explicit version.
