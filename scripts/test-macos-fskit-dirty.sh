#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

mkdir -p "${tmp}/upper/agent/workspace"
printf "changed" > "${tmp}/upper/agent/workspace/file.txt"
printf "same" > "${tmp}/upper/agent/workspace/copied.txt"
chmod 0644 "${tmp}/upper/agent/workspace/copied.txt"
ln -s target.txt "${tmp}/upper/agent/workspace/copied-link"
ln -s new-target.txt "${tmp}/upper/agent/workspace/changed-link"
touch "${tmp}/upper/agent/workspace/.wh.old.txt"
touch "${tmp}/upper/agent/workspace/.wh.never-existed.txt"
printf "hidden" > "${tmp}/upper/agent/workspace/hidden-file.txt"
touch "${tmp}/upper/agent/workspace/.wh.hidden-file.txt"
mkdir -p "${tmp}/upper/agent/workspace/hidden-dir"
printf "hidden child" > "${tmp}/upper/agent/workspace/hidden-dir/child.txt"
touch "${tmp}/upper/agent/workspace/.wh.hidden-dir"
mkdir -p "${tmp}/upper/agent/opaque"
touch "${tmp}/upper/agent/opaque/.wh..wh..opq"

xcrun swiftc \
  -framework FSKit \
  -o "${tmp}/DirtyIndexSmoke" \
  "${repo_root}/macos/OSIxFSKit/Tests/DirtyIndexSmoke.swift" \
  "${repo_root}/macos/OSIxFSKit/Extension/OSIxDirtyIndex.swift" \
  "${repo_root}/macos/OSIxFSKit/Extension/OSIxMountOptions.swift" \
  "${repo_root}/macos/OSIxFSKit/Extension/OSIxVolume.swift"

workspace="${tmp}/workspace"
mkdir -p "${workspace}/.osix/blobs/sha256"
copied_digest="$(printf "same" | shasum -a 256 | awk '{print $1}')"
copied_link_digest="$(printf "target.txt" | shasum -a 256 | awk '{print $1}')"
agent_mode="$((8#$(/usr/bin/stat -f '%Lp' "${tmp}/upper/agent")))"
workspace_mode="$((8#$(/usr/bin/stat -f '%Lp' "${tmp}/upper/agent/workspace")))"
opaque_mode="$((8#$(/usr/bin/stat -f '%Lp' "${tmp}/upper/agent/opaque")))"
copied_link_mode="$((8#$(/usr/bin/stat -f '%Lp' "${tmp}/upper/agent/workspace/copied-link")))"
config_digest="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
manifest_digest="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
cat > "${workspace}/.osix/blobs/sha256/${config_digest}" <<JSON
{"tree":[{"path":"agent","type":"dir","mode":${agent_mode}},{"path":"agent/workspace","type":"dir","mode":${workspace_mode}},{"path":"agent/workspace/copied.txt","type":"file","mode":420,"size":4,"digest":"sha256:${copied_digest}"},{"path":"agent/workspace/copied-link","type":"symlink","mode":${copied_link_mode},"digest":"sha256:${copied_link_digest}","linkname":"target.txt"},{"path":"agent/workspace/old.txt","type":"file","mode":420,"size":3,"digest":"sha256:1111111111111111111111111111111111111111111111111111111111111111"},{"path":"agent/workspace/hidden-file.txt","type":"file","mode":420,"size":6,"digest":"sha256:2222222222222222222222222222222222222222222222222222222222222222"},{"path":"agent/workspace/hidden-dir","type":"dir","mode":493},{"path":"agent/opaque","type":"dir","mode":${opaque_mode}},{"path":"agent/opaque/lower-only.txt","type":"file","mode":420,"size":10,"digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000"}]}
JSON
cat > "${workspace}/.osix/blobs/sha256/${manifest_digest}" <<JSON
{"config":{"digest":"sha256:${config_digest}"}}
JSON

"${tmp}/DirtyIndexSmoke" "${tmp}/upper" "${workspace}" "sha256:${manifest_digest}" > "${tmp}/dirty.json"

if "${tmp}/DirtyIndexSmoke" "${tmp}/upper" "${workspace}" "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc" > "${tmp}/missing-parent.json" 2> "${tmp}/missing-parent.err"; then
  echo "DirtyIndexSmoke accepted missing parent snapshot metadata" >&2
  exit 1
fi

python3 - "$tmp/dirty.json" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)

