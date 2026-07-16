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
./osix push main --expected-parent "$(cat .osix/refs/snap-000002)"
./osix pull localhost:5000/acme/research-agent:main --tag pulled-main
./osix restore localhost:5000/acme/research-agent:snap-000003 ./restored
```

If `--expected-parent` sees that the remote branch tag no longer points at the
expected digest, `osix push` and `osix snapshot --push` fail with a remote branch
conflict and exit code `3`. This is a portable stale-head check, not an atomic
registry-side compare-and-swap.

To defer layer downloads until a specific file is needed:

```sh
./osix pull localhost:5000/acme/research-agent:main --tag lazy-main --lazy
./osix read lazy-main /agent/workspace/notes.md
```

`pull --lazy` stores snapshot manifests, configs, and remote layer locations.
`read` fetches and caches a missing unencrypted layer only when the requested
file is found there. `restore` and runtime mount preparation can use the same
remote source metadata to fetch and cache missing whole layers when they need to
materialize an unencrypted complete lowerdir. Encrypted lazy refs with lazy
indexes can restore with `--decrypt` from encrypted per-file blobs without
fetching the whole encrypted layer.

Browse or extract data directly from a local or remote snapshot reference:

```sh
./osix browse lazy-main /agent/workspace
./osix browse lazy-main /agent/workspace --plain
./osix browse lazy-main /agent/workspace --json
./osix extract lazy-main /agent/workspace/results ./results
./osix extract lazy-main /agent/workspace/report.json ./report.json
```

Non-interactive `browse` reads only manifest/config tree metadata. Interactive
file previews use range reads capped at 64 KiB. `extract` verifies the composed
snapshot tree before atomically installing the selected path; pass `--force`
only when the destination should be replaced. Add `--decrypt IDENTITIES` to
either command for encrypted snapshots.

For encrypted snapshots, `read --decrypt` can use encrypted per-file lazy blobs,
and range reads can fetch only the needed encrypted lazy chunks:

```sh
./osix read lazy-main /agent/workspace/large.bin \
  --decrypt gpg:test-recipient \
  --offset 65536 \
  --length 4096
```

For authenticated registries, either export explicit OSIx credentials:

```sh
export OSIX_REGISTRY_USERNAME=robot
export OSIX_REGISTRY_PASSWORD='...'
```

or log in with Docker-compatible tooling so `~/.docker/config.json` contains an
auth entry or `credHelpers`/`credsStore` entry for the target registry.
`OSIX_REGISTRY_TOKEN` can be used when a registry or automation system provides
a bearer token directly.

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

The same harness has provider profiles for the hosted registries in the
compatibility matrix. The profile is detected from the repository host, or can
be set explicitly with `OSIX_HOSTED_REGISTRY_PROVIDER`.

```sh
# AWS ECR. Authenticate first with:
# aws ecr get-login-password --region us-east-1 |
#   docker login --username AWS --password-stdin 123456789012.dkr.ecr.us-east-1.amazonaws.com
OSIX_HOSTED_REGISTRY_PROVIDER=ecr \
OSIX_HOSTED_REGISTRY_REPO=123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat \
  ./scripts/test-registry-hosted.sh

# Google Artifact Registry. Authenticate first with:
# gcloud auth configure-docker us-docker.pkg.dev
OSIX_HOSTED_REGISTRY_PROVIDER=gar \
OSIX_HOSTED_REGISTRY_REPO=us-docker.pkg.dev/PROJECT/REPOSITORY/osix-compat \
  ./scripts/test-registry-hosted.sh

# Azure Container Registry. Authenticate first with:
# az acr login --name REGISTRY
OSIX_HOSTED_REGISTRY_PROVIDER=acr \
OSIX_HOSTED_REGISTRY_REPO=REGISTRY.azurecr.io/osix-compat \
  ./scripts/test-registry-hosted.sh
```

To generate the provider-specific repository exports from logged-in cloud CLIs,
run:

```sh
./scripts/setup-hosted-registry-env.sh \
  --provider all \
  --repo-name osix-compat \
  --ecr-region us-east-1 \
  --gar-project PROJECT \
  --gar-location us \
  --gar-repository osix-compat \
  --acr-registry REGISTRY \
  --acr-resource-group RESOURCE_GROUP \
  --acr-location eastus \
  --acr-sku Basic \
  --env-file ./hosted-registry.env
