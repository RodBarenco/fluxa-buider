# ADR 0003: Make terminal mode explicit

- Status: Accepted for configuration design
- Date: 2026-07-29

## Decision

The Builder project configuration will expose `build.terminal` as a boolean,
defaulting to `true`.

## Consequences

Target formatters decide how the preference maps to native artifacts. In
particular, Windows packaging can distinguish console and GUI subsystems.
Targets that cannot implement the distinction must retain deterministic,
documented behavior rather than silently guessing.
