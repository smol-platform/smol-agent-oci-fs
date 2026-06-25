#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
package_dir="${repo_root}/macos/OSIxFSKit"
install_dir="${OSIX_MACOS_TOOLS_DIR:-${repo_root}/.osix-tools/bin}"
bundle_id="${OSIX_FSKIT_BUNDLE_ID:-io.github.smol-platform.smol-agent-oci-fs.fskit.extension}"
fs_type="${OSIX_FSKIT_TYPE:-OSIxFS}"
codesign_identity="${OSIX_FSKIT_CODESIGN_IDENTITY:-}"
require_team_signing="${OSIX_FSKIT_REQUIRE_TEAM_SIGNING:-0}"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "FSKit helper build requires Darwin; current OS is $(uname -s)" >&2
  exit 2
fi

if [[ ! -d /System/Library/Frameworks/FSKit.framework ]]; then
  echo "missing prerequisite: FSKit.framework runtime is unavailable; macOS 15.4 or newer is required" >&2
  exit 2
fi

if ! xcrun --sdk macosx --show-sdk-path >/dev/null 2>&1; then
  echo "missing prerequisite: Xcode with macOS SDK is required" >&2
  exit 2
fi

if [[ "${require_team_signing}" == "1" && ( -z "${codesign_identity}" || "${codesign_identity}" == "-" ) ]]; then
  echo "OSIX_FSKIT_REQUIRE_TEAM_SIGNING=1 requires OSIX_FSKIT_CODESIGN_IDENTITY to be an Apple signing identity, not ad-hoc '-'" >&2
  exit 2
fi

team_identifier() {
  local binary="$1"
  codesign -dv --verbose=4 "${binary}" 2>&1 | awk -F= '/^TeamIdentifier=/ {print $2; exit}'
}

verify_team_signed() {
  local binary="$1"
  local team
  team="$(team_identifier "${binary}")"
  if [[ -z "${team}" || "${team}" == "not set" ]]; then
    echo "osix-fskitctl is not signed with an Apple TeamIdentifier; set OSIX_FSKIT_CODESIGN_IDENTITY to an Apple Development or Developer ID identity approved for com.apple.developer.fskit.fsmodule" >&2
    exit 2
  fi
  echo "osix-fskitctl TeamIdentifier=${team}"
}

swift build --package-path "${package_dir}" -c release
mkdir -p "${install_dir}"
cp "${package_dir}/.build/release/osix-fskitctl" "${install_dir}/osix-fskitctl"
echo "installed ${install_dir}/osix-fskitctl"
if [[ -n "${codesign_identity}" ]]; then
  codesign --force --sign "${codesign_identity}" "${install_dir}/osix-fskitctl"
  echo "signed ${install_dir}/osix-fskitctl with ${codesign_identity}"
fi
if [[ "${require_team_signing}" == "1" ]]; then
  verify_team_signed "${install_dir}/osix-fskitctl"
fi
"${install_dir}/osix-fskitctl" doctor --bundle-id "${bundle_id}" --fstype "${fs_type}" || {
  echo "warning: the OSIx FSKit extension is not installed/enabled yet" >&2
}
