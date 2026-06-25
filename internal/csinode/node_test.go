package csinode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestNodePublishPersistsMountRecordAndUnpublishRemoves(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "pod-volume")
	fs := testFileSystem("agent-record")
	node := Node{WorkspaceRoot: filepath.Join(root, "workspaces")}
	policy := &k8soperator.AgentOCISnapshotPolicySpec{Every: "1s", Push: true, MaxDirtyBytes: "1"}
	if _, err := node.Publish(context.Background(), PublishRequest{
		FileSystem:   fs,
		Policy:       policy,
		VolumeID:     "pvc-record",
		TargetPath:   target,
		AutoSnapshot: true,
	}); err != nil {
		t.Fatal(err)
	}
	record, err := node.readMountRecord("pvc-record")
	if err != nil {
		t.Fatal(err)
	}
	if record.VolumeID != "pvc-record" || record.TargetPath != target || record.WorkspacePath == "" {
		t.Fatalf("unexpected mount record: %#v", record)
	}
	if !record.AutoSnapshot || record.Policy == nil || record.Policy.Every != "1s" {
		t.Fatalf("autosnapshot policy not persisted: %#v", record)
	}
	if err := node.Unpublish(context.Background(), PublishRequest{FileSystem: fs, VolumeID: "pvc-record", TargetPath: target}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := node.readMountRecord("pvc-record"); !os.IsNotExist(err) {
		t.Fatalf("mount record should be removed, err=%v", err)
	}
}

func TestWorkerManagerSnapshotsChangesAndSkipsCleanTicks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "pod-volume")
	reports := filepath.Join(root, "reports")
	fs := testFileSystem("agent-worker")
	node := Node{WorkspaceRoot: filepath.Join(root, "workspaces")}
	policy := &k8soperator.AgentOCISnapshotPolicySpec{
		Every:               "20ms",
		Push:                false,
		MaxDirtyBytes:       "1",
		CompactEvery:        1,
		SquashEvery:         2,
		CheckpointTagPrefix: "checkpoint",
	}
	if _, err := node.Publish(context.Background(), PublishRequest{
		FileSystem:   fs,
		Policy:       policy,
		VolumeID:     "pvc-worker",
		TargetPath:   target,
		AutoSnapshot: true,
	}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(target, "agent", "workspace", "file.txt"), "v1\n")
	manager := NewWorkerManager(node, FileReporter{Root: reports})
	manager.PollInterval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	event := waitSnapshotEvent(t, filepath.Join(reports, "pvc-worker-last.json"), 2*time.Second)
	if event.SnapshotDigest == "" {
		t.Fatalf("expected worker snapshot event: %#v", event)
	}
	firstDigest := event.SnapshotDigest
	stable := waitSnapshotEvent(t, filepath.Join(reports, "pvc-worker-last.json"), 150*time.Millisecond)
	if stable.SnapshotDigest != firstDigest {
		t.Fatalf("clean worker tick created duplicate snapshot: first=%s later=%s", firstDigest, stable.SnapshotDigest)
	}
	mustWrite(t, filepath.Join(target, "agent", "workspace", "file.txt"), "v2\n")
	changed := waitForSnapshotDigest(t, filepath.Join(reports, "pvc-worker-last.json"), firstDigest, 2*time.Second)
	if changed == firstDigest {
		t.Fatalf("expected changed snapshot digest, still %s", changed)
	}
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("worker manager exit = %v, want context.Canceled", err)
	}
}

