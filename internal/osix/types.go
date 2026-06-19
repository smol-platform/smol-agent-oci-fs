package osix

import "time"

const (
	Version = "0.1"

	MediaTypeOCIManifest = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeConfig      = "application/vnd.osix.agent.config.v1+json"
	MediaTypeLayer       = "application/vnd.osix.agent.layer.diff.v1.tar+zstd"
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
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type Manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	ArtifactType  string            `json:"artifactType"`
	Config        Descriptor        `json:"config"`
	Layers        []Descriptor      `json:"layers"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

type InitOptions struct {
	Base          string
	Name          string
	StateRef      string
	Mount         string
	DefaultBranch string
}

type SnapshotOptions struct {
	Message string
	Tag     string
	AlsoTag string
}

type SnapshotResult struct {
	ManifestDigest string
	Tags           []string
}

type RestoreOptions struct {
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
