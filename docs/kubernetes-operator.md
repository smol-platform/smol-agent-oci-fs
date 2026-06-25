# Kubernetes Operator And CSI Runtime

OSIx exposes agent state to Kubernetes through an operator-owned API and a node
runtime that delegates filesystem work to the existing OSIx library.

## What This Is For

The operator is for clusters that run agents whose working state should be
portable outside the lifetime of a pod or node. Instead of treating agent state
as an opaque PersistentVolume, OSIx stores it as OCI artifacts in a registry.
That makes state easy to copy between clusters, inspect by digest, sign, verify,
encrypt, compact, retain, and restore into a new agent.

Use it when you need:

- agent handoff from one pod, node, or cluster to another.
- resumable workspaces for long-running agents.
- registry-backed state images alongside normal container images.
- incremental snapshots and checkpoint compaction for large workspaces.
- explicit branch tags such as `main` and immutable snapshot digests.
- policy-controlled snapshot cadence and pruning.
- a path to verify or sign agent state before restore.

The first Kubernetes surface is intentionally small and deterministic: the
operator owns CRDs, manifests, health endpoints, and planning; the CSI node
runtime invokes OSIx library calls for publish, snapshot, retention, and
unpublish. Full CSI gRPC sidecars and richer controller reconciliation can wrap
the same runtime boundary.

The current implementation provides:

- CRD schemas for `AgentOCIFileSystem`, `AgentOCISnapshotPolicy`,
  `AgentOCISnapshot`, and `AgentOCIRuntimeClass`.
- Static install manifests under `deploy/kubernetes`.
- `osix-k8s-operator render-install` for a single manifest stream.
- `osix-k8s-operator plan` for deterministic publish plans.
- `osix-k8s-operator serve` health and readiness endpoints.
- `osix-csi-node` publish, snapshot, and unpublish commands backed by the OSIx
  mount, watch, registry, compaction, and retention APIs.

The next cluster-native step is to wrap the same node runtime in the Kubernetes
CSI gRPC lifecycle and connect controller reconciliation to CRD status updates.

## Install

Build and publish images:

```sh
docker buildx build --platform linux/amd64 \
  -f Dockerfile.operator \
  -t ghcr.io/smol-platform/smol-agent-oci-fs-operator:latest \
  --push .

docker buildx build --platform linux/amd64 \
  -f Dockerfile.csi \
  -t ghcr.io/smol-platform/smol-agent-oci-fs-csi:latest \
  --push .
```

Install manifests:

```sh
kubectl apply -k deploy/kubernetes
```

For a rendered manifest stream:

```sh
go run ./cmd/osix-k8s-operator render-install | kubectl apply -f -
```

Private image registries need an image pull secret:

```sh
kubectl -n osix-system create secret docker-registry ghcr-smol-platform-pull \
  --docker-server=ghcr.io \
  --docker-username="$GITHUB_USER" \
  --docker-password="$GITHUB_TOKEN"

kubectl -n osix-system patch deployment osix-operator --type merge \
  -p '{"spec":{"template":{"spec":{"imagePullSecrets":[{"name":"ghcr-smol-platform-pull"}]}}}}'

kubectl -n osix-system patch daemonset osix-csi-node --type merge \
  -p '{"spec":{"template":{"spec":{"imagePullSecrets":[{"name":"ghcr-smol-platform-pull"}]}}}}'
```

Verify rollout:

```sh
kubectl -n osix-system rollout status deployment/osix-operator
kubectl -n osix-system rollout status daemonset/osix-csi-node
kubectl -n osix-system get pods -o wide
```

## Resources

`AgentOCIFileSystem` declares the state repository, base image, branch,
optional source ref, mount mode, registry secret, encryption/signing settings,
snapshot policy, and runtime class.

`AgentOCISnapshotPolicy` declares snapshot cadence, dirty-byte thresholds,
turn-boundary behavior, push behavior, compaction, checkpoint tags, and local or
remote pruning.

`AgentOCIRuntimeClass` declares node runtime preferences such as overlay,
FUSE, lazy FUSE, materialized fallback, cache root, runtime image, and node
selectors.

`AgentOCISnapshot` records snapshots and checkpoints produced by the runtime.

## Examples

Apply the example runtime, policy, filesystem, PVC, and pod:

```sh
kubectl apply -f deploy/kubernetes/examples/runtime-class.yaml
kubectl apply -f deploy/kubernetes/examples/snapshot-policy.yaml
kubectl apply -f deploy/kubernetes/examples/filesystem.yaml
kubectl apply -f deploy/kubernetes/examples/pvc.yaml
kubectl apply -f deploy/kubernetes/examples/pod.yaml
```

Set `spec.stateRef` to a registry repository that accepts OCI Distribution
pushes from the node driver.

