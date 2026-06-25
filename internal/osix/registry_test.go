package osix

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPushPullSnapshotThroughOCIRegistry(t *testing.T) {
	reg := newFakeRegistry()
	server := httptest.NewServer(reg)
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo := u.Host + "/acme/agent-state"

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
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "notes.md"), "v1\n")
	first, err := Snapshot(source, fs, SnapshotOptions{Tag: "snap-000001", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := PushSnapshot(source, repo, first.ManifestDigest, first.Tags); err != nil {
		t.Fatal(err)
	}

	mustWrite(t, filepath.Join(fs, "agent", "workspace", "notes.md"), "v2\n")
	second, err := Snapshot(source, fs, SnapshotOptions{Tag: "snap-000002", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := PushSnapshot(source, repo, second.ManifestDigest, second.Tags); err != nil {
		t.Fatal(err)
	}

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
	pulled, err := PullSnapshot(dest, repo+":snap-000002", "main")
	if err != nil {
		t.Fatal(err)
	}
	if pulled != second.ManifestDigest {
		t.Fatalf("pulled digest mismatch: want %s got %s", second.ManifestDigest, pulled)
	}
	restoreDir := filepath.Join(dest, "restore")
	if err := Restore(dest, "main", restoreDir, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(restoreDir, "agent", "workspace", "notes.md"), "v2\n")
	refs, err := Refs(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRef(refs, "snap-000001") || !hasRef(refs, "snap-000002") || !hasRef(refs, "main") {
		t.Fatalf("expected pulled chain refs, got %#v", refs)
	}
}

func TestPushPullSnapshotThroughBearerAuthRegistry(t *testing.T) {
	reg := newAuthFakeRegistry()
	server := httptest.NewServer(reg)
	defer server.Close()
	reg.realm = server.URL + "/token"
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo := u.Host + "/acme/private-agent-state"
	t.Setenv("OSIX_REGISTRY_USERNAME", "robot")
	t.Setenv("OSIX_REGISTRY_PASSWORD", "secret")

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
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "private.md"), "private\n")
	snap, err := Snapshot(source, fs, SnapshotOptions{Tag: "snap-private", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := PushSnapshot(source, repo, "main", []string{"release"}); err != nil {
		t.Fatal(err)
	}

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
	pulled, err := PullSnapshot(dest, repo+":release", "release")
	if err != nil {
		t.Fatal(err)
	}
	if pulled != snap.ManifestDigest {
		t.Fatalf("pulled digest mismatch: want %s got %s", snap.ManifestDigest, pulled)
	}
	if reg.tokenRequests == 0 {
		t.Fatal("expected bearer token endpoint to be used")
	}
	if reg.basicTokenRequests == 0 {
		t.Fatal("expected token request to include basic credentials")
	}
	if reg.bearerRegistryRequests == 0 {
		t.Fatal("expected registry requests to retry with bearer token")
	}
}

func TestPushSnapshotExpectedParentChecksRemoteTag(t *testing.T) {
	reg := newFakeRegistry()
	server := httptest.NewServer(reg)
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo := u.Host + "/acme/conflict-agent-state"

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
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "notes.md"), "v1\n")
	first, err := Snapshot(source, fs, SnapshotOptions{Tag: "snap-000001", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := PushSnapshot(source, repo, first.ManifestDigest, first.Tags); err != nil {
		t.Fatal(err)
	}

	mustWrite(t, filepath.Join(fs, "agent", "workspace", "notes.md"), "v2\n")
	second, err := Snapshot(source, fs, SnapshotOptions{Tag: "snap-000002", AlsoTag: "main", ExpectedParent: first.ManifestDigest})
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "notes.md"), "v3\n")
	third, err := Snapshot(source, fs, SnapshotOptions{Tag: "snap-000003", AlsoTag: "main", ExpectedParent: second.ManifestDigest})
	if err != nil {
		t.Fatal(err)
	}
	if err := PushSnapshot(source, repo, third.ManifestDigest, third.Tags); err != nil {
		t.Fatal(err)
	}

	err = PushSnapshotWithOptions(source, repo, second.ManifestDigest, []string{"main"}, PushOptions{ExpectedParent: first.ManifestDigest})
	if err == nil || !strings.Contains(err.Error(), "remote branch conflict for main") {
		t.Fatalf("expected remote conflict, got %v", err)
	}
	if !IsRemoteBranchConflict(err) {
		t.Fatalf("expected typed remote conflict, got %T: %v", err, err)
	}
	var conflict RemoteBranchConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected RemoteBranchConflictError, got %T: %v", err, err)
	}
	if conflict.Tag != "main" || conflict.Expected != first.ManifestDigest || conflict.Current != third.ManifestDigest {
		t.Fatalf("unexpected conflict details: %#v", conflict)
	}
	_, remoteMain, err := newRegistryClient(u.Host, "acme/conflict-agent-state").getManifest("main")
	if err != nil {
		t.Fatal(err)
	}
	if remoteMain != third.ManifestDigest {
		t.Fatalf("remote main moved after failed precondition: got %s want %s", remoteMain, third.ManifestDigest)
	}
}

func TestPruneRemoteSnapshotsDeletesManifestDigest(t *testing.T) {
	reg := newFakeRegistry()
	server := httptest.NewServer(reg)
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo := u.Host + "/acme/agent-state"
	root := t.TempDir()
	if _, err := Init(root, InitOptions{Base: "base", Name: "agent", StateRef: repo, Mount: filepath.Join(root, "fs"), DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "v1\n")
	snap, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001"})
	if err != nil {
		t.Fatal(err)
	}
	if err := PushSnapshot(root, repo, snap.ManifestDigest, snap.Tags); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.manifests["acme/agent-state@"+snap.ManifestDigest]; !ok {
		t.Fatalf("expected remote manifest %s before prune", snap.ManifestDigest)
	}
	deleted, err := PruneRemoteSnapshots(repo, []string{snap.ManifestDigest})
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(stringsJoin(deleted), snap.ManifestDigest) {
		t.Fatalf("deleted set missing %s: %#v", snap.ManifestDigest, deleted)
	}
	if _, ok := reg.manifests["acme/agent-state@"+snap.ManifestDigest]; ok {
		t.Fatalf("remote manifest %s was not deleted", snap.ManifestDigest)
	}
	if _, ok := reg.manifests["acme/agent-state:snap-000001"]; ok {
		t.Fatalf("remote tag for pruned manifest was not deleted")
	}
}

func TestLazyPullReadsRemoteFileOnDemand(t *testing.T) {
	reg := newFakeRegistry()
	server := httptest.NewServer(reg)
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo := u.Host + "/acme/lazy-agent-state"

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
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "notes.md"), "lazy remote\n")
	snap, err := Snapshot(source, fs, SnapshotOptions{Tag: "snap-lazy", AlsoTag: "main"})
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
	pulled, err := PullSnapshotWithOptions(dest, repo+":snap-lazy", "lazy", PullOptions{Lazy: true})
	if err != nil {
		t.Fatal(err)
	}
	if pulled != snap.ManifestDigest {
		t.Fatalf("pulled digest mismatch: want %s got %s", snap.ManifestDigest, pulled)
	}
	if got := reg.blobGets[layerDigest]; got != 0 {
		t.Fatalf("lazy pull fetched layer %s %d times", layerDigest, got)
	}
	destStore, err := findStore(dest)
	if err != nil {
		t.Fatal(err)
	}
	if destStore.hasBlob(layerDigest) {
		t.Fatal("lazy pull stored layer before file read")
	}
	data, err := ReadSnapshotFile(dest, "lazy", "/agent/workspace/notes.md", ReadFileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "lazy remote\n" {
		t.Fatalf("unexpected lazy file data: %q", data)
	}
	if got := reg.blobGets[layerDigest]; got != 1 {
		t.Fatalf("lazy file read fetched layer %s %d times, want 1", layerDigest, got)
	}
	if !destStore.hasBlob(layerDigest) {
		t.Fatal("lazy file read did not cache fetched layer")
	}
	if _, err := ReadSnapshotFile(dest, "lazy", "agent/workspace/notes.md", ReadFileOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := reg.blobGets[layerDigest]; got != 1 {
		t.Fatalf("cached lazy file read fetched layer %s again; got %d fetches", layerDigest, got)
	}
}

func TestLazyPullRestoreFetchesMissingRemoteLayer(t *testing.T) {
	reg := newFakeRegistry()
	server := httptest.NewServer(reg)
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo := u.Host + "/acme/lazy-restore-agent-state"

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
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "notes.md"), "lazy restore\n")
	snap, err := Snapshot(source, fs, SnapshotOptions{Tag: "snap-lazy-restore", AlsoTag: "main"})
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
	if _, err := PullSnapshotWithOptions(dest, repo+":snap-lazy-restore", "lazy-restore", PullOptions{Lazy: true}); err != nil {
		t.Fatal(err)
	}
	destStore, err := findStore(dest)
	if err != nil {
		t.Fatal(err)
	}
	if destStore.hasBlob(layerDigest) {
		t.Fatal("lazy pull stored layer before restore")
	}
	restoreDir := filepath.Join(dest, "restore")
	if err := Restore(dest, "lazy-restore", restoreDir, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(restoreDir, "agent", "workspace", "notes.md"), "lazy restore\n")
	if got := reg.blobGets[layerDigest]; got != 1 {
		t.Fatalf("lazy restore fetched layer %s %d times, want 1", layerDigest, got)
	}
	if !destStore.hasBlob(layerDigest) {
		t.Fatal("lazy restore did not cache fetched layer")
	}
}

