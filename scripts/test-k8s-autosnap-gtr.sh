#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
namespace="${OSIX_GTR_NAMESPACE:-osix-autosnap-$(date +%s)}"
operator_image="${OSIX_OPERATOR_IMAGE:-ghcr.io/smol-platform/smol-agent-oci-fs-operator:latest}"
csi_image="${OSIX_CSI_IMAGE:-ghcr.io/smol-platform/smol-agent-oci-fs-csi:latest}"
state_ref="${OSIX_GTR_STATE_REF:-}"
registry_secret="${OSIX_GTR_REGISTRY_SECRET:-}"
keep_namespace="${OSIX_GTR_KEEP_NAMESPACE:-false}"
skip_install="${OSIX_GTR_SKIP_INSTALL:-false}"

cleanup() {
	if [ "${keep_namespace}" != "true" ]; then
		kubectl delete namespace "${namespace}" --ignore-not-found >/dev/null 2>&1 || true
	fi
}
trap cleanup EXIT

for bin in kubectl; do
	if ! command -v "${bin}" >/dev/null 2>&1; then
		echo "${bin} is required" >&2
		exit 2
	fi
done

if [ -z "${state_ref}" ]; then
	echo "OSIX_GTR_STATE_REF is required, for example ghcr.io/acme/osix-autosnap-live" >&2
	exit 2
fi

if command -v gtr >/dev/null 2>&1; then
	echo "gtr detected; using current kubectl context: $(kubectl config current-context)"
else
	echo "gtr is not installed; using current kubectl context: $(kubectl config current-context)"
fi

if [ "${skip_install}" != "true" ]; then
	kubectl apply -k "${repo_root}/deploy/kubernetes"
	kubectl -n osix-system set image deployment/osix-operator operator="${operator_image}"
	kubectl -n osix-system set image daemonset/osix-csi-node node="${csi_image}"
	kubectl -n osix-system rollout status deployment/osix-operator --timeout=180s
	kubectl -n osix-system rollout status daemonset/osix-csi-node --timeout=240s
	kubectl get csidriver osix.agent.smol.ai >/dev/null
fi

kubectl create namespace "${namespace}" --dry-run=client -o yaml | kubectl apply -f -

registry_secret_yaml=""
registry_secret_attr=""
if [ -n "${registry_secret}" ]; then
	registry_secret_yaml="  registrySecretRef:
    name: ${registry_secret}"
	registry_secret_attr="      registrySecretRef: ${registry_secret}"
fi

kubectl -n "${namespace}" apply -f - <<YAML
apiVersion: agent.smol.ai/v1alpha1
kind: AgentOCISnapshotPolicy
metadata:
  name: autosnap-live
spec:
  every: 5s
  maxDirtyBytes: "1"
  push: true
  compactEvery: 1
  squashEvery: 2
  checkpointTagPrefix: checkpoint
---
apiVersion: agent.smol.ai/v1alpha1
kind: AgentOCIFileSystem
metadata:
  name: autosnap-live
spec:
  baseImage: example/base:latest
  stateRef: ${state_ref}
  branch: main
  mountMode: materialized
${registry_secret_yaml}
  snapshotPolicyRef:
    name: autosnap-live
---
apiVersion: v1
kind: PersistentVolume
metadata:
  name: ${namespace}-writer
spec:
  capacity:
    storage: 1Gi
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Delete
  storageClassName: osix-agent-state
  csi:
    driver: osix.agent.smol.ai
    volumeHandle: ${namespace}-writer
    volumeAttributes:
      name: autosnap-live
      namespace: ${namespace}
      baseImage: example/base:latest
      stateRef: ${state_ref}
${registry_secret_attr}
      branch: main
      mountMode: materialized
      autoSnapshot: "true"
      snapshotEvery: 5s
      maxDirtyBytes: "1"
      compactEvery: "1"
      squashEvery: "2"
      checkpointTagPrefix: checkpoint
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: autosnap-live-writer
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: osix-agent-state
  volumeName: ${namespace}-writer
  resources:
    requests:
      storage: 1Gi
