#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install_dir="${OSIX_FSKIT_APP_DIR:-${HOME}/Applications}"
source_app="${OSIX_FSKIT_DIST_DIR:-${repo_root}/.osix-tools/dist/macos}/OSIxFSKitHost.app"
target_app="${install_dir}/OSIxFSKitHost.app"
appex_bundle="${target_app}/Contents/PlugIns/OSIxFSKitExtension.appex"
bundle_id="${OSIX_FSKIT_BUNDLE_ID:-io.github.smol-platform.smol-agent-oci-fs.fskit.extension}"
open_after_install=1
register_after_install=1
elect_after_install=1
build_helper=1
background_registration_launch=0

usage() {
  cat >&2 <<EOF
usage: $0 [--no-open] [--background-register] [--no-register] [--no-elect] [--no-helper]

Builds and installs the OSIx FSKit host app into:
  ${target_app}

By default this also registers the app extension with PlugInKit, elects it
for the current user, and opens the host app. Use --background-register with
--no-open when tests need to trigger ExtensionKit discovery without foregrounding
the app. macOS may still require enabling the File System Extension in System
Settings before FSClient reports it ready.
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

if [[ "${build_helper}" -eq 1 ]]; then
  "${repo_root}/scripts/build-macos-fskit.sh"
fi
"${repo_root}/scripts/build-macos-fskit-app.sh"

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
  if ! "${helper}" doctor --bundle-id "${bundle_id}"; then
    echo "enable the OSIx FSKit extension in System Settings > General > Login Items & Extensions > File System Extensions"
  fi
fi
