#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source_root="${repo_root}/macos/OSIxFSKit"
dist_root="${OSIX_FSKIT_DIST_DIR:-${repo_root}/.osix-tools/dist/macos}"
build_root="${repo_root}/.osix-tools/build/fskit-app"
app_bundle="${dist_root}/OSIxFSKitHost.app"
appex_bundle="${app_bundle}/Contents/PlugIns/OSIxFSKitExtension.appex"
host_bin="${app_bundle}/Contents/MacOS/OSIxFSKitHost"
extension_bin="${appex_bundle}/Contents/MacOS/OSIxFSKitExtension"
codesign_identity="${OSIX_FSKIT_CODESIGN_IDENTITY:--}"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "FSKit app build requires Darwin; current OS is $(uname -s)" >&2
  exit 2
fi

if [[ ! -d /System/Library/Frameworks/FSKit.framework ]]; then
  echo "missing prerequisite: FSKit.framework runtime is unavailable; macOS 15.4 or newer is required" >&2
  exit 2
fi

sdk="$(xcrun --sdk macosx --show-sdk-path)"
arch="$(uname -m)"
target="${arch}-apple-macosx15.4"

rm -rf "${build_root}" "${app_bundle}"
mkdir -p "${app_bundle}/Contents/MacOS" \
  "${app_bundle}/Contents/PlugIns" \
  "${appex_bundle}/Contents/MacOS" \
  "${build_root}"

cp "${source_root}/App/Info.plist" "${app_bundle}/Contents/Info.plist"
cp "${source_root}/Extension/Info.plist" "${appex_bundle}/Contents/Info.plist"

xcrun swiftc \
  -sdk "${sdk}" \
  -target "${target}" \
  -parse-as-library \
  -framework AppKit \
  -o "${host_bin}" \
  "${source_root}/App/OSIxFSKitHost.swift"

xcrun swiftc \
  -sdk "${sdk}" \
  -target "${target}" \
  -parse-as-library \
  -framework ExtensionFoundation \
  -framework FSKit \
  -o "${extension_bin}" \
  "${source_root}/Extension/OSIxFSKitExtension.swift" \
  "${source_root}/Extension/OSIxDirtyIndex.swift" \
  "${source_root}/Extension/OSIxFileSystem.swift" \
  "${source_root}/Extension/OSIxMountOptions.swift" \
  "${source_root}/Extension/OSIxVolume.swift"

codesign --force --sign "${codesign_identity}" \
  --entitlements "${source_root}/Extension/OSIxFSKitExtension.entitlements" \
  "${appex_bundle}"
codesign --force --sign "${codesign_identity}" \
  --entitlements "${source_root}/App/OSIxFSKitHost.entitlements" \
  "${app_bundle}"

codesign -dvvv --entitlements "${build_root}/extension-entitlements.plist" "${appex_bundle}" >/dev/null
grep -q "com.apple.developer.fskit.fsmodule" "${build_root}/extension-entitlements.plist"
plutil -lint \
  "${app_bundle}/Contents/Info.plist" \
  "${appex_bundle}/Contents/Info.plist"
echo "built ${app_bundle}"
echo "signed ${app_bundle} and embedded extension with ${codesign_identity}"
