# OSIx Filesystem Runtimes

OSIx supports one runtime interface with three mount modes:

| Mode | Platform | Behavior |
| --- | --- | --- |
| `auto` | all | Selects `overlay`, then `fuse`, then `materialized` based on prerequisites. |
| `overlay` | Linux, macOS | Linux uses kernel overlayfs. macOS uses a native FSKit module for overlay-style semantics. |
| `fuse` | Linux with `/dev/fuse`, macOS with FSKit | Linux uses `fuse-overlayfs`; macOS uses the same native FSKit module as the Darwin userspace filesystem path. |
| `materialized` | all | Restores a writable copy into the target and records mount metadata. |

## Commands

```sh
osix mount REF TARGET --mode auto --force
osix mount REF TARGET --mode overlay --force
osix mount REF TARGET --mode fuse --force
osix mount status TARGET
osix mount recover TARGET
osix unmount TARGET --force
```

`mount status` reports the selected mode, target, source digest, state, upperdir, lowerdir, workdir, and runtime PID when available. For overlay/FUSE runtimes it also persists detected lifecycle state changes, such as stale mounts after a process exit, while refreshing `dirty.json`.

## Docker Integration Test

Run the FUSE integration test in a privileged Linux container with:

```sh
./scripts/test-fuse-docker.sh
```

The script uses `golang:1.24-bookworm`, installs `fuse-overlayfs`, exposes `/dev/fuse`, and runs `TestFUSERuntimeIntegrationLinux`. Override the image with `OSIX_FUSE_DOCKER_IMAGE` or the test selector with `OSIX_FUSE_TEST_PATTERN`.

## Mode Selection

`--mode auto` chooses the first available runtime:

1. Linux kernel overlayfs when the process has mount privileges and `/proc/filesystems` lists `overlay`.
2. macOS FSKit backend when `osix-fskitctl` is available and the OSIx File System app extension is installed and enabled.
3. Linux `fuse-overlayfs` when the binary is on `PATH` and `/dev/fuse` exists.
4. Materialized restore.

Explicit `--mode overlay` or `--mode fuse` fails with a prerequisite error instead of falling back.

## Runtime State

Each mount has a stable ID derived from its absolute target path:

```text
.osix/mounts/<id>/
  mount.json
  lower/000000/
  upper/
  work/
  dirty.json
```

`mount.json` records the source digest, selected mode, branch, state, PID, and runtime paths. The legacy `.osix/mounts/<id>.json` metadata path is still written for compatibility.

Overlay/FUSE snapshots package the upperdir and OCI whiteouts, not the runtime directory itself. This keeps snapshot layers focused on user-visible changes.

## Dirty Tracking

The runtime rebuilds `dirty.json` from the upperdir when status, recovery, or watch needs it. The dirty index includes:

- `dirtyBytes`: bytes in upperdir regular files.
- `paths`: modified paths from upper entries and deleted paths from overlay whiteouts.
- `updatedAt`: rebuild time.

`osix watch TARGET --max-dirty SIZE` uses upperdir dirty bytes for overlay/FUSE mounts and falls back to a target tree scan for materialized mounts.

## Recovery And Permissions

`osix mount recover TARGET` reloads mount metadata, rejects world-writable runtime directories, rebuilds `dirty.json`, and marks the mount recovered. It does not discard upperdir contents.

Runtime lower, upper, and work directories are created with `0700`. Mount metadata and dirty indexes are written with `0600`. User-provided runtime cache directories must already be private or creatable as private; OSIx rejects caches with group or other permissions.

## FUSE Adapter Decision

The first Linux implementation uses `fuse-overlayfs` as the FUSE runtime adapter instead of embedding a Go FUSE server. The macOS path is native FSKit, not macFUSE.

## macOS Setup

macOS overlay-style mounts require:

- macOS 15.4 or newer with FSKit.
- Xcode with the macOS SDK.
- `osix-fskitctl` available on `PATH` or `OSIX_FSKIT_HELPER` set.
- A signed host app containing the OSIx File System app extension.
- The FSKit extension entitlement `com.apple.developer.fskit.fsmodule`.
- The extension enabled in System Settings for the current user.

Build the FSKit control helper when you only need the CLI-side prerequisite
check:

```sh
./scripts/build-macos-fskit.sh
```

Install the local host app plus embedded File System extension:

```sh
./scripts/install-macos-fskit-app.sh
```

The installer builds the helper and app, copies the app into `~/Applications`,
registers the embedded extension with PlugInKit, elects it for the current user,
and runs `osix-fskitctl doctor`. If doctor says FSClient does not report the
module enabled, finish enablement in System Settings > General > Login Items &
Extensions > File System Extensions.

The integration harness uses `--no-open --background-register` to launch the
host app hidden long enough for ExtensionKit/PlugInKit discovery without
foregrounding the app. It also passes `--wait-ready`, controlled by
`OSIX_FSKIT_READY_TIMEOUT`, so an enabled extension has a short grace period to
appear in `FSClient` before the harness reports the System Settings prerequisite.

By default OSIx looks for extension bundle id `io.github.smol-platform.smol-agent-oci-fs.fskit.extension` and filesystem type `OSIxFS`. Override local development values with:

```sh
export OSIX_FSKIT_BUNDLE_ID=com.example.OSIxFSKit.Extension
export OSIX_FSKIT_TYPE=OSIxFS
```

Run the local prerequisite and integration smoke harness with:

```sh
./scripts/test-macos-fskit.sh
```

The helper checks installed FSKit modules through `FSClient` and invokes Darwin `mount -F`. When the extension is unavailable or not enabled, explicit `--mode overlay` and `--mode fuse` return a prerequisite error. `--mode auto` falls back to the materialized runtime.

macOS behavior can differ from Linux overlayfs for case sensitivity, ownership, chmod/chown, and xattrs. Snapshot compatibility is preserved through the OSIx upperdir and whiteout path.

## Known Limits

- Overlay mode requires Linux mount privileges.
- macOS overlay mode requires a signed and enabled FSKit app extension; it is not Linux kernel overlayfs.
- Lazy remote reads are not implemented. Lowerdirs are restored locally before overlay/FUSE mount.
- Dirty tracking is rebuilt from upperdir state rather than maintained by an always-on daemon.