func TestLazyReadMissingRemoteBlobFailsClosed(t *testing.T) {
	reg := newFakeRegistry()
	server := httptest.NewServer(reg)
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo := u.Host + "/acme/lazy-missing-agent-state"

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
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "notes.md"), "missing remote\n")
	snap, err := Snapshot(source, fs, SnapshotOptions{Tag: "snap-lazy-missing", AlsoTag: "main"})
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
	if _, err := PullSnapshotWithOptions(dest, repo+":snap-lazy-missing", "lazy-missing", PullOptions{Lazy: true}); err != nil {
		t.Fatal(err)
	}
	delete(reg.blobs, layerDigest)

	_, err = ReadSnapshotFile(dest, "lazy-missing", "agent/workspace/notes.md", ReadFileOptions{})
	if err == nil || !strings.Contains(err.Error(), "read blob "+layerDigest) {
		t.Fatalf("expected missing local blob error after remote fetch failure, got %v", err)
	}
	if err := Restore(dest, "lazy-missing", filepath.Join(dest, "restore"), RestoreOptions{}); err == nil || !strings.Contains(err.Error(), "read blob "+layerDigest) {
		t.Fatalf("expected restore missing local blob error after remote fetch failure, got %v", err)
	}
}

func TestLazyPullReadsRemoteEncryptedFileOnDemand(t *testing.T) {
	reg := newFakeRegistry()
	server := httptest.NewServer(reg)
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo := u.Host + "/acme/encrypted-lazy-agent-state"

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
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "secret.txt"), "lazy encrypted remote\n")
	snap, err := Snapshot(source, fs, SnapshotOptions{
		Tag:     "enc-lazy",
		AlsoTag: "main",
		Encrypt: "gpg:test-recipient",
	})
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
	lazyRecord, err := readEncryptedLazyIndexRecord(sourceStore, snap.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if lazyRecord.IndexDigest == "" || len(lazyRecord.Files) == 0 {
		t.Fatalf("snapshot did not create encrypted lazy index: %#v", lazyRecord)
	}
	var lazyFileDigest string
	for _, entry := range lazyRecord.Files {
		if entry.Path == "agent/workspace/secret.txt" {
			lazyFileDigest = entry.Digest
			break
		}
	}
	if lazyFileDigest == "" {
		t.Fatalf("encrypted lazy index missing file entry: %#v", lazyRecord.Files)
	}
	if err := PushSnapshot(source, repo, snap.ManifestDigest, snap.Tags); err != nil {
		t.Fatal(err)
	}
	if got := len(reg.referrers["acme/encrypted-lazy-agent-state@"+snap.ManifestDigest]); got != 1 {
		t.Fatalf("expected one encrypted lazy referrer, got %d", got)
	}
	reg.blobGets[layerDigest] = 0
	reg.blobGets[lazyFileDigest] = 0

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
	pulled, err := PullSnapshotWithOptions(dest, repo+":enc-lazy", "enc-lazy", PullOptions{Lazy: true})
	if err != nil {
		t.Fatal(err)
	}
	if pulled != snap.ManifestDigest {
		t.Fatalf("pulled digest mismatch: want %s got %s", snap.ManifestDigest, pulled)
	}
	if got := reg.blobGets[layerDigest]; got != 0 {
		t.Fatalf("lazy encrypted pull fetched whole layer %s %d times", layerDigest, got)
	}
	destStore, err := findStore(dest)
	if err != nil {
		t.Fatal(err)
	}
	if destStore.hasBlob(layerDigest) {
		t.Fatal("lazy encrypted pull stored whole layer before file read")
	}
	if destStore.hasBlob(lazyFileDigest) {
		t.Fatal("lazy encrypted pull stored per-file blob before file read")
	}
	data, err := ReadSnapshotFile(dest, "enc-lazy", "agent/workspace/secret.txt", ReadFileOptions{Decrypt: "gpg:test-recipient"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "lazy encrypted remote\n" {
		t.Fatalf("unexpected encrypted lazy file data: %q", data)
	}
	if got := reg.blobGets[layerDigest]; got != 0 {
		t.Fatalf("lazy encrypted file read fetched whole layer %s %d times", layerDigest, got)
	}
	if got := reg.blobGets[lazyFileDigest]; got != 1 {
		t.Fatalf("lazy encrypted file read fetched per-file blob %s %d times, want 1", lazyFileDigest, got)
	}
	if destStore.hasBlob(layerDigest) {
		t.Fatal("lazy encrypted file read cached whole layer")
	}
	if !destStore.hasBlob(lazyFileDigest) {
		t.Fatal("lazy encrypted file read did not cache per-file blob")
	}
}

func TestLazyPullRestoresRemoteEncryptedFileOnDemand(t *testing.T) {
	reg := newFakeRegistry()
	server := httptest.NewServer(reg)
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo := u.Host + "/acme/encrypted-lazy-restore-agent-state"

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
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "secret.txt"), "lazy encrypted restore remote\n")
	snap, err := Snapshot(source, fs, SnapshotOptions{
		Tag:     "enc-lazy-restore",
		AlsoTag: "main",
		Encrypt: "gpg:test-recipient",
	})
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
	lazyRecord, err := readEncryptedLazyIndexRecord(sourceStore, snap.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	var lazyFileDigest string
	for _, entry := range lazyRecord.Files {
		if entry.Path == "agent/workspace/secret.txt" {
			lazyFileDigest = entry.Digest
			break
		}
	}
	if lazyFileDigest == "" {
		t.Fatalf("encrypted lazy index missing file entry: %#v", lazyRecord.Files)
	}
	if err := PushSnapshot(source, repo, snap.ManifestDigest, snap.Tags); err != nil {
		t.Fatal(err)
	}
	reg.blobGets[layerDigest] = 0
	reg.blobGets[lazyFileDigest] = 0

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
	if _, err := PullSnapshotWithOptions(dest, repo+":enc-lazy-restore", "enc-lazy-restore", PullOptions{Lazy: true}); err != nil {
		t.Fatal(err)
	}
	destStore, err := findStore(dest)
	if err != nil {
		t.Fatal(err)
	}
	if destStore.hasBlob(layerDigest) {
		t.Fatal("lazy encrypted restore pull stored whole layer before restore")
	}
	err = Restore(dest, "enc-lazy-restore", filepath.Join(dest, "wrong-restore"), RestoreOptions{Decrypt: "gpg:other-recipient"})
	if err == nil || !strings.Contains(err.Error(), "no matching decrypt material") {
		t.Fatalf("expected wrong decrypt material error, got %v", err)
	}
	if got := reg.blobGets[layerDigest]; got != 0 {
		t.Fatalf("wrong decrypt lazy encrypted restore fetched whole layer %s %d times", layerDigest, got)
	}
	if got := reg.blobGets[lazyFileDigest]; got != 0 {
		t.Fatalf("wrong decrypt lazy encrypted restore fetched per-file blob %s %d times", lazyFileDigest, got)
	}

	restore := filepath.Join(dest, "restore")
	if err := Restore(dest, "enc-lazy-restore", restore, RestoreOptions{Decrypt: "gpg:test-recipient"}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(restore, "agent", "workspace", "secret.txt"), "lazy encrypted restore remote\n")
	if got := reg.blobGets[layerDigest]; got != 0 {
		t.Fatalf("lazy encrypted restore fetched whole layer %s %d times", layerDigest, got)
	}
	if got := reg.blobGets[lazyFileDigest]; got != 1 {
		t.Fatalf("lazy encrypted restore fetched per-file blob %s %d times, want 1", lazyFileDigest, got)
	}
	if destStore.hasBlob(layerDigest) {
		t.Fatal("lazy encrypted restore cached whole layer")
	}
}

func TestLazyPullReadsRemoteEncryptedRangeChunkOnDemand(t *testing.T) {
	reg := newFakeRegistry()
	server := httptest.NewServer(reg)
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo := u.Host + "/acme/encrypted-lazy-range-agent-state"

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
	content := bytes.Repeat([]byte("abcdef0123456789"), encryptedLazyChunkSize/16*3)
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "large.bin"), string(content))
	snap, err := Snapshot(source, fs, SnapshotOptions{
		Tag:     "enc-range-lazy",
		AlsoTag: "main",
		Encrypt: "gpg:test-recipient",
	})
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
	lazyRecord, err := readEncryptedLazyIndexRecord(sourceStore, snap.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	var entry encryptedLazyFileEntry
	for _, candidate := range lazyRecord.Files {
		if candidate.Path == "agent/workspace/large.bin" {
			entry = candidate
			break
		}
	}
	if entry.Digest == "" || len(entry.Chunks) < 2 {
		t.Fatalf("encrypted lazy index missing chunked file entry: %#v", lazyRecord.Files)
	}
	if err := PushSnapshot(source, repo, snap.ManifestDigest, snap.Tags); err != nil {
		t.Fatal(err)
	}

	offset := int64(encryptedLazyChunkSize + 17)
	length := int64(123)
	selectedChunk := entry.Chunks[1]
	if selectedChunk.Offset > offset || selectedChunk.Offset+selectedChunk.PlaintextSize < offset+length {
		t.Fatalf("test range does not fit selected chunk: chunk=%#v offset=%d length=%d", selectedChunk, offset, length)
	}
	reg.blobGets[layerDigest] = 0
	reg.blobGets[entry.Digest] = 0
	for _, chunk := range entry.Chunks {
		reg.blobGets[chunk.Digest] = 0
	}

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
	pulled, err := PullSnapshotWithOptions(dest, repo+":enc-range-lazy", "enc-range-lazy", PullOptions{Lazy: true})
	if err != nil {
		t.Fatal(err)
	}
	if pulled != snap.ManifestDigest {
		t.Fatalf("pulled digest mismatch: want %s got %s", snap.ManifestDigest, pulled)
	}
	if got := reg.blobGets[layerDigest]; got != 0 {
		t.Fatalf("lazy encrypted range pull fetched whole layer %s %d times", layerDigest, got)
	}
	if got := reg.blobGets[entry.Digest]; got != 0 {
		t.Fatalf("lazy encrypted range pull fetched full per-file blob %s %d times", entry.Digest, got)
	}
	destStore, err := findStore(dest)
	if err != nil {
		t.Fatal(err)
	}
	if destStore.hasBlob(layerDigest) {
		t.Fatal("lazy encrypted range pull stored whole layer before range read")
	}
	if destStore.hasBlob(entry.Digest) {
		t.Fatal("lazy encrypted range pull stored full per-file blob before range read")
	}
	for _, chunk := range entry.Chunks {
		if got := reg.blobGets[chunk.Digest]; got != 0 {
			t.Fatalf("lazy encrypted range pull fetched chunk %s %d times", chunk.Digest, got)
		}
		if destStore.hasBlob(chunk.Digest) {
			t.Fatalf("lazy encrypted range pull stored chunk %s before range read", chunk.Digest)
		}
	}

	data, err := ReadSnapshotFileRange(dest, "enc-range-lazy", "agent/workspace/large.bin", offset, length, ReadFileOptions{Decrypt: "gpg:test-recipient"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(content[offset:offset+length]) {
		t.Fatalf("unexpected encrypted lazy range data: %q", data)
	}
	if got := reg.blobGets[layerDigest]; got != 0 {
		t.Fatalf("lazy encrypted range read fetched whole layer %s %d times", layerDigest, got)
	}
	if got := reg.blobGets[entry.Digest]; got != 0 {
		t.Fatalf("lazy encrypted range read fetched full per-file blob %s %d times", entry.Digest, got)
	}
	for _, chunk := range entry.Chunks {
		want := 0
		if chunk.Digest == selectedChunk.Digest {
			want = 1
		}
		if got := reg.blobGets[chunk.Digest]; got != want {
			t.Fatalf("chunk %s fetched %d times, want %d", chunk.Digest, got, want)
		}
		if chunk.Digest == selectedChunk.Digest {
			if !destStore.hasBlob(chunk.Digest) {
				t.Fatalf("range read did not cache selected chunk %s", chunk.Digest)
			}
			continue
		}
		if destStore.hasBlob(chunk.Digest) {
			t.Fatalf("range read cached unneeded chunk %s", chunk.Digest)
		}
	}
	if destStore.hasBlob(layerDigest) {
		t.Fatal("lazy encrypted range read cached whole layer")
	}
	if destStore.hasBlob(entry.Digest) {
		t.Fatal("lazy encrypted range read cached full per-file blob")
	}

	cached, err := ReadSnapshotFileRange(dest, "enc-range-lazy", "agent/workspace/large.bin", offset, length, ReadFileOptions{Decrypt: "gpg:test-recipient"})
	if err != nil {
		t.Fatal(err)
	}
	if string(cached) != string(content[offset:offset+length]) {
		t.Fatalf("unexpected cached encrypted lazy range data: %q", cached)
	}
	if got := reg.blobGets[selectedChunk.Digest]; got != 1 {
		t.Fatalf("cached range read fetched selected chunk again; got %d fetches", got)
	}
}

func TestPushPullSignedSnapshotReferrersThroughOCIRegistry(t *testing.T) {
	reg := newFakeRegistry()
	server := httptest.NewServer(reg)
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo := u.Host + "/acme/signed-agent-state"

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
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "signed.md"), "signed\n")
	snap, err := Snapshot(source, fs, SnapshotOptions{Tag: "signed", AlsoTag: "main", Sign: "keyless", Attest: "slsa"})
	if err != nil {
		t.Fatal(err)
	}
	verify, err := VerifySnapshot(source, "signed", VerifyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if verify.SignatureDigest == "" || verify.ProvenanceDigest == "" {
		t.Fatalf("expected local signature and provenance, got %#v", verify)
	}
	assertLocalSigstoreCompatibilityArtifacts(t, source, snap.ManifestDigest)
	cosignPub := filepath.Join(source, ".osix", "keys", "cosign_ecdsa_p256.pem.pub")
	cosignVerify, err := VerifySnapshot(source, "signed", VerifyOptions{TrustedKey: cosignPub})
	if err != nil {
		t.Fatal(err)
	}
	if cosignVerify.SignatureDigest == verify.SignatureDigest || cosignVerify.ProvenanceDigest == "" {
		t.Fatalf("expected cosign verification result with Sigstore provenance, got %#v", cosignVerify)
	}
	wrongPub := filepath.Join(source, "wrong-cosign.pub")
	writeWrongECDSAPublicKey(t, wrongPub)
	if _, err := VerifySnapshot(source, "signed", VerifyOptions{TrustedKey: wrongPub}); err == nil || !strings.Contains(err.Error(), "cosign signature verification failed") {
		t.Fatalf("expected wrong ECDSA key verification failure, got %v", err)
	}
	if err := PushSnapshot(source, repo, "signed", []string{"release"}); err != nil {
		t.Fatal(err)
	}
	if got := len(reg.referrers["acme/signed-agent-state@"+snap.ManifestDigest]); got != 4 {
		t.Fatalf("expected four indexed referrers, got %d", got)
	}
	cosignManifestData := reg.manifests["acme/signed-agent-state:"+cosignSignatureTag(snap.ManifestDigest)]
	if len(cosignManifestData) == 0 {
		t.Fatalf("missing cosign signature tag %s", cosignSignatureTag(snap.ManifestDigest))
	}
	var cosignManifest Manifest
	if err := json.Unmarshal(cosignManifestData, &cosignManifest); err != nil {
		t.Fatal(err)
	}
	if len(cosignManifest.Layers) != 1 || cosignManifest.Layers[0].MediaType != MediaTypeCosignSimpleSigning {
		t.Fatalf("unexpected cosign manifest layers: %#v", cosignManifest.Layers)
	}
	if cosignManifest.Layers[0].Annotations["dev.cosignproject.cosign/signature"] == "" {
		t.Fatalf("missing cosign signature annotation: %#v", cosignManifest.Layers[0].Annotations)
	}
	referrersIndexData := reg.manifests["acme/signed-agent-state:"+sigstoreReferrersTag(snap.ManifestDigest)]
	if len(referrersIndexData) == 0 {
		t.Fatalf("missing Sigstore referrers fallback tag %s", sigstoreReferrersTag(snap.ManifestDigest))
	}
	var referrersIndex Index
	if err := json.Unmarshal(referrersIndexData, &referrersIndex); err != nil {
		t.Fatal(err)
	}
	if got := countArtifactType(referrersIndex.Manifests, MediaTypeSigstoreBundle); got != 2 {
		t.Fatalf("expected two Sigstore bundle descriptors in fallback index, got %d: %#v", got, referrersIndex.Manifests)
	}

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
	pulled, err := PullSnapshot(dest, repo+":release", "release")
	if err != nil {
		t.Fatal(err)
	}
	if pulled != snap.ManifestDigest {
		t.Fatalf("pulled digest mismatch: want %s got %s", snap.ManifestDigest, pulled)
	}
	pulledVerify, err := VerifySnapshot(dest, "release", VerifyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if pulledVerify.SignatureDigest != verify.SignatureDigest || pulledVerify.ProvenanceDigest != verify.ProvenanceDigest {
		t.Fatalf("pulled verify mismatch: want %#v got %#v", verify, pulledVerify)
	}
	destStore, err := findStore(dest)
	if err != nil {
		t.Fatal(err)
	}
	for _, refName := range []string{
		cosignPayloadRefName(snap.ManifestDigest),
		cosignSignatureRefName(snap.ManifestDigest),
		sigstoreSignatureBundleRefName(snap.ManifestDigest),
		sigstoreAttestationBundleRefName(snap.ManifestDigest),
	} {
		if _, err := destStore.resolveRef(refName); err != nil {
			t.Fatalf("expected pulled compatibility ref %s: %v", refName, err)
		}
	}
}

func hasRef(refs []Ref, name string) bool {
	for _, ref := range refs {
		if ref.Name == name {
			return true
		}
	}
	return false
}

func countArtifactType(descs []Descriptor, artifactType string) int {
	count := 0
	for _, desc := range descs {
		if desc.ArtifactType == artifactType {
			count++
		}
	}
	return count
}

func assertLocalSigstoreCompatibilityArtifacts(t *testing.T, root, manifestDigest string) {
	t.Helper()
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	payloadDigest, err := s.resolveRef(cosignPayloadRefName(manifestDigest))
	if err != nil {
		t.Fatal(err)
	}
	payloadData, err := s.readBlob(payloadDigest)
	if err != nil {
		t.Fatal(err)
	}
	var simpleSigning struct {
		Critical struct {
			Image struct {
				Digest string `json:"Docker-manifest-digest"`
			} `json:"image"`
			Type string `json:"type"`
		} `json:"critical"`
	}
	if err := json.Unmarshal(payloadData, &simpleSigning); err != nil {
		t.Fatal(err)
	}
	if simpleSigning.Critical.Type != "cosign container image signature" || simpleSigning.Critical.Image.Digest != manifestDigest {
		t.Fatalf("unexpected simple-signing payload: %s", payloadData)
	}
	metaDigest, err := s.resolveRef(cosignSignatureRefName(manifestDigest))
	if err != nil {
		t.Fatal(err)
	}
	metaData, err := s.readBlob(metaDigest)
	if err != nil {
		t.Fatal(err)
	}
	var meta cosignSignatureMetadata
	if err := json.Unmarshal(metaData, &meta); err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(meta.PublicKeyPEM))
	if block == nil {
		t.Fatalf("missing public key PEM in cosign metadata")
	}
	pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pub, ok := pubAny.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("cosign public key is %T, want ECDSA", pubAny)
	}
	sig, err := base64.StdEncoding.DecodeString(meta.Signature)
	if err != nil {
		t.Fatal(err)
	}
	payloadHash := sha256.Sum256(payloadData)
	if !ecdsa.VerifyASN1(pub, payloadHash[:], sig) {
		t.Fatal("cosign simple-signing signature did not verify")
	}
	for _, refName := range []string{
		sigstoreSignatureBundleRefName(manifestDigest),
		sigstoreAttestationBundleRefName(manifestDigest),
	} {
		digest, err := s.resolveRef(refName)
		if err != nil {
			t.Fatal(err)
		}
		data, err := s.readBlob(digest)
		if err != nil {
			t.Fatal(err)
		}
		var bundle struct {
			MediaType        string          `json:"mediaType"`
			MessageSignature json.RawMessage `json:"messageSignature,omitempty"`
			DSSEEnvelope     json.RawMessage `json:"dsseEnvelope,omitempty"`
		}
		if err := json.Unmarshal(data, &bundle); err != nil {
			t.Fatal(err)
		}
		if bundle.MediaType != MediaTypeSigstoreBundle {
			t.Fatalf("bundle mediaType = %q", bundle.MediaType)
		}
		if refName == sigstoreSignatureBundleRefName(manifestDigest) && len(bundle.MessageSignature) == 0 {
			t.Fatalf("signature bundle missing messageSignature: %s", data)
		}
		if refName == sigstoreAttestationBundleRefName(manifestDigest) && len(bundle.DSSEEnvelope) == 0 {
			t.Fatalf("attestation bundle missing dsseEnvelope: %s", data)
		}
	}
}

