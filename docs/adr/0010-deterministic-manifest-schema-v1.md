# ADR 0010: Deterministic manifest schema v1

Status: accepted

## Decision

The internal package manifest uses strict UTF-8 JSON with
`format_version: 1`. Encoding uses a fixed struct field order, two-space
indentation, a final LF, and lexicographically sorted file paths.

The schema records:

- project identity, version, type, and logical entry path;
- Fluxa probe protocol, reported version when available, executable hash, and
  raw `fluxa.libs` hash;
- bytecode version and ABI when a real compiler eventually provides them;
- resolved operating system, architecture, and terminal preference;
- preflight state, program format, debug state, and source exposure;
- package path, project logical path, kind, size, and SHA-256 for every file.

Program artifacts use the `program/` namespace. Resources use `resources/`
while retaining their original logical project path separately.

## Reproducibility and privacy

The manifest contains no timestamp, host name, user name, absolute source path,
workspace ID, or build-machine directory. Equal logical inputs therefore encode
byte-for-byte identically.

Decoding rejects unknown fields, unsupported schema versions, malformed hashes,
unsafe paths, duplicate or case-colliding paths, unsorted entries, trailing JSON
values, and input larger than 16 MiB. The file count is limited to 100,000.

Manifest publication uses a private temporary file, sync, close, and same-folder
rename, and refuses to replace an existing destination.
