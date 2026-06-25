#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version="${OSIX_RELEASE_VERSION:-${1:-}}"
registry="${OSIX_RELEASE_REGISTRY:-ghcr.io/smol-platform}"
platform="${OSIX_RELEASE_PLATFORM:-linux/amd64}"
push="${OSIX_RELEASE_PUSH:-true}"

if [ -z "${version}" ]; then
	version="$(git -C "${repo_root}" describe --tags --dirty --always 2>/dev/null || true)"
fi
if [ -z "${version}" ]; then
	echo "OSIX_RELEASE_VERSION or a version argument is required" >&2
	exit 2
fi

for bin in docker; do
	if ! command -v "${bin}" >/dev/null 2>&1; then
		echo "${bin} is required" >&2
		exit 2
	fi
done

operator_image="${registry}/smol-agent-oci-fs-operator:${version}"
csi_image="${registry}/smol-agent-oci-fs-csi:${version}"
push_flag=()
if [ "${push}" = "true" ]; then
	push_flag=(--push)
else
	push_flag=(--load)
fi

docker buildx build \
	--platform "${platform}" \
	-f "${repo_root}/Dockerfile.operator" \
	-t "${operator_image}" \
	"${push_flag[@]}" \
	"${repo_root}"

docker buildx build \
	--platform "${platform}" \
	-f "${repo_root}/Dockerfile.csi" \
	-t "${csi_image}" \
	"${push_flag[@]}" \
	"${repo_root}"

cat <<EOF
OSIX_K8S_IMAGES_RELEASED
operator=${operator_image}
csi=${csi_image}
platform=${platform}
push=${push}

Kustomize override:
images:
  - name: ghcr.io/smol-platform/smol-agent-oci-fs-operator
    newName: ${registry}/smol-agent-oci-fs-operator
    newTag: ${version}
  - name: ghcr.io/smol-platform/smol-agent-oci-fs-csi
    newName: ${registry}/smol-agent-oci-fs-csi
    newTag: ${version}

Live verification:
OSIX_OPERATOR_IMAGE=${operator_image} \\
OSIX_CSI_IMAGE=${csi_image} \\
OSIX_GTR_STATE_REF=<registry/repo> \\
scripts/test-k8s-autosnap-gtr.sh
EOF
