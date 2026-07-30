# ADR 0003: Make terminal mode explicit

- Status: Accepted for configuration design
- Date: 2026-07-29

## Decision

The Builder project configuration will expose `build.terminal` as a boolean,
defaulting to `true`.

## Consequences

Target formatters map the preference to native artifacts. Windows launchers use
PE GUI subsystem when false and Console subsystem when true. Linux desktop
entries use the corresponding `Terminal` value. macOS applications launch
through their bundle.
