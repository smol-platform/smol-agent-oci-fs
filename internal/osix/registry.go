package osix

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type RegistryReference struct {
	Registry  string
	Repo      string
	Reference string
}

type RegistryProbeOptions struct {
	Tag string
}

type RegistryProbeResult struct {
	Repository     string   `json:"repository"`
	RegistryHost   string   `json:"registryHost"`
	Tag            string   `json:"tag"`
	ConfigDigest   string   `json:"configDigest"`
	LayerDigest    string   `json:"layerDigest"`
	ManifestDigest string   `json:"manifestDigest"`
	Operations     []string `json:"operations"`
	Result         string   `json:"result"`
	TestedAt       string   `json:"testedAt"`
}

type RemoteBranchConflictError struct {
	Tag      string
	Expected string
	Current  string
	Missing  bool
}

func (e RemoteBranchConflictError) Error() string {
	if e.Missing {
		return fmt.Sprintf("remote branch conflict for %s: expected %s but current is missing", e.Tag, e.Expected)
	}
	return fmt.Sprintf("remote branch conflict for %s: expected %s but current is %s", e.Tag, e.Expected, e.Current)
}

func IsRemoteBranchConflict(err error) bool {
	var conflict RemoteBranchConflictError
	return errors.As(err, &conflict)
}

func ParseRegistryReference(ref string) (RegistryReference, error) {
	var out RegistryReference
	if strings.TrimSpace(ref) == "" {
		return out, fmt.Errorf("empty registry reference")
	}
	var name, reference string
	if before, after, ok := strings.Cut(ref, "@"); ok {
		name = before
		reference = after
	} else {
		slash := strings.LastIndex(ref, "/")
		colon := strings.LastIndex(ref, ":")
		if colon <= slash {
			return out, fmt.Errorf("registry reference %q must include tag or digest", ref)
		}
		name = ref[:colon]
		reference = ref[colon+1:]
	}
	parts := strings.SplitN(name, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return out, fmt.Errorf("registry reference %q must be REGISTRY/REPO:TAG or REGISTRY/REPO@DIGEST", ref)
	}
	return RegistryReference{Registry: parts[0], Repo: parts[1], Reference: reference}, nil
}

func IsRegistryReference(ref string) bool {
	_, err := ParseRegistryReference(ref)
	return err == nil
}

func ProbeRegistryAccess(remoteRepo string, opts RegistryProbeOptions) (RegistryProbeResult, error) {
	remote, err := parseRegistryRepo(remoteRepo)
	if err != nil {
		return RegistryProbeResult{}, err
	}
	tag := strings.TrimSpace(opts.Tag)
	if tag == "" {
		tag = "osix-probe-" + time.Now().UTC().Format("20060102T150405Z")
	}
	client := newRegistryClient(remote.Registry, remote.Repo)
	configData := []byte(`{"createdBy":"osix registry probe"}`)
	layerData := []byte("osix registry probe\n")
	configDigest := digestBytes(configData)
	layerDigest := digestBytes(layerData)
	manifest := Manifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCIManifest,
		Config: Descriptor{
			MediaType: MediaTypeOCIConfig,
			Digest:    configDigest,
			Size:      int64(len(configData)),
		},
		Layers: []Descriptor{{
			MediaType: MediaTypeLayer,
			Digest:    layerDigest,
			Size:      int64(len(layerData)),
		}},
		Annotations: map[string]string{
			"org.opencontainers.image.title": "osix registry probe",
		},
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		return RegistryProbeResult{}, err
	}
	manifestDigest := digestBytes(manifestData)
	if err := client.putBlob(configDigest, configData); err != nil {
		return RegistryProbeResult{}, err
	}
	if err := client.putBlob(layerDigest, layerData); err != nil {
		return RegistryProbeResult{}, err
	}
	if err := client.putManifest(tag, manifestData); err != nil {
		return RegistryProbeResult{}, err
	}
	pulledManifest, pulledDigest, err := client.getManifest(tag)
	if err != nil {
		return RegistryProbeResult{}, err
	}
	if pulledDigest != manifestDigest {
		return RegistryProbeResult{}, fmt.Errorf("probe manifest digest mismatch: pushed %s got %s", manifestDigest, pulledDigest)
	}
	if !bytes.Equal(pulledManifest, manifestData) {
		return RegistryProbeResult{}, fmt.Errorf("probe manifest content mismatch for %s", tag)
	}
	pulledLayer, err := client.getBlob(layerDigest)
	if err != nil {
		return RegistryProbeResult{}, err
	}
	if !bytes.Equal(pulledLayer, layerData) {
		return RegistryProbeResult{}, fmt.Errorf("probe layer content mismatch for %s", layerDigest)
	}
	return RegistryProbeResult{
		Repository:     remoteRepo,
		RegistryHost:   remote.Registry,
		Tag:            tag,
		ConfigDigest:   configDigest,
		LayerDigest:    layerDigest,
		ManifestDigest: manifestDigest,
		Operations: []string{
			"put-config-blob",
			"put-layer-blob",
			"put-manifest",
			"get-manifest",
			"get-layer-blob",
		},
		Result:   "passed",
		TestedAt: time.Now().UTC().Truncate(time.Second).Format(time.RFC3339),
	}, nil
}

