package csinode

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/smol-platform/smol-agent-oci-fs/internal/k8soperator"
	"google.golang.org/grpc"
)

const DriverName = "osix.agent.smol.ai"

type CSIServerOptions struct {
	Endpoint      string
	NodeID        string
	EnableWorkers bool
	Reporter      SnapshotReporter
}

type CSIServer struct {
	csi.UnimplementedIdentityServer
	csi.UnimplementedNodeServer

	Node          Node
	NodeID        string
	manager       *WorkerManager
	workerContext context.Context
}

func ServeCSI(ctx context.Context, node Node, opts CSIServerOptions) error {
	endpoint := strings.TrimPrefix(opts.Endpoint, "unix://")
	if endpoint == "" {
		return fmt.Errorf("CSI endpoint is required")
	}
	if opts.NodeID == "" {
		if host, err := os.Hostname(); err == nil {
			opts.NodeID = host
		} else {
			opts.NodeID = "osix-node"
		}
	}
	if err := os.MkdirAll(filepathDir(endpoint), 0o755); err != nil {
		return err
	}
	_ = os.Remove(endpoint)
	lis, err := net.Listen("unix", endpoint)
	if err != nil {
		return err
	}
	defer lis.Close()
	reporter := opts.Reporter
	if reporter == nil {
		reporter = FileReporter{Root: node.reportsDir()}
	}
	server := &CSIServer{Node: node, NodeID: opts.NodeID, workerContext: ctx}
	if opts.EnableWorkers {
		server.manager = NewWorkerManager(node, reporter)
		go func() {
			if err := server.manager.Run(ctx); err != nil && err != context.Canceled {
				fmt.Fprintf(os.Stderr, "osix CSI worker manager: %v\n", err)
			}
		}()
	}
	grpcServer := grpc.NewServer()
	csi.RegisterIdentityServer(grpcServer, server)
	csi.RegisterNodeServer(grpcServer, server)
	go func() {
		<-ctx.Done()
		grpcServer.GracefulStop()
	}()
	return grpcServer.Serve(lis)
}

func (s *CSIServer) GetPluginInfo(ctx context.Context, req *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	return &csi.GetPluginInfoResponse{Name: DriverName, VendorVersion: "v0.1.0"}, nil
}

func (s *CSIServer) GetPluginCapabilities(ctx context.Context, req *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {
	return &csi.GetPluginCapabilitiesResponse{Capabilities: []*csi.PluginCapability{{
		Type: &csi.PluginCapability_Service_{Service: &csi.PluginCapability_Service{Type: csi.PluginCapability_Service_VOLUME_ACCESSIBILITY_CONSTRAINTS}},
	}}}, nil
}

func (s *CSIServer) Probe(ctx context.Context, req *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	return &csi.ProbeResponse{}, nil
}

func (s *CSIServer) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{NodeId: s.NodeID}, nil
}

func (s *CSIServer) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{}, nil
}

func (s *CSIServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, fmt.Errorf("volume id is required")
	}
	if req.GetTargetPath() == "" {
		return nil, fmt.Errorf("target path is required")
	}
	fs, policy, autoSnapshot, err := volumeContext(req.GetVolumeContext())
	if err != nil {
		return nil, err
	}
	if _, err := s.Node.Publish(ctx, PublishRequest{
		FileSystem:   fs,
		Policy:       policy,
		VolumeID:     req.GetVolumeId(),
		TargetPath:   req.GetTargetPath(),
		AutoSnapshot: autoSnapshot,
	}); err != nil {
		return nil, err
	}
	if autoSnapshot && s.manager != nil {
		workerCtx := s.workerContext
		if workerCtx == nil {
			workerCtx = context.Background()
		}
		if err := s.manager.StartRecord(workerCtx, req.GetVolumeId()); err != nil {
			return nil, err
		}
	}
	return &csi.NodePublishVolumeResponse{}, nil
}

