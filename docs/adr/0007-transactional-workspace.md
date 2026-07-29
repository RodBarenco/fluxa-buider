# ADR 0007: Use a project-local transactional workspace

- Status: Accepted
- Date: 2026-07-29

## Decision

Each build receives a cryptographically random workspace:

```text
<project>/.fluxa-builder/work/<128-bit-id>/
├── compiled/
├── package/
├── runtime/
├── output/
└── report/
```

Builder control directories and transaction directories use owner-only
permissions where the operating system supports POSIX modes. Existing symlinks
in Builder control paths are rejected.

The workspace is removed after success or failure. `--keep-work` retains it
only for explicit debugging.

Completed artifacts are created under the workspace `output/` directory and
published with a same-filesystem rename. Publication rejects an existing
destination and any source or destination outside approved roots.

## Consequences

Intermediate files are never written directly into the final distribution
directory. A failed build cannot expose a partial release through the Builder.
Because the workspace is project-local, publication normally remains on the
same filesystem and can use an atomic rename.

External programs modifying the final destination concurrently are outside the
transaction protocol. The Builder checks for an existing destination
immediately before rename and never intentionally overwrites one.