func PushSnapshot(workspaceRoot, remoteRepo, ref string, extraTags []string) error {
	return PushSnapshotWithOptions(workspaceRoot, remoteRepo, ref, extraTags, PushOptions{})
}

func PruneRemoteSnapshots(remoteRepo string, digests []string) ([]string, error) {
	remote, err := parseRegistryRepo(remoteRepo)
	if err != nil {
		return nil, err
	}
	client := newRegistryClient(remote.Registry, remote.Repo)
	var deleted []string
	for _, digest := range uniqueTags(digests) {
		if strings.TrimSpace(digest) == "" {
			continue
		}
		if _, err := digestHex(digest); err != nil {
			return deleted, err
		}
		if err := client.deleteManifest(digest); err != nil {
			if isRegistryNotFound(err) {
				continue
			}
			return deleted, err
		}
		deleted = append(deleted, digest)
	}
	return deleted, nil
}

func PushSnapshotWithOptions(workspaceRoot, remoteRepo, ref string, extraTags []string, opts PushOptions) error {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return err
	}
	digest, _, _, err := s.loadManifest(ref)
	if err != nil {
		return err
	}
	remote, err := parseRegistryRepo(remoteRepo)
	if err != nil {
		return err
	}
	client := newRegistryClient(remote.Registry, remote.Repo)
	chain, err := s.snapshotChainWithDigests(digest)
	if err != nil {
		return err
	}
	if len(chain) == 0 {
		return fmt.Errorf("snapshot chain for %s is empty", digest)
	}
	head := chain[len(chain)-1]
	if err := checkRemoteTagPreconditions(client, uniqueTags(append(extraTags, head.Config.Snapshot.ID)), head.Config.Snapshot.ID, digest, opts.ExpectedParent); err != nil {
		return err
	}
	for _, item := range chain {
		cfgData, err := s.readBlob(item.Manifest.Config.Digest)
		if err != nil {
			return err
		}
		if err := client.putBlob(item.Manifest.Config.Digest, cfgData); err != nil {
			return err
		}
		for _, layer := range item.Manifest.Layers {
			layerData, err := s.readBlob(layer.Digest)
			if err != nil {
				return err
			}
			if err := client.putBlob(layer.Digest, layerData); err != nil {
				return err
			}
		}
		manifestData, err := s.readBlob(item.Digest)
		if err != nil {
			return err
		}
		if item.Config.Snapshot.ID != "" {
			if err := client.putManifest(item.Config.Snapshot.ID, manifestData); err != nil {
				return err
			}
		}
		if item.Digest == digest {
			for _, tag := range uniqueTags(append(extraTags, item.Config.Snapshot.ID)) {
				if err := client.putManifest(tag, manifestData); err != nil {
					return err
				}
			}
		}
		if err := pushSnapshotReferrers(s, client, item.Digest, int64(len(manifestData))); err != nil {
			return err
		}
	}
	return nil
}

func checkRemoteTagPreconditions(client registryClient, tags []string, snapshotID, newDigest, expectedParent string) error {
	expectedParent = strings.TrimSpace(expectedParent)
	if expectedParent == "" {
		return nil
	}
	for _, tag := range tags {
		if tag == "" || tag == snapshotID || strings.HasPrefix(tag, "sha256:") {
			continue
		}
		_, current, err := client.getManifest(tag)
		if err != nil {
			if isRegistryNotFound(err) {
				return RemoteBranchConflictError{Tag: tag, Expected: expectedParent, Missing: true}
			}
			return fmt.Errorf("check remote tag %s: %w", tag, err)
		}
		if current == newDigest {
			continue
		}
		if current != expectedParent {
			return RemoteBranchConflictError{Tag: tag, Expected: expectedParent, Current: current}
		}
	}
	return nil
}

func PullSnapshot(workspaceRoot, remoteRef, localTag string) (string, error) {
	return PullSnapshotWithOptions(workspaceRoot, remoteRef, localTag, PullOptions{})
}

func PullSnapshotWithOptions(workspaceRoot, remoteRef, localTag string, opts PullOptions) (string, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return "", err
	}
	ref, err := ParseRegistryReference(remoteRef)
	if err != nil {
		return "", err
	}
	client := newRegistryClient(ref.Registry, ref.Repo)
	digest, err := pullManifestRecursive(s, client, ref.Reference, opts)
	if err != nil {
		return "", err
	}
	if localTag != "" {
		if err := s.writeRef(localTag, digest); err != nil {
			return "", err
		}
	}
	return digest, nil
}