func (s *CSIServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	record, _ := s.Node.readMountRecord(req.GetVolumeId())
	if s.manager != nil {
		s.manager.stop(req.GetVolumeId())
	}
	if record.VolumeID != "" {
		if err := s.Node.Unpublish(ctx, PublishRequest{
			FileSystem: record.FileSystem,
			Policy:     record.Policy,
			VolumeID:   record.VolumeID,
			TargetPath: record.TargetPath,
		}, false); err != nil {
			return nil, err
		}
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}
	if req.GetTargetPath() == "" {
		return nil, fmt.Errorf("target path is required")
	}
	if err := s.Node.removeMountRecord(req.GetVolumeId()); err != nil {
		return nil, err
	}
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func volumeContext(values map[string]string) (k8soperator.AgentOCIFileSystem, *k8soperator.AgentOCISnapshotPolicySpec, bool, error) {
	name := first(values, "name", "filesystemName", "agent.smol.ai/name")
	if name == "" {
		name = "agent"
	}
	fs := k8soperator.NormalizeFileSystem(k8soperator.AgentOCIFileSystem{
		TypeMeta: k8soperator.TypeMeta{APIVersion: k8soperator.APIVersion, Kind: k8soperator.KindAgentOCIFileSystem},
		ObjectMeta: k8soperator.ObjectMeta{
			Name:      name,
			Namespace: first(values, "namespace", "filesystemNamespace", "agent.smol.ai/namespace"),
			UID:       first(values, "uid", "filesystemUID", "agent.smol.ai/uid"),
		},
		Spec: k8soperator.AgentOCIFileSystemSpec{
			BaseImage: first(values, "baseImage", "base", "agent.smol.ai/base-image"),
			StateRef:  first(values, "stateRef", "state", "agent.smol.ai/state-ref"),
			Branch:    first(values, "branch", "agent.smol.ai/branch"),
			SourceRef: first(values, "sourceRef", "source", "agent.smol.ai/source-ref"),
			MountMode: first(values, "mountMode", "mode", "agent.smol.ai/mount-mode"),
		},
	})
	if secret := first(values, "registrySecretRef", "registrySecretName", "agent.smol.ai/registry-secret"); secret != "" {
		fs.Spec.RegistrySecretRef = &k8soperator.LocalObjectReference{Name: secret}
	}
	if recipients := first(values, "encryptionRecipients", "agent.smol.ai/encryption-recipients"); recipients != "" {
		fs.Spec.Encryption = &k8soperator.EncryptionSpec{Recipients: recipients}
	}
	signing := k8soperator.SigningSpec{
		Signer:                       first(values, "signer", "agent.smol.ai/signer"),
		Attestation:                  first(values, "attestation", "agent.smol.ai/attestation"),
		CertificateIdentity:          first(values, "certificateIdentity", "agent.smol.ai/certificate-identity"),
		CertificateIdentityRegexp:    first(values, "certificateIdentityRegexp", "agent.smol.ai/certificate-identity-regexp"),
		CertificateOIDCIssuer:        first(values, "certificateOIDCIssuer", "agent.smol.ai/certificate-oidc-issuer"),
		CertificateOIDCIssuerRegexp:  first(values, "certificateOIDCIssuerRegexp", "agent.smol.ai/certificate-oidc-issuer-regexp"),
		SigstoreTrustedRoot:          first(values, "sigstoreTrustedRoot", "agent.smol.ai/sigstore-trusted-root"),
		SigstoreIgnoreTlog:           boolValue(first(values, "sigstoreIgnoreTlog", "agent.smol.ai/sigstore-ignore-tlog")),
		SigstoreIgnoreTimestamp:      boolValue(first(values, "sigstoreIgnoreTimestamp", "agent.smol.ai/sigstore-ignore-timestamp")),
		SigstoreIgnoreCertificateSCT: boolValue(first(values, "sigstoreIgnoreCertificateSCT", "agent.smol.ai/sigstore-ignore-certificate-sct")),
	}
	if signing != (k8soperator.SigningSpec{}) {
		fs.Spec.Signing = &signing
	}
	if err := k8soperator.ValidateFileSystem(fs); err != nil {
		return k8soperator.AgentOCIFileSystem{}, nil, false, err
	}
	policy := &k8soperator.AgentOCISnapshotPolicySpec{
		Every:               first(values, "snapshotEvery", "policy.every", "agent.smol.ai/snapshot-every"),
		MaxDirtyBytes:       first(values, "maxDirtyBytes", "policy.maxDirtyBytes", "agent.smol.ai/max-dirty-bytes"),
		OnTurnBoundary:      boolValue(first(values, "onTurnBoundary", "policy.onTurnBoundary", "agent.smol.ai/on-turn-boundary")),
		Push:                !boolValue(first(values, "pushDisabled", "policy.pushDisabled", "agent.smol.ai/push-disabled")),
		CompactEvery:        intValue(first(values, "compactEvery", "policy.compactEvery", "agent.smol.ai/compact-every")),
		SquashEvery:         intValue(first(values, "squashEvery", "policy.squashEvery", "agent.smol.ai/squash-every")),
		CheckpointTagPrefix: first(values, "checkpointTagPrefix", "policy.checkpointTagPrefix", "agent.smol.ai/checkpoint-tag-prefix"),
		PreserveSigned:      boolValue(first(values, "preserveSigned", "policy.preserveSigned", "agent.smol.ai/preserve-signed")),
		PruneLocal:          boolValue(first(values, "pruneLocal", "policy.pruneLocal", "agent.smol.ai/prune-local")),
		PruneRemote:         boolValue(first(values, "pruneRemote", "policy.pruneRemote", "agent.smol.ai/prune-remote")),
	}
	autoSnapshot := boolValue(first(values, "autoSnapshot", "agent.smol.ai/auto-snapshot"))
	return fs, policy, autoSnapshot, nil
}

func first(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func boolValue(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func intValue(value string) int {
	if value == "" {
		return 0
	}
	n, _ := strconv.Atoi(value)
	return n
}

func filepathDir(path string) string {
	idx := strings.LastIndex(path, string(os.PathSeparator))
	if idx <= 0 {
		return "."
	}
	return path[:idx]
}
