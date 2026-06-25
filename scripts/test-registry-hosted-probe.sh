#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

success_osix="${tmp}/osix-success"
cat > "${success_osix}" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" != "registry" || "$2" != "probe" ]]; then
	echo "unexpected command: $*" >&2
	exit 9
fi
repo="$3"
tag=""
shift 3
while [[ "$#" -gt 0 ]]; do
	case "$1" in
		--tag)
			tag="$2"
			shift 2
			;;
		--json)
			shift
			;;
		*)
			echo "unexpected arg: $1" >&2
			exit 9
			;;
	esac
done
cat <<JSON
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
  "registryHost": "${repo%%/*}",
  "repository": "${repo}",
  "result": "passed",
  "tag": "${tag}",
  "testedAt": "2026-06-22T22:29:00Z"
}
JSON
SH
chmod +x "${success_osix}"

success_dir="${tmp}/success"
OSIX_HOSTED_REGISTRY_PROVIDER=ecr \
OSIX_HOSTED_REGISTRY_REPO=123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat \
OSIX_HOSTED_REGISTRY_PROBE_DIR="${success_dir}" \
OSIX_HOSTED_REGISTRY_PROBE_OSIX="${success_osix}" \
	"${repo_root}/scripts/probe-hosted-registry.sh" > "${tmp}/success.out"
success_json="$(find "${success_dir}" -name 'ecr-*.json' -type f | head -n 1)"
test -n "${success_json}"
python3 - "${success_json}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    probe = json.load(f)
assert probe["result"] == "passed", probe
assert probe["provider"] == "ecr", probe
assert probe["repository"] == "123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat", probe
assert probe["tag"].startswith("osix-probe-"), probe
assert "failureClass" not in probe, probe
PY

failure_osix="${tmp}/osix-failure"
cat > "${failure_osix}" <<'SH'
#!/usr/bin/env bash
echo "osix: start blob upload sha256:deadbeef: 403 Forbidden" >&2
exit 1
SH
chmod +x "${failure_osix}"

failure_dir="${tmp}/failure"
set +e
OSIX_HOSTED_REGISTRY_PROVIDER=ecr \
OSIX_HOSTED_REGISTRY_REPO=123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat \
OSIX_HOSTED_REGISTRY_PROBE_DIR="${failure_dir}" \
OSIX_HOSTED_REGISTRY_PROBE_OSIX="${failure_osix}" \
	"${repo_root}/scripts/probe-hosted-registry.sh" > "${tmp}/failure.out" 2> "${tmp}/failure.err"
rc=$?
set -e
if [[ "${rc}" -ne 1 ]]; then
	echo "expected failed probe to exit 1, got ${rc}" >&2
	cat "${tmp}/failure.out" >&2
	cat "${tmp}/failure.err" >&2
	exit 1
fi
failure_json="$(find "${failure_dir}" -name 'ecr-*.json' -type f | head -n 1)"
failure_log="$(find "${failure_dir}" -name 'ecr-*.log' -type f | head -n 1)"
test -n "${failure_json}"
test -n "${failure_log}"
python3 - "${failure_json}" "${failure_log}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    probe = json.load(f)
assert probe["result"] == "failed", probe
assert probe["exitCode"] == 1, probe
assert probe["failureClass"] == "registry-write-forbidden", probe
assert "403 Forbidden" in probe["failureHint"], probe
assert probe["logFile"] == sys.argv[2], probe
assert probe["tag"].startswith("osix-probe-"), probe
PY
grep -q "403 Forbidden" "${failure_log}"

echo "Hosted registry probe smoke passed"
