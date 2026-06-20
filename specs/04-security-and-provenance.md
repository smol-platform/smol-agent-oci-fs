# Spec 04: Security And Provenance

## Purpose

Define OSIx confidentiality, integrity, signing, provenance, and secret hygiene requirements.

## Encryption

OSIx uses envelope encryption per layer.

Each encrypted layer gets:

```text
DEK = random 256-bit key
ciphertext = encrypt(layer.tar.zst, DEK)
wrappedDEK = encrypt(DEK, recipient public key or KMS key)
```

v0 SHOULD support:

- `age:age1...`
- `kms:aws:kms:region:account:key/key-id`

Future versions MAY support:

- `jwe:...`
- additional cloud KMS providers
- hardware-backed recipients

## Encryption Metadata

Layer descriptors MAY include minimal public encryption metadata:

```json
{
  "mediaType": "application/vnd.osix.agent.layer.diff.v1.tar+zstd+encrypted",
  "digest": "sha256:ciphertextDigest",
  "size": 987654321,
  "annotations": {
    "com.osix.encryption.alg": "xchacha20poly1305",
    "com.osix.encryption.keywrap": "age",
    "com.osix.encryption.recipients": "3",
    "com.osix.plaintext.digest": "sha256:plaintextDigest",
    "com.osix.plaintext.size": "1234567890"
  }
}
```

Public annotations MUST NOT expose sensitive path lists, secret names, prompts, memory content, tool inputs, or external resource payloads. Path-level indexes belong inside encrypted blobs.

## Signing

Encryption does not prove who created a snapshot. OSIx snapshots SHOULD be signed by manifest digest.

v0 SHOULD integrate with cosign-compatible signing:

```text
osix snapshot ./agentfs \
  --push \
  --encrypt kms:aws:kms:... \
  --sign keyless \
  --attest slsa
```

Signatures and attestations SHOULD be attached as OCI referrers to the snapshot
manifest. v0 also publishes deterministic fallback tags for registries that can
store subject-bearing manifests but cannot list them through the Referrers API.
Pull clients import signature/provenance artifacts when present, but basic
restore remains possible without them unless verification policy requires a
signature.

## Provenance

Provenance SHOULD record:

- creator identity
- base image digest
- parent snapshot digest
- command and version used to create the snapshot
- policy results
- source machine or CI identity when available
- signing identity
- timestamp

For higher-trust environments, deployments MAY require:

- publisher signature
- registry countersignature
- trusted registry identity pinning
- consumer-side signature enforcement

## Secret Hygiene

Agents may accidentally write secrets. Snapshot creation MUST apply path exclusions before creating the tar layer.

Default deny list:

```text
/agent/secrets/**
**/.env
**/id_rsa
**/id_ed25519
```

Default excludes:

```text
/agent/tmp/**
/agent/cache/**
**/node_modules/.cache/**
**/__pycache__/**
```

The CLI SHOULD support:

```text
osix snapshot ./agentfs --secret-scan block
osix snapshot ./agentfs --secret-scan warn
osix snapshot ./agentfs --secret-scan off
```

`block` is the recommended default for pushed snapshots.

## Restore Safety

Restoring or forking a snapshot MUST NOT automatically replay external side effects.

Tool integrations SHOULD default to one of these modes after restore:

- mock
- read-only
- require approval

## Integrity

Snapshot config SHOULD include:

- plaintext layer digest
- ciphertext layer digest
- mtree digest
- Merkle root when chunked storage exists
- signature referrer digest when available

Clients SHOULD fail closed when required integrity metadata is missing or invalid.
