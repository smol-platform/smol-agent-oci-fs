# smol-agent-oci-fs

OSIx is an OCI Agent State Image runtime. It packages the mutable filesystem
state of an AI agent as OCI artifacts so agent workspaces can be snapshotted,
pushed to a registry, verified, restored, forked, compacted, and mounted again
on another machine or Kubernetes node.

The project includes:

- `osix`, a CLI and Go library for creating, mounting, watching, snapshotting,
  pushing, pulling, restoring, signing, verifying, and compacting agent state.
- Linux filesystem runtimes for materialized directories, overlayfs,
  fuse-overlayfs, and native lazy FUSE copy-up.
- A macOS FSKit runtime path for native Darwin overlay-style mounts.
- OCI Distribution registry integration for GHCR, local registries, ECR-style
  registries, and standard Docker credential discovery.
- Encryption, signing, provenance, secret-scan policy, branch refs,
  expected-parent conflict checks, and retention checkpoints.
- Kubernetes operator and CSI-node components for running registry-backed agent
  filesystems in clusters.

Use OSIx when an agent needs a durable, portable, inspectable state image rather
than an opaque VM disk, ad hoc object-store dump, or one-off persistent volume.
The snapshot format is content-addressed, incremental, registry-native, and
designed around the parts of agent state that matter: workspace files, memory,
skills, side-effect ledgers, and handoff artifacts.

## Why OCI Agent State Images

Long-running agents create useful state that should survive beyond one process
or pod. They also need to move between environments: local development,
sandboxed execution, CI jobs, GPU nodes, and production clusters. OCI registries
already solve distribution, auth, retention policy, replication, and audit for
container images. OSIx uses that same infrastructure for agent state.

That gives you:

- portable agent handoff between machines and clusters.
- incremental snapshots instead of full directory uploads.
- branch tags such as `main`, checkpoint tags, and immutable digest refs.
- registry auth and compatibility with existing image operations.
- optional signing, verification, provenance, and encryption.
- compaction and pruning so long-running agents do not accumulate unbounded
  overlay history.
- a path to run agents on Kubernetes without inventing a new storage backend.

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
go build -o ./osix-k8s-operator ./cmd/osix-k8s-operator
go build -o ./osix-csi-node ./cmd/osix-csi-node
```

Build Kubernetes images:

```sh
OSIX_RELEASE_VERSION=v0.1.1 scripts/release-k8s-images.sh
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
./osix verify signed --trusted-key .osix/keys/cosign_ecdsa_p256.pem.pub
```

For public Sigstore keyless signing, use `sigstore-keyless` and provide an OIDC
identity token directly, from a file, or through `SIGSTORE_ID_TOKEN`:

```sh
./osix snapshot agentfs --tag sigstore-signed \
  --sign sigstore-keyless \
  --sigstore-identity-token-file ./oidc-token.jwt \
  --attest slsa
```

To require public Sigstore keyless policy for a pulled bundle, pass the
expected Fulcio certificate identity and OIDC issuer:

```sh
./osix verify sigstore-signed \
  --certificate-identity-regexp '^https://github.com/smol-platform/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
./osix restore sigstore-signed restored-verified \
  --certificate-identity-regexp '^https://github.com/smol-platform/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

For registry-backed use, initialize with a registry repository:

```sh
./osix init ghcr.io/acme/agent-base:2026-06-19 \
  --name research-agent-a \
  --state localhost:5000/acme/research-agent-a \
  --mount ./agentfs

./osix snapshot agentfs --tag snap-000001 --also-tag main --push
./osix push main
./osix pull localhost:5000/acme/research-agent-a:main --tag pulled-main
./osix restore localhost:5000/acme/research-agent-a:snap-000001 restored
```

Registry credentials are discovered from `OSIX_REGISTRY_TOKEN`,
`OSIX_REGISTRY_USERNAME`/`OSIX_REGISTRY_PASSWORD`, Docker `config.json` auth
entries, and Docker `credHelpers`/`credsStore` helpers under `DOCKER_CONFIG` or
`~/.docker/config.json`. The client handles Basic credentials, helper-returned
tokens, and Bearer `WWW-Authenticate` token challenges for push and pull
requests.

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