func pullManifestRecursive(s store, client registryClient, ref string, opts PullOptions) (string, error) {
	manifestData, digest, err := client.getManifest(ref)
	if err != nil {
		return "", err
	}
	if _, err := s.writeBlob(manifestData); err != nil {
		return "", err
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return "", fmt.Errorf("parse remote manifest %s: %w", digest, err)
	}
	cfgData, err := client.getBlob(manifest.Config.Digest)
	if err != nil {
		return "", err
	}
	if _, err := s.writeBlob(cfgData); err != nil {
		return "", err
	}
	for _, layer := range manifest.Layers {
		if opts.Lazy {
			if err := s.writeRemoteBlobSource(remoteBlobSource{Registry: client.registry, Repo: client.repo, Digest: layer.Digest}); err != nil {
				return "", err
			}
			continue
		}
		if err := fetchRemoteBlob(s, client, layer.Digest); err != nil {
			return "", err
		}
	}
	var cfg AgentConfig
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		return "", fmt.Errorf("parse remote config %s: %w", manifest.Config.Digest, err)
	}
	if cfg.Snapshot.ID != "" {
		if err := s.writeRef(cfg.Snapshot.ID, digest); err != nil {
			return "", err
		}
	}
	if cfg.Parent != nil {
		if _, err := pullManifestRecursive(s, client, cfg.Parent.Digest, opts); err != nil {
			return "", err
		}
	}
	if err := pullSnapshotReferrers(s, client, digest); err != nil {
		return "", err
	}
	return digest, nil
}

func fetchRemoteBlob(s store, client registryClient, digest string) error {
	if s.hasBlob(digest) {
		return nil
	}
	data, err := client.getBlob(digest)
	if err != nil {
		return err
	}
	if _, err := s.writeBlob(data); err != nil {
		return err
	}
	return nil
}

func fetchRemoteBlobFromSource(s store, digest string) error {
	source, err := s.readRemoteBlobSource(digest)
	if err != nil {
		return fmt.Errorf("no remote source for lazy blob %s: %w", digest, err)
	}
	client := newRegistryClient(source.Registry, source.Repo)
	return fetchRemoteBlob(s, client, digest)
}

func pushSnapshotReferrers(s store, client registryClient, subjectDigest string, subjectSize int64) error {
	subjectDesc := Descriptor{
		MediaType: MediaTypeOCIManifest,
		Digest:    subjectDigest,
		Size:      subjectSize,
	}
	emptyConfig := []byte("{}")
	for _, artifact := range []struct {
		refName   string
		mediaType string
	}{
		{refName: signatureRefName(subjectDigest), mediaType: MediaTypeSignature},
		{refName: provenanceRefName(subjectDigest), mediaType: MediaTypeProvenance},
	} {
		artifactDigest, err := s.resolveRef(artifact.refName)
		if err != nil {
			continue
		}
		artifactData, err := s.readBlob(artifactDigest)
		if err != nil {
			return err
		}
		if err := client.putBlob(artifactDigest, artifactData); err != nil {
			return err
		}
		if err := client.putBlob(digestBytes(emptyConfig), emptyConfig); err != nil {
			return err
		}
		manifestData, err := artifactReferrerManifest(subjectDesc, artifact.mediaType, artifactDigest, int64(len(artifactData)), MediaTypeEmptyConfig, emptyConfig, nil)
		if err != nil {
			return err
		}
		manifestDigest := digestBytes(manifestData)
		if err := client.putManifest(manifestDigest, manifestData); err != nil {
			return err
		}
		if err := client.putManifest(artifact.refName, manifestData); err != nil {
			return err
		}
	}
	if err := pushCosignSignature(s, client, subjectDigest, emptyConfig); err != nil {
		return err
	}
	if err := pushSigstoreBundles(s, client, subjectDesc, emptyConfig); err != nil {
		return err
	}
	return pushEncryptedLazyIndex(s, client, subjectDesc, emptyConfig)
}

func pushCosignSignature(s store, client registryClient, subjectDigest string, emptyConfig []byte) error {
	payloadDigest, err := s.resolveRef(cosignPayloadRefName(subjectDigest))
	if err != nil {
		return nil
	}
	metaDigest, err := s.resolveRef(cosignSignatureRefName(subjectDigest))
	if err != nil {
		return nil
	}
	payloadData, err := s.readBlob(payloadDigest)
	if err != nil {
		return err
	}
	metaData, err := s.readBlob(metaDigest)
	if err != nil {
		return err
	}
	var meta cosignSignatureMetadata
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return fmt.Errorf("parse cosign signature metadata for %s: %w", subjectDigest, err)
	}
	if meta.PayloadDigest != payloadDigest {
		return fmt.Errorf("cosign payload ref mismatch for %s: metadata has %s, ref has %s", subjectDigest, meta.PayloadDigest, payloadDigest)
	}
	if err := client.putBlob(payloadDigest, payloadData); err != nil {
		return err
	}
	if err := client.putBlob(digestBytes(emptyConfig), emptyConfig); err != nil {
		return err
	}
	manifest := Manifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCIManifest,
		ArtifactType:  "",
		Config: Descriptor{
			MediaType: MediaTypeOCIConfig,
			Digest:    digestBytes(emptyConfig),
			Size:      int64(len(emptyConfig)),
		},
		Layers: []Descriptor{{
			MediaType: MediaTypeCosignSimpleSigning,
			Digest:    payloadDigest,
			Size:      int64(len(payloadData)),
			Annotations: map[string]string{
				"dev.cosignproject.cosign/signature": meta.Signature,
			},
		}},
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	return client.putManifest(cosignSignatureTag(subjectDigest), manifestData)
}

