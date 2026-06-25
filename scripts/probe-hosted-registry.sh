#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
remote_repo="${OSIX_HOSTED_REGISTRY_REPO:-}"
provider="${OSIX_HOSTED_REGISTRY_PROVIDER:-auto}"
probe_dir="${OSIX_HOSTED_REGISTRY_PROBE_DIR:-}"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/osix-hosted-registry-probe.XXXXXX")"
tag_suffix="$(date -u +%Y%m%dT%H%M%SZ)-$$"
remote_tag="osix-probe-${tag_suffix}"

cleanup() {
	rm -rf "${tmp}"
}
trap cleanup EXIT

probe_file_for_provider() {
	if [[ -z "${probe_dir}" ]]; then
		return 0
	fi
	mkdir -p "${probe_dir}"
	printf '%s/%s-%s.json' "${probe_dir}" "${provider}" "$(date -u +%Y%m%dT%H%M%SZ)"
}

classify_probe_failure() {
	local log_file="$1"
	if [[ ! -f "${log_file}" ]]; then
		printf '\t'
		return 0
	fi
	if grep -q "start blob upload .*403 Forbidden" "${log_file}"; then
		printf 'registry-write-forbidden\t%s registry rejected blob upload with 403 Forbidden; refresh credentials or grant repository write/push permissions.' "${provider}"
		return 0
	fi
	if grep -qi "401 Unauthorized\\|unauthorized" "${log_file}"; then
		printf 'registry-unauthorized\t%s registry authentication failed; refresh Docker or OSIx registry credentials.' "${provider}"
		return 0
	fi
	if grep -qi "repository .*not found\\|name unknown\\|404 Not Found" "${log_file}"; then
		printf 'registry-repository-missing\t%s repository was not found; create the repository or fix OSIX_HOSTED_REGISTRY_REPO.' "${provider}"
		return 0
	fi
	printf '\t'
}

write_probe_evidence() {
	local path="$1"
	local result="$2"
	local probe_json_file="$3"
	local log_file="$4"
	local exit_code="$5"
	local failure_class="$6"
	local failure_hint="$7"
	if [[ -z "${path}" ]]; then
		return 0
	fi
	python3 - "${path}" "${provider}" "${remote_repo}" "${registry_host}" "${remote_tag}" "${result}" "${probe_json_file}" "${log_file}" "${exit_code}" "${failure_class}" "${failure_hint}" <<'PY'
import json
import os
import sys
from datetime import datetime, timezone

path, provider, remote_repo, registry_host, remote_tag, result, probe_json_file, log_file, exit_code, failure_class, failure_hint = sys.argv[1:12]
probe = {}
if probe_json_file and os.path.exists(probe_json_file):
    with open(probe_json_file, "r", encoding="utf-8") as f:
        probe = json.load(f)
probe.update({
    "provider": provider,
    "profile": provider,
    "repository": probe.get("repository") or remote_repo,
    "registryHost": probe.get("registryHost") or registry_host,
    "tag": probe.get("tag") or remote_tag,
    "result": result,
    "testedAt": probe.get("testedAt") or datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
    "auth": {
        "tokenEnv": bool(os.environ.get("OSIX_REGISTRY_TOKEN")),
        "basicEnv": bool(os.environ.get("OSIX_REGISTRY_USERNAME") and os.environ.get("OSIX_REGISTRY_PASSWORD")),
        "dockerConfig": bool(os.path.exists(os.path.join(os.environ.get("DOCKER_CONFIG", os.path.join(os.environ["HOME"], ".docker")), "config.json"))),
    },
})
if log_file:
    probe["logFile"] = log_file
if exit_code:
    probe["exitCode"] = int(exit_code)
if failure_class:
    probe["failureClass"] = failure_class
if failure_hint:
    probe["failureHint"] = failure_hint
if "operations" not in probe:
    probe["operations"] = [
        "put-config-blob",
        "put-layer-blob",
        "put-manifest",
        "get-manifest",
        "get-layer-blob",
    ]
with open(path, "w", encoding="utf-8") as f:
    json.dump(probe, f, indent=2, sort_keys=True)
    f.write("\n")
print(f"Wrote hosted registry probe evidence {path}")
PY
}

if [[ -z "${remote_repo}" ]]; then
	echo "OSIX_HOSTED_REGISTRY_REPO is required" >&2
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

echo "Probing hosted registry write/read access for ${provider}:${remote_repo}"
osix_bin="${OSIX_HOSTED_REGISTRY_PROBE_OSIX:-}"
if [[ -z "${osix_bin}" ]]; then
	go build -o "${tmp}/osix" "${repo_root}/cmd/osix"
	osix_bin="${tmp}/osix"
fi
probe_output="${tmp}/probe.out"
set +e
"${osix_bin}" registry probe "${remote_repo}" --tag "${remote_tag}" --json >"${probe_output}" 2>&1
probe_status=$?
set -e
if [[ "${probe_status}" -ne 0 ]]; then
	cat "${probe_output}" >&2
	probe_file="$(probe_file_for_provider)"
	failure_log=""
	if [[ -n "${probe_file}" ]]; then
		failure_log="${probe_file%.json}.log"
		cp "${probe_output}" "${failure_log}"
	fi
	classify_log="${failure_log:-${probe_output}}"
	IFS=$'\t' read -r failure_class failure_hint <<<"$(classify_probe_failure "${classify_log}")"
	write_probe_evidence "${probe_file}" "failed" "" "${failure_log}" "${probe_status}" "${failure_class}" "${failure_hint}"
	exit "${probe_status}"
fi
probe_json="$(cat "${probe_output}")"
printf '%s\n' "${probe_json}"
probe_json_file="${tmp}/probe.json"
printf '%s\n' "${probe_json}" > "${probe_json_file}"

if [[ -n "${probe_dir}" ]]; then
	probe_file="$(probe_file_for_provider)"
	write_probe_evidence "${probe_file}" "passed" "${probe_json_file}" "" "0" "" ""
fi

echo "Hosted registry probe passed for ${provider}:${remote_repo}:${remote_tag}"
