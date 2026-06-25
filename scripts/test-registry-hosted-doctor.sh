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

ready_report="${tmp}/ready.json"
PATH="${tmp}/bin:${PATH}" \
OSIX_HOSTED_REGISTRY_ECR_REPO=123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat \
OSIX_REGISTRY_TOKEN=dummy \
	"${repo_root}/scripts/doctor-hosted-registry.sh" \
	--providers ecr \
	--required ecr \
	--report "${ready_report}" \
	> "${tmp}/ready.out"
grep -q "Hosted registry readiness" "${tmp}/ready.out"
grep -q -- "- ecr: ready" "${tmp}/ready.out"
grep -q "repo: 123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat" "${tmp}/ready.out"
grep -q "cli aws: not-authenticated" "${tmp}/ready.out"
test -f "${ready_report}"

docker_config="${tmp}/docker"
mkdir -p "${docker_config}"
printf '{"auths":{"123456789012.dkr.ecr.us-east-1.amazonaws.com":{"auth":"dummy"}}}\n' \
	> "${docker_config}/config.json"
not_ready_report="${tmp}/not-ready.json"
set +e
PATH="${tmp}/bin:${PATH}" \
DOCKER_CONFIG="${docker_config}" \
	"${repo_root}/scripts/doctor-hosted-registry.sh" \
	--providers ecr \
	--required ecr \
	--report "${not_ready_report}" \
	> "${tmp}/not-ready.out" \
	2> "${tmp}/not-ready.err"
rc=$?
set -e
if [[ "${rc}" -ne 2 ]]; then
	echo "expected not-ready doctor to exit 2, got ${rc}" >&2
	cat "${tmp}/not-ready.out" >&2
	cat "${tmp}/not-ready.err" >&2
	exit 1
fi
grep -q "! ecr: not-ready" "${tmp}/not-ready.out"
grep -q "missing: OSIX_HOSTED_REGISTRY_ECR_REPO" "${tmp}/not-ready.out"
grep -q "credentialed hosts: 123456789012.dkr.ecr.us-east-1.amazonaws.com" "${tmp}/not-ready.out"
grep -q "cli aws: not-authenticated" "${tmp}/not-ready.out"
grep -q "suggestion: export OSIX_HOSTED_REGISTRY_ECR_REPO=123456789012.dkr.ecr.us-east-1.amazonaws.com/osix-compat" "${tmp}/not-ready.out"
grep -q "Required providers not ready: ecr" "${tmp}/not-ready.out"
test -f "${not_ready_report}"

echo "Hosted registry doctor smoke passed"
