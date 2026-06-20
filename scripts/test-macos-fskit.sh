#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export PATH="${repo_root}/.osix-tools/bin:${PATH}"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "macOS FSKit test requires Darwin; current OS is $(uname -s)" >&2
  exit 2
fi

"${repo_root}/scripts/install-macos-fskit-app.sh" --no-open --background-register --wait-ready="${OSIX_FSKIT_READY_TIMEOUT:-10}"

if ! osix-fskitctl doctor --bundle-id "${OSIX_FSKIT_BUNDLE_ID:-io.github.smol-platform.smol-agent-oci-fs.fskit.extension}"; then
  echo "fix the listed FSKit extension prerequisite, then rerun this script" >&2
  echo "for local development, run: ${repo_root}/scripts/install-macos-fskit-app.sh" >&2
  exit 2
fi

cd "${repo_root}"
go test -run TestDarwinFSKitIntegration -count=1 -v ./internal/osix
