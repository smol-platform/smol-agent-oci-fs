# 0003: macOS Overlay Backend

## Status

Superseded by [0004: Darwin Native FSKit Runtime](0004-darwin-native-fskit.md).

## Context

OSIx already supports Linux kernel overlayfs and Linux `fuse-overlayfs` behind `MountRuntime`. macOS does not provide Linux overlayfs, and macFUSE exposes a FUSE API rather than a native overlay filesystem.

The macOS runtime still needs the same OSIx behavior:

- writable merged view over restored lower state;
- copy-on-write changes in OSIx upper state;
- whiteout-compatible deletes;
- snapshot, diff, watch, recover, status, and unmount through the same runtime API.

## Decision

Use a macFUSE-compatible overlay adapter as the first macOS overlay backend. OSIx will prefer `fuse-overlayfs` when it is available, then fall back to `unionfs` or `unionfs-fuse`.

For `fuse-overlayfs`, OSIx mounts:

```text
lowerdir=<lower>,upperdir=<upper>,workdir=<work>
```

For unionfs backends, OSIx mounts:

```text
upper=RW:lower=RO
```

with copy-on-write enabled. Both `--mode overlay` and `--mode fuse` may use the selected backend on macOS:

- `--mode overlay` means "overlay-style mount" on Darwin, not kernel overlayfs.
- `--mode fuse` explicitly selects the same macFUSE union adapter.
- `--mode auto` selects the macOS adapter when prerequisites are present, otherwise falls back to materialized mode.

## Prerequisites

- macFUSE installed and approved in macOS System Settings.
- A backend binary available on `PATH`: `fuse-overlayfs`, `unionfs`, or `unionfs-fuse`.
- Local lowerdir, upperdir, and workdir created by OSIx.

## Tradeoffs

- This avoids embedding a Go FUSE server before the OSIx snapshot semantics settle.
- The backend is operationally familiar to macOS users who already use macFUSE.
- Metadata fidelity may differ from Linux overlayfs for ownership, xattrs, case sensitivity, and unsupported operations.
- A future in-process Go FUSE backend can replace this adapter without changing `MountRuntime`.

## Deferred

- First-class macOS package management.
- A pure Go FUSE implementation.
- Lazy remote lowerdir reads.
- Strong cross-platform xattr normalization.
