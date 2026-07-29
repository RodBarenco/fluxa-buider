# ADR 0012: Treat every FLXPKG as hostile input

Status: accepted

## Decision

Package verification accepts an `io.ReaderAt` plus an explicit size internally,
allowing the same bounded parser to serve files, memory tests, and fuzzing.
Filesystem paths are only an outer transport concern.

Validation proceeds from cheap structure to expensive content:

1. physical size, magic, version, flags, and reserved signature;
2. contiguous region layout with overflow checks;
3. strict bounded manifest decode;
4. bounded table parsing and manifest correlation;
5. streamed payload decompression, size, and per-entry hash;
6. global body hash and complete-file hash.

This ordering provides specific diagnostics for malformed structures instead of
hiding every corruption behind the global hash.

Compressed entries are limited by stored and original size, declared expansion
ratio, exact decompressed size, and a single zlib stream. Trailing compressed
bytes and concatenated streams are rejected.

## Fuzzing

`FuzzPackageReader` operates directly on byte slices and includes seeds for:

- minimal valid package;
- complete compressed package;
- truncated package;
- random header;
- corrupted table;
- corrupted manifest.

The required final 60-second campaign completed without panic, hang, or
uncontrolled allocation and exercised more than 4.4 million inputs.
