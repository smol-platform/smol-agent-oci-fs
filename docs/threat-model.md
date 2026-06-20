# OSIx Threat Model

## Assets

- Agent workspace, memory, skill, runtime, and state files.
- Snapshot manifests, configs, and layers.
- Signing keys and verification policies.
- Side-effect ledger entries.
- Registry credentials and snapshot tags.

## Trust Boundaries

- Local workspace boundary: `.osix` metadata and mounted/materialized state.
- Registry boundary: pushed blobs, manifests, and tags.
- Tool boundary: side effects performed by external tool integrations.
- Crypto boundary: encryption identities, signing keys, and KMS-style recipients.

## Threats And Mitigations

| Threat | Mitigation |
| --- | --- |
| Registry blob disclosure | Encrypted layer media type with age and KMS-style envelope support. |
| Manifest tampering | Manifest-digest signatures and `osix verify`. |
| Tag rollback or race | Immutable digest restore, `osix validate`, and expected-parent conflict checks. |
| Secret inclusion | Default path exclusions, `--secret-scan block`, and log redaction hooks. |
| Side-effect replay | Side-effect ledger validation and restore-time replay marker requiring approval. |
| Broken parent chains | Chain validation checks base consistency, parent digests, and increasing sequence numbers. |
| Long delta-chain recovery cost | `osix compact` creates checkpoint snapshots and dry-run retention plans. |

## Residual Risk

- `mount` is a materialized writable copy, not kernel overlayfs/fuse-overlayfs.
- AWS KMS integration is local envelope behavior in v0, not a live AWS API call.
- Full Sigstore transparency log and keyless identity verification are not implemented.
- OCI Referrers API publication for signatures/provenance is not implemented.
- Lazy encrypted chunk reads are not implemented.

