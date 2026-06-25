#!/usr/bin/env bash
set -euo pipefail

probe_dir="${1:-${OSIX_HOSTED_REGISTRY_PROBE_DIR:-}}"
required="${2:-${OSIX_HOSTED_REGISTRY_REQUIRED:-ecr,gar,acr}}"

if [[ -z "${probe_dir}" ]]; then
	echo "usage: $0 PROBE_DIR [required-providers]" >&2
	echo "or set OSIX_HOSTED_REGISTRY_PROBE_DIR" >&2
	exit 64
fi

python3 - "${probe_dir}" "${required}" <<'PY'
import glob
import json
import os
import sys

probe_dir, required_csv = sys.argv[1:]
required = [p.strip() for p in required_csv.split(",") if p.strip()]
required_operations = {
    "put-config-blob",
    "put-layer-blob",
    "put-manifest",
    "get-manifest",
    "get-layer-blob",
}

def fail(message):
    print(message, file=sys.stderr)
    sys.exit(2)

if not os.path.isdir(probe_dir):
    fail(f"probe directory is missing: {probe_dir}")

by_provider = {}
for path in sorted(glob.glob(os.path.join(probe_dir, "*.json"))):
    try:
        with open(path, "r", encoding="utf-8") as f:
            probe = json.load(f)
    except json.JSONDecodeError as exc:
        fail(f"parse probe evidence {path}: {exc}")
    provider = probe.get("provider")
    if provider:
        by_provider.setdefault(provider, []).append((path, probe))

missing = [provider for provider in required if provider not in by_provider]
if missing:
    fail(f"probe evidence missing required providers: {', '.join(missing)}")

for provider in required:
    passed = [(path, probe) for path, probe in by_provider[provider] if probe.get("result") == "passed"]
    if not passed:
        latest_path, latest_probe = by_provider[provider][-1]
        if latest_probe.get("result") == "failed":
            detail = latest_probe.get("failureClass") or latest_probe.get("failureHint") or latest_probe.get("exitCode")
            fail(f"required provider {provider} probe did not pass: {detail} ({latest_path})")
        fail(f"required provider {provider} has no passed probe evidence")
    path, probe = passed[-1]
    if probe.get("profile") != provider:
        fail(f"probe {path} profile mismatch: {probe.get('profile')} != {provider}")
    for key in ("repository", "registryHost", "tag", "testedAt"):
        if not probe.get(key):
            fail(f"probe {path} missing {key}")
    operations = set(probe.get("operations") or [])
    missing_operations = sorted(required_operations - operations)
    if missing_operations:
        fail(f"probe {path} missing operations: {', '.join(missing_operations)}")
    for key in ("configDigest", "layerDigest", "manifestDigest"):
        if not probe.get(key):
            fail(f"probe {path} missing {key}")

print(f"Hosted registry probes verified for: {', '.join(required)}")
PY
