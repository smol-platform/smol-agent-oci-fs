# Spec 07: Kubernetes Operator And CSI Runtime

## Purpose

Define the Kubernetes control plane and CSI node runtime for OSIx-backed agent
filesystems. The operator installs and tracks agent filesystem objects, while
the CSI driver mounts OSIx state into pods and delegates snapshot, push,
compaction, and retention work to the existing OSIx runtime.

## Goals

- Install OSIx cluster components with standard Kubernetes manifests.
- Expose OSIx state as a CSI volume for agent pods.
- Track desired state, mount status, snapshot status, and retention status in
  Kubernetes objects.
- Reuse OSIx snapshot, registry, watch, compaction, signing, encryption, and
  mount semantics instead of inventing a second state format.
- Support Linux node runtimes first: kernel overlay, FUSE, lazy FUSE, and
  materialized fallback according to node prerequisites.

## Non-Goals

- Windows CSI support.
- Distributed filesystem semantics across concurrent writers.
- Strong registry-side compare-and-swap beyond existing expected-parent checks.
- Capturing live process memory or container checkpoints.

## Custom Resources

### AgentOCIFileSystem

Declares one logical OSIx-backed filesystem.

Important spec fields:

- `stateRef`: OCI registry repository for pushed state.
- `baseImage`: base image used for initial OSIx workspace setup.
- `branch`: mutable branch tag, default `main`.
- `sourceRef`: optional digest or tag to mount initially.
- `mountMode`: `auto`, `overlay`, `fuse`, or `materialized`.
- `registrySecretRef`: Kubernetes secret containing registry credentials.
- `encryption`: recipient or keywrap policy passed to OSIx.
- `signing`: snapshot signing and verification policy.
- `snapshotPolicyRef`: optional `AgentOCISnapshotPolicy`.
- `runtimeClassRef`: optional `AgentOCIRuntimeClass`.

Status conditions:

- `Ready`
- `Mounted`
- `Snapshotting`
- `SnapshotFailed`
- `Checkpointed`
- `RegistryReady`

### AgentOCISnapshotPolicy

Declares the scheduling and retention policy for one or more filesystems.

Important spec fields:

- `every`: interval for background snapshots.
- `maxDirtyBytes`: dirty-byte threshold.
- `onTurnBoundary`: wait for OSIx turn-boundary marker.
- `push`: push snapshots after creation.
- `compactEvery`: run compaction every N watch snapshots.
- `squashEvery`: chain threshold for checkpoint creation.
- `checkpointTagPrefix`: prefix for checkpoint tags.
- `keepSnapshots`: explicit snapshots to retain.
- `preserveSigned`: keep signed snapshots during pruning.
- `pruneLocal`: remove unretained local refs/blobs.
- `pruneRemote`: delete unretained remote manifests when registry supports it.

### AgentOCISnapshot

Records a produced snapshot or checkpoint.

Important spec/status fields:

- source filesystem name and UID.
- snapshot digest.
- parent digest.
- branch tag.
- checkpoint digest when compaction ran.
- pushed/pruned state.
- verification result.

### AgentOCIRuntimeClass

Defines node runtime behavior.

Important spec fields:

- preferred mount mode.
- cache root.
- privileged overlay allowed.
- FUSE/lazy FUSE allowed.
- required node labels.
- runtime image.

## CSI Driver

The CSI node driver supports:

- `NodeGetInfo`
- `NodeGetCapabilities`
- `NodeStageVolume`
- `NodeUnstageVolume`
- `NodePublishVolume`
- `NodeUnpublishVolume`

`NodePublishVolume` performs this command plan:

1. Resolve the target `AgentOCIFileSystem` or StorageClass parameters.
2. Create a node-local OSIx workspace directory.
3. Run `osix init` if the workspace does not exist.
4. Pull the requested branch/digest when `sourceRef` is remote.
5. Run `osix mount SOURCE TARGET --mode MODE --cache CACHE`.
6. Persist mount metadata for later unpublish/recovery.

`NodeUnpublishVolume` performs:

1. Optional final `osix snapshot`/`osix watch --once` when policy requires it.
2. `osix unmount TARGET`.
3. Status update through the operator.

## Operator Reconciliation

The operator reconciles:

- CRD installation readiness.
- node-driver DaemonSet readiness.
- filesystem object validation.
- snapshot policy binding.
- runtime class defaults.
- status conditions from node-side records.
- snapshot object creation from watch/checkpoint results.

The first implementation may use deterministic command plans and file-backed
status records for integration tests. Cluster deployments use the same command
plans through the CSI node driver and controller loops.

## Security

- Registry credentials come from Kubernetes secrets and are projected into node
  driver processes, never into snapshot metadata.
- Encryption and signing settings are copied into OSIx command flags only.
- Secrets must not be written to CRD status.
- The CSI node driver needs mount privileges only when overlay/FUSE modes need
  them; materialized mode may run without privileged mounts.
- Remote pruning is opt-in because registries vary in delete support and policy.

## Install Layout

The repository provides:

- `deploy/kubernetes/crds/*.yaml`
- `deploy/kubernetes/rbac.yaml`
- `deploy/kubernetes/operator.yaml`
- `deploy/kubernetes/csi-node.yaml`
- `deploy/kubernetes/storageclass.yaml`
- `deploy/kubernetes/examples/*.yaml`

## Integration Test

The deterministic integration test must exercise the same OSIx command plans as
the CSI node driver:

1. Start a local OCI registry.
2. Create an OSIx-backed volume plan.
3. Publish/mount the volume.
4. Write files through the mount target.
5. Run snapshot/retention policy.
6. Verify snapshot and checkpoint were pushed.
7. Verify old refs/manifests were pruned when enabled.
8. Publish a second volume from the branch and verify restored content.
9. Unpublish the volumes.

The full cluster path should later run against Kind with the same manifests and
a local registry.