`auto` selects Linux kernel overlayfs when prerequisites are available, then the platform userspace runtime, then the portable materialized runtime. On Linux the default userspace runtime is `fuse-overlayfs`, while `--lazy` FUSE mounts use OSIx's native Go FUSE lower-store backend for read-through plus writable copy-up. On macOS it is a native FSKit app extension controlled by `osix-fskitctl`. Overlay/FUSE mode stores lower, upper, work, mount metadata, and dirty indexes under `.osix/mounts/<mount-id>/`; snapshotting an overlay/FUSE target uses the upperdir and whiteouts rather than packaging runtime internals.

## Kubernetes Operator And CSI Runtime

OSIx can be installed into Kubernetes with an operator Deployment, CRDs, a
node-local CSI runtime DaemonSet, RBAC, and a StorageClass. The Kubernetes
pieces let a cluster treat an agent filesystem as a registry-backed state image:
a workload writes files, node-local CSI workers snapshot and checkpoint them to
an OCI registry, and a later workload can launch from the pushed `main` tag.

The operator API defines:

- `AgentOCIFileSystem`: base image, state repository, branch, source ref,
  mount mode, registry secret, encryption/signing settings, snapshot policy,
  and runtime class.
- `AgentOCISnapshotPolicy`: watch cadence, dirty-byte threshold,
  turn-boundary behavior, push behavior, compaction, checkpoint tags, and local
  or remote pruning.
- `AgentOCISnapshot`: produced snapshot/checkpoint records.
- `AgentOCIRuntimeClass`: node runtime preferences such as overlay, FUSE,
  lazy FUSE, materialized fallback, cache root, runtime image, and node
  selectors.

Install:

```sh
kubectl apply -k deploy/kubernetes
```

Render a single install stream:

```sh
go run ./cmd/osix-k8s-operator render-install | kubectl apply -f -
```

Run the deterministic Docker integration test:

```sh
scripts/test-k8s-operator-docker.sh
```

Run the automatic snapshot Docker integration test:

```sh
scripts/test-k8s-autosnap-docker.sh
```

The operator exposes `/healthz` and `/readyz`, and the CSI node can run either
the health server or the kubelet-facing CSI gRPC server. The CSI node command
surface can also be used directly for deterministic tests:

```sh
osix-csi-node serve \
  --addr 127.0.0.1:18081 \
  --workspace-root /var/lib/osix \
  --enable-workers

osix-csi-node publish \
  --workspace-root /var/lib/osix \
  --target /var/lib/kubelet/pods/example/volumes/osix \
  --volume-id pvc-example \
  --name research-agent-a \
  --state ghcr.io/acme/research-agent-a \
  --base ghcr.io/acme/agent-base:latest \
  --mode materialized \
  --auto-snapshot \
  --every 30s \
  --max-dirty 1MiB \
  --push=true \
  --compact-every 1 \
  --squash-every 2

# The workload writes files only. The node worker snapshots and pushes.

osix-csi-node publish \
  --workspace-root /var/lib/osix \
  --target /tmp/new-agent \
  --volume-id pvc-restored \
  --name research-agent-b \
  --state ghcr.io/acme/research-agent-a \
  --base ghcr.io/acme/agent-base:latest \
  --source ghcr.io/acme/research-agent-a:main \
  --mode materialized
```

Repeatable Kubernetes checks live under `scripts/`:

- `scripts/test-k8s-autosnap-docker.sh` proves automatic worker snapshotting and
  second-agent restore against a local Docker registry.
- `scripts/test-k8s-autosnap-kind.sh` deploys the operator/CSI stack to Kind and
  verifies writer-to-reader restore through CSI.
- `scripts/test-k8s-autosnap-gtr.sh` runs the same flow against a live
  gtr-provisioned cluster and registry.

