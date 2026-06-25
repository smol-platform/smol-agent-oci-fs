#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
providers="${OSIX_HOSTED_REGISTRY_MATRIX:-ghcr,ecr,gar,acr}"
required="${OSIX_HOSTED_REGISTRY_REQUIRED:-}"
report_path=""

usage() {
	cat <<'EOF'
Usage: doctor-hosted-registry.sh [options]

Runs hosted registry preflight and prints a human-readable readiness summary.

Options:
  --providers LIST   Providers to inspect. Default: ghcr,ecr,gar,acr
  --required LIST    Providers that must be ready. Default: OSIX_HOSTED_REGISTRY_REQUIRED
  --report PATH      Also write the machine-readable preflight JSON to PATH
  --help
EOF
}

while [[ "$#" -gt 0 ]]; do
	case "$1" in
		--providers)
			providers="${2:?missing --providers value}"
			shift 2
			;;
		--required)
			required="${2:?missing --required value}"
			shift 2
			;;
		--report)
			report_path="${2:?missing --report value}"
			shift 2
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

tmp="$(mktemp -d "${TMPDIR:-/tmp}/osix-hosted-registry-doctor.XXXXXX")"
cleanup() {
	rm -rf "${tmp}"
}
trap cleanup EXIT

preflight_report="${report_path:-${tmp}/preflight.json}"
preflight_stderr="${tmp}/preflight.err"
set +e
OSIX_HOSTED_REGISTRY_MATRIX="${providers}" \
OSIX_HOSTED_REGISTRY_REQUIRED="${required}" \
OSIX_HOSTED_REGISTRY_PREFLIGHT_ONLY=1 \
OSIX_HOSTED_REGISTRY_PREFLIGHT_REPORT="${preflight_report}" \
	"${repo_root}/scripts/test-registry-hosted-matrix.sh" >/dev/null 2>"${preflight_stderr}"
preflight_status=$?
set -e
if [[ "${preflight_status}" -ne 0 && "${preflight_status}" -ne 2 ]]; then
	cat "${preflight_stderr}" >&2
	exit "${preflight_status}"
fi

python3 - "${preflight_report}" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, "r", encoding="utf-8") as f:
    report = json.load(f)

print("Hosted registry readiness")
print(f"Report: {path}")
print(f"Matrix: {', '.join(report.get('matrix', [])) or '(none)'}")
required = report.get("required", [])
print(f"Required: {', '.join(required) if required else '(none)'}")
print("")

not_ready_required = []
for item in report.get("providers", []):
    provider = item.get("provider", "")
    status = "ready" if item.get("readyToRun") else "not-ready"
    marker = "!" if item.get("required") and not item.get("readyToRun") else "-"
    print(f"{marker} {provider}: {status}")
    repo = item.get("repository") or "(unset)"
    print(f"  repo: {repo}")
    missing = item.get("missing") or []
    if missing:
        print(f"  missing: {', '.join(missing)}")
    hosts = item.get("credentialedRegistryHosts") or []
    if hosts:
        print(f"  credentialed hosts: {', '.join(hosts)}")
    identities = item.get("providerCLIIdentities") or {}
    for cli, identity in identities.items():
        if not identity.get("available"):
            print(f"  cli {cli}: missing")
            continue
        state = "authenticated" if identity.get("authenticated") else "not-authenticated"
        details = []
        for key in ("account", "project", "subscriptionId", "name"):
            if identity.get(key):
                details.append(f"{key}={identity[key]}")
        accounts = identity.get("accounts") or []
        if accounts:
            details.append(f"accounts={','.join(accounts)}")
        if identity.get("error"):
            details.append(f"error={identity['error']}")
        suffix = f" ({'; '.join(details)})" if details else ""
        print(f"  cli {cli}: {state}{suffix}")
    suggestions = item.get("suggestedRepositoryExports") or []
    for suggestion in suggestions:
        print(f"  suggestion: {suggestion}")
    if item.get("required") and not item.get("readyToRun"):
        not_ready_required.append(provider)

if not_ready_required:
    print("")
    print(f"Required providers not ready: {', '.join(not_ready_required)}")
    sys.exit(2)
PY