func pushSigstoreBundles(s store, client registryClient, subjectDesc Descriptor, emptyConfig []byte) error {
	var descriptors []Descriptor
	created := sigstoreArtifactCreatedAt(s, subjectDesc.Digest)
	for _, bundle := range []struct {
		refName   string
		content   string
		predicate string
	}{
		{refName: sigstoreSignatureBundleRefName(subjectDesc.Digest), content: "message-signature"},
		{refName: sigstoreAttestationBundleRefName(subjectDesc.Digest), content: "dsse-envelope", predicate: "https://slsa.dev/provenance/v1"},
	} {
		bundleDigest, err := s.resolveRef(bundle.refName)
		if err != nil {
			continue
		}
		bundleData, err := s.readBlob(bundleDigest)
		if err != nil {
			return err
		}
		if err := client.putBlob(bundleDigest, bundleData); err != nil {
			return err
		}
		if err := client.putBlob(digestBytes(emptyConfig), emptyConfig); err != nil {
			return err
		}
		annotations := map[string]string{
			"dev.sigstore.bundle.content":      bundle.content,
			"org.opencontainers.image.created": created,
		}
		if bundle.predicate != "" {
			annotations["dev.sigstore.bundle.predicateType"] = bundle.predicate
		}
		manifestData, err := artifactReferrerManifest(subjectDesc, MediaTypeSigstoreBundle, bundleDigest, int64(len(bundleData)), MediaTypeOCIEmpty, emptyConfig, annotations)
		if err != nil {
			return err
		}
		manifestDigest := digestBytes(manifestData)
		if err := client.putManifest(manifestDigest, manifestData); err != nil {
			return err
		}
		descriptors = append(descriptors, Descriptor{
			MediaType:    MediaTypeOCIManifest,
			ArtifactType: MediaTypeSigstoreBundle,
			Digest:       manifestDigest,
			Size:         int64(len(manifestData)),
			Annotations:  annotations,
		})
	}
	if len(descriptors) == 0 {
		return nil
	}
	return client.putReferrersTag(sigstoreReferrersTag(subjectDesc.Digest), descriptors)
}

func pushEncryptedLazyIndex(s store, client registryClient, subjectDesc Descriptor, emptyConfig []byte) error {
	record, err := readEncryptedLazyIndexRecord(s, subjectDesc.Digest)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if record.IndexDigest == "" {
		return nil
	}
	for _, entry := range record.Files {
		if entry.Digest != "" {
			data, err := s.readBlob(entry.Digest)
			if err != nil {
				return err
			}
			if err := client.putBlob(entry.Digest, data); err != nil {
				return err
			}
		}
		for _, chunk := range entry.Chunks {
			if chunk.Digest == "" {
				continue
			}
			data, err := s.readBlob(chunk.Digest)
			if err != nil {
				return err
			}
			if err := client.putBlob(chunk.Digest, data); err != nil {
				return err
			}
		}
	}
	indexData, err := s.readBlob(record.IndexDigest)
	if err != nil {
		return err
	}
	if err := client.putBlob(record.IndexDigest, indexData); err != nil {
		return err
	}
	if err := client.putBlob(digestBytes(emptyConfig), emptyConfig); err != nil {
		return err
	}
	annotations := map[string]string{
		"com.osix.lazy.kind":               "encrypted-per-file-index",
		"org.opencontainers.image.created": record.CreatedAt.UTC().Format(time.RFC3339),
	}
	manifestData, err := artifactReferrerManifest(subjectDesc, MediaTypeLazyEncryptedIndex, record.IndexDigest, int64(len(indexData)), MediaTypeOCIEmpty, emptyConfig, annotations)
	if err != nil {
		return err
	}
	manifestDigest := digestBytes(manifestData)
	if err := client.putManifest(manifestDigest, manifestData); err != nil {
		return err
	}
	if err := client.putManifest(encryptedLazyIndexTag(subjectDesc.Digest), manifestData); err != nil {
		return err
	}
	return nil
}

func sigstoreArtifactCreatedAt(s store, subjectDigest string) string {
	metaDigest, err := s.resolveRef(cosignSignatureRefName(subjectDigest))
	if err == nil {
		if metaData, readErr := s.readBlob(metaDigest); readErr == nil {
			var meta cosignSignatureMetadata
			if json.Unmarshal(metaData, &meta) == nil && !meta.CreatedAt.IsZero() {
				return meta.CreatedAt.UTC().Format(time.RFC3339)
			}
		}
	}
	return time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
}