```

By default the setup helper only prints exports plus the cloud create/login
commands to review. Add `--create` to ensure supported ECR/GAR repositories
and Azure Container Registry registries exist, and add `--login` to run the
Docker-compatible login commands. For ACR, `--create` requires
`--acr-resource-group`; `--acr-location` and `--acr-sku` default to `eastus` and
`Basic`. Run `./scripts/test-registry-hosted-setup.sh` to regression-test the
generated provider exports without cloud credentials.

To run all configured hosted providers as a matrix:

```sh
export OSIX_HOSTED_REGISTRY_GHCR_REPO=ghcr.io/OWNER/osix-compat
export OSIX_HOSTED_REGISTRY_ECR_REPO=123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat
export OSIX_HOSTED_REGISTRY_GAR_REPO=us-docker.pkg.dev/PROJECT/REPOSITORY/osix-compat
export OSIX_HOSTED_REGISTRY_ACR_REPO=REGISTRY.azurecr.io/osix-compat

# Optional: fail instead of skipping when a listed provider is not ready.
export OSIX_HOSTED_REGISTRY_REQUIRED=ecr,gar,acr

# Optional: write one JSON evidence file per successful provider run.
export OSIX_HOSTED_REGISTRY_EVIDENCE_DIR=./hosted-registry-evidence

# Optional: write one JSON summary for the whole matrix run.
export OSIX_HOSTED_REGISTRY_MATRIX_REPORT=./hosted-registry-matrix.json

./scripts/test-registry-hosted-matrix.sh
```

To verify a configured repository has basic OCI write/read permissions before
the heavier signed snapshot round trip, run:

```sh
OSIX_HOSTED_REGISTRY_PROVIDER=ecr \
OSIX_HOSTED_REGISTRY_REPO=123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat \
OSIX_HOSTED_REGISTRY_PROBE_DIR=./hosted-registry-probe \
  ./scripts/probe-hosted-registry.sh
```

The probe publishes a tiny OCI config, layer, and manifest under an
`osix-probe-*` tag, then reads the manifest and layer back through the same
registry auth path used by `osix push`. When `OSIX_HOSTED_REGISTRY_PROBE_DIR`
is set, the probe writes JSON evidence for both passed and failed runs. Failed
records include `failureClass`, `failureHint`, `exitCode`, and a sibling
`logFile` path when the failure matches a known registry/auth pattern.

Before spending time on the full signed snapshot matrix, verify the probe
bundle:

```sh
./scripts/verify-hosted-registry-probes.sh \
  ./hosted-registry-probe \
  ecr,gar,acr
```

After the repository variables and credentials are configured, the preferred
one-command live verification flow is:

```sh
./scripts/run-hosted-registry-verification.sh \
  --providers ecr,gar,acr \
  --env-file ./hosted-registry.env \
  --out-dir ./hosted-registry-run
```

The runner writes preflight, lightweight probe evidence, provider logs, full
verification evidence, and aggregate matrix JSON under the output directory,
plus a top-level `run.json` summary with the final result, failed stage, paths,
and provider list. It verifies both the probe bundle and full evidence bundle,
and updates `docs/compatibility-matrix.md` from verified evidence. Pass
`--skip-update-docs` to stop after evidence verification. Run
`./scripts/test-registry-hosted-runner.sh` to regression-test the runner without
live registry credentials.

To verify a completed output directory as a single bundle:

```sh
./scripts/verify-hosted-registry-run.sh \
  ./hosted-registry-run \
  ecr,gar,acr
```

To summarize a passed or failed output directory for handoff:

```sh
./scripts/summarize-hosted-registry-run.sh ./hosted-registry-run
```

The summary prints the final result/stage, provider readiness, probe outcome,
failure class/hint when present, log paths, and provider-specific next steps for
known registry/auth failures. For recognized ECR/GAR/ACR write/auth failures it
also prints copyable create/login/rerun commands inferred from the configured
repository.

To check whether a machine is ready before running live hosted tests, generate a
preflight report:

```sh
OSIX_HOSTED_REGISTRY_PREFLIGHT_ONLY=1 \
OSIX_HOSTED_REGISTRY_PREFLIGHT_REPORT=./hosted-registry-preflight.json \
  ./scripts/test-registry-hosted-matrix.sh
