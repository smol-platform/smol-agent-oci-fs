#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
image="${OSIX_K8S_OPERATOR_DOCKER_IMAGE:-golang:1.24-bookworm}"
network="osix-k8s-operator-net-$$"
registry_container="osix-k8s-operator-registry-$$"

cleanup() {
	docker rm -f "${registry_container}" >/dev/null 2>&1 || true
	docker network rm "${network}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

if ! command -v docker >/dev/null 2>&1; then
	echo "docker is required for the Kubernetes operator integration test" >&2
	exit 2
fi

if ! docker info >/dev/null 2>&1; then
	echo "docker daemon is not available" >&2
	exit 2
fi

docker network create "${network}" >/dev/null
docker run -d --rm \
	--name "${registry_container}" \
	--network "${network}" \
	--network-alias registry \
	-e REGISTRY_STORAGE_DELETE_ENABLED=true \
	registry:2 >/dev/null

docker run --rm \
	--network "${network}" \
	-v "${repo_root}:/work" \
	-w /work \
	"${image}" \
	bash -lc '
set -euo pipefail

export PATH="/usr/local/go/bin:${PATH}"
export DEBIAN_FRONTEND=noninteractive
apt-get update >/dev/null
apt-get install -y curl socat jq >/dev/null

socat TCP-LISTEN:5000,bind=127.0.0.1,fork,reuseaddr TCP:registry:5000 >/tmp/osix-k8s-registry-socat.log 2>&1 &
forward_pid=$!
/usr/bin/env bash -c "while true; do sleep 3600; done" &
keepalive_pid=$!
cleanup_inner() {
	kill "${forward_pid}" "${keepalive_pid}" >/dev/null 2>&1 || true
}
trap cleanup_inner EXIT

for _ in $(seq 1 30); do
	if curl -fsS http://127.0.0.1:5000/v2/ >/dev/null 2>&1; then
		break
	fi
	sleep 1
done
curl -fsS http://127.0.0.1:5000/v2/ >/dev/null

go build -o /tmp/osix ./cmd/osix
go build -o /tmp/osix-k8s-operator ./cmd/osix-k8s-operator
go build -o /tmp/osix-csi-node ./cmd/osix-csi-node

tmp="$(mktemp -d /tmp/osix-k8s-operator.XXXXXX)"
remote="127.0.0.1:5000/acme/k8s-agent"
workspace_root="${tmp}/workspaces"
target="${tmp}/pod-volume"
restore_target="${tmp}/restored-volume"

/tmp/osix-k8s-operator render-install > "${tmp}/install.yaml"
grep -q "kind: CustomResourceDefinition" "${tmp}/install.yaml"
grep -q "kind: DaemonSet" "${tmp}/install.yaml"
grep -q "name: osix-agent-state" "${tmp}/install.yaml"

/tmp/osix-k8s-operator plan \
	--name k8s-agent \
	--state "${remote}" \
	--base example/base:latest \
	--target "${target}" \
	--workspace-root "${workspace_root}" \
	--volume-id pvc-123 \
	--mode materialized > "${tmp}/empty-plan.json"
jq -e ".steps[] | select(.name == \"prepare-empty\")" "${tmp}/empty-plan.json" >/dev/null

/tmp/osix-k8s-operator plan \
	--name k8s-agent \
	--state "${remote}" \
	--base example/base:latest \
	--source "${remote}:main" \
	--target "${restore_target}" \
	--workspace-root "${workspace_root}" \
	--volume-id pvc-restore \
	--mode materialized > "${tmp}/restore-plan.json"
jq -e ".steps[] | select(.name == \"pull\")" "${tmp}/restore-plan.json" >/dev/null
jq -e ".steps[] | select(.name == \"mount\")" "${tmp}/restore-plan.json" >/dev/null

/tmp/osix-k8s-operator serve --addr 127.0.0.1:18080 >/tmp/osix-k8s-operator.log 2>&1 &
operator_pid=$!
for _ in $(seq 1 30); do
	if curl -fsS http://127.0.0.1:18080/readyz >/dev/null 2>&1; then
		break
	fi
	sleep 1
done
curl -fsS http://127.0.0.1:18080/healthz | jq -e ".status == \"ok\"" >/dev/null
curl -fsS http://127.0.0.1:18080/readyz | jq -e ".status == \"ready\"" >/dev/null
kill "${operator_pid}" >/dev/null 2>&1 || true

/tmp/osix-csi-node serve --addr 127.0.0.1:18081 >/tmp/osix-csi-node.log 2>&1 &
csi_pid=$!
for _ in $(seq 1 30); do
	if curl -fsS http://127.0.0.1:18081/readyz >/dev/null 2>&1; then
		break
	fi
	sleep 1
done
curl -fsS http://127.0.0.1:18081/healthz | jq -e ".status == \"ok\"" >/dev/null
curl -fsS http://127.0.0.1:18081/readyz | jq -e ".status == \"ready\"" >/dev/null
kill "${csi_pid}" >/dev/null 2>&1 || true

/tmp/osix-csi-node publish \
	--workspace-root "${workspace_root}" \
	--target "${target}" \
	--volume-id pvc-123 \
	--name k8s-agent \
	--state "${remote}" \
	--base example/base:latest \
	--mode materialized | tee "${tmp}/publish.json"
jq -e ".workspace | length > 0" "${tmp}/publish.json" >/dev/null

mkdir -p "${target}/agent/workspace"
printf "v1\n" > "${target}/agent/workspace/file.txt"
printf "persisted from CSI\n" > "${target}/agent/workspace/notes.txt"

/tmp/osix-csi-node snapshot \
	--workspace-root "${workspace_root}" \
	--target "${target}" \
	--volume-id pvc-123 \
	--name k8s-agent \
	--state "${remote}" \
	--base example/base:latest \
	--push=true \
	--max-dirty 1 | tee "${tmp}/snapshot-1.json"
first_digest="$(jq -r ".snapshotDigests[0]" "${tmp}/snapshot-1.json")"
test -n "${first_digest}"
test "${first_digest}" != "null"

printf "v2\n" > "${target}/agent/workspace/file.txt"
printf "retained through checkpoint\n" > "${target}/agent/workspace/checkpoint.txt"

/tmp/osix-csi-node snapshot \
	--workspace-root "${workspace_root}" \
	--target "${target}" \
	--volume-id pvc-123 \
	--name k8s-agent \
	--state "${remote}" \
	--base example/base:latest \
	--push=true \
	--max-dirty 1 \
	--compact-every 1 \
	--squash-every 2 \
	--checkpoint-tag-prefix checkpoint \
	--prune-local \
	--prune-remote | tee "${tmp}/snapshot-2.json"
second_digest="$(jq -r ".snapshotDigests[0]" "${tmp}/snapshot-2.json")"
checkpoint_digest="$(jq -r ".checkpointDigests[0]" "${tmp}/snapshot-2.json")"
test -n "${second_digest}"
test -n "${checkpoint_digest}"
test "${checkpoint_digest}" != "null"

manifest_status() {
	local digest="$1"
	curl -sS -o /tmp/osix-k8s-manifest-check.out -w "%{http_code}" \
		-H "Accept: application/vnd.oci.image.manifest.v1+json" \
		"http://127.0.0.1:5000/v2/acme/k8s-agent/manifests/${digest}"
}

test "$(manifest_status "${checkpoint_digest}")" = "200"

/tmp/osix-csi-node publish \
	--workspace-root "${workspace_root}" \
	--target "${restore_target}" \
	--volume-id pvc-restore \
	--name k8s-agent \
	--state "${remote}" \
	--base example/base:latest \
	--source "${remote}:main" \
	--mode materialized | tee "${tmp}/publish-restore.json"
jq -e ".sourceRef | startswith(\"sha256:\")" "${tmp}/publish-restore.json" >/dev/null
grep -qx "v2" "${restore_target}/agent/workspace/file.txt"
grep -qx "persisted from CSI" "${restore_target}/agent/workspace/notes.txt"
grep -qx "retained through checkpoint" "${restore_target}/agent/workspace/checkpoint.txt"

/tmp/osix-csi-node unpublish \
	--workspace-root "${workspace_root}" \
	--target "${target}" \
	--volume-id pvc-123 \
	--name k8s-agent \
	--state "${remote}" \
	--base example/base:latest
/tmp/osix-csi-node unpublish \
	--workspace-root "${workspace_root}" \
	--target "${restore_target}" \
	--volume-id pvc-restore \
	--name k8s-agent \
	--state "${remote}" \
	--base example/base:latest

echo "OSIX_K8S_OPERATOR_DOCKER_TEST_PASSED"
'
