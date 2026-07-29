# ADR 0004: Transitional Fluxa toolchain identity

- Status: Accepted, transitional
- Date: 2026-07-29

## Context

The Fluxa CLI has no `--version` command. Its offline `fluxa runtime info`
command identifies the executable as Fluxa but currently prints runtime
configuration rather than a release or ABI version.

## Decision

The Builder probes a selected executable with:

```text
fluxa runtime info
```

The probe has a five-second timeout, limits stdout and stderr to 1 MiB each,
requires output beginning with `Fluxa Runtime`, and calculates SHA-256 over the
executable. The resulting identity protocol is `runtime-info-v1`.

If a future output contains a line in the form `Version: X`, the transitional
probe records it. Until then, any exact version requirement is rejected as
unverifiable.

## Consequences

The Builder can securely distinguish a responding Fluxa executable from an
arbitrary program and can record the exact binary used. It cannot yet establish
release or ABI compatibility. A future machine-readable Fluxa identity command
should replace this protocol before standalone artifacts are released.