## Snapshot And Restore Flow

A typical agent lifecycle is:

1. Create or mount an `AgentOCIFileSystem`.
2. Start the agent workload with the OSIx target mounted.
3. Run `osix watch` or the CSI-node snapshot path with a policy.
4. Push snapshots and checkpoint tags to `spec.stateRef`.
5. Launch another agent with `sourceRef` set to `STATE_REPO:main`.
6. Verify the new agent sees the prior workspace, memory, and handoff files.

The deterministic node command path looks like this:

```sh
osix-csi-node publish \
  --workspace-root /var/lib/osix \
  --target /state \
  --volume-id pvc-agent-a \
  --name agent-a \
  --state ghcr.io/acme/agent-a-state \
  --base ghcr.io/acme/agent-base:latest \
  --mode materialized

osix-csi-node snapshot \
  --workspace-root /var/lib/osix \
  --target /state \
  --volume-id pvc-agent-a \
  --name agent-a \
  --state ghcr.io/acme/agent-a-state \
  --base ghcr.io/acme/agent-base:latest \
  --push \
  --max-dirty 1MiB \
  --compact-every 1 \
  --squash-every 2 \
  --checkpoint-tag-prefix checkpoint

osix-csi-node publish \
  --workspace-root /var/lib/osix \
  --target /state \
  --volume-id pvc-agent-b \
  --name agent-b \
  --state ghcr.io/acme/agent-a-state \
  --base ghcr.io/acme/agent-base:latest \
  --source ghcr.io/acme/agent-a-state:main \
  --mode materialized
```

For a workload-level watch loop:

```sh
osix init ghcr.io/acme/agent-base:latest \
  --name agent-a \
  --state ghcr.io/acme/agent-a-state \
  --mount ./agentfs

osix watch agentfs \
  --every 30s \
  --max-dirty 1MiB \
  --push \
  --compact-every 10 \
  --squash-every 50 \
  --checkpoint-tag-prefix checkpoint
```

## Registry Credentials

The operator API models registry credentials with `registrySecretRef`. Node
runtime processes should project that secret into OSIx-compatible environment
or Docker config material. OSIx currently discovers registry credentials from:

- `OSIX_REGISTRY_TOKEN`
- `OSIX_REGISTRY_USERNAME` and `OSIX_REGISTRY_PASSWORD`
- Docker `config.json` auth entries
- Docker credential helpers under `DOCKER_CONFIG` or `~/.docker/config.json`

Credential values must not be copied into CRD status or snapshot metadata.

For workloads that invoke `osix watch` directly, project credentials into the
container as environment variables:

```yaml
env:
  - name: OSIX_REGISTRY_USERNAME
    valueFrom:
      secretKeyRef:
        name: osix-registry-auth
        key: username
  - name: OSIX_REGISTRY_PASSWORD
    valueFrom:
      secretKeyRef:
        name: osix-registry-auth
        key: password
```

For private runtime images, use normal Kubernetes `imagePullSecrets`; do not
reuse the OSIx push credentials unless that is intentional.

## Observability

The operator and CSI node expose `/healthz` and `/readyz` endpoints from their
`serve` commands. Status helpers use `Ready`, `Mounted`, `Snapshotting`,
`SnapshotFailed`, `Checkpointed`, and `RegistryReady` conditions.

Metric names reserved for the controller and node runtime are:

- `osix_operator_reconcile_total`
- `osix_operator_reconcile_errors_total`
- `osix_csi_publish_total`
- `osix_csi_unpublish_total`
- `osix_csi_snapshot_total`
- `osix_csi_snapshot_errors_total`
- `osix_csi_checkpoint_total`

## Local Integration Test

Run the deterministic Docker test:

```sh
scripts/test-k8s-operator-docker.sh
```

The test starts a local OCI registry, builds `osix`, `osix-k8s-operator`, and
`osix-csi-node`, validates rendered manifests and plans, publishes a volume,
writes through the mount target, snapshots and pushes, compacts into a
checkpoint, restores from the remote `main` tag into a second volume, and
unpublishes both volumes.

## Live Cluster Verification

A useful cluster acceptance test is:

1. Deploy the operator and CSI node image.
2. Create registry credentials in a temporary namespace.
3. Run a writer Job that starts `osix watch --push`, mutates
   `/agent/workspace`, and waits for snapshots/checkpoints.
4. Pull `STATE_REPO:main` from outside the cluster and restore it locally.
5. Run a reader Job that publishes with `--source STATE_REPO:main`.
6. Assert the reader sees the writer's transcript and handoff marker.
7. Delete the temporary namespace.

This verifies more than pod readiness: it proves the deployed runtime can push
OCI state and that another agent can launch from the pushed state image.
