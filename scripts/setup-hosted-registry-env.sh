#!/usr/bin/env bash
set -euo pipefail

provider="all"
repo_name="${OSIX_HOSTED_REGISTRY_REPO_NAME:-osix-compat}"
ecr_region="${OSIX_HOSTED_REGISTRY_ECR_REGION:-${AWS_REGION:-${AWS_DEFAULT_REGION:-us-east-1}}}"
ecr_account="${OSIX_HOSTED_REGISTRY_ECR_ACCOUNT:-}"
gar_location="${OSIX_HOSTED_REGISTRY_GAR_LOCATION:-us}"
gar_project="${OSIX_HOSTED_REGISTRY_GAR_PROJECT:-}"
gar_repository="${OSIX_HOSTED_REGISTRY_GAR_REPOSITORY:-osix-compat}"
gar_image="${OSIX_HOSTED_REGISTRY_GAR_IMAGE:-${repo_name}}"
acr_registry="${OSIX_HOSTED_REGISTRY_ACR_REGISTRY:-}"
acr_repository="${OSIX_HOSTED_REGISTRY_ACR_REPOSITORY:-${repo_name}}"
acr_resource_group="${OSIX_HOSTED_REGISTRY_ACR_RESOURCE_GROUP:-}"
acr_location="${OSIX_HOSTED_REGISTRY_ACR_LOCATION:-eastus}"
acr_sku="${OSIX_HOSTED_REGISTRY_ACR_SKU:-Basic}"
print_commands=1
create=0
login=0
env_file=""

usage() {
	cat <<'EOF'
Usage: setup-hosted-registry-env.sh [options]

Prints OSIX_HOSTED_REGISTRY_*_REPO exports for live ECR/GAR/ACR verification.
By default it does not create cloud resources or run docker login.

Options:
  --provider ecr|gar|acr|all
  --repo-name NAME                 Default: osix-compat
  --ecr-region REGION             Default: AWS_REGION/AWS_DEFAULT_REGION/us-east-1
  --ecr-account ACCOUNT           Default: aws sts get-caller-identity
  --gar-location LOCATION         Default: us
  --gar-project PROJECT           Default: gcloud config get-value project
  --gar-repository REPOSITORY     Default: osix-compat
  --gar-image IMAGE               Default: repo-name
  --acr-registry REGISTRY         Azure registry name, without .azurecr.io
  --acr-repository REPOSITORY     Default: repo-name
  --acr-resource-group GROUP      Required for --create with ACR
  --acr-location LOCATION         Default: eastus
  --acr-sku Basic|Standard|Premium Default: Basic
  --create                        Create/ensure supported provider repositories
  --login                         Run Docker-compatible provider login commands
  --env-file PATH                 Also write sourceable export lines to PATH
  --help
EOF
}

while [[ "$#" -gt 0 ]]; do
	case "$1" in
		--provider)
			provider="${2:?missing --provider value}"
			shift 2
			;;
		--repo-name)
			repo_name="${2:?missing --repo-name value}"
			gar_image="${repo_name}"
			acr_repository="${repo_name}"
			shift 2
			;;
		--ecr-region)
			ecr_region="${2:?missing --ecr-region value}"
			shift 2
			;;
		--ecr-account)
			ecr_account="${2:?missing --ecr-account value}"
			shift 2
			;;
		--gar-location)
			gar_location="${2:?missing --gar-location value}"
			shift 2
			;;
		--gar-project)
			gar_project="${2:?missing --gar-project value}"
			shift 2
			;;
		--gar-repository)
			gar_repository="${2:?missing --gar-repository value}"
			shift 2
			;;
		--gar-image)
			gar_image="${2:?missing --gar-image value}"
			shift 2
			;;
		--acr-registry)
			acr_registry="${2:?missing --acr-registry value}"
			shift 2
			;;
		--acr-repository)
			acr_repository="${2:?missing --acr-repository value}"
			shift 2
			;;
		--acr-resource-group)
			acr_resource_group="${2:?missing --acr-resource-group value}"
			shift 2
			;;
		--acr-location)
			acr_location="${2:?missing --acr-location value}"
			shift 2
			;;
		--acr-sku)
			acr_sku="${2:?missing --acr-sku value}"
			shift 2
			;;
		--create)
			create=1
			shift
			;;
		--login)
			login=1
			shift
			;;
		--env-file)
			env_file="${2:?missing --env-file value}"
			shift 2
			;;
		--help|-h)
			usage
			exit 0
			;;
		*)
			echo "unknown option: $1" >&2
			usage >&2
			exit 64
			;;
	esac
done

case "${provider}" in
	ecr|gar|acr|all) ;;
	*)
		echo "--provider must be ecr, gar, acr, or all" >&2
		exit 64
		;;
esac

have_provider() {
	local candidate="$1"
	[[ "${provider}" == "all" || "${provider}" == "${candidate}" ]]
}

shell_quote() {
	python3 - "$1" <<'PY'
import shlex
import sys
print(shlex.quote(sys.argv[1]))
PY
}

require_cmd() {
	local cmd="$1"
	if ! command -v "${cmd}" >/dev/null 2>&1; then
		echo "missing required command: ${cmd}" >&2
		exit 2
	fi
}

emit_export() {
	local key="$1"
	local value="$2"
	local line
	line="$(printf 'export %s=%s' "${key}" "$(shell_quote "${value}")")"
	printf '%s\n' "${line}"
	if [[ -n "${env_file}" ]]; then
		printf '%s\n' "${line}" >> "${env_file}"
	fi
}

