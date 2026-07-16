package osix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListAndExtractSnapshotDirectoryAcrossLayers(t *testing.T) {
	root := t.TempDir()
	fsRoot := filepath.Join(root, "agentfs")
	if _, err := Init(root, InitOptions{
		Base:          "base",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         fsRoot,
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fsRoot, "agent", "workspace", "docs", "keep.txt"), "keep-v1\n")
	mustWrite(t, filepath.Join(fsRoot, "agent", "workspace", "docs", "delete.txt"), "delete-me\n")
	mustWrite(t, filepath.Join(fsRoot, "agent", "workspace", "other.txt"), "outside selection\n")
	if err := os.MkdirAll(filepath.Join(fsRoot, "agent", "workspace", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Snapshot(root, fsRoot, SnapshotOptions{Tag: "extract-v1"}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fsRoot, "agent", "workspace", "docs", "keep.txt"), "keep-v2\n")
	if err := os.Remove(filepath.Join(fsRoot, "agent", "workspace", "docs", "delete.txt")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fsRoot, "agent", "workspace", "docs", "new.txt"), "new-v2\n")
	second, err := Snapshot(root, fsRoot, SnapshotOptions{Tag: "extract-v2"})
	if err != nil {
		t.Fatal(err)
	}

	digest, entries, err := ListSnapshotDirectory(root, "extract-v2", "/agent/workspace/docs/")
	if err != nil {
		t.Fatal(err)
	}
	if digest != second.ManifestDigest {
		t.Fatalf("listed digest %s, want %s", digest, second.ManifestDigest)
	}
	if len(entries) != 2 || entries[0].Path != "agent/workspace/docs/keep.txt" || entries[1].Path != "agent/workspace/docs/new.txt" {
		t.Fatalf("unexpected directory listing: %#v", entries)
	}
	_, emptyEntries, err := ListSnapshotDirectory(root, "extract-v2", "agent/workspace/empty/")
	if err != nil {
		t.Fatal(err)
	}
	if emptyEntries == nil || len(emptyEntries) != 0 {
		t.Fatalf("empty directory listing = %#v, want non-nil empty slice", emptyEntries)
	}

	destination := filepath.Join(root, "selected-docs")
	result, err := ExtractSnapshotPath(root, "extract-v2", "agent/workspace/docs/", destination, ExtractOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceDigest != second.ManifestDigest || result.Files != 2 || result.Entries != 3 {
		t.Fatalf("unexpected extraction result: %#v", result)
	}
	assertFile(t, filepath.Join(destination, "keep.txt"), "keep-v2\n")
	assertFile(t, filepath.Join(destination, "new.txt"), "new-v2\n")
	if _, err := os.Stat(filepath.Join(destination, "delete.txt")); !os.IsNotExist(err) {
		t.Fatalf("deleted file was extracted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "other.txt")); !os.IsNotExist(err) {
		t.Fatalf("file outside selected directory was extracted: %v", err)
	}

	if _, err := ExtractSnapshotPath(root, "extract-v2", "agent/workspace/docs", destination, ExtractOptions{}); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected existing destination error, got %v", err)
	}
	mustWrite(t, filepath.Join(destination, "stale.txt"), "stale\n")
	if _, err := ExtractSnapshotPath(root, "extract-v2", "agent/workspace/docs", destination, ExtractOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("forced extraction retained stale file: %v", err)
	}

	backup := extractBackupPath(destination)
	if err := os.Rename(destination, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractSnapshotPath(root, "extract-v2", "agent/workspace/docs", destination, ExtractOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(destination, "keep.txt"), "keep-v2\n")
	if _, err := os.Lstat(backup); !os.IsNotExist(err) {
		t.Fatalf("interrupted extraction backup was not cleaned up: %v", err)
	}
}

func TestExtractSnapshotFileAndRejectUnsafeSelection(t *testing.T) {
	root := t.TempDir()
	fsRoot := filepath.Join(root, "agentfs")
	if _, err := Init(root, InitOptions{Base: "base", Name: "agent", StateRef: "local/agent", Mount: fsRoot, DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fsRoot, "agent", "workspace", "one.txt"), "one\n")
	if _, err := Snapshot(root, fsRoot, SnapshotOptions{Tag: "one"}); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "one-copy.txt")
	result, err := ExtractSnapshotPath(root, "one", "/agent/workspace/one.txt", destination, ExtractOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 1 || result.Bytes != 4 {
		t.Fatalf("unexpected file extraction result: %#v", result)
	}
	assertFile(t, destination, "one\n")

	for _, unsafe := range []string{"../one.txt", "agent/../one.txt", "//etc/passwd"} {
		if _, err := ExtractSnapshotPath(root, "one", unsafe, filepath.Join(root, "unsafe"), ExtractOptions{}); err == nil {
			t.Fatalf("unsafe path %q was accepted", unsafe)
		}
	}
}
