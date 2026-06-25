package csinode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/smol-platform/smol-agent-oci-fs/internal/k8soperator"
	"github.com/smol-platform/smol-agent-oci-fs/internal/osix"
)

type Node struct {
	WorkspaceRoot  string
	SecretProvider SecretProvider
}

type PublishRequest struct {
	FileSystem   k8soperator.AgentOCIFileSystem
	Policy       *k8soperator.AgentOCISnapshotPolicySpec
	VolumeID     string
	TargetPath   string
	AutoSnapshot bool
}

type PublishResult struct {
	Workspace string `json:"workspace"`
	Target    string `json:"target"`
	SourceRef string `json:"sourceRef,omitempty"`
	Mode      string `json:"mode"`
}

type SnapshotRequest struct {
	FileSystem k8soperator.AgentOCIFileSystem
	Policy     *k8soperator.AgentOCISnapshotPolicySpec
	VolumeID   string
	TargetPath string
}

type SnapshotResult struct {
	StatePath         string   `json:"statePath"`
	SnapshotDigests   []string `json:"snapshotDigests,omitempty"`
	CheckpointDigests []string `json:"checkpointDigests,omitempty"`
}

type SnapshotDecision struct {
	Needed           bool  `json:"needed"`
	MissingReference bool  `json:"missingReference,omitempty"`
	DirtyBytes       int64 `json:"dirtyBytes"`
	ChangeCount      int   `json:"changeCount"`
}

func (n Node) Publish(ctx context.Context, req PublishRequest) (PublishResult, error) {
	nodeMetrics.publishTotal.Add(1)
	fs := k8soperator.NormalizeFileSystem(req.FileSystem)
	if err := k8soperator.ValidateFileSystem(fs); err != nil {
		return PublishResult{}, err
	}
	if strings.TrimSpace(n.WorkspaceRoot) == "" {
		return PublishResult{}, fmt.Errorf("workspace root is required")
	}
	if strings.TrimSpace(req.TargetPath) == "" {
		return PublishResult{}, fmt.Errorf("target path is required")
	}
	workspace := n.workspaceFor(req.VolumeID, fs)
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return PublishResult{}, err
	}
	if _, err := os.Stat(filepath.Join(workspace, ".osix")); os.IsNotExist(err) {
		if _, err := osix.Init(workspace, osix.InitOptions{
			Base:          fs.Spec.BaseImage,
			Name:          fs.ObjectMeta.Name,
			StateRef:      fs.Spec.StateRef,
			Mount:         req.TargetPath,
			DefaultBranch: fs.Spec.Branch,
			Encrypt:       encryptionRecipients(fs),
		}); err != nil {
			return PublishResult{}, err
		}
	} else if err != nil {
		return PublishResult{}, err
	}
	sourceRef := fs.Spec.SourceRef
	if sourceRef == "" {
		sourceRef = fs.Spec.Branch
	}
	if k8soperatorRefIsRemote(sourceRef) {
		var digest string
		err := n.withRegistryCredentials(ctx, fs, func() error {
			var pullErr error
			digest, pullErr = osix.PullSnapshot(workspace, sourceRef, "csi-source")
			return pullErr
		})
		if err != nil {
			return PublishResult{}, err
		}
		if err := n.verifySource(ctx, fs, workspace, digest); err != nil {
			return PublishResult{}, err
		}
		sourceRef = digest
	}
	if hasLocalRef(workspace, sourceRef) {
		if _, err := osix.Mount(workspace, sourceRef, req.TargetPath, osix.MountOptions{
			Force:  true,
			RW:     true,
			Branch: fs.Spec.Branch,
			Mode:   osix.MountMode(fs.Spec.MountMode),
		}); err != nil {
			return PublishResult{}, err
		}
	} else if err := os.MkdirAll(filepath.Join(req.TargetPath, "agent", "workspace"), 0o755); err != nil {
		return PublishResult{}, err
	}
	volumeID := req.VolumeID
	if volumeID == "" {
		volumeID = fs.ObjectMeta.Name
	}
	if err := n.writeMountRecord(MountRecord{
		VolumeID:      volumeID,
		TargetPath:    req.TargetPath,
		WorkspacePath: workspace,
		FileSystem:    fs,
		Policy:        req.Policy,
		AutoSnapshot:  req.AutoSnapshot,
	}); err != nil {
		return PublishResult{}, err
	}
	return PublishResult{Workspace: workspace, Target: req.TargetPath, SourceRef: sourceRef, Mode: fs.Spec.MountMode}, nil
}

