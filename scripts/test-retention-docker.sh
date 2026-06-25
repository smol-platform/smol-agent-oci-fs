#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
image="${OSIX_RETENTION_DOCKER_IMAGE:-golang:1.24-bookworm}"
network="osix-retention-net-$$"
registry_container="osix-retention-registry-$$"

cleanup() {
	docker rm -f "${registry_container}" >/dev/null 2>&1 || true
	docker network rm "${network}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

if ! command -v docker >/dev/null 2>&1; then
	echo "docker is required for the retention integration test" >&2
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

socat TCP-LISTEN:5000,bind=127.0.0.1,fork,reuseaddr TCP:registry:5000 >/tmp/osix-retention-socat.log 2>&1 &
forward_pid=$!
cleanup_inner() {
	kill "${forward_pid}" >/dev/null 2>&1 || true
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
tmp="$(mktemp -d /tmp/osix-retention.XXXXXX)"
remote="127.0.0.1:5000/acme/retention-agent"
echo "tmp=${tmp}"
echo "remote=${remote}"

mkdir -p "${tmp}/source" "${tmp}/dest"
cd "${tmp}/source"
/tmp/osix init example/base:latest --name retention-agent --state "${remote}" --mount ./agentfs
mkdir -p agentfs/agent/workspace
printf "v1\n" > agentfs/agent/workspace/file.txt
/tmp/osix snapshot agentfs --tag snap-000001 --also-tag main | tee "${tmp}/initial.out"
initial_digest="$(awk "/^sha256:/ {print \$1; exit}" "${tmp}/initial.out")"
test -n "${initial_digest}"
/tmp/osix push main --tag main

printf "v2\n" > agentfs/agent/workspace/file.txt
printf "retained through checkpoint\n" > agentfs/agent/workspace/retained.txt
/tmp/osix watch agentfs \
	--once \
	--max-dirty 1 \
	--push \
	--compact-every 1 \
	--squash-every 2 \
	--checkpoint-tag-prefix checkpoint \
	--prune-local \
	--prune-remote | tee "${tmp}/watch-retention.out"

watch_digest="$(awk "/^snapshot / {print \$2; exit}" "${tmp}/watch-retention.out")"
checkpoint_digest="$(awk "/^checkpoint / {print \$NF; exit}" "${tmp}/watch-retention.out")"
test -n "${watch_digest}"
test -n "${checkpoint_digest}"
grep -q "^remote-deleted ${initial_digest}$" "${tmp}/watch-retention.out"
grep -q "^remote-deleted ${watch_digest}$" "${tmp}/watch-retention.out"
grep -q "^pruned-ref snap-000001$" "${tmp}/watch-retention.out"

initial_hex="${initial_digest#sha256:}"
watch_hex="${watch_digest#sha256:}"
test ! -f ".osix/blobs/sha256/${initial_hex}"
test ! -f ".osix/blobs/sha256/${watch_hex}"

manifest_status() {
	local digest="$1"
	curl -sS -o /tmp/osix-manifest-check.out -w "%{http_code}" \
		-H "Accept: application/vnd.oci.image.manifest.v1+json" \
		"http://127.0.0.1:5000/v2/acme/retention-agent/manifests/${digest}"
}

test "$(manifest_status "${initial_digest}")" = "404"
test "$(manifest_status "${watch_digest}")" = "404"
test "$(manifest_status "${checkpoint_digest}")" = "200"

cd "${tmp}/dest"
/tmp/osix init example/base:latest --name retention-agent --state "${remote}" --mount ./agentfs
/tmp/osix pull "${remote}:main" --tag pulled-main
/tmp/osix restore pulled-main ./restored
grep -qx "v2" restored/agent/workspace/file.txt
grep -qx "retained through checkpoint" restored/agent/workspace/retained.txt

echo "OSIX_RETENTION_DOCKER_TEST_PASSED"
'
