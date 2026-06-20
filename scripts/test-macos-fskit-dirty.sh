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
{"tree":[{"path":"agent","type":"dir","mode":${agent_mode}},{"path":"agent/workspace","type":"dir","mode":${workspace_mode}},{"path":"agent/workspace/copied.txt","type":"file","mode":420,"size":4,"digest":"sha256:${copied_digest}"},{"path":"agent/workspace/copied-link","type":"symlink","mode":${copied_link_mode},"digest":"sha256:${copied_link_digest}","linkname":"target.txt"},{"path":"agent/opaque","type":"dir","mode":${opaque_mode}},{"path":"agent/opaque/lower-only.txt","type":"file","mode":420,"size":10,"digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000"}]}
JSON
cat > "${workspace}/.osix/blobs/sha256/${manifest_digest}" <<JSON
{"config":{"digest":"sha256:${config_digest}"}}
JSON

"${tmp}/DirtyIndexSmoke" "${tmp}/upper" "${workspace}" "sha256:${manifest_digest}" > "${tmp}/dirty.json"

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

echo "FSKit dirty-index, metadata, and xattr smoke passed"
