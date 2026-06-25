#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
providers="${OSIX_HOSTED_REGISTRY_MATRIX:-ghcr,ecr,gar,acr}"
required="${OSIX_HOSTED_REGISTRY_REQUIRED:-}"
preflight_only="${OSIX_HOSTED_REGISTRY_PREFLIGHT_ONLY:-}"
preflight_report="${OSIX_HOSTED_REGISTRY_PREFLIGHT_REPORT:-}"
matrix_report="${OSIX_HOSTED_REGISTRY_MATRIX_REPORT:-}"
hosted_registry_script="${OSIX_HOSTED_REGISTRY_TEST_SCRIPT:-${repo_root}/scripts/test-registry-hosted.sh}"
log_dir="${OSIX_HOSTED_REGISTRY_LOG_DIR:-}"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/osix-hosted-registry-matrix.XXXXXX")"
matrix_events="${tmp}/events.jsonl"

cleanup() {
	rm -rf "${tmp}"
}
trap cleanup EXIT

contains_provider() {
  local needle="$1"
  local list="$2"
  IFS=',' read -r -a items <<<"${list}"
  for item in "${items[@]}"; do
    item="$(echo "${item}" | xargs)"
    if [[ "${item}" == "${needle}" ]]; then
      return 0
    fi
  done
  return 1
}

repo_for_provider() {
	local provider="$1"
	local var=""
  case "${provider}" in
    ghcr) var="OSIX_HOSTED_REGISTRY_GHCR_REPO" ;;
    ecr) var="OSIX_HOSTED_REGISTRY_ECR_REPO" ;;
    gar) var="OSIX_HOSTED_REGISTRY_GAR_REPO" ;;
    acr) var="OSIX_HOSTED_REGISTRY_ACR_REPO" ;;
    *) var="" ;;
  esac
  if [[ -n "${var}" ]]; then
    printf '%s' "${!var:-}"
	fi
}

repo_var_for_provider() {
	local provider="$1"
	case "${provider}" in
		ghcr) echo "OSIX_HOSTED_REGISTRY_GHCR_REPO" ;;
		ecr) echo "OSIX_HOSTED_REGISTRY_ECR_REPO" ;;
		gar) echo "OSIX_HOSTED_REGISTRY_GAR_REPO" ;;
		acr) echo "OSIX_HOSTED_REGISTRY_ACR_REPO" ;;
		*) echo "OSIX_HOSTED_REGISTRY_${provider}_REPO" ;;
	esac
}

append_matrix_event() {
	local provider="$1"
	local repo="$2"
	local result="$3"
	local exit_code="$4"
	local message="$5"
	local required_provider="$6"
	local evidence_files="${7:-}"
	local log_file="${8:-}"
	local failure_class="${9:-}"
	local failure_hint="${10:-}"
	python3 - "${matrix_events}" "${provider}" "${repo}" "${result}" "${exit_code}" "${message}" "${required_provider}" "${evidence_files}" "${log_file}" "${failure_class}" "${failure_hint}" <<'PY'
import json
import sys
from datetime import datetime, timezone

path, provider, repo, result, exit_code, message, required_provider, evidence_files, log_file, failure_class, failure_hint = sys.argv[1:]
event = {
    "provider": provider,
    "repository": repo,
    "result": result,
    "exitCode": int(exit_code),
    "message": message,
    "required": required_provider == "true",
    "finishedAt": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
}
if evidence_files:
    event["evidenceFiles"] = [p for p in evidence_files.split("\n") if p]
if log_file:
    event["logFile"] = log_file
if failure_class:
    event["failureClass"] = failure_class
if failure_hint:
    event["failureHint"] = failure_hint
with open(path, "a", encoding="utf-8") as f:
    f.write(json.dumps(event, sort_keys=True))
    f.write("\n")
PY
}

snapshot_evidence_files() {
	local output="$1"
	local evidence_dir="${OSIX_HOSTED_REGISTRY_EVIDENCE_DIR:-}"
	if [[ -n "${evidence_dir}" && -d "${evidence_dir}" ]]; then
		find "${evidence_dir}" -type f | sort > "${output}"
	else
		: > "${output}"
	fi
}

