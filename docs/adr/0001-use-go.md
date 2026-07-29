# ADR 0001: Use Go

- Status: Accepted
- Date: 2026-07-29

## Decision

Implement Fluxa Builder in Go.

## Context

The Builder needs a portable CLI, filesystem and process APIs, hashing,
compression, deterministic binary I/O, and simple cross-platform distribution.

## Consequences

The Builder can be distributed as a single native executable. The project uses
the Go standard library by default and adds dependencies only when they provide
clear value.
