#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

mkdir -p "${tmp}/bin"

cat > "${tmp}/bin/aws" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == "sts get-caller-identity --query Account --output text" ]]; then
	echo "123456789012"
	exit 0
fi
echo "unexpected aws args: $*" >&2
exit 99
SH

cat > "${tmp}/bin/gcloud" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == "config get-value project" ]]; then
	echo "demo-project"
	exit 0
fi
echo "unexpected gcloud args: $*" >&2
exit 99
SH

cat > "${tmp}/bin/az" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == "acr show --name osixregistry --resource-group rg-osix" ]]; then
	exit 3
fi
if [[ "$*" == "acr create --resource-group rg-osix --name osixregistry --sku Standard --location westus2" ]]; then
	echo "created acr"
	exit 0
fi
echo "unexpected az args: $*" >&2
exit 99
SH

chmod +x "${tmp}/bin/aws" "${tmp}/bin/gcloud" "${tmp}/bin/az"

PATH="${tmp}/bin:${PATH}" \
	"${repo_root}/scripts/setup-hosted-registry-env.sh" \
	--provider ecr \
	--repo-name osix-live \
	--ecr-region us-west-2 \
	--env-file "${tmp}/ecr-source.env" \
	> "${tmp}/ecr.env"
grep -q "export OSIX_HOSTED_REGISTRY_ECR_REPO=123456789012.dkr.ecr.us-west-2.amazonaws.com/osix-live" "${tmp}/ecr.env"
grep -q "export OSIX_HOSTED_REGISTRY_REQUIRED=ecr" "${tmp}/ecr.env"
grep -q "# aws ecr get-login-password --region us-west-2" "${tmp}/ecr.env"
grep -q "export OSIX_HOSTED_REGISTRY_ECR_REPO=123456789012.dkr.ecr.us-west-2.amazonaws.com/osix-live" "${tmp}/ecr-source.env"
grep -q "export OSIX_HOSTED_REGISTRY_REQUIRED=ecr" "${tmp}/ecr-source.env"

PATH="${tmp}/bin:${PATH}" \
	"${repo_root}/scripts/setup-hosted-registry-env.sh" \
	--provider gar \
	--gar-location us-central1 \
	--gar-repository osix-repo \
	--gar-image osix-image \
	> "${tmp}/gar.env"
grep -q "export OSIX_HOSTED_REGISTRY_GAR_REPO=us-central1-docker.pkg.dev/demo-project/osix-repo/osix-image" "${tmp}/gar.env"
grep -q "export OSIX_HOSTED_REGISTRY_REQUIRED=gar" "${tmp}/gar.env"
grep -q "# gcloud auth configure-docker us-central1-docker.pkg.dev" "${tmp}/gar.env"

PATH="${tmp}/bin:${PATH}" \
	"${repo_root}/scripts/setup-hosted-registry-env.sh" \
	--provider acr \
	--acr-registry osixregistry \
	--acr-repository osix-live \
	--acr-resource-group rg-osix \
	--acr-location westus2 \
	--acr-sku Standard \
	> "${tmp}/acr.env"
grep -q "export OSIX_HOSTED_REGISTRY_ACR_REPO=osixregistry.azurecr.io/osix-live" "${tmp}/acr.env"
grep -q "export OSIX_HOSTED_REGISTRY_REQUIRED=acr" "${tmp}/acr.env"
grep -q "# az acr create --resource-group rg-osix --name osixregistry --sku Standard --location westus2" "${tmp}/acr.env"
grep -q "# az acr login --name osixregistry" "${tmp}/acr.env"

PATH="${tmp}/bin:${PATH}" \
	"${repo_root}/scripts/setup-hosted-registry-env.sh" \
	--provider acr \
	--acr-registry osixregistry \
	--acr-repository osix-live \
	--acr-resource-group rg-osix \
	--acr-location westus2 \
	--acr-sku Standard \
	--create \
	> "${tmp}/acr-create.env"
grep -q "export OSIX_HOSTED_REGISTRY_ACR_REPO=osixregistry.azurecr.io/osix-live" "${tmp}/acr-create.env"

PATH="${tmp}/bin:${PATH}" \
	"${repo_root}/scripts/setup-hosted-registry-env.sh" \
	--provider all \
	--repo-name osix-live \
	--ecr-account 210987654321 \
	--ecr-region us-east-2 \
	--gar-project other-project \
	--gar-location europe-west1 \
	--gar-repository osix-repo \
	--acr-registry osixregistry \
	> "${tmp}/all.env"
grep -q "export OSIX_HOSTED_REGISTRY_ECR_REPO=210987654321.dkr.ecr.us-east-2.amazonaws.com/osix-live" "${tmp}/all.env"
grep -q "export OSIX_HOSTED_REGISTRY_GAR_REPO=europe-west1-docker.pkg.dev/other-project/osix-repo/osix-live" "${tmp}/all.env"
grep -q "export OSIX_HOSTED_REGISTRY_ACR_REPO=osixregistry.azurecr.io/osix-live" "${tmp}/all.env"
grep -q "export OSIX_HOSTED_REGISTRY_REQUIRED=ecr,gar,acr" "${tmp}/all.env"

set +e
PATH="${tmp}/bin:${PATH}" \
	"${repo_root}/scripts/setup-hosted-registry-env.sh" \
	--provider acr \
	> "${tmp}/acr-missing.out" \
	2> "${tmp}/acr-missing.err"
rc=$?
set -e
if [[ "${rc}" -ne 2 ]]; then
	echo "expected missing ACR registry to exit 2, got ${rc}" >&2
	exit 1
fi
grep -q "ACR registry is required" "${tmp}/acr-missing.err"

set +e
PATH="${tmp}/bin:${PATH}" \
	"${repo_root}/scripts/setup-hosted-registry-env.sh" \
	--provider acr \
	--acr-registry osixregistry \
	--create \
	> "${tmp}/acr-create-missing-rg.out" \
	2> "${tmp}/acr-create-missing-rg.err"
rc=$?
set -e
if [[ "${rc}" -ne 2 ]]; then
	echo "expected missing ACR resource group to exit 2, got ${rc}" >&2
	exit 1
fi
grep -q "ACR resource group is required" "${tmp}/acr-create-missing-rg.err"

set +e
PATH="${tmp}/bin:${PATH}" \
	"${repo_root}/scripts/setup-hosted-registry-env.sh" \
	--provider acr \
	--acr-registry osixregistry \
	--acr-sku Developer \
	> "${tmp}/acr-bad-sku.out" \
	2> "${tmp}/acr-bad-sku.err"
rc=$?
set -e
if [[ "${rc}" -ne 64 ]]; then
	echo "expected invalid ACR SKU to exit 64, got ${rc}" >&2
	exit 1
fi
grep -q -- "--acr-sku must be Basic, Standard, or Premium" "${tmp}/acr-bad-sku.err"

echo "Hosted registry setup smoke passed"
