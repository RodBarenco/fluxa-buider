# ADR 0002: Use the external Fluxa toolchain

- Status: Accepted
- Date: 2026-07-29

## Decision

Fluxa Builder orchestrates the official Fluxa executable as an external
dependency. It will not copy or reimplement Fluxa language internals.

## Consequences

Fluxa remains the authority for parsing, resolution, preflight, bytecode, and
runtime behavior. Stable machine-readable commands will be required for
toolchain identity, compilation output, and runtime/package compatibility.
