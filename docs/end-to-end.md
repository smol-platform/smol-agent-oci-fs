# OSIx End-To-End Guide

This guide covers the v0 CLI workflow.

## Build

```sh
go build -o ./osix ./cmd/osix
```

## Local State Flow

```sh
./osix init example/base:latest \
  --name research-agent \
  --state localhost:5000/acme/research-agent \
  --mount ./agentfs

mkdir -p agentfs/agent/workspace
printf 'hello\n' > agentfs/agent/workspace/notes.md

./osix snapshot agentfs --tag snap-000001 --also-tag main --sign keyless --attest slsa
./osix verify snap-000001
./osix validate snap-000001
./osix mount snap-000001 ./mounted --mode auto --force
./osix mount status ./mounted
printf 'updated\n' > mounted/agent/workspace/notes.md
./osix diff ./mounted
./osix snapshot ./mounted --tag snap-000002 --also-tag main --expected-parent "$(cat .osix/refs/snap-000001)"
./osix mount recover ./mounted
./osix unmount ./mounted
./osix fork snap-000001 experiment-a
```

## Registry Flow

Start a local OCI registry such as Docker Distribution on `localhost:5000`, then:

```sh
./osix snapshot agentfs --tag snap-000003 --also-tag main --push
./osix push main
./osix pull localhost:5000/acme/research-agent:main --tag pulled-main
./osix restore localhost:5000/acme/research-agent:snap-000003 ./restored
```

For authenticated registries, either export explicit OSIx credentials:

```sh
export OSIX_REGISTRY_USERNAME=robot
export OSIX_REGISTRY_PASSWORD='...'
```

or log in with Docker-compatible tooling so `~/.docker/config.json` contains an
entry for the target registry. `OSIX_REGISTRY_TOKEN` can be used when a registry
or automation system provides a bearer token directly.

To run the local Docker Distribution compatibility harness:

```sh
./scripts/test-registry-docker.sh
```

To run the hosted compatibility harness, provide a repository and credentials:

```sh
export OSIX_HOSTED_REGISTRY_REPO=ghcr.io/OWNER/osix-compat
export OSIX_REGISTRY_USERNAME=robot
export OSIX_REGISTRY_PASSWORD='...'
./scripts/test-registry-hosted.sh
```

## Encryption

Age:

```sh
age-keygen -o age.key
RECIPIENT="$(age-keygen -y age.key)"
./osix snapshot agentfs --tag encrypted --encrypt "age:${RECIPIENT}"
./osix restore encrypted ./decrypted --decrypt ./age.key
```

KMS-style envelope:

```sh
./osix snapshot agentfs --tag kms \
  --encrypt kms:aws:kms:us-east-1:123456789012:key/demo
./osix restore kms ./kms-restore \
  --decrypt kms:aws:kms:us-east-1:123456789012:key/demo
```

Mixed-recipient envelope:

```sh
./osix snapshot agentfs --tag multi-recipient \
  --encrypt "age:${RECIPIENT},kms:aws:kms:us-east-1:123456789012:key/demo,gpg:alice@example.com,endpoint:https://keys.example.test/wrap"

./osix restore multi-recipient ./multi-age --decrypt ./age.key
./osix restore multi-recipient ./multi-kms \
  --decrypt kms:aws:kms:us-east-1:123456789012:key/demo
./osix restore multi-recipient ./multi-gpg --decrypt gpg:alice@example.com
./osix restore multi-recipient ./multi-endpoint \
  --decrypt endpoint:https://keys.example.test/wrap
```

Age-only snapshots use age v1 directly. Single `kms:aws:kms:...` snapshots keep the legacy local KMS-style envelope. Mixed-recipient snapshots use the OSIx layer envelope: one random content key encrypted to each recipient. KMS, GPG, and endpoint recipients are local deterministic key-wrap shims keyed by the recipient string in this prototype; they exercise recipient metadata and restore authorization semantics without calling AWS KMS, `gpg`, or a remote service.

## Watch

```sh
touch agentfs/.osix-turn-boundary
./osix watch agentfs --once --max-dirty 1 --on-turn-boundary
```

Watch writes lifecycle state under `.osix/watch/`.

For overlay/FUSE mounts, watch uses runtime upperdir dirty bytes when available rather than counting the whole merged tree.

## Compaction

```sh
./osix compact main --dry-run --squash-every 2 --preserve-signed
./osix compact main --squash-every 2 --tag checkpoint-main
./osix restore checkpoint-main ./checkpoint-restore
```
