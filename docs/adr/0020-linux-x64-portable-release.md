# ADR 0020: Linux x64 portable release

Status: accepted

## Official target

The first official Linux target is `linux-x64`, represented internally as
`linux/amd64`. Linux ARM64 remains fail-closed until it has a native runner and
release gate.

On a Linux host, a registered runtime is parsed as untrusted ELF input. It must
be ELF64 for x86-64, be an executable or PIE, have bounded program headers, and
contain an executable load segment. The verified runtime is copied unchanged.

## Portable layout

The deterministic tar.gz has one top-level application directory containing:

```text
application
application.flxpkg
application.flxpkg.sig   (optional)
application.png          (when configured)
linux-runtime.json
build-info.json
```

The runtime is mode `0700` while regular data is `0600`; archive modes are
derived from the target contract rather than the host filesystem. A configured
Linux icon must be a bounded, decodable PNG regular file and is verified again
after copying.

`linux-runtime.json` records application identity, x64 architecture, filenames,
verified hashes, `data_policy: xdg`, and `libc_policy: runtime-defined`.
Desktop files and AppImage remain future distribution layers.

## Writable data contract

The installation directory is immutable application content. A Fluxa runtime
must never save configuration, state, cache, logs, or user content beside the
executable or package. It must follow the XDG Base Directory variables:

- `XDG_CONFIG_HOME` for configuration;
- `XDG_DATA_HOME` for persistent application data;
- `XDG_STATE_HOME` for state and logs;
- `XDG_CACHE_HOME` for disposable cache.

The native Ubuntu gate makes the complete installation tree read-only, runs the
real ELF self-test from a path containing spaces and Unicode, verifies an XDG
write, and confirms that no installation byte changed.

## glibc compatibility

The Builder does not link or rewrite the runtime and therefore cannot truthfully
declare a glibc baseline. Compatibility is defined by the registered Fluxa
runtime binary. Official runtime publishers must build on the oldest supported
glibc baseline, record that policy in their release process, and test on every
supported distribution. Static or musl runtimes require their own declared
runtime identity in a future metadata revision.