func (n Node) Snapshot(ctx context.Context, req SnapshotRequest) (SnapshotResult, error) {
	fs := k8soperator.NormalizeFileSystem(req.FileSystem)
	workspace := n.workspaceFor(req.VolumeID, fs)
	policy := k8soperator.AgentOCISnapshotPolicySpec{Push: true}
	if req.Policy != nil {
		policy = *req.Policy
	}
	every, err := parseOptionalDuration(policy.Every)
	if err != nil {
		return SnapshotResult{}, err
	}
	maxDirtyBytes, err := parseByteSize(policy.MaxDirtyBytes)
	if err != nil {
		return SnapshotResult{}, err
	}
	var watch osix.WatchResult
	err = n.withRegistryCredentials(ctx, fs, func() error {
		var watchErr error
		watch, watchErr = osix.Watch(workspace, req.TargetPath, osix.WatchOptions{
			Every:          every,
			MaxDirtyBytes:  maxDirtyBytes,
			OnTurnBoundary: policy.OnTurnBoundary,
			Push:           policy.Push,
			Encrypt:        encryptionRecipients(fs),
			Sign:           signingSigner(fs),
			Attest:         signingAttestation(fs),
			Once:           true,
			Retention: osix.WatchRetentionPolicy{
				CompactEvery:        policy.CompactEvery,
				SquashEvery:         policy.SquashEvery,
				CheckpointTagPrefix: policy.CheckpointTagPrefix,
				KeepSnapshots:       policy.KeepSnapshots,
				PreserveSigned:      policy.PreserveSigned,
				PruneLocal:          policy.PruneLocal,
				PruneRemote:         policy.PruneRemote,
			},
		})
		return watchErr
	})
	if err != nil {
		nodeMetrics.snapshotErrorsTotal.Add(1)
		return SnapshotResult{}, err
	}
	result := SnapshotResult{StatePath: watch.StatePath}
	for _, snap := range watch.Snapshots {
		result.SnapshotDigests = append(result.SnapshotDigests, snap.ManifestDigest)
	}
	nodeMetrics.snapshotTotal.Add(uint64(len(result.SnapshotDigests)))
	for _, plan := range watch.Compactions {
		if plan.CheckpointDigest != "" {
			result.CheckpointDigests = append(result.CheckpointDigests, plan.CheckpointDigest)
		}
	}
	nodeMetrics.checkpointTotal.Add(uint64(len(result.CheckpointDigests)))
	return result, nil
}

func (n Node) SnapshotNeeded(req SnapshotRequest) (SnapshotDecision, error) {
	fs := k8soperator.NormalizeFileSystem(req.FileSystem)
	workspace := n.workspaceFor(req.VolumeID, fs)
	summary, err := osix.TargetChanges(workspace, req.TargetPath, fs.Spec.Branch)
	if err != nil {
		return SnapshotDecision{}, err
	}
	decision := SnapshotDecision{
		Needed:           summary.Changed,
		MissingReference: summary.MissingReference,
		DirtyBytes:       summary.DirtyBytes,
		ChangeCount:      summary.ChangeCount,
	}
	if !decision.Needed || summary.MissingReference {
		return decision, nil
	}
	if req.Policy == nil {
		return decision, nil
	}
	maxDirtyBytes, err := parseByteSize(req.Policy.MaxDirtyBytes)
	if err != nil {
		return SnapshotDecision{}, err
	}
	if maxDirtyBytes > 0 && summary.DirtyBytes > 0 && summary.DirtyBytes < maxDirtyBytes {
		decision.Needed = false
	}
	return decision, nil
}

func (n Node) Unpublish(ctx context.Context, req PublishRequest, finalSnapshot bool) error {
	nodeMetrics.unpublishTotal.Add(1)
	if finalSnapshot {
		if _, err := n.Snapshot(ctx, SnapshotRequest{
			FileSystem: req.FileSystem,
			Policy:     req.Policy,
			VolumeID:   req.VolumeID,
			TargetPath: req.TargetPath,
		}); err != nil {
			return err
		}
	}
	workspace := n.workspaceFor(req.VolumeID, k8soperator.NormalizeFileSystem(req.FileSystem))
	if err := osix.NewMountRuntime(workspace, osix.MountAuto).Unmount(ctx, req.TargetPath, osix.UnmountOptions{Force: true}); err != nil && !strings.Contains(err.Error(), "no such file") {
		return err
	}
	volumeID := req.VolumeID
	if volumeID == "" {
		volumeID = req.FileSystem.ObjectMeta.Name
	}
	return n.removeMountRecord(volumeID)
}

