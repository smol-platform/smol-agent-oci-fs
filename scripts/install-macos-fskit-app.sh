#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install_dir="${OSIX_FSKIT_APP_DIR:-${HOME}/Applications}"
source_app="${OSIX_FSKIT_DIST_DIR:-${repo_root}/.osix-tools/dist/macos}/OSIxFSKitHost.app"
target_app="${install_dir}/OSIxFSKitHost.app"
appex_bundle="${target_app}/Contents/PlugIns/OSIxFSKitExtension.appex"
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

host_executable="${target_app}/Contents/MacOS/OSIxFSKitHost"
if pids="$(/usr/bin/pgrep -f "^${host_executable}$" || true)" && [[ -n "${pids}" ]]; then
  /bin/kill ${pids} >/dev/null 2>&1 || true
  sleep 1
fi

mkdir -p "${install_dir}"
rm -rf "${target_app}"
ditto "${source_app}" "${target_app}"

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
if [[ -x "${helper}" ]]; then
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
    echo "enable the OSIx FSKit extension in System Settings > General > Login Items & Extensions > File System Extensions"
    echo "settings URL: ${settings_url}"
    if [[ "${open_settings_on_failure}" == "1" || ( "${open_settings_on_failure}" == "auto" && "${open_after_install}" -eq 1 ) ]]; then
      /usr/bin/open "${settings_url}" >/dev/null 2>&1 || {
        echo "warning: failed to open System Settings; open ${settings_url} manually" >&2
      }
    fi
  fi
fi
