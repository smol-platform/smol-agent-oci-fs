# OSIx Filesystem Runtimes

OSIx supports one runtime interface with three mount modes:

| Mode | Platform | Behavior |
| --- | --- | --- |
| `auto` | all | Selects `overlay`, then `fuse`, then `materialized` based on prerequisites. |
| `overlay` | Linux, macOS | Linux uses kernel overlayfs. macOS uses a native FSKit module for overlay-style semantics. |
| `fuse` | Linux with `/dev/fuse`, macOS with FSKit | Linux uses `fuse-overlayfs`, except `--lazy` mounts use OSIx's native Go FUSE lower-store backend with writable copy-up; macOS uses the same native FSKit module as the Darwin userspace filesystem path. |
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

The script uses `golang:1.24-bookworm`, installs `fuse-overlayfs`, exposes `/dev/fuse`, and runs the Linux FUSE integration tests. Coverage includes the writable `fuse-overlayfs` adapter, in-process native lazy read-only and writable backends, and CLI-level lazy remote mounts that build a real `osix` binary, start the hidden helper process, verify its PID through mount metadata, read and write through FUSE, snapshot/restore writable changes, and unmount the helper cleanly. Override the image with `OSIX_FUSE_DOCKER_IMAGE` or the test selector with `OSIX_FUSE_TEST_PATTERN`.

## Mode Selection

`--mode auto` chooses the first available runtime:

1. Linux native lazy FUSE when `--lazy` is requested and `/dev/fuse` exists.
2. Linux kernel overlayfs when the process has mount privileges, `/proc/filesystems` lists `overlay`, and `--lazy` is not requested.
3. macOS FSKit backend when `osix-fskitctl` is available and the OSIx File System app extension is installed and enabled.
4. Linux `fuse-overlayfs` when the binary is on `PATH` and `/dev/fuse` exists.
5. Materialized restore.

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

The default non-lazy Linux FUSE implementation uses `fuse-overlayfs`. `--lazy` Linux FUSE mounts use an embedded Go FUSE server backed by snapshot lower-store metadata, ranged lazy file reads, writable upperdir copy-up, and overlay whiteouts, so the lowerdir is not restored before mount. The macOS path is native FSKit, not macFUSE.

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

Local builds are ad-hoc signed by default. To use a real Apple signing identity
for the helper, host app, and embedded extension, set `OSIX_FSKIT_CODESIGN_IDENTITY`
before running the build or install script:

```sh
OSIX_FSKIT_CODESIGN_IDENTITY="Apple Development: Example Developer (TEAMID)" \
  ./scripts/install-macos-fskit-app.sh
```

For capable-host validation, add `--require-team-signing` or set
`OSIX_FSKIT_REQUIRE_TEAM_SIGNING=1`. This refuses ad-hoc signing and verifies
that the helper, host app, and embedded extension carry an Apple
`TeamIdentifier` before registration.

The installer builds the helper and app, copies the app into `~/Applications`,
registers the embedded extension with PlugInKit, elects it for the current user,
and runs `osix-fskitctl doctor`. If doctor says FSClient does not report the
module enabled, finish enablement in System Settings > Login Items & Extensions
> OSIxFSKitHost Extensions > FSKit Modules. Interactive installs open System
Settings automatically on a failed doctor check; pass `--no-open-settings` to
keep the install fully noninteractive or `--open-settings` to force System
Settings after a failed readiness check.

If the switch is enabled but `doctor` still only sees Apple's built-in FSKit
modules, check signing before debugging mount operations. FSKit associates
third-party modules with the signing team; ad-hoc signatures report
`TeamIdentifier=not set`. Install a valid Apple code-signing identity approved
for `com.apple.developer.fskit.fsmodule`, then rerun the installer with
`OSIX_FSKIT_CODESIGN_IDENTITY`.

PlugInKit registration and election only make the embedded extension
discoverable to the system. They are not the same as FSKit runtime enablement.
The public FSKit `FSClient` API only exposes installed module identities and
their enabled state; it does not provide a public API for enabling a file system
extension. Enablement is handled by System Settings.

If the System Settings search field does not find "File System Extensions",
search for "Login Items", open Login Items & Extensions, then click Show Detail
next to OSIxFSKitHost and enable the FSKit Modules switch.

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

For machine-readable capable-host evidence, set:

```sh
OSIX_FSKIT_PREFLIGHT_REPORT=./fskit-preflight.json \
OSIX_FSKIT_EVIDENCE_DIR=./fskit-evidence \
  ./scripts/test-macos-fskit.sh
```

`OSIX_FSKIT_PREFLIGHT_REPORT` is written before live mounted tests run and
records FSKit doctor status, helper/app/extension signing summaries, bundle id,
filesystem type, and whether the host is blocked or ready. On a passing capable
host, `OSIX_FSKIT_EVIDENCE_DIR` receives a timestamped JSON file with
`result: passed`.

The helper checks installed FSKit modules through `FSClient`, verifies the
enabled extension declares the requested filesystem type, and invokes Darwin
`mount -F`. When the extension is unavailable or not enabled, explicit `--mode
overlay` and `--mode fuse` return a prerequisite error. `--mode auto` falls
back to the materialized runtime.

macOS behavior can differ from Linux overlayfs for case sensitivity, ownership, chmod/chown, and xattrs. Snapshot compatibility is preserved through the OSIx upperdir and whiteout path.

## Known Limits

- Overlay mode requires Linux mount privileges.
- macOS overlay mode requires a signed and enabled FSKit app extension; it is not Linux kernel overlayfs.
- Lazy single-file remote reads are available through `osix pull --lazy` and `osix read`. Age-only, legacy KMS, and OSIx-envelope encrypted snapshots can also satisfy `read --decrypt` from encrypted per-file lazy blobs, and `read --offset N --length N --decrypt ...` can fetch only the encrypted chunks needed for the requested range. When encrypted lazy indexes are present, `restore --decrypt` can materialize files from encrypted lazy blobs without fetching the whole encrypted layer. The library also exposes a snapshot lower-store API that can do lookup and directory enumeration from config metadata before fetching content. Darwin FSKit and Linux native lazy FUSE can use this lower-store path for `--lazy` lower reads without restoring the lowerdir first, including encrypted reads when decrypt material is supplied. Linux kernel overlay runtime preparation still fetches/materializes whole lower layers.
- Dirty tracking is rebuilt from upperdir state rather than maintained by an always-on daemon.
