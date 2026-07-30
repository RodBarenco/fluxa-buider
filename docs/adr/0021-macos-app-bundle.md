# ADR 0021: macOS application bundle

Status: accepted

## Official targets

Fluxa Builder supports thin `macos-x64` and `macos-arm64` application bundles.
Each build requires a matching verified runtime and is smoke-tested only on a
host with the same architecture. Universal binaries remain future work.

On macOS, registered runtimes are parsed as untrusted Mach-O input. The runtime
must be a thin executable matching the selected x86-64 or arm64 architecture.
The Builder copies it without relinking or modifying load commands.

## Bundle layout

```text
application.app/
└── Contents/
    ├── MacOS/
    │   └── application
    ├── Resources/
    │   ├── application.flxpkg
    │   ├── application.flxpkg.sig  (optional)
    │   ├── AppIcon.icns            (optional)
    │   └── build-info.json
    └── Info.plist
```

The executable is mode `0700`; resources and metadata are `0600`. `Info.plist`
is deterministic XML and records display name, bundle identifier, executable,
semantic version, package type, and optional icon. If
`targets.macos.bundle_id` is omitted, `project.id` is used.

ICNS input is bounded, must be a regular non-symlink file, and every container
chunk is boundary checked. At least one recognized icon chunk is required, and
the copied icon is validated again.

## Native gate and archive

The macOS CI gate uses its real test Mach-O as the runtime, confirms its exact
architecture, builds a bundle under a Unicode path with spaces, parses the
plist, executes the package self-test from `Contents/MacOS`, confirms package
discovery under `Contents/Resources`, and validates the complete deterministic
tar.gz hierarchy.

Code signing, hardened runtime entitlements, notarization, DMG/PKG installers,
and universal binaries are intentionally deferred. Ed25519 package signatures
remain distinct from Apple code signing.

