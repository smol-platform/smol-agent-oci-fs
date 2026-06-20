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
./osix restore localhost:5000/acme/research-agent:snap-000003 ./restored
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

The current KMS path is a local deterministic envelope keyed by the recipient string. It exercises OSIx recipient and descriptor semantics without calling AWS KMS.

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
