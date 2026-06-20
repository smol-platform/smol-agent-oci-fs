# OSIx v0 Release Notes

## Highlights

- Local content-addressed OSIx store.
- OCI image manifest generation with custom OSIx config and layer media types.
- Zstd tar diff layers with OCI-style whiteouts.
- Registry push/pull through OCI Distribution APIs.
- Materialized writable mount flow with mount metadata.
- Snapshot, restore, diff, fork, validate, watch, verify, compact, and run commands.
- Age and KMS-style encrypted layers.
- Manifest-digest signing and provenance blobs.
- Side-effect ledger validation, replay safety markers, secret scan, and log redaction.
- Dry-run compaction planning and checkpoint snapshot creation.

## Verification

Use:

```sh
go test ./...
go build ./cmd/osix
```

## Known Limits

- Kernel overlayfs/fuse-overlayfs mounting is not implemented.
- Live AWS KMS API calls are not implemented.
- Full Sigstore/cosign registry artifact emission is not implemented.
- OCI Referrers API publication is not implemented.
- Watch runs as a bounded CLI scheduler in v0; no persistent daemon process is shipped yet.

