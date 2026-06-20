# smol-agent-oci-fs

OSIx is an OCI Agent State Image prototype. This repository contains a Go CLI that demonstrates the core state-image loop with local content-addressed storage, OCI Distribution push/pull, delta layers, encryption, signing, provenance, branch refs, chain validation, and writable filesystem runtimes.

The prototype stores snapshots in a local content-addressed `.osix` store:

```text
.osix/
  blobs/sha256/<digest>
  refs/<tag>
  config.toml
```

Snapshot manifests use the OSIx media types from the specs and point at zstd-compressed tar checkpoint layers.
Snapshot layers are zstd-compressed tar diffs with OCI-style whiteouts. Encrypted snapshots use `application/vnd.osix.agent.layer.diff.v1.tar+zstd+encrypted`.

## Build

```sh
go build -o ./osix ./cmd/osix
```

## Demo

```sh
./osix init ghcr.io/acme/agent-base:2026-06-19 \
  --name research-agent-a \
  --state local/research-agent-a \
  --mount ./agentfs

mkdir -p agentfs/agent/workspace agentfs/agent/memory agentfs/agent/skills
printf 'initial notes\n' > agentfs/agent/workspace/notes.md
printf '{"fact":"remembered"}\n' > agentfs/agent/memory/memory.jsonl
printf '# skill\n' > agentfs/agent/skills/README.md

./osix snapshot agentfs --message 'initial state' --tag snap-000001 --also-tag main

printf 'updated notes\n' > agentfs/agent/workspace/notes.md
printf 'new skill\n' > agentfs/agent/skills/new-skill.md
rm agentfs/agent/memory/memory.jsonl

./osix snapshot agentfs --message 'updated state' --tag snap-000002 --also-tag main
./osix diff snap-000001 snap-000002
./osix restore snap-000001 restore-snap1
./osix fork snap-000001 experiment-a
./osix validate main
./osix snapshot agentfs --tag signed --sign keyless --attest slsa
./osix verify signed
```

For registry-backed use, initialize with a registry repository:

```sh
./osix init ghcr.io/acme/agent-base:2026-06-19 \
  --name research-agent-a \
  --state localhost:5000/acme/research-agent-a \
  --mount ./agentfs

./osix snapshot agentfs --tag snap-000001 --also-tag main --push
./osix restore localhost:5000/acme/research-agent-a:snap-000001 restored
```

Encrypted snapshots:

```sh
./osix init example/base:latest \
  --name secure-agent \
  --state localhost:5000/acme/secure-agent \
  --mount ./agentfs \
  --encrypt age:age1...

./osix snapshot agentfs --tag encrypted
./osix restore encrypted restored --decrypt ./age-identity.txt
```

Filesystem runtime modes:

```sh
./osix mount snap-000001 ./mounted --mode auto --force
./osix mount snap-000001 ./mounted-overlay --mode overlay --force
./osix mount snap-000001 ./mounted-fuse --mode fuse --force
./osix mount status ./mounted
./osix mount recover ./mounted
./osix unmount ./mounted
```

`auto` selects Linux kernel overlayfs when prerequisites are available, then the platform userspace runtime, then the portable materialized runtime. On Linux the userspace runtime is `fuse-overlayfs`; on macOS it is a native FSKit app extension controlled by `osix-fskitctl`. Overlay/FUSE mode stores lower, upper, work, mount metadata, and dirty indexes under `.osix/mounts/<mount-id>/`; snapshotting an overlay/FUSE target uses the upperdir and whiteouts rather than packaging runtime internals.

Implemented commands:

- `osix init`
- `osix snapshot`
- `osix restore`
- `osix mount`
- `osix mount status`
- `osix mount recover`
- `osix unmount`
- `osix diff`
- `osix fork`
- `osix validate`
- `osix verify`
- `osix watch`
- `osix compact`
- `osix run`
- `osix show`
- `osix refs`

## Current Limits

- `mount --mode overlay` requires Linux with overlayfs mount privileges, or macOS with the OSIx FSKit extension installed and enabled.
- `mount --mode fuse` uses `fuse-overlayfs` on Linux and native FSKit on macOS.
- Other non-Linux builds fall back to the materialized runtime unless a nonportable mode is requested explicitly.
- AWS KMS support is currently a local KMS-style envelope path keyed by `kms:aws:kms:...` recipient strings, not a live AWS KMS API call.
- Signing is OSIx-native manifest-digest signing with ed25519 keys. It is cosign-style, but not yet emitted as a full Sigstore/cosign artifact.
- `watch` is a bounded CLI scheduler with lifecycle state files, not a persistent daemon process.
- Side-effect ledger validation and replay markers are implemented; external tool adapters are not part of this repo.

See [docs/runtime-filesystems.md](docs/runtime-filesystems.md) for runtime prerequisites and operational behavior.
