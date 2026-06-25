#!/usr/bin/env bash
set -euo pipefail

run_dir="${1:-${OSIX_HOSTED_REGISTRY_OUTPUT_DIR:-}}"

if [[ -z "${run_dir}" ]]; then
	echo "usage: $0 RUN_DIR" >&2
	echo "or set OSIX_HOSTED_REGISTRY_OUTPUT_DIR" >&2
	exit 64
fi

python3 - "${run_dir}" <<'PY'
import glob
import json
import os
import re
import shlex
import sys

run_dir = sys.argv[1]

def load_json(path):
    if not os.path.exists(path):
        return None
    with open(path, "r", encoding="utf-8") as f:
        return json.load(f)

def provider_items(preflight):
    return {
        item.get("provider"): item
        for item in (preflight or {}).get("providers", [])
        if item.get("provider")
    }

def latest_provider_probe(probe_dir, provider):
    paths = sorted(glob.glob(os.path.join(probe_dir, f"{provider}-*.json")) + glob.glob(os.path.join(probe_dir, f"{provider}.json")))
    if not paths:
        return None, None
    path = paths[-1]
    return path, load_json(path)

def matrix_events(matrix):
    return {
        item.get("provider"): item
        for item in (matrix or {}).get("providers", [])
        if item.get("provider")
    }

def remediation(provider, failure_class):
    if failure_class == "registry-write-forbidden":
        if provider == "ecr":
            return "Grant ECR push permissions for the repository, refresh docker login with aws ecr get-login-password, then rerun the hosted runner."
        if provider == "gar":
            return "Grant Artifact Registry writer on the repository/image path, refresh gcloud docker auth, then rerun the hosted runner."
        if provider == "acr":
            return "Grant AcrPush on the registry/repository, refresh az acr login, then rerun the hosted runner."
        return "Grant registry push/write permission and refresh credentials, then rerun the hosted runner."
    if failure_class == "registry-unauthorized":
        return "Refresh registry credentials and confirm Docker or OSIx auth is scoped to the configured registry host."
    if failure_class == "registry-repository-missing":
        return "Create the configured repository/image path or fix the OSIX_HOSTED_REGISTRY_*_REPO value."
    return ""

def remediation_commands(provider, repo, failure_class):
    if failure_class not in {"registry-write-forbidden", "registry-unauthorized", "registry-repository-missing"}:
        return []
    if "/" not in repo or repo == "(unset)":
        return []
    host, repo_path = repo.split("/", 1)
    if provider == "ecr":
        match = re.match(r"^(?P<account>[0-9]{12})\.dkr\.ecr\.(?P<region>[a-z0-9-]+)\.amazonaws\.com(?:\.cn)?$", host)
        if not match:
            return []
        region = match.group("region")
        return [
            f"aws ecr describe-repositories --region {shlex.quote(region)} --repository-names {shlex.quote(repo_path)} || aws ecr create-repository --region {shlex.quote(region)} --repository-name {shlex.quote(repo_path)}",
            f"aws ecr get-login-password --region {shlex.quote(region)} | docker login --username AWS --password-stdin {shlex.quote(host)}",
            f"OSIX_HOSTED_REGISTRY_ECR_REPO={shlex.quote(repo)} ./scripts/run-hosted-registry-verification.sh --providers ecr --out-dir ./hosted-registry-run-ecr",
        ]
    if provider == "gar":
        parts = repo_path.split("/")
        if len(parts) < 3:
            return []
        project, repository = parts[0], parts[1]
        location = host.removesuffix("-docker.pkg.dev")
        return [
            f"gcloud artifacts repositories describe {shlex.quote(repository)} --location {shlex.quote(location)} --project {shlex.quote(project)} || gcloud artifacts repositories create {shlex.quote(repository)} --repository-format docker --location {shlex.quote(location)} --project {shlex.quote(project)}",
            f"gcloud auth configure-docker {shlex.quote(host)}",
            f"OSIX_HOSTED_REGISTRY_GAR_REPO={shlex.quote(repo)} ./scripts/run-hosted-registry-verification.sh --providers gar --out-dir ./hosted-registry-run-gar",
        ]
    if provider == "acr":
        registry = host.removesuffix(".azurecr.io")
        return [
            f"az acr login --name {shlex.quote(registry)}",
            f"OSIX_HOSTED_REGISTRY_ACR_REPO={shlex.quote(repo)} ./scripts/run-hosted-registry-verification.sh --providers acr --out-dir ./hosted-registry-run-acr",
        ]
    return []

run_report = load_json(os.path.join(run_dir, "run.json")) or {}
preflight = load_json(os.path.join(run_dir, "preflight.json")) or {}
matrix = load_json(os.path.join(run_dir, "matrix.json")) or {}
paths = run_report.get("paths") or {}
probe_dir = paths.get("probe") or os.path.join(run_dir, "probe")
log_dir = paths.get("logs") or os.path.join(run_dir, "logs")
providers = run_report.get("providers") or preflight.get("matrix") or []
preflight_by_provider = provider_items(preflight)
matrix_by_provider = matrix_events(matrix)

print("Hosted registry run summary")
print(f"Run: {run_dir}")
print(f"Result: {run_report.get('result', 'unknown')}")
print(f"Stage: {run_report.get('stage', 'unknown')}")
if run_report.get("message"):
    print(f"Message: {run_report['message']}")
print(f"Providers: {', '.join(providers) if providers else '(unknown)'}")
print("")

for provider in providers:
    item = preflight_by_provider.get(provider, {})
    repo = item.get("repository") or "(unset)"
    print(f"{provider}:")
    print(f"  repo: {repo}")
    print(f"  preflight: {'ready' if item.get('readyToRun') else 'not-ready'}")
    missing = item.get("missing") or []
    if missing:
        print(f"  missing: {', '.join(missing)}")
    identities = item.get("providerCLIIdentities") or {}
    for cli, identity in identities.items():
        state = "authenticated" if identity.get("authenticated") else "not-authenticated"
        detail = identity.get("account") or identity.get("project") or identity.get("subscriptionId") or identity.get("error") or ""
        suffix = f" ({detail})" if detail else ""
        print(f"  cli {cli}: {state}{suffix}")
    probe_path, probe = latest_provider_probe(probe_dir, provider)
    if probe:
        print(f"  probe: {probe.get('result', 'unknown')} ({probe_path})")
        if probe.get("failureClass"):
            print(f"  failure: {probe['failureClass']}")
        if probe.get("failureHint"):
            print(f"  hint: {probe['failureHint']}")
        if probe.get("logFile"):
            print(f"  probe log: {probe['logFile']}")
        fix = remediation(provider, probe.get("failureClass"))
        if fix:
            print(f"  next: {fix}")
        commands = remediation_commands(provider, repo, probe.get("failureClass"))
        if commands:
            print("  commands:")
            for command in commands:
                print(f"    {command}")
    else:
        print("  probe: missing")
    event = matrix_by_provider.get(provider)
    if event:
        print(f"  matrix: {event.get('result', 'unknown')}")
        if event.get("failureClass"):
            print(f"  matrix failure: {event['failureClass']}")
        if event.get("failureHint"):
            print(f"  matrix hint: {event['failureHint']}")
        if event.get("logFile"):
            print(f"  matrix log: {event['logFile']}")
    else:
        print("  matrix: not-run")
    probe_log = os.path.join(log_dir, f"{provider}-probe.log")
    if os.path.exists(probe_log):
        print(f"  runner probe log: {probe_log}")
    print("")
PY
