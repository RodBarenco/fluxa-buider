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

## Amendments

**Metadata is now schema v2** (`format_version: 2`), which added the
`packaged` flag to the field list above — the assertion that a runtime
accepts only the launcher's private execution entry and refuses the public
CLI. Source-package builds require it (ADR 0009, ADR 0025). The canonical
field-by-field reference is
[`docs/configuration.md`](../configuration.md#runtime-registry).

**Occupied slots can be replaced, with confirmation.** The directory
layout above keys a slot on version/target/terminal, while Compatibility
additionally demands the exact toolchain and `fluxa.libs` hashes. Those
two keys differ in practice precisely because Fluxa reports no version
(ADR 0004): every runtime built today lands in the same `unreported` slot
for a given target, so two runtimes that are genuinely incompatible — from
projects with different `fluxa.libs`, or built against different
toolchains — collide, and `Add` refuses to overwrite.

Leaving that unresolvable made the automatic acquisition of ADR 0027 a
dead end on any second project: it would rebuild a correct runtime for 25
minutes and then be unable to register it. `runtime.Remove` deletes a slot,
re-validating that the directory really is a registry slot inside the
registry root before touching anything. It is deliberately not exposed as
a CLI verb; `fluxa-builder init` is its only caller, and only after
resolution has already proven the occupant unusable for the build in hand,
and only with an explicit confirmation. This whole amendment becomes
unnecessary once Fluxa reports a real version, which makes slot identity
and compatibility identity agree again.