func artifactReferrerManifest(subject Descriptor, artifactType, blobDigest string, blobSize int64, configMediaType string, emptyConfig []byte, annotations map[string]string) ([]byte, error) {
	manifest := Manifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCIManifest,
		ArtifactType:  artifactType,
		Annotations:   annotations,
		Config: Descriptor{
			MediaType: configMediaType,
			Digest:    digestBytes(emptyConfig),
			Size:      int64(len(emptyConfig)),
		},
		Layers: []Descriptor{{
			MediaType: artifactType,
			Digest:    blobDigest,
			Size:      blobSize,
		}},
		Subject: &Descriptor{
			MediaType: subject.MediaType,
			Digest:    subject.Digest,
			Size:      subject.Size,
		},
	}
	return json.Marshal(manifest)
}

func (c registryClient) putReferrersTag(tag string, descriptors []Descriptor) error {
	existing, _, err := c.getManifestAccept(tag, MediaTypeOCIIndex)
	if err == nil {
		var index Index
		if decodeErr := json.Unmarshal(existing, &index); decodeErr == nil {
			descriptors = mergeDescriptors(index.Manifests, descriptors)
		}
	} else if !isRegistryNotFound(err) {
		return err
	}
	index := Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCIIndex,
		Manifests:     descriptors,
	}
	data, err := json.Marshal(index)
	if err != nil {
		return err
	}
	return c.putManifestMediaType(tag, data, MediaTypeOCIIndex)
}

func mergeDescriptors(existing, next []Descriptor) []Descriptor {
	out := append([]Descriptor{}, existing...)
	for _, desc := range next {
		if !hasDescriptor(out, desc.Digest) {
			out = append(out, desc)
		}
	}
	return out
}

func hasDescriptor(descs []Descriptor, digest string) bool {
	for _, desc := range descs {
		if desc.Digest == digest {
			return true
		}
	}
	return false
}

func pullSnapshotReferrers(s store, client registryClient, subjectDigest string) error {
	pulled := map[string]bool{}
	referrers, err := client.getReferrers(subjectDigest)
	if err == nil {
		for _, desc := range referrers {
			if desc.ArtifactType != MediaTypeSignature && desc.ArtifactType != MediaTypeProvenance && desc.ArtifactType != MediaTypeSigstoreBundle && desc.ArtifactType != MediaTypeLazyEncryptedIndex {
				continue
			}
			if err := pullReferrerManifest(s, client, desc.Digest, subjectDigest); err != nil {
				return err
			}
			pulled[desc.ArtifactType] = true
		}
	}
	for _, artifact := range []struct {
		refName   string
		mediaType string
	}{
		{refName: signatureRefName(subjectDigest), mediaType: MediaTypeSignature},
		{refName: provenanceRefName(subjectDigest), mediaType: MediaTypeProvenance},
	} {
		if pulled[artifact.mediaType] {
			continue
		}
		if err := pullReferrerManifest(s, client, artifact.refName, subjectDigest); err != nil && !isRegistryNotFound(err) {
			return err
		}
	}
	if err := pullCosignSignature(s, client, subjectDigest); err != nil && !isRegistryNotFound(err) {
		return err
	}
	if err := pullSigstoreReferrersTag(s, client, subjectDigest); err != nil && !isRegistryNotFound(err) {
		return err
	}
	if err := pullEncryptedLazyIndexTag(s, client, subjectDigest); err != nil && !isRegistryNotFound(err) {
		return err
	}
	return nil
}

func pullReferrerManifest(s store, client registryClient, ref, subjectDigest string) error {
	manifestData, _, err := client.getManifest(ref)
	if err != nil {
		return err
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("parse referrer manifest %s: %w", ref, err)
	}
	if manifest.Subject == nil || manifest.Subject.Digest != subjectDigest {
		return fmt.Errorf("referrer %s subject mismatch", ref)
	}
	if manifest.ArtifactType != MediaTypeSignature && manifest.ArtifactType != MediaTypeProvenance && manifest.ArtifactType != MediaTypeSigstoreBundle && manifest.ArtifactType != MediaTypeLazyEncryptedIndex {
		return nil
	}
	if len(manifest.Layers) != 1 {
		return fmt.Errorf("referrer %s has %d blobs, want 1", ref, len(manifest.Layers))
	}
	layer := manifest.Layers[0]
	data, err := client.getBlob(layer.Digest)
	if err != nil {
		return err
	}
	if _, err := s.writeBlob(data); err != nil {
		return err
	}
	switch manifest.ArtifactType {
	case MediaTypeSignature:
		return s.writeRef(signatureRefName(subjectDigest), layer.Digest)
	case MediaTypeProvenance:
		return s.writeRef(provenanceRefName(subjectDigest), layer.Digest)
	case MediaTypeSigstoreBundle:
		refName := sigstoreSignatureBundleRefName(subjectDigest)
		if manifest.Annotations["dev.sigstore.bundle.content"] == "dsse-envelope" {
			refName = sigstoreAttestationBundleRefName(subjectDigest)
		}
		return s.writeRef(refName, layer.Digest)
	case MediaTypeLazyEncryptedIndex:
		if err := s.writeRemoteBlobSource(remoteBlobSource{Registry: client.registry, Repo: client.repo, Digest: layer.Digest}); err != nil {
			return err
		}
		return s.writeRef(encryptedLazyIndexRefName(subjectDigest), layer.Digest)
	default:
		return nil
	}
}

