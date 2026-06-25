package osix

import (
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
)

func TestSnapshotLowerStoreReadsTreeWithoutFetchingLazyLayer(t *testing.T) {
	reg := newFakeRegistry()
	server := httptest.NewServer(reg)
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo := u.Host + "/acme/lower-store-agent-state"

	source := t.TempDir()
	if _, err := Init(source, InitOptions{
		Base:          "example/base:latest",
		Name:          "agent",
		StateRef:      repo,
		Mount:         filepath.Join(source, "agentfs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(source, "agentfs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "notes.md"), "lazy lower store\n")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "todo.txt"), "todo\n")
	snap, err := Snapshot(source, fs, SnapshotOptions{Tag: "lower-store-lazy", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}
	sourceStore, err := findStore(source)
	if err != nil {
		t.Fatal(err)
	}
	_, manifest, _, err := sourceStore.loadManifest(snap.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	layerDigest := manifest.Layers[0].Digest
	if err := PushSnapshot(source, repo, snap.ManifestDigest, snap.Tags); err != nil {
		t.Fatal(err)
	}
	reg.blobGets[layerDigest] = 0

	dest := t.TempDir()
	if _, err := Init(dest, InitOptions{
		Base:          "example/base:latest",
		Name:          "agent",
		StateRef:      repo,
		Mount:         filepath.Join(dest, "agentfs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := PullSnapshotWithOptions(dest, repo+":lower-store-lazy", "lower-store-lazy", PullOptions{Lazy: true}); err != nil {
		t.Fatal(err)
	}
	destStore, err := findStore(dest)
	if err != nil {
		t.Fatal(err)
	}
	if destStore.hasBlob(layerDigest) {
		t.Fatal("lazy pull stored layer before lower-store open")
	}

	lower, err := OpenSnapshotLowerStore(dest, "lower-store-lazy", ReadFileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if lower.Digest() != snap.ManifestDigest {
		t.Fatalf("lower digest = %s, want %s", lower.Digest(), snap.ManifestDigest)
	}
	entry, found, err := lower.Lookup("/agent/workspace/notes.md")
	if err != nil {
		t.Fatal(err)
	}
	if !found || entry.Type != "file" || entry.Size != int64(len("lazy lower store\n")) {
		t.Fatalf("unexpected lookup entry: found=%v %#v", found, entry)
	}
	children, err := lower.ReadDir("agent/workspace")
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 2 || children[0].Path != "agent/workspace/notes.md" || children[1].Path != "agent/workspace/todo.txt" {
		t.Fatalf("unexpected lower dir children: %#v", children)
	}
	if got := reg.blobGets[layerDigest]; got != 0 {
		t.Fatalf("metadata lower-store operations fetched layer %s %d times", layerDigest, got)
	}
	if destStore.hasBlob(layerDigest) {
		t.Fatal("metadata lower-store operations cached layer")
	}

	data, err := lower.ReadFile("agent/workspace/notes.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "lazy lower store\n" {
		t.Fatalf("unexpected lower file data: %q", data)
	}
	if got := reg.blobGets[layerDigest]; got != 1 {
		t.Fatalf("lower-store file read fetched layer %s %d times, want 1", layerDigest, got)
	}
	if !destStore.hasBlob(layerDigest) {
		t.Fatal("lower-store file read did not cache fetched layer")
	}
}

func TestSnapshotLowerStoreValidatesPathsAndTypes(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{
		Base:          "base",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "fs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "file\n")
	snap, err := Snapshot(root, fs, SnapshotOptions{Tag: "lower-store"})
	if err != nil {
		t.Fatal(err)
	}
	lower, err := OpenSnapshotLowerStore(root, snap.ManifestDigest, ReadFileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rootEntry, found, err := lower.Lookup("/")
	if err != nil {
		t.Fatal(err)
	}
	if !found || rootEntry.Type != "dir" {
		t.Fatalf("unexpected root entry: found=%v %#v", found, rootEntry)
	}
	if _, err := lower.ReadFile("agent/workspace"); err == nil {
		t.Fatal("expected directory read as file to fail")
	}
	if _, err := lower.ReadDir("agent/workspace/file.txt"); err == nil {
		t.Fatal("expected file read as directory to fail")
	}
	if _, _, err := lower.Lookup("../escape"); err == nil {
		t.Fatal("expected unsafe lookup path to fail")
	}
}
