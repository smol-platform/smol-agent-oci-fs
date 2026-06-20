# Spec 01: OCI Artifact Model

## Purpose

Define how OSIx represents agent snapshots in an OCI registry using standard registry primitives.

## Snapshot Identity

Each snapshot has:

- immutable manifest digest
- parent manifest digest, except for the root snapshot
- base image digest
- agent id
- branch name
- sequence number
- creation timestamp
- author or creator identity
- snapshot reason

Tags are mutable pointers. Digests are immutable identities. Restore operations SHOULD resolve tags to digests and persist the digest used.

## Media Types

Implementations MUST use custom media types outside the `org.opencontainers.image` namespace.

Required v0 media types:

```text
application/vnd.osix.agent.config.v1+json
application/vnd.osix.agent.layer.diff.v1.tar+zstd
application/vnd.osix.agent.layer.diff.v1.tar+zstd+encrypted
application/vnd.osix.agent.snapshot.manifest.v1+json
```

Recommended future media types:

```text
application/vnd.osix.agent.index.v1+json
application/vnd.osix.agent.memory.v1+json
application/vnd.osix.agent.skillpack.v1.tar+zstd
application/vnd.osix.agent.chunked-layer.v1+json
```

## Image Manifest Form

In v0, a snapshot SHOULD be publishable as a normal OCI image manifest. The manifest config descriptor points to an OSIx config blob. The layer descriptors point to zstd-compressed tar diff layers, encrypted when policy requires.

Example:

```json
{
  "schemaVersion": 2,
  "mediaType": "application/vnd.oci.image.manifest.v1+json",
  "artifactType": "application/vnd.osix.agent.snapshot.v1",
  "config": {
    "mediaType": "application/vnd.osix.agent.config.v1+json",
    "digest": "sha256:...",
    "size": 8192
  },
  "layers": [
    {
      "mediaType": "application/vnd.osix.agent.layer.diff.v1.tar+zstd+encrypted",
      "digest": "sha256:...",
      "size": 12345678,
      "annotations": {
        "com.osix.layer.kind": "filesystem-diff",
        "com.osix.parent.digest": "sha256:...",
        "com.osix.diff.algorithm": "overlayfs-whiteout-v1",
        "com.osix.encryption": "age+xchacha20poly1305",
        "com.osix.turn.range": "1204-1248"
      }
    }
  ],
  "annotations": {
    "com.osix.snapshot.id": "snap-000003",
    "com.osix.agent.id": "research-agent-a",
    "com.osix.parent": "sha256:parent-manifest",
    "com.osix.created": "2026-06-19T18:34:00Z",
    "com.osix.kind": "checkpoint",
    "com.osix.branch": "main"
  }
}
```

## Config Blob

The config blob is the agent state capsule manifest. It MUST be JSON in v0.

Required fields:

```json
{
  "osixVersion": "0.1",
  "agent": {
    "id": "research-agent-a",
    "name": "Agentic FS Researcher",
    "createdBy": "zach"
  },
  "base": {
    "image": "ghcr.io/acme/agent-base:2026-06-19",
    "digest": "sha256:base-image-manifest"
  },
  "parent": {
    "snapshot": "snap-000002",
    "digest": "sha256:parent-snapshot-manifest"
  },
  "runtime": {
    "entrypoint": ["/usr/local/bin/agent"],
    "workingDir": "/agent/workspace",
    "uid": 1000,
    "gid": 1000
  },
  "stateRoots": [
    {
      "path": "/agent/workspace",
      "mode": "cow"
    },
    {
      "path": "/agent/memory",
      "mode": "versioned"
    },
    {
      "path": "/agent/skills",
      "mode": "signed-versioned"
    }
  ],
  "snapshot": {
    "id": "snap-000003",
    "sequence": 3,
    "createdAt": "2026-06-19T18:34:00Z",
    "reason": "periodic",
    "turnStart": 1204,
    "turnEnd": 1248,
    "dirtyBytes": 104857600
  },
  "encryption": {
    "mode": "layer",
    "recipients": ["age1..."],
    "aad": {
      "manifestDigest": "sha256:...",
      "agentId": "research-agent-a"
    }
  },
  "integrity": {
    "mtreeDigest": "sha256:...",
    "merkleRoot": "sha256:...",
    "signatureRef": "sha256:..."
  }
}
```

## Referrer Artifacts

When supported by the registry, implementations SHOULD attach these artifacts as referrers to the snapshot manifest:

- signature
- provenance attestation
- SBOM
- memory index
- policy evaluation
- compacted snapshot index

Referrers MUST NOT be required for basic v0 restore. A client that can pull the snapshot manifest and its layers MUST be able to restore state.

For v0 image-mode compatibility, OSIx publishes signature and provenance
payloads as subject-bearing OCI image manifests whose `artifactType` is the OSIx
signature or provenance media type. The JSON payload is stored as the single
layer blob, and the manifest subject points at the snapshot manifest digest. The
same artifact manifests are also published under fallback tags derived from the
subject digest:

```text
signature-<snapshot-manifest-hex>
provenance-<snapshot-manifest-hex>
```

Pull clients SHOULD query the OCI Referrers API first and fall back to those
tags when a registry does not expose referrer discovery.

## Branch Pointers

Branches are represented by tags:

```text
:main
:latest
:snap-000003
:branch-risky-refactor
```

Immutable references are represented by digests:

```text
@sha256:...
```

Implementations SHOULD warn when restoring from mutable tags unless the resolved digest is recorded.
