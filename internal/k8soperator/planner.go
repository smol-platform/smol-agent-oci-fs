package k8soperator

import (
	"fmt"
	"path/filepath"
	"strings"
)

type CommandPlan struct {
	Steps []CommandStep `json:"steps"`
}

type CommandStep struct {
	Name    string            `json:"name"`
	Command []string          `json:"command"`
	Env     map[string]string `json:"env,omitempty"`
}

type VolumePlanOptions struct {
	WorkspaceRoot string
	TargetPath    string
	VolumeID      string
	Policy        *AgentOCISnapshotPolicySpec
}

func PublishPlan(fs AgentOCIFileSystem, opts VolumePlanOptions) (CommandPlan, error) {
	fs = NormalizeFileSystem(fs)
	if err := ValidateFileSystem(fs); err != nil {
		return CommandPlan{}, err
	}
	if strings.TrimSpace(opts.WorkspaceRoot) == "" {
		return CommandPlan{}, fmt.Errorf("workspace root is required")
	}
	if strings.TrimSpace(opts.TargetPath) == "" {
		return CommandPlan{}, fmt.Errorf("target path is required")
	}
	volumeID := opts.VolumeID
	if volumeID == "" {
		volumeID = fs.ObjectMeta.Name
	}
	workspace := filepath.Join(opts.WorkspaceRoot, safePathSegment(volumeID))
	sourceRef := fs.Spec.SourceRef
	plan := CommandPlan{Steps: []CommandStep{
		{
			Name: "init",
			Command: []string{
				"osix", "init", fs.Spec.BaseImage,
				"--name", fs.ObjectMeta.Name,
				"--state", fs.Spec.StateRef,
				"--mount", opts.TargetPath,
			},
		},
	}}
	if fs.Spec.Encryption != nil && fs.Spec.Encryption.Recipients != "" {
		plan.Steps[0].Command = append(plan.Steps[0].Command, "--encrypt", fs.Spec.Encryption.Recipients)
	}
	if isRegistryRef(sourceRef) {
		plan.Steps = append(plan.Steps, CommandStep{
			Name:    "pull",
			Command: []string{"osix", "pull", sourceRef, "--tag", "csi-source"},
		})
		sourceRef = "csi-source"
	}
	if sourceRef != "" {
		plan.Steps = append(plan.Steps, CommandStep{
			Name: "mount",
			Command: []string{
				"osix", "mount", sourceRef, opts.TargetPath,
				"--mode", fs.Spec.MountMode,
				"--branch", fs.Spec.Branch,
				"--force",
			},
		})
	} else {
		plan.Steps = append(plan.Steps, CommandStep{
			Name:    "prepare-empty",
			Command: []string{"mkdir", "-p", filepath.Join(opts.TargetPath, "agent", "workspace")},
		})
	}
	for i := range plan.Steps {
		plan.Steps[i].Env = registryEnv(fs)
		if plan.Steps[i].Env == nil {
			plan.Steps[i].Env = map[string]string{}
		}
		plan.Steps[i].Env["OSIX_WORKSPACE"] = workspace
	}
	return plan, nil
}

