#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export PATH="${repo_root}/.osix-tools/bin:${PATH}"
bundle_id="${OSIX_FSKIT_BUNDLE_ID:-io.github.smol-platform.smol-agent-oci-fs.fskit.extension}"
fs_type="${OSIX_FSKIT_TYPE:-OSIxFS}"
ready_timeout="${OSIX_FSKIT_READY_TIMEOUT:-10}"
preflight_report="${OSIX_FSKIT_PREFLIGHT_REPORT:-}"
evidence_dir="${OSIX_FSKIT_EVIDENCE_DIR:-}"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/osix-fskit-test.XXXXXX")"

cleanup() {
  rm -rf "${tmp}"
}
trap cleanup EXIT

write_report() {
  local path="$1"
  local result="$2"
  local doctor_status="$3"
  local test_status="$4"
  local doctor_output="$5"
  mkdir -p "$(dirname "${path}")"
  python3 - "$path" "$result" "$doctor_status" "$test_status" "$doctor_output" <<'PY'
import json
import os
import shutil
import subprocess
import sys
from datetime import datetime, timezone

path, result, doctor_status, test_status, doctor_output_path = sys.argv[1:6]
repo_root = os.environ["OSIX_REPO_ROOT"]
bundle_id = os.environ["OSIX_FSKIT_BUNDLE_ID_EFFECTIVE"]
fs_type = os.environ["OSIX_FSKIT_TYPE_EFFECTIVE"]
app_dir = os.environ.get("OSIX_FSKIT_APP_DIR", os.path.join(os.environ["HOME"], "Applications"))
app_path = os.path.join(app_dir, "OSIxFSKitHost.app")
appex_path = os.path.join(app_path, "Contents", "PlugIns", "OSIxFSKitExtension.appex")
helper = shutil.which("osix-fskitctl")

def codesign_summary(target):
    if not target or not os.path.exists(target):
        return {"exists": False}
    try:
        proc = subprocess.run(
            ["/usr/bin/codesign", "-dv", "--verbose=4", target],
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
        )
    except Exception as exc:
        return {"exists": True, "error": str(exc)}
    summary = {"exists": True, "exitCode": proc.returncode}
    for line in proc.stdout.splitlines():
        if line.startswith("TeamIdentifier="):
            summary["teamIdentifier"] = line.split("=", 1)[1]
        elif line.startswith("Signature="):
            summary["signature"] = line.split("=", 1)[1]
        elif line.startswith("Identifier="):
            summary["identifier"] = line.split("=", 1)[1]
    return summary

try:
    with open(doctor_output_path, "r", encoding="utf-8") as f:
        doctor_output = f.read()
except FileNotFoundError:
    doctor_output = ""

report = {
    "generatedAt": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
    "result": result,
    "bundleID": bundle_id,
    "fileSystemType": fs_type,
    "readyTimeoutSeconds": int(os.environ["OSIX_FSKIT_READY_TIMEOUT_EFFECTIVE"]),
    "requireTeamSigning": os.environ.get("OSIX_FSKIT_REQUIRE_TEAM_SIGNING", "0") == "1",
    "frameworkAvailable": os.path.isdir("/System/Library/Frameworks/FSKit.framework"),
    "helper": {
        "path": helper,
        "codesign": codesign_summary(helper),
    },
    "hostApp": {
        "path": app_path,
        "codesign": codesign_summary(app_path),
    },
    "extension": {
        "path": appex_path,
        "codesign": codesign_summary(appex_path),
    },
    "doctor": {
        "exitCode": int(doctor_status),
        "output": doctor_output,
    },
    "integrationTest": {
        "command": "go test -run TestDarwinFSKitIntegration -count=1 -v ./internal/osix",
        "status": test_status,
    },
    "operations": [
        "build-helper",
        "build-host-app",
        "install-host-app",
        "pluginkit-register",
        "pluginkit-elect",
        "fskit-doctor",
        "darwin-fskit-integration",
    ],
    "repository": repo_root,
}
with open(path, "w", encoding="utf-8") as f:
    json.dump(report, f, indent=2, sort_keys=True)
    f.write("\n")
print(f"Wrote macOS FSKit report {path}")
PY
}

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "macOS FSKit test requires Darwin; current OS is $(uname -s)" >&2
  exit 2
fi

export OSIX_REPO_ROOT="${repo_root}"
export OSIX_FSKIT_BUNDLE_ID_EFFECTIVE="${bundle_id}"
export OSIX_FSKIT_TYPE_EFFECTIVE="${fs_type}"
export OSIX_FSKIT_READY_TIMEOUT_EFFECTIVE="${ready_timeout}"

"${repo_root}/scripts/install-macos-fskit-app.sh" --no-open --background-register --wait-ready="${ready_timeout}" --no-open-settings

if osix-fskitctl doctor --bundle-id "${bundle_id}" --fstype "${fs_type}" > "${tmp}/doctor.out" 2>&1; then
  doctor_status=0
else
  doctor_status=$?
fi
if [[ -n "${preflight_report}" ]]; then
  result="ready"
  if [[ "${doctor_status}" -ne 0 ]]; then
    result="blocked"
  fi
  write_report "${preflight_report}" "${result}" "${doctor_status}" "not-run" "${tmp}/doctor.out"
fi
if [[ "${doctor_status}" -ne 0 ]]; then
  cat "${tmp}/doctor.out" >&2
  echo "fix the listed FSKit extension prerequisite, then rerun this script" >&2
  echo "for local development, run: ${repo_root}/scripts/install-macos-fskit-app.sh" >&2
  exit 2
fi
cat "${tmp}/doctor.out"

cd "${repo_root}"
go test -run TestDarwinFSKitIntegration -count=1 -v ./internal/osix
if [[ -n "${evidence_dir}" ]]; then
  evidence_file="${evidence_dir}/macos-fskit-$(date -u +%Y%m%dT%H%M%SZ).json"
  write_report "${evidence_file}" "passed" "${doctor_status}" "passed" "${tmp}/doctor.out"
fi
