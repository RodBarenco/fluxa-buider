# ADR 0008: Separate logical project paths from release layout

Status: accepted

## Context

Fluxa resolves modules and resources from project paths. A distributor-facing
ZIP benefits from a predictable layout with separate executable, package,
assets, and build metadata. Automatically moving media based on file extension,
however, would break programs that open the original path.

## Decision

The collector preserves every selected file's normalized project-relative
logical path. It also records a logical kind, currently entry, module, or asset.
Future manifest work may add media metadata such as audio, image, font, video,
or data without changing that path.

The intended release layout is:

```text
Application-version-target/
├── Application[.exe]
├── package/
│   └── Application.flxpkg
├── assets/
│   ├── audio/
│   ├── images/
│   ├── fonts/
│   ├── video/
│   └── data/
└── build-info.json
```

Physical categorization under `assets/` is allowed only after the package
manifest and Fluxa runtime define a verified logical-to-physical lookup. Until
then, portable output must preserve logical paths exactly. The Builder never
requires the source project itself to use the release layout.

## Consequences

- Project organization remains under the project author's control.
- Existing resource references remain valid.
- Release archives can gain a clean stable layout without encoding that layout
  into source projects.
- Extension-based reorganization is deferred until runtime lookup supports it.
