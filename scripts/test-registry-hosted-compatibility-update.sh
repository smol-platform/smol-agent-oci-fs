#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

python_bin="$(command -v python3)"
if [[ -z "${python_bin}" ]]; then
	echo "python3 is required" >&2
	exit 2
fi

compatibility="${tmp}/compatibility-matrix.md"
cp "${repo_root}/docs/compatibility-matrix.md" "${compatibility}"
matrix_report="${tmp}/matrix.json"
evidence_dir="${tmp}/evidence"
mkdir -p "${evidence_dir}"

"${python_bin}" - "${matrix_report}" "${evidence_dir}" <<'PY'
import json
import os
import sys

matrix_path, evidence_dir = sys.argv[1:]
providers = {
    "ecr": "123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat",
    "gar": "us-docker.pkg.dev/project/repository/image",
    "acr": "example.azurecr.io/osix-compat",
}
operations = [
    "init",
    "snapshot",
    "keyless-sign",
    "verify-local",
    "push",
    "pull",
    "verify-pulled",
    "restore",
    "content-check",
]
events = []
for provider, repo in providers.items():
    path = os.path.join(evidence_dir, f"{provider}.json")
    with open(path, "w", encoding="utf-8") as f:
        json.dump({
            "auth": {"dockerConfig": True},
            "operations": operations,
            "profile": provider,
            "provider": provider,
            "registryHost": repo.split("/", 1)[0],
            "remoteTag": f"compat-{provider}",
            "repository": repo,
            "result": "passed",
            "testedAt": "2026-06-22T22:15:00Z",
        }, f, indent=2, sort_keys=True)
        f.write("\n")
    events.append({
        "evidenceFiles": [path],
        "exitCode": 0,
        "finishedAt": "2026-06-22T22:15:00Z",
        "message": f"Hosted registry provider {provider} passed",
        "provider": provider,
        "repository": repo,
        "required": True,
        "result": "passed",
    })
with open(matrix_path, "w", encoding="utf-8") as f:
    json.dump({
        "counts": {"failed": 0, "passed": 3, "skipped": 0},
        "exitCode": 0,
        "generatedAt": "2026-06-22T22:15:00Z",
        "matrix": ["ecr", "gar", "acr"],
        "message": "Hosted registry matrix completed: ran=3 skipped=0",
        "providers": events,
        "required": ["acr", "ecr", "gar"],
        "result": "passed",
    }, f, indent=2, sort_keys=True)
    f.write("\n")
PY

"${repo_root}/scripts/update-hosted-registry-compatibility.sh" "${matrix_report}" "${compatibility}" \
	> "${tmp}/update.out"
grep -q "Updated ${compatibility} for: acr, ecr, gar" "${tmp}/update.out"
grep -q "| AWS ECR | .* | Supported | .*123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat" "${compatibility}"
grep -q "| Google Artifact Registry | .* | Supported | .*us-docker.pkg.dev/project/repository/image" "${compatibility}"
grep -q "| Azure Container Registry | .* | Supported | .*example.azurecr.io/osix-compat" "${compatibility}"
grep -q "remote tag \`compat-ecr\`" "${compatibility}"
grep -q "Evidence: " "${compatibility}"
grep -q "aggregate report: " "${compatibility}"

bad_matrix="${tmp}/bad-matrix.json"
"${python_bin}" - "${matrix_report}" "${bad_matrix}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    matrix = json.load(f)
matrix["providers"] = [event for event in matrix["providers"] if event["provider"] != "acr"]
matrix["counts"]["passed"] = 2
with open(sys.argv[2], "w", encoding="utf-8") as f:
    json.dump(matrix, f, indent=2, sort_keys=True)
    f.write("\n")
PY
set +e
"${repo_root}/scripts/update-hosted-registry-compatibility.sh" "${bad_matrix}" "${tmp}/bad-compat.md" \
	> "${tmp}/bad-update.out" \
	2> "${tmp}/bad-update.err"
rc=$?
set -e
if [[ "${rc}" -ne 2 ]]; then
	echo "expected bad evidence update to fail with exit 2, got ${rc}" >&2
	exit 1
fi
grep -q "matrix report missing required providers: acr" "${tmp}/bad-update.err"

echo "Hosted registry compatibility update smoke passed"
