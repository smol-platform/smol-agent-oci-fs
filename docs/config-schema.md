# OSIx Config Schema v0.1

This document describes the implemented local prototype schema. The broader target schema is in [specs/01-oci-artifact-model.md](../specs/01-oci-artifact-model.md).

## Workspace Config

`osix init` writes `.osix/config.toml`:

```toml
osix_version = "0.1"
name = "research-agent-a"
base = "ghcr.io/acme/agent-base:2026-06-19"
base_digest = "sha256:..."
state_ref = "local/research-agent-a"
mount = "./agentfs"
default_branch = "main"
encrypt = "age:age1..."
```

## Snapshot Config Blob

Each snapshot manifest points to a JSON config blob with:

- `osixVersion`
- `agent`
- `base`
- `parent`
- `runtime`
- `stateRoots`
- `snapshot`
- `integrity`
- `tree`

The `tree` field is prototype-specific. It records a path index used by `osix diff` and restore verification tests.

Mount metadata is stored under `.osix/mounts/*.json` and records the materialized mount target, source digest, branch, and writable mode. Snapshotting a mounted tree uses this metadata to set the parent digest.

## Manifest Shape

Each local snapshot is an OCI image manifest with:

- `mediaType`: `application/vnd.oci.image.manifest.v1+json`
- `artifactType`: `application/vnd.osix.agent.snapshot.v1`
- config media type: `application/vnd.osix.agent.config.v1+json`
- layer media type: `application/vnd.osix.agent.layer.diff.v1.tar+zstd`

Encrypted layers use:

- `application/vnd.osix.agent.layer.diff.v1.tar+zstd+encrypted`

Signature and provenance blobs use:

- `application/vnd.osix.agent.signature.v1+json`
- `application/vnd.osix.agent.provenance.v1+json`
