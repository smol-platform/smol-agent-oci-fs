package k8soperator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishAndSnapshotPlans(t *testing.T) {
	empty := NormalizeFileSystem(AgentOCIFileSystem{
		ObjectMeta: ObjectMeta{Name: "agent-empty", Namespace: "default"},
		Spec: AgentOCIFileSystemSpec{
			BaseImage: "example/base:latest",
			StateRef:  "127.0.0.1:5000/acme/agent-empty",
			MountMode: "materialized",
		},
	})
	emptyPlan, err := PublishPlan(empty, VolumePlanOptions{WorkspaceRoot: "/var/lib/osix", TargetPath: "/pods/empty", VolumeID: "pvc-empty"})
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyPlan.Steps) != 2 || emptyPlan.Steps[1].Name != "prepare-empty" {
		t.Fatalf("unexpected empty publish plan: %#v", emptyPlan)
	}

	fs := NormalizeFileSystem(AgentOCIFileSystem{
		ObjectMeta: ObjectMeta{Name: "agent-a", Namespace: "default"},
		Spec: AgentOCIFileSystemSpec{
			BaseImage:         "example/base:latest",
			StateRef:          "127.0.0.1:5000/acme/agent-a",
			SourceRef:         "127.0.0.1:5000/acme/agent-a:main",
			MountMode:         "materialized",
			RegistrySecretRef: &LocalObjectReference{Name: "registry-auth"},
			Encryption: &EncryptionSpec{
				Recipients: "age:age1example",
				SecretRef:  &SecretKeySelector{Name: "age-identities", Key: "identity.txt"},
			},
			Signing: &SigningSpec{
				Signer:                 "sigstore-keyless",
				Attestation:            "slsa",
				TrustedKeySecretRef:    &SecretKeySelector{Name: "trusted-key", Key: "cosign.pub"},
				IdentityTokenSecretRef: &SecretKeySelector{Name: "oidc-token", Key: "token"},
			},
		},
	})
	plan, err := PublishPlan(fs, VolumePlanOptions{WorkspaceRoot: "/var/lib/osix", TargetPath: "/pods/vol", VolumeID: "pvc-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 3 || plan.Steps[1].Name != "pull" || plan.Steps[2].Name != "mount" {
		t.Fatalf("unexpected publish plan: %#v", plan)
	}
	if got := strings.Join(plan.Steps[2].Command, " "); !strings.Contains(got, "--mode materialized") {
		t.Fatalf("mount plan missing mode: %s", got)
	}
	if plan.Steps[0].Env["OSIX_REGISTRY_SECRET_NAME"] != "registry-auth" {
		t.Fatalf("registry secret env missing: %#v", plan.Steps[0].Env)
	}
	for _, want := range []string{"OSIX_ENCRYPTION_SECRET_NAME", "OSIX_ENCRYPTION_SECRET_KEY", "OSIX_TRUSTED_KEY_SECRET_NAME", "OSIX_IDENTITY_TOKEN_SECRET_NAME"} {
		if plan.Steps[0].Env[want] == "" {
			t.Fatalf("secret reference env %q missing: %#v", want, plan.Steps[0].Env)
		}
	}

	snap, err := SnapshotPlan(fs, VolumePlanOptions{
		TargetPath: "/pods/vol",
		Policy: &AgentOCISnapshotPolicySpec{
			MaxDirtyBytes:       "1MiB",
			Push:                true,
			CompactEvery:        1,
			SquashEvery:         2,
			CheckpointTagPrefix: "checkpoint",
			PruneLocal:          true,
			PruneRemote:         true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := strings.Join(snap.Steps[0].Command, " ")
	for _, want := range []string{"osix watch /pods/vol --once", "--push", "--encrypt age:age1example", "--sign sigstore-keyless", "--attest slsa", "--compact-every 1", "--squash-every 2", "--prune-local", "--prune-remote"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("snapshot command missing %q: %s", want, cmd)
		}
	}
}

func TestSafeVolumePathSegmentContainsAndSeparatesUnsafeIDs(t *testing.T) {
	root := t.TempDir()
	ids := []string{".", "..", "a/b", "a:b", "a-b", "日本語", ""}
	seen := map[string]string{}
	for _, id := range ids {
		segment := SafeVolumePathSegment(id)
		if segment == "." || segment == ".." || strings.ContainsAny(segment, `/\\`) {
			t.Fatalf("unsafe segment %q for volume ID %q", segment, id)
		}
		if previous, ok := seen[segment]; ok {
			t.Fatalf("volume IDs %q and %q collide as %q", previous, id, segment)
		}
		seen[segment] = id
		path := filepath.Join(root, segment)
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatalf("volume ID %q escaped root as %q: %v", id, path, err)
		}
	}
	if got := SafeVolumePathSegment("pvc-existing"); got != "pvc-existing" {
		t.Fatalf("safe existing ID remapped to %q", got)
	}
	encoded := SafeVolumePathSegment("a/b")
	if got := SafeVolumePathSegment(encoded); got == encoded {
		t.Fatalf("encoded namespace collides with unchanged safe ID %q", encoded)
	}
}

func TestValidateFileSystemAndRenderInstall(t *testing.T) {
	if err := ValidateFileSystem(AgentOCIFileSystem{}); err == nil {
		t.Fatal("expected validation error")
	}
	manifest := RenderInstallManifests()
	for _, want := range []string{
		"kind: CustomResourceDefinition",
		"AgentOCIFileSystem",
		"AgentOCISnapshotPolicy",
		"kind: CSIDriver",
		"kind: DaemonSet",
		"serve-csi",
		"csi-node-driver-registrar",
		"kind: StorageClass",
	} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("install manifest missing %q", want)
		}
	}
}

