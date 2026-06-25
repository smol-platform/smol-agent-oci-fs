#!/usr/bin/env bash
set -euo pipefail

matrix_report="${1:-${OSIX_HOSTED_REGISTRY_MATRIX_REPORT:-}}"
required="${2:-${OSIX_HOSTED_REGISTRY_REQUIRED:-ecr,gar,acr}}"

if [[ -z "${matrix_report}" ]]; then
	echo "usage: $0 MATRIX_REPORT [required-providers]" >&2
	echo "or set OSIX_HOSTED_REGISTRY_MATRIX_REPORT" >&2
	exit 64
fi

python3 - "${matrix_report}" "${required}" <<'PY'
import json
import os
import sys

matrix_path, required_csv = sys.argv[1:]
required = [p.strip() for p in required_csv.split(",") if p.strip()]
required_operations = {
    "init",
    "snapshot",
    "keyless-sign",
    "verify-local",
    "push",
    "pull",
    "verify-pulled",
    "restore",
    "content-check",
}

def fail(message):
    print(message, file=sys.stderr)
    sys.exit(2)

try:
    with open(matrix_path, "r", encoding="utf-8") as f:
        matrix = json.load(f)
except OSError as exc:
    fail(f"read matrix report {matrix_path}: {exc}")
except json.JSONDecodeError as exc:
    fail(f"parse matrix report {matrix_path}: {exc}")

if matrix.get("result") != "passed" or matrix.get("exitCode") != 0:
    fail(f"matrix report did not pass: result={matrix.get('result')} exitCode={matrix.get('exitCode')}")

providers = matrix.get("providers", [])
provider_events = {}
for event in providers:
    provider = event.get("provider")
    if provider:
        provider_events[provider] = event

missing_providers = [provider for provider in required if provider not in provider_events]
if missing_providers:
    fail(f"matrix report missing required providers: {', '.join(missing_providers)}")

for provider in required:
    event = provider_events[provider]
    if event.get("result") != "passed" or event.get("exitCode") != 0:
        fail(f"required provider {provider} did not pass: {event}")
    evidence_files = event.get("evidenceFiles") or []
    if not evidence_files:
        fail(f"required provider {provider} has no linked evidenceFiles")
    for evidence_path in evidence_files:
        if not os.path.exists(evidence_path):
            fail(f"required provider {provider} evidence file is missing: {evidence_path}")
        try:
            with open(evidence_path, "r", encoding="utf-8") as f:
                evidence = json.load(f)
        except json.JSONDecodeError as exc:
            fail(f"parse evidence {evidence_path}: {exc}")
        if evidence.get("result") != "passed":
            fail(f"evidence {evidence_path} did not pass: {evidence.get('result')}")
        if evidence.get("provider") != provider:
            fail(f"evidence {evidence_path} provider mismatch: {evidence.get('provider')} != {provider}")
        if evidence.get("repository") != event.get("repository"):
            fail(f"evidence {evidence_path} repository mismatch: {evidence.get('repository')} != {event.get('repository')}")
        if evidence.get("profile") != provider:
            fail(f"evidence {evidence_path} profile mismatch: {evidence.get('profile')} != {provider}")
        for key in ("registryHost", "remoteTag", "testedAt"):
            if not evidence.get(key):
                fail(f"evidence {evidence_path} missing {key}")
        operations = set(evidence.get("operations") or [])
        missing_operations = sorted(required_operations - operations)
        if missing_operations:
            fail(f"evidence {evidence_path} missing operations: {', '.join(missing_operations)}")

print(f"Hosted registry evidence verified for: {', '.join(required)}")
PY