func pullCosignSignature(s store, client registryClient, subjectDigest string) error {
	manifestData, _, err := client.getManifest(cosignSignatureTag(subjectDigest))
	if err != nil {
		return err
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("parse cosign signature manifest for %s: %w", subjectDigest, err)
	}
	if len(manifest.Layers) == 0 {
		return fmt.Errorf("cosign signature manifest for %s has no layers", subjectDigest)
	}
	layer := manifest.Layers[0]
	if layer.MediaType != MediaTypeCosignSimpleSigning {
		return fmt.Errorf("cosign signature layer for %s has media type %s", subjectDigest, layer.MediaType)
	}
	signature := layer.Annotations["dev.cosignproject.cosign/signature"]
	if signature == "" {
		return fmt.Errorf("cosign signature manifest for %s missing signature annotation", subjectDigest)
	}
	payloadData, err := client.getBlob(layer.Digest)
	if err != nil {
		return err
	}
	if _, err := s.writeBlob(payloadData); err != nil {
		return err
	}
	if err := s.writeRef(cosignPayloadRefName(subjectDigest), layer.Digest); err != nil {
		return err
	}
	meta := cosignSignatureMetadata{
		OSIxVersion:   Version,
		PayloadDigest: layer.Digest,
		Algorithm:     "ecdsa-p256-sha256",
		Signature:     signature,
		Signer:        "cosign",
		CreatedAt:     time.Now().UTC().Truncate(time.Second),
	}
	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	metaDesc, err := s.writeBlob(metaData)
	if err != nil {
		return err
	}
	return s.writeRef(cosignSignatureRefName(subjectDigest), metaDesc.Digest)
}

func pullSigstoreReferrersTag(s store, client registryClient, subjectDigest string) error {
	indexData, _, err := client.getManifestAccept(sigstoreReferrersTag(subjectDigest), MediaTypeOCIIndex)
	if err != nil {
		return err
	}
	var index Index
	if err := json.Unmarshal(indexData, &index); err != nil {
		return fmt.Errorf("parse Sigstore referrers index for %s: %w", subjectDigest, err)
	}
	for _, desc := range index.Manifests {
		if desc.ArtifactType != MediaTypeSigstoreBundle {
			continue
		}
		if err := pullReferrerManifest(s, client, desc.Digest, subjectDigest); err != nil {
			return err
		}
	}
	return nil
}

func pullEncryptedLazyIndexTag(s store, client registryClient, subjectDigest string) error {
	return pullReferrerManifest(s, client, encryptedLazyIndexTag(subjectDigest), subjectDigest)
}

type registryRepo struct {
	Registry string
	Repo     string
}

type registryStatusError struct {
	op     string
	ref    string
	status string
	code   int
}

func (e registryStatusError) Error() string {
	return fmt.Sprintf("%s %s: %s", e.op, e.ref, e.status)
}

func isRegistryNotFound(err error) bool {
	statusErr, ok := err.(registryStatusError)
	return ok && statusErr.code == http.StatusNotFound
}

func parseRegistryRepo(remoteRepo string) (registryRepo, error) {
	remoteRepo = strings.TrimSpace(remoteRepo)
	if strings.Contains(remoteRepo, "://") {
		return registryRepo{}, fmt.Errorf("registry repo should not include scheme: %s", remoteRepo)
	}
	parts := strings.SplitN(remoteRepo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return registryRepo{}, fmt.Errorf("registry repo must be REGISTRY/REPO, got %q", remoteRepo)
	}
	return registryRepo{Registry: parts[0], Repo: parts[1]}, nil
}

func registryBaseURL(registry string) string {
	if isLocalRegistry(registry) {
		return "http://" + registry
	}
	return "https://" + registry
}

func isLocalRegistry(registry string) bool {
	host := registry
	if strings.HasPrefix(host, "[") {
		if end := strings.Index(host, "]"); end >= 0 {
			host = strings.Trim(host[:end+1], "[]")
		}
	} else if before, _, ok := strings.Cut(host, ":"); ok {
		host = before
	}
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return strings.HasSuffix(strings.ToLower(host), ".localhost")
	}
}

type registryClient struct {
	registry     string
	base         string
	repo         string
	http         *http.Client
	auth         registryCredentials
	bearerTokens map[string]string
}

type registryCredentials struct {
	Username string
	Password string
	Token    string
}

func newRegistryClient(registry, repo string) registryClient {
	return registryClient{
		registry:     registry,
		base:         registryBaseURL(registry),
		repo:         repo,
		http:         http.DefaultClient,
		auth:         loadRegistryCredentials(registry),
		bearerTokens: map[string]string{},
	}
}

