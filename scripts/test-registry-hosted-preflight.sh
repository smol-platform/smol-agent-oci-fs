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

python_bin="$(command -v python3)"
if [[ -z "${python_bin}" ]]; then
	echo "python3 is required" >&2
	exit 2
fi

run_preflight() {
	local name="$1"
	local report="${tmp}/${name}.json"
	shift
	env \
		PATH="${tmp}/bin:${PATH}" \
		OSIX_HOSTED_REGISTRY_PREFLIGHT_ONLY=1 \
		OSIX_HOSTED_REGISTRY_PREFLIGHT_REPORT="${report}" \
		"$@" \
		"${repo_root}/scripts/test-registry-hosted-matrix.sh" \
		> "${tmp}/${name}.out" \
		2> "${tmp}/${name}.err"
	printf '%s' "${report}"
}

expect_preflight_failure() {
	local name="$1"
	local report="${tmp}/${name}.json"
	shift
	set +e
	env \
		PATH="${tmp}/bin:${PATH}" \
		OSIX_HOSTED_REGISTRY_PREFLIGHT_ONLY=1 \
		OSIX_HOSTED_REGISTRY_PREFLIGHT_REPORT="${report}" \
		"$@" \
		"${repo_root}/scripts/test-registry-hosted-matrix.sh" \
		> "${tmp}/${name}.out" \
		2> "${tmp}/${name}.err"
	local rc=$?
	set -e
	if [[ "${rc}" -ne 2 ]]; then
		echo "expected ${name} preflight to fail with exit 2, got ${rc}" >&2
		cat "${tmp}/${name}.out" >&2
		cat "${tmp}/${name}.err" >&2
		exit 1
	fi
	printf '%s' "${report}"
}

ecr_token_report="$(run_preflight ecr-token \
	OSIX_HOSTED_REGISTRY_REQUIRED=ecr \
	OSIX_HOSTED_REGISTRY_ECR_REPO=123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat \
	OSIX_REGISTRY_TOKEN=dummy)"
"${python_bin}" - "${ecr_token_report}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    report = json.load(f)
ecr = next(p for p in report["providers"] if p["provider"] == "ecr")
assert ecr["repositoryShapeValid"] is True, ecr
assert ecr["providerCLIs"]["aws"] is True, ecr
assert ecr["providerCLIIdentities"]["aws"]["available"] is True, ecr
assert ecr["providerCLIIdentities"]["aws"]["authenticated"] is False, ecr
assert ecr["readyToRun"] is True, ecr
assert ecr["missing"] == [], ecr
PY

bad_ecr_report="$(expect_preflight_failure bad-ecr-shape \
	OSIX_HOSTED_REGISTRY_REQUIRED=ecr \
	OSIX_HOSTED_REGISTRY_ECR_REPO=ghcr.io/wrong/osix \
	OSIX_REGISTRY_TOKEN=dummy)"
grep -q "repo-format:ACCOUNT.dkr.ecr.REGION.amazonaws.com/REPOSITORY" "${tmp}/bad-ecr-shape.err"
"${python_bin}" - "${bad_ecr_report}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    report = json.load(f)
ecr = next(p for p in report["providers"] if p["provider"] == "ecr")
assert ecr["repositoryShapeValid"] is False, ecr
assert ecr["readyToRun"] is False, ecr
assert "repo-format:ACCOUNT.dkr.ecr.REGION.amazonaws.com/REPOSITORY" in ecr["missing"], ecr
PY

docker_auth_config="${tmp}/docker-auth"
mkdir -p "${docker_auth_config}"
printf '{"auths":{"123456789012.dkr.ecr.us-east-1.amazonaws.com":{"auth":"dummy"}}}\n' \
	> "${docker_auth_config}/config.json"
ecr_docker_report="$(run_preflight ecr-docker-auth \
	DOCKER_CONFIG="${docker_auth_config}" \
	OSIX_HOSTED_REGISTRY_REQUIRED=ecr \
	OSIX_HOSTED_REGISTRY_ECR_REPO=123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat)"
"${python_bin}" - "${ecr_docker_report}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    report = json.load(f)
ecr = next(p for p in report["providers"] if p["provider"] == "ecr")
assert ecr["repositoryShapeValid"] is True, ecr
assert ecr["dockerAuthForHost"] is True, ecr
assert ecr["dockerCredentialsForHost"] is True, ecr
assert ecr["readyToRun"] is True, ecr
PY