func writeWrongECDSAPublicKey(t *testing.T, path string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM, _, err := ecdsaPublicKeyPEM(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pubPEM, 0o644); err != nil {
		t.Fatal(err)
	}
}

type fakeRegistry struct {
	blobs     map[string][]byte
	manifests map[string][]byte
	referrers map[string][]Descriptor
	uploads   map[string]string
	blobGets  map[string]int
	nextID    int
}

type authFakeRegistry struct {
	inner                  *fakeRegistry
	realm                  string
	tokenRequests          int
	basicTokenRequests     int
	bearerRegistryRequests int
}

func newAuthFakeRegistry() *authFakeRegistry {
	return &authFakeRegistry{inner: newFakeRegistry()}
}

func (r *authFakeRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/token" {
		r.handleToken(w, req)
		return
	}
	if !strings.HasPrefix(req.URL.Path, "/v2/") {
		http.NotFound(w, req)
		return
	}
	if req.Header.Get("Authorization") != "Bearer registry-token" {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s",service="osix-test",scope="repository:acme/private-agent-state:pull,push"`, r.realm))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	r.bearerRegistryRequests++
	r.inner.ServeHTTP(w, req)
}

func (r *authFakeRegistry) handleToken(w http.ResponseWriter, req *http.Request) {
	r.tokenRequests++
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("robot:secret"))
	if req.Header.Get("Authorization") == want {
		r.basicTokenRequests++
	}
	if req.URL.Query().Get("service") != "osix-test" {
		http.Error(w, "missing service", http.StatusBadRequest)
		return
	}
	if req.URL.Query().Get("scope") != "repository:acme/private-agent-state:pull,push" {
		http.Error(w, "missing scope", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"token":"registry-token"}`))
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{
		blobs:     map[string][]byte{},
		manifests: map[string][]byte{},
		referrers: map[string][]Descriptor{},
		uploads:   map[string]string{},
		blobGets:  map[string]int{},
	}
}

func (r *fakeRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/v2/" {
		w.WriteHeader(http.StatusOK)
		return
	}
	parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/v2/"), "/")
	for i, part := range parts {
		if part == "blobs" && i+1 < len(parts) {
			repo := strings.Join(parts[:i], "/")
			if parts[i+1] == "uploads" {
				r.handleUploads(w, req, repo, parts[i+2:])
				return
			}
			digest := strings.Join(parts[i+1:], "/")
			r.handleBlob(w, req, repo, digest)
			return
		}
		if part == "manifests" && i+1 < len(parts) {
			repo := strings.Join(parts[:i], "/")
			ref := strings.Join(parts[i+1:], "/")
			r.handleManifest(w, req, repo, ref)
			return
		}
		if part == "referrers" && i+1 < len(parts) {
			repo := strings.Join(parts[:i], "/")
			digest := strings.Join(parts[i+1:], "/")
			r.handleReferrers(w, req, repo, digest)
			return
		}
	}
	http.NotFound(w, req)
}

func (r *fakeRegistry) handleUploads(w http.ResponseWriter, req *http.Request, repo string, rest []string) {
	switch req.Method {
	case http.MethodPost:
		r.nextID++
		id := fmt.Sprintf("upload-%d", r.nextID)
		r.uploads[id] = repo
		w.Header().Set("Location", "/v2/"+repo+"/blobs/uploads/"+id)
		w.WriteHeader(http.StatusAccepted)
	case http.MethodPut:
		if len(rest) != 1 {
			http.NotFound(w, req)
			return
		}
		id := rest[0]
		if r.uploads[id] != repo {
			http.NotFound(w, req)
			return
		}
		digest := req.URL.Query().Get("digest")
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if got := digestBytes(body); got != digest {
			http.Error(w, "digest mismatch", http.StatusBadRequest)
			return
		}
		r.blobs[digest] = body
		delete(r.uploads, id)
		w.WriteHeader(http.StatusCreated)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (r *fakeRegistry) handleBlob(w http.ResponseWriter, req *http.Request, repo, digest string) {
	data, ok := r.blobs[digest]
	if !ok {
		http.NotFound(w, req)
		return
	}
	switch req.Method {
	case http.MethodHead:
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		r.blobGets[digest]++
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (r *fakeRegistry) handleManifest(w http.ResponseWriter, req *http.Request, repo, ref string) {
	key := repo + ":" + ref
	switch req.Method {
	case http.MethodPut:
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var envelope struct {
			MediaType string `json:"mediaType"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if envelope.MediaType == MediaTypeOCIIndex {
			var index Index
			if err := json.Unmarshal(body, &index); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		} else {
			var manifest Manifest
			if err := json.Unmarshal(body, &manifest); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			for _, desc := range append([]Descriptor{manifest.Config}, manifest.Layers...) {
				if _, ok := r.blobs[desc.Digest]; !ok {
					http.Error(w, "missing blob "+desc.Digest, http.StatusBadRequest)
					return
				}
			}
			digest := digestBytes(body)
			if manifest.Subject != nil && manifest.Subject.Digest != "" {
				referrerKey := repo + "@" + manifest.Subject.Digest
				desc := Descriptor{
					MediaType:    manifest.MediaType,
					ArtifactType: manifest.ArtifactType,
					Digest:       digest,
					Size:         int64(len(body)),
					Annotations:  manifest.Annotations,
				}
				if !hasDescriptorDigest(r.referrers[referrerKey], digest) {
					r.referrers[referrerKey] = append(r.referrers[referrerKey], desc)
				}
			}
		}
		digest := digestBytes(body)
		r.manifests[key] = body
		r.manifests[repo+"@"+digest] = body
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusCreated)
	case http.MethodGet:
		data, ok := r.manifests[key]
		if !ok && strings.HasPrefix(ref, "sha256:") {
			data, ok = r.manifests[repo+"@"+ref]
		}
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", MediaTypeOCIManifest)
		w.Header().Set("Docker-Content-Digest", digestBytes(data))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case http.MethodDelete:
		digest := ref
		if !strings.HasPrefix(digest, "sha256:") {
			http.Error(w, "delete requires digest", http.StatusBadRequest)
			return
		}
		data, ok := r.manifests[repo+"@"+digest]
		if !ok {
			http.NotFound(w, req)
			return
		}
		for manifestKey, manifestData := range r.manifests {
			if strings.HasPrefix(manifestKey, repo+":") || strings.HasPrefix(manifestKey, repo+"@") {
				if digestBytes(manifestData) == digestBytes(data) {
					delete(r.manifests, manifestKey)
				}
			}
		}
		w.WriteHeader(http.StatusAccepted)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func hasDescriptorDigest(descs []Descriptor, digest string) bool {
	for _, desc := range descs {
		if desc.Digest == digest {
			return true
		}
	}
	return false
}

