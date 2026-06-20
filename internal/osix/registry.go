package osix

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
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
	client := registryClient{base: registryBaseURL(remote.Registry), repo: remote.Repo, http: http.DefaultClient}
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
	client := registryClient{base: registryBaseURL(ref.Registry), repo: ref.Repo, http: http.DefaultClient}
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
	return digest, nil
}

type registryRepo struct {
	Registry string
	Repo     string
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
	base string
	repo string
	http *http.Client
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
	resp, err := c.http.Do(req)
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
	resp, err = c.http.Do(req)
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
	resp, err := c.http.Do(req)
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
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("get blob %s: %s", digest, resp.Status)
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
	resp, err := c.http.Do(req)
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
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, "", fmt.Errorf("get manifest %s: %s", ref, resp.Status)
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
