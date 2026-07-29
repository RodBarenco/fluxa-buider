# ADR 0017: Ed25519 package signatures

Status: accepted

## Key format and trust

Fluxa Builder accepts the format emitted by `fluxa keygen`:

- `signing.key`: 64 raw Ed25519 private-key bytes;
- `signing.pub`: 32 raw Ed25519 public-key bytes.

The private key is loaded only from the external path passed with `--sign-key`
or `FLUXA_SIGN_KEY`. The explicit option takes precedence. It is never copied
to the workspace, output, report, or log. On Unix, group or other permissions
on a private key cause a fail-closed error.

Verification always requires an externally supplied trusted public key. A
signature cannot establish trust by embedding its own key.

## Detached signature format

`<package>.sig` is canonical indented JSON with:

```json
{
  "format_version": 1,
  "algorithm": "Ed25519",
  "key_id": "<sha256 of raw public key>",
  "package_sha256": "<verified FLXPKG sha256>",
  "signature": "<base64 Ed25519 signature>"
}
```

The signed message is the ASCII domain `fluxa-package-signature-v1`, one NUL
byte, and the 32 raw bytes of the final package SHA-256. Domain separation
prevents the same signature from being reused by another protocol.

## Verification and distribution

`fluxa-builder verify <package> --public-key <signing.pub>` verifies FLXPKG
integrity, the sidecar format, key ID, package hash, and Ed25519 signature. A
custom sidecar can be selected with `--signature`.

Signed portable directories and their deterministic archives include the
sidecar. `build-info.json` records its filename, SHA-256, and signing key ID;
the public and private keys are not distributed automatically.
