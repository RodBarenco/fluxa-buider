# ADR 0022: Debian installer

Status: accepted

## Scope

Linux x64 builds additionally produce a Debian 2.0 package:

```text
<project.id>_<version>_amd64.deb
<project.id>_<version>_amd64.deb.sha256
```

The package is generated directly by Fluxa Builder as a deterministic `ar`
archive containing `debian-binary`, `control.tar.gz`, and `data.tar.gz`. No
external packaging tool is required for generation.

## Installation layout

```text
/opt/<project.id>/<portable files>
/usr/bin/<application>
/usr/share/applications/<project.id>.desktop
/usr/share/pixmaps/<project.id>.png  (when configured)
```

The `/usr/bin` entry executes the immutable integrated launcher under `/opt`,
where its private runtime and sibling FLXPKG remain available. The desktop entry
preserves `build.terminal` and references the pixmap by stable project ID.

The package contains no maintainer scripts. Installation and removal therefore
cannot start the application, mutate a home directory, or remove configuration,
saves, state, logs, or caches. Authoritative user data remains governed by the
XDG policy from ADR 0020; explicit exports fall back to Documents because
`/opt` is not user-writable.

## Verification

Unit tests parse both ar and tar layers, compare independent builds for exact
reproducibility, and verify paths, launchers, metadata, modes, and checksums.
When `dpkg-deb` is available, the generated package must be accepted and
extracted by the system tool.

The dedicated Ubuntu CI gate installs the package with `dpkg`, runs the
installed launcher and package self-test, removes the package, and confirms
that XDG user data remains.

RPM is the next Linux installer backend. AppImage, Flatpak, Snap, and signed
repositories remain deferred.
