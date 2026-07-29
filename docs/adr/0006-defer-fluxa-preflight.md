# ADR 0006: Defer automatic Fluxa preflight

- Status: Accepted, temporary
- Date: 2026-07-29

## Context

The current Fluxa preflight command is experimental. It can reject working
projects, and a successful invocation of `fluxa run <entry> -p` currently
continues into normal program execution. Running it from the Builder would
therefore be both unreliable and unsafe for an untrusted project.

The disassembler is not an alternative: `fluxa dis` does not provide the same
resolver validation contract.

## Decision

Fluxa Builder will not invoke the current preflight command. Until Fluxa
provides a stable, non-executing validation contract, users are responsible for
testing their project before packaging it.

The Builder must:

- never report that Fluxa preflight passed;
- describe project validation as an external prerequisite;
- record future build reports as `preflight: not_run` or an equivalent explicit
  state;
- avoid a `--force` or silent fallback that suggests equivalent validation;
- add automatic preflight only after the Fluxa command is stable and proven not
  to execute user code.

## Consequences

Builder configuration and packaging validation still run normally, but they do
not prove that the Fluxa program parses, resolves, or executes correctly.
Automatic package creation may proceed under the explicit assumption that the
user already tested the project. This limitation must remain visible until the
Fluxa validation contract is ready.
