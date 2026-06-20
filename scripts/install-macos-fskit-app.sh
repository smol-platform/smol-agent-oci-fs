#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install_dir="${OSIX_FSKIT_APP_DIR:-${HOME}/Applications}"
source_app="${OSIX_FSKIT_DIST_DIR:-${repo_root}/.osix-tools/dist/macos}/OSIxFSKitHost.app"
target_app="${install_dir}/OSIxFSKitHost.app"
appex_bundle="${target_app}/Contents/PlugIns/OSIxFSKitExtension.appex"
host_executable="${target_app}/Contents/MacOS/OSIxFSKitHost"
extension_executable="${appex_bundle}/Contents/MacOS/OSIxFSKitExtension"
expected_host_bundle_id="io.github.smol-platform.smol-agent-oci-fs.fskit.host"
bundle_id="${OSIX_FSKIT_BUNDLE_ID:-io.github.smol-platform.smol-agent-oci-fs.fskit.extension}"
fs_type="${OSIX_FSKIT_TYPE:-OSIxFS}"
open_after_install=1
register_after_install=1
elect_after_install=1
build_helper=1
background_registration_launch=0
wait_ready_seconds=0
open_settings_on_failure=auto
settings_url="x-apple.systempreferences:com.apple.LoginItems-Settings.extension"
settings_path="System Settings > Login Items & Extensions > OSIxFSKitHost Extensions > FSKit Modules"

verify_installed_app() {
  if [[ ! -d "${target_app}" ]]; then
    echo "installed app bundle is missing: ${target_app}" >&2
    exit 1
  fi
  if [[ ! -d "${appex_bundle}" ]]; then
    echo "installed FSKit extension bundle is missing: ${appex_bundle}" >&2
    exit 1
  fi
  if [[ ! -x "${host_executable}" ]]; then
    echo "installed host executable is missing or not executable: ${host_executable}" >&2
    exit 1
  fi
  if [[ ! -x "${extension_executable}" ]]; then
    echo "installed extension executable is missing or not executable: ${extension_executable}" >&2
    exit 1
  fi

  plutil -lint \
    "${target_app}/Contents/Info.plist" \
    "${appex_bundle}/Contents/Info.plist"
  codesign --verify --strict "${appex_bundle}"
  codesign --verify --strict "${target_app}"

  entitlements_plist="$(mktemp "${TMPDIR:-/tmp}/osix-fskit-extension-entitlements.XXXXXX.plist")"
  codesign -dvvv --entitlements "${entitlements_plist}" "${appex_bundle}" >/dev/null
  if ! grep -q "com.apple.developer.fskit.fsmodule" "${entitlements_plist}"; then
    rm -f "${entitlements_plist}"
    echo "installed extension is missing com.apple.developer.fskit.fsmodule entitlement" >&2
    exit 1
  fi
  rm -f "${entitlements_plist}"

  host_bundle_id="$(/usr/libexec/PlistBuddy -c "Print :CFBundleIdentifier" "${target_app}/Contents/Info.plist")"
  extension_bundle_id="$(/usr/libexec/PlistBuddy -c "Print :CFBundleIdentifier" "${appex_bundle}/Contents/Info.plist")"
  fs_short_name="$(/usr/libexec/PlistBuddy -c "Print :EXAppExtensionAttributes:FSShortName" "${appex_bundle}/Contents/Info.plist")"
  fs_personality_name="$(/usr/libexec/PlistBuddy -c "Print :EXAppExtensionAttributes:FSPersonalities:OSIxFSPersonality:FSName" "${appex_bundle}/Contents/Info.plist")"
  if [[ "${host_bundle_id}" != "${expected_host_bundle_id}" ]]; then
    echo "installed host bundle id ${host_bundle_id} does not match ${expected_host_bundle_id}" >&2
    exit 1
  fi
  if [[ "${extension_bundle_id}" != "${bundle_id}" ]]; then
    echo "installed extension bundle id ${extension_bundle_id} does not match ${bundle_id}" >&2
    exit 1
  fi
  if [[ "${fs_short_name}" != "${fs_type}" || "${fs_personality_name}" != "${fs_type}" ]]; then
    echo "installed extension filesystem type does not declare ${fs_type}" >&2
    exit 1
  fi
}