func (r *fakeRegistry) handleReferrers(w http.ResponseWriter, req *http.Request, repo, digest string) {
	if req.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	index := Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCIIndex,
		Manifests:     r.referrers[repo+"@"+digest],
	}
	w.Header().Set("Content-Type", MediaTypeOCIIndex)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(index)
}

func TestParseRegistryReference(t *testing.T) {
	cases := map[string]RegistryReference{
		"localhost:5000/acme/state:snap": {
			Registry:  "localhost:5000",
			Repo:      "acme/state",
			Reference: "snap",
		},
		"http://registry.registry.svc.cluster.local:5000/acme/state:main": {
			Scheme:    "http",
			Registry:  "registry.registry.svc.cluster.local:5000",
			Repo:      "acme/state",
			Reference: "main",
		},
		"https://registry.example.io/acme/state@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": {
			Scheme:    "https",
			Registry:  "registry.example.io",
			Repo:      "acme/state",
			Reference: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		"ghcr.io/acme/state@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": {
			Registry:  "ghcr.io",
			Repo:      "acme/state",
			Reference: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	}
	for input, want := range cases {
		got, err := ParseRegistryReference(input)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s: want %#v got %#v", input, want, got)
		}
	}
}

func TestParseRegistryRepoWithExplicitScheme(t *testing.T) {
	cases := map[string]registryRepo{
		"http://registry.registry.svc.cluster.local:5000/acme/state": {
			Scheme:   "http",
			Registry: "registry.registry.svc.cluster.local:5000",
			Repo:     "acme/state",
		},
		"https://registry.example.io/acme/nested/state": {
			Scheme:   "https",
			Registry: "registry.example.io",
			Repo:     "acme/nested/state",
		},
	}
	for input, want := range cases {
		got, err := parseRegistryRepo(input)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s: want %#v got %#v", input, want, got)
		}
		client := newRegistryClientForRepo(got)
		if got.Scheme != "" && !strings.HasPrefix(client.base, got.Scheme+"://") {
			t.Fatalf("%s: client base %q does not use explicit scheme %q", input, client.base, got.Scheme)
		}
	}
}

