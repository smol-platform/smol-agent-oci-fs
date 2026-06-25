# OSIx Milestones

## M0: Project Skeleton And Decisions

Outcome: repository structure and core technical decisions are in place.

Deliverables:

- CLI package skeleton.
- Core package boundaries for registry, manifests, diffing, crypto, mounts, and policy.
- `osixVersion` config schema draft.
- Decision record for v0 publication mode: `hybrid` default with `image` fallback.
- Local fixture registry or registry test harness selected.

Acceptance criteria:

- `osix --help` runs.
- Schema examples validate against tests.
- A design document explains what is intentionally deferred from v0.

## M1: OCI Manifest And Local Snapshot Prototype

Outcome: create and read OSIx snapshot manifests without mounting.

Deliverables:

- OSIx config JSON encoder and decoder.
- OCI manifest builder with custom media types and annotations.
- Local content-addressed blob store.
- zstd tar diff layer writer for a directory tree.
- Restore-by-applying-layer into an empty directory.

Acceptance criteria:

- A fixture directory can be snapshotted into local blobs.
- The generated manifest references the config and layer blobs by digest.
- Restoring the manifest recreates the fixture tree.
- Unit tests cover add, modify, delete, symlink, mode, uid, and gid metadata where the platform allows.

## M2: Registry Push And Pull

Outcome: snapshots can be pushed to and pulled from an OCI registry.

Deliverables:

- Registry client integration.
- Push blobs, config, and manifest.
- Pull manifest, config, and layers.
- Tag resolution and digest pinning.
- Basic and Bearer-token registry authentication.
- `mode=image` compatibility path.

Acceptance criteria:

- `osix snapshot DIR --push --tag snap-000001` publishes to a registry.
- `osix restore REF DIR` restores from the registry.
- The client records the resolved digest for tag-based restores.
- Integration tests pass against at least one local OCI registry.

## M3: Mount And Diff UX

Outcome: users can mount, modify, diff, and snapshot agent filesystems.

Deliverables:

- `.osix/` local layout.
- `osix init`.
- `osix mount` using overlayfs or fuse-overlayfs.
- `osix diff`.
- `osix snapshot` from a mounted upperdir.
- Path exclusion policy.

Acceptance criteria:

- `osix init IMAGE --mount ./agentfs` creates a writable mount.
- File changes under `./agentfs` appear in `osix diff`.
- `osix snapshot ./agentfs` emits only changed files and whiteouts.
- Default excludes skip `/agent/tmp`, `/agent/cache`, and common cache paths.

## M4: Encryption

Outcome: pushed snapshot layers are encrypted and restorable by authorized recipients.

Deliverables:

- Per-layer random DEK generation.
- age recipient support.
- AWS KMS recipient support.
- Mixed-recipient OSIx envelope support for age, KMS, GPG, and endpoint recipients.
- Encrypted layer descriptor annotations.
- Decrypt-on-restore flow.
- Secret metadata review to avoid leaking path lists in public annotations.

Acceptance criteria:

- Registry blobs do not contain plaintext layer tar content.
- Restore succeeds with the correct recipient key.
- Restore fails with the wrong key.
- Public manifest annotations contain only approved metadata.

## M5: Signing And Provenance

Outcome: snapshots can be verified by digest and provenance can be attached.

Deliverables:

- Cosign-compatible manifest signing flow.
- Keyless signing option if available in the deployment environment.
- Provenance attestation payload.
- Referrer attachment for signature and provenance artifacts.
- Verification command or restore-time verification policy.

Acceptance criteria:

- `osix snapshot --sign` attaches a signature to the snapshot manifest.
- `osix verify REF` validates the manifest digest signature.
- Restore can require a trusted signer.
- Provenance records base digest, parent digest, tool version, creator, and timestamp.

## M6: Branching, Forking, And Chain Resolution

Outcome: snapshots form a Git-like DAG with branch pointers.

Deliverables:

