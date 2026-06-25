#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
providers="${OSIX_HOSTED_REGISTRY_MATRIX:-ecr,gar,acr}"
out_dir="${OSIX_HOSTED_REGISTRY_OUTPUT_DIR:-}"
compatibility_file="${repo_root}/docs/compatibility-matrix.md"
update_docs=1
env_file=""

usage() {
	cat <<'EOF'
Usage: run-hosted-registry-verification.sh [options]

Runs the full hosted registry verification flow:
  1. required-provider preflight
  2. minimal registry write/read probe
  3. live hosted registry matrix
  4. evidence verification
  5. full run bundle verification
  6. compatibility matrix update

Options:
  --providers LIST             Default: ecr,gar,acr
  --out-dir DIR                Default: hosted-registry-run-UTC_TIMESTAMP
  --compatibility-file PATH    Default: docs/compatibility-matrix.md
  --env-file PATH              Source hosted registry repo exports before running
  --skip-update-docs           Do not update compatibility matrix after verify
  --help
EOF
}

while [[ "$#" -gt 0 ]]; do
	case "$1" in
		--providers)
			providers="${2:?missing --providers value}"
			shift 2
			;;
		--out-dir)
			out_dir="${2:?missing --out-dir value}"
			shift 2
			;;
		--compatibility-file)
			compatibility_file="${2:?missing --compatibility-file value}"
			shift 2
			;;
		--env-file)
			env_file="${2:?missing --env-file value}"
			shift 2
			;;
		--skip-update-docs)
			update_docs=0
			shift
			;;
		--help|-h)
			usage
			exit 0
			;;
		*)
			echo "unknown option: $1" >&2
			usage >&2
			exit 64
			;;
	esac
done

if [[ -z "${providers}" ]]; then
	echo "--providers must not be empty" >&2
	exit 64
fi

if [[ -n "${env_file}" ]]; then
	if [[ ! -f "${env_file}" ]]; then
		echo "env file is missing: ${env_file}" >&2
		exit 2
	fi
	# shellcheck disable=SC1090
	. "${env_file}"
fi

if [[ -z "${out_dir}" ]]; then
	out_dir="${repo_root}/hosted-registry-run-$(date -u +%Y%m%dT%H%M%SZ)"
fi
mkdir -p "${out_dir}"

preflight_report="${out_dir}/preflight.json"
evidence_dir="${out_dir}/evidence"
matrix_report="${out_dir}/matrix.json"
log_dir="${out_dir}/logs"
probe_dir="${out_dir}/probe"
run_report="${out_dir}/run.json"
mkdir -p "${evidence_dir}"
mkdir -p "${log_dir}"
mkdir -p "${probe_dir}"

write_run_report() {
	local result="$1"
	local stage="$2"
	local exit_code="$3"
	local message="$4"
	python3 - "${run_report}" "${result}" "${stage}" "${exit_code}" "${message}" <<'PY'
import json
import os
import sys
from datetime import datetime, timezone

path, result, stage, exit_code, message = sys.argv[1:6]
report = {
    "generatedAt": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
    "result": result,
    "stage": stage,
    "exitCode": int(exit_code),
    "message": message,
    "providers": [item.strip() for item in os.environ["OSIX_HOSTED_REGISTRY_RUN_PROVIDERS"].split(",") if item.strip()],
    "updateDocs": os.environ.get("OSIX_HOSTED_REGISTRY_RUN_UPDATE_DOCS", "0") == "1",
    "compatibilityFile": os.environ["OSIX_HOSTED_REGISTRY_RUN_COMPATIBILITY_FILE"],
    "paths": {
        "preflight": os.environ["OSIX_HOSTED_REGISTRY_RUN_PREFLIGHT"],
        "probe": os.environ["OSIX_HOSTED_REGISTRY_RUN_PROBE_DIR"],
        "evidence": os.environ["OSIX_HOSTED_REGISTRY_RUN_EVIDENCE_DIR"],
        "matrix": os.environ["OSIX_HOSTED_REGISTRY_RUN_MATRIX"],
        "logs": os.environ["OSIX_HOSTED_REGISTRY_RUN_LOG_DIR"],
    },
}
with open(path, "w", encoding="utf-8") as f:
    json.dump(report, f, indent=2, sort_keys=True)
    f.write("\n")
PY
}

export OSIX_HOSTED_REGISTRY_RUN_PROVIDERS="${providers}"
export OSIX_HOSTED_REGISTRY_RUN_UPDATE_DOCS="${update_docs}"
export OSIX_HOSTED_REGISTRY_RUN_COMPATIBILITY_FILE="${compatibility_file}"
export OSIX_HOSTED_REGISTRY_RUN_PREFLIGHT="${preflight_report}"
export OSIX_HOSTED_REGISTRY_RUN_PROBE_DIR="${probe_dir}"
export OSIX_HOSTED_REGISTRY_RUN_EVIDENCE_DIR="${evidence_dir}"
export OSIX_HOSTED_REGISTRY_RUN_MATRIX="${matrix_report}"
export OSIX_HOSTED_REGISTRY_RUN_LOG_DIR="${log_dir}"

