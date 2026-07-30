# ADR 0015: Package self-test protocol

Status: accepted

## Decision

Before publication, the staged application is executed directly, without a
shell, from its portable directory:

```text
<application> --fluxa-package-self-test
```

The command is non-interactive and must not create an application window. Its
stdout contains exactly one JSON document:

```json
{
  "protocol": "fluxa-package-self-test-v1",
  "package_sha256": "<64 lowercase hex characters>",
  "package_opened": true,
  "vm_compatible": true,
  "ui_opened": false
}
```

All fields are required and unknown fields are rejected. `package_sha256` must
equal the package hash independently verified by the Builder. Diagnostic logs
belong on stderr.

## Bounds and failure handling

The default deadline is 10 seconds. Stdout is bounded to 64 KiB and stderr to
1 MiB. The Builder captures both streams and classifies timeout, cancellation,
abnormal termination, nonzero exit, excessive output, malformed protocol, hash
mismatch, and VM incompatibility.

An exit code of zero without the valid confirmation document is a failure.
Neither a failed self-test nor a target that cannot run on the build host is
published to `dist`.

## Consequences

The integrated application launcher implements this protocol. The private
Fluxa language runtime does not need native FLXPKG support.
