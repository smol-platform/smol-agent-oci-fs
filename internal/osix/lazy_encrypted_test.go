package osix

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

func TestEncryptedLazyReadDoesNotRequireWholeLayer(t *testing.T) {
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
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "secret.txt"), "encrypted lazy\n")
	snap, err := Snapshot(root, fs, SnapshotOptions{Tag: "encrypted-lazy", Encrypt: "gpg:test-recipient"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	_, manifest, _, err := s.loadManifest(snap.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	layerDigest := manifest.Layers[0].Digest
	layerHex, err := digestHex(layerDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(s.blobRoot(), layerHex)); err != nil {
		t.Fatal(err)
	}
	data, err := ReadSnapshotFile(root, "encrypted-lazy", "agent/workspace/secret.txt", ReadFileOptions{Decrypt: "gpg:test-recipient"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "encrypted lazy\n" {
		t.Fatalf("unexpected encrypted lazy data: %q", data)
	}
	if s.hasBlob(layerDigest) {
		t.Fatal("encrypted lazy read should not restore the whole layer blob")
	}
}

func TestEncryptedLazyRestoreDoesNotRequireWholeLayers(t *testing.T) {
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
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "stable.txt"), "stable\n")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "changed.txt"), "v1\n")
	if err := os.Symlink("stable.txt", filepath.Join(fs, "agent", "workspace", "stable-link.txt")); err != nil {
		t.Fatal(err)
	}
	first, err := Snapshot(root, fs, SnapshotOptions{Tag: "encrypted-lazy-restore-1", Encrypt: "gpg:test-recipient"})
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "changed.txt"), "v2\n")
	second, err := Snapshot(root, fs, SnapshotOptions{
		Tag:            "encrypted-lazy-restore-2",
		Encrypt:        "gpg:test-recipient",
		ExpectedParent: first.ManifestDigest,
	})
	if err != nil {
		t.Fatal(err)
	}

	s := removeWholeLayerBlob(t, root, first.ManifestDigest)
	removeWholeLayerBlob(t, root, second.ManifestDigest)
	firstLayer := onlyLayerDigest(t, s, first.ManifestDigest)
	secondLayer := onlyLayerDigest(t, s, second.ManifestDigest)

	restore := filepath.Join(root, "restore")
	if err := Restore(root, second.ManifestDigest, restore, RestoreOptions{Decrypt: "gpg:test-recipient"}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(restore, "agent", "workspace", "stable.txt"), "stable\n")
	assertFile(t, filepath.Join(restore, "agent", "workspace", "changed.txt"), "v2\n")
	linkTarget, err := os.Readlink(filepath.Join(restore, "agent", "workspace", "stable-link.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if linkTarget != "stable.txt" {
		t.Fatalf("unexpected restored symlink target %q", linkTarget)
	}
	if s.hasBlob(firstLayer) || s.hasBlob(secondLayer) {
		t.Fatal("encrypted lazy restore restored a whole encrypted layer")
	}
}

func TestEncryptedLazyReadHonorsWhiteout(t *testing.T) {
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
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "remove.txt"), "remove\n")
	first, err := Snapshot(root, fs, SnapshotOptions{Tag: "first", Encrypt: "gpg:test-recipient"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(fs, "agent", "workspace", "remove.txt")); err != nil {
		t.Fatal(err)
	}
	second, err := Snapshot(root, fs, SnapshotOptions{Tag: "second", Encrypt: "gpg:test-recipient", ExpectedParent: first.ManifestDigest})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ReadSnapshotFile(root, second.ManifestDigest, "agent/workspace/remove.txt", ReadFileOptions{Decrypt: "gpg:test-recipient"})
	if err == nil {
		t.Fatal("expected encrypted lazy read to report deleted file")
	}
}