new_evidence_files() {
	local before="$1"
	local evidence_dir="${OSIX_HOSTED_REGISTRY_EVIDENCE_DIR:-}"
	if [[ -z "${evidence_dir}" || ! -d "${evidence_dir}" ]]; then
		return 0
	fi
	python3 - "${before}" "${evidence_dir}" <<'PY'
import os
import sys

before_path, evidence_dir = sys.argv[1:]
with open(before_path, "r", encoding="utf-8") as f:
    before = {line.rstrip("\n") for line in f if line.rstrip("\n")}
current = []
for root, _, files in os.walk(evidence_dir):
    for name in files:
        current.append(os.path.join(root, name))
for path in sorted(set(current) - before):
    print(path)
PY
}

classify_provider_failure() {
	local provider="$1"
	local log_file="$2"
	if [[ -z "${log_file}" || ! -f "${log_file}" ]]; then
		printf '\t'
		return 0
	fi
	if grep -q "start blob upload .*403 Forbidden" "${log_file}"; then
		printf 'registry-write-forbidden\t%s registry rejected blob upload with 403 Forbidden; refresh credentials or grant repository write/push permissions.' "${provider}"
		return 0
	fi
	if grep -qi "401 Unauthorized\\|unauthorized" "${log_file}"; then
		printf 'registry-unauthorized\t%s registry authentication failed; refresh Docker or OSIx registry credentials.' "${provider}"
		return 0
	fi
	if grep -qi "repository .*not found\\|name unknown\\|404 Not Found" "${log_file}"; then
		printf 'registry-repository-missing\t%s repository was not found; create the repository or fix the OSIX_HOSTED_REGISTRY_*_REPO value.' "${provider}"
		return 0
	fi
	printf '\t'
}

