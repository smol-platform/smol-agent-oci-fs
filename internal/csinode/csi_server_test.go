package csinode

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/smol-platform/smol-agent-oci-fs/internal/k8soperator"
	"github.com/smol-platform/smol-agent-oci-fs/internal/osix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestServeCSIIdentityPublishAndUnpublish(t *testing.T) {
	root := t.TempDir()
	socketDir, err := os.MkdirTemp("/tmp", "osix-csi-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socketDir)
	endpoint := filepath.Join(socketDir, "csi.sock")
	node := Node{WorkspaceRoot: filepath.Join(root, "workspaces")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- ServeCSI(ctx, node, CSIServerOptions{
			Endpoint:      "unix://" + endpoint,
			NodeID:        "node-a",
			EnableWorkers: false,
		})
	}()
	waitForSocket(t, endpoint, done)
	conn, err := grpc.DialContext(ctx, "unix://"+endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", endpoint)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	identity := csi.NewIdentityClient(conn)
	info, err := identity.GetPluginInfo(ctx, &csi.GetPluginInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if info.GetName() != DriverName {
		t.Fatalf("driver name = %q", info.GetName())
	}
	if info.GetVendorVersion() != "v"+osix.Version {
		t.Fatalf("driver version = %q, want v%s", info.GetVendorVersion(), osix.Version)
	}
	nodeClient := csi.NewNodeClient(conn)
	nodeInfo, err := nodeClient.NodeGetInfo(ctx, &csi.NodeGetInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if nodeInfo.GetNodeId() != "node-a" {
		t.Fatalf("node id = %q", nodeInfo.GetNodeId())
	}
	target := filepath.Join(root, "target")
	_, err = nodeClient.NodePublishVolume(ctx, &csi.NodePublishVolumeRequest{
		VolumeId:   "pvc-csi",
		TargetPath: target,
		VolumeContext: map[string]string{
			"name":      "agent-csi",
			"namespace": "default",
			"baseImage": "base",
			"stateRef":  "local/agent-csi",
			"mountMode": "materialized",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.readMountRecord("pvc-csi"); err != nil {
		t.Fatal(err)
	}
	_, err = nodeClient.NodeUnpublishVolume(ctx, &csi.NodeUnpublishVolumeRequest{VolumeId: "pvc-csi", TargetPath: target})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.readMountRecord("pvc-csi"); !os.IsNotExist(err) {
		t.Fatalf("mount record should be removed after unpublish, err=%v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("ServeCSI exit = %v", err)
	}
}

func TestNodeUnpublishFinalSnapshotsAutosnapshotVolume(t *testing.T) {
	root := t.TempDir()
	node := Node{WorkspaceRoot: filepath.Join(root, "workspaces")}
	server := &CSIServer{Node: node}
	target := filepath.Join(root, "target")
	_, err := server.NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
		VolumeId:   "pvc-final",
		TargetPath: target,
		VolumeContext: map[string]string{
			"name":                "agent-final",
			"namespace":           "default",
			"baseImage":           "base",
			"stateRef":            "local/agent-final",
			"mountMode":           "materialized",
			"autoSnapshot":        "true",
			"maxDirtyBytes":       "1",
			"pushDisabled":        "true",
			"snapshotEvery":       "5s",
			"compactEvery":        "1",
			"squashEvery":         "2",
			"checkpointTagPrefix": "checkpoint",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(target, "agent", "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "agent", "workspace", "final.txt"), []byte("survived unpublish\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = server.NodeUnpublishVolume(context.Background(), &csi.NodeUnpublishVolumeRequest{VolumeId: "pvc-final", TargetPath: target})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.readMountRecord("pvc-final"); !os.IsNotExist(err) {
		t.Fatalf("mount record should be removed after unpublish, err=%v", err)
	}
	workspace := filepath.Join(root, "workspaces", "pvc-final")
	restore := filepath.Join(root, "restore")
	if err := osix.Restore(workspace, "main", restore, osix.RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(restore, "agent", "workspace", "final.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "survived unpublish\n" {
		t.Fatalf("restored final write = %q", data)
	}
}

func TestNodeUnpublishWaitsForWorkerBeforeFinalSnapshot(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	fs := testFileSystem("agent-unpublish-join")
	workerStarted := make(chan struct{})
	releaseWorker := make(chan struct{})
	finalStarted := make(chan struct{})
	calls := 0
	node := Node{WorkspaceRoot: filepath.Join(root, "workspaces")}
	node.snapshotHook = func(context.Context, SnapshotRequest) (SnapshotResult, error) {
		calls++
		if calls == 1 {
			close(workerStarted)
			<-releaseWorker
		} else if calls == 2 {
			close(finalStarted)
		}
		return SnapshotResult{}, nil
	}
	policy := &k8soperator.AgentOCISnapshotPolicySpec{Every: "10ms", Push: false, MaxDirtyBytes: "1"}
	if _, err := node.Publish(context.Background(), PublishRequest{FileSystem: fs, Policy: policy, VolumeID: "pvc-unpublish-join", TargetPath: target, AutoSnapshot: true}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(target, "agent", "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "agent", "workspace", "file.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewWorkerManager(node, FileReporter{Root: filepath.Join(root, "reports")})
	workerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.StartRecord(workerCtx, "pvc-unpublish-join"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-workerStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("worker snapshot did not start")
	}
	server := &CSIServer{Node: node, manager: manager}
	done := make(chan error, 1)
	go func() {
		_, err := server.NodeUnpublishVolume(context.Background(), &csi.NodeUnpublishVolumeRequest{VolumeId: "pvc-unpublish-join", TargetPath: target})
		done <- err
	}()
	select {
	case <-finalStarted:
		t.Fatal("final snapshot started before worker exited")
	case err := <-done:
		t.Fatalf("unpublish returned before worker exited: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseWorker)
	select {
	case <-finalStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("final snapshot did not start after worker exited")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCSIReadOnlyPublishPersistsAndEnforcesAccessMode(t *testing.T) {
	root := t.TempDir()
	node := Node{WorkspaceRoot: filepath.Join(root, "workspaces")}
	fs := testFileSystem("agent-read-only")
	seedTarget := filepath.Join(root, "seed")
	published, err := node.Publish(context.Background(), PublishRequest{FileSystem: fs, VolumeID: "pvc-read-only", TargetPath: seedTarget})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(seedTarget, "agent", "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seedTarget, "agent", "workspace", "file.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := node.Snapshot(context.Background(), SnapshotRequest{FileSystem: fs, VolumeID: "pvc-read-only", TargetPath: seedTarget, Policy: &k8soperator.AgentOCISnapshotPolicySpec{Push: false, MaxDirtyBytes: "1"}}); err != nil {
		t.Fatal(err)
	}
	server := &CSIServer{Node: node}
	readOnlyTarget := filepath.Join(root, "read-only")
	t.Cleanup(func() {
		_ = filepath.WalkDir(readOnlyTarget, func(path string, entry os.DirEntry, err error) error {
			if err == nil && entry.Type()&os.ModeSymlink == 0 {
				_ = os.Chmod(path, 0o700)
			}
			return nil
		})
	})
	_, err = server.NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
		VolumeId: "pvc-read-only", TargetPath: readOnlyTarget, Readonly: true,
		VolumeContext: map[string]string{"name": fs.ObjectMeta.Name, "baseImage": fs.Spec.BaseImage, "stateRef": fs.Spec.StateRef, "sourceRef": "main", "mountMode": "materialized"},
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := node.readMountRecord("pvc-read-only")
	if err != nil {
		t.Fatal(err)
	}
	if !record.ReadOnly {
		t.Fatalf("read-only access mode not persisted: %#v", record)
	}
	if _, err := server.NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
		VolumeId: "pvc-read-only", TargetPath: readOnlyTarget, Readonly: true,
		VolumeContext: map[string]string{"name": fs.ObjectMeta.Name, "baseImage": fs.Spec.BaseImage, "stateRef": fs.Spec.StateRef, "sourceRef": "main", "mountMode": "materialized"},
	}); err != nil {
		t.Fatalf("idempotent read-only publish retry failed: %v", err)
	}
	record, err = node.readMountRecord("pvc-read-only")
	if err != nil || !record.ReadOnly {
		t.Fatalf("read-only retry did not preserve access mode: record=%#v err=%v", record, err)
	}
	info, err := os.Stat(filepath.Join(readOnlyTarget, "agent", "workspace", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("read-only file remains writable: mode=%o", info.Mode().Perm())
	}
	status, err := osix.NewMountRuntime(published.Workspace, osix.MountAuto).Status(context.Background(), readOnlyTarget)
	if err != nil {
		t.Fatal(err)
	}
	if status.RW {
		t.Fatalf("read-only CSI mount reported writable: %#v", status)
	}
	if os.Geteuid() != 0 {
		if err := os.WriteFile(filepath.Join(readOnlyTarget, "agent", "workspace", "file.txt"), []byte("changed\n"), 0o644); err == nil {
			t.Fatal("write unexpectedly succeeded through read-only CSI mount")
		}
	}
	_, err = server.NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
		VolumeId: "pvc-empty-read-only", TargetPath: filepath.Join(root, "empty-read-only"), Readonly: true,
		VolumeContext: map[string]string{"name": "agent-empty-read-only", "baseImage": "base", "stateRef": "local/empty", "mountMode": "materialized"},
	})
	if err == nil {
		t.Fatal("read-only publish without a source snapshot should fail")
	}
}

func TestNodeUnpublishCleansMountWithoutReadableRecord(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "corrupt"}[corrupt], func(t *testing.T) {
			root := t.TempDir()
			node := Node{WorkspaceRoot: filepath.Join(root, "workspaces")}
			fs := testFileSystem("agent-unpublish-fallback")
			seed := filepath.Join(root, "seed")
			published, err := node.Publish(context.Background(), PublishRequest{FileSystem: fs, VolumeID: "pvc-fallback", TargetPath: seed})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(seed, "agent", "workspace"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(seed, "agent", "workspace", "file.txt"), []byte("seed\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := node.Snapshot(context.Background(), SnapshotRequest{FileSystem: fs, VolumeID: "pvc-fallback", TargetPath: seed, Policy: &k8soperator.AgentOCISnapshotPolicySpec{Push: false, MaxDirtyBytes: "1"}}); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "mounted")
			if _, err := node.Publish(context.Background(), PublishRequest{FileSystem: fs, VolumeID: "pvc-fallback", TargetPath: target}); err != nil {
				t.Fatal(err)
			}
			recordPath := node.recordPath("pvc-fallback")
			if corrupt {
				if err := os.WriteFile(recordPath, []byte("{bad json"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(recordPath); err != nil {
				t.Fatal(err)
			}
			server := &CSIServer{Node: node}
			_, unpublishErr := server.NodeUnpublishVolume(context.Background(), &csi.NodeUnpublishVolumeRequest{VolumeId: "pvc-fallback", TargetPath: target})
			if corrupt && unpublishErr == nil {
				t.Fatal("expected corrupt record error after cleanup")
			}
			if !corrupt && unpublishErr != nil {
				t.Fatal(unpublishErr)
			}
			status, err := osix.NewMountRuntime(published.Workspace, osix.MountAuto).Status(context.Background(), target)
			if err != nil {
				t.Fatal(err)
			}
			if status.State != "unmounted" {
				t.Fatalf("mount not cleaned without record: %#v", status)
			}
		})
	}
}

func TestNodeUnpublishUsesRequestTargetWhenRecordIsStale(t *testing.T) {
	root := t.TempDir()
	node := Node{WorkspaceRoot: filepath.Join(root, "workspaces")}
	fs := testFileSystem("agent-stale-record")
	seed := filepath.Join(root, "seed")
	published, err := node.Publish(context.Background(), PublishRequest{FileSystem: fs, VolumeID: "pvc-stale", TargetPath: seed})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(seed, "agent", "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, "agent", "workspace", "file.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := node.Snapshot(context.Background(), SnapshotRequest{FileSystem: fs, VolumeID: "pvc-stale", TargetPath: seed, Policy: &k8soperator.AgentOCISnapshotPolicySpec{Push: false, MaxDirtyBytes: "1"}}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "mounted")
	if _, err := node.Publish(context.Background(), PublishRequest{FileSystem: fs, VolumeID: "pvc-stale", TargetPath: target}); err != nil {
		t.Fatal(err)
	}
	record, err := node.readMountRecord("pvc-stale")
	if err != nil {
		t.Fatal(err)
	}
	record.TargetPath = filepath.Join(root, "stale-target")
	if err := node.writeMountRecord(record); err != nil {
		t.Fatal(err)
	}
	server := &CSIServer{Node: node}
	if _, err := server.NodeUnpublishVolume(context.Background(), &csi.NodeUnpublishVolumeRequest{VolumeId: "pvc-stale", TargetPath: target}); err != nil {
		t.Fatal(err)
	}
	status, err := osix.NewMountRuntime(published.Workspace, osix.MountAuto).Status(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "unmounted" {
		t.Fatalf("request target remains mounted with stale record: %#v", status)
	}
}

func TestVolumeContextMapsSigstoreVerificationPolicy(t *testing.T) {
	fs, _, _, err := volumeContext(map[string]string{
		"name":                         "agent-sigstore",
		"baseImage":                    "base",
		"stateRef":                     "registry.example/acme/state",
		"certificateIdentity":          "https://github.com/smol-platform/smol-agent-oci-fs/.github/workflows/release.yml@refs/heads/main",
		"certificateOIDCIssuer":        "https://token.actions.githubusercontent.com",
		"sigstoreTrustedRoot":          "/var/run/osix/sigstore/trusted-root.json",
		"sigstoreIgnoreTlog":           "true",
		"sigstoreIgnoreTimestamp":      "true",
		"sigstoreIgnoreCertificateSCT": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fs.Spec.Signing == nil {
		t.Fatal("missing signing policy")
	}
	signing := fs.Spec.Signing
	if signing.CertificateIdentity == "" || signing.CertificateOIDCIssuer == "" || signing.SigstoreTrustedRoot == "" {
		t.Fatalf("verification policy not mapped: %#v", signing)
	}
	if !signing.SigstoreIgnoreTlog || !signing.SigstoreIgnoreTimestamp || !signing.SigstoreIgnoreCertificateSCT {
		t.Fatalf("sigstore verifier toggles not mapped: %#v", signing)
	}
}

func waitForSocket(t *testing.T, path string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("CSI server exited before socket was ready: %v", err)
		default:
		}
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for CSI socket %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