```

For a human-readable summary of the same readiness data, run:

```sh
./scripts/doctor-hosted-registry.sh \
  --providers ecr,gar,acr \
  --required ecr,gar,acr \
  --report ./hosted-registry-preflight.json
```

The preflight JSON records each provider's repository variable, registry host,
whether a repo is configured, whether the repo matches the provider-specific
shape, provider CLI availability, OSIx credential sources, Docker credential
hosts, Docker credential-helper executable availability, provider cloud CLI
identity status, and whether Docker has usable credentials for the configured
registry host. If
`OSIX_HOSTED_REGISTRY_REQUIRED` is set in preflight mode, the script exits
non-zero when any required provider is missing its repo, has a malformed repo,
is missing a provider CLI, or lacks usable credentials. The live matrix applies
the same required-provider preflight gate before starting any provider push/pull
work.

When Docker already has credentials for known hosted registry hosts but the repo
variables are missing, preflight includes `credentialedRegistryHosts` and
`suggestedRepositoryExports` to help construct the missing
`OSIX_HOSTED_REGISTRY_*_REPO` values.

Run `./scripts/test-registry-hosted-preflight.sh` to regression-test the
machine-readable preflight contract without live registry credentials. Run
`./scripts/test-registry-hosted-doctor.sh` to regression-test the human-readable
doctor without live registry credentials.
Run `./scripts/test-registry-hosted-probe.sh` and
`./scripts/test-registry-hosted-probe-verifier.sh` to regression-test probe
evidence and probe verification without live registry credentials.
Run `./scripts/test-registry-hosted-run-verifier.sh` to regression-test the
completed-run bundle verifier.
Run `./scripts/test-registry-hosted-run-summary.sh` to regression-test the
human-readable run summary.
Run `./scripts/test-registry-hosted-matrix-report.sh` to regression-test the
aggregate matrix report for skipped, passed, and failed provider outcomes
without live registry credentials.
Run `./scripts/test-registry-hosted-local.sh` to execute the full hosted local
smoke suite before attempting a live ECR/GAR/ACR run.

Each evidence file records the provider, repository, unique remote tag, UTC test
time, authentication source class, operations covered, and `result: passed`.
The aggregate matrix report records the selected providers, required providers,
per-provider result, counts, exit code, final matrix result, provider log paths,
structured failure class/hint for recognized provider failures, and evidence
file paths discovered for each passing provider. Attach the per-provider logs,
per-provider evidence files, and aggregate report when updating
`docs/compatibility-matrix.md` from pending-live-evidence to supported.

Before updating the compatibility matrix, verify the live artifacts:

```sh
./scripts/verify-hosted-registry-evidence.sh \
  ./hosted-registry-matrix.json \
  ecr,gar,acr
```

Then update the hosted rows from the verified evidence:

```sh
./scripts/update-hosted-registry-compatibility.sh \
  ./hosted-registry-matrix.json \
  docs/compatibility-matrix.md
```

Run `./scripts/test-registry-hosted-evidence.sh` to regression-test that
verifier without live registry credentials. Run
`./scripts/test-registry-hosted-compatibility-update.sh` to regression-test the
compatibility-matrix updater without live registry credentials.

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

Age-only snapshots use age v1 directly. Single `kms:aws:kms:...` snapshots keep the legacy local KMS-style envelope unless provider mode is enabled. Mixed-recipient snapshots use the OSIx layer envelope: one random content key encrypted to each recipient.

Provider-backed wrapping is opt-in:

```sh
# AWS KMS through the AWS CLI.
OSIX_KMS_PROVIDER=aws \
  ./osix snapshot agentfs --tag aws-kms \
  --encrypt kms:aws:kms:us-east-1:123456789012:key/demo

OSIX_KMS_PROVIDER=aws \
  ./osix restore aws-kms ./aws-kms-restore \
  --decrypt kms:aws:kms:us-east-1:123456789012:key/demo

# GPG through the gpg command.
OSIX_GPG_PROVIDER=gpg \
  ./osix snapshot agentfs --tag gpg \
  --encrypt gpg:alice@example.com

OSIX_GPG_PROVIDER=gpg \
  ./osix restore gpg ./gpg-restore --decrypt gpg:alice@example.com

# Generic HTTPS endpoint protocol.
OSIX_ENDPOINT_PROVIDER=http \
  ./osix snapshot agentfs --tag endpoint \
  --encrypt endpoint:https://keys.example.test/wrap

