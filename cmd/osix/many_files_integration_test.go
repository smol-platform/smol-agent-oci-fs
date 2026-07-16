package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smol-platform/smol-agent-oci-fs/internal/osix"
)

func TestManyFileImageBrowseExtractAndCompactIntegration(t *testing.T) {
	root := t.TempDir()
	fsRoot := filepath.Join(root, "agentfs")
	if _, err := osix.Init(root, osix.InitOptions{
		Base:          "example/base:latest",
		Name:          "many-files-agent",
		StateRef:      "local/many-files-agent",
		Mount:         fsRoot,
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}

	expected := make(map[string]string)
	for directory := 0; directory < 24; directory++ {
		for file := 0; file < 32; file++ {
			index := directory*32 + file
			rel := filepath.Join(fmt.Sprintf("dir-%02d", directory), fmt.Sprintf("file-%04d.txt", index))
			content := fmt.Sprintf("file=%04d version=1 payload=%08x\n", index, index*2654435761)
			expected[rel] = content
			mustWriteCLI(t, filepath.Join(fsRoot, "agent", "workspace", rel), content)
		}
	}
	if err := os.Symlink("dir-00/file-0001.txt", filepath.Join(fsRoot, "agent", "workspace", "first-file")); err != nil {
		t.Fatal(err)
	}
	if _, err := osix.Snapshot(root, fsRoot, osix.SnapshotOptions{Tag: "many-v1", AlsoTag: "main"}); err != nil {
		t.Fatal(err)
	}

	for directory := 0; directory < 24; directory++ {
		for file := 0; file < 32; file++ {
			index := directory*32 + file
			rel := filepath.Join(fmt.Sprintf("dir-%02d", directory), fmt.Sprintf("file-%04d.txt", index))
			switch {
			case index%11 == 0:
				if err := os.Remove(filepath.Join(fsRoot, "agent", "workspace", rel)); err != nil {
					t.Fatal(err)
				}
				delete(expected, rel)
			case index%7 == 0:
				content := fmt.Sprintf("file=%04d version=2 updated=true\n", index)
				expected[rel] = content
				mustWriteCLI(t, filepath.Join(fsRoot, "agent", "workspace", rel), content)
			}
		}
	}
	for file := 0; file < 64; file++ {
		rel := filepath.Join("new", fmt.Sprintf("added-%03d.json", file))
		content := fmt.Sprintf("{\"index\":%d,\"state\":\"added\"}\n", file)
		expected[rel] = content
		mustWriteCLI(t, filepath.Join(fsRoot, "agent", "workspace", rel), content)
	}
	if _, err := osix.Snapshot(root, fsRoot, osix.SnapshotOptions{Tag: "many-v2", AlsoTag: "main"}); err != nil {
		t.Fatal(err)
	}

	withWorkingDir(t, root, func() {
		if _, err := captureStdout(t, func() error {
			return run([]string{"compact", "many-v2", "--squash-every", "2", "--tag", "checkpoint-many"})
		}); err != nil {
			t.Fatal(err)
		}
	})
	assertCheckpointImageCLI(t, root, "checkpoint-many")

	var plainListing string
	withWorkingDir(t, root, func() {
		var err error
		plainListing, err = captureStdout(t, func() error {
			return run([]string{"browse", "checkpoint-many", "agent/workspace", "--plain"})
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"dir-00/", "dir-23/", "new/", "first-file -> dir-00/file-0001.txt"} {
		if !strings.Contains(plainListing, want) {
			t.Fatalf("plain browser listing missing %q:\n%s", want, plainListing)
		}
	}

	var jsonListing string
	withWorkingDir(t, root, func() {
		var err error
		jsonListing, err = captureStdout(t, func() error {
			return run([]string{"browse", "checkpoint-many", "agent/workspace/new", "--json"})
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	var listed []osix.TreeEntry
	if err := json.Unmarshal([]byte(jsonListing), &listed); err != nil {
		t.Fatalf("parse browser JSON: %v\n%s", err, jsonListing)
	}
	if len(listed) != 64 {
		t.Fatalf("browser listed %d new files, want 64", len(listed))
	}

	extracted := filepath.Join(root, "extracted-workspace")
	withWorkingDir(t, root, func() {
		output, err := captureStdout(t, func() error {
			return run([]string{"extract", "checkpoint-many", "agent/workspace", extracted})
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output, fmt.Sprintf("(%d files,", len(expected))) {
			t.Fatalf("unexpected extract output: %s", output)
		}
	})

	regularFiles := 0
	err := filepath.WalkDir(extracted, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			regularFiles++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if regularFiles != len(expected) {
		t.Fatalf("extracted %d regular files, want %d", regularFiles, len(expected))
	}
	for rel, want := range expected {
		assertFileCLI(t, filepath.Join(extracted, rel), want)
	}
	link, err := os.Readlink(filepath.Join(extracted, "first-file"))
	if err != nil {
		t.Fatal(err)
	}
	if link != "dir-00/file-0001.txt" {
		t.Fatalf("extracted symlink target = %q", link)
	}

	oneFile := filepath.Join(root, "one-extracted-file.txt")
	withWorkingDir(t, root, func() {
		if _, err := captureStdout(t, func() error {
			return run([]string{"extract", "checkpoint-many", "agent/workspace/new/added-063.json", oneFile})
		}); err != nil {
			t.Fatal(err)
		}
	})
	assertFileCLI(t, oneFile, expected[filepath.Join("new", "added-063.json")])
}

func assertCheckpointImageCLI(t *testing.T, root, ref string) {
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
		t.Fatalf("checkpoint ref %s not found in %#v", ref, refs)
	}
	manifestData, err := os.ReadFile(filepath.Join(root, ".osix", "blobs", "sha256", strings.TrimPrefix(digest, "sha256:")))
	if err != nil {
		t.Fatal(err)
	}
	var manifest osix.Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Annotations["com.osix.kind"] != "checkpoint" || len(manifest.Layers) != 1 {
		t.Fatalf("unexpected checkpoint manifest: %#v", manifest)
	}
	configData, err := os.ReadFile(filepath.Join(root, ".osix", "blobs", "sha256", strings.TrimPrefix(manifest.Config.Digest, "sha256:")))
	if err != nil {
		t.Fatal(err)
	}
	var config osix.AgentConfig
	if err := json.Unmarshal(configData, &config); err != nil {
		t.Fatal(err)
	}
	if config.Parent != nil {
		t.Fatalf("checkpoint retained parent %s", config.Parent.Digest)
	}
}