func TestEncryptedLazyReadRejectsWrongDecryptMaterial(t *testing.T) {
	root, snap := createEncryptedLazySnapshot(t, "wrong identity\n")
	_, err := ReadSnapshotFile(root, snap.ManifestDigest, "agent/workspace/secret.txt", ReadFileOptions{Decrypt: "gpg:other-recipient"})
	if err == nil || !strings.Contains(err.Error(), "no matching decrypt material") {
		t.Fatalf("expected wrong decrypt material error, got %v", err)
	}
}

func TestEncryptedLazyReadRejectsTamperedFileBlob(t *testing.T) {
	root, snap := createEncryptedLazySnapshot(t, "tamper me\n")
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	record, err := readEncryptedLazyIndexRecord(s, snap.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	var fileDigest string
	for _, entry := range record.Files {
		if entry.Path == "agent/workspace/secret.txt" {
			fileDigest = entry.Digest
			break
		}
	}
	if fileDigest == "" {
		t.Fatalf("missing lazy file entry: %#v", record.Files)
	}
	fileHex, err := digestHex(fileDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.blobRoot(), fileHex), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = ReadSnapshotFile(root, snap.ManifestDigest, "agent/workspace/secret.txt", ReadFileOptions{Decrypt: "gpg:test-recipient"})
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("expected digest mismatch for tampered lazy blob, got %v", err)
	}
}