func (c registryClient) putBlob(digest string, data []byte) error {
	if err := c.ensureBlobAbsentOrPresent(digest); err == nil {
		return nil
	}
	startURL := c.url("/v2/" + c.repo + "/blobs/uploads/")
	req, err := http.NewRequest(http.MethodPost, startURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("start blob upload %s: %s", digest, resp.Status)
	}
	location := resp.Header.Get("Location")
	if location == "" {
		return fmt.Errorf("start blob upload %s: missing Location header", digest)
	}
	uploadURL, err := c.resolveLocation(location)
	if err != nil {
		return err
	}
	u, err := url.Parse(uploadURL)
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("digest", digest)
	u.RawQuery = q.Encode()
	req, err = http.NewRequest(http.MethodPut, u.String(), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err = c.do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("complete blob upload %s: %s", digest, resp.Status)
	}
	return nil
}

func (c registryClient) ensureBlobAbsentOrPresent(digest string) error {
	req, err := http.NewRequest(http.MethodHead, c.url("/v2/"+c.repo+"/blobs/"+digest), nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("blob %s not present: %s", digest, resp.Status)
}

func (c registryClient) getBlob(digest string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, c.url("/v2/"+c.repo+"/blobs/"+digest), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, registryStatusError{op: "get blob", ref: digest, status: resp.Status, code: resp.StatusCode}
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if got := digestBytes(data); got != digest {
		return nil, fmt.Errorf("remote blob %s digest mismatch: got %s", digest, got)
	}
	return data, nil
}

func (c registryClient) putManifest(ref string, data []byte) error {
	return c.putManifestMediaType(ref, data, MediaTypeOCIManifest)
}

func (c registryClient) putManifestMediaType(ref string, data []byte, mediaType string) error {
	req, err := http.NewRequest(http.MethodPut, c.url("/v2/"+c.repo+"/manifests/"+ref), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mediaType)
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("put manifest %s: %s", ref, resp.Status)
	}
	return nil
}

func (c registryClient) deleteManifest(digest string) error {
	req, err := http.NewRequest(http.MethodDelete, c.url("/v2/"+c.repo+"/manifests/"+digest), nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return registryStatusError{op: "delete manifest", ref: digest, status: resp.Status, code: resp.StatusCode}
	}
	return fmt.Errorf("delete manifest %s: %s", digest, resp.Status)
}

func (c registryClient) getManifest(ref string) ([]byte, string, error) {
	return c.getManifestAccept(ref, MediaTypeOCIManifest)
}

func (c registryClient) getManifestAccept(ref string, accept string) ([]byte, string, error) {
	req, err := http.NewRequest(http.MethodGet, c.url("/v2/"+c.repo+"/manifests/"+ref), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", accept)
	resp, err := c.do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, "", registryStatusError{op: "get manifest", ref: ref, status: resp.Status, code: resp.StatusCode}
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		digest = digestBytes(data)
	}
	if got := digestBytes(data); got != digest {
		return nil, "", fmt.Errorf("remote manifest %s digest mismatch: got %s", digest, got)
	}
	return data, digest, nil
}

func (c registryClient) getReferrers(subjectDigest string) ([]Descriptor, error) {
	req, err := http.NewRequest(http.MethodGet, c.url("/v2/"+c.repo+"/referrers/"+subjectDigest), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", MediaTypeOCIIndex)
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, registryStatusError{op: "get referrers", ref: subjectDigest, status: resp.Status, code: resp.StatusCode}
	}
	var index Index
	if err := json.NewDecoder(resp.Body).Decode(&index); err != nil {
		return nil, fmt.Errorf("decode referrers for %s: %w", subjectDigest, err)
	}
	return index.Manifests, nil
}

func (c registryClient) url(p string) string {
	u, _ := url.Parse(c.base)
	u.Path = path.Clean(p)
	if strings.HasSuffix(p, "/") && !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	return u.String()
}

func (c registryClient) resolveLocation(location string) (string, error) {
	u, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	if u.IsAbs() {
		return u.String(), nil
	}
	base, err := url.Parse(c.base)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(u).String(), nil
}

func (c registryClient) do(req *http.Request) (*http.Response, error) {
	resp, err := c.doWithAuth(req, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	challenge := resp.Header.Get("WWW-Authenticate")
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	token, err := c.bearerToken(challenge)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return resp, nil
	}
	return c.doWithAuth(req, token)
}

func (c registryClient) doWithAuth(req *http.Request, bearerToken string) (*http.Response, error) {
	next := req.Clone(req.Context())
	if req.GetBody != nil && req.Body != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		next.Body = body
	}
	if bearerToken != "" {
		next.Header.Set("Authorization", "Bearer "+bearerToken)
	} else if c.auth.Token != "" {
		next.Header.Set("Authorization", "Bearer "+c.auth.Token)
	} else if c.auth.Username != "" || c.auth.Password != "" {
		next.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	return c.http.Do(next)
}