func SnapshotPlan(fs AgentOCIFileSystem, opts VolumePlanOptions) (CommandPlan, error) {
	fs = NormalizeFileSystem(fs)
	if err := ValidateFileSystem(fs); err != nil {
		return CommandPlan{}, err
	}
	if strings.TrimSpace(opts.TargetPath) == "" {
		return CommandPlan{}, fmt.Errorf("target path is required")
	}
	policy := AgentOCISnapshotPolicySpec{Push: true}
	if opts.Policy != nil {
		policy = *opts.Policy
	}
	cmd := []string{"osix", "watch", opts.TargetPath, "--once"}
	if policy.Every != "" {
		cmd = append(cmd, "--every", policy.Every)
	}
	if policy.MaxDirtyBytes != "" {
		cmd = append(cmd, "--max-dirty", policy.MaxDirtyBytes)
	}
	if policy.OnTurnBoundary {
		cmd = append(cmd, "--on-turn-boundary")
	}
	if policy.Push {
		cmd = append(cmd, "--push")
	}
	if fs.Spec.Encryption != nil && fs.Spec.Encryption.Recipients != "" {
		cmd = append(cmd, "--encrypt", fs.Spec.Encryption.Recipients)
	}
	if fs.Spec.Signing != nil {
		if fs.Spec.Signing.Signer != "" {
			cmd = append(cmd, "--sign", fs.Spec.Signing.Signer)
		}
		if fs.Spec.Signing.Attestation != "" {
			cmd = append(cmd, "--attest", fs.Spec.Signing.Attestation)
		}
	}
	if policy.CompactEvery > 0 {
		cmd = append(cmd, "--compact-every", fmt.Sprintf("%d", policy.CompactEvery))
	}
	if policy.SquashEvery > 0 {
		cmd = append(cmd, "--squash-every", fmt.Sprintf("%d", policy.SquashEvery))
	}
	if policy.CheckpointTagPrefix != "" {
		cmd = append(cmd, "--checkpoint-tag-prefix", policy.CheckpointTagPrefix)
	}
	if len(policy.KeepSnapshots) > 0 {
		cmd = append(cmd, "--keep-snapshots", strings.Join(policy.KeepSnapshots, ","))
	}
	if policy.PreserveSigned {
		cmd = append(cmd, "--preserve-signed")
	}
	if policy.PruneLocal {
		cmd = append(cmd, "--prune-local")
	}
	if policy.PruneRemote {
		cmd = append(cmd, "--prune-remote")
	}
	return CommandPlan{Steps: []CommandStep{{
		Name:    "snapshot",
		Command: cmd,
		Env:     registryEnv(fs),
	}}}, nil
}

func UnpublishPlan(targetPath string, finalSnapshot bool, fs AgentOCIFileSystem, opts VolumePlanOptions) (CommandPlan, error) {
	if strings.TrimSpace(targetPath) == "" {
		return CommandPlan{}, fmt.Errorf("target path is required")
	}
	plan := CommandPlan{}
	if finalSnapshot {
		snap, err := SnapshotPlan(fs, opts)
		if err != nil {
			return CommandPlan{}, err
		}
		plan.Steps = append(plan.Steps, snap.Steps...)
	}
	plan.Steps = append(plan.Steps, CommandStep{Name: "unmount", Command: []string{"osix", "unmount", targetPath, "--force"}})
	return plan, nil
}

func registryEnv(fs AgentOCIFileSystem) map[string]string {
	env := map[string]string{}
	if fs.Spec.RegistrySecretRef != nil && fs.Spec.RegistrySecretRef.Name != "" {
		env["OSIX_REGISTRY_SECRET_NAME"] = fs.Spec.RegistrySecretRef.Name
	}
	if fs.Spec.Encryption != nil && fs.Spec.Encryption.SecretRef != nil {
		env["OSIX_ENCRYPTION_SECRET_NAME"] = fs.Spec.Encryption.SecretRef.Name
		env["OSIX_ENCRYPTION_SECRET_KEY"] = fs.Spec.Encryption.SecretRef.Key
	}
	if fs.Spec.Signing != nil {
		if fs.Spec.Signing.TrustedKeySecretRef != nil {
			env["OSIX_TRUSTED_KEY_SECRET_NAME"] = fs.Spec.Signing.TrustedKeySecretRef.Name
			env["OSIX_TRUSTED_KEY_SECRET_KEY"] = fs.Spec.Signing.TrustedKeySecretRef.Key
		}
		if fs.Spec.Signing.IdentityTokenSecretRef != nil {
			env["OSIX_IDENTITY_TOKEN_SECRET_NAME"] = fs.Spec.Signing.IdentityTokenSecretRef.Name
			env["OSIX_IDENTITY_TOKEN_SECRET_KEY"] = fs.Spec.Signing.IdentityTokenSecretRef.Key
		}
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

func isRegistryRef(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	slash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	return colon > slash || strings.Contains(ref, "@sha256:")
}

func safePathSegment(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "/", "-")
	value = strings.ReplaceAll(value, ":", "-")
	if value == "" {
		return "volume"
	}
	return value
}
