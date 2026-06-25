#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
namespace="${OSIX_GTR_NAMESPACE:-osix-autosnap-$(date +%s)}"
operator_image="${OSIX_OPERATOR_IMAGE:-ghcr.io/smol-platform/smol-agent-oci-fs-operator:latest}"
csi_image="${OSIX_CSI_IMAGE:-ghcr.io/smol-platform/smol-agent-oci-fs-csi:latest}"
state_ref="${OSIX_GTR_STATE_REF:-}"
registry_secret="${OSIX_GTR_REGISTRY_SECRET:-}"
registry_server="${OSIX_GTR_REGISTRY_SERVER:-}"
registry_username="${OSIX_GTR_REGISTRY_USERNAME:-}"
registry_password="${OSIX_GTR_REGISTRY_PASSWORD:-}"
image_pull_secret="${OSIX_GTR_IMAGE_PULL_SECRET:-}"
image_pull_server="${OSIX_GTR_IMAGE_PULL_SERVER:-ghcr.io}"
image_pull_username="${OSIX_GTR_IMAGE_PULL_USERNAME:-${registry_username}}"
image_pull_password="${OSIX_GTR_IMAGE_PULL_PASSWORD:-${registry_password}}"
context="${OSIX_GTR_CONTEXT:-}"
kubelet_root="${OSIX_GTR_KUBELET_ROOT:-/var/lib/k0s/kubelet}"
keep_namespace="${OSIX_GTR_KEEP_NAMESPACE:-false}"
skip_install="${OSIX_GTR_SKIP_INSTALL:-false}"

kubectl_args=()
if [ -n "${context}" ]; then
	kubectl_args+=(--context "${context}")
fi

k() {
	kubectl "${kubectl_args[@]}" "$@"
}

jsonpath() {
	k -n "${namespace}" get agentocifilesystem autosnap-live -o "jsonpath={$1}" 2>/dev/null || true
}

dump_failure_context() {
	k -n "${namespace}" get agentocifilesystem autosnap-live -o yaml >&2 || true
	k -n "${namespace}" get agentocisnapshots -o yaml >&2 || true
	k -n "${namespace}" get pods -o wide >&2 || true
	k -n "${namespace}" get events --sort-by=.lastTimestamp >&2 || true
	k -n osix-system logs daemonset/osix-csi-node -c node --tail=200 >&2 || true
}

wait_for_snapshot_digest() {
	local previous="${1:-}"
	local timeout="${2:-240}"
	local deadline=$((SECONDS + timeout))
	local digest=""
	while [ "${SECONDS}" -lt "${deadline}" ]; do
		digest="$(jsonpath '.status.lastSnapshotDigest')"
		if expr "${digest}" : '^sha256:' >/dev/null; then
			if [ -z "${previous}" ] || [ "${digest}" != "${previous}" ]; then
				printf '%s\n' "${digest}"
				return 0
			fi
		fi
		sleep 5
	done
	return 1
}

wait_for_checkpoint_digest() {
	local timeout="${1:-180}"
	local deadline=$((SECONDS + timeout))
	local digest=""
	while [ "${SECONDS}" -lt "${deadline}" ]; do
		digest="$(jsonpath '.status.lastCheckpointDigest')"
		if expr "${digest}" : '^sha256:' >/dev/null; then
			printf '%s\n' "${digest}"
			return 0
		fi
		sleep 5
	done
	return 1
}