usage() {
  cat >&2 <<EOF
usage: $0 [--no-open] [--background-register] [--wait-ready=SECONDS] [--open-settings] [--no-open-settings] [--no-register] [--no-elect] [--no-helper]

Builds and installs the OSIx FSKit host app into:
  ${target_app}

By default this also registers the app extension with PlugInKit, elects it
for the current user, and opens the host app. Use --background-register with
--no-open when tests need to trigger ExtensionKit discovery without foregrounding
the app. macOS may still require enabling the File System Extension in System
Settings before FSClient reports it ready. Interactive installs open the Login
Items & Extensions settings pane when FSClient still reports the extension
disabled; use --no-open-settings to suppress this.
EOF
}

for arg in "$@"; do
  case "${arg}" in
    --no-open)
      open_after_install=0
      ;;
    --background-register)
      background_registration_launch=1
      ;;
    --wait-ready)
      echo "--wait-ready requires a numeric seconds value" >&2
      usage
      exit 64
      ;;
    --wait-ready=*)
      wait_ready_seconds="${arg#--wait-ready=}"
      ;;
    --open-settings)
      open_settings_on_failure=1
      ;;
    --no-open-settings)
      open_settings_on_failure=0
      ;;
    --no-register)
      register_after_install=0
      elect_after_install=0
      ;;
    --no-elect)
      elect_after_install=0
      ;;
    --no-helper)
      build_helper=0
      ;;
    *)
      echo "unknown argument: ${arg}" >&2
      usage
      exit 64
      ;;
  esac
done

if [[ "${wait_ready_seconds}" != "0" && ! "${wait_ready_seconds}" =~ ^[0-9]+$ ]]; then
  echo "--wait-ready expects a non-negative integer, got ${wait_ready_seconds}" >&2
  exit 64
fi

if [[ "${build_helper}" -eq 1 ]]; then
  "${repo_root}/scripts/build-macos-fskit.sh"
fi
"${repo_root}/scripts/build-macos-fskit-app.sh"

if pids="$(/usr/bin/pgrep -f "^${host_executable}$" || true)" && [[ -n "${pids}" ]]; then
  /bin/kill ${pids} >/dev/null 2>&1 || true
  sleep 1
fi

mkdir -p "${install_dir}"
rm -rf "${target_app}"
ditto "${source_app}" "${target_app}"
verify_installed_app

if [[ "${register_after_install}" -eq 1 ]]; then
  /usr/bin/pluginkit -a "${target_app}" || true
  /usr/bin/pluginkit -a "${appex_bundle}" || true
fi

if [[ "${background_registration_launch}" -eq 1 ]]; then
  /usr/bin/open -g -j "${target_app}" >/dev/null 2>&1 || {
    echo "warning: background host launch did not complete; continuing with PlugInKit registration" >&2
  }
  sleep 2
fi

if [[ "${elect_after_install}" -eq 1 ]]; then
  /usr/bin/pluginkit -e use -i "${bundle_id}"
fi

if [[ "${open_after_install}" -eq 1 ]]; then
  /usr/bin/open -n "${target_app}"
fi

echo "installed ${target_app}"
if [[ "${register_after_install}" -eq 1 ]]; then
  /usr/bin/pluginkit -m -A -D -vv -i "${bundle_id}" || true
fi

helper="${repo_root}/.osix-tools/bin/osix-fskitctl"
if [[ "${register_after_install}" -eq 1 && -x "${helper}" ]]; then
  ready=0
  if [[ "${wait_ready_seconds}" -gt 0 ]]; then
    deadline=$((SECONDS + wait_ready_seconds))
    while true; do
      if "${helper}" doctor --bundle-id "${bundle_id}" --fstype "${fs_type}" >/dev/null 2>&1; then
        ready=1
        break
      fi
      if [[ "${SECONDS}" -ge "${deadline}" ]]; then
        break
      fi
      sleep 1
    done
  fi
  if [[ "${ready}" -eq 1 ]]; then
    "${helper}" doctor --bundle-id "${bundle_id}" --fstype "${fs_type}"
  elif ! "${helper}" doctor --bundle-id "${bundle_id}" --fstype "${fs_type}"; then
    echo "enable the OSIx FSKit extension in ${settings_path}"
    echo "settings URL: ${settings_url}"
    if [[ "${open_settings_on_failure}" == "1" || ( "${open_settings_on_failure}" == "auto" && "${open_after_install}" -eq 1 ) ]]; then
      /usr/bin/open "${settings_url}" >/dev/null 2>&1 || {
        echo "warning: failed to open System Settings; open ${settings_url} manually" >&2
      }
    fi
  fi
fi
