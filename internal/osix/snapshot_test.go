package osix

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"
)

func TestSnapshotRestoreDiffAndFork(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{
		Base:          "ghcr.io/acme/agent-base:2026-06-19",
		Name:          "research-agent-a",
		StateRef:      "local/research-agent-a",
		Mount:         filepath.Join(root, "agentfs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}

	agentfs := filepath.Join(root, "agentfs")
	mustWrite(t, filepath.Join(agentfs, "agent", "workspace", "notes.md"), "hello\n")
	mustWrite(t, filepath.Join(agentfs, "agent", "memory", "memory.jsonl"), "{}\n")
	mustWrite(t, filepath.Join(agentfs, "agent", "tmp", "scratch.txt"), "excluded\n")
	mustWrite(t, filepath.Join(agentfs, ".env"), "SECRET=excluded\n")
	if err := os.Symlink("../workspace/notes.md", filepath.Join(agentfs, "agent", "memory", "latest-notes")); err != nil {
		t.Fatal(err)
	}

	first, err := Snapshot(root, agentfs, SnapshotOptions{Message: "first", Tag: "snap-000001", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Tags) == 0 || first.ManifestDigest == "" {
		t.Fatalf("expected digest and tags, got %#v", first)
	}

	restore1 := filepath.Join(root, "restore1")
	if err := Restore(root, "main", restore1, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(restore1, "agent", "workspace", "notes.md"), "hello\n")
	assertFile(t, filepath.Join(restore1, "agent", "memory", "memory.jsonl"), "{}\n")
	assertMissing(t, filepath.Join(restore1, "agent", "tmp", "scratch.txt"))
	assertMissing(t, filepath.Join(restore1, ".env"))
	linkTarget, err := os.Readlink(filepath.Join(restore1, "agent", "memory", "latest-notes"))
	if err != nil {
		t.Fatal(err)
	}
	if linkTarget != "../workspace/notes.md" {
		t.Fatalf("unexpected symlink target %q", linkTarget)
	}

	mustWrite(t, filepath.Join(agentfs, "agent", "workspace", "notes.md"), "hello, updated\n")
	mustWrite(t, filepath.Join(agentfs, "agent", "skills", "skill.md"), "# skill\n")
	if err := os.Remove(filepath.Join(agentfs, "agent", "memory", "memory.jsonl")); err != nil {
		t.Fatal(err)
	}
	second, err := Snapshot(root, agentfs, SnapshotOptions{Message: "second", Tag: "snap-000002", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ManifestDigest == first.ManifestDigest {
		t.Fatalf("expected changed snapshot digest")
	}
	assertLayerEntries(t, root, second.ManifestDigest, []string{
		"agent/memory/.wh.memory.jsonl",
		"agent/skills",
		"agent/skills/skill.md",
		"agent/workspace/notes.md",
	})

	changes, err := Diff(root, "snap-000001", "snap-000002")
	if err != nil {
		t.Fatal(err)
	}
	got := changeStrings(changes)
	want := []string{
		"D /agent/memory/memory.jsonl",
		"A /agent/skills",
		"A /agent/skills/skill.md",
		"M /agent/workspace/notes.md",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes mismatch\nwant: %#v\n got: %#v", want, got)
	}

	digest, err := Fork(root, "snap-000001", "experiment-a")
	if err != nil {
		t.Fatal(err)
	}
	if digest != first.ManifestDigest {
		t.Fatalf("fork digest mismatch: want %s got %s", first.ManifestDigest, digest)
	}
	restoreFork := filepath.Join(root, "restore-fork")
	if err := Restore(root, "experiment-a", restoreFork, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(restoreFork, "agent", "workspace", "notes.md"), "hello\n")
}

func TestSnapshotSigningFailureLeavesPublishedRefsUnchanged(t *testing.T) {
	root := t.TempDir()
	fsRoot := filepath.Join(root, "fs")
	if _, err := Init(root, InitOptions{Base: "base", Name: "agent", StateRef: "local/agent", Mount: fsRoot, DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fsRoot, "agent", "workspace", "file.txt"), "v1\n")
	first, err := Snapshot(root, fsRoot, SnapshotOptions{Tag: "first", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fsRoot, "agent", "workspace", "file.txt"), "v2\n")
	if _, err := Snapshot(root, fsRoot, SnapshotOptions{Tag: "signed-fail", AlsoTag: "main", Sign: filepath.Join(root, "missing-signing-key")}); err == nil {
		t.Fatal("expected signing failure")
	}
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"main", "latest"} {
		digest, err := s.resolveRef(ref)
		if err != nil {
			t.Fatal(err)
		}
		if digest != first.ManifestDigest {
			t.Fatalf("%s advanced after signing failure: got %s want %s", ref, digest, first.ManifestDigest)
		}
	}
	if _, err := s.resolveRef("signed-fail"); err == nil {
		t.Fatal("failed signed snapshot tag was published")
	}
}

func TestSnapshotAttestationFailureLeavesPublishedRefsUnchanged(t *testing.T) {
	root := t.TempDir()
	fsRoot := filepath.Join(root, "fs")
	if _, err := Init(root, InitOptions{Base: "base", Name: "agent", StateRef: "local/agent", Mount: fsRoot, DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fsRoot, "agent", "workspace", "file.txt"), "v1\n")
	first, err := Snapshot(root, fsRoot, SnapshotOptions{Tag: "first", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fsRoot, "agent", "workspace", "file.txt"), "v2\n")
	previousSign := signForPublication
	signForPublication = func(string, string, SignOptions) (VerifyResult, error) {
		return VerifyResult{}, errors.New("injected attestation failure")
	}
	t.Cleanup(func() { signForPublication = previousSign })
	if _, err := Snapshot(root, fsRoot, SnapshotOptions{Tag: "attestation-fail", AlsoTag: "main", Sign: "keyless", Attest: "slsa"}); err == nil {
		t.Fatal("expected attestation failure")
	}
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"main", "latest"} {
		digest, err := s.resolveRef(ref)
		if err != nil {
			t.Fatal(err)
		}
		if digest != first.ManifestDigest {
			t.Fatalf("%s advanced after attestation failure: got %s want %s", ref, digest, first.ManifestDigest)
		}
	}
	if _, err := s.resolveRef("attestation-fail"); err == nil {
		t.Fatal("failed attested snapshot tag was published")
	}
}

func TestSnapshotRefTransactionRollsBackMultiTagRenameFailure(t *testing.T) {
	root := t.TempDir()
	fsRoot := filepath.Join(root, "fs")
	if _, err := Init(root, InitOptions{Base: "base", Name: "agent", StateRef: "local/agent", Mount: fsRoot, DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fsRoot, "agent", "workspace", "file.txt"), "v1\n")
	first, err := Snapshot(root, fsRoot, SnapshotOptions{Tag: "first", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fsRoot, "agent", "workspace", "file.txt"), "v2\n")
	previousRename := renameRefFile
	renameRefFile = func(oldPath, newPath string) error {
		if filepath.Base(newPath) == safeRefName("main") {
			return errors.New("injected ref rename failure")
		}
		return os.Rename(oldPath, newPath)
	}
	t.Cleanup(func() { renameRefFile = previousRename })
	if _, err := Snapshot(root, fsRoot, SnapshotOptions{Tag: "second", AlsoTag: "main"}); err == nil {
		t.Fatal("expected ref transaction failure")
	}
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"main", "latest"} {
		digest, err := s.resolveRef(ref)
		if err != nil {
			t.Fatal(err)
		}
		if digest != first.ManifestDigest {
			t.Fatalf("%s changed after transaction failure: got %s want %s", ref, digest, first.ManifestDigest)
		}
	}
	if _, err := s.resolveRef("second"); err == nil {
		t.Fatal("partially published snapshot tag survived rollback")
	}
}

func TestRestoreFailuresPreserveExistingDestination(t *testing.T) {
	newSnapshot := func(t *testing.T, encrypt string) (string, string) {
		t.Helper()
		root := t.TempDir()
		fsRoot := filepath.Join(root, "fs")
		if _, err := Init(root, InitOptions{Base: "base", Name: "agent", StateRef: "local/agent", Mount: fsRoot, DefaultBranch: "main", Encrypt: encrypt}); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(fsRoot, "agent", "workspace", "file.txt"), "snapshot\n")
		result, err := Snapshot(root, fsRoot, SnapshotOptions{Tag: "source"})
		if err != nil {
			t.Fatal(err)
		}
		return root, result.ManifestDigest
	}
	assertPreserved := func(t *testing.T, target string) {
		t.Helper()
		assertFile(t, filepath.Join(target, "keep.txt"), "original\n")
		assertMissing(t, filepath.Join(target, "agent", "workspace", "file.txt"))
	}

	t.Run("missing-layer", func(t *testing.T) {
		root, digest := newSnapshot(t, "")
		s, err := findStore(root)
		if err != nil {
			t.Fatal(err)
		}
		_, manifest, _, err := s.loadManifest(digest)
		if err != nil {
			t.Fatal(err)
		}
		layerHex, err := digestHex(manifest.Layers[0].Digest)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(s.blobRoot(), layerHex)); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(root, "destination")
		mustWrite(t, filepath.Join(target, "keep.txt"), "original\n")
		if err := Restore(root, digest, target, RestoreOptions{Force: true}); err == nil {
			t.Fatal("expected missing layer failure")
		}
		assertPreserved(t, target)
	})

	t.Run("decrypt", func(t *testing.T) {
		root, digest := newSnapshot(t, "gpg:test-recipient")
		target := filepath.Join(root, "destination")
		mustWrite(t, filepath.Join(target, "keep.txt"), "original\n")
		if err := Restore(root, digest, target, RestoreOptions{Force: true, Decrypt: "gpg:wrong-recipient"}); err == nil {
			t.Fatal("expected decrypt failure")
		}
		assertPreserved(t, target)
	})

	t.Run("extract", func(t *testing.T) {
		root, digest := newSnapshot(t, "")
		s, err := findStore(root)
		if err != nil {
			t.Fatal(err)
		}
		_, manifest, _, err := s.loadManifest(digest)
		if err != nil {
			t.Fatal(err)
		}
		var tarData bytes.Buffer
		tw := tar.NewWriter(&tarData)
		if err := tw.WriteHeader(&tar.Header{Name: "unsupported", Typeflag: tar.TypeLink, Linkname: "elsewhere", Mode: 0o644}); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		var layerData bytes.Buffer
		zw, err := zstd.NewWriter(&layerData)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := zw.Write(tarData.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		layer, err := s.writeBlob(layerData.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		layer.MediaType = MediaTypeLayer
		manifest.Layers = []Descriptor{layer}
		manifestData, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		badManifest, err := s.writeBlob(manifestData)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.writeRef("bad-layer", badManifest.Digest); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(root, "destination")
		mustWrite(t, filepath.Join(target, "keep.txt"), "original\n")
		if err := Restore(root, "bad-layer", target, RestoreOptions{Force: true}); err == nil {
			t.Fatal("expected extraction failure")
		}
		assertPreserved(t, target)
	})

	t.Run("final-replacement", func(t *testing.T) {
		root, digest := newSnapshot(t, "")
		target := filepath.Join(root, "destination")
		mustWrite(t, filepath.Join(target, "keep.txt"), "original\n")
		previousRename := renameRestorePath
		calls := 0
		renameRestorePath = func(oldPath, newPath string) error {
			calls++
			if calls == 2 {
				return errors.New("injected final restore rename failure")
			}
			return os.Rename(oldPath, newPath)
		}
		t.Cleanup(func() { renameRestorePath = previousRename })
		if err := Restore(root, digest, target, RestoreOptions{Force: true}); err == nil {
			t.Fatal("expected final replacement failure")
		}
		assertPreserved(t, target)
		assertMissing(t, filepath.Join(root, ".destination.osix-backup"))
	})
}

func TestRestoreRemovesStaleSiblingStage(t *testing.T) {
	root := t.TempDir()
	fsRoot := filepath.Join(root, "fs")
	if _, err := Init(root, InitOptions{Base: "base", Name: "agent", StateRef: "local/agent", Mount: fsRoot, DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fsRoot, "agent", "workspace", "file.txt"), "snapshot\n")
	result, err := Snapshot(root, fsRoot, SnapshotOptions{Tag: "source"})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "destination")
	stale := filepath.Join(root, ".destination.osix-restore-stale")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := Restore(root, result.ManifestDigest, target, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	assertMissing(t, stale)
	assertFile(t, filepath.Join(target, "agent", "workspace", "file.txt"), "snapshot\n")
}

func TestManifestUsesOSIxMediaTypes(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{
		Base:          "example/base:latest",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "fs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "content")
	result, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001"})
	if err != nil {
		t.Fatal(err)
	}

	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := s.readBlob(result.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.MediaType != MediaTypeOCIManifest {
		t.Fatalf("manifest media type: %s", manifest.MediaType)
	}
	if manifest.ArtifactType != ArtifactTypeSnapshot {
		t.Fatalf("artifact type: %s", manifest.ArtifactType)
	}
	if manifest.Config.MediaType != MediaTypeConfig {
		t.Fatalf("config media type: %s", manifest.Config.MediaType)
	}
	if len(manifest.Layers) != 1 || manifest.Layers[0].MediaType != MediaTypeLayer {
		t.Fatalf("layer media types: %#v", manifest.Layers)
	}
}

func TestMountDiffAndSnapshotUsesMountedParent(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{
		Base:          "example/base:latest",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "fs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "v1\n")
	first, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}

	mountDir := filepath.Join(root, "mounted")
	if _, err := Mount(root, "snap-000001", mountDir, MountOptions{Force: true, RW: true, Branch: "experiment"}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(mountDir, "agent", "workspace", "file.txt"), "v2\n")
	mustWrite(t, filepath.Join(mountDir, "agent", "workspace", "new.txt"), "new\n")
	changes, err := DiffMount(root, mountDir)
	if err != nil {
		t.Fatal(err)
	}
	got := changeStrings(changes)
	want := []string{
		"M /agent/workspace/file.txt",
		"A /agent/workspace/new.txt",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes mismatch\nwant: %#v\n got: %#v", want, got)
	}
	second, err := Snapshot(root, mountDir, SnapshotOptions{Tag: "snap-000002"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	_, _, cfg, err := s.loadManifest(second.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Parent == nil || cfg.Parent.Digest != first.ManifestDigest {
		t.Fatalf("snapshot parent mismatch: %#v want %s", cfg.Parent, first.ManifestDigest)
	}
}

func TestSnapshotRestoreTypeChanges(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{
		Base:          "example/base:latest",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "fs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "replace-dir", "child.txt"), "old child\n")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "replace-file"), "old file\n")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "mode-file"), "mode\n")
	if err := os.Chmod(filepath.Join(fs, "agent", "workspace", "mode-file"), 0o644); err != nil {
		t.Fatal(err)
	}
	modeDir := filepath.Join(fs, "agent", "workspace", "mode-dir")
	if err := os.MkdirAll(modeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("old-target", filepath.Join(fs, "agent", "workspace", "replace-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("old-same-type-target", filepath.Join(fs, "agent", "workspace", "retarget-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001", AlsoTag: "main"}); err != nil {
		t.Fatal(err)
	}

	if err := os.RemoveAll(filepath.Join(fs, "agent", "workspace", "replace-dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(fs, "agent", "workspace", "replace-file")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(fs, "agent", "workspace", "replace-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(fs, "agent", "workspace", "retarget-link")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "replace-dir"), "new file\n")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "replace-file", "child.txt"), "new child\n")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "replace-link"), "new link file\n")
	if err := os.Chmod(filepath.Join(fs, "agent", "workspace", "mode-file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(modeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("new-same-type-target", filepath.Join(fs, "agent", "workspace", "retarget-link")); err != nil {
		t.Fatal(err)
	}
	second, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000002", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}
	assertLayerEntries(t, root, second.ManifestDigest, []string{
		"agent/workspace/.wh.replace-dir",
		"agent/workspace/.wh.replace-file",
		"agent/workspace/.wh.replace-link",
		"agent/workspace/mode-dir",
		"agent/workspace/mode-file",
		"agent/workspace/replace-dir",
		"agent/workspace/replace-file",
		"agent/workspace/replace-file/child.txt",
		"agent/workspace/replace-link",
		"agent/workspace/retarget-link",
	})

	restore := filepath.Join(root, "restore-type-change")
	if err := Restore(root, "main", restore, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(restore, "agent", "workspace", "replace-dir"), "new file\n")
	assertFile(t, filepath.Join(restore, "agent", "workspace", "replace-file", "child.txt"), "new child\n")
	assertFile(t, filepath.Join(restore, "agent", "workspace", "replace-link"), "new link file\n")
	linkTarget, err := os.Readlink(filepath.Join(restore, "agent", "workspace", "retarget-link"))
	if err != nil {
		t.Fatal(err)
	}
	if linkTarget != "new-same-type-target" {
		t.Fatalf("retarget-link target = %q", linkTarget)
	}
	assertMode(t, filepath.Join(restore, "agent", "workspace", "mode-file"), 0o600)
	assertMode(t, filepath.Join(restore, "agent", "workspace", "mode-dir"), 0o700)
}

func TestSnapshotRejectsReservedWhiteoutNames(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{
		Base:          "example/base:latest",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "fs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", ".wh.user-data"), "not a whiteout\n")

	_, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001"})
	if err == nil || !strings.Contains(err.Error(), "reserved overlay whiteout path") {
		t.Fatalf("expected reserved whiteout snapshot error, got %v", err)
	}
}

func TestExtractLayerRejectsSymlinkTraversal(t *testing.T) {
	restore := t.TempDir()
	outside := t.TempDir()
	layer := rawLayer(t, func(tw *tar.Writer) {
		if err := tw.WriteHeader(&tar.Header{
			Name:     "agent/workspace/link",
			Typeflag: tar.TypeSymlink,
			Linkname: outside,
			Mode:     0o777,
		}); err != nil {
			t.Fatal(err)
		}
		payload := []byte("escaped\n")
		if err := tw.WriteHeader(&tar.Header{
			Name:     "agent/workspace/link/owned.txt",
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     int64(len(payload)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(payload); err != nil {
			t.Fatal(err)
		}
	})

	err := extractLayer(layer, restore)
	if err == nil || !strings.Contains(err.Error(), "refusing to extract through symlink") {
		t.Fatalf("expected symlink traversal extraction error, got %v", err)
	}
	assertMissing(t, filepath.Join(outside, "owned.txt"))
}

func TestExtractLayerRejectsNonCanonicalPaths(t *testing.T) {
	for _, name := range []string{
		"agent/../workspace/file.txt",
		"/agent/workspace/file.txt",
		"agent//workspace/file.txt",
		"agent/./workspace/file.txt",
	} {
		t.Run(name, func(t *testing.T) {
			restore := t.TempDir()
			payload := []byte("bad\n")
			layer := rawLayer(t, func(tw *tar.Writer) {
				if err := tw.WriteHeader(&tar.Header{
					Name:     name,
					Typeflag: tar.TypeReg,
					Mode:     0o644,
					Size:     int64(len(payload)),
				}); err != nil {
					t.Fatal(err)
				}
				if _, err := tw.Write(payload); err != nil {
					t.Fatal(err)
				}
			})

			err := extractLayer(layer, restore)
			if err == nil || !strings.Contains(err.Error(), "unsafe tar path") {
				t.Fatalf("expected unsafe tar path error, got %v", err)
			}
			assertMissing(t, filepath.Join(restore, "agent", "workspace", "file.txt"))
			assertMissing(t, filepath.Join(restore, "workspace", "file.txt"))
		})
	}
}

func TestExtractLayerAcceptsCanonicalNestedPath(t *testing.T) {
	restore := t.TempDir()
	payload := []byte("ok\n")
	layer := rawLayer(t, func(tw *tar.Writer) {
		if err := tw.WriteHeader(&tar.Header{
			Name:     "agent/workspace/file.txt",
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     int64(len(payload)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(payload); err != nil {
			t.Fatal(err)
		}
	})

	if err := extractLayer(layer, restore); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(restore, "agent", "workspace", "file.txt"), "ok\n")
}

func TestExtractLayerRejectsMalformedWhiteout(t *testing.T) {
	restore := t.TempDir()
	mustWrite(t, filepath.Join(restore, "agent", "workspace", "keep.txt"), "keep\n")
	layer := rawLayer(t, func(tw *tar.Writer) {
		if err := tw.WriteHeader(&tar.Header{
			Name:     "agent/workspace/.wh..",
			Typeflag: tar.TypeReg,
			Mode:     0,
			Size:     0,
		}); err != nil {
			t.Fatal(err)
		}
	})

	err := extractLayer(layer, restore)
	if err == nil || !strings.Contains(err.Error(), "invalid whiteout") {
		t.Fatalf("expected invalid whiteout error, got %v", err)
	}
	assertFile(t, filepath.Join(restore, "agent", "workspace", "keep.txt"), "keep\n")
}

func TestExtractLayerRejectsInvalidWhiteoutHeader(t *testing.T) {
	for _, tc := range []struct {
		name    string
		header  tar.Header
		payload []byte
	}{
		{
			name: "directory",
			header: tar.Header{
				Name:     "agent/workspace/.wh.remove.txt",
				Typeflag: tar.TypeDir,
				Mode:     0o755,
			},
		},
		{
			name: "payload",
			header: tar.Header{
				Name:     "agent/workspace/.wh.remove.txt",
				Typeflag: tar.TypeReg,
				Mode:     0,
				Size:     int64(len("payload")),
			},
			payload: []byte("payload"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restore := t.TempDir()
			mustWrite(t, filepath.Join(restore, "agent", "workspace", "remove.txt"), "keep\n")
			layer := rawLayer(t, func(tw *tar.Writer) {
				if err := tw.WriteHeader(&tc.header); err != nil {
					t.Fatal(err)
				}
				if len(tc.payload) > 0 {
					if _, err := tw.Write(tc.payload); err != nil {
						t.Fatal(err)
					}
				}
			})

			err := extractLayer(layer, restore)
			if err == nil || !strings.Contains(err.Error(), "invalid whiteout") {
				t.Fatalf("expected invalid whiteout header error, got %v", err)
			}
			assertFile(t, filepath.Join(restore, "agent", "workspace", "remove.txt"), "keep\n")
		})
	}
}

func TestExtractLayerRejectsWhiteoutThroughSymlinkParent(t *testing.T) {
	restore := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "owned.txt"), "outside\n")
	layer := rawLayer(t, func(tw *tar.Writer) {
		if err := tw.WriteHeader(&tar.Header{
			Name:     "agent/workspace/link",
			Typeflag: tar.TypeSymlink,
			Linkname: outside,
			Mode:     0o777,
		}); err != nil {
			t.Fatal(err)
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:     "agent/workspace/link/.wh.owned.txt",
			Typeflag: tar.TypeReg,
			Mode:     0,
			Size:     0,
		}); err != nil {
			t.Fatal(err)
		}
	})

	err := extractLayer(layer, restore)
	if err == nil || !strings.Contains(err.Error(), "refusing to extract through symlink") {
		t.Fatalf("expected symlink whiteout parent error, got %v", err)
	}
	assertFile(t, filepath.Join(outside, "owned.txt"), "outside\n")
}

func TestExtractLayerAppliesValidWhiteout(t *testing.T) {
	restore := t.TempDir()
	mustWrite(t, filepath.Join(restore, "agent", "workspace", "remove.txt"), "remove\n")
	mustWrite(t, filepath.Join(restore, "agent", "workspace", "nested", "remove.txt"), "remove nested\n")
	layer := rawLayer(t, func(tw *tar.Writer) {
		if err := tw.WriteHeader(&tar.Header{
			Name:     "agent/workspace/.wh.remove.txt",
			Typeflag: tar.TypeReg,
			Mode:     0,
			Size:     0,
		}); err != nil {
			t.Fatal(err)
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:     "agent/workspace/nested/.wh.remove.txt",
			Typeflag: tar.TypeReg,
			Mode:     0,
			Size:     0,
		}); err != nil {
			t.Fatal(err)
		}
	})

	if err := extractLayer(layer, restore); err != nil {
		t.Fatal(err)
	}
	assertMissing(t, filepath.Join(restore, "agent", "workspace", "remove.txt"))
	assertMissing(t, filepath.Join(restore, "agent", "workspace", "nested", "remove.txt"))
}

func TestAgeEncryptedSnapshotRestore(t *testing.T) {
	root := t.TempDir()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	identityPath := filepath.Join(root, "age.key")
	if err := os.WriteFile(identityPath, []byte(identity.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(root, InitOptions{
		Base:          "example/base:latest",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "fs"),
		DefaultBranch: "main",
		Encrypt:       "age:" + identity.Recipient().String(),
	}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "secret.txt"), "classified\n")
	result, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	_, manifest, _, err := s.loadManifest(result.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Layers[0].MediaType != MediaTypeLayerEnc {
		t.Fatalf("layer was not encrypted: %s", manifest.Layers[0].MediaType)
	}
	layerData, err := s.readBlob(manifest.Layers[0].Digest)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(layerData, []byte("classified")) {
		t.Fatalf("encrypted layer leaked plaintext")
	}
	if err := Restore(root, "snap-000001", filepath.Join(root, "restore-no-key"), RestoreOptions{}); err == nil {
		t.Fatalf("restore without decrypt identity succeeded unexpectedly")
	}
	restoreDir := filepath.Join(root, "restore")
	if err := Restore(root, "snap-000001", restoreDir, RestoreOptions{Decrypt: identityPath}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(restoreDir, "agent", "workspace", "secret.txt"), "classified\n")
}

func TestKMSStyleEncryptedSnapshotRestore(t *testing.T) {
	root := t.TempDir()
	recipient := "kms:aws:kms:us-east-1:123456789012:key/demo"
	if _, err := Init(root, InitOptions{
		Base:          "example/base:latest",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "fs"),
		DefaultBranch: "main",
		Encrypt:       recipient,
	}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "kms.txt"), "kms protected\n")
	result, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	_, manifest, _, err := s.loadManifest(result.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Layers[0].Annotations["com.osix.encryption.keywrap"] != "aws-kms" {
		t.Fatalf("unexpected keywrap annotations: %#v", manifest.Layers[0].Annotations)
	}
	restoreDir := filepath.Join(root, "restore")
	if err := Restore(root, "snap-000001", restoreDir, RestoreOptions{Decrypt: recipient}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(restoreDir, "agent", "workspace", "kms.txt"), "kms protected\n")
}

func TestMixedRecipientEncryptedSnapshotRestore(t *testing.T) {
	root := t.TempDir()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	identityPath := filepath.Join(root, "age.key")
	if err := os.WriteFile(identityPath, []byte(identity.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	kmsRecipient := "kms:aws:kms:us-east-1:123456789012:key/demo"
	gpgRecipient := "gpg:alice@example.com"
	endpointRecipient := "endpoint:https://keys.example.test/wrap"
	recipients := strings.Join([]string{
		"age:" + identity.Recipient().String(),
		kmsRecipient,
		gpgRecipient,
		endpointRecipient,
	}, ",")
	if _, err := Init(root, InitOptions{
		Base:          "example/base:latest",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "fs"),
		DefaultBranch: "main",
		Encrypt:       recipients,
	}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "multi.txt"), "multi recipient protected\n")
	result, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	_, manifest, _, err := s.loadManifest(result.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	annotations := manifest.Layers[0].Annotations
	if annotations["com.osix.encryption.keywrap"] != "osix-envelope" {
		t.Fatalf("unexpected keywrap annotations: %#v", annotations)
	}
	if annotations["com.osix.encryption.recipient_types"] != "age,kms,gpg,endpoint" {
		t.Fatalf("unexpected recipient types: %#v", annotations)
	}
	layerData, err := s.readBlob(manifest.Layers[0].Digest)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(layerData, []byte("multi recipient protected")) {
		t.Fatalf("encrypted layer leaked plaintext")
	}
	for name, decrypt := range map[string]string{
		"age":      identityPath,
		"kms":      kmsRecipient,
		"gpg":      gpgRecipient,
		"endpoint": endpointRecipient,
	} {
		t.Run(name, func(t *testing.T) {
			restoreDir := filepath.Join(root, "restore-"+name)
			if err := Restore(root, "snap-000001", restoreDir, RestoreOptions{Decrypt: decrypt}); err != nil {
				t.Fatal(err)
			}
			assertFile(t, filepath.Join(restoreDir, "agent", "workspace", "multi.txt"), "multi recipient protected\n")
		})
	}
}

func TestSignedSnapshotVerifyAndProvenance(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{
		Base:          "example/base:latest",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "fs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "signed.txt"), "signed\n")
	result, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001", Sign: "keyless", Attest: "slsa"})
	if err != nil {
		t.Fatal(err)
	}
	verify, err := VerifySnapshot(root, result.ManifestDigest, VerifyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if verify.SignatureDigest == "" || verify.ProvenanceDigest == "" || verify.Signer != "keyless-local" {
		t.Fatalf("unexpected verify result: %#v", verify)
	}
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	pubKeyPath := filepath.Join(s.root, "keys", "keyless_ed25519.pub")
	if _, err := VerifySnapshot(root, "snap-000001", VerifyOptions{TrustedKey: pubKeyPath}); err != nil {
		t.Fatal(err)
	}
	wrongKey := filepath.Join(root, "wrong.pub")
	if err := os.WriteFile(wrongKey, []byte("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifySnapshot(root, "snap-000001", VerifyOptions{TrustedKey: wrongKey}); err == nil {
		t.Fatalf("verify with wrong trusted key succeeded unexpectedly")
	}
}

func TestValidateChainAndExpectedParentConflict(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{
		Base:          "example/base:latest",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "fs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "v1\n")
	first, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateChain(root, "snap-000001"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "v2\n")
	second, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000002", AlsoTag: "main", ExpectedParent: first.ManifestDigest})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateChain(root, "main"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "v3\n")
	if _, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000003", AlsoTag: "main", ExpectedParent: first.ManifestDigest}); err == nil {
		t.Fatalf("expected branch conflict")
	}
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if mainDigest, err := s.resolveRef("main"); err != nil || mainDigest != second.ManifestDigest {
		t.Fatalf("main changed after expected-parent conflict: digest=%s err=%v", mainDigest, err)
	}
	if _, err := s.resolveRef("snap-000003"); err == nil {
		t.Fatal("snapshot tag survived expected-parent transaction failure")
	}
	digest, err := Fork(root, "snap-000001", "experiment")
	if err != nil {
		t.Fatal(err)
	}
	if digest != first.ManifestDigest {
		t.Fatalf("fork mismatch: %s != %s", digest, first.ManifestDigest)
	}
	if err := ValidateChain(root, second.ManifestDigest); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s: want %q got %q", path, want, string(got))
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Fatalf("%s exists unexpectedly", path)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func assertMode(t *testing.T, path string, want fs.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func changeStrings(changes []Change) []string {
	out := make([]string, len(changes))
	for i, change := range changes {
		out[i] = strings.TrimSpace(change.Kind + " " + change.Path)
	}
	return out
}

func assertLayerEntries(t *testing.T, root, manifestDigest string, want []string) {
	t.Helper()
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	_, manifest, _, err := s.loadManifest(manifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	layerData, err := s.readBlob(manifest.Layers[0].Digest)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zstd.NewReader(bytes.NewReader(layerData))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	var got []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, hdr.Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("layer entries mismatch\nwant: %#v\n got: %#v", want, got)
	}
}

func rawLayer(t *testing.T, write func(*tar.Writer)) []byte {
	t.Helper()
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	write(tw)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var zstdBuf bytes.Buffer
	zw, err := zstd.NewWriter(&zstdBuf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(tarBuf.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return zstdBuf.Bytes()
}
