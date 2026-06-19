# Spec 02: Filesystem Snapshot Model

## Purpose

Define how OSIx mounts agent state, computes diffs, represents deletions, and restores branchable filesystems.

## Mount Model

An OSIx mount behaves like a branchable copy-on-write filesystem:

```text
lowerdir = base image rootfs + previous snapshot chain
upperdir = local writable changes
workdir  = local overlay workdir
target   = user-visible mount path
```

The initial Linux implementation SHOULD use:

- overlayfs for privileged local mounts
- fuse-overlayfs for rootless mounts
- OCI tar layer extraction for lowerdirs
- zstd-compressed tar archives for snapshot layers

Future implementations MAY add eStargz, zstd:chunked, EROFS, or a custom encrypted chunk index for lazy reads.

## Snapshot Diff Format

Each snapshot diff layer MUST be an OCI-compatible tar layer. The layer MUST preserve:

- regular files
- directories
- symlinks
- hardlinks where supported
- uid, gid, and mode
- mtimes, normalized when reproducible mode is enabled
- extended attributes when supported
- deletions using OCI whiteout conventions

The v0 diff algorithm name is:

```text
overlayfs-whiteout-v1
```

## Snapshot Flow

The snapshotter MUST perform these steps:

1. Freeze, quiesce, or otherwise stabilize the mount.
2. Compute the diff from the writable upperdir.
3. Apply exclude and secret policies.
4. Emit an OCI tar layer with whiteouts.
5. Normalize metadata for reproducible hashing when requested.
6. Compress with zstd.
7. Encrypt the compressed layer if policy requires.
8. Upload the layer blob.
9. Upload the config blob.
10. Upload the snapshot manifest.
11. Attach signature and provenance referrers when configured.
12. Move branch tags after the manifest exists.

## Restore Flow

The restore path MUST perform these steps:

1. Resolve the user reference to a manifest digest.
2. Fetch and validate the config blob.
3. Walk the parent chain or checkpoint index.
4. Fetch required layers.
5. Decrypt layers.
6. Apply layers in order.
7. Verify integrity metadata when present.
8. Create a writable upperdir for new changes.
9. Expose the target mount.

## Snapshot Types

OSIx defines three snapshot types:

- `delta`: changes from a parent snapshot.
- `checkpoint`: squashed state from a base image to a selected snapshot.
- `anchor`: full encrypted state at a retention policy boundary.

v0 MUST support `delta`. v0 SHOULD support `checkpoint` for compaction.

## Compaction

Compaction replaces a long delta chain with a smaller checkpoint chain:

```text
base + delta1 + delta2 + ... + delta50
```

becomes:

```text
base + checkpoint50
```

Compaction MUST preserve manifests required by configured audit, signature, retention, or legal hold policy.

Example policy:

```text
osix compact ghcr.io/acme/agent-state/research-agent-a:main \
  --keep-hourly 24h \
  --keep-daily 30d \
  --keep-weekly 12w \
  --squash-every 50 \
  --preserve-signed
```

## Lazy Remote Access

v0 MAY require full layer download before mount.

Future lazy encrypted reads SHOULD use:

```text
chunk -> compress -> encrypt -> merkle-index
```

The index MUST support integrity verification per chunk and SHOULD avoid leaking path lists in public metadata.

