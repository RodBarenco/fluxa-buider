# ADR 0014: Portable directory format

Status: accepted

## Layout

The first standalone output is:

```text
dist/<os>-<arch>/<application>/
├── <application>[.exe]
├── <application>.flxpkg
└── build-info.json
```

The application name is a deterministic lowercase filesystem-safe form of the
display name, with the final project ID component as fallback. Unicode letters
and digits are retained; spaces, hyphens, and underscores become one hyphen.

The runtime and package share the same basename. This is the runtime discovery
convention for the sibling package.

## Assembly

Assembly occurs only under the transactional workspace. The Builder:

- re-verifies the input FLXPKG;
- validates runtime target and terminal mode;
- copies runtime and package while hashing;
- compares both hashes with their verified identities;
- sets executable permission for Unix targets;
- reopens and verifies the copied package;
- writes deterministic `build-info.json`;
- rejects unexpected files.

`build-info.json` records project identity, target, terminal mode, filenames,
runtime and package hashes, and source exposure.

## Smoke and publication

Before publication, the staged executable is run with:

```text
--fluxa-package-self-test
```

from its portable directory. The runtime must locate the sibling package,
validate compatibility, and exit zero without opening the application UI.
Cross-target artifacts cannot be executed by the current host and are therefore
not published yet.

Only a successful host smoke test allows the workspace directory to be renamed
atomically into `dist`. Existing destinations are never replaced.

The detailed response and failure contract is defined by ADR 0015.
