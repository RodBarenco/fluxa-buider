# Versioning Policy

Fluxa Builder uses Semantic Versioning:

- patch: compatible fixes;
- minor: compatible features, or pre-1.0 contract changes;
- major: incompatible stable CLI or configuration changes.

Development builds use the suffix `-dev`.

The following contracts are versioned independently:

- Builder CLI and configuration;
- `.flxpkg` binary format;
- package manifest schema;
- Fluxa bytecode ABI;
- runtime compatibility metadata.

An artifact must never be selected solely by a human-readable version string.
Compatibility decisions must use validated, machine-readable metadata.
