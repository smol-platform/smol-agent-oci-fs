# OSIx Prototype

OSIx is an OCI Agent State Image prototype. This repository currently contains a local CLI implementation that demonstrates the core state-image loop before remote registry and real overlay mount support are added.

The prototype stores snapshots in a local content-addressed `.osix` store:

```text
.osix/
  blobs/sha256/<digest>
  refs/<tag>
  config.toml
```

Snapshot manifests use the OSIx media types from the specs and point at zstd-compressed tar checkpoint layers.

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
```

Implemented commands:

- `osix init`
- `osix snapshot`
- `osix restore`
- `osix mount`, currently an alias for restoring a writable local copy
- `osix diff`
- `osix fork`
- `osix show`
- `osix refs`

## Current Limits

- Registry push/pull is not implemented yet.
- `mount` does not use overlayfs or fuse-overlayfs yet; it restores a local copy.
- Snapshots are checkpoint-style full filesystem snapshots, not compact delta chains.
- Encryption, signing, provenance, watch, and compaction are still future milestones.