func TestEncryptedLazyReadVerifiesPlaintextMetadata(t *testing.T) {
	root, snap := createEncryptedLazySnapshot(t, "metadata check\n")
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	record, err := readEncryptedLazyIndexRecord(s, snap.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for i := range record.Files {
		if record.Files[i].Path != "agent/workspace/secret.txt" {
			continue
		}
		found = true
		if record.Files[i].PlaintextDigest != digestBytes([]byte("metadata check\n")) || record.Files[i].PlaintextSize != int64(len("metadata check\n")) {
			t.Fatalf("missing plaintext metadata: %#v", record.Files[i])
		}
		record.Files[i].PlaintextDigest = digestBytes([]byte("wrong plaintext\n"))
	}
	if !found {
		t.Fatalf("missing lazy file entry: %#v", record.Files)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	recordPath, err := encryptedLazyRecordPath(s, snap.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(recordPath, data); err != nil {
		t.Fatal(err)
	}

	_, err = ReadSnapshotFile(root, snap.ManifestDigest, "agent/workspace/secret.txt", ReadFileOptions{Decrypt: "gpg:test-recipient"})
	if err == nil || !strings.Contains(err.Error(), "plaintext digest mismatch") {
		t.Fatalf("expected plaintext digest mismatch, got %v", err)
	}
}

func TestEncryptedLazyRangeReadUsesChunksWithoutWholeFileBlob(t *testing.T) {
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
	content := bytes.Repeat([]byte("abcdef0123456789"), encryptedLazyChunkSize/16*3)
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "large.bin"), string(content))
	snap, err := Snapshot(root, fs, SnapshotOptions{Tag: "chunked-lazy", Encrypt: "gpg:test-recipient"})
	if err != nil {
		t.Fatal(err)
	}
	s := removeWholeLayerBlob(t, root, snap.ManifestDigest)
	record, err := readEncryptedLazyIndexRecord(s, snap.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	var entry encryptedLazyFileEntry
	for _, candidate := range record.Files {
		if candidate.Path == "agent/workspace/large.bin" {
			entry = candidate
			break
		}
	}
	if len(entry.Chunks) < 2 || entry.MerkleRoot == "" {
		t.Fatalf("expected chunked lazy entry, got %#v", entry)
	}
	fullHex, err := digestHex(entry.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(s.blobRoot(), fullHex)); err != nil {
		t.Fatal(err)
	}
	offset := int64(encryptedLazyChunkSize + 17)
	length := int64(123)
	data, err := ReadSnapshotFileRange(root, snap.ManifestDigest, "agent/workspace/large.bin", offset, length, ReadFileOptions{Decrypt: "gpg:test-recipient"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(content[offset:offset+length]) {
		t.Fatalf("unexpected range data: %q", data)
	}
	if s.hasBlob(entry.Digest) {
		t.Fatal("range read restored the full per-file lazy blob")
	}
}

func TestEncryptedLazyRangeReadRejectsTamperedChunkBlob(t *testing.T) {
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
	content := bytes.Repeat([]byte("abcdef0123456789"), encryptedLazyChunkSize/16*3)
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "large.bin"), string(content))
	snap, err := Snapshot(root, fs, SnapshotOptions{Tag: "chunked-lazy-tamper", Encrypt: "gpg:test-recipient"})
	if err != nil {
		t.Fatal(err)
	}
	s := removeWholeLayerBlob(t, root, snap.ManifestDigest)
	record, err := readEncryptedLazyIndexRecord(s, snap.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	var entry encryptedLazyFileEntry
	for _, candidate := range record.Files {
		if candidate.Path == "agent/workspace/large.bin" {
			entry = candidate
			break
		}
	}
	if len(entry.Chunks) < 2 {
		t.Fatalf("expected chunked lazy entry, got %#v", entry)
	}
	tampered, _, err := encryptLayer([]byte("wrong chunk plaintext"), "gpg:test-recipient")
	if err != nil {
		t.Fatal(err)
	}
	chunkHex, err := digestHex(entry.Chunks[1].Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.blobRoot(), chunkHex), tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = ReadSnapshotFileRange(root, snap.ManifestDigest, "agent/workspace/large.bin", int64(encryptedLazyChunkSize+17), 123, ReadFileOptions{Decrypt: "gpg:test-recipient"})
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("expected tampered chunk digest failure, got %v", err)
	}
}

func TestEncryptedLazyReadFallsBackToWholeLayerWithoutIndex(t *testing.T) {
	root, snap := createEncryptedLazySnapshot(t, "whole layer fallback\n")
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(s.refsRoot(), safeRefName(encryptedLazyIndexRefName(snap.ManifestDigest)))); err != nil {
		t.Fatal(err)
	}
	recordPath, err := encryptedLazyRecordPath(s, snap.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(recordPath); err != nil {
		t.Fatal(err)
	}

	data, err := ReadSnapshotFile(root, snap.ManifestDigest, "agent/workspace/secret.txt", ReadFileOptions{Decrypt: "gpg:test-recipient"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "whole layer fallback\n" {
		t.Fatalf("unexpected fallback data: %q", data)
	}
}

func TestAgeEncryptedLazyReadDoesNotRequireWholeLayer(t *testing.T) {
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
		Base:          "base",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "fs"),
		DefaultBranch: "main",
		Encrypt:       "age:" + identity.Recipient().String(),
	}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "secret.txt"), "age lazy\n")
	snap, err := Snapshot(root, fs, SnapshotOptions{Tag: "age-lazy"})
	if err != nil {
		t.Fatal(err)
	}
	s := removeWholeLayerBlob(t, root, snap.ManifestDigest)

	data, err := ReadSnapshotFile(root, snap.ManifestDigest, "agent/workspace/secret.txt", ReadFileOptions{Decrypt: identityPath})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "age lazy\n" {
		t.Fatalf("unexpected age lazy data: %q", data)
	}
	_, manifest, _, err := s.loadManifest(snap.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if s.hasBlob(manifest.Layers[0].Digest) {
		t.Fatal("age encrypted lazy read should not restore the whole layer blob")
	}
}

func TestAgeEncryptedLazyReadMaterializesEncryptedIndexRecord(t *testing.T) {
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
		Base:          "base",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "fs"),
		DefaultBranch: "main",
		Encrypt:       "age:" + identity.Recipient().String(),
	}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "secret.txt"), "age index\n")
	snap, err := Snapshot(root, fs, SnapshotOptions{Tag: "age-index-lazy"})
	if err != nil {
		t.Fatal(err)
	}
	s := removeWholeLayerBlob(t, root, snap.ManifestDigest)
	recordPath, err := encryptedLazyRecordPath(s, snap.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(recordPath); err != nil {
		t.Fatal(err)
	}

	data, err := ReadSnapshotFile(root, snap.ManifestDigest, "agent/workspace/secret.txt", ReadFileOptions{Decrypt: identityPath})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "age index\n" {
		t.Fatalf("unexpected age lazy data: %q", data)
	}
	if _, err := os.Stat(recordPath); err != nil {
		t.Fatalf("encrypted lazy index record was not materialized: %v", err)
	}
}

