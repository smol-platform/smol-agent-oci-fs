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

## Manifest Shape

Each local snapshot is an OCI image manifest with:

- `mediaType`: `application/vnd.oci.image.manifest.v1+json`
- `artifactType`: `application/vnd.osix.agent.snapshot.v1`
- config media type: `application/vnd.osix.agent.config.v1+json`
- layer media type: `application/vnd.osix.agent.layer.diff.v1.tar+zstd`

