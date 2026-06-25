#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
image="${OSIX_K8S_AUTOSNAP_DOCKER_IMAGE:-golang:1.24-bookworm}"
network="osix-k8s-autosnap-net-$$"
registry_container="osix-k8s-autosnap-registry-$$"

cleanup() {
	docker rm -f "${registry_container}" >/dev/null 2>&1 || true
	docker network rm "${network}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

if ! command -v docker >/dev/null 2>&1; then
	echo "docker is required for the Kubernetes autosnapshot integration test" >&2
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

socat TCP-LISTEN:5000,bind=127.0.0.1,fork,reuseaddr TCP:registry:5000 >/tmp/osix-k8s-autosnap-socat.log 2>&1 &
forward_pid=$!
cleanup_inner() {
	kill "${forward_pid}" "${worker_pid:-}" >/dev/null 2>&1 || true
}
trap cleanup_inner EXIT

for _ in $(seq 1 30); do
	if curl -fsS http://127.0.0.1:5000/v2/ >/dev/null 2>&1; then
		break
	fi
	sleep 1
done
curl -fsS http://127.0.0.1:5000/v2/ >/dev/null

go build -buildvcs=false -o /tmp/osix ./cmd/osix
go build -buildvcs=false -o /tmp/osix-csi-node ./cmd/osix-csi-node

tmp="$(mktemp -d /tmp/osix-k8s-autosnap.XXXXXX)"
remote="127.0.0.1:5000/acme/autosnap-agent"
workspace_root="${tmp}/workspaces"
target="${tmp}/writer-volume"
restore_target="${tmp}/reader-volume"
report="${workspace_root}/csi/reports/pvc-autosnap-last.json"

/tmp/osix-csi-node serve \
	--addr 127.0.0.1:18081 \
	--workspace-root "${workspace_root}" \
	--enable-workers >/tmp/osix-k8s-autosnap-worker.log 2>&1 &
worker_pid=$!
for _ in $(seq 1 30); do
	if curl -fsS http://127.0.0.1:18081/readyz >/dev/null 2>&1; then
		break
	fi
	sleep 1
done
curl -fsS http://127.0.0.1:18081/healthz | jq -e ".status == \"ok\"" >/dev/null

/tmp/osix-csi-node publish \
	--workspace-root "${workspace_root}" \
	--target "${target}" \
	--volume-id pvc-autosnap \
	--name autosnap-agent \
	--state "${remote}" \
	--base example/base:latest \
	--mode materialized \
	--auto-snapshot \
	--every 200ms \
	--max-dirty 1 \
	--push=true \
	--compact-every 1 \
	--squash-every 2 \
	--checkpoint-tag-prefix checkpoint | tee "${tmp}/publish-writer.json"

mkdir -p "${target}/agent/workspace"
printf "v1\n" > "${target}/agent/workspace/file.txt"
printf "automatic snapshot path\n" > "${target}/agent/workspace/notes.txt"

wait_report_digest() {
	local previous="${1:-}"
	local deadline=$((SECONDS + 30))
	while [ "${SECONDS}" -lt "${deadline}" ]; do
		if [ -f "${report}" ]; then
			local digest
			digest="$(jq -r ".snapshotDigest // empty" "${report}")"
			if [ -n "${digest}" ] && [ "${digest}" != "${previous}" ]; then
				printf "%s" "${digest}"
				return 0
			fi
		fi
		sleep 0.2
	done
	echo "timed out waiting for automatic snapshot report" >&2
	cat /tmp/osix-k8s-autosnap-worker.log >&2 || true
	return 1
}

first_digest="$(wait_report_digest)"
sleep 0.5
stable_digest="$(jq -r ".snapshotDigest" "${report}")"
if [ "${stable_digest}" != "${first_digest}" ]; then
	echo "clean autosnapshot tick produced duplicate digest: first=${first_digest} stable=${stable_digest}" >&2
	exit 1
fi

printf "v2\n" > "${target}/agent/workspace/file.txt"
printf "restored by second agent\n" > "${target}/agent/workspace/second.txt"
second_digest="$(wait_report_digest "${first_digest}")"
checkpoint_digest="$(jq -r ".checkpointDigest // empty" "${report}")"
test -n "${second_digest}"
test -n "${checkpoint_digest}"

manifest_status() {
	local digest="$1"
	curl -sS -o /tmp/osix-k8s-autosnap-manifest.out -w "%{http_code}" \
		-H "Accept: application/vnd.oci.image.manifest.v1+json" \
		"http://127.0.0.1:5000/v2/acme/autosnap-agent/manifests/${digest}"
}

test "$(manifest_status "${second_digest}")" = "200"
test "$(manifest_status "${checkpoint_digest}")" = "200"

/tmp/osix-csi-node publish \
	--workspace-root "${workspace_root}" \
	--target "${restore_target}" \
	--volume-id pvc-autosnap-restore \
	--name autosnap-agent \
	--state "${remote}" \
	--base example/base:latest \
	--source "${remote}:main" \
	--mode materialized | tee "${tmp}/publish-reader.json"

jq -e ".sourceRef | startswith(\"sha256:\")" "${tmp}/publish-reader.json" >/dev/null
grep -qx "v2" "${restore_target}/agent/workspace/file.txt"
grep -qx "automatic snapshot path" "${restore_target}/agent/workspace/notes.txt"
grep -qx "restored by second agent" "${restore_target}/agent/workspace/second.txt"

/tmp/osix-csi-node unpublish \
	--workspace-root "${workspace_root}" \
	--target "${target}" \
	--volume-id pvc-autosnap \
	--name autosnap-agent \
	--state "${remote}" \
	--base example/base:latest
/tmp/osix-csi-node unpublish \
	--workspace-root "${workspace_root}" \
	--target "${restore_target}" \
	--volume-id pvc-autosnap-restore \
	--name autosnap-agent \
	--state "${remote}" \
	--base example/base:latest

echo "OSIX_K8S_AUTOSNAP_DOCKER_TEST_PASSED"
'
