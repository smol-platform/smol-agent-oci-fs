#!/usr/bin/env bash
set -euo pipefail

matrix_report="${1:-${OSIX_HOSTED_REGISTRY_MATRIX_REPORT:-}}"
compatibility_file="${2:-docs/compatibility-matrix.md}"
required="${OSIX_HOSTED_REGISTRY_REQUIRED:-ecr,gar,acr}"

if [[ -z "${matrix_report}" ]]; then
	echo "usage: $0 MATRIX_REPORT [compatibility-file]" >&2
	echo "or set OSIX_HOSTED_REGISTRY_MATRIX_REPORT" >&2
	exit 64
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
"${repo_root}/scripts/verify-hosted-registry-evidence.sh" "${matrix_report}" "${required}" >/dev/null

python3 - "${matrix_report}" "${compatibility_file}" "${required}" <<'PY'
import json
import os
import sys

matrix_path, compatibility_path, required_csv = sys.argv[1:]
required = {p.strip() for p in required_csv.split(",") if p.strip()}
provider_targets = {
    "ecr": "AWS ECR",
    "gar": "Google Artifact Registry",
    "acr": "Azure Container Registry",
}

with open(matrix_path, "r", encoding="utf-8") as f:
    matrix = json.load(f)

events = {event["provider"]: event for event in matrix.get("providers", []) if event.get("provider")}
evidence_by_provider = {}
for provider in required:
    event = events[provider]
    evidence_path = event["evidenceFiles"][0]
    with open(evidence_path, "r", encoding="utf-8") as f:
        evidence = json.load(f)
    evidence_by_provider[provider] = (event, evidence_path, evidence)

with open(compatibility_path, "r", encoding="utf-8") as f:
    lines = f.readlines()

updated = set()
output = []
for line in lines:
    stripped = line.strip()
    replaced = False
    for provider, target in provider_targets.items():
        if provider not in required:
            continue
        prefix = f"| {target} |"
        if stripped.startswith(prefix):
            event, evidence_path, evidence = evidence_by_provider[provider]
            operations = ", ".join(evidence.get("operations") or [])
            auth = evidence.get("auth") or {}
            auth_methods = [
                name
                for name, enabled in (
                    ("token env", auth.get("tokenEnv")),
                    ("basic env", auth.get("basicEnv")),
                    ("Docker config", auth.get("dockerConfig")),
                )
                if enabled
            ]
            auth_text = ", ".join(auth_methods) if auth_methods else "provider credentials"
            relative_evidence = os.path.relpath(evidence_path, os.path.dirname(os.path.abspath(compatibility_path)))
            if not relative_evidence.startswith("."):
                relative_evidence = f"./{relative_evidence}"
            output.append(
                f"| {target} | `image` + fallback artifact tags + auth | Supported | "
                f"`scripts/test-registry-hosted-matrix.sh` passed for `{evidence['repository']}` on {evidence['testedAt']} "
                f"using {auth_text}; remote tag `{evidence['remoteTag']}` covered {operations}. "
                f"Evidence: `{relative_evidence}`; aggregate report: `{os.path.relpath(matrix_path, os.path.dirname(os.path.abspath(compatibility_path)))}`. |\n"
            )
            updated.add(provider)
            replaced = True
            break
    if not replaced:
        output.append(line)

missing = sorted(required - updated)
if missing:
    print(f"compatibility matrix missing rows for: {', '.join(missing)}", file=sys.stderr)
    sys.exit(2)

with open(compatibility_path, "w", encoding="utf-8") as f:
    f.writelines(output)

print(f"Updated {compatibility_path} for: {', '.join(sorted(updated))}")
PY
