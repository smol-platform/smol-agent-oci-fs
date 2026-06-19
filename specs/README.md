# OCI Agent State Image Specs

Working name: OSIx, the OCI Agent State Image extension.

OSIx defines a disciplined convention for storing branchable, encrypted agent state in standard OCI registries. It does not require a new registry protocol. It uses OCI manifests, descriptors, blobs, tags, annotations, and referrers where available.

## Goals

- Store agent filesystem state as OCI-compatible snapshot layers.
- Mount, snapshot, restore, fork, diff, and compact agent state.
- Encrypt mutable state before upload.
- Sign and attest snapshot manifests.
- Preserve agent-specific state boundaries, including memory, skills, runtime locks, and side-effect ledgers.
- Remain portable across existing OCI registries.

## Non-Goals For v0

- Live process checkpointing.
- Distributed merge semantics.
- Kernel-native snapshotter plugins.
- Fully lazy random access into encrypted remote layers.
- Strong registry-independent compare-and-swap tag updates.

## Spec Set

- [OCI Artifact Model](./01-oci-artifact-model.md)
- [Filesystem Snapshot Model](./02-filesystem-snapshot-model.md)
- [CLI And Runtime](./03-cli-and-runtime.md)
- [Security And Provenance](./04-security-and-provenance.md)
- [Agent State Semantics](./05-agent-state-semantics.md)
- [Milestones](./milestones.md)

## Compatibility Modes

OSIx implementations SHOULD support these publication modes:

- `image`: each snapshot is an OCI image manifest with custom OSIx config and diff layers.
- `artifact`: each snapshot is an OCI artifact linked through `subject` and discoverable through referrers.
- `hybrid`: each snapshot is published as an image manifest for broad compatibility, with referrer artifacts for signatures, provenance, indexes, and audit metadata.

The v0 default is `hybrid`, with `image` as the fallback for registries that do not support OCI 1.1 referrers.

