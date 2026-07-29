# ADR 0009: Source-package fallback is development-only

Status: accepted

## Context

The current Fluxa CLI can run, inspect, reload, and observe projects, but it
does not expose a stable non-executing command that compiles a project into
distributable bytecode. Fluxa Builder must not reimplement the language
compiler and must not claim that source files are protected when they are not.

## Decision

Normal release compilation fails closed until Fluxa provides a stable compiler
contract, including bytecode version and ABI identity.

An explicit `--include-source` flag, or `package.include_source = true`, enables
a temporary development fallback. It stages the collected `.flx` entry and
modules under `compiled/source/`, records their size and SHA-256 digest, keeps
debug behavior, leaves bytecode version and ABI unset, and marks the result as
`source-exposed`.

The CLI emits a prominent warning. This fallback must not be described as a
secure or source-protected release.

## Required Fluxa contract

The future compiler integration needs:

- a stable non-executing `fluxa compile` command;
- explicit output directory;
- machine-readable diagnostics;
- bytecode format version and ABI;
- deterministic module-to-artifact association;
- development/release debug controls;
- non-empty output validation.

## Consequences

- Default builds cannot accidentally distribute `.flx` files.
- Development packaging can proceed through later Builder phases.
- Release packaging remains blocked, visibly and intentionally.
- Bytecode failure, empty-output, version, ABI, and debug-stripping tests remain
  requirements for the real compiler adapter rather than being simulated.
