# ADR 0013: Verified local runtime registry

Status: accepted

## Decision

Standalone builds select runtimes only from the local registry:

```text
~/.fluxa-builder/runtimes/<fluxa-version>/<os>-<arch>/<terminal-mode>/
├── runtime.json
└── fluxa-runtime[.exe]
```

`FLUXA_BUILDER_HOME` changes the Builder home. CLI and tests may use an exact
registry directory with `--registry` or `--runtime-registry`.

Runtime metadata schema v1 records:

- Fluxa version, or `unreported`;
- producing toolchain SHA-256;
- supported FLXPKG format version;
- bytecode version and ABI;
- `fluxa.libs` SHA-256;
- supported program representations;
- OS, architecture, and terminal mode;
- runtime filename and SHA-256.

Metadata is strict JSON with required fields. Registry paths may not traverse
symlinks. Runtime binaries are regular non-symlink files and Unix targets must
be executable.

## Compatibility

Target, terminal mode, package format, libraries, program format, bytecode
version, and bytecode ABI must match exactly. When Fluxa reports a version, that
version must match. While it remains unreported, selection requires the exact
toolchain hash, intentionally preventing unverifiable cross-target selection.

Every list and resolution operation rehashes the binary. A runtime modified
after registration is rejected.

## Trust boundary

`runtime add` verifies that metadata is internally valid and matches the binary
hash. It does not prove that the binary honestly implements the asserted ABI.
Signed runtime provenance remains a later requirement. Until then, adding a
runtime is an explicit local trust decision by the user.