- Parent pointer validation.
- Branch tag management.
- `osix fork SOURCE TARGET`.
- Snapshot sequence handling.
- Conflict detection for expected-parent updates.

Acceptance criteria:

- Forking from a snapshot creates a new branch pointer.
- Restoring by digest is stable even after tags move.
- Updating a branch with an unexpected parent reports a conflict.
- DAG traversal works across at least one fork.

## M7: Watcher And Turn Boundary Snapshots

Outcome: OSIx can take periodic agent-aware snapshots.

Deliverables:

- `osix watch`.
- Dirty byte threshold tracking.
- Periodic snapshot scheduling.
- Turn boundary integration hook.
- Background push lifecycle management.

Acceptance criteria:

- `osix watch --every 10m --max-dirty 512MiB` creates snapshots according to policy.
- `--on-turn-boundary` waits for the runtime hook before snapshotting.
- Failed pushes are retried or surfaced clearly.
- Watch state survives process restarts where practical.

## M8: Agent State Policies And Side-Effect Ledger

Outcome: snapshots are agent-aware, not just filesystem archives.

Deliverables:

- Standard `/agent` state root policy.
- Side-effect ledger schema and writer helper.
- Restore-time replay mode marker.
- Secret scan integration.
- Redaction hooks for logs.

Acceptance criteria:

- `/agent/secrets` is blocked from snapshots.
- `/agent/side-effects/ledger.jsonl` validates as JSONL.
- Restore marks external tools as mock, read-only, or approval-required by default.
- `--secret-scan block` prevents publishing likely secrets.

## M9: Compaction And Retention

Outcome: long-running agents can keep chains manageable.

Deliverables:

- Checkpoint snapshot creation.
- Retention policy parser.
- `osix compact`.
- Signed snapshot preservation.
- Audit-safe deletion planning.

Acceptance criteria:

- A chain of deltas can be compacted into a checkpoint.
- Restoring before and after compaction produces the same filesystem state.
- Signed or retained snapshots are not deleted.
- The command can run in dry-run mode and explain planned changes.

## M10: Registry Compatibility Matrix And v0 Release

Outcome: v0 is ready for real use against common registries.

Deliverables:

- Compatibility tests for selected registries.
- Fallback behavior for missing referrers support.
- End-to-end documentation.
- Threat model and limitations document.
- Release artifacts.

Acceptance criteria:

- End-to-end flow works: `init`, `mount`, `run`, `snapshot`, `restore`, `fork`, `diff`, `compact`.
- At least one registry works in `hybrid` mode.
- At least one registry works in `image` fallback mode.
- Known limitations are documented, including tag atomicity and encrypted lazy reads.

## M11: Kubernetes Operator And CSI Runtime

Outcome: OSIx can be installed on a Kubernetes cluster and used as a persistent,
snapshotting, registry-backed agent filesystem through a CSI volume.

Deliverables:

- Kubernetes CRDs for agent filesystems, snapshot policies, snapshots, and
  runtime classes.
- Operator deployment, RBAC, install manifests, and status/event conventions.
- CSI node driver that prepares OSIx workspaces and mounts agent state into pods.
- Snapshot/retention controller that invokes OSIx watch, push, compact, and
  prune behavior according to policy.
- Registry credential, encryption, signing, mount-mode, and retention plumbing.
- Observability through status conditions, logs, health checks, and metrics.
- Kind/local-registry integration test for install, mount, snapshot, checkpoint,
  restore, and teardown flows.

Acceptance criteria:

- Applying the install manifests deploys the operator and CSI node driver.
- A pod can mount an OSIx-backed PVC through the CSI driver.
- File changes written by a pod are snapshotted and pushed to an OCI registry.
- A second pod can restore or mount from the pushed branch or digest.
- Retention creates checkpoint snapshots and prunes according to policy.
- Failures surface through CRD status conditions and Kubernetes events.
- Integration tests pass against a local Kubernetes cluster and local OCI
  registry, or a deterministic fake cluster harness that exercises the same
  operator/CSI command plans.