cleanup() {
	if [ "${keep_namespace}" != "true" ]; then
		k -n "${namespace}" delete pod autosnap-live-writer autosnap-live-reader \
			--ignore-not-found --force --grace-period=0 --wait=false >/dev/null 2>&1 || true
		k -n "${namespace}" delete pvc autosnap-live-writer autosnap-live-reader \
			--ignore-not-found --wait=false >/dev/null 2>&1 || true
		k delete pv "${namespace}-writer" "${namespace}-reader" \
			--ignore-not-found --wait=false >/dev/null 2>&1 || true
		k delete namespace "${namespace}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
		local deadline=$((SECONDS + 120))
		while k get namespace "${namespace}" >/dev/null 2>&1; do
			if [ "${SECONDS}" -ge "${deadline}" ]; then
				echo "timed out waiting for namespace cleanup: ${namespace}" >&2
				return 1
			fi
			sleep 2
		done
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
if [ -z "${registry_server}" ]; then
	registry_server="${state_ref#http://}"
	registry_server="${registry_server#https://}"
	registry_server="${registry_server%%/*}"
fi

if command -v gtr >/dev/null 2>&1; then
	echo "gtr detected; using kubectl context: ${context:-$(kubectl config current-context)}"
else
	echo "gtr is not installed; using kubectl context: ${context:-$(kubectl config current-context)}"
fi

create_docker_secret() {
	local namespace_arg="$1"
	local name="$2"
	local server="$3"
	local username="$4"
	local password="$5"
	if [ -z "${name}" ]; then
		return 0
	fi
	if [ -z "${username}" ] || [ -z "${password}" ]; then
		echo "secret ${namespace_arg}/${name} requires username and password environment values" >&2
		exit 2
	fi
	k -n "${namespace_arg}" create secret docker-registry "${name}" \
		--docker-server="${server}" \
		--docker-username="${username}" \
		--docker-password="${password}" \
		--dry-run=client -o yaml | k apply -f -
}

if [ "${skip_install}" != "true" ]; then
	k create namespace osix-system --dry-run=client -o yaml | k apply -f -
	create_docker_secret osix-system "${image_pull_secret}" "${image_pull_server}" "${image_pull_username}" "${image_pull_password}"
	existing_provisioner="$(k get storageclass osix-agent-state -o jsonpath='{.provisioner}' 2>/dev/null || true)"
	if [ -n "${existing_provisioner}" ] && [ "${existing_provisioner}" != "osix.agent.smol.ai" ]; then
		echo "replacing stale osix-agent-state StorageClass provisioner ${existing_provisioner}" >&2
		k delete storageclass osix-agent-state
	fi
	k apply -k "${repo_root}/deploy/kubernetes"
	k -n osix-system set image deployment/osix-operator operator="${operator_image}"
	k -n osix-system set image daemonset/osix-csi-node node="${csi_image}"
	if [ -n "${image_pull_secret}" ]; then
		k -n osix-system patch deployment osix-operator --type merge \
			-p "{\"spec\":{\"template\":{\"spec\":{\"imagePullSecrets\":[{\"name\":\"${image_pull_secret}\"}]}}}}"
		k -n osix-system patch daemonset osix-csi-node --type merge \
			-p "{\"spec\":{\"template\":{\"spec\":{\"imagePullSecrets\":[{\"name\":\"${image_pull_secret}\"}]}}}}"
	fi
	if [ -n "${kubelet_root}" ] && [ "${kubelet_root}" != "/var/lib/kubelet" ]; then
		duplicate_kubelet_mount_name="$(k -n osix-system get daemonset osix-csi-node -o jsonpath='{.spec.template.spec.containers[0].volumeMounts[2].name}' 2>/dev/null || true)"
		duplicate_kubelet_mount_path="$(k -n osix-system get daemonset osix-csi-node -o jsonpath='{.spec.template.spec.containers[0].volumeMounts[2].mountPath}' 2>/dev/null || true)"
		if [ "${duplicate_kubelet_mount_name}" = "kubelet" ] && [ "${duplicate_kubelet_mount_path}" = "${kubelet_root}" ]; then
			k -n osix-system patch daemonset osix-csi-node --type json -p '[
			  {"op":"remove","path":"/spec/template/spec/containers/0/volumeMounts/2"}
			]'
		fi
		k -n osix-system patch daemonset osix-csi-node --type json -p "[
		  {\"op\":\"replace\",\"path\":\"/spec/template/spec/volumes/0/hostPath/path\",\"value\":\"${kubelet_root}/plugins/osix.agent.smol.ai\"},
		  {\"op\":\"replace\",\"path\":\"/spec/template/spec/volumes/1/hostPath/path\",\"value\":\"${kubelet_root}/plugins_registry\"},
		  {\"op\":\"replace\",\"path\":\"/spec/template/spec/volumes/2/hostPath/path\",\"value\":\"${kubelet_root}\"},
		  {\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/volumeMounts/1/mountPath\",\"value\":\"${kubelet_root}\"},
		  {\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/1/args/1\",\"value\":\"--kubelet-registration-path=${kubelet_root}/plugins/osix.agent.smol.ai/csi.sock\"}
		]"
	fi
	k -n osix-system rollout status deployment/osix-operator --timeout=180s
	k -n osix-system rollout status daemonset/osix-csi-node --timeout=240s
	k get csidriver osix.agent.smol.ai >/dev/null
fi

k create namespace "${namespace}" --dry-run=client -o yaml | k apply -f -
create_docker_secret "${namespace}" "${registry_secret}" "${registry_server}" "${registry_username}" "${registry_password}"

registry_secret_yaml=""
registry_secret_attr=""
if [ -n "${registry_secret}" ]; then
	registry_secret_yaml="  registrySecretRef:
    name: ${registry_secret}"
	registry_secret_attr="      registrySecretRef: ${registry_secret}"
fi

k -n "${namespace}" apply -f - <<YAML
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
          sleep 600
      volumeMounts:
        - name: state
          mountPath: /state
  volumes:
    - name: state
      persistentVolumeClaim:
        claimName: autosnap-live-writer
YAML

k -n "${namespace}" wait pod/autosnap-live-writer --for=condition=Ready --timeout=180s

first_snapshot_digest="$(wait_for_snapshot_digest "" 240 || true)"
if ! expr "${first_snapshot_digest}" : '^sha256:' >/dev/null; then
	dump_failure_context
	echo "timed out waiting for first live automatic snapshot" >&2
	exit 1
fi

k -n "${namespace}" exec pod/autosnap-live-writer -- sh -c '
  set -eu
  printf "gtr-v2\n" > /state/agent/workspace/file.txt
  printf "restore marker\n" > /state/agent/workspace/second.txt
'

snapshot_digest="$(wait_for_snapshot_digest "${first_snapshot_digest}" 240 || true)"
if ! expr "${snapshot_digest}" : '^sha256:' >/dev/null; then
	dump_failure_context
	echo "timed out waiting for second live automatic snapshot after mutation" >&2
	exit 1
fi

checkpoint_digest="$(wait_for_checkpoint_digest 180 || true)"
if ! expr "${checkpoint_digest}" : '^sha256:' >/dev/null; then
	dump_failure_context
	echo "timed out waiting for live automatic checkpoint after second snapshot" >&2
	exit 1
fi

k -n "${namespace}" apply -f - <<YAML
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

k -n "${namespace}" wait pod/autosnap-live-reader --for=jsonpath='{.status.phase}'=Succeeded --timeout=240s
k -n "${namespace}" get agentocisnapshots -o wide
trap - EXIT
cleanup
printf 'OSIX_K8S_AUTOSNAP_GTR_TEST_PASSED snapshot=%s checkpoint=%s stateRef=%s\n' "${snapshot_digest}" "${checkpoint_digest}" "${state_ref}"
