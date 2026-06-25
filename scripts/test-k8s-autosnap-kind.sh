#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cluster="${OSIX_KIND_CLUSTER:-osix-autosnap}"
csi_image="${OSIX_CSI_IMAGE:-ghcr.io/smol-platform/smol-agent-oci-fs-csi:latest}"
operator_image="${OSIX_OPERATOR_IMAGE:-ghcr.io/smol-platform/smol-agent-oci-fs-operator:latest}"
keep_cluster="${OSIX_KIND_KEEP_CLUSTER:-false}"
state_ref="${OSIX_KIND_STATE_REF:-http://registry.registry.svc.cluster.local:5000/acme/autosnap-kind}"

cleanup() {
	if [ "${keep_cluster}" != "true" ]; then
		kind delete cluster --name "${cluster}" >/dev/null 2>&1 || true
	fi
}
trap cleanup EXIT

for bin in docker kind kubectl; do
	if ! command -v "${bin}" >/dev/null 2>&1; then
		echo "${bin} is required" >&2
		exit 2
	fi
done

if ! docker info >/dev/null 2>&1; then
	echo "docker daemon is not available" >&2
	exit 2
fi

kind delete cluster --name "${cluster}" >/dev/null 2>&1 || true
kind create cluster --name "${cluster}" --wait 120s

docker build -f "${repo_root}/Dockerfile.csi" -t "${csi_image}" "${repo_root}"
docker build -f "${repo_root}/Dockerfile.operator" -t "${operator_image}" "${repo_root}"
kind load docker-image --name "${cluster}" "${csi_image}" "${operator_image}"

kubectl apply -k "${repo_root}/deploy/kubernetes"
kubectl -n osix-system rollout status deployment/osix-operator --timeout=120s
kubectl -n osix-system rollout status daemonset/osix-csi-node --timeout=180s
kubectl get csidriver osix.agent.smol.ai >/dev/null

kubectl create namespace registry
kubectl -n registry create deployment registry --image=registry:2
kubectl -n registry set env deployment/registry REGISTRY_STORAGE_DELETE_ENABLED=true
kubectl -n registry expose deployment registry --port=5000 --target-port=5000
kubectl -n registry rollout status deployment/registry --timeout=120s

kubectl create namespace agents
kubectl -n agents apply -f - <<YAML
apiVersion: agent.smol.ai/v1alpha1
kind: AgentOCISnapshotPolicy
metadata:
  name: autosnap-fast
spec:
  every: 2s
  maxDirtyBytes: "1"
  push: true
  compactEvery: 1
  squashEvery: 2
  checkpointTagPrefix: checkpoint
---
apiVersion: agent.smol.ai/v1alpha1
kind: AgentOCIFileSystem
metadata:
  name: autosnap-kind
spec:
  baseImage: example/base:latest
  stateRef: ${state_ref}
  branch: main
  mountMode: materialized
  snapshotPolicyRef:
    name: autosnap-fast
---
apiVersion: v1
kind: PersistentVolume
metadata:
  name: autosnap-kind-writer
spec:
  capacity:
    storage: 1Gi
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Delete
  storageClassName: osix-agent-state
  csi:
    driver: osix.agent.smol.ai
    volumeHandle: autosnap-kind-writer
    volumeAttributes:
      name: autosnap-kind
      namespace: agents
      baseImage: example/base:latest
      stateRef: ${state_ref}
      branch: main
      mountMode: materialized
      autoSnapshot: "true"
      snapshotEvery: 2s
      maxDirtyBytes: "1"
      compactEvery: "1"
      squashEvery: "2"
      checkpointTagPrefix: checkpoint
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: autosnap-kind-writer
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: osix-agent-state
  volumeName: autosnap-kind-writer
  resources:
    requests:
      storage: 1Gi
---
apiVersion: v1
kind: Pod
metadata:
  name: autosnap-writer
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
          printf "v1\n" > /state/agent/workspace/file.txt
          printf "kind automatic snapshot\n" > /state/agent/workspace/notes.txt
          sleep 4
          printf "v2\n" > /state/agent/workspace/file.txt
          printf "reader restore marker\n" > /state/agent/workspace/second.txt
          sleep 120
      volumeMounts:
        - name: state
          mountPath: /state
  volumes:
    - name: state
      persistentVolumeClaim:
        claimName: autosnap-kind-writer
YAML

kubectl -n agents wait pod/autosnap-writer --for=condition=Ready --timeout=120s

deadline=$((SECONDS + 180))
snapshot_name=""
snapshot_digest=""
while [ "${SECONDS}" -lt "${deadline}" ]; do
	snapshot_name="$(kubectl -n agents get agentocisnapshots -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
	snapshot_digest="$(kubectl -n agents get agentocifilesystem autosnap-kind -o jsonpath='{.status.lastSnapshotDigest}' 2>/dev/null || true)"
	if [ -n "${snapshot_name}" ] && expr "${snapshot_digest}" : '^sha256:' >/dev/null; then
		break
	fi
	sleep 2
done
if [ -z "${snapshot_name}" ] || ! expr "${snapshot_digest}" : '^sha256:' >/dev/null; then
	kubectl -n agents get agentocifilesystem autosnap-kind -o yaml >&2 || true
	kubectl -n agents get agentocisnapshots -o yaml >&2 || true
	kubectl -n agents get events --sort-by=.lastTimestamp >&2 || true
	kubectl -n agents describe pod/autosnap-writer >&2 || true
	kubectl -n osix-system get pods -o wide >&2 || true
	kubectl -n osix-system logs daemonset/osix-csi-node -c node --tail=200 >&2 || true
	kubectl -n osix-system logs daemonset/osix-csi-node -c node-driver-registrar --tail=100 >&2 || true
	echo "timed out waiting for AgentOCISnapshot status.lastSnapshotDigest" >&2
	exit 1
fi

kubectl -n agents apply -f - <<YAML
apiVersion: v1
kind: PersistentVolume
metadata:
  name: autosnap-kind-reader
spec:
  capacity:
    storage: 1Gi
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Delete
  storageClassName: osix-agent-state
  csi:
    driver: osix.agent.smol.ai
    volumeHandle: autosnap-kind-reader
    volumeAttributes:
      name: autosnap-kind
      namespace: agents
      baseImage: example/base:latest
      stateRef: ${state_ref}
      sourceRef: ${state_ref}:main
      branch: main
      mountMode: materialized
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: autosnap-kind-reader
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: osix-agent-state
  volumeName: autosnap-kind-reader
  resources:
    requests:
      storage: 1Gi
---
apiVersion: v1
kind: Pod
metadata:
  name: autosnap-reader
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
          grep -qx "v2" /state/agent/workspace/file.txt
          grep -qx "kind automatic snapshot" /state/agent/workspace/notes.txt
          grep -qx "reader restore marker" /state/agent/workspace/second.txt
      volumeMounts:
        - name: state
          mountPath: /state
  volumes:
    - name: state
      persistentVolumeClaim:
        claimName: autosnap-kind-reader
YAML

kubectl -n agents wait pod/autosnap-reader --for=jsonpath='{.status.phase}'=Succeeded --timeout=180s
echo "OSIX_K8S_AUTOSNAP_KIND_TEST_PASSED"