OSIX_ENDPOINT_PROVIDER=http \
  ./osix restore endpoint ./endpoint-restore \
  --decrypt endpoint:https://keys.example.test/wrap
```

Without provider mode, KMS, GPG, and endpoint recipients use local deterministic key-wrap shims for offline development and repeatable tests. Command-provider hooks are available through `OSIX_KMS_WRAP_COMMAND` and `OSIX_KMS_UNWRAP_COMMAND`; `OSIX_AWS_COMMAND`, `OSIX_GPG_COMMAND`, `OSIX_GPG_HOMEDIR`, `OSIX_ENDPOINT_TOKEN`, and `OSIX_KEYWRAP_TIMEOUT` tune the concrete providers.

## Watch

```sh
touch agentfs/.osix-turn-boundary
./osix watch agentfs --once --max-dirty 1 --on-turn-boundary
```

Watch writes lifecycle state under `.osix/watch/`.

For overlay/FUSE mounts, watch uses runtime upperdir dirty bytes when available rather than counting the whole merged tree.

For long-running local watches:

```sh
./osix watch start agentfs --every 10m --max-dirty 512MiB --push
./osix watch list
./osix watch status agentfs
./osix watch restart agentfs
./osix watch stop agentfs
```

The daemon lifecycle stores a daemon record, heartbeat state, stop file, and log
under `.osix/watch/`. Status marks stale daemon records when the heartbeat stops
advancing. `watch restart` reuses the prior watch options for stale, stopped, or
failed records and refuses to replace a fresh running daemon.

For long-running agents that should bound snapshot-chain growth, enable watch
retention. This keeps the normal interval/dirty-byte snapshot behavior, pushes
the watch snapshot, creates a checkpoint when the chain reaches the threshold,
pushes the checkpoint to the branch tags with an expected-parent check, and then
prunes old local refs/blobs plus remote manifests when explicitly requested:

```sh
./osix watch start agentfs \
  --every 10m \
  --max-dirty 512MiB \
  --push \
  --compact-every 1 \
  --squash-every 50 \
  --checkpoint-tag-prefix checkpoint \
  --preserve-signed \
  --prune-local \
  --prune-remote
```

Use `--retention-dry-run` to record the compaction plan in watch state without
creating checkpoints or pruning. Remote pruning issues OCI Distribution manifest
deletes and requires registry-side deletion support plus credentials authorized
to delete manifests.

An hourly snapshot stream with an approximately daily checkpoint can be
configured with count-based retention:

```sh
./osix watch start agentfs \
  --every 1h \
  --push \
  --compact-every 24 \
  --squash-every 1 \
  --checkpoint-tag-prefix daily
```

To create a full checkpoint every hour instead of hourly deltas, use
`--compact-every 1 --squash-every 1 --checkpoint-tag-prefix hourly`.
Watch-generated checkpoint tags contain both the source sequence and a digest
suffix so successive checkpoints remain individually addressable.

The current policy does not yet express tiered time windows such as “retain one
hourly checkpoint for 2 days, then one daily checkpoint for 7 days.” Local and
remote pruning operates on the compacted chain rather than hourly/daily age
buckets, so enabling `--prune-local` or `--prune-remote` in the example would
remove the hourly chain members as soon as the daily checkpoint is created.
Registry lifecycle rules can enforce age limits externally, but exact tiered
retention requires time-bucket fields in the OSIx retention policy.

## Compaction

```sh
./osix compact main --dry-run --squash-every 2 --preserve-signed
./osix compact main --squash-every 2 --tag checkpoint-main --prune-local
./osix restore checkpoint-main ./checkpoint-restore
```

To compact an encrypted chain, provide its decrypt identity. If encryption was
set only on individual snapshots rather than in workspace configuration, also
provide the checkpoint recipient:

```sh
./osix compact encrypted-main \
  --squash-every 24 \
  --tag encrypted-checkpoint \
  --decrypt ./age-identity.txt \
  --encrypt age:age1...
```

Automatic watch compaction builds the checkpoint from the already materialized
watch target, so it does not require a decrypt identity and preserves the watch
or workspace encryption recipient.

Manual compaction is conservative by default: the source branch head is kept
unless a retention policy creates a checkpoint that replaces the branch/latest
tags. The live Linux/container registry retention check is:

```sh
./scripts/test-retention-docker.sh
```
