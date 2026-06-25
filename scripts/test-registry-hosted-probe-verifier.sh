#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

probe_dir="${tmp}/probes"
mkdir -p "${probe_dir}"

python3 - "${probe_dir}" <<'PY'
import json
import os
import sys
from datetime import datetime, timezone

probe_dir = sys.argv[1]
providers = {
    "ecr": "123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat",
    "gar": "us-docker.pkg.dev/project/repository/image",
    "acr": "example.azurecr.io/osix-compat",
}
operations = [
    "put-config-blob",
    "put-layer-blob",
    "put-manifest",
    "get-manifest",
    "get-layer-blob",
]
now = datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
for provider, repo in providers.items():
    host = repo.split("/", 1)[0]
    with open(os.path.join(probe_dir, f"{provider}.json"), "w", encoding="utf-8") as f:
        json.dump({
            "configDigest": f"sha256:{provider}config",
            "layerDigest": f"sha256:{provider}layer",
            "manifestDigest": f"sha256:{provider}manifest",
            "operations": operations,
            "profile": provider,
            "provider": provider,
            "registryHost": host,
            "repository": repo,
            "result": "passed",
            "tag": f"osix-probe-{provider}",
            "testedAt": now,
        }, f, indent=2, sort_keys=True)
        f.write("\n")
PY

"${repo_root}/scripts/verify-hosted-registry-probes.sh" "${probe_dir}" ecr,gar,acr \
	> "${tmp}/verify-ok.out"
grep -q "Hosted registry probes verified for: ecr, gar, acr" "${tmp}/verify-ok.out"

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

missing_dir="${tmp}/missing"
cp -R "${probe_dir}" "${missing_dir}"
rm "${missing_dir}/acr.json"
expect_failure missing-provider "probe evidence missing required providers: acr" \
	"${repo_root}/scripts/verify-hosted-registry-probes.sh" "${missing_dir}" ecr,gar,acr

failed_dir="${tmp}/failed"
cp -R "${probe_dir}" "${failed_dir}"
python3 - "${failed_dir}/gar.json" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as f:
    probe = json.load(f)
probe["result"] = "failed"
probe["exitCode"] = 1
probe["failureClass"] = "registry-write-forbidden"
probe["failureHint"] = "gar registry rejected blob upload with 403 Forbidden"
with open(path, "w", encoding="utf-8") as f:
    json.dump(probe, f, indent=2, sort_keys=True)
    f.write("\n")
PY
expect_failure failed-probe "required provider gar probe did not pass: registry-write-forbidden" \
	"${repo_root}/scripts/verify-hosted-registry-probes.sh" "${failed_dir}" ecr,gar,acr

bad_ops_dir="${tmp}/bad-ops"
cp -R "${probe_dir}" "${bad_ops_dir}"
python3 - "${bad_ops_dir}/ecr.json" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as f:
    probe = json.load(f)
probe["operations"] = [op for op in probe["operations"] if op != "get-layer-blob"]
with open(path, "w", encoding="utf-8") as f:
    json.dump(probe, f, indent=2, sort_keys=True)
    f.write("\n")
PY
expect_failure missing-operation "missing operations: get-layer-blob" \
	"${repo_root}/scripts/verify-hosted-registry-probes.sh" "${bad_ops_dir}" ecr,gar,acr

echo "Hosted registry probe verifier smoke passed"