assert data["dirtyBytes"] == len("changed"), data
assert data["paths"]["agent/workspace/file.txt"] == "modified", data
assert data["paths"]["agent/workspace/old.txt"] == "deleted", data
assert data["paths"]["agent/workspace/hidden-file.txt"] == "deleted", data
assert data["paths"]["agent/workspace/hidden-dir"] == "deleted", data
assert data["paths"]["agent/opaque/lower-only.txt"] == "deleted", data
assert data["paths"]["agent/workspace/changed-link"] == "modified", data
assert "agent/workspace/copied.txt" not in data["paths"], data
assert "agent/workspace/copied-link" not in data["paths"], data
assert "agent/workspace/.wh.old.txt" not in data["paths"], data
assert "agent/workspace/never-existed.txt" not in data["paths"], data
assert "agent/workspace/hidden-dir/child.txt" not in data["paths"], data
assert "agent/opaque/.wh..wh..opq" not in data["paths"], data
PY

mkdir -p "${tmp}/volume/lower/agent/workspace" "${tmp}/volume/upper" "${tmp}/volume/work"
printf "lower" > "${tmp}/volume/lower/agent/workspace/file.txt"
chmod 0644 "${tmp}/volume/lower/agent/workspace/file.txt"

xcrun swiftc \
  -framework FSKit \
  -o "${tmp}/VolumeMetadataSmoke" \
  "${repo_root}/macos/OSIxFSKit/Tests/VolumeMetadataSmoke.swift" \
  "${repo_root}/macos/OSIxFSKit/Extension/OSIxDirtyIndex.swift" \
  "${repo_root}/macos/OSIxFSKit/Extension/OSIxMountOptions.swift" \
  "${repo_root}/macos/OSIxFSKit/Extension/OSIxVolume.swift"

"${tmp}/VolumeMetadataSmoke" \
  "${tmp}/volume/lower" \
  "${tmp}/volume/upper" \
  "${tmp}/volume/work"

swift build --package-path "${repo_root}/macos/OSIxFSKit" -c release >/dev/null
fskitctl="${repo_root}/macos/OSIxFSKit/.build/release/osix-fskitctl"
valid_digest="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

mkdir -p "${tmp}/helper/lower" "${tmp}/helper/upper" "${tmp}/helper/work" "${tmp}/helper/nested-upper/work" "${tmp}/helper/world-upper"
printf "not a directory" > "${tmp}/helper/file-target"
chmod 0777 "${tmp}/helper/world-upper"

if "${fskitctl}" mount \
  --source-ref snap \
  --source-digest "${valid_digest}" \
  --workspace-root "${tmp}" \
  --lower "${tmp}/volume/lower" \
  --upper "${tmp}/volume/upper" \
  --work "${tmp}/volume/work" \
  2> "${tmp}/missing-target.err"; then
  echo "osix-fskitctl accepted missing --target" >&2
  exit 1
elif [[ "$?" -ne 64 ]]; then
  echo "osix-fskitctl missing --target returned unexpected status" >&2
  cat "${tmp}/missing-target.err" >&2
  exit 1
fi
grep -q "missing --target" "${tmp}/missing-target.err"

if "${fskitctl}" mount \
  --target \
  --source-ref snap \
  --source-digest "${valid_digest}" \
  --workspace-root "${tmp}" \
  --lower "${tmp}/volume/lower" \
  --upper "${tmp}/volume/upper" \
  --work "${tmp}/volume/work" \
  2> "${tmp}/missing-target-value.err"; then
  echo "osix-fskitctl accepted missing --target value" >&2
  exit 1
elif [[ "$?" -ne 64 ]]; then
  echo "osix-fskitctl missing --target value returned unexpected status" >&2
  cat "${tmp}/missing-target-value.err" >&2
  exit 1
fi
grep -q "missing --target" "${tmp}/missing-target-value.err"

if "${fskitctl}" mount \
  --target "${tmp}/helper/file-target" \
  --source-ref snap \
  --source-digest "${valid_digest}" \
  --workspace-root "${tmp}" \
  --lower "${tmp}/helper/lower" \
  --upper "${tmp}/helper/upper" \
  --work "${tmp}/helper/work" \
  2> "${tmp}/file-target.err"; then
  echo "osix-fskitctl accepted file --target" >&2
  exit 1
elif [[ "$?" -ne 64 ]]; then
  echo "osix-fskitctl file --target returned unexpected status" >&2
  cat "${tmp}/file-target.err" >&2
  exit 1
fi
grep -q -- "--target .* is not a directory" "${tmp}/file-target.err"

if "${fskitctl}" mount \
  --target "${tmp}/target" \
  --source-ref snap \
  --source-digest "${valid_digest}" \
  --workspace-root "${tmp}" \
  --lower "${tmp}/helper/missing-lower" \
  --upper "${tmp}/helper/upper" \
  --work "${tmp}/helper/work" \
  2> "${tmp}/missing-lower.err"; then
  echo "osix-fskitctl accepted missing --lower" >&2
  exit 1
