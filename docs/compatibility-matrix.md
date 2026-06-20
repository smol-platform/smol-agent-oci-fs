# Registry Compatibility Matrix

| Target | Mode | Status | Evidence |
| --- | --- | --- | --- |
| In-process OCI Distribution fixture | `image` | Supported | `TestPushPullSnapshotThroughOCIRegistry` pushes blobs/config/manifests, resolves tags, pulls parent chains, and restores. |
| Docker Distribution `registry:2` | `image` + fallback artifact tags | Supported | `scripts/test-registry-docker.sh` starts `registry:2`, signs a snapshot, pushes it with `osix push`, pulls it with `osix pull`, verifies signature/provenance after pull, and restores the pulled ref. |
| Generic OCI registry with custom media types | `image` | Expected compatible | OSIx publishes normal OCI image manifests with custom config/layer media types. |
| Referrer/hybrid registry mode | `hybrid` | Partially implemented | `TestPushPullSignedSnapshotReferrersThroughOCIRegistry` publishes signature/provenance artifacts as subject-bearing manifests, pulls them through Referrers API discovery, and verifies after pull. Fallback tags are written for registries without referrer listing. |

## Fallback Behavior

The v0 CLI defaults to image-manifest compatibility. This means a snapshot is
restorable from the manifest, config blob, and layer blobs without requiring OCI
1.1 Referrers API support. Signature and provenance artifacts are also published
under deterministic fallback tags so verification can work on registries that
store subject-bearing manifests but do not list referrers.

## Known Registry Limits

- Tag movement is optimistic and uses local expected-parent checks before push.
- Registry-side compare-and-swap is not portable across all registries.
- Hosted authenticated registries still need explicit compatibility verification.
- OSIx-native signature/provenance artifacts are pushed and pulled, but full Sigstore registry artifact compatibility is future work.
- Encrypted lazy random access is not implemented; encrypted layers are downloaded and decrypted as whole blobs.
