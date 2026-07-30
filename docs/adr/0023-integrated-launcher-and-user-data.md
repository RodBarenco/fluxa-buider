# ADR 0023: Integrated launcher and explicit user data

Status: accepted

## Context

The official Fluxa runtime is primarily a script runtime. Requiring it to
natively parse Builder-specific FLXPKG would couple language execution to a
distribution format and block real application testing.

Running every package from a disposable directory also loses databases,
settings, generated cards, and other user state. Persisting the whole extracted
project would make packaged source mutable and blur application files with
user-owned output.

## Decision

Portable output uses a Fluxa Builder launcher plus a private, verified Fluxa
runtime. The launcher owns FLXPKG verification, materialization, self-test, and
the call to:

```text
fluxa run <entry> -proj .
```

Project configuration defines two explicit data sets:

- `build.persistent`: runtime paths retained in platform user data;
- `build.export`: an exact subset of persistent patterns mirrored to a
  user-visible directory.

Packaged `.flx` files are refreshed on every launch and cannot be declared
persistent or exported. Missing persistent files may be created by the
application and do not require seed assets.

The launcher exports before application startup and again after a normal exit.
The first copy makes existing files immediately discoverable. The second copy
captures newly generated output. After a crash, authoritative state remains in
the persistent project and is exported on the next launch.

## Terminal mode

`build.terminal` is enforced by target packaging. Windows launchers use PE GUI
subsystem when false and Console subsystem when true. Linux desktop entries
copy the setting. macOS uses its application bundle.

## Consequences

- End users launch the named application without installing Fluxa.
- Runtime packages do not need native FLXPKG support.
- Databases and settings survive application replacement.
- User-owned output can be visible without exposing all internal state.
- Installed read-only application trees fall back to Documents for exports.
- The current source representation remains explicitly development-only.

