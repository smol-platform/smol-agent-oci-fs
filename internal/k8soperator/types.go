package k8soperator

import (
	"fmt"
	"strings"
	"time"
)

const (
	APIVersion = "agent.smol.ai/v1alpha1"

	KindAgentOCIFileSystem     = "AgentOCIFileSystem"
	KindAgentOCISnapshotPolicy = "AgentOCISnapshotPolicy"
	KindAgentOCISnapshot       = "AgentOCISnapshot"
	KindAgentOCIRuntimeClass   = "AgentOCIRuntimeClass"
)

type TypeMeta struct {
	APIVersion string `json:"apiVersion" yaml:"apiVersion"`
	Kind       string `json:"kind" yaml:"kind"`
}

type ObjectMeta struct {
	Name      string            `json:"name" yaml:"name"`
	Namespace string            `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	UID       string            `json:"uid,omitempty" yaml:"uid,omitempty"`
	Labels    map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

type LocalObjectReference struct {
	Name string `json:"name" yaml:"name"`
}

type SecretKeySelector struct {
	Name string `json:"name" yaml:"name"`
	Key  string `json:"key" yaml:"key"`
}

type AgentOCIFileSystem struct {
	TypeMeta   `json:",inline" yaml:",inline"`
	ObjectMeta ObjectMeta             `json:"metadata" yaml:"metadata"`
	Spec       AgentOCIFileSystemSpec `json:"spec" yaml:"spec"`
	Status     AgentOCIStatus         `json:"status,omitempty" yaml:"status,omitempty"`
}

type AgentOCIFileSystemSpec struct {
	BaseImage         string                `json:"baseImage" yaml:"baseImage"`
	StateRef          string                `json:"stateRef" yaml:"stateRef"`
	Branch            string                `json:"branch,omitempty" yaml:"branch,omitempty"`
	SourceRef         string                `json:"sourceRef,omitempty" yaml:"sourceRef,omitempty"`
	MountMode         string                `json:"mountMode,omitempty" yaml:"mountMode,omitempty"`
	RegistrySecretRef *LocalObjectReference `json:"registrySecretRef,omitempty" yaml:"registrySecretRef,omitempty"`
	Encryption        *EncryptionSpec       `json:"encryption,omitempty" yaml:"encryption,omitempty"`
	Signing           *SigningSpec          `json:"signing,omitempty" yaml:"signing,omitempty"`
	SnapshotPolicyRef *LocalObjectReference `json:"snapshotPolicyRef,omitempty" yaml:"snapshotPolicyRef,omitempty"`
	RuntimeClassRef   *LocalObjectReference `json:"runtimeClassRef,omitempty" yaml:"runtimeClassRef,omitempty"`
}

type EncryptionSpec struct {
	Recipients string             `json:"recipients,omitempty" yaml:"recipients,omitempty"`
	SecretRef  *SecretKeySelector `json:"secretRef,omitempty" yaml:"secretRef,omitempty"`
}

type SigningSpec struct {
	Signer                       string             `json:"signer,omitempty" yaml:"signer,omitempty"`
	Attestation                  string             `json:"attestation,omitempty" yaml:"attestation,omitempty"`
	TrustedKeySecretRef          *SecretKeySelector `json:"trustedKeySecretRef,omitempty" yaml:"trustedKeySecretRef,omitempty"`
	IdentityTokenSecretRef       *SecretKeySelector `json:"identityTokenSecretRef,omitempty" yaml:"identityTokenSecretRef,omitempty"`
	CertificateIdentity          string             `json:"certificateIdentity,omitempty" yaml:"certificateIdentity,omitempty"`
	CertificateIdentityRegexp    string             `json:"certificateIdentityRegexp,omitempty" yaml:"certificateIdentityRegexp,omitempty"`
	CertificateOIDCIssuer        string             `json:"certificateOIDCIssuer,omitempty" yaml:"certificateOIDCIssuer,omitempty"`
	CertificateOIDCIssuerRegexp  string             `json:"certificateOIDCIssuerRegexp,omitempty" yaml:"certificateOIDCIssuerRegexp,omitempty"`
	SigstoreTrustedRoot          string             `json:"sigstoreTrustedRoot,omitempty" yaml:"sigstoreTrustedRoot,omitempty"`
	SigstoreIgnoreTlog           bool               `json:"sigstoreIgnoreTlog,omitempty" yaml:"sigstoreIgnoreTlog,omitempty"`
	SigstoreIgnoreTimestamp      bool               `json:"sigstoreIgnoreTimestamp,omitempty" yaml:"sigstoreIgnoreTimestamp,omitempty"`
	SigstoreIgnoreCertificateSCT bool               `json:"sigstoreIgnoreCertificateSCT,omitempty" yaml:"sigstoreIgnoreCertificateSCT,omitempty"`
}

type AgentOCISnapshotPolicy struct {
	TypeMeta   `json:",inline" yaml:",inline"`
	ObjectMeta ObjectMeta                 `json:"metadata" yaml:"metadata"`
	Spec       AgentOCISnapshotPolicySpec `json:"spec" yaml:"spec"`
	Status     AgentOCIStatus             `json:"status,omitempty" yaml:"status,omitempty"`
}

type AgentOCISnapshotPolicySpec struct {
	Every               string   `json:"every,omitempty" yaml:"every,omitempty"`
	MaxDirtyBytes       string   `json:"maxDirtyBytes,omitempty" yaml:"maxDirtyBytes,omitempty"`
	OnTurnBoundary      bool     `json:"onTurnBoundary,omitempty" yaml:"onTurnBoundary,omitempty"`
	Push                bool     `json:"push,omitempty" yaml:"push,omitempty"`
	CompactEvery        int      `json:"compactEvery,omitempty" yaml:"compactEvery,omitempty"`
	SquashEvery         int      `json:"squashEvery,omitempty" yaml:"squashEvery,omitempty"`
	CheckpointTagPrefix string   `json:"checkpointTagPrefix,omitempty" yaml:"checkpointTagPrefix,omitempty"`
	KeepSnapshots       []string `json:"keepSnapshots,omitempty" yaml:"keepSnapshots,omitempty"`
	PreserveSigned      bool     `json:"preserveSigned,omitempty" yaml:"preserveSigned,omitempty"`
	PruneLocal          bool     `json:"pruneLocal,omitempty" yaml:"pruneLocal,omitempty"`
	PruneRemote         bool     `json:"pruneRemote,omitempty" yaml:"pruneRemote,omitempty"`
}

type AgentOCISnapshot struct {
	TypeMeta   `json:",inline" yaml:",inline"`
	ObjectMeta ObjectMeta           `json:"metadata" yaml:"metadata"`
	Spec       AgentOCISnapshotSpec `json:"spec" yaml:"spec"`
	Status     AgentOCIStatus       `json:"status,omitempty" yaml:"status,omitempty"`
}

type AgentOCISnapshotSpec struct {
	FileSystemName   string `json:"fileSystemName" yaml:"fileSystemName"`
	FileSystemUID    string `json:"fileSystemUID,omitempty" yaml:"fileSystemUID,omitempty"`
	SnapshotDigest   string `json:"snapshotDigest,omitempty" yaml:"snapshotDigest,omitempty"`
	ParentDigest     string `json:"parentDigest,omitempty" yaml:"parentDigest,omitempty"`
	Branch           string `json:"branch,omitempty" yaml:"branch,omitempty"`
	CheckpointDigest string `json:"checkpointDigest,omitempty" yaml:"checkpointDigest,omitempty"`
}

type AgentOCIRuntimeClass struct {
	TypeMeta   `json:",inline" yaml:",inline"`
	ObjectMeta ObjectMeta               `json:"metadata" yaml:"metadata"`
	Spec       AgentOCIRuntimeClassSpec `json:"spec" yaml:"spec"`
	Status     AgentOCIStatus           `json:"status,omitempty" yaml:"status,omitempty"`
}

type AgentOCIRuntimeClassSpec struct {
	MountMode         string            `json:"mountMode,omitempty" yaml:"mountMode,omitempty"`
	CacheRoot         string            `json:"cacheRoot,omitempty" yaml:"cacheRoot,omitempty"`
	RuntimeImage      string            `json:"runtimeImage,omitempty" yaml:"runtimeImage,omitempty"`
	PrivilegedOverlay bool              `json:"privilegedOverlay,omitempty" yaml:"privilegedOverlay,omitempty"`
	FUSE              bool              `json:"fuse,omitempty" yaml:"fuse,omitempty"`
	LazyFUSE          bool              `json:"lazyFuse,omitempty" yaml:"lazyFuse,omitempty"`
	NodeSelector      map[string]string `json:"nodeSelector,omitempty" yaml:"nodeSelector,omitempty"`
}

type AgentOCIStatus struct {
	ObservedGeneration   int64       `json:"observedGeneration,omitempty" yaml:"observedGeneration,omitempty"`
	Conditions           []Condition `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	LastSnapshotDigest   string      `json:"lastSnapshotDigest,omitempty" yaml:"lastSnapshotDigest,omitempty"`
	LastCheckpointDigest string      `json:"lastCheckpointDigest,omitempty" yaml:"lastCheckpointDigest,omitempty"`
	LastError            string      `json:"lastError,omitempty" yaml:"lastError,omitempty"`
}

