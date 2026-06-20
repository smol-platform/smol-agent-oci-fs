# Spec 03: CLI And Runtime

## Purpose

Define the user-facing commands, local project layout, daemon responsibilities, and Go API shape.

## Local Layout

`osix init` creates:

```text
.osix/
  config.toml
  cache/
  upper/
  work/
  manifests/
  keys/
```

The `.osix/config.toml` file records the state repository, base image, mount path, encryption recipients, signing policy, and default branch.

## Commands

### Initialize

```text
osix init ghcr.io/acme/agent-base:2026-06-19 \
  --name research-agent-a \
  --state ghcr.io/acme/agent-state/research-agent-a \
  --encrypt age:age1xyz... \
  --mount ./agentfs
```

Creates local OSIx metadata, prepares the writable mount, and records the base image digest.

### Mount

```text
osix mount ghcr.io/acme/agent-state/research-agent-a:latest ./agentfs
```

Options:

```text
--rw
--decrypt age:key.txt
--cache ~/.cache/osix
--lazy
--branch main
```

### Run

```text
osix run ghcr.io/acme/agent-state/research-agent-a:latest -- claude-code /agent/workspace
```

If the first argument is a local mount path, the command runs against that mount:

```text
osix run ./agentfs -- claude-code /agent/workspace
```

### Shell

```text
osix shell ./agentfs
```

### Snapshot

```text
osix snapshot ./agentfs \
  --message "after adding agentic filesystem notes" \
  --push \
  --tag snap-000004 \
  --also-tag latest
```

### Push

```text
osix push main ghcr.io/acme/agent-state/research-agent-a
osix push snap-000004 --tag latest
```

Push uploads the selected snapshot manifest, config blob, layer blobs, and
reachable parent chain to the configured OCI repository. If `REGISTRY/REPO` is
omitted, the workspace `stateRef` repository is used. Digest refs are published
under their snapshot id unless `--tag` is supplied; mutable local refs are also
published as the same remote tag by default.

### Pull

```text
osix pull ghcr.io/acme/agent-state/research-agent-a:main
osix pull ghcr.io/acme/agent-state/research-agent-a:snap-000004 --tag restored-main
```

Pull resolves the remote manifest, records the resolved digest locally, fetches
the config and layer blobs, and recursively fetches parent snapshots. When the
remote reference is a tag, the local tag defaults to the same name unless
`--tag` overrides it. Digest pulls preserve immutable identity without creating
a mutable local tag unless requested.

Push and pull clients SHOULD discover credentials from explicit OSIx
environment variables first, then Docker-compatible registry auth config. The
client MUST keep credentials out of snapshot manifests, refs, logs, and local
content blobs. v0 supports Basic credentials and Bearer token challenges from
the OCI registry `WWW-Authenticate` response.

### Watch

```text
osix watch ./agentfs \
  --every 10m \
  --max-dirty 512MiB \
  --on-turn-boundary \
  --push \
  --encrypt age:age1xyz...
```

`--on-turn-boundary` SHOULD avoid snapshots during active tool calls.

### Restore

```text
osix restore ghcr.io/acme/agent-state/research-agent-a:snap-000002 ./agentfs
```

### Fork

```text
osix fork \
  ghcr.io/acme/agent-state/research-agent-a:snap-000003 \
  ghcr.io/acme/agent-state/research-agent-a:branch-risky-refactor
```

### Diff

```text
osix diff snap-000002 snap-000003
```

Example output:

```text
M /agent/skills/aws-network-firewall/SKILL.md
A /agent/memory/research/agentic-filesystems.md
D /agent/workspace/tmp/scratch.json
M /agent/state/checkpoints/langgraph.sqlite
```

### Compact

```text
osix compact ghcr.io/acme/agent-state/research-agent-a:main \
  --squash-every 25 \
  --keep-snapshots snap-000001,snap-000050
```

## Daemon Responsibilities

`osixd` owns long-lived local operations:

- registry client
- content-addressed blob cache
- decryptor
- snapshot resolver
- overlay or FUSE mount manager
- diff generator
- signer
- pusher
- watch scheduler
- side-effect replay policy enforcement

The CLI MAY call library code directly for simple commands in v0. A daemon becomes necessary when mounts, watches, and background pushes need lifecycle management.

## Go API

```go
type SnapshotConfig struct {
    AgentID      string
    BaseDigest   digest.Digest
    ParentDigest digest.Digest
    Sequence     int64
    CreatedAt    time.Time
    StateRoots   []StateRoot
    Encryption   EncryptionConfig
    Integrity    IntegrityConfig
}

type Snapshotter interface {
    Mount(ctx context.Context, ref string, target string, opts MountOptions) error
    Snapshot(ctx context.Context, target string, opts SnapshotOptions) (digest.Digest, error)
    Push(ctx context.Context, snapshot digest.Digest, ref string) error
    Restore(ctx context.Context, ref string, target string) error
    Fork(ctx context.Context, sourceRef string, targetRef string) error
    Compact(ctx context.Context, ref string, policy CompactPolicy) error
}
```

## Exit Behavior

Commands MUST return non-zero when:

- registry resolution fails
- push or pull fails to verify a manifest, config, or layer digest
- decryption fails
- integrity verification fails
- secret policy blocks snapshot creation
- branch update detects a conflict
- restore would overwrite non-empty local state without explicit force
