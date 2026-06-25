#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
remote_repo="${OSIX_HOSTED_REGISTRY_REPO:-}"
provider="${OSIX_HOSTED_REGISTRY_PROVIDER:-auto}"
evidence_dir="${OSIX_HOSTED_REGISTRY_EVIDENCE_DIR:-}"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/osix-hosted-registry-test.XXXXXX")"
tag_suffix="$(date +%Y%m%d%H%M%S)-$$"
remote_tag="compat-${tag_suffix}"

cleanup() {
  rm -rf "${tmp}"
}
trap cleanup EXIT

if [[ -z "${remote_repo}" ]]; then
  cat >&2 <<'EOF'
OSIX_HOSTED_REGISTRY_REPO is required.

Examples:
  ghcr.io/OWNER/osix-compat
  123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat
  us-docker.pkg.dev/PROJECT/REPOSITORY/osix-compat
  REGISTRY.azurecr.io/osix-compat
EOF
  exit 2
fi

registry_host="${remote_repo%%/*}"
repo_path="${remote_repo#*/}"
if [[ "${registry_host}" == "${remote_repo}" || -z "${repo_path}" ]]; then
  echo "OSIX_HOSTED_REGISTRY_REPO must be REGISTRY/REPOSITORY, got ${remote_repo}" >&2
  exit 2
fi

detect_provider() {
  case "${registry_host}" in
    ghcr.io)
      echo ghcr
      ;;
    *.dkr.ecr.*.amazonaws.com|*.dkr.ecr.*.amazonaws.com.cn)
      echo ecr
      ;;
    *.pkg.dev)
      echo gar
      ;;
    *.azurecr.io)
      echo acr
      ;;
    *)
      echo generic
      ;;
  esac
}

if [[ "${provider}" == "auto" ]]; then
  provider="$(detect_provider)"
fi

case "${provider}" in
  ghcr)
    if [[ "${registry_host}" != "ghcr.io" ]]; then
      echo "GHCR profile requires ghcr.io host, got ${registry_host}" >&2
      exit 2
    fi
    ;;
  ecr)
    if [[ ! "${registry_host}" =~ ^[0-9]{12}\.dkr\.ecr\.[a-z0-9-]+\.amazonaws\.com(\.cn)?$ ]]; then
      echo "ECR profile requires ACCOUNT.dkr.ecr.REGION.amazonaws.com[/cn], got ${registry_host}" >&2
      exit 2
    fi
    ;;
  gar)
    if [[ ! "${registry_host}" =~ \.pkg\.dev$ || "${repo_path}" != */*/* ]]; then
      echo "GAR profile requires REGION-docker.pkg.dev/PROJECT/REPOSITORY/IMAGE, got ${remote_repo}" >&2
      exit 2
    fi
    ;;
  acr)
    if [[ ! "${registry_host}" =~ \.azurecr\.io$ ]]; then
      echo "ACR profile requires REGISTRY.azurecr.io, got ${registry_host}" >&2
      exit 2
    fi
    ;;
  generic)
    ;;
  *)
    echo "OSIX_HOSTED_REGISTRY_PROVIDER must be auto, ghcr, ecr, gar, acr, or generic" >&2
    exit 2
    ;;
esac

if [[ -z "${OSIX_REGISTRY_TOKEN:-}" && ( -z "${OSIX_REGISTRY_USERNAME:-}" || -z "${OSIX_REGISTRY_PASSWORD:-}" ) ]]; then
  docker_config="${DOCKER_CONFIG:-${HOME}/.docker}/config.json"
  if [[ ! -f "${docker_config}" ]]; then
    echo "registry credentials are required via OSIX_REGISTRY_TOKEN, OSIX_REGISTRY_USERNAME/OSIX_REGISTRY_PASSWORD, or Docker config.json" >&2
    exit 2
  fi
fi

echo "Running hosted registry compatibility profile ${provider} for ${remote_repo}"

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

if [[ -n "${evidence_dir}" ]]; then
  mkdir -p "${evidence_dir}"
  evidence_file="${evidence_dir}/${provider}-$(date -u +%Y%m%dT%H%M%SZ).json"
  python3 - "$evidence_file" <<PY
import json
import os
import sys
from datetime import datetime, timezone

path = sys.argv[1]
evidence = {
    "provider": ${provider@Q},
    "repository": ${remote_repo@Q},
    "registryHost": ${registry_host@Q},
    "remoteTag": ${remote_tag@Q},
    "testedAt": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
    "profile": ${provider@Q},
    "auth": {
        "tokenEnv": bool(os.environ.get("OSIX_REGISTRY_TOKEN")),
        "basicEnv": bool(os.environ.get("OSIX_REGISTRY_USERNAME") and os.environ.get("OSIX_REGISTRY_PASSWORD")),
        "dockerConfig": bool(os.path.exists(os.path.join(os.environ.get("DOCKER_CONFIG", os.path.join(os.environ["HOME"], ".docker")), "config.json"))),
    },
    "operations": [
        "init",
        "snapshot",
        "keyless-sign",
        "verify-local",
        "push",
        "pull",
        "verify-pulled",
        "restore",
        "content-check",
    ],
    "result": "passed",
}
with open(path, "w", encoding="utf-8") as f:
    json.dump(evidence, f, indent=2, sort_keys=True)
    f.write("\n")
print(f"Wrote hosted registry evidence {path}")
PY
fi

echo "Hosted registry signed push/pull integration passed for ${provider}:${remote_repo}:${remote_tag}"
