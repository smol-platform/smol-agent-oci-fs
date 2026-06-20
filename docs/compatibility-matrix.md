# Registry Compatibility Matrix

| Target | Mode | Status | Evidence |
| --- | --- | --- | --- |
| In-process OCI Distribution fixture | `image` | Supported | `TestPushPullSnapshotThroughOCIRegistry` pushes blobs/config/manifests, resolves tags, pulls parent chains, and restores. |
| Docker Distribution `registry:2` | `image` | Expected compatible | Uses the same OCI Distribution endpoints as the in-process fixture: blob upload/download and manifest push/pull. |
| Generic OCI registry with custom media types | `image` | Expected compatible | OSIx publishes normal OCI image manifests with custom config/layer media types. |
| Referrer/hybrid registry mode | `hybrid` | Partially implemented | Snapshot image manifests are supported. OSIx signature/provenance artifacts exist locally; registry Referrers API publication is documented as future hardening. |

## Fallback Behavior

The v0 CLI defaults to image-manifest compatibility. This means a snapshot is restorable from the manifest, config blob, and layer blobs without requiring OCI 1.1 Referrers API support.

## Known Registry Limits

- Tag movement is optimistic and uses local expected-parent checks before push.
- Registry-side compare-and-swap is not portable across all registries.
- Sigstore/cosign-compatible local signing is implemented, but full Sigstore registry artifact emission is future work.
- Encrypted lazy random access is not implemented; encrypted layers are downloaded and decrypted as whole blobs.