func TestRegistryBaseURL(t *testing.T) {
	cases := map[string]string{
		"localhost:5000":      "http://localhost:5000",
		"127.0.0.1:5000":      "http://127.0.0.1:5000",
		"[::1]:5000":          "http://[::1]:5000",
		"registry.localhost":  "http://registry.localhost",
		"ghcr.io":             "https://ghcr.io",
		"registry.example.io": "https://registry.example.io",
	}
	for input, want := range cases {
		if got := registryBaseURL(input); got != want {
			t.Fatalf("%s: want %s got %s", input, want, got)
		}
	}
}

func TestLoadDockerConfigCredentials(t *testing.T) {
	dockerConfig := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dockerConfig)
	t.Setenv("OSIX_REGISTRY_USERNAME", "")
	t.Setenv("OSIX_REGISTRY_PASSWORD", "")
	t.Setenv("OSIX_REGISTRY_TOKEN", "")
	auth := base64.StdEncoding.EncodeToString([]byte("robot:secret"))
	config := fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q},"registry.example.io":{"identitytoken":"bearer-token"}}}`, auth)
	if err := os.WriteFile(filepath.Join(dockerConfig, "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	basic := loadRegistryCredentials("ghcr.io")
	if basic.Username != "robot" || basic.Password != "secret" || basic.Token != "" {
		t.Fatalf("unexpected basic credentials: %#v", basic)
	}
	bearer := loadRegistryCredentials("registry.example.io")
	if bearer.Token != "bearer-token" || bearer.Username != "" || bearer.Password != "" {
		t.Fatalf("unexpected bearer credentials: %#v", bearer)
	}
}

func TestLoadDockerCredentialHelperCredentials(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper fixture is POSIX-only")
	}
	dockerConfig := t.TempDir()
	binDir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dockerConfig)
	t.Setenv("OSIX_REGISTRY_USERNAME", "")
	t.Setenv("OSIX_REGISTRY_PASSWORD", "")
	t.Setenv("OSIX_REGISTRY_TOKEN", "")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	helperPath := filepath.Join(binDir, "docker-credential-osix-test")
	helper := `#!/bin/sh
