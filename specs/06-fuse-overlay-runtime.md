# Spec 06: FUSE And Overlay Filesystem Runtime

## Purpose

Define how the OSIx library becomes a real mounted filesystem runtime instead of only materializing restored directories. The runtime MUST support a privileged Linux overlayfs path and a rootless FUSE path behind the same library and CLI contract.

## Goals

- Expose `osix mount REF TARGET` as a writable branchable filesystem.
- Use kernel overlayfs when privileges and platform support are available.
- Use FUSE or FSKit when rootless operation, macOS development, or custom lazy read behavior is needed.
- Preserve OSIx snapshot semantics: parent digest, branch metadata, whiteouts, excludes, secret policy, encryption, signing, and side-effect safety.
- Allow snapshotting from the mount without scanning unrelated runtime/cache internals.
- Provide a library API that can be embedded by agents, CLIs, and future daemon processes.

## Non-Goals

- Live process checkpointing.
- Distributed merge/conflict resolution.
- Kernel snapshotter plugins for containerd.
- Fully lazy encrypted chunk reads in the first implementation.
- Supporting arbitrary host filesystem semantics beyond what OSIx can encode into OCI tar diff layers.

## Runtime Modes

### Materialized Mode

Materialized mode is the portability fallback. It restores the snapshot chain into a normal directory and records mount metadata.

Use when:

- FUSE is unavailable.
- overlayfs is unavailable.
- tests need deterministic behavior without elevated privileges.
- the user requests `--mode materialized`.

Limitations:

- Not a real mount.
- Host tools can mutate files without filesystem-level interception.
- Dirty tracking requires tree scans.

### Overlayfs Mode

Overlayfs mode uses Linux kernel overlayfs:

```text
lowerdir = extracted read-only parent chain
upperdir = .osix/mounts/<id>/upper
workdir  = .osix/mounts/<id>/work
target   = user mount target
```

Use when:

- OS is Linux.
- The process has mount privileges.
- The lowerdir chain is local and decrypted.
- The user requests `--mode overlay` or `--mode auto`.

Required behavior:

- Mount lower layers read-only.
- Write all mutations into upperdir.
- Snapshot only upperdir changes.
- Convert overlayfs whiteouts into OCI whiteouts.
- Unmount cleanly on `osix unmount TARGET`.

On macOS, `--mode overlay` means an overlay-style writable mount implemented through the native FSKit runtime. It MUST NOT claim Linux kernel overlayfs semantics. The selected macOS backend MUST still write mutations to OSIx upper state and expose snapshot-compatible whiteouts.

### FUSE Mode

FUSE mode implements a userspace filesystem with OSIx-aware copy-on-write behavior.

Use when:

- Running rootless.
- Running on macOS with FSKit or Linux with `/dev/fuse`.
- The runtime needs custom read-through behavior.
- The user requests `--mode fuse` or `--mode auto`.

Required behavior:

- Present a merged tree built from parent snapshot layers plus writable upper state.
- On first write to a lower file, copy it into upper state.
- Track creates, modifies, deletes, renames, chmod, chown where supported, symlinks, and xattrs where supported.
- Represent deletes internally as whiteout records.
- Flush enough state for snapshot consistency.
- Return clear errors for unsupported filesystem operations.

## Selection Policy

`osix mount` SHOULD support:

```text
--mode auto|overlay|fuse|materialized
--rw
--branch BRANCH
--cache DIR
--lazy
--decrypt IDENTITY
```

Selection rules for `--mode auto`:

1. Use overlayfs on Linux when mount privilege and kernel support are present.
2. Use FUSE when a FUSE device/runtime is available.
3. Fall back to materialized mode.
4. Print the selected mode unless `--quiet` is set.

An explicit `--mode overlay` or `--mode fuse` MUST fail if unavailable. It MUST NOT silently fall back.

## Mount Layout

Each mount gets an id derived from the absolute target path:

```text
.osix/mounts/<mount-id>/
  mount.json
  lower/
    000000/
    000001/
  upper/
  work/
  whiteouts.json
  dirty.json
  replay-policy.json
```

`mount.json`:

```json
{
  "target": "/abs/path/agentfs",
  "sourceRef": "main",
  "sourceDigest": "sha256:...",
  "mode": "fuse",
  "branch": "main",
  "rw": true,
  "createdAt": "2026-06-19T19:00:00Z",
  "pid": 12345
}
```

## Library API

The public library SHOULD expose a runtime boundary:

```go
type MountMode string

const (
    MountAuto         MountMode = "auto"
    MountOverlay      MountMode = "overlay"
    MountFUSE         MountMode = "fuse"
    MountMaterialized MountMode = "materialized"
)

type MountRuntime interface {
    Mount(ctx context.Context, sourceRef string, target string, opts MountOptions) (MountInfo, error)
    Unmount(ctx context.Context, target string, opts UnmountOptions) error
    Status(ctx context.Context, target string) (MountInfo, error)
    Diff(ctx context.Context, target string) ([]Change, error)
    Snapshot(ctx context.Context, target string, opts SnapshotOptions) (SnapshotResult, error)
}
```

The existing materialized implementation SHOULD become one implementation of this interface.

## Snapshot Semantics

For every runtime mode:

- The snapshot parent MUST be the mounted source digest unless explicitly overridden.
- Snapshotting MUST apply OSIx excludes and secret policy before layer creation.
- Snapshot layers MUST use OCI whiteout semantics.
- Snapshotting MUST ignore runtime internals such as mount metadata, dirty indexes, replay marker files, and FUSE bookkeeping.
- Snapshotting SHOULD quiesce or flush the mount before diff generation.

Overlayfs snapshot:

- Diff from `upperdir`.
- Translate overlayfs character-device whiteouts and `.wh.*` files to OCI tar whiteouts.
- Include copied-up metadata.