type Condition struct {
	Type               string    `json:"type" yaml:"type"`
	Status             string    `json:"status" yaml:"status"`
	Reason             string    `json:"reason,omitempty" yaml:"reason,omitempty"`
	Message            string    `json:"message,omitempty" yaml:"message,omitempty"`
	LastTransitionTime time.Time `json:"lastTransitionTime,omitempty" yaml:"lastTransitionTime,omitempty"`
}

func FileSystemName(fs AgentOCIFileSystem) string {
	if fs.ObjectMeta.Namespace == "" {
		return fs.ObjectMeta.Name
	}
	return fs.ObjectMeta.Namespace + "/" + fs.ObjectMeta.Name
}

func ValidateFileSystem(fs AgentOCIFileSystem) error {
	var missing []string
	if strings.TrimSpace(fs.ObjectMeta.Name) == "" {
		missing = append(missing, "metadata.name")
	}
	if strings.TrimSpace(fs.Spec.BaseImage) == "" {
		missing = append(missing, "spec.baseImage")
	}
	if strings.TrimSpace(fs.Spec.StateRef) == "" {
		missing = append(missing, "spec.stateRef")
	}
	if len(missing) > 0 {
		return fmt.Errorf("invalid AgentOCIFileSystem %s: missing %s", FileSystemName(fs), strings.Join(missing, ", "))
	}
	return nil
}

func NormalizeFileSystem(fs AgentOCIFileSystem) AgentOCIFileSystem {
	if fs.TypeMeta.APIVersion == "" {
		fs.TypeMeta.APIVersion = APIVersion
	}
	if fs.TypeMeta.Kind == "" {
		fs.TypeMeta.Kind = KindAgentOCIFileSystem
	}
	if fs.Spec.Branch == "" {
		fs.Spec.Branch = "main"
	}
	if fs.Spec.MountMode == "" {
		fs.Spec.MountMode = "auto"
	}
	return fs
}

func ConditionStatus(status bool) string {
	if status {
		return "True"
	}
	return "False"
}

func SetCondition(status *AgentOCIStatus, condition Condition) {
	if condition.LastTransitionTime.IsZero() {
		condition.LastTransitionTime = time.Now().UTC().Truncate(time.Second)
	}
	for i, existing := range status.Conditions {
		if existing.Type == condition.Type {
			status.Conditions[i] = condition
			return
		}
	}
	status.Conditions = append(status.Conditions, condition)
}