func (c registryClient) bearerToken(challenge string) (string, error) {
	scheme, params := parseAuthChallenge(challenge)
	if !strings.EqualFold(scheme, "Bearer") {
		return "", nil
	}
	if token := c.bearerTokens[challenge]; token != "" {
		return token, nil
	}
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("registry requested bearer auth without token realm")
	}
	u, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("parse bearer token realm: %w", err)
	}
	q := u.Query()
	if service := params["service"]; service != "" {
		q.Set("service", service)
	}
	if scope := params["scope"]; scope != "" {
		q.Set("scope", scope)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	if c.auth.Username != "" || c.auth.Password != "" {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("fetch bearer token for %s: %s", c.registry, resp.Status)
	}
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode bearer token response: %w", err)
	}
	token := payload.Token
	if token == "" {
		token = payload.AccessToken
	}
	if token == "" {
		return "", fmt.Errorf("bearer token response for %s did not include token", c.registry)
	}
	c.bearerTokens[challenge] = token
	return token, nil
}

func parseAuthChallenge(header string) (string, map[string]string) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", nil
	}
	scheme, rest, ok := strings.Cut(header, " ")
	if !ok {
		return header, map[string]string{}
	}
	params := map[string]string{}
	for _, part := range splitChallengeParams(rest) {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"`)
		params[key] = value
	}
	return scheme, params
}

func splitChallengeParams(input string) []string {
	var parts []string
	var current strings.Builder
	quoted := false
	escaped := false
	for _, r := range input {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\' && quoted:
			escaped = true
		case r == '"':
			quoted = !quoted
			current.WriteRune(r)
		case r == ',' && !quoted:
			if part := strings.TrimSpace(current.String()); part != "" {
				parts = append(parts, part)
			}
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if part := strings.TrimSpace(current.String()); part != "" {
		parts = append(parts, part)
	}
	return parts
}

func loadRegistryCredentials(registry string) registryCredentials {
	if token := strings.TrimSpace(os.Getenv("OSIX_REGISTRY_TOKEN")); token != "" {
		return registryCredentials{Token: token}
	}
	user := os.Getenv("OSIX_REGISTRY_USERNAME")
	password := os.Getenv("OSIX_REGISTRY_PASSWORD")
	if user != "" || password != "" {
		return registryCredentials{Username: user, Password: password}
	}
	return loadDockerConfigCredentials(registry)
}

func loadDockerConfigCredentials(registry string) registryCredentials {
	path := dockerConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return registryCredentials{}
	}
	var cfg struct {
		Auths map[string]struct {
			Auth          string `json:"auth"`
			Username      string `json:"username"`
			Password      string `json:"password"`
			IdentityToken string `json:"identitytoken"`
		} `json:"auths"`
		CredHelpers map[string]string `json:"credHelpers"`
		CredsStore  string            `json:"credsStore"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return registryCredentials{}
	}
	for _, key := range dockerConfigRegistryKeys(registry) {
		entry, ok := cfg.Auths[key]
		if !ok {
			continue
		}
		if entry.IdentityToken != "" {
			return registryCredentials{Token: entry.IdentityToken}
		}
		if entry.Username != "" || entry.Password != "" {
			return registryCredentials{Username: entry.Username, Password: entry.Password}
		}
		if entry.Auth != "" {
			decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
			if err != nil {
				continue
			}
			username, password, ok := strings.Cut(string(decoded), ":")
			if !ok {
				continue
			}
			return registryCredentials{Username: username, Password: password}
		}
	}
	for _, key := range dockerConfigRegistryKeys(registry) {
		if helper := strings.TrimSpace(cfg.CredHelpers[key]); helper != "" {
			return loadDockerCredentialHelperCredentials(helper, key)
		}
	}
	if helper := strings.TrimSpace(cfg.CredsStore); helper != "" {
		for _, key := range dockerConfigRegistryKeys(registry) {
			if creds := loadDockerCredentialHelperCredentials(helper, key); creds != (registryCredentials{}) {
				return creds
			}
		}
	}
	return registryCredentials{}
}

func loadDockerCredentialHelperCredentials(helper, serverURL string) registryCredentials {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker-credential-"+helper, "get")
	cmd.Stdin = strings.NewReader(serverURL + "\n")
	out, err := cmd.Output()
	if err != nil {
		return registryCredentials{}
	}
	var payload struct {
		Username string `json:"Username"`
		Secret   string `json:"Secret"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return registryCredentials{}
	}
	if payload.Username == "" && payload.Secret == "" {
		return registryCredentials{}
	}
	if payload.Username == "<token>" {
		return registryCredentials{Token: payload.Secret}
	}
	return registryCredentials{Username: payload.Username, Password: payload.Secret}
}

func dockerConfigPath() string {
	if dir := strings.TrimSpace(os.Getenv("DOCKER_CONFIG")); dir != "" {
		return filepath.Join(dir, "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".docker", "config.json")
}

func dockerConfigRegistryKeys(registry string) []string {
	return []string{
		registry,
		"https://" + registry,
		"http://" + registry,
	}
}