FUSE snapshot:

- Diff from the runtime dirty index plus upper files.
- If the dirty index is unavailable or corrupt, fall back to tree comparison against parent.
- Preserve deterministic tar ordering.

Materialized snapshot:

- Diff current tree against parent tree.
- Treat this as the slow fallback.

## Dirty Tracking

The runtime SHOULD maintain `.osix/mounts/<id>/dirty.json`:

```json
{
  "dirtyBytes": 1048576,
  "paths": {
    "agent/workspace/file.txt": "modified",
    "agent/memory/old.jsonl": "deleted"
  },
  "updatedAt": "2026-06-19T19:00:00Z"
}
```

Dirty tracking MUST be advisory. Correctness MUST NOT depend solely on it; snapshot creation may rescan to verify.

## Unmount Semantics

`osix unmount TARGET` MUST:

1. Flush pending writes.
2. Refuse unmount when active writers exist unless `--force` is set.
3. Unmount FUSE or overlayfs mount.
4. Leave `upper/` and metadata intact for later snapshot or recovery.
5. Mark mount state as unmounted.

`osix cleanup TARGET` MAY remove mount internals after the user confirms or after all changes are snapshotted.

## Failure Recovery

The runtime MUST recover from:

- CLI crash while mount remains active.
- stale mount metadata after reboot.
- interrupted snapshot.
- interrupted unmount.
- corrupted dirty index.

Recovery command:

```text
osix mount status TARGET
osix mount recover TARGET
```

Recovery SHOULD prefer preserving user data in `upper/` over aggressive cleanup.

## Platform Requirements

Linux overlayfs:

- Requires Linux kernel overlayfs support.
- Requires mount privileges or appropriate namespace/capability setup.
- Requires local lowerdirs.

Linux FUSE:

- Requires `/dev/fuse`.
- Requires FUSE library/runtime.

macOS FSKit:

- Requires macOS 15.4 or newer with FSKit available.
- Requires `osix-fskitctl` on `PATH` or `OSIX_FSKIT_HELPER`.
- Requires an installed and enabled OSIx File System app extension discoverable through `FSClient`.
- Requires the extension entitlement `com.apple.developer.fskit.fsmodule`.
- Must document host-app packaging, signing, and per-user extension enablement requirements.
- `--mode auto` MAY select the macOS FSKit backend when prerequisites exist.
- Explicit `--mode overlay` and `--mode fuse` on macOS MUST fail before partial setup when the helper or enabled extension is unavailable.
- Case sensitivity, ownership, chmod/chown, and xattrs MAY differ from Linux overlayfs; unsupported operations MUST return clear errors.

Tests MUST include materialized mode everywhere. FUSE and overlayfs tests SHOULD be skipped with clear messages when platform prerequisites are missing.

## Security Requirements

- Never mount `/agent/secrets` from a snapshot unless explicitly enabled by policy.
- Do not expose decrypted layer cache to other users; use `0700` cache directories.
- Avoid leaking encrypted snapshot path indexes in public registry metadata.
- Refuse world-writable cache or upper directories.
- After restore/fork/mount, write replay safety metadata so external tools default to mock, read-only, or approval-required behavior.

## CLI Changes

Required commands:

```text
osix mount REF TARGET --mode auto|overlay|fuse|materialized --rw
osix unmount TARGET
osix mount status TARGET
osix mount recover TARGET
osix diff TARGET
osix snapshot TARGET --tag TAG
```

`osix mount` MUST print:

```text
mounted sha256:... at ./agentfs
mode fuse
upper .osix/mounts/<id>/upper
```

## Implementation Milestones

### FS-M0: Runtime Interface

Deliverables:

- `MountRuntime` interface.
- Materialized runtime moved behind the interface.
- Mount metadata includes selected mode.

Acceptance criteria:

- Existing mount, diff, snapshot, and restore tests pass through the runtime interface.

### FS-M1: Overlayfs Runtime

Deliverables:

- Linux overlayfs mount implementation.
- Unmount/status/recover commands.
- Upperdir snapshot diff.

Acceptance criteria:

- On Linux with privileges, `osix mount --mode overlay REF TARGET` creates a real overlay mount.
- Mutations land in upperdir.
- Snapshot emits only upperdir changes and whiteouts.

### FS-M2: FUSE Runtime

Deliverables:

- Rootless FUSE implementation.
- Copy-on-write reads/writes.
- Dirty path tracking.
- Delete/rename/chmod handling.

Acceptance criteria:

- `osix mount --mode fuse REF TARGET` exposes a merged writable filesystem.
- Snapshot after FUSE writes restores correctly.
- Dirty tracking matches a verification tree diff.

### FS-M3: Watch Integration

Deliverables:

- Watch consumes runtime dirty bytes when available.
- Turn-boundary snapshots flush the runtime first.

Acceptance criteria:

- `osix watch TARGET --max-dirty 512MiB` does not require full tree scans when runtime dirty tracking is healthy.

### FS-M4: Recovery And Hardening

Deliverables:

- stale mount detection.
- recovery command.
- cache permission checks.
- platform prerequisite checks.

Acceptance criteria:

- Simulated crash leaves upperdir recoverable.
- `osix mount recover TARGET` preserves dirty files.

## Open Questions

- First Linux implementation decision: use a minimal `fuse-overlayfs` platform adapter behind `MountRuntime`; keep the interface capable of hosting an in-process Go FUSE backend later.
- macOS implementation decision: use a native FSKit app extension controlled by `osix-fskitctl`; macFUSE is superseded.
- Full FSKit host app and File System app extension implementation remains open.
- Lazy remote reads are deferred to a future chunked lower-store abstraction.
- How strongly to normalize xattrs and platform-specific metadata across Linux and macOS.
