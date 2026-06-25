# OSIx FSKit Runtime

This package contains the Darwin-native FSKit control helper, host app, and
File System app extension used by the Go `MountRuntime`.

The runtime intentionally does not depend on macFUSE. The helper verifies that FSKit is
available, discovers an enabled OSIx FSKit app extension with `FSClient`, and
invokes Darwin `mount -F` so the filesystem type is resolved through FSKit.

The default extension bundle id is:

```text
io.github.smol-platform.smol-agent-oci-fs.fskit.extension
```

Override it for local development with:

```sh
export OSIX_FSKIT_BUNDLE_ID=com.example.OSIxFSKit.Extension
```

The filesystem type passed to `mount -F -t` defaults to `OSIxFS` and can be
overridden with `OSIX_FSKIT_TYPE`. `osix-fskitctl doctor` checks that the
enabled extension declares that filesystem type before mounted tests attempt
`mount -F`.

## Build And Install

Build the control helper:

```sh
./scripts/build-macos-fskit.sh
```

Build the host app with its embedded File System extension:

```sh
./scripts/build-macos-fskit-app.sh
```

The app bundle is written to `.osix-tools/dist/macos/OSIxFSKitHost.app` by
default. Override this with `OSIX_FSKIT_DIST_DIR` when packaging to another
location.

Local builds are ad-hoc signed by default. To build the host app and embedded
extension with an Apple signing identity, set `OSIX_FSKIT_CODESIGN_IDENTITY`:

```sh
OSIX_FSKIT_CODESIGN_IDENTITY="Apple Development: Example Developer (TEAMID)" \
  ./scripts/build-macos-fskit-app.sh
```

Set `OSIX_FSKIT_REQUIRE_TEAM_SIGNING=1` when preparing a capable-host build.
That mode refuses ad-hoc signing and verifies that the helper, host app, and
embedded extension report a non-empty Apple `TeamIdentifier` after signing:

```sh
OSIX_FSKIT_CODESIGN_IDENTITY="Apple Development: Example Developer (TEAMID)" \
OSIX_FSKIT_REQUIRE_TEAM_SIGNING=1 \
  ./scripts/build-macos-fskit.sh

OSIX_FSKIT_CODESIGN_IDENTITY="Apple Development: Example Developer (TEAMID)" \
OSIX_FSKIT_REQUIRE_TEAM_SIGNING=1 \
  ./scripts/build-macos-fskit-app.sh
```

Install the host app into `~/Applications` and launch it for local development:

```sh
./scripts/install-macos-fskit-app.sh
```

The installer builds the helper and app bundle, copies the app into
`~/Applications`, registers the embedded `.appex` with PlugInKit, elects it for
the current user, and runs `osix-fskitctl doctor`. If `FSClient` still reports
the module disabled during an interactive install, the installer opens the Login
Items & Extensions settings pane so the File System Extension can be enabled.

For noninteractive test setup, use:

```sh
./scripts/install-macos-fskit-app.sh --no-open --background-register --wait-ready=10
```

That launches the host app hidden briefly so ExtensionKit/PlugInKit discovers
the embedded File System extension without bringing the app to the foreground,
then waits up to the requested number of seconds for `FSClient` to report the
extension ready.

PlugInKit registration is not the same as FSKit runtime enablement. If doctor
reports that FSClient does not see an enabled module, enable the extension in
System Settings > Login Items & Extensions > OSIxFSKitHost Extensions > FSKit
Modules. Use `--open-settings` to force opening System Settings after a failed
doctor check, or `--no-open-settings` to suppress it in scripted setup.

The public FSKit `FSClient` API exposes installed module identities and their
enabled state, but it does not expose an API to enable a file system extension.
The System Settings File System Extensions pane owns that user approval step.
If System Settings search does not find "File System Extensions", search for
"Login Items", open Login Items & Extensions, click Show Detail next to
OSIxFSKitHost, and enable the FSKit Modules switch.

Distribution requires a Developer ID or App Store signing identity and an
approved FSKit entitlement profile. The same `OSIX_FSKIT_CODESIGN_IDENTITY`
setting is honored by `install-macos-fskit-app.sh` because it delegates to the
helper and app build scripts. If `doctor` still reports that FSClient cannot see
the extension after the System Settings switch is enabled, verify that
`security find-identity -v -p codesigning` lists a valid Apple signing identity
and rebuild with `OSIX_FSKIT_CODESIGN_IDENTITY`; ad-hoc signatures have no
TeamIdentifier for FSKit to associate with the enabled module.
Use `./scripts/install-macos-fskit-app.sh --require-team-signing` to enforce
that requirement before the app is installed or registered.

## Runtime Contract

`osix-fskitctl mount` passes OSIx mount state to the FSKit module through
base64url-encoded mount options:

- `osix.bundle`
- `osix.workspace`
- `osix.source_ref`
- `osix.source_digest`
- `osix.lower`
- `osix.upper`
- `osix.work`
- `osix.mode`

The app extension is responsible for presenting a writable merged filesystem
from `lower` and `upper`, using OSIx whiteout-compatible delete semantics, and
flushing mutations before unmount or snapshot. When `osix.workspace` and
`osix.source_digest` are available, the extension loads the parent snapshot tree
and omits copied-up upper entries that exactly match the parent from
`dirty.json`. Parent metadata is authoritative for mounted OSIx runtimes: if the
referenced parent manifest or config cannot be loaded, dirty-index rebuild fails
instead of silently producing a degraded dirty set.

Names beginning with `.wh.` are reserved by the local upperdir whiteout
encoding. The FSKit module rejects those names for lookup and mutation rather
than exposing them as user files; future OCI-compatible escaping can loosen this
without allowing user-visible files to collide with internal delete markers.

## Remaining Native Extension Work

The extension builds and carries the `com.apple.developer.fskit.fsmodule`
entitlement. It implements the merged-tree FSKit operation surface used by the
runtime: lookup, enumeration, reads, writes, create/remove/rename, symlinks,
copy-on-write, whiteouts, dirty tracking, chmod/chown/timestamps where the host
filesystem permits them, and xattrs. Hard links are intentionally unsupported
and return `ENOTSUP` without mutating upperdir state.

The remaining work is capable-host validation after the app extension is enabled
for FSKit. On an enabled macOS 15.4+ host, run:

```sh
./scripts/test-macos-fskit.sh
```

Set `OSIX_FSKIT_READY_TIMEOUT` to change how long the harness waits for
`FSClient` readiness after installing/registering the local app.
Set `OSIX_FSKIT_PREFLIGHT_REPORT=path` to write a JSON readiness report even
when FSClient blocks the run, and `OSIX_FSKIT_EVIDENCE_DIR=dir` to write a
timestamped `result: passed` evidence file after the live Darwin FSKit
integration test succeeds.

That test mounts both macOS `overlay` and `fuse` modes through FSKit, mutates the
mounted tree, verifies dirty tracking, snapshots, restores, and unmounts.