write_matrix_report() {
	local result="$1"
	local exit_code="$2"
	local message="$3"
	if [[ -z "${matrix_report}" ]]; then
		return 0
	fi
	mkdir -p "$(dirname "${matrix_report}")"
	python3 - "${matrix_report}" "${matrix_events}" "${result}" "${exit_code}" "${message}" <<'PY'
import json
import os
import sys
from datetime import datetime, timezone

path, events_path, result, exit_code, message = sys.argv[1:]
providers = [p.strip() for p in os.environ.get("OSIX_HOSTED_REGISTRY_MATRIX", "ghcr,ecr,gar,acr").split(",") if p.strip()]
required = sorted(p.strip() for p in os.environ.get("OSIX_HOSTED_REGISTRY_REQUIRED", "").split(",") if p.strip())
events = []
if os.path.exists(events_path):
    with open(events_path, "r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if line:
                events.append(json.loads(line))
counts = {
    "passed": sum(1 for event in events if event.get("result") == "passed"),
    "skipped": sum(1 for event in events if event.get("result") == "skipped"),
    "failed": sum(1 for event in events if event.get("result") == "failed"),
}
report = {
    "generatedAt": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
    "matrix": providers,
    "required": required,
    "result": result,
    "exitCode": int(exit_code),
    "message": message,
    "counts": counts,
    "providers": events,
}
with open(path, "w", encoding="utf-8") as f:
    json.dump(report, f, indent=2, sort_keys=True)
    f.write("\n")
print(f"Wrote hosted registry matrix report {path}")
PY
}

finish_matrix() {
	local result="$1"
	local exit_code="$2"
	local message="$3"
	write_matrix_report "${result}" "${exit_code}" "${message}"
	if [[ "${exit_code}" -eq 0 ]]; then
		echo "${message}"
	else
		echo "${message}" >&2
	fi
	exit "${exit_code}"
}

write_preflight_report() {
	local report_path="$1"
	mkdir -p "$(dirname "${report_path}")"
	python3 - "${report_path}" <<'PY'
import json
import os
import re
import shutil
import subprocess
import sys
from datetime import datetime, timezone

path = sys.argv[1]
providers = [p.strip() for p in os.environ.get("OSIX_HOSTED_REGISTRY_MATRIX", "ghcr,ecr,gar,acr").split(",") if p.strip()]
required = {p.strip() for p in os.environ.get("OSIX_HOSTED_REGISTRY_REQUIRED", "").split(",") if p.strip()}
repo_vars = {
    "ghcr": "OSIX_HOSTED_REGISTRY_GHCR_REPO",
    "ecr": "OSIX_HOSTED_REGISTRY_ECR_REPO",
    "gar": "OSIX_HOSTED_REGISTRY_GAR_REPO",
    "acr": "OSIX_HOSTED_REGISTRY_ACR_REPO",
}
cli_by_provider = {
    "ghcr": [],
    "ecr": ["aws"],
    "gar": ["gcloud"],
    "acr": ["az"],
}
docker_config = os.path.join(os.environ.get("DOCKER_CONFIG", os.path.join(os.environ["HOME"], ".docker")), "config.json")
docker_config_data = {}
if os.path.exists(docker_config):
    try:
        with open(docker_config, "r", encoding="utf-8") as f:
            docker_config_data = json.load(f)
    except Exception:
        docker_config_data = {}
docker_auth_hosts = sorted(docker_config_data.get("auths", {}).keys())
docker_cred_helpers = docker_config_data.get("credHelpers", {})
docker_cred_helper_hosts = sorted(docker_cred_helpers.keys())
docker_creds_store = docker_config_data.get("credsStore", "")
helper_names = sorted({name for name in docker_cred_helpers.values() if name} | ({docker_creds_store} if docker_creds_store else set()))
helper_executables = {
    name: shutil.which(f"docker-credential-{name}") is not None
    for name in helper_names
}
credential_sources = {
    "osixRegistryToken": bool(os.environ.get("OSIX_REGISTRY_TOKEN")),
    "osixRegistryBasic": bool(os.environ.get("OSIX_REGISTRY_USERNAME") and os.environ.get("OSIX_REGISTRY_PASSWORD")),
    "dockerConfig": os.path.exists(docker_config),
    "dockerConfigPath": docker_config,
    "dockerAuthHosts": docker_auth_hosts,
    "dockerCredentialHelperHosts": docker_cred_helper_hosts,
    "dockerCredentialStore": docker_creds_store,
    "dockerCredentialHelperExecutables": helper_executables,
}
credentialed_hosts = sorted(set(docker_auth_hosts) | set(docker_cred_helper_hosts))

def command_identity(args, parser):
    if shutil.which(args[0]) is None:
        return {"available": False, "authenticated": False}
    try:
        proc = subprocess.run(
            args,
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=5,
        )
    except subprocess.TimeoutExpired:
        return {"available": True, "authenticated": False, "error": "timeout"}
    except Exception as exc:
        return {"available": True, "authenticated": False, "error": str(exc)}
    identity = {
        "available": True,
        "authenticated": False,
        "exitCode": proc.returncode,
    }
    if proc.returncode != 0:
        message = (proc.stderr or proc.stdout).strip()
        if message:
            identity["error"] = message.splitlines()[0]
        return identity
    try:
        parsed = parser(proc.stdout)
    except Exception as exc:
        identity["error"] = f"parse-error:{exc}"
        return identity
    identity.update(parsed)
    return identity

def aws_identity():
    def parse(stdout):
        payload = json.loads(stdout or "{}")
        return {
            "authenticated": bool(payload.get("Account")),
            "account": payload.get("Account", ""),
            "arn": payload.get("Arn", ""),
            "userId": payload.get("UserId", ""),
        }
    return command_identity(["aws", "sts", "get-caller-identity", "--output", "json"], parse)

def gcloud_identity():
    identity = command_identity(
        ["gcloud", "auth", "list", "--filter=status:ACTIVE", "--format=json"],
        lambda stdout: {
            "authenticated": bool(json.loads(stdout or "[]")),
            "accounts": [item.get("account", "") for item in json.loads(stdout or "[]") if item.get("account")],
        },
    )
    if not identity.get("available"):
        return identity
    project = command_identity(
        ["gcloud", "config", "get-value", "project"],
        lambda stdout: {
            "project": stdout.strip() if stdout.strip() and stdout.strip() != "(unset)" else "",
        },
    )
    if project.get("project"):
        identity["project"] = project["project"]
    return identity

def az_identity():
    def parse(stdout):
        payload = json.loads(stdout or "{}")
        return {
            "authenticated": bool(payload.get("id")),
            "subscriptionId": payload.get("id", ""),
            "tenantId": payload.get("tenantId", ""),
            "name": payload.get("name", ""),
        }
    return command_identity(["az", "account", "show", "--output", "json"], parse)

identity_by_cli = {
    "aws": aws_identity,
    "gcloud": gcloud_identity,
    "az": az_identity,
}

def repository_shape_error(provider, repo):
    if not repo:
        return None
    if "/" not in repo:
        return "repo-format:REGISTRY/REPOSITORY"
    registry_host, repo_path = repo.split("/", 1)
    if not registry_host or not repo_path:
        return "repo-format:REGISTRY/REPOSITORY"
    if provider == "ghcr" and registry_host != "ghcr.io":
        return "repo-format:ghcr.io/OWNER/REPOSITORY"
    if provider == "ecr" and not re.match(r"^[0-9]{12}\.dkr\.ecr\.[a-z0-9-]+\.amazonaws\.com(\.cn)?$", registry_host):
        return "repo-format:ACCOUNT.dkr.ecr.REGION.amazonaws.com/REPOSITORY"
    if provider == "gar" and (not registry_host.endswith(".pkg.dev") or len(repo_path.split("/")) < 3):
        return "repo-format:REGION-docker.pkg.dev/PROJECT/REPOSITORY/IMAGE"
    if provider == "acr" and not registry_host.endswith(".azurecr.io"):
        return "repo-format:REGISTRY.azurecr.io/REPOSITORY"
    return None

def provider_for_host(host):
    if host == "ghcr.io":
        return "ghcr"
    if re.match(r"^[0-9]{12}\.dkr\.ecr\.[a-z0-9-]+\.amazonaws\.com(\.cn)?$", host):
        return "ecr"
    if host.endswith(".pkg.dev"):
        return "gar"
    if host.endswith(".azurecr.io"):
        return "acr"
    return ""

def suggested_export(provider, host):
    if provider == "ghcr":
        return f"export OSIX_HOSTED_REGISTRY_GHCR_REPO={host}/OWNER/osix-compat"
    if provider == "ecr":
        return f"export OSIX_HOSTED_REGISTRY_ECR_REPO={host}/osix-compat"
    if provider == "gar":
        return f"export OSIX_HOSTED_REGISTRY_GAR_REPO={host}/PROJECT/REPOSITORY/osix-compat"
    if provider == "acr":
        return f"export OSIX_HOSTED_REGISTRY_ACR_REPO={host}/osix-compat"
    return ""

items = []
for provider in providers:
    var = repo_vars.get(provider, f"OSIX_HOSTED_REGISTRY_{provider.upper()}_REPO")
    repo = os.environ.get(var, "")
    registry_host = repo.split("/", 1)[0] if "/" in repo else ""
    helper = docker_cred_helpers.get(registry_host, "")
    helper_missing = bool(helper and not helper_executables.get(helper, False))
    store_missing = bool(docker_creds_store and not helper_executables.get(docker_creds_store, False))
    docker_auth_for_host = registry_host in docker_auth_hosts
    docker_helper_for_host = bool(helper and not helper_missing)
    docker_store_for_host = bool(docker_creds_store and not store_missing)
    docker_credentials_for_host = bool(
        registry_host
        and (
            docker_auth_for_host
            or docker_helper_for_host
            or docker_store_for_host
        )
    )
    missing_cli = [name for name in cli_by_provider.get(provider, []) if shutil.which(name) is None]
    shape_error = repository_shape_error(provider, repo)
    missing = []
    if not repo:
        missing.append(var)
    elif shape_error:
        missing.append(shape_error)
    if missing_cli:
        missing.extend(f"cli:{name}" for name in missing_cli)
    if helper_missing and not docker_auth_for_host:
        missing.append(f"cli:docker-credential-{helper}")
    if store_missing and not (docker_auth_for_host or docker_helper_for_host):
        missing.append(f"cli:docker-credential-{docker_creds_store}")
    if not (credential_sources["osixRegistryToken"] or credential_sources["osixRegistryBasic"] or docker_credentials_for_host):
        missing.append("registry-credentials")
    credentialed_registry_hosts = [
        host for host in credentialed_hosts
        if provider_for_host(host) == provider
    ]
    suggested_repository_exports = [
        suggestion for suggestion in (
            suggested_export(provider, host) for host in credentialed_registry_hosts
        )
        if suggestion
    ]
    provider_clis = {name: shutil.which(name) is not None for name in cli_by_provider.get(provider, [])}
    provider_cli_identities = {
        name: identity_by_cli[name]()
        for name in cli_by_provider.get(provider, [])
        if name in identity_by_cli
    }
    items.append({
        "provider": provider,
        "required": provider in required,
        "repositoryVariable": var,
        "repositoryConfigured": bool(repo),
        "repository": repo,
        "registryHost": registry_host,
        "repositoryShapeValid": bool(repo) and not shape_error,
        "providerCLIs": provider_clis,
        "providerCLIIdentities": provider_cli_identities,
        "credentialSources": credential_sources,
        "dockerAuthForHost": docker_auth_for_host,
        "dockerCredentialHelperForHost": docker_helper_for_host,
        "dockerCredentialStoreForHost": docker_store_for_host,
        "dockerCredentialsForHost": docker_credentials_for_host,
        "credentialedRegistryHosts": credentialed_registry_hosts,
        "suggestedRepositoryExports": [] if repo else suggested_repository_exports,
        "readyToRun": bool(repo) and not shape_error and not missing_cli and (
            credential_sources["osixRegistryToken"]
            or credential_sources["osixRegistryBasic"]
            or docker_credentials_for_host
        ),
        "missing": missing,
    })
report = {
    "generatedAt": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
    "matrix": providers,
    "required": sorted(required),
    "providers": items,
}
with open(path, "w", encoding="utf-8") as f:
    json.dump(report, f, indent=2, sort_keys=True)
    f.write("\n")
print(f"Wrote hosted registry preflight report {path}")
PY
}

validate_required_preflight() {
	local report_path="$1"
	python3 - "${report_path}" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, "r", encoding="utf-8") as f:
    report = json.load(f)

failed = []
for item in report.get("providers", []):
    if item.get("required") and not item.get("readyToRun"):
        missing = ", ".join(item.get("missing", [])) or "unknown"
        failed.append(f"{item.get('provider')}: {missing}")

if failed:
    print("required hosted registry providers are not ready:", file=sys.stderr)
    for line in failed:
        print(f"  {line}", file=sys.stderr)
    sys.exit(2)
PY
}