suggestions_config="${tmp}/suggestions"
mkdir -p "${suggestions_config}"
printf '{"auths":{"123456789012.dkr.ecr.us-east-1.amazonaws.com":{"auth":"dummy"},"us-docker.pkg.dev":{"auth":"dummy"},"example.azurecr.io":{"auth":"dummy"}}}\n' \
	> "${suggestions_config}/config.json"
suggestions_report="$(run_preflight suggestions \
	DOCKER_CONFIG="${suggestions_config}")"
"${python_bin}" - "${suggestions_report}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    report = json.load(f)
by_provider = {p["provider"]: p for p in report["providers"]}
ecr = by_provider["ecr"]
gar = by_provider["gar"]
acr = by_provider["acr"]
assert ecr["credentialedRegistryHosts"] == ["123456789012.dkr.ecr.us-east-1.amazonaws.com"], ecr
assert ecr["suggestedRepositoryExports"] == ["export OSIX_HOSTED_REGISTRY_ECR_REPO=123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat"], ecr
assert gar["credentialedRegistryHosts"] == ["us-docker.pkg.dev"], gar
assert gar["suggestedRepositoryExports"] == ["export OSIX_HOSTED_REGISTRY_GAR_REPO=us-docker.pkg.dev/PROJECT/REPOSITORY/osix-compat"], gar
assert acr["credentialedRegistryHosts"] == ["example.azurecr.io"], acr
assert acr["suggestedRepositoryExports"] == ["export OSIX_HOSTED_REGISTRY_ACR_REPO=example.azurecr.io/osix-compat"], acr
PY

missing_helper_config="${tmp}/missing-helper"
mkdir -p "${missing_helper_config}"
printf '{"credHelpers":{"example.azurecr.io":"missinghelper"}}\n' \
	> "${missing_helper_config}/config.json"
acr_helper_report="$(expect_preflight_failure acr-missing-helper \
	DOCKER_CONFIG="${missing_helper_config}" \
	OSIX_HOSTED_REGISTRY_REQUIRED=acr \
	OSIX_HOSTED_REGISTRY_ACR_REPO=example.azurecr.io/osix-compat)"
grep -q "cli:docker-credential-missinghelper" "${tmp}/acr-missing-helper.err"
"${python_bin}" - "${acr_helper_report}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    report = json.load(f)
acr = next(p for p in report["providers"] if p["provider"] == "acr")
assert acr["repositoryShapeValid"] is True, acr
assert acr["providerCLIs"]["az"] is True, acr
assert acr["dockerCredentialHelperForHost"] is False, acr
assert acr["dockerCredentialsForHost"] is False, acr
assert "cli:docker-credential-missinghelper" in acr["missing"], acr
assert "registry-credentials" in acr["missing"], acr
PY

helper_bin="${tmp}/bin/docker-credential-teststore"
printf '#!/usr/bin/env sh\nexit 0\n' > "${helper_bin}"
chmod +x "${helper_bin}"
store_config="${tmp}/store-helper"
mkdir -p "${store_config}"
printf '{"credsStore":"teststore"}\n' > "${store_config}/config.json"
gar_store_report="$(run_preflight gar-store-helper \
	DOCKER_CONFIG="${store_config}" \
	OSIX_HOSTED_REGISTRY_REQUIRED=gar \
	OSIX_HOSTED_REGISTRY_GAR_REPO=us-docker.pkg.dev/project/repository/image)"
"${python_bin}" - "${gar_store_report}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    report = json.load(f)
gar = next(p for p in report["providers"] if p["provider"] == "gar")
assert gar["repositoryShapeValid"] is True, gar
assert gar["providerCLIs"]["gcloud"] is True, gar
assert gar["providerCLIIdentities"]["gcloud"]["available"] is True, gar
assert gar["dockerCredentialStoreForHost"] is True, gar
assert gar["dockerCredentialsForHost"] is True, gar
assert gar["readyToRun"] is True, gar
helpers = gar["credentialSources"]["dockerCredentialHelperExecutables"]
assert helpers["teststore"] is True, helpers
PY

echo "Hosted registry preflight smoke passed"
