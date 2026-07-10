package k8soperator

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var safeVolumeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

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
	workspace := filepath.Join(opts.WorkspaceRoot, SafeVolumePathSegment(volumeID))
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

func CSIVolumeContext(fs AgentOCIFileSystem, policy *AgentOCISnapshotPolicySpec, autoSnapshot bool) (map[string]string, error) {
	fs = NormalizeFileSystem(fs)
	if err := ValidateFileSystem(fs); err != nil {
		return nil, err
	}
	values := map[string]string{
		"name":                     fs.ObjectMeta.Name,
		"namespace":                fs.ObjectMeta.Namespace,
		"uid":                      fs.ObjectMeta.UID,
		"baseImage":                fs.Spec.BaseImage,
		"stateRef":                 fs.Spec.StateRef,
		"branch":                   fs.Spec.Branch,
		"sourceRef":                fs.Spec.SourceRef,
		"mountMode":                fs.Spec.MountMode,
		"agent.smol.ai/name":       fs.ObjectMeta.Name,
		"agent.smol.ai/namespace":  fs.ObjectMeta.Namespace,
		"agent.smol.ai/uid":        fs.ObjectMeta.UID,
		"agent.smol.ai/base-image": fs.Spec.BaseImage,
		"agent.smol.ai/state-ref":  fs.Spec.StateRef,
		"agent.smol.ai/branch":     fs.Spec.Branch,
		"agent.smol.ai/source-ref": fs.Spec.SourceRef,
		"agent.smol.ai/mount-mode": fs.Spec.MountMode,
	}
	if fs.Spec.RegistrySecretRef != nil && fs.Spec.RegistrySecretRef.Name != "" {
		values["registrySecretRef"] = fs.Spec.RegistrySecretRef.Name
		values["agent.smol.ai/registry-secret"] = fs.Spec.RegistrySecretRef.Name
	}
	if fs.Spec.Encryption != nil && fs.Spec.Encryption.Recipients != "" {
		values["encryptionRecipients"] = fs.Spec.Encryption.Recipients
		values["agent.smol.ai/encryption-recipients"] = fs.Spec.Encryption.Recipients
	}
	if fs.Spec.Signing != nil {
		if fs.Spec.Signing.Signer != "" {
			values["signer"] = fs.Spec.Signing.Signer
			values["agent.smol.ai/signer"] = fs.Spec.Signing.Signer
		}
		if fs.Spec.Signing.Attestation != "" {
			values["attestation"] = fs.Spec.Signing.Attestation
			values["agent.smol.ai/attestation"] = fs.Spec.Signing.Attestation
		}
	}
	if policy != nil {
		autoSnapshot = true
		values["snapshotEvery"] = policy.Every
		values["maxDirtyBytes"] = policy.MaxDirtyBytes
		values["agent.smol.ai/snapshot-every"] = policy.Every
		values["agent.smol.ai/max-dirty-bytes"] = policy.MaxDirtyBytes
		if policy.OnTurnBoundary {
			values["onTurnBoundary"] = "true"
			values["agent.smol.ai/on-turn-boundary"] = "true"
		}
		if !policy.Push {
			values["pushDisabled"] = "true"
			values["agent.smol.ai/push-disabled"] = "true"
		}
		if policy.CompactEvery > 0 {
			values["compactEvery"] = fmt.Sprintf("%d", policy.CompactEvery)
			values["agent.smol.ai/compact-every"] = fmt.Sprintf("%d", policy.CompactEvery)
		}
		if policy.SquashEvery > 0 {
			values["squashEvery"] = fmt.Sprintf("%d", policy.SquashEvery)
			values["agent.smol.ai/squash-every"] = fmt.Sprintf("%d", policy.SquashEvery)
		}
		if policy.CheckpointTagPrefix != "" {
			values["checkpointTagPrefix"] = policy.CheckpointTagPrefix
			values["agent.smol.ai/checkpoint-tag-prefix"] = policy.CheckpointTagPrefix
		}
		if policy.PreserveSigned {
			values["preserveSigned"] = "true"
			values["agent.smol.ai/preserve-signed"] = "true"
		}
		if policy.PruneLocal {
			values["pruneLocal"] = "true"
			values["agent.smol.ai/prune-local"] = "true"
		}
		if policy.PruneRemote {
			values["pruneRemote"] = "true"
			values["agent.smol.ai/prune-remote"] = "true"
		}
	}
	if autoSnapshot {
		values["autoSnapshot"] = "true"
		values["agent.smol.ai/auto-snapshot"] = "true"
	}
	for key, value := range values {
		if strings.TrimSpace(value) == "" {
			delete(values, key)
		}
	}
	return values, nil
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
	return SafeVolumePathSegment(value)
}

// SafeVolumePathSegment maps a CSI volume ID to one collision-resistant path
// component. IDs already safe as a single component remain unchanged so
// existing node workspaces continue to resolve after upgrades.
func SafeVolumePathSegment(value string) string {
	if safeVolumeIDPattern.MatchString(value) && value != "." && value != ".." {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	// Tilde is outside safeVolumeIDPattern, so unchanged safe IDs cannot
	// collide with the encoded namespace.
	return fmt.Sprintf("~volume-%x", sum[:16])
}
