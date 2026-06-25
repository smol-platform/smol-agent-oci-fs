package csinode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/smol-platform/smol-agent-oci-fs/internal/k8soperator"
	"github.com/smol-platform/smol-agent-oci-fs/internal/osix"
)

func TestNodePublishSnapshotRetentionAndUnpublish(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "pod-volume")
	fs := k8soperator.NormalizeFileSystem(k8soperator.AgentOCIFileSystem{
		ObjectMeta: k8soperator.ObjectMeta{Name: "agent-a", Namespace: "default"},
		Spec: k8soperator.AgentOCIFileSystemSpec{
			BaseImage: "base",
			StateRef:  "local/agent-a",
			Branch:    "main",
			MountMode: "materialized",
		},
	})
	node := Node{WorkspaceRoot: filepath.Join(root, "workspaces")}
	published, err := node.Publish(context.Background(), PublishRequest{
		FileSystem: fs,
		VolumeID:   "pvc-1",
		TargetPath: target,
	})
	if err != nil {
		t.Fatal(err)
	}
	if published.Workspace == "" {
		t.Fatalf("missing workspace in publish result: %#v", published)
	}
	mustWrite(t, filepath.Join(target, "agent", "workspace", "file.txt"), "v1\n")
	first, err := node.Snapshot(context.Background(), SnapshotRequest{
		FileSystem: fs,
		VolumeID:   "pvc-1",
		TargetPath: target,
		Policy:     &k8soperator.AgentOCISnapshotPolicySpec{Push: false, MaxDirtyBytes: "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.SnapshotDigests) != 1 {
		t.Fatalf("expected first snapshot: %#v", first)
	}
	mustWrite(t, filepath.Join(target, "agent", "workspace", "file.txt"), "v2\n")
	second, err := node.Snapshot(context.Background(), SnapshotRequest{
		FileSystem: fs,
		VolumeID:   "pvc-1",
		TargetPath: target,
		Policy: &k8soperator.AgentOCISnapshotPolicySpec{
			Push:                false,
			MaxDirtyBytes:       "1",
			CompactEvery:        1,
			SquashEvery:         2,
			CheckpointTagPrefix: "checkpoint",
			PruneLocal:          true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.CheckpointDigests) != 1 {
		t.Fatalf("expected checkpoint from retention: %#v", second)
	}
	restore := filepath.Join(root, "restore")
	if err := osix.Restore(published.Workspace, "main", restore, osix.RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(restore, "agent", "workspace", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v2\n" {
		t.Fatalf("restore content = %q", data)
	}
	if err := node.Unpublish(context.Background(), PublishRequest{FileSystem: fs, VolumeID: "pvc-1", TargetPath: target}, false); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
