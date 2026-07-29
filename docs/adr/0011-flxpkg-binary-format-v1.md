# ADR 0011: FLXPKG binary format v1

Status: accepted

## Layout

All integers are unsigned little-endian values. The package is contiguous:

```text
160-byte header
canonical manifest JSON
binary file table
payload entries
```

The header contains the eight-byte magic `FLXPKG\r\n`, format version, flags,
absolute offset and size for the manifest, table, and payload, a SHA-256 of the
entire body after the header, and 64 reserved signature bytes.

The v1 signature and flags are zero. Future signed packages must define a new
compatible flag contract before using those bytes.

## File table

The table begins with a 32-bit entry count. Every entry stores:

- UTF-8 package path length and bytes;
- kind (`program` or `asset`);
- compression (`none` or deterministic zlib);
- reserved flags;
- absolute payload offset;
- stored and original size;
- SHA-256 of the original bytes.

Entries use the same strict lexicographic order and paths as manifest files.
Payloads are contiguous without gaps or overlap.

## Integrity and publication

The writer streams source data through SHA-256 and optional zlib into private
temporary files. It rejects any source whose type, size, or digest changed
after manifest creation. The final package is written to a private temporary,
synced, closed, reopened, and fully verified.

Publication uses a same-directory hard link so an existing destination is
never replaced, followed by directory sync where supported and removal of the
temporary name.

## Limits

- 100,000 entries;
- paths up to 4,096 bytes;
- manifest up to 16 MiB;
- table up to 512 MiB;
- original or stored entry up to 1 GiB;
- payload up to 16 GiB.

Readers reject non-contiguous regions, arithmetic overflow, trailing bytes,
unknown compression, mismatched manifest metadata, invalid hashes, truncated
payloads, and decompressed output larger than declared.