func TestVolumeContextMapsPolicyAndAutoSnapshot(t *testing.T) {
	fs, policy, autoSnapshot, err := volumeContext(map[string]string{
		"name":                           "agent-context",
		"namespace":                      "agents",
		"uid":                            "uid-1",
		"baseImage":                      "ubuntu:24.04",
		"stateRef":                       "registry.example/agents/context",
		"sourceRef":                      "registry.example/agents/context:main",
		"mountMode":                      "materialized",
		"registrySecretRef":              "registry-creds",
		"encryptionRecipients":           "age1example",
		"signer":                         "cosign-key",
		"attestation":                    "slsa",
		"autoSnapshot":                   "true",
		"snapshotEvery":                  "5s",
		"maxDirtyBytes":                  "10MiB",
		"onTurnBoundary":                 "true",
		"compactEvery":                   "3",
		"squashEvery":                    "5",
		"checkpointTagPrefix":            "checkpoint",
		"preserveSigned":                 "true",
		"pruneLocal":                     "true",
		"pruneRemote":                    "true",
		"agent.smol.ai/push-disabled":    "true",
		"agent.smol.ai/encryption-extra": "ignored",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !autoSnapshot {
		t.Fatal("autoSnapshot not enabled")
	}
	if fs.ObjectMeta.Name != "agent-context" || fs.ObjectMeta.Namespace != "agents" || fs.Spec.StateRef == "" {
		t.Fatalf("filesystem not mapped: %#v", fs)
	}
	if fs.Spec.RegistrySecretRef == nil || fs.Spec.RegistrySecretRef.Name != "registry-creds" {
		t.Fatalf("registry secret not mapped: %#v", fs.Spec.RegistrySecretRef)
	}
	if fs.Spec.Encryption == nil || fs.Spec.Encryption.Recipients != "age1example" {
		t.Fatalf("encryption not mapped: %#v", fs.Spec.Encryption)
	}
	if fs.Spec.Signing == nil || fs.Spec.Signing.Signer != "cosign-key" || fs.Spec.Signing.Attestation != "slsa" {
		t.Fatalf("signing not mapped: %#v", fs.Spec.Signing)
	}
	if policy.Every != "5s" || policy.MaxDirtyBytes != "10MiB" || !policy.OnTurnBoundary || policy.Push {
		t.Fatalf("policy not mapped: %#v", policy)
	}
	if policy.CompactEvery != 3 || policy.SquashEvery != 5 || policy.CheckpointTagPrefix != "checkpoint" {
		t.Fatalf("retention not mapped: %#v", policy)
	}
	if !policy.PreserveSigned || !policy.PruneLocal || !policy.PruneRemote {
		t.Fatalf("retention booleans not mapped: %#v", policy)
	}
}

func TestNodeSnapshotAppliesSigningPolicy(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "pod-volume")
	fs := testFileSystem("agent-signed")
	fs.Spec.Signing = &k8soperator.SigningSpec{Signer: "keyless", Attestation: "slsa"}
	node := Node{WorkspaceRoot: filepath.Join(root, "workspaces")}
	published, err := node.Publish(context.Background(), PublishRequest{
		FileSystem: fs,
		VolumeID:   "pvc-signed",
		TargetPath: target,
	})
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(target, "agent", "workspace", "signed.txt"), "signed\n")
	result, err := node.Snapshot(context.Background(), SnapshotRequest{
		FileSystem: fs,
		VolumeID:   "pvc-signed",
		TargetPath: target,
		Policy:     &k8soperator.AgentOCISnapshotPolicySpec{Push: false, MaxDirtyBytes: "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SnapshotDigests) != 1 {
		t.Fatalf("missing signed snapshot: %#v", result)
	}
	verify, err := osix.VerifySnapshot(published.Workspace, result.SnapshotDigests[0], osix.VerifyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if verify.SignatureDigest == "" || verify.ProvenanceDigest == "" || verify.Signer == "" {
		t.Fatalf("snapshot was not signed with provenance: %#v", verify)
	}
}

func testFileSystem(name string) k8soperator.AgentOCIFileSystem {
	return k8soperator.NormalizeFileSystem(k8soperator.AgentOCIFileSystem{
		ObjectMeta: k8soperator.ObjectMeta{Name: name, Namespace: "default"},
		Spec: k8soperator.AgentOCIFileSystemSpec{
			BaseImage: "base",
			StateRef:  "local/" + name,
			Branch:    "main",
			MountMode: "materialized",
		},
	})
}

func waitSnapshotEvent(t *testing.T, path string, timeout time.Duration) SnapshotEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			var event SnapshotEvent
			if err := json.Unmarshal(data, &event); err != nil {
				t.Fatal(err)
			}
			if event.SnapshotDigest != "" || event.Error != "" {
				return event
			}
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for snapshot event at %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForSnapshotDigest(t *testing.T, path, oldDigest string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		event := waitSnapshotEvent(t, path, 100*time.Millisecond)
		if event.SnapshotDigest != "" && event.SnapshotDigest != oldDigest {
			return event.SnapshotDigest
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for snapshot digest different from %s", oldDigest)
		}
		time.Sleep(10 * time.Millisecond)
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