run_or_print() {
	if [[ "${print_commands}" -eq 1 ]]; then
		printf '# %s\n' "$*"
	else
		"$@"
	fi
}

if [[ -n "${env_file}" ]]; then
	mkdir -p "$(dirname "${env_file}")"
	: > "${env_file}"
fi

if have_provider ecr; then
	require_cmd aws
	if [[ -z "${ecr_account}" ]]; then
		ecr_account="$(aws sts get-caller-identity --query Account --output text)"
	fi
	ecr_repo="${ecr_account}.dkr.ecr.${ecr_region}.amazonaws.com/${repo_name}"
	if [[ "${create}" -eq 1 ]]; then
		aws ecr describe-repositories --region "${ecr_region}" --repository-names "${repo_name}" >/dev/null 2>&1 \
			|| aws ecr create-repository --region "${ecr_region}" --repository-name "${repo_name}" >/dev/null
	else
		run_or_print aws ecr create-repository --region "${ecr_region}" --repository-name "${repo_name}"
	fi
	if [[ "${login}" -eq 1 ]]; then
		aws ecr get-login-password --region "${ecr_region}" \
			| docker login --username AWS --password-stdin "${ecr_account}.dkr.ecr.${ecr_region}.amazonaws.com"
	else
		printf '# aws ecr get-login-password --region %s | docker login --username AWS --password-stdin %s\n' \
			"$(shell_quote "${ecr_region}")" \
			"$(shell_quote "${ecr_account}.dkr.ecr.${ecr_region}.amazonaws.com")"
	fi
	emit_export OSIX_HOSTED_REGISTRY_ECR_REPO "${ecr_repo}"
fi

if have_provider gar; then
	require_cmd gcloud
	if [[ -z "${gar_project}" ]]; then
		gar_project="$(gcloud config get-value project 2>/dev/null)"
	fi
	if [[ -z "${gar_project}" || "${gar_project}" == "(unset)" ]]; then
		echo "GAR project is required; pass --gar-project or set OSIX_HOSTED_REGISTRY_GAR_PROJECT" >&2
		exit 2
	fi
	gar_host="${gar_location}-docker.pkg.dev"
	gar_repo="${gar_host}/${gar_project}/${gar_repository}/${gar_image}"
	if [[ "${create}" -eq 1 ]]; then
		gcloud artifacts repositories describe "${gar_repository}" --location "${gar_location}" --project "${gar_project}" >/dev/null 2>&1 \
			|| gcloud artifacts repositories create "${gar_repository}" --repository-format docker --location "${gar_location}" --project "${gar_project}" >/dev/null
	else
		run_or_print gcloud artifacts repositories create "${gar_repository}" --repository-format docker --location "${gar_location}" --project "${gar_project}"
	fi
	if [[ "${login}" -eq 1 ]]; then
		gcloud auth configure-docker "${gar_host}" --quiet
	else
		printf '# gcloud auth configure-docker %s\n' "$(shell_quote "${gar_host}")"
	fi
	emit_export OSIX_HOSTED_REGISTRY_GAR_REPO "${gar_repo}"
fi

if have_provider acr; then
	require_cmd az
	if [[ -z "${acr_registry}" ]]; then
		echo "ACR registry is required; pass --acr-registry or set OSIX_HOSTED_REGISTRY_ACR_REGISTRY" >&2
		exit 2
	fi
	case "${acr_sku}" in
		Basic|Standard|Premium) ;;
		*)
			echo "--acr-sku must be Basic, Standard, or Premium" >&2
			exit 64
			;;
	esac
	acr_repo="${acr_registry}.azurecr.io/${acr_repository}"
	if [[ "${create}" -eq 1 ]]; then
		if [[ -z "${acr_resource_group}" ]]; then
			echo "ACR resource group is required for --create; pass --acr-resource-group or set OSIX_HOSTED_REGISTRY_ACR_RESOURCE_GROUP" >&2
			exit 2
		fi
		az acr show --name "${acr_registry}" --resource-group "${acr_resource_group}" >/dev/null 2>&1 \
			|| az acr create --resource-group "${acr_resource_group}" --name "${acr_registry}" --sku "${acr_sku}" --location "${acr_location}" >/dev/null
	elif [[ -n "${acr_resource_group}" ]]; then
		run_or_print az acr create --resource-group "${acr_resource_group}" --name "${acr_registry}" --sku "${acr_sku}" --location "${acr_location}"
	else
		printf '# az acr create --resource-group RESOURCE_GROUP --name %s --sku %s --location %s\n' \
			"$(shell_quote "${acr_registry}")" \
			"$(shell_quote "${acr_sku}")" \
			"$(shell_quote "${acr_location}")"
	fi
	if [[ "${login}" -eq 1 ]]; then
		az acr login --name "${acr_registry}" >/dev/null
	else
		printf '# az acr login --name %s\n' "$(shell_quote "${acr_registry}")"
	fi
	emit_export OSIX_HOSTED_REGISTRY_ACR_REPO "${acr_repo}"
fi

case "${provider}" in
	all) required_providers="ecr,gar,acr" ;;
	*) required_providers="${provider}" ;;
esac
emit_export OSIX_HOSTED_REGISTRY_REQUIRED "${required_providers}"
printf '# Run: OSIX_HOSTED_REGISTRY_EVIDENCE_DIR=./hosted-registry-evidence OSIX_HOSTED_REGISTRY_MATRIX_REPORT=./hosted-registry-matrix.json ./scripts/test-registry-hosted-matrix.sh\n'
printf '# Then verify: ./scripts/verify-hosted-registry-evidence.sh ./hosted-registry-matrix.json ecr,gar,acr\n'