GitHub Actions run the self-contained coverage on pull requests and `main`:

- `CI` runs Go tests on Linux and macOS, shell syntax checks, the hosted
  registry harness smoke suite, Docker registry/retention/operator/autosnapshot
  integration tests, optional FUSE integration when `/dev/fuse` is available,
  and the Kind CSI autosnapshot E2E.
- `Images` builds the operator and CSI images with Docker Buildx for
  `linux/amd64` and `linux/arm64`; it pushes to GHCR on `main`, `v*` tags, or
  manual dispatch when image pushing is enabled.

See [docs/kubernetes-operator.md](docs/kubernetes-operator.md) for the detailed
operator design, install flow, image publishing, registry credentials, CRD
examples, and live workload verification pattern.

Implemented commands:

- `osix init`
- `osix snapshot`
- `osix push`
- `osix pull`
- `osix read`
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
- `osix watch start/status/stop/list/restart`
- `osix compact`
- `osix side-effect check`
- `osix run`
- `osix show`
- `osix refs`
- `osix-k8s-operator render-install/plan/serve`
- `osix-csi-node publish/snapshot/unpublish/serve/serve-csi`

## Current Limits

- `mount --mode overlay` requires Linux with overlayfs mount privileges, or macOS with the OSIx FSKit extension installed and enabled.
- `mount --mode fuse` uses `fuse-overlayfs` on Linux, native Go FUSE for lazy Linux mounts, and native FSKit on macOS.
- Other non-Linux builds fall back to the materialized runtime unless a nonportable mode is requested explicitly.
- Encryption supports age-only layers, a legacy local `kms:aws:kms:...` path, and mixed OSIx envelopes with `age:`, `kms:`, `gpg:`, and `endpoint:` recipients. Provider-backed wrapping is available through opt-in AWS CLI, `gpg`, and HTTP endpoint modes; offline local shims remain the default for KMS/GPG/endpoint recipients.
- `pull --lazy` plus `read` can defer unencrypted layer download until a file is requested. Lazy-pulled unencrypted refs can also be restored by fetching and caching missing whole layers during materialization. Encrypted snapshots emit encrypted per-file and chunked lazy blobs for age-only, legacy KMS, and OSIx-envelope `read --decrypt`; `read --offset N --length N --decrypt ...` can fetch only the needed encrypted lazy chunks. When encrypted lazy indexes are available, `restore --decrypt` can materialize the snapshot tree from encrypted lazy blobs without fetching the whole encrypted layer. The library has a snapshot lower-store API for metadata lookup, directory enumeration, and lazy content reads. Darwin FSKit and Linux native FUSE can use that path for lazy lower reads when mounted with `--lazy`, including encrypted reads when `--decrypt` material is supplied. Linux lazy FUSE supports writable copy-up and whiteouts without restoring the lowerdir first. Linux kernel overlay still materializes whole lower layers.
- `--sign keyless` is a local generated-key development mode that writes OSIx-native ed25519 signature/provenance artifacts and cosign/Sigstore-compatible registry artifacts. `--sign sigstore-keyless` uses Fulcio/Rekor-backed public Sigstore signing with OIDC identity-token input and emits certificate-backed Sigstore bundles. Public keyless verification is available through explicit `--certificate-*` Sigstore policy flags.
- `watch` supports one-shot bounded runs and a local `watch start/status/stop/list/restart` daemon lifecycle; daemon supervision is file/PID based and does not yet manage mounts, cache cleanup, or all background jobs.
- Side-effect ledger validation, replay markers, a generic `side-effect check` adapter gate, and provider adapters for GitHub issues, Gmail messages/drafts/sends, Google Calendar events, Linear issues/comments, and Slack messages/reactions are implemented; the adapter set is extensible rather than exhaustive.

See [docs/runtime-filesystems.md](docs/runtime-filesystems.md) for runtime prerequisites and operational behavior.
See [docs/kubernetes-operator.md](docs/kubernetes-operator.md) for the Kubernetes operator, CSI node runtime, manifests, and Docker integration test.
