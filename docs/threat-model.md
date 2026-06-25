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
| Side-effect replay | Side-effect ledger validation, restore-time replay marker requiring approval, and write-capable provider adapters that block unsafe restored writes. |
| Broken parent chains | Chain validation checks base consistency, parent digests, and increasing sequence numbers. |
| Long delta-chain recovery cost | `osix compact` creates checkpoint snapshots and dry-run retention plans. |

## Residual Risk

- macOS overlay-style mounts require a signed and enabled FSKit app extension.
- AWS KMS/GPG/endpoint recipient support is provider-backed only when the relevant external provider mode is configured; offline local shims remain available for development.
- OSIx emits cosign simple-signing and Sigstore bundle registry artifacts. `osix verify --trusted-key` validates OSIx ed25519 or cosign ECDSA P-256 signatures, and explicit Sigstore `--certificate-*` policy validates public Fulcio/Rekor-backed bundles with identity, issuer, tlog, timestamp, and SCT checks.
- Public Sigstore keyless signing depends on a caller-supplied OIDC identity token. The CLI does not perform an interactive browser OIDC flow, so CI or another identity provider must supply the token.
- Encrypted lazy byte-range reads are implemented for `osix read --offset N --length N --decrypt ...` using chunk descriptors and per-chunk integrity checks. Darwin FSKit and Linux native lazy FUSE lower reads pass mount decrypt material through this path. When encrypted lazy indexes are present, `restore --decrypt` can restore from encrypted lazy blobs without materializing whole encrypted layers; Linux kernel overlay lowerdirs still materialize whole layers.
