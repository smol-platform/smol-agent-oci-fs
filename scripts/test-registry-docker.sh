#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
port="${OSIX_TEST_REGISTRY_PORT:-5001}"
registry="127.0.0.1:${port}"
container="osix-registry-test-$$"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/osix-registry-test.XXXXXX")"

cleanup() {
  docker rm -f "${container}" >/dev/null 2>&1 || true
  rm -rf "${tmp}"
}
trap cleanup EXIT

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required for the registry integration test" >&2
  exit 2
fi

if ! docker info >/dev/null 2>&1; then
  echo "docker daemon is not available" >&2
  exit 2
fi

go build -o "${tmp}/osix" "${repo_root}/cmd/osix"

docker run -d --rm \
  --name "${container}" \
  -p "127.0.0.1:${port}:5000" \
  registry:2 >/dev/null

for _ in $(seq 1 30); do
  if curl -fsS "http://${registry}/v2/" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS "http://${registry}/v2/" >/dev/null

source_dir="${tmp}/source"
dest_dir="${tmp}/dest"
mkdir -p "${source_dir}" "${dest_dir}"

(
  cd "${source_dir}"
  "${tmp}/osix" init example/base:latest \
    --name registry-agent \
    --state "${registry}/acme/registry-agent" \
    --mount ./agentfs
  mkdir -p agentfs/agent/workspace
  printf 'registry round trip\n' > agentfs/agent/workspace/notes.md
  "${tmp}/osix" snapshot agentfs --tag snap-000001 --also-tag main --sign keyless --attest docker-registry
  "${tmp}/osix" verify main
  "${tmp}/osix" push main --tag release
)

(
  cd "${dest_dir}"
  "${tmp}/osix" init example/base:latest \
    --name registry-agent \
    --state "${registry}/acme/registry-agent" \
    --mount ./agentfs
  "${tmp}/osix" pull "${registry}/acme/registry-agent:release" --tag pulled-release
  "${tmp}/osix" verify pulled-release
  "${tmp}/osix" restore pulled-release ./restored
  grep -qx 'registry round trip' restored/agent/workspace/notes.md
)

echo "Docker registry signed push/pull integration passed"
