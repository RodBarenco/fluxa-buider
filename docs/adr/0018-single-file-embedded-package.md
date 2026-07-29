# ADR 0018: Single-file embedded package

Status: accepted

## Layout

`build --embed` produces exactly one executable:

```text
runtime bytes
FLXPKG bytes
FluxaEmbeddedFooter
```

The footer is exactly 60 bytes, encoded little-endian without native structure
padding:

```c
unsigned char magic[8];          /* "FLXEMBED" */
uint32_t version;                /* 1 */
uint64_t package_offset;
uint64_t package_size;
unsigned char package_hash[32];  /* SHA-256 of complete FLXPKG */
```

## Validation

The Builder validates inputs before appending and then reopens the final
executable. It reads the footer from EOF, checks magic and version, performs
checked offset arithmetic, requires the package to end exactly where the footer
starts, hashes the embedded region, and validates the complete FLXPKG directly
through a bounded `ReaderAt`.

The exact EOF rule rejects both truncation and trailing bytes. Runtime,
package, and final executable have explicit size limits. Symlinks and special
files are rejected.

## Runtime contract and publication

An embed-capable Fluxa runtime opens its own executable, performs the same
footer and package validation, and answers `--fluxa-package-self-test` with the
hash of the embedded package. The Builder publishes the file atomically only
after its own verification and the runtime self-test both succeed.

This Builder implementation establishes and tests the binary contract. The
official Fluxa runtime must implement the reader before a real runtime can be
registered and used successfully with `--embed`.

The Phase 15 signature is a detached sidecar, which would violate the
single-file property and is not represented by the v1 footer. Therefore
`--embed` and `--sign-key` fail closed when combined. A future embedded
signature format requires a new footer version.
