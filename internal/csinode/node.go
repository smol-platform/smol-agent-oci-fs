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
	WorkspaceRoot string
}

type PublishRequest struct {
	FileSystem k8soperator.AgentOCIFileSystem
	Policy     *k8soperator.AgentOCISnapshotPolicySpec
	VolumeID   string
	TargetPath string
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

func (n Node) Publish(ctx context.Context, req PublishRequest) (PublishResult, error) {
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
		digest, err := osix.PullSnapshot(workspace, sourceRef, "csi-source")
		if err != nil {
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
	watch, err := osix.Watch(workspace, req.TargetPath, osix.WatchOptions{
		Every:          every,
		MaxDirtyBytes:  maxDirtyBytes,
		OnTurnBoundary: policy.OnTurnBoundary,
		Push:           policy.Push,
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
	if err != nil {
		return SnapshotResult{}, err
	}
	result := SnapshotResult{StatePath: watch.StatePath}
	for _, snap := range watch.Snapshots {
		result.SnapshotDigests = append(result.SnapshotDigests, snap.ManifestDigest)
	}
	for _, plan := range watch.Compactions {
		if plan.CheckpointDigest != "" {
			result.CheckpointDigests = append(result.CheckpointDigests, plan.CheckpointDigest)
		}
	}
	return result, nil
}

func (n Node) Unpublish(ctx context.Context, req PublishRequest, finalSnapshot bool) error {
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
	return nil
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
