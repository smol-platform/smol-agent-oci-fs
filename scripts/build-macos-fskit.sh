#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
package_dir="${repo_root}/macos/OSIxFSKit"
install_dir="${OSIX_MACOS_TOOLS_DIR:-${repo_root}/.osix-tools/bin}"
bundle_id="${OSIX_FSKIT_BUNDLE_ID:-io.github.smol-platform.smol-agent-oci-fs.fskit.extension}"
fs_type="${OSIX_FSKIT_TYPE:-OSIxFS}"
codesign_identity="${OSIX_FSKIT_CODESIGN_IDENTITY:-}"

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

swift build --package-path "${package_dir}" -c release
mkdir -p "${install_dir}"
cp "${package_dir}/.build/release/osix-fskitctl" "${install_dir}/osix-fskitctl"
echo "installed ${install_dir}/osix-fskitctl"
if [[ -n "${codesign_identity}" ]]; then
  codesign --force --sign "${codesign_identity}" "${install_dir}/osix-fskitctl"
  echo "signed ${install_dir}/osix-fskitctl with ${codesign_identity}"
fi
"${install_dir}/osix-fskitctl" doctor --bundle-id "${bundle_id}" --fstype "${fs_type}" || {
  echo "warning: the OSIx FSKit extension is not installed/enabled yet" >&2
}