func TestKMSStyleEncryptedLazyReadDoesNotRequireWholeLayer(t *testing.T) {
	root := t.TempDir()
	recipient := "kms:aws:kms:us-east-1:123456789012:key/demo"
	if _, err := Init(root, InitOptions{
		Base:          "base",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "fs"),
		DefaultBranch: "main",
		Encrypt:       recipient,
	}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "secret.txt"), "kms lazy\n")
	snap, err := Snapshot(root, fs, SnapshotOptions{Tag: "kms-lazy"})
	if err != nil {
		t.Fatal(err)
	}
	s := removeWholeLayerBlob(t, root, snap.ManifestDigest)

	data, err := ReadSnapshotFile(root, snap.ManifestDigest, "agent/workspace/secret.txt", ReadFileOptions{Decrypt: recipient})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "kms lazy\n" {
		t.Fatalf("unexpected kms lazy data: %q", data)
	}
	_, manifest, _, err := s.loadManifest(snap.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if s.hasBlob(manifest.Layers[0].Digest) {
		t.Fatal("kms encrypted lazy read should not restore the whole layer blob")
	}
}

func TestKMSStyleEncryptedLazyReadMaterializesEncryptedIndexRecord(t *testing.T) {
	root := t.TempDir()
	recipient := "kms:aws:kms:us-east-1:123456789012:key/demo"
	if _, err := Init(root, InitOptions{
		Base:          "base",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "fs"),
		DefaultBranch: "main",
		Encrypt:       recipient,
	}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "secret.txt"), "kms index\n")
	snap, err := Snapshot(root, fs, SnapshotOptions{Tag: "kms-index-lazy"})
	if err != nil {
		t.Fatal(err)
	}
	s := removeWholeLayerBlob(t, root, snap.ManifestDigest)
	recordPath, err := encryptedLazyRecordPath(s, snap.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(recordPath); err != nil {
		t.Fatal(err)
	}

	data, err := ReadSnapshotFile(root, snap.ManifestDigest, "agent/workspace/secret.txt", ReadFileOptions{Decrypt: recipient})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "kms index\n" {
		t.Fatalf("unexpected kms lazy data: %q", data)
	}
	if _, err := os.Stat(recordPath); err != nil {
		t.Fatalf("encrypted lazy index record was not materialized: %v", err)
	}
}

func createEncryptedLazySnapshot(t *testing.T, content string) (string, SnapshotResult) {
	t.Helper()
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
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "secret.txt"), content)
	snap, err := Snapshot(root, fs, SnapshotOptions{Tag: "encrypted-lazy", Encrypt: "gpg:test-recipient"})
	if err != nil {
		t.Fatal(err)
	}
	return root, snap
}

func removeWholeLayerBlob(t *testing.T, root, manifestDigest string) store {
	t.Helper()
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	_, manifest, _, err := s.loadManifest(manifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	layerDigest := manifest.Layers[0].Digest
	layerHex, err := digestHex(layerDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(s.blobRoot(), layerHex)); err != nil {
		t.Fatal(err)
	}
	return s
}

func onlyLayerDigest(t *testing.T, s store, manifestDigest string) string {
	t.Helper()
	_, manifest, _, err := s.loadManifest(manifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Layers) != 1 {
		t.Fatalf("expected one layer, got %d", len(manifest.Layers))
	}
	return manifest.Layers[0].Digest
}
