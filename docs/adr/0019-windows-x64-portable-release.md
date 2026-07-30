# ADR 0019: Windows x64 portable release

Status: accepted

## Official target

The first official Windows target is `windows-x64`, represented internally as
`windows/amd64`. Windows ARM64 remains fail-closed until it receives its own
platform phase and native test coverage.

A Windows build requires a verified registry runtime named
`fluxa-runtime.exe`. On a Windows host the Builder parses it as PE32+, requires
the AMD64 machine type, executable-image characteristics, no DLL flag, and a
bounded section count. The Builder copies the runtime without rewriting or
packing the PE.

## Portable layout

The deterministic ZIP has one top-level application directory containing:

```text
application.exe
application.flxpkg
application.flxpkg.sig   (optional)
application.ico          (optional)
windows-version.json
build-info.json
```

Configured ICO files are treated as untrusted: symlinks and special files are
rejected, directory/image bounds are checked, and every payload must be PNG or
a supported DIB header. The copied icon is reopened and validated.

`windows-version.json` is deterministic and records application identity,
semantic version, x64 architecture, terminal mode, filenames, and verified
runtime, package, and icon hashes. It is intended for future installer and PE
resource stages.

## Native Windows gate

CI has a dedicated Windows x64 integration test in addition to the complete
test suite. It uses a real PE test runtime and performs:

- portable build and FLXPKG verification;
- actual `--fluxa-package-self-test` process execution;
- `.exe`, ICO, version metadata, and ZIP validation;
- paths with spaces, Unicode user/directory names, and a long directory;
- rejection of a tampered sibling package;
- exact, traversal-free archive structure.

Cross-compilation on non-Windows hosts checks build compatibility but does not
replace this native execution gate.

## Deferred Windows formats

Project-specific PE resource rewriting, Authenticode code signing, and an
installer are deliberately deferred. They must not be confused with the
Phase 15 Ed25519 package signature. Preserving the verified runtime PE and
using a transparent sibling layout also avoids packer/self-modifying patterns
that commonly trigger antivirus heuristics.
