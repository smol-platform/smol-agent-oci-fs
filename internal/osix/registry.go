package osix

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type RegistryReference struct {
	Registry  string
	Repo      string
	Reference string
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

func PushSnapshot(workspaceRoot, remoteRepo, ref string, extraTags []string) error {
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

func PullSnapshot(workspaceRoot, remoteRef, localTag string) (string, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return "", err
	}
	ref, err := ParseRegistryReference(remoteRef)
	if err != nil {
		return "", err
	}
	client := newRegistryClient(ref.Registry, ref.Repo)
	digest, err := pullManifestRecursive(s, client, ref.Reference)
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

func pullManifestRecursive(s store, client registryClient, ref string) (string, error) {
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
		layerData, err := client.getBlob(layer.Digest)
		if err != nil {
			return "", err
		}
		if _, err := s.writeBlob(layerData); err != nil {
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
		if _, err := pullManifestRecursive(s, client, cfg.Parent.Digest); err != nil {
			return "", err
		}
	}
	if err := pullSnapshotReferrers(s, client, digest); err != nil {
		return "", err
	}
	return digest, nil
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
		manifestData, err := artifactReferrerManifest(subjectDesc, artifact.mediaType, artifactDigest, int64(len(artifactData)), emptyConfig)
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
	return nil
}

func artifactReferrerManifest(subject Descriptor, artifactType, blobDigest string, blobSize int64, emptyConfig []byte) ([]byte, error) {
	manifest := Manifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCIManifest,
		ArtifactType:  artifactType,
		Config: Descriptor{
			MediaType: MediaTypeEmptyConfig,
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

func pullSnapshotReferrers(s store, client registryClient, subjectDigest string) error {
	pulled := map[string]bool{}
	referrers, err := client.getReferrers(subjectDigest)
	if err == nil {
		for _, desc := range referrers {
			if desc.ArtifactType != MediaTypeSignature && desc.ArtifactType != MediaTypeProvenance {
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
	if manifest.ArtifactType != MediaTypeSignature && manifest.ArtifactType != MediaTypeProvenance {
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
	default:
		return nil
	}
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
	req, err := http.NewRequest(http.MethodPut, c.url("/v2/"+c.repo+"/manifests/"+ref), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", MediaTypeOCIManifest)
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

func (c registryClient) getManifest(ref string) ([]byte, string, error) {
	req, err := http.NewRequest(http.MethodGet, c.url("/v2/"+c.repo+"/manifests/"+ref), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", MediaTypeOCIManifest)
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
	return registryCredentials{}
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
