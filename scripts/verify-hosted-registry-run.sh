#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
run_dir="${1:-${OSIX_HOSTED_REGISTRY_OUTPUT_DIR:-}}"
required="${2:-${OSIX_HOSTED_REGISTRY_REQUIRED:-ecr,gar,acr}}"

if [[ -z "${run_dir}" ]]; then
	echo "usage: $0 RUN_DIR [required-providers]" >&2
	echo "or set OSIX_HOSTED_REGISTRY_OUTPUT_DIR" >&2
	exit 64
fi

preflight_report="${run_dir}/preflight.json"
probe_dir="${run_dir}/probe"
matrix_report="${run_dir}/matrix.json"
log_dir="${run_dir}/logs"
run_report="${run_dir}/run.json"

python3 - "${run_report}" "${required}" <<'PY'
import json
import os
import sys

run_path, required_csv = sys.argv[1:]
required = [p.strip() for p in required_csv.split(",") if p.strip()]

def fail(message):
    print(message, file=sys.stderr)
    sys.exit(2)

if not os.path.exists(run_path):
    fail(f"run report is missing: {run_path}")
try:
    with open(run_path, "r", encoding="utf-8") as f:
        report = json.load(f)
except json.JSONDecodeError as exc:
    fail(f"parse run report {run_path}: {exc}")

if report.get("result") != "passed" or report.get("stage") != "complete" or report.get("exitCode") != 0:
    fail(f"run report did not pass: result={report.get('result')} stage={report.get('stage')} exitCode={report.get('exitCode')}")
reported_providers = report.get("providers") or []
missing = [provider for provider in required if provider not in reported_providers]
if missing:
    fail(f"run report missing required providers: {', '.join(missing)}")
paths = report.get("paths") or {}
for key in ("preflight", "probe", "evidence", "matrix", "logs"):
    if not paths.get(key):
        fail(f"run report missing paths.{key}")
PY

python3 - "${preflight_report}" "${required}" <<'PY'
import json
import os
import sys

preflight_path, required_csv = sys.argv[1:]
required = [p.strip() for p in required_csv.split(",") if p.strip()]

def fail(message):
    print(message, file=sys.stderr)
    sys.exit(2)

if not os.path.exists(preflight_path):
    fail(f"preflight report is missing: {preflight_path}")
try:
    with open(preflight_path, "r", encoding="utf-8") as f:
        preflight = json.load(f)
except json.JSONDecodeError as exc:
    fail(f"parse preflight report {preflight_path}: {exc}")

providers = {
    item.get("provider"): item
    for item in preflight.get("providers", [])
    if item.get("provider")
}
missing = [provider for provider in required if provider not in providers]
if missing:
    fail(f"preflight report missing required providers: {', '.join(missing)}")
not_ready = []
for provider in required:
    item = providers[provider]
    if not item.get("readyToRun"):
        missing_items = ", ".join(item.get("missing") or []) or "unknown"
        not_ready.append(f"{provider}: {missing_items}")
if not_ready:
    fail("preflight required providers were not ready: " + "; ".join(not_ready))
PY

"${repo_root}/scripts/verify-hosted-registry-probes.sh" "${probe_dir}" "${required}"
"${repo_root}/scripts/verify-hosted-registry-evidence.sh" "${matrix_report}" "${required}"

if [[ ! -d "${log_dir}" ]]; then
	echo "hosted registry log directory is missing: ${log_dir}" >&2
	exit 2
fi

python3 - "${matrix_report}" "${log_dir}" "${required}" <<'PY'
import json
import os
import sys

matrix_path, log_dir, required_csv = sys.argv[1:]
required = [p.strip() for p in required_csv.split(",") if p.strip()]

def fail(message):
    print(message, file=sys.stderr)
    sys.exit(2)

with open(matrix_path, "r", encoding="utf-8") as f:
    matrix = json.load(f)
events = {
    event.get("provider"): event
    for event in matrix.get("providers", [])
    if event.get("provider")
}
for provider in required:
    probe_log = os.path.join(log_dir, f"{provider}-probe.log")
    if not os.path.exists(probe_log):
        fail(f"probe log is missing for {provider}: {probe_log}")
    event = events.get(provider, {})
    log_file = event.get("logFile")
    if not log_file:
        fail(f"matrix event missing logFile for {provider}")
    if not os.path.exists(log_file):
        fail(f"matrix logFile is missing for {provider}: {log_file}")
print(f"Hosted registry run verified for: {', '.join(required)}")
PY