echo "Writing hosted registry preflight report: ${preflight_report}"
set +e
OSIX_HOSTED_REGISTRY_MATRIX="${providers}" \
	OSIX_HOSTED_REGISTRY_REQUIRED="${providers}" \
	OSIX_HOSTED_REGISTRY_PREFLIGHT_ONLY=1 \
	OSIX_HOSTED_REGISTRY_PREFLIGHT_REPORT="${preflight_report}" \
	"${repo_root}/scripts/test-registry-hosted-matrix.sh"
preflight_status=$?
set -e
if [[ "${preflight_status}" -ne 0 ]]; then
	write_run_report "failed" "preflight" "${preflight_status}" "required hosted registry preflight failed"
	exit "${preflight_status}"
fi

repo_for_provider() {
	case "$1" in
		ghcr) printf '%s' "${OSIX_HOSTED_REGISTRY_GHCR_REPO:-}" ;;
		ecr) printf '%s' "${OSIX_HOSTED_REGISTRY_ECR_REPO:-}" ;;
		gar) printf '%s' "${OSIX_HOSTED_REGISTRY_GAR_REPO:-}" ;;
		acr) printf '%s' "${OSIX_HOSTED_REGISTRY_ACR_REPO:-}" ;;
		*) printf '%s' "" ;;
	esac
}

probe_script="${OSIX_HOSTED_REGISTRY_PROBE_SCRIPT:-${repo_root}/scripts/probe-hosted-registry.sh}"
IFS=',' read -r -a provider_items <<<"${providers}"
for provider in "${provider_items[@]}"; do
	provider="$(echo "${provider}" | xargs)"
	if [[ -z "${provider}" ]]; then
		continue
	fi
	repo="$(repo_for_provider "${provider}")"
	probe_log="${log_dir}/${provider}-probe.log"
	echo "Probing hosted registry access for ${provider}: ${repo}"
	if ! OSIX_HOSTED_REGISTRY_PROVIDER="${provider}" \
		OSIX_HOSTED_REGISTRY_REPO="${repo}" \
		OSIX_HOSTED_REGISTRY_PROBE_DIR="${probe_dir}" \
		"${probe_script}" >"${probe_log}" 2>&1; then
		cat "${probe_log}" >&2
		echo "Hosted registry probe failed for ${provider}; see ${probe_log}" >&2
		write_run_report "failed" "probe:${provider}" "1" "hosted registry probe failed for ${provider}"
		exit 1
	fi
done

echo "Verifying hosted registry probes: ${probe_dir}"
set +e
"${repo_root}/scripts/verify-hosted-registry-probes.sh" "${probe_dir}" "${providers}"
probe_verify_status=$?
set -e
if [[ "${probe_verify_status}" -ne 0 ]]; then
	write_run_report "failed" "probe-verification" "${probe_verify_status}" "hosted registry probe verification failed"
	exit "${probe_verify_status}"
fi

echo "Running hosted registry live matrix: ${providers}"
set +e
OSIX_HOSTED_REGISTRY_MATRIX="${providers}" \
	OSIX_HOSTED_REGISTRY_REQUIRED="${providers}" \
	OSIX_HOSTED_REGISTRY_EVIDENCE_DIR="${evidence_dir}" \
	OSIX_HOSTED_REGISTRY_MATRIX_REPORT="${matrix_report}" \
	OSIX_HOSTED_REGISTRY_LOG_DIR="${log_dir}" \
	"${repo_root}/scripts/test-registry-hosted-matrix.sh"
matrix_status=$?
set -e
if [[ "${matrix_status}" -ne 0 ]]; then
	write_run_report "failed" "matrix" "${matrix_status}" "hosted registry live matrix failed"
	exit "${matrix_status}"
fi

echo "Verifying hosted registry evidence: ${matrix_report}"
set +e
"${repo_root}/scripts/verify-hosted-registry-evidence.sh" "${matrix_report}" "${providers}"
evidence_status=$?
set -e
if [[ "${evidence_status}" -ne 0 ]]; then
	write_run_report "failed" "evidence-verification" "${evidence_status}" "hosted registry evidence verification failed"
	exit "${evidence_status}"
fi

write_run_report "passed" "complete" "0" "hosted registry verification artifacts complete"

echo "Verifying hosted registry run bundle: ${out_dir}"
set +e
"${repo_root}/scripts/verify-hosted-registry-run.sh" "${out_dir}" "${providers}"
run_verify_status=$?
set -e
if [[ "${run_verify_status}" -ne 0 ]]; then
	write_run_report "failed" "run-verification" "${run_verify_status}" "hosted registry run bundle verification failed"
	exit "${run_verify_status}"
fi

if [[ "${update_docs}" -eq 1 ]]; then
	echo "Updating compatibility matrix: ${compatibility_file}"
	set +e
	OSIX_HOSTED_REGISTRY_REQUIRED="${providers}" \
		"${repo_root}/scripts/update-hosted-registry-compatibility.sh" "${matrix_report}" "${compatibility_file}"
	update_status=$?
	set -e
	if [[ "${update_status}" -ne 0 ]]; then
		write_run_report "failed" "compatibility-update" "${update_status}" "hosted registry compatibility update failed"
		exit "${update_status}"
	fi
else
	echo "Skipping compatibility matrix update"
fi

write_run_report "passed" "complete" "0" "hosted registry verification complete"

cat <<EOF
Hosted registry verification complete.
Preflight: ${preflight_report}
Evidence: ${evidence_dir}
Matrix: ${matrix_report}
Logs: ${log_dir}
Probe: ${probe_dir}
Run: ${run_report}
EOF
