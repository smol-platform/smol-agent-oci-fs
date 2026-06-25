#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

mkdir -p "${tmp}/bin"
for tool in aws gcloud az; do
	printf '#!/usr/bin/env sh\nexit 0\n' > "${tmp}/bin/${tool}"
	chmod +x "${tmp}/bin/${tool}"
done
export PATH="${tmp}/bin:${PATH}"

python_bin="$(command -v python3)"
if [[ -z "${python_bin}" ]]; then
	echo "python3 is required" >&2
	exit 2
fi

success_harness="${tmp}/hosted-success.sh"
cat > "${success_harness}" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [[ -z "${OSIX_HOSTED_REGISTRY_PROVIDER:-}" || -z "${OSIX_HOSTED_REGISTRY_REPO:-}" ]]; then
	echo "missing provider env" >&2
	exit 9
fi
if [[ -n "${OSIX_HOSTED_REGISTRY_EVIDENCE_DIR:-}" ]]; then
	mkdir -p "${OSIX_HOSTED_REGISTRY_EVIDENCE_DIR}"
	printf '{"provider":"%s","repository":"%s","result":"passed"}\n' \
		"${OSIX_HOSTED_REGISTRY_PROVIDER}" \
		"${OSIX_HOSTED_REGISTRY_REPO}" \
		> "${OSIX_HOSTED_REGISTRY_EVIDENCE_DIR}/${OSIX_HOSTED_REGISTRY_PROVIDER}.json"
fi
exit 0
SH
chmod +x "${success_harness}"

unexpected_harness="${tmp}/hosted-unexpected.sh"
cat > "${unexpected_harness}" <<'SH'
#!/usr/bin/env bash
echo "unexpected hosted child invocation" >&2
exit 99
SH
chmod +x "${unexpected_harness}"

failure_harness="${tmp}/hosted-failure.sh"
cat > "${failure_harness}" <<'SH'
#!/usr/bin/env bash
echo "osix: start blob upload sha256:deadbeef: 403 Forbidden"
exit 7
SH
chmod +x "${failure_harness}"

expect_failure() {
	local expected="$1"
	shift
	set +e
	"$@"
	local rc=$?
	set -e
	if [[ "${rc}" -ne "${expected}" ]]; then
		echo "expected exit ${expected}, got ${rc}" >&2
		exit 1
	fi
}

skip_report="${tmp}/skip-report.json"
expect_failure 2 env \
	OSIX_HOSTED_REGISTRY_MATRIX=ecr \
	OSIX_HOSTED_REGISTRY_MATRIX_REPORT="${skip_report}" \
	"${repo_root}/scripts/test-registry-hosted-matrix.sh"
"${python_bin}" - "${skip_report}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    report = json.load(f)
assert report["result"] == "failed", report
assert report["exitCode"] == 2, report
assert report["counts"] == {"failed": 0, "passed": 0, "skipped": 1}, report
assert report["providers"][0]["provider"] == "ecr", report
assert report["providers"][0]["result"] == "skipped", report
PY

required_report="${tmp}/required-report.json"
expect_failure 2 env \
	OSIX_HOSTED_REGISTRY_MATRIX=acr \
	OSIX_HOSTED_REGISTRY_REQUIRED=acr \
	OSIX_HOSTED_REGISTRY_MATRIX_REPORT="${required_report}" \
	"${repo_root}/scripts/test-registry-hosted-matrix.sh"
"${python_bin}" - "${required_report}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    report = json.load(f)
assert report["result"] == "failed", report
assert report["counts"]["failed"] == 1, report
event = report["providers"][0]
assert event["provider"] == "acr", event
assert event["required"] is True, event
assert event["result"] == "failed", event
assert "not ready" in event["message"], event
assert "OSIX_HOSTED_REGISTRY_ACR_REPO" in event["message"], event
PY

