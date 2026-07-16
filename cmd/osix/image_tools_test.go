package main

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/smol-platform/smol-agent-oci-fs/internal/osix"
)

func TestEncryptedBrowseReadAndExtractCommands(t *testing.T) {
	root := t.TempDir()
	fsRoot := filepath.Join(root, "agentfs")
	if _, err := osix.Init(root, osix.InitOptions{
		Base:          "example/base:latest",
		Name:          "encrypted-image-tools",
		StateRef:      "local/encrypted-image-tools",
		Mount:         fsRoot,
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	recipient := "age:" + identity.Recipient().String()
	mustWriteCLI(t, filepath.Join(fsRoot, "agent", "workspace", "secret.txt"), "encrypted cli data\n")

	withWorkingDir(t, root, func() {
		if _, err := captureStdout(t, func() error {
			return run([]string{"snapshot", fsRoot, "--tag", "encrypted-tools", "--encrypt", recipient})
		}); err != nil {
			t.Fatal(err)
		}

		listing, err := captureStdout(t, func() error {
			return run([]string{"browse", "encrypted-tools", "agent/workspace", "--plain"})
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(listing, "agent/workspace/secret.txt") {
			t.Fatalf("encrypted image browser listing missing file: %s", listing)
		}

		read, err := captureStdout(t, func() error {
			return run([]string{"read", "encrypted-tools", "agent/workspace/secret.txt", "--decrypt", identity.String()})
		})
		if err != nil {
			t.Fatal(err)
		}
		if read != "encrypted cli data\n" {
			t.Fatalf("encrypted CLI read = %q", read)
		}

		extracted := filepath.Join(root, "secret-copy.txt")
		if _, err := captureStdout(t, func() error {
			return run([]string{"extract", "encrypted-tools", "agent/workspace/secret.txt", extracted})
		}); err == nil {
			t.Fatal("encrypted extraction succeeded without decrypt material")
		}
		if _, err := os.Stat(extracted); !os.IsNotExist(err) {
			t.Fatalf("failed encrypted extraction created destination: %v", err)
		}
		if _, err := captureStdout(t, func() error {
			return run([]string{"extract", "encrypted-tools", "agent/workspace/secret.txt", extracted, "--decrypt", identity.String()})
		}); err != nil {
			t.Fatal(err)
		}
		assertFileCLI(t, extracted, "encrypted cli data\n")

		mustWriteCLI(t, filepath.Join(fsRoot, "agent", "workspace", "secret.txt"), "encrypted checkpoint data\n")
		if _, err := captureStdout(t, func() error {
			return run([]string{"snapshot", fsRoot, "--tag", "encrypted-tools-v2", "--encrypt", recipient})
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := captureStdout(t, func() error {
			return run([]string{
				"compact", "encrypted-tools-v2",
				"--squash-every", "2",
				"--tag", "encrypted-checkpoint",
				"--decrypt", identity.String(),
				"--encrypt", recipient,
			})
		}); err != nil {
			t.Fatal(err)
		}
		assertEncryptedSnapshotCLI(t, root, "encrypted-checkpoint")
		checkpointCopy := filepath.Join(root, "checkpoint-copy.txt")
		if _, err := captureStdout(t, func() error {
			return run([]string{"extract", "encrypted-checkpoint", "agent/workspace/secret.txt", checkpointCopy})
		}); err == nil {
			t.Fatal("encrypted checkpoint extraction succeeded without decrypt material")
		}
		if _, err := captureStdout(t, func() error {
			return run([]string{"extract", "encrypted-checkpoint", "agent/workspace/secret.txt", checkpointCopy, "--decrypt", identity.String()})
		}); err != nil {
			t.Fatal(err)
		}
		assertFileCLI(t, checkpointCopy, "encrypted checkpoint data\n")
	})
}

func TestEncryptedWatchCreatesEncryptedCheckpointWithoutDecryptIdentity(t *testing.T) {
	root := t.TempDir()
	fsRoot := filepath.Join(root, "agentfs")
	if _, err := osix.Init(root, osix.InitOptions{
		Base:          "example/base:latest",
		Name:          "encrypted-watch",
		StateRef:      "local/encrypted-watch",
		Mount:         fsRoot,
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	recipient := "age:" + identity.Recipient().String()
	mustWriteCLI(t, filepath.Join(fsRoot, "agent", "workspace", "hourly.txt"), "hourly encrypted checkpoint\n")

	withWorkingDir(t, root, func() {
		if _, err := captureStdout(t, func() error {
			return run([]string{
				"watch", fsRoot, "--once",
				"--encrypt", recipient,
				"--compact-every", "1",
				"--squash-every", "1",
				"--checkpoint-tag-prefix", "encrypted-watch-checkpoint",
			})
		}); err != nil {
			t.Fatal(err)
		}
		checkpointRef := findRefWithPrefixCLI(t, root, "encrypted-watch-checkpoint-")
		assertEncryptedSnapshotCLI(t, root, checkpointRef)
		restored := filepath.Join(root, "hourly-copy.txt")
		if _, err := captureStdout(t, func() error {
			return run([]string{"extract", checkpointRef, "agent/workspace/hourly.txt", restored, "--decrypt", identity.String()})
		}); err != nil {
			t.Fatal(err)
		}
		assertFileCLI(t, restored, "hourly encrypted checkpoint\n")
	})
}

func findRefWithPrefixCLI(t *testing.T, root, prefix string) string {
	t.Helper()
	refs, err := osix.Refs(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		if strings.HasPrefix(ref.Name, prefix) {
			return ref.Name
		}
	}
	t.Fatalf("ref with prefix %q not found in %#v", prefix, refs)
	return ""
}

func assertEncryptedSnapshotCLI(t *testing.T, root, ref string) {
	t.Helper()
	refs, err := osix.Refs(root)
	if err != nil {
		t.Fatal(err)
	}
	digest := ""
	for _, candidate := range refs {
		if candidate.Name == ref {
			digest = candidate.Digest
			break
		}
	}
	if digest == "" {
		t.Fatalf("ref %q not found in %#v", ref, refs)
	}
	manifestData, err := os.ReadFile(filepath.Join(root, ".osix", "blobs", "sha256", strings.TrimPrefix(digest, "sha256:")))
	if err != nil {
		t.Fatal(err)
	}
	var manifest osix.Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Layers) != 1 || manifest.Layers[0].MediaType != osix.MediaTypeLayerEnc {
		t.Fatalf("snapshot %s is not encrypted: %#v", ref, manifest.Layers)
	}
}

func TestRemoteBrowseIsMetadataOnlyAndExtractFetchesContent(t *testing.T) {
	registry := newCLIFakeRegistry()
	server := httptest.NewServer(registry)
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo := u.Host + "/acme/image-tools"

	source := t.TempDir()
	sourceFS := filepath.Join(source, "agentfs")
	if _, err := osix.Init(source, osix.InitOptions{
		Base:          "example/base:latest",
		Name:          "image-tools",
		StateRef:      repo,
		Mount:         sourceFS,
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	mustWriteCLI(t, filepath.Join(sourceFS, "agent", "workspace", "remote.txt"), "remote image data\n")
	snapshot, err := osix.Snapshot(source, sourceFS, osix.SnapshotOptions{Tag: "remote-tools", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}
	layerDigest := readCLISnapshotLayerDigest(t, source, snapshot.ManifestDigest)
	withWorkingDir(t, source, func() {
		if err := run([]string{"push", "remote-tools"}); err != nil {
			t.Fatal(err)
		}
	})
	registry.blobGets[layerDigest] = 0

	destinationWorkspace := t.TempDir()
	if _, err := osix.Init(destinationWorkspace, osix.InitOptions{
		Base:          "example/base:latest",
		Name:          "image-tools-reader",
		StateRef:      repo,
		Mount:         filepath.Join(destinationWorkspace, "agentfs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	remoteRef := repo + ":remote-tools"
	withWorkingDir(t, destinationWorkspace, func() {
		listing, err := captureStdout(t, func() error {
			return run([]string{"browse", remoteRef, "agent/workspace", "--plain"})
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(listing, "agent/workspace/remote.txt") {
			t.Fatalf("remote browser listing missing file: %s", listing)
		}
		if got := registry.blobGets[layerDigest]; got != 0 {
			t.Fatalf("metadata-only browse fetched layer %s %d times", layerDigest, got)
		}

		extracted := filepath.Join(destinationWorkspace, "remote.txt")
		if _, err := captureStdout(t, func() error {
			return run([]string{"extract", remoteRef, "agent/workspace/remote.txt", extracted})
		}); err != nil {
			t.Fatal(err)
		}
		assertFileCLI(t, extracted, "remote image data\n")
		if got := registry.blobGets[layerDigest]; got != 1 {
			t.Fatalf("remote extraction fetched layer %s %d times, want 1", layerDigest, got)
		}
	})
}
