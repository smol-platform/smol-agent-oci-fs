#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

mkdir -p "${tmp}/bin"
cat > "${tmp}/bin/aws" <<'SH'
#!/usr/bin/env bash
exit 0
SH
chmod +x "${tmp}/bin/aws"

hosted_success="${tmp}/hosted-success.sh"
cat > "${hosted_success}" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${OSIX_HOSTED_REGISTRY_PROVIDER:-}" != "ecr" ]]; then
	echo "expected ecr provider" >&2
	exit 9
fi
if [[ -z "${OSIX_HOSTED_REGISTRY_EVIDENCE_DIR:-}" ]]; then
	echo "missing evidence dir" >&2
	exit 9
fi
mkdir -p "${OSIX_HOSTED_REGISTRY_EVIDENCE_DIR}"
cat > "${OSIX_HOSTED_REGISTRY_EVIDENCE_DIR}/ecr.json" <<JSON
{
  "auth": {"tokenEnv": true},
  "operations": [
    "init",
    "snapshot",
    "keyless-sign",
    "verify-local",
    "push",
    "pull",
    "verify-pulled",
    "restore",
    "content-check"
  ],
  "profile": "ecr",
  "provider": "ecr",
  "registryHost": "123456789012.dkr.ecr.us-east-1.amazonaws.com",
  "remoteTag": "compat-ecr",
  "repository": "${OSIX_HOSTED_REGISTRY_REPO}",
  "result": "passed",
  "testedAt": "2026-06-22T22:30:00Z"
}
JSON
SH
chmod +x "${hosted_success}"

probe_success="${tmp}/probe-success.sh"
cat > "${probe_success}" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${OSIX_HOSTED_REGISTRY_PROVIDER:-}" != "ecr" ]]; then
	echo "expected ecr probe provider" >&2
	exit 9
fi
if [[ -z "${OSIX_HOSTED_REGISTRY_PROBE_DIR:-}" ]]; then
	echo "missing probe dir" >&2
	exit 9
fi
mkdir -p "${OSIX_HOSTED_REGISTRY_PROBE_DIR}"
cat > "${OSIX_HOSTED_REGISTRY_PROBE_DIR}/ecr.json" <<JSON
{
  "configDigest": "sha256:config",
  "layerDigest": "sha256:layer",
  "manifestDigest": "sha256:manifest",
  "operations": [
    "put-config-blob",
    "put-layer-blob",
    "put-manifest",
    "get-manifest",
    "get-layer-blob"
  ],
  "profile": "ecr",
  "provider": "ecr",
  "registryHost": "123456789012.dkr.ecr.us-east-1.amazonaws.com",
  "repository": "${OSIX_HOSTED_REGISTRY_REPO}",
  "result": "passed",
  "tag": "osix-probe-ecr",
  "testedAt": "2026-06-22T22:29:00Z"
}
JSON
echo "fake probe passed"
SH
chmod +x "${probe_success}"

compatibility="${tmp}/compatibility-matrix.md"
cp "${repo_root}/docs/compatibility-matrix.md" "${compatibility}"
out_dir="${tmp}/run"
env_file="${tmp}/hosted.env"
cat > "${env_file}" <<'EOF'
export OSIX_HOSTED_REGISTRY_ECR_REPO=123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat
EOF

PATH="${tmp}/bin:${PATH}" \
OSIX_REGISTRY_TOKEN=dummy \
OSIX_HOSTED_REGISTRY_PROBE_SCRIPT="${probe_success}" \
OSIX_HOSTED_REGISTRY_TEST_SCRIPT="${hosted_success}" \
	"${repo_root}/scripts/run-hosted-registry-verification.sh" \
	--providers ecr \
	--env-file "${env_file}" \
	--out-dir "${out_dir}" \
	--compatibility-file "${compatibility}" \
	> "${tmp}/runner.out"

test -f "${out_dir}/preflight.json"
test -f "${out_dir}/run.json"
test -f "${out_dir}/probe/ecr.json"
test -f "${out_dir}/matrix.json"
test -f "${out_dir}/evidence/ecr.json"
test -f "${out_dir}/logs/ecr-probe.log"
test -f "${out_dir}/logs/ecr.log"
grep -q "Hosted registry evidence verified for: ecr" "${tmp}/runner.out"
grep -q "Hosted registry probes verified for: ecr" "${tmp}/runner.out"
grep -q "Hosted registry run verified for: ecr" "${tmp}/runner.out"
grep -q "Hosted registry verification complete" "${tmp}/runner.out"
grep -q "Logs: ${out_dir}/logs" "${tmp}/runner.out"
grep -q "Probe: ${out_dir}/probe" "${tmp}/runner.out"
grep -q "Run: ${out_dir}/run.json" "${tmp}/runner.out"
grep -q "| AWS ECR | .* | Supported | .*123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat" "${compatibility}"
python3 - "${out_dir}/run.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    report = json.load(f)
assert report["result"] == "passed", report
assert report["stage"] == "complete", report
assert report["exitCode"] == 0, report
assert report["providers"] == ["ecr"], report
assert report["paths"]["matrix"].endswith("/matrix.json"), report
PY

failed_out="${tmp}/failed-run"
set +e
PATH="${tmp}/bin:${PATH}" \
	"${repo_root}/scripts/run-hosted-registry-verification.sh" \
	--providers ecr \
	--out-dir "${failed_out}" \
	--skip-update-docs \
	> "${tmp}/failed.out" \
	2> "${tmp}/failed.err"
rc=$?
set -e
if [[ "${rc}" -ne 2 ]]; then
	echo "expected missing repo runner preflight to fail with exit 2, got ${rc}" >&2
	cat "${tmp}/failed.out" >&2
	cat "${tmp}/failed.err" >&2
	exit 1
fi
test -f "${failed_out}/preflight.json"
test -f "${failed_out}/run.json"
grep -q "required hosted registry providers are not ready" "${tmp}/failed.err"
python3 - "${failed_out}/run.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    report = json.load(f)
assert report["result"] == "failed", report
assert report["stage"] == "preflight", report
assert report["exitCode"] == 2, report
PY

echo "Hosted registry verification runner smoke passed"