append_required_preflight_failures() {
	local report_path="$1"
	python3 - "${report_path}" "${matrix_events}" <<'PY'
import json
import sys
from datetime import datetime, timezone

report_path, events_path = sys.argv[1:]
with open(report_path, "r", encoding="utf-8") as f:
    report = json.load(f)

now = datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
with open(events_path, "a", encoding="utf-8") as events:
    for item in report.get("providers", []):
        if not item.get("required") or item.get("readyToRun"):
            continue
        missing = ", ".join(item.get("missing", [])) or "unknown"
        event = {
            "provider": item.get("provider", ""),
            "repository": item.get("repository", ""),
            "result": "failed",
            "exitCode": 2,
            "message": f"required hosted registry provider {item.get('provider', '')} is not ready: {missing}",
            "required": True,
            "finishedAt": now,
        }
        events.write(json.dumps(event, sort_keys=True))
        events.write("\n")
PY
}

if [[ -n "${preflight_report}" ]]; then
	write_preflight_report "${preflight_report}"
fi

if [[ -n "${preflight_only}" ]]; then
	if [[ -z "${preflight_report}" ]]; then
		preflight_report="$(mktemp "${TMPDIR:-/tmp}/osix-hosted-registry-preflight.XXXXXX.json")"
		write_preflight_report "${preflight_report}"
	fi
	if [[ -n "${required}" ]]; then
		validate_required_preflight "${preflight_report}"
	fi
	echo "Hosted registry preflight completed"
	exit 0
