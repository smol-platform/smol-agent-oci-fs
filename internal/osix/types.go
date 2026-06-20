package osix

import "time"

const (
	Version = "0.1"

	MediaTypeOCIManifest = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeOCIIndex    = "application/vnd.oci.image.index.v1+json"
	MediaTypeConfig      = "application/vnd.osix.agent.config.v1+json"
	MediaTypeLayer       = "application/vnd.osix.agent.layer.diff.v1.tar+zstd"
	MediaTypeLayerEnc    = "application/vnd.osix.agent.layer.diff.v1.tar+zstd+encrypted"
	MediaTypeEmptyConfig = "application/vnd.osix.empty.v1+json"
	MediaTypeSignature   = "application/vnd.osix.agent.signature.v1+json"
	MediaTypeProvenance  = "application/vnd.osix.agent.provenance.v1+json"
	ArtifactTypeSnapshot = "application/vnd.osix.agent.snapshot.v1"
)

type WorkspaceConfig struct {
	OSIxVersion   string `json:"osixVersion"`
	Name          string `json:"name"`
	Base          string `json:"base"`
	BaseDigest    string `json:"baseDigest"`
	StateRef      string `json:"stateRef"`
	Mount         string `json:"mount"`
	DefaultBranch string `json:"defaultBranch"`
	Encrypt       string `json:"encrypt,omitempty"`
}

type AgentConfig struct {
	OSIxVersion string        `json:"osixVersion"`
	Agent       AgentIdentity `json:"agent"`
	Base        BaseRef       `json:"base"`
	Parent      *ParentRef    `json:"parent,omitempty"`
	Runtime     RuntimeConfig `json:"runtime"`
	StateRoots  []StateRoot   `json:"stateRoots"`
	Snapshot    SnapshotMeta  `json:"snapshot"`
	Integrity   Integrity     `json:"integrity"`
	Tree        []TreeEntry   `json:"tree"`
}

type AgentIdentity struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedBy string `json:"createdBy,omitempty"`
}

type BaseRef struct {
	Image  string `json:"image"`
	Digest string `json:"digest"`
}

type ParentRef struct {
	Snapshot string `json:"snapshot,omitempty"`
	Digest   string `json:"digest"`
}

type RuntimeConfig struct {
	Entrypoint []string `json:"entrypoint,omitempty"`
	WorkingDir string   `json:"workingDir"`
	UID        int      `json:"uid"`
	GID        int      `json:"gid"`
}

type StateRoot struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
}

type SnapshotMeta struct {
	ID         string    `json:"id"`
	Sequence   int64     `json:"sequence"`
	CreatedAt  time.Time `json:"createdAt"`
	Reason     string    `json:"reason"`
	Message    string    `json:"message,omitempty"`
	DirtyBytes int64     `json:"dirtyBytes"`
}

type Integrity struct {
	MTreeDigest string `json:"mtreeDigest,omitempty"`
	LayerDigest string `json:"layerDigest"`
}

type TreeEntry struct {
	Path     string `json:"path"`
	Type     string `json:"type"`
	Mode     int64  `json:"mode"`
	Size     int64  `json:"size,omitempty"`
	Digest   string `json:"digest,omitempty"`
	Linkname string `json:"linkname,omitempty"`
}

type Descriptor struct {
	MediaType    string            `json:"mediaType"`
	ArtifactType string            `json:"artifactType,omitempty"`
	Digest       string            `json:"digest"`
	Size         int64             `json:"size"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

type Manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	ArtifactType  string            `json:"artifactType"`
	Config        Descriptor        `json:"config"`
	Layers        []Descriptor      `json:"layers"`
	Annotations   map[string]string `json:"annotations,omitempty"`
	Subject       *Descriptor       `json:"subject,omitempty"`
}

type Index struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Manifests     []Descriptor `json:"manifests"`
}

type InitOptions struct {
	Base          string
	Name          string
	StateRef      string
	Mount         string
	DefaultBranch string
	Encrypt       string
}

type SnapshotOptions struct {
	Message        string
	Tag            string
	AlsoTag        string
	Encrypt        string
	Sign           string
	Attest         string
	ExpectedParent string
	SecretScan     string
	Checkpoint     bool
}

type SnapshotResult struct {
	ManifestDigest string
	Tags           []string
}

type RestoreOptions struct {
	Force   bool
	Decrypt string
}

type MountMode string

const (
	MountAuto         MountMode = "auto"
	MountOverlay      MountMode = "overlay"
	MountFUSE         MountMode = "fuse"
	MountMaterialized MountMode = "materialized"
)

type MountOptions struct {
	Force    bool
	RW       bool
	ReadOnly bool
	Branch   string
	Decrypt  string
	Mode     MountMode
	Cache    string
	Lazy     bool
}

type MountInfo struct {
	Target       string    `json:"target"`
	SourceRef    string    `json:"sourceRef"`
	SourceDigest string    `json:"sourceDigest"`
	Mode         MountMode `json:"mode"`
	Branch       string    `json:"branch,omitempty"`
	RW           bool      `json:"rw"`
	UpperDir     string    `json:"upperDir,omitempty"`
	WorkDir      string    `json:"workDir,omitempty"`
	LowerDir     string    `json:"lowerDir,omitempty"`
	State        string    `json:"state,omitempty"`
	PID          int       `json:"pid,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt,omitempty"`
}

type UnmountOptions struct {
	Force bool
}

type Change struct {
	Kind string
	Path string
}

type Ref struct {
	Name   string
	Digest string
}

type VerifyOptions struct {
	TrustedKey string
}

type VerifyResult struct {
	ManifestDigest   string
	SignatureDigest  string
	ProvenanceDigest string
	Signer           string
}

type WatchOptions struct {
	Every          time.Duration
	MaxDirtyBytes  int64
	OnTurnBoundary bool
	Push           bool
	Encrypt        string
	Once           bool
	Iterations     int
	TagPrefix      string
}

type WatchResult struct {
	Snapshots []SnapshotResult `json:"snapshots"`
	StatePath string           `json:"statePath"`
}

type WatchState struct {
	Target       string    `json:"target"`
	LastSnapshot string    `json:"lastSnapshot,omitempty"`
	LastError    string    `json:"lastError,omitempty"`
	Iterations   int       `json:"iterations"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type CompactPolicy struct {
	SquashEvery    int
	KeepSnapshots  []string
	PreserveSigned bool
	DryRun         bool
	CheckpointTag  string
}

type CompactPlan struct {
	SourceRef        string   `json:"sourceRef"`
	SourceDigest     string   `json:"sourceDigest"`
	ChainLength      int      `json:"chainLength"`
	CreateCheckpoint bool     `json:"createCheckpoint"`
	CheckpointTag    string   `json:"checkpointTag,omitempty"`
	CheckpointDigest string   `json:"checkpointDigest,omitempty"`
	Keep             []string `json:"keep"`
	DeleteCandidates []string `json:"deleteCandidates"`
	Reasons          []string `json:"reasons"`
}