server="$(cat)"
case "$server" in
  123456789012.dkr.ecr.us-east-1.amazonaws.com)
    printf '{"Username":"AWS","Secret":"ecr-secret"}'
    ;;
  us-docker.pkg.dev)
    printf '{"Username":"<token>","Secret":"gar-token"}'
    ;;
  myregistry.azurecr.io)
    printf '{"Username":"00000000-0000-0000-0000-000000000000","Secret":"acr-secret"}'
    ;;
  *)
    exit 1
    ;;
esac
`
	if err := os.WriteFile(helperPath, []byte(helper), 0o755); err != nil {
		t.Fatal(err)
	}
	config := `{
  "credHelpers": {
    "123456789012.dkr.ecr.us-east-1.amazonaws.com": "osix-test",
    "us-docker.pkg.dev": "osix-test",
    "myregistry.azurecr.io": "osix-test"
  }
}`
	if err := os.WriteFile(filepath.Join(dockerConfig, "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	ecr := loadRegistryCredentials("123456789012.dkr.ecr.us-east-1.amazonaws.com")
	if ecr.Username != "AWS" || ecr.Password != "ecr-secret" || ecr.Token != "" {
		t.Fatalf("unexpected ECR helper credentials: %#v", ecr)
	}
	gar := loadRegistryCredentials("us-docker.pkg.dev")
	if gar.Token != "gar-token" || gar.Username != "" || gar.Password != "" {
		t.Fatalf("unexpected GAR helper credentials: %#v", gar)
	}
	acr := loadRegistryCredentials("myregistry.azurecr.io")
	if acr.Username != "00000000-0000-0000-0000-000000000000" || acr.Password != "acr-secret" || acr.Token != "" {
		t.Fatalf("unexpected ACR helper credentials: %#v", acr)
	}
}

func TestLoadDockerCredentialStoreCredentials(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper fixture is POSIX-only")
	}
	dockerConfig := t.TempDir()
	binDir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dockerConfig)
	t.Setenv("OSIX_REGISTRY_USERNAME", "")
	t.Setenv("OSIX_REGISTRY_PASSWORD", "")
	t.Setenv("OSIX_REGISTRY_TOKEN", "")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	helperPath := filepath.Join(binDir, "docker-credential-osix-store")
	helper := `#!/bin/sh
server="$(cat)"
case "$server" in
  https://myregistry.azurecr.io|myregistry.azurecr.io)
    printf '{"Username":"00000000-0000-0000-0000-000000000000","Secret":"acr-store-secret"}'
    ;;
  *)
    exit 1
    ;;
esac
`
	if err := os.WriteFile(helperPath, []byte(helper), 0o755); err != nil {
		t.Fatal(err)
	}
	config := `{"credsStore":"osix-store"}`
	if err := os.WriteFile(filepath.Join(dockerConfig, "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	acr := loadRegistryCredentials("myregistry.azurecr.io")
	if acr.Username != "00000000-0000-0000-0000-000000000000" || acr.Password != "acr-store-secret" || acr.Token != "" {
		t.Fatalf("unexpected ACR credsStore credentials: %#v", acr)
	}
}