fi

if [[ -n "${required}" ]]; then
	if [[ -z "${preflight_report}" ]]; then
		preflight_report="${tmp}/preflight.json"
		write_preflight_report "${preflight_report}"
	fi
	if ! validate_required_preflight "${preflight_report}"; then
		append_required_preflight_failures "${preflight_report}"
		finish_matrix "failed" "2" "Required hosted registry providers are not ready"
	fi
fi

ran=0
skipped=0
IFS=',' read -r -a provider_list <<<"${providers}"
for provider in "${provider_list[@]}"; do
  provider="$(echo "${provider}" | xargs)"
  if [[ -z "${provider}" ]]; then
    continue
	fi
	repo="$(repo_for_provider "${provider}")"
	if [[ -z "${repo}" ]]; then
		if [[ -n "${required}" ]] && contains_provider "${provider}" "${required}"; then
			message="required hosted registry provider ${provider} is missing its $(repo_var_for_provider "${provider}") variable"
			append_matrix_event "${provider}" "" "failed" "2" "${message}" "true"
			finish_matrix "failed" "2" "${message}"
		fi
    message="Skipping hosted registry provider ${provider}: no repository configured"
    echo "${message}" >&2
    append_matrix_event "${provider}" "" "skipped" "0" "${message}" "false"
    skipped=$((skipped + 1))
    continue
  fi
  echo "Running hosted registry provider ${provider}: ${repo}"
  evidence_before="${tmp}/${provider}-evidence-before.txt"
  snapshot_evidence_files "${evidence_before}"
  provider_log=""
  if [[ -n "${log_dir}" ]]; then
    mkdir -p "${log_dir}"
    provider_log="${log_dir}/${provider}.log"
  fi
  set +e
  if [[ -n "${provider_log}" ]]; then
    OSIX_HOSTED_REGISTRY_PROVIDER="${provider}" \
    OSIX_HOSTED_REGISTRY_REPO="${repo}" \
      "${hosted_registry_script}" 2>&1 | tee "${provider_log}"
    rc=${PIPESTATUS[0]}
  else
    OSIX_HOSTED_REGISTRY_PROVIDER="${provider}" \
    OSIX_HOSTED_REGISTRY_REPO="${repo}" \
      "${hosted_registry_script}"
    rc=$?
  fi
  set -e
  if [[ "${rc}" -ne 0 ]]; then
    message="Hosted registry provider ${provider} failed with exit ${rc}"
    failure_class=""
    failure_hint=""
    if [[ -n "${provider_log}" ]]; then
      failure_summary="$(classify_provider_failure "${provider}" "${provider_log}")"
      failure_class="${failure_summary%%$'\t'*}"
      failure_hint="${failure_summary#*$'\t'}"
      if [[ "${failure_hint}" == "${failure_summary}" ]]; then
        failure_hint=""
      fi
    fi
    append_matrix_event "${provider}" "${repo}" "failed" "${rc}" "${message}" "$(if [[ -n "${required}" ]] && contains_provider "${provider}" "${required}"; then echo true; else echo false; fi)" "" "${provider_log}" "${failure_class}" "${failure_hint}"
    finish_matrix "failed" "${rc}" "${message}"
  fi
  evidence_files="$(new_evidence_files "${evidence_before}")"
  append_matrix_event "${provider}" "${repo}" "passed" "0" "Hosted registry provider ${provider} passed" "$(if [[ -n "${required}" ]] && contains_provider "${provider}" "${required}"; then echo true; else echo false; fi)" "${evidence_files}" "${provider_log}"
  ran=$((ran + 1))
done

if [[ "${ran}" -eq 0 ]]; then
  finish_matrix "failed" "2" "No hosted registry providers ran; set OSIX_HOSTED_REGISTRY_*_REPO variables"
fi

finish_matrix "passed" "0" "Hosted registry matrix completed: ran=${ran} skipped=${skipped}"