elif [[ "$?" -ne 64 ]]; then
  echo "osix-fskitctl missing --lower returned unexpected status" >&2
  cat "${tmp}/missing-lower.err" >&2
  exit 1
fi
grep -q -- "--lower .* is unavailable" "${tmp}/missing-lower.err"

if "${fskitctl}" mount \
  --target "${tmp}/target" \
  --source-ref snap \
  --source-digest not-a-digest \
  --workspace-root "${tmp}" \
  --lower "${tmp}/volume/lower" \
  --upper "${tmp}/volume/upper" \
  --work "${tmp}/volume/work" \
  2> "${tmp}/bad-digest.err"; then
  echo "osix-fskitctl accepted malformed --source-digest" >&2
  exit 1
elif [[ "$?" -ne 64 ]]; then
  echo "osix-fskitctl malformed --source-digest returned unexpected status" >&2
  cat "${tmp}/bad-digest.err" >&2
  exit 1
fi
grep -q -- "--source-digest must be a sha256 digest" "${tmp}/bad-digest.err"

if "${fskitctl}" mount \
  --target "${tmp}/target" \
  --source-ref snap \
  --source-digest "${valid_digest}" \
  --workspace-root "${tmp}" \
  --lower "${tmp}/helper/lower" \
  --upper "${tmp}/helper/world-upper" \
  --work "${tmp}/helper/work" \
  2> "${tmp}/world-upper.err"; then
  echo "osix-fskitctl accepted world-writable --upper" >&2
  exit 1
elif [[ "$?" -ne 64 ]]; then
  echo "osix-fskitctl world-writable --upper returned unexpected status" >&2
  cat "${tmp}/world-upper.err" >&2
  exit 1
fi
grep -q "refusing world-writable runtime directory --upper" "${tmp}/world-upper.err"

if "${fskitctl}" mount \
  --target "${tmp}/helper/upper/target" \
  --source-ref snap \
  --source-digest "${valid_digest}" \
  --workspace-root "${tmp}" \
  --lower "${tmp}/helper/lower" \
  --upper "${tmp}/helper/upper" \
  --work "${tmp}/helper/work" \
  2> "${tmp}/target-in-upper.err"; then
  echo "osix-fskitctl accepted target nested in upper" >&2
  exit 1
elif [[ "$?" -ne 64 ]]; then
  echo "osix-fskitctl target nested in upper returned unexpected status" >&2
  cat "${tmp}/target-in-upper.err" >&2
  exit 1
fi
grep -q -- "--target and --upper must be separate directories" "${tmp}/target-in-upper.err"

if "${fskitctl}" mount \
  --target "${tmp}/target" \
  --source-ref snap \
  --source-digest "${valid_digest}" \
  --workspace-root "${tmp}" \
  --lower "${tmp}/helper/lower" \
  --upper "${tmp}/helper/nested-upper" \
  --work "${tmp}/helper/nested-upper/work" \
  2> "${tmp}/nested-work.err"; then
  echo "osix-fskitctl accepted nested --upper/--work" >&2
  exit 1
elif [[ "$?" -ne 64 ]]; then
  echo "osix-fskitctl nested --upper/--work returned unexpected status" >&2
  cat "${tmp}/nested-work.err" >&2
  exit 1
fi
grep -q -- "--upper and --work must be separate directories" "${tmp}/nested-work.err"

if "${fskitctl}" mount \
  --target "${tmp}/target" \
  --source-ref snap \
  --source-digest "${valid_digest}" \
  --workspace-root "${tmp}" \
  --lower "${tmp}/volume/lower" \
  --upper "${tmp}/volume/upper" \
  --work "${tmp}/volume/work" \
  --mode materialized \
  2> "${tmp}/bad-mode.err"; then
  echo "osix-fskitctl accepted unsupported --mode" >&2
  exit 1
elif [[ "$?" -ne 64 ]]; then
  echo "osix-fskitctl unsupported --mode returned unexpected status" >&2
  cat "${tmp}/bad-mode.err" >&2
  exit 1
fi
grep -q "unsupported --mode materialized" "${tmp}/bad-mode.err"

if "${fskitctl}" unmount \
  --target \
  --force \
  2> "${tmp}/unmount-missing-target-value.err"; then
  echo "osix-fskitctl accepted unmount missing --target value" >&2
  exit 1
elif [[ "$?" -ne 64 ]]; then
  echo "osix-fskitctl unmount missing --target value returned unexpected status" >&2
  cat "${tmp}/unmount-missing-target-value.err" >&2
  exit 1
fi
grep -q "missing --target" "${tmp}/unmount-missing-target-value.err"

echo "FSKit dirty-index, metadata, and xattr smoke passed"
