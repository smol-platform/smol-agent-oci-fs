#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
image="${OSIX_FUSE_DOCKER_IMAGE:-golang:1.24-bookworm}"
test_pattern="${OSIX_FUSE_TEST_PATTERN:-TestFUSERuntimeIntegrationLinux}"

docker run --rm \
  --privileged \
  --device /dev/fuse \
  -e TEST_PATTERN="${test_pattern}" \
  -v "${repo_root}:/work" \
  -w /work \
  "${image}" \
  bash -lc '
    set -euo pipefail
    export PATH="/usr/local/go/bin:${PATH}"
    export DEBIAN_FRONTEND=noninteractive
    apt-get update >/dev/null
    apt-get install -y fuse-overlayfs >/dev/null
    fuse-overlayfs --version
    go test -run "${TEST_PATTERN}" -count=1 -v ./internal/osix
  '
