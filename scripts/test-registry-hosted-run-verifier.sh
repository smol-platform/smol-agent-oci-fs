#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

run_dir="${tmp}/run"
mkdir -p "${run_dir}/probe" "${run_dir}/evidence" "${run_dir}/logs"

python3 - "${run_dir}" <<'PY'
import json
import os
import sys
from datetime import datetime, timezone

run_dir = sys.argv[1]
providers = {
    "ecr": "123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat",
    "gar": "us-docker.pkg.dev/project/repository/image",
    "acr": "example.azurecr.io/osix-compat",
}
now = datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
preflight = {"generatedAt": now, "matrix": list(providers), "required": sorted(providers), "providers": []}
events = []
for provider, repo in providers.items():
    host = repo.split("/", 1)[0]
    preflight["providers"].append({
        "provider": provider,
        "required": True,
        "repository": repo,
        "registryHost": host,
        "readyToRun": True,
        "missing": [],
    })
    probe_path = os.path.join(run_dir, "probe", f"{provider}.json")
    with open(probe_path, "w", encoding="utf-8") as f:
        json.dump({
            "configDigest": f"sha256:{provider}config",
            "layerDigest": f"sha256:{provider}layer",
            "manifestDigest": f"sha256:{provider}manifest",
            "operations": ["put-config-blob", "put-layer-blob", "put-manifest", "get-manifest", "get-layer-blob"],
            "profile": provider,
            "provider": provider,
            "registryHost": host,
            "repository": repo,
            "result": "passed",
            "tag": f"osix-probe-{provider}",
            "testedAt": now,
        }, f, indent=2, sort_keys=True)
        f.write("\n")
    evidence_path = os.path.join(run_dir, "evidence", f"{provider}.json")
    with open(evidence_path, "w", encoding="utf-8") as f:
        json.dump({
            "auth": {"dockerConfig": True},
            "operations": ["init", "snapshot", "keyless-sign", "verify-local", "push", "pull", "verify-pulled", "restore", "content-check"],
            "profile": provider,
            "provider": provider,
            "registryHost": host,
            "remoteTag": f"compat-{provider}",
            "repository": repo,
            "result": "passed",
            "testedAt": now,
        }, f, indent=2, sort_keys=True)
        f.write("\n")
    probe_log = os.path.join(run_dir, "logs", f"{provider}-probe.log")
    with open(probe_log, "w", encoding="utf-8") as f:
        f.write("probe ok\n")
    provider_log = os.path.join(run_dir, "logs", f"{provider}.log")
    with open(provider_log, "w", encoding="utf-8") as f:
        f.write("matrix ok\n")
    events.append({
        "evidenceFiles": [evidence_path],
        "exitCode": 0,
        "finishedAt": now,
        "logFile": provider_log,
        "message": f"Hosted registry provider {provider} passed",
        "provider": provider,
        "repository": repo,
        "required": True,
        "result": "passed",
    })
with open(os.path.join(run_dir, "preflight.json"), "w", encoding="utf-8") as f:
    json.dump(preflight, f, indent=2, sort_keys=True)
    f.write("\n")
with open(os.path.join(run_dir, "matrix.json"), "w", encoding="utf-8") as f:
    json.dump({
        "counts": {"failed": 0, "passed": 3, "skipped": 0},
        "exitCode": 0,
        "generatedAt": now,
        "matrix": list(providers),
        "message": "Hosted registry matrix completed: ran=3 skipped=0",
        "providers": events,
        "required": sorted(providers),
        "result": "passed",
    }, f, indent=2, sort_keys=True)
    f.write("\n")
with open(os.path.join(run_dir, "run.json"), "w", encoding="utf-8") as f:
    json.dump({
        "compatibilityFile": "docs/compatibility-matrix.md",
        "exitCode": 0,
        "generatedAt": now,
        "message": "hosted registry verification complete",
        "paths": {
            "evidence": os.path.join(run_dir, "evidence"),
            "logs": os.path.join(run_dir, "logs"),
            "matrix": os.path.join(run_dir, "matrix.json"),
            "preflight": os.path.join(run_dir, "preflight.json"),
            "probe": os.path.join(run_dir, "probe"),
        },
        "providers": list(providers),
        "result": "passed",
        "stage": "complete",
        "updateDocs": True,
    }, f, indent=2, sort_keys=True)
    f.write("\n")
PY

"${repo_root}/scripts/verify-hosted-registry-run.sh" "${run_dir}" ecr,gar,acr \
	> "${tmp}/verify-ok.out"
grep -q "Hosted registry run verified for: ecr, gar, acr" "${tmp}/verify-ok.out"

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

bad_preflight="${tmp}/bad-preflight"
cp -R "${run_dir}" "${bad_preflight}"
python3 - "${bad_preflight}/preflight.json" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as f:
    preflight = json.load(f)
gar = next(item for item in preflight["providers"] if item["provider"] == "gar")
gar["readyToRun"] = False
gar["missing"] = ["registry-credentials"]
with open(path, "w", encoding="utf-8") as f:
    json.dump(preflight, f, indent=2, sort_keys=True)
    f.write("\n")
PY
expect_failure bad-preflight "preflight required providers were not ready: gar: registry-credentials" \
	"${repo_root}/scripts/verify-hosted-registry-run.sh" "${bad_preflight}" ecr,gar,acr

bad_log="${tmp}/bad-log"
cp -R "${run_dir}" "${bad_log}"
rm "${bad_log}/logs/ecr-probe.log"
expect_failure missing-probe-log "probe log is missing for ecr" \
	"${repo_root}/scripts/verify-hosted-registry-run.sh" "${bad_log}" ecr,gar,acr

bad_matrix="${tmp}/bad-matrix"
cp -R "${run_dir}" "${bad_matrix}"
python3 - "${bad_matrix}/matrix.json" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as f:
    matrix = json.load(f)
event = next(item for item in matrix["providers"] if item["provider"] == "acr")
event.pop("logFile", None)
with open(path, "w", encoding="utf-8") as f:
    json.dump(matrix, f, indent=2, sort_keys=True)
    f.write("\n")
PY
expect_failure missing-matrix-log "matrix event missing logFile for acr" \
	"${repo_root}/scripts/verify-hosted-registry-run.sh" "${bad_matrix}" ecr,gar,acr

bad_run="${tmp}/bad-run"
cp -R "${run_dir}" "${bad_run}"
python3 - "${bad_run}/run.json" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as f:
    report = json.load(f)
report["result"] = "failed"
report["stage"] = "probe:ecr"
report["exitCode"] = 1
with open(path, "w", encoding="utf-8") as f:
    json.dump(report, f, indent=2, sort_keys=True)
    f.write("\n")
PY
expect_failure failed-run-report "run report did not pass: result=failed stage=probe:ecr exitCode=1" \
	"${repo_root}/scripts/verify-hosted-registry-run.sh" "${bad_run}" ecr,gar,acr

echo "Hosted registry run verifier smoke passed"
