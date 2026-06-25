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

matrix_report="${tmp}/matrix.json"
evidence_dir="${tmp}/evidence"
mkdir -p "${evidence_dir}"

"${python_bin}" - "${matrix_report}" "${evidence_dir}" <<'PY'
import json
import os
import sys
from datetime import datetime, timezone

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
now = datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
for provider, repo in providers.items():
    path = os.path.join(evidence_dir, f"{provider}.json")
    host = repo.split("/", 1)[0]
    with open(path, "w", encoding="utf-8") as f:
        json.dump({
            "auth": {"dockerConfig": True},
            "operations": operations,
            "profile": provider,
            "provider": provider,
            "registryHost": host,
            "remoteTag": f"compat-{provider}",
            "repository": repo,
            "result": "passed",
            "testedAt": now,
        }, f, indent=2, sort_keys=True)
        f.write("\n")
    events.append({
        "evidenceFiles": [path],
        "exitCode": 0,
        "finishedAt": now,
        "message": f"Hosted registry provider {provider} passed",
        "provider": provider,
        "repository": repo,
        "required": True,
        "result": "passed",
    })
matrix = {
    "counts": {"failed": 0, "passed": 3, "skipped": 0},
    "exitCode": 0,
    "generatedAt": now,
    "matrix": ["ecr", "gar", "acr"],
    "message": "Hosted registry matrix completed: ran=3 skipped=0",
    "providers": events,
    "required": ["acr", "ecr", "gar"],
    "result": "passed",
}
with open(matrix_path, "w", encoding="utf-8") as f:
    json.dump(matrix, f, indent=2, sort_keys=True)
    f.write("\n")
PY

"${repo_root}/scripts/verify-hosted-registry-evidence.sh" "${matrix_report}" ecr,gar,acr \
	> "${tmp}/verify-ok.out"
grep -q "Hosted registry evidence verified for: ecr, gar, acr" "${tmp}/verify-ok.out"

expect_failure() {
	local name="$1"
	local expected="$2"
	shift 2
	set +e
	"$@" > "${tmp}/${name}.out" 2> "${tmp}/${name}.err"
	local rc=$?
	set -e
	if [[ "${rc}" -ne 2 ]]; then
		echo "expected ${name} to fail with exit 2, got ${rc}" >&2
		cat "${tmp}/${name}.out" >&2
		cat "${tmp}/${name}.err" >&2
		exit 1
	fi
	grep -q "${expected}" "${tmp}/${name}.err"
}

bad_matrix="${tmp}/bad-missing-provider.json"
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
expect_failure missing-provider "matrix report missing required providers: acr" \
	"${repo_root}/scripts/verify-hosted-registry-evidence.sh" "${bad_matrix}" ecr,gar,acr

bad_evidence="${tmp}/bad-evidence.json"
"${python_bin}" - "${matrix_report}" "${bad_evidence}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    matrix = json.load(f)
event = next(event for event in matrix["providers"] if event["provider"] == "gar")
event["evidenceFiles"] = []
with open(sys.argv[2], "w", encoding="utf-8") as f:
    json.dump(matrix, f, indent=2, sort_keys=True)
    f.write("\n")
PY
expect_failure missing-evidence "required provider gar has no linked evidenceFiles" \
	"${repo_root}/scripts/verify-hosted-registry-evidence.sh" "${bad_evidence}" ecr,gar,acr

bad_operations="${tmp}/bad-operations"
cp -R "${evidence_dir}" "${bad_operations}"
bad_operations_matrix="${tmp}/bad-operations-matrix.json"
"${python_bin}" - "${matrix_report}" "${bad_operations_matrix}" "${bad_operations}" <<'PY'
import json
import os
import sys

matrix_path, output_path, evidence_dir = sys.argv[1:]
with open(matrix_path, encoding="utf-8") as f:
    matrix = json.load(f)
for event in matrix["providers"]:
    event["evidenceFiles"] = [os.path.join(evidence_dir, f"{event['provider']}.json")]
gar_path = os.path.join(evidence_dir, "gar.json")
with open(gar_path, encoding="utf-8") as f:
    evidence = json.load(f)
evidence["operations"] = [op for op in evidence["operations"] if op != "restore"]
with open(gar_path, "w", encoding="utf-8") as f:
    json.dump(evidence, f, indent=2, sort_keys=True)
    f.write("\n")
with open(output_path, "w", encoding="utf-8") as f:
    json.dump(matrix, f, indent=2, sort_keys=True)
    f.write("\n")
PY
expect_failure missing-operation "missing operations: restore" \
	"${repo_root}/scripts/verify-hosted-registry-evidence.sh" "${bad_operations_matrix}" ecr,gar,acr

echo "Hosted registry evidence verifier smoke passed"
