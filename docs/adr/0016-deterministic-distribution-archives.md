# ADR 0016: Deterministic distribution archives

Status: accepted

## Decision

After a successful package self-test, the Builder creates:

- `<application>.zip` for Windows;
- `<application>.tar.gz` for Linux and macOS;
- `<archive>.sha256` beside either archive.

Each archive contains one top-level `<application>/` directory with exactly the
portable executable, FLXPKG, and `build-info.json`. No symlink, special file, or
unexpected entry is accepted as archive input.

## Reproducibility

Entries are sorted by name. ZIP timestamps are normalized to
`1980-01-01T00:00:00Z`, the earliest portable ZIP timestamp. tar and gzip
timestamps are Unix epoch. Gzip has no source filename and uses a fixed OS
header. File modes come from the verified portable directory.

The external checksum uses the conventional deterministic form:

```text
<sha256>  <archive filename>
```

Thus identical portable inputs built with the same Builder version produce
byte-identical archives.

## Publication

The portable directory, archive, and checksum are first created under one
target staging directory in the transactional workspace. The complete target
directory is published with one atomic rename only after the smoke test and
archive creation succeed. An existing target output is never replaced.

Archive extraction is not a Builder runtime feature. Test extraction still
validates every entry path before writing and explicitly rejects absolute
paths, traversal components, and backslash-based ZIP Slip paths.
