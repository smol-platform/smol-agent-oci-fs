# Decision 0001: Build Local Prototype Before Registry Integration

## Status

Accepted.

## Context

The OSIx design depends on several separable concerns: manifest/config shape, content-addressed blobs, filesystem layers, tags, registry push/pull, encryption, signing, and mount runtime behavior.

Building registry and overlay support first would make the earliest demo depend on infrastructure and platform-specific behavior before the core state-image model is proven.

## Decision

The first working CLI uses a local OCI-like content store under `.osix`:

- blobs are addressed by `sha256:` digest
- refs are mutable local tag files
- manifests use OCI image manifest shape
- layers are zstd-compressed tar checkpoint layers
- `mount` is a local restore-copy alias until overlayfs/fuse-overlayfs support exists

This completes the local M0/M1 loop and provides a concrete base for M2 registry push/pull and M3 real mount work.

## Consequences

The prototype can demonstrate `init`, `snapshot`, `restore`, `diff`, `fork`, `show`, and `refs` without a registry.

The prototype intentionally does not claim completion for:

- remote registry push/pull
- OCI referrers
- overlayfs/fuse-overlayfs mounts
- encrypted layers
- signatures and attestations
- compaction