func TestRenderedInstallMatchesCanonicalKustomizeResources(t *testing.T) {
	deployRoot := filepath.Join("..", "..", "deploy", "kubernetes")
	kustomization, err := os.ReadFile(filepath.Join(deployRoot, "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	inResources := false
	var parts []string
	for _, line := range strings.Split(string(kustomization), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "resources:" {
			inResources = true
			continue
		}
		if !inResources {
			continue
		}
		if strings.HasPrefix(trimmed, "-") {
			resource := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			data, err := os.ReadFile(filepath.Join(deployRoot, filepath.FromSlash(resource)))
			if err != nil {
				t.Fatal(err)
			}
			parts = append(parts, strings.TrimSpace(string(data)))
		}
	}
	want := strings.Join(parts, "\n---\n") + "\n"
	if got := RenderInstallManifests(); got != want {
		t.Fatal("generated render-install stream drifted from deploy/kubernetes resources; run go generate ./internal/k8soperator")
	}
	for _, hardened := range []string{"allowPrivilegeEscalation: false", "readOnlyRootFilesystem: true", "imagePullPolicy: IfNotPresent"} {
		if !strings.Contains(want, hardened) {
			t.Fatalf("canonical install stream lost hardened setting %q", hardened)
		}
	}
}

func TestCSIVolumeContextIncludesFilesystemAndPolicy(t *testing.T) {
	fs := NormalizeFileSystem(AgentOCIFileSystem{
		ObjectMeta: ObjectMeta{Name: "agent-context", Namespace: "agents", UID: "uid-1"},
		Spec: AgentOCIFileSystemSpec{
			BaseImage:         "example/base:latest",
			StateRef:          "registry.example/agents/context",
			SourceRef:         "registry.example/agents/context:main",
			MountMode:         "materialized",
			RegistrySecretRef: &LocalObjectReference{Name: "registry-auth"},
			Encryption:        &EncryptionSpec{Recipients: "age1example"},
			Signing:           &SigningSpec{Signer: "cosign-key", Attestation: "slsa"},
		},
	})
	context, err := CSIVolumeContext(fs, &AgentOCISnapshotPolicySpec{
		Every:               "5s",
		MaxDirtyBytes:       "1MiB",
		OnTurnBoundary:      true,
		Push:                false,
		CompactEvery:        2,
		SquashEvery:         4,
		CheckpointTagPrefix: "checkpoint",
		PreserveSigned:      true,
		PruneLocal:          true,
		PruneRemote:         true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"name":                                "agent-context",
		"namespace":                           "agents",
		"stateRef":                            "registry.example/agents/context",
		"sourceRef":                           "registry.example/agents/context:main",
		"mountMode":                           "materialized",
		"registrySecretRef":                   "registry-auth",
		"encryptionRecipients":                "age1example",
		"signer":                              "cosign-key",
		"attestation":                         "slsa",
		"autoSnapshot":                        "true",
		"snapshotEvery":                       "5s",
		"maxDirtyBytes":                       "1MiB",
		"pushDisabled":                        "true",
		"compactEvery":                        "2",
		"squashEvery":                         "4",
		"checkpointTagPrefix":                 "checkpoint",
		"agent.smol.ai/registry-secret":       "registry-auth",
		"agent.smol.ai/auto-snapshot":         "true",
		"agent.smol.ai/checkpoint-tag-prefix": "checkpoint",
		"agent.smol.ai/encryption-recipients": "age1example",
		"agent.smol.ai/signer":                "cosign-key",
		"agent.smol.ai/attestation":           "slsa",
		"agent.smol.ai/push-disabled":         "true",
		"agent.smol.ai/on-turn-boundary":      "true",
		"agent.smol.ai/preserve-signed":       "true",
		"agent.smol.ai/prune-local":           "true",
		"agent.smol.ai/prune-remote":          "true",
		"agent.smol.ai/max-dirty-bytes":       "1MiB",
		"agent.smol.ai/snapshot-every":        "5s",
		"agent.smol.ai/compact-every":         "2",
		"agent.smol.ai/squash-every":          "4",
	} {
		if got := context[key]; got != want {
			t.Fatalf("context[%q] = %q, want %q in %#v", key, got, want, context)
		}
	}
}