preflight_gate_report="${tmp}/preflight-gate-report.json"
expect_failure 2 env \
	OSIX_HOSTED_REGISTRY_MATRIX=ecr \
	OSIX_HOSTED_REGISTRY_REQUIRED=ecr \
	OSIX_HOSTED_REGISTRY_ECR_REPO=ghcr.io/wrong/osix \
	OSIX_REGISTRY_TOKEN=dummy \
	OSIX_HOSTED_REGISTRY_TEST_SCRIPT="${unexpected_harness}" \
	OSIX_HOSTED_REGISTRY_MATRIX_REPORT="${preflight_gate_report}" \
	"${repo_root}/scripts/test-registry-hosted-matrix.sh"
"${python_bin}" - "${preflight_gate_report}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    report = json.load(f)
assert report["result"] == "failed", report
assert report["exitCode"] == 2, report
assert report["counts"] == {"failed": 1, "passed": 0, "skipped": 0}, report
event = report["providers"][0]
assert event["provider"] == "ecr", event
assert event["required"] is True, event
assert event["result"] == "failed", event
assert event["exitCode"] == 2, event
assert "not ready" in event["message"], event
assert "repo-format:ACCOUNT.dkr.ecr.REGION.amazonaws.com/REPOSITORY" in event["message"], event
PY

success_report="${tmp}/success-report.json"
success_evidence="${tmp}/success-evidence"
success_logs="${tmp}/success-logs"
OSIX_HOSTED_REGISTRY_MATRIX=ecr \
OSIX_HOSTED_REGISTRY_ECR_REPO=123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat \
OSIX_HOSTED_REGISTRY_TEST_SCRIPT="${success_harness}" \
OSIX_HOSTED_REGISTRY_EVIDENCE_DIR="${success_evidence}" \
OSIX_HOSTED_REGISTRY_MATRIX_REPORT="${success_report}" \
OSIX_HOSTED_REGISTRY_LOG_DIR="${success_logs}" \
	"${repo_root}/scripts/test-registry-hosted-matrix.sh" >/dev/null
"${python_bin}" - "${success_report}" "${success_evidence}/ecr.json" "${success_logs}/ecr.log" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    report = json.load(f)
assert report["result"] == "passed", report
assert report["exitCode"] == 0, report
assert report["counts"] == {"failed": 0, "passed": 1, "skipped": 0}, report
event = report["providers"][0]
assert event["provider"] == "ecr", event
assert event["result"] == "passed", event
assert event["repository"] == "123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat", event
assert event["evidenceFiles"] == [sys.argv[2]], event
assert event["logFile"] == sys.argv[3], event
PY
test -f "${success_logs}/ecr.log"

failure_report="${tmp}/failure-report.json"
failure_logs="${tmp}/failure-logs"
expect_failure 7 env \
	OSIX_HOSTED_REGISTRY_MATRIX=gar \
	OSIX_HOSTED_REGISTRY_GAR_REPO=us-docker.pkg.dev/project/repository/image \
	OSIX_HOSTED_REGISTRY_TEST_SCRIPT="${failure_harness}" \
	OSIX_HOSTED_REGISTRY_MATRIX_REPORT="${failure_report}" \
	OSIX_HOSTED_REGISTRY_LOG_DIR="${failure_logs}" \
	"${repo_root}/scripts/test-registry-hosted-matrix.sh"
"${python_bin}" - "${failure_report}" "${failure_logs}/gar.log" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    report = json.load(f)
assert report["result"] == "failed", report
assert report["exitCode"] == 7, report
assert report["counts"] == {"failed": 1, "passed": 0, "skipped": 0}, report
event = report["providers"][0]
assert event["provider"] == "gar", event
assert event["result"] == "failed", event
assert event["exitCode"] == 7, event
assert event["logFile"] == sys.argv[2], event
assert event["failureClass"] == "registry-write-forbidden", event
assert "403 Forbidden" in event["failureHint"], event
PY
test -f "${failure_logs}/gar.log"

echo "Hosted registry matrix report smoke passed"
