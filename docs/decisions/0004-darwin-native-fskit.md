# 0004: Darwin Native FSKit Runtime

## Status

Accepted.

## Context

ADR 0003 selected a macFUSE-compatible union adapter for macOS. That path
requires a third-party filesystem runtime, local approval of macFUSE, and
backend-specific behavior from `fuse-overlayfs`, `unionfs`, or `unionfs-fuse`.

Apple now exposes FSKit as the native user-space filesystem extension model for
macOS. FSKit modules are delivered as File System app extensions inside a host
app, discovered through `FSClient`, and mounted by Darwin `mount -F`.

The OSIx runtime still needs the same behavior:

- writable merged view over restored lower state;
- copy-on-write changes in OSIx upper state;
- whiteout-compatible deletes;
- snapshot, diff, watch, recover, status, and unmount through `MountRuntime`.

## Decision

Use a Darwin-native FSKit module as the macOS overlay-style filesystem runtime.
The Go library invokes a control helper, `osix-fskitctl`, which verifies that
FSKit is available, discovers an enabled OSIx FSKit app extension, and calls:

```text
mount -F -t OSIxFS -o osix.*=... osixfs TARGET
```

Both `--mode overlay` and `--mode fuse` may select the same FSKit-backed native
runtime on Darwin:

- `--mode overlay` means "overlay-style writable mount" on Darwin, not Linux
  kernel overlayfs.
- `--mode fuse` remains an API-compatible request for a rootless userspace
  filesystem but is implemented by FSKit on Darwin.
- `--mode auto` selects FSKit only when the helper and enabled extension are
  available; otherwise it falls back to materialized mode.

## Prerequisites

- macOS 15.4 or newer with `/System/Library/Frameworks/FSKit.framework`.
- Xcode macOS SDK to build `osix-fskitctl` and the host app/extension.
- A signed host app containing an OSIx File System app extension.
- The `com.apple.developer.fskit.fsmodule` entitlement on the extension.
- The extension enabled in System Settings for the current user.

## Runtime Contract

`osix-fskitctl mount` passes these base64url-encoded mount options to the FSKit
module:

- `osix.bundle`
- `osix.workspace`
- `osix.source_ref`
- `osix.source_digest`
- `osix.lower`
- `osix.upper`
- `osix.work`
- `osix.mode`

The FSKit module owns merged-tree operations, copy-on-write, advisory dirty
tracking, and flushing before unmount or snapshot. OSIx owns snapshot/restore,
OCI whiteout conversion, mount metadata, policy checks, and materialized
fallback.

## Tradeoffs

- This avoids macFUSE and kernel-extension approval.
- FSKit requires Apple signing, entitlement, host-app packaging, and per-user
  extension enablement.
- The filesystem implementation must live behind Swift/Objective-C FSKit
  protocols or bridge from the extension to a non-Swift engine.
- Integration tests can build the helper everywhere FSKit is available, but real
  mount tests require the signed/enabled app extension.

## Deferred

- Bridged Go engine behind the FSKit extension.
- Complete merged-tree `FSVolume` operation implementation.
- Lazy remote lowerdir reads.
- Strong cross-platform xattr normalization.
