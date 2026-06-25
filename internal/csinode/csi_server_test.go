package csinode

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
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
