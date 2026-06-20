#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
remote_repo="${OSIX_HOSTED_REGISTRY_REPO:-}"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/osix-hosted-registry-test.XXXXXX")"
tag_suffix="$(date +%Y%m%d%H%M%S)-$$"
remote_tag="compat-${tag_suffix}"

cleanup() {
  rm -rf "${tmp}"
}
trap cleanup EXIT

if [[ -z "${remote_repo}" ]]; then
  echo "OSIX_HOSTED_REGISTRY_REPO is required, for example ghcr.io/OWNER/osix-compat" >&2
  exit 2
fi

if [[ -z "${OSIX_REGISTRY_TOKEN:-}" && ( -z "${OSIX_REGISTRY_USERNAME:-}" || -z "${OSIX_REGISTRY_PASSWORD:-}" ) ]]; then
  docker_config="${DOCKER_CONFIG:-${HOME}/.docker}/config.json"
  if [[ ! -f "${docker_config}" ]]; then
    echo "registry credentials are required via OSIX_REGISTRY_TOKEN, OSIX_REGISTRY_USERNAME/OSIX_REGISTRY_PASSWORD, or Docker config.json" >&2
    exit 2
  fi
fi

go build -o "${tmp}/osix" "${repo_root}/cmd/osix"

source_dir="${tmp}/source"
dest_dir="${tmp}/dest"
mkdir -p "${source_dir}" "${dest_dir}"

(
  cd "${source_dir}"
  "${tmp}/osix" init example/base:latest \
    --name hosted-registry-agent \
    --state "${remote_repo}" \
    --mount ./agentfs
  mkdir -p agentfs/agent/workspace
  printf 'hosted registry round trip %s\n' "${remote_tag}" > agentfs/agent/workspace/notes.md
  "${tmp}/osix" snapshot agentfs --tag snap-000001 --also-tag main --sign keyless --attest hosted-registry
  "${tmp}/osix" verify main
  "${tmp}/osix" push "$(cat .osix/refs/main)" --tag "${remote_tag}"
)

(
  cd "${dest_dir}"
  "${tmp}/osix" init example/base:latest \
    --name hosted-registry-agent \
    --state "${remote_repo}" \
    --mount ./agentfs
  "${tmp}/osix" pull "${remote_repo}:${remote_tag}" --tag pulled-hosted
  "${tmp}/osix" verify pulled-hosted
  "${tmp}/osix" restore pulled-hosted ./restored
  grep -qx "hosted registry round trip ${remote_tag}" restored/agent/workspace/notes.md
)

echo "Hosted registry signed push/pull integration passed for ${remote_repo}:${remote_tag}"