func (n Node) verifySource(ctx context.Context, fs k8soperator.AgentOCIFileSystem, workspace, ref string) error {
	signing := fs.Spec.Signing
	if signing == nil {
		return nil
	}
	opts, cleanup, err := n.verifyOptions(ctx, fs)
	if err != nil {
		return err
	}
	defer cleanup()
	if _, err := osix.VerifySnapshot(workspace, ref, opts); err != nil {
		return fmt.Errorf("verify sourceRef before restore handoff: %w", err)
	}
	return nil
}

func (n Node) verifyOptions(ctx context.Context, fs k8soperator.AgentOCIFileSystem) (osix.VerifyOptions, func(), error) {
	signing := fs.Spec.Signing
	if signing == nil {
		return osix.VerifyOptions{}, func() {}, nil
	}
	opts := osix.VerifyOptions{
		CertificateIdentity:          signing.CertificateIdentity,
		CertificateIdentityRegexp:    signing.CertificateIdentityRegexp,
		CertificateOIDCIssuer:        signing.CertificateOIDCIssuer,
		CertificateOIDCIssuerRegexp:  signing.CertificateOIDCIssuerRegexp,
		SigstoreTrustedRoot:          signing.SigstoreTrustedRoot,
		SigstoreIgnoreTlog:           signing.SigstoreIgnoreTlog,
		SigstoreIgnoreTimestamp:      signing.SigstoreIgnoreTimestamp,
		SigstoreIgnoreCertificateSCT: signing.SigstoreIgnoreCertificateSCT,
	}
	cleanup := func() {}
	if signing.TrustedKeySecretRef != nil && signing.TrustedKeySecretRef.Name != "" {
		provider, ok := n.secretProvider()
		if !ok {
			return osix.VerifyOptions{}, cleanup, fmt.Errorf("trustedKeySecretRef %q requires an in-cluster Kubernetes secret provider", signing.TrustedKeySecretRef.Name)
		}
		data, err := provider.SecretData(ctx, namespaceOrDefault(fs.ObjectMeta.Namespace), signing.TrustedKeySecretRef.Name)
		if err != nil {
			return osix.VerifyOptions{}, cleanup, err
		}
		keyName := signing.TrustedKeySecretRef.Key
		if keyName == "" {
			keyName = "cosign.pub"
		}
		keyData := data[keyName]
		if len(keyData) == 0 {
			return osix.VerifyOptions{}, cleanup, fmt.Errorf("trusted key secret %s/%s missing key %q", namespaceOrDefault(fs.ObjectMeta.Namespace), signing.TrustedKeySecretRef.Name, keyName)
		}
		dir, err := os.MkdirTemp("", "osix-trusted-key-*")
		if err != nil {
			return osix.VerifyOptions{}, cleanup, err
		}
		cleanup = func() { _ = os.RemoveAll(dir) }
		keyPath := filepath.Join(dir, keyName)
		if err := os.WriteFile(keyPath, keyData, 0o600); err != nil {
			cleanup()
			return osix.VerifyOptions{}, func() {}, err
		}
		opts.TrustedKey = keyPath
	}
	return opts, cleanup, nil
}

func (n Node) workspaceFor(volumeID string, fs k8soperator.AgentOCIFileSystem) string {
	if volumeID == "" {
		volumeID = fs.ObjectMeta.Name
	}
	volumeID = strings.ReplaceAll(volumeID, "/", "-")
	volumeID = strings.ReplaceAll(volumeID, ":", "-")
	return filepath.Join(n.WorkspaceRoot, volumeID)
}

func encryptionRecipients(fs k8soperator.AgentOCIFileSystem) string {
	if fs.Spec.Encryption == nil {
		return ""
	}
	return fs.Spec.Encryption.Recipients
}

func signingSigner(fs k8soperator.AgentOCIFileSystem) string {
	if fs.Spec.Signing == nil {
		return ""
	}
	return fs.Spec.Signing.Signer
}

func signingAttestation(fs k8soperator.AgentOCIFileSystem) string {
	if fs.Spec.Signing == nil {
		return ""
	}
	return fs.Spec.Signing.Attestation
}

func hasLocalRef(workspace, ref string) bool {
	if ref == "" {
		return false
	}
	if strings.HasPrefix(ref, "sha256:") {
		return true
	}
	refs, err := osix.Refs(workspace)
	if err != nil {
		return false
	}
	for _, item := range refs {
		if item.Name == ref {
			return true
		}
	}
	return false
}

func k8soperatorRefIsRemote(ref string) bool {
	slash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	return colon > slash || strings.Contains(ref, "@sha256:")
}