---
apiVersion: v1
kind: Pod
metadata:
  name: autosnap-live-writer
spec:
  restartPolicy: Never
  containers:
    - name: writer
      image: busybox:1.36
      command:
        - sh
        - -c
        - |
          set -eu
          mkdir -p /state/agent/workspace
          printf "gtr-v1\n" > /state/agent/workspace/file.txt
          printf "live cluster automatic snapshot\n" > /state/agent/workspace/notes.txt
          sleep 8
          printf "gtr-v2\n" > /state/agent/workspace/file.txt
          printf "restore marker\n" > /state/agent/workspace/second.txt
          sleep 120
      volumeMounts:
        - name: state
          mountPath: /state
  volumes:
    - name: state
      persistentVolumeClaim:
        claimName: autosnap-live-writer
YAML

kubectl -n "${namespace}" wait pod/autosnap-live-writer --for=condition=Ready --timeout=180s

deadline=$((SECONDS + 240))
snapshot_digest=""
checkpoint_digest=""
while [ "${SECONDS}" -lt "${deadline}" ]; do
	snapshot_digest="$(kubectl -n "${namespace}" get agentocifilesystem autosnap-live -o jsonpath='{.status.lastSnapshotDigest}' 2>/dev/null || true)"
	checkpoint_digest="$(kubectl -n "${namespace}" get agentocifilesystem autosnap-live -o jsonpath='{.status.lastCheckpointDigest}' 2>/dev/null || true)"
	if expr "${snapshot_digest}" : '^sha256:' >/dev/null && expr "${checkpoint_digest}" : '^sha256:' >/dev/null; then
		break
	fi
	sleep 5
done
if ! expr "${snapshot_digest}" : '^sha256:' >/dev/null || ! expr "${checkpoint_digest}" : '^sha256:' >/dev/null; then
	kubectl -n "${namespace}" get agentocifilesystem autosnap-live -o yaml >&2 || true
	kubectl -n "${namespace}" get agentocisnapshots -o yaml >&2 || true
	kubectl -n "${namespace}" get events --sort-by=.lastTimestamp >&2 || true
	kubectl -n osix-system logs daemonset/osix-csi-node -c node --tail=200 >&2 || true
	echo "timed out waiting for live automatic snapshot/checkpoint" >&2
	exit 1
fi

kubectl -n "${namespace}" apply -f - <<YAML
apiVersion: v1
kind: PersistentVolume
metadata:
  name: ${namespace}-reader
spec:
  capacity:
    storage: 1Gi
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Delete
  storageClassName: osix-agent-state
  csi:
    driver: osix.agent.smol.ai
    volumeHandle: ${namespace}-reader
    volumeAttributes:
      name: autosnap-live
      namespace: ${namespace}
      baseImage: example/base:latest
      stateRef: ${state_ref}
      sourceRef: ${state_ref}:main
${registry_secret_attr}
      branch: main
      mountMode: materialized
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: autosnap-live-reader
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: osix-agent-state
  volumeName: ${namespace}-reader
  resources:
    requests:
      storage: 1Gi
---
apiVersion: v1
kind: Pod
metadata:
  name: autosnap-live-reader
spec:
  restartPolicy: Never
  containers:
    - name: reader
      image: busybox:1.36
      command:
        - sh
        - -c
        - |
          set -eu
          grep -qx "gtr-v2" /state/agent/workspace/file.txt
          grep -qx "live cluster automatic snapshot" /state/agent/workspace/notes.txt
          grep -qx "restore marker" /state/agent/workspace/second.txt
      volumeMounts:
        - name: state
          mountPath: /state
  volumes:
    - name: state
      persistentVolumeClaim:
        claimName: autosnap-live-reader
YAML

kubectl -n "${namespace}" wait pod/autosnap-live-reader --for=jsonpath='{.status.phase}'=Succeeded --timeout=240s
kubectl -n "${namespace}" get agentocisnapshots -o wide
printf 'OSIX_K8S_AUTOSNAP_GTR_TEST_PASSED snapshot=%s checkpoint=%s stateRef=%s\n' "${snapshot_digest}" "${checkpoint_digest}" "${state_ref}"
