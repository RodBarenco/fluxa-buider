# ADR 0005: Centralize external command execution

- Status: Accepted
- Date: 2026-07-29

## Decision

Every external process launched by Fluxa Builder must use
`internal/executor.Run`. No other package may call `exec.Command` or
`exec.CommandContext` directly.

The executor:

- passes an executable and argument vector directly, without a shell;
- supports an explicit working directory and environment;
- defaults to a 30-second timeout;
- distinguishes timeout, caller cancellation, start failure, non-zero exit, and
  output-limit failure;
- captures stdout and stderr independently;
- defaults each output stream to a 1 MiB maximum;
- preserves the process exit code and bounded output on failure;
- records execution duration.

Callers may select other positive bounds and a command-specific timeout.

## Consequences

Shell metacharacters in project paths and arguments remain literal. A child
cannot force unbounded Builder memory growth through output. Toolchain
diagnostics retain stderr and exit status without passing command construction
through a shell.
