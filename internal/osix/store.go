package osix

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const osixDirName = ".osix"

func Init(root string, opts InitOptions) (WorkspaceConfig, error) {
	if opts.DefaultBranch == "" {
		opts.DefaultBranch = "main"
	}
	if opts.Mount == "" {
		opts.Mount = "./agentfs"
	}
	baseDigest := digestBytes([]byte(opts.Base))
	cfg := WorkspaceConfig{
		OSIxVersion:   Version,
		Name:          opts.Name,
		Base:          opts.Base,
		BaseDigest:    baseDigest,
		StateRef:      opts.StateRef,
		Mount:         opts.Mount,
		DefaultBranch: opts.DefaultBranch,
		Encrypt:       opts.Encrypt,
	}
	store, err := openStore(root)
	if err != nil {
		return cfg, err
	}
	for _, dir := range []string{
		store.root,
		store.blobRoot(),
		store.refsRoot(),
		filepath.Join(store.root, "cache"),
		filepath.Join(store.root, "upper"),
		filepath.Join(store.root, "work"),
		filepath.Join(store.root, "manifests"),
		filepath.Join(store.root, "keys"),
		filepath.Join(store.root, "mounts"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return cfg, err
		}
	}
	if err := os.MkdirAll(opts.Mount, 0o755); err != nil {
		return cfg, err
	}
	return cfg, writeWorkspaceConfig(store.configPath(), cfg)
}

func Workspace(workspaceRoot string) (WorkspaceConfig, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return WorkspaceConfig{}, err
	}
	return readWorkspaceConfig(s.configPath())
}

type store struct {
	root string
}

func openStore(root string) (store, error) {
	if root == "" {
		root = "."
	}
	return store{root: filepath.Join(root, osixDirName)}, nil
}

func findStore(start string) (store, error) {
	if start == "" {
		start = "."
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return store{}, err
	}
	for {
		candidate := filepath.Join(abs, osixDirName)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return store{root: candidate}, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			break
		}
		abs = parent
	}
	return store{}, fmt.Errorf("no .osix workspace found from %s; run osix init first", start)
}

func (s store) configPath() string {
	return filepath.Join(s.root, "config.toml")
}

func (s store) blobRoot() string {
	return filepath.Join(s.root, "blobs", "sha256")
}

func (s store) refsRoot() string {
	return filepath.Join(s.root, "refs")
}

func (s store) remoteRoot() string {
	return filepath.Join(s.root, "remotes", "sha256")
}

func (s store) lazyRoot() string {
	return filepath.Join(s.root, "lazy", "sha256")
}

func (s store) mountsRoot() string {
	return filepath.Join(s.root, "mounts")
}

func (s store) writeBlob(data []byte) (Descriptor, error) {
	digest := digestBytes(data)
	hexDigest := strings.TrimPrefix(digest, "sha256:")
	path := filepath.Join(s.blobRoot(), hexDigest)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Descriptor{}, err
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return Descriptor{}, err
		}
	}
	return Descriptor{Digest: digest, Size: int64(len(data))}, nil
}

func writePrivateFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func (s store) readBlob(digest string) ([]byte, error) {
	hexDigest, err := digestHex(digest)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(s.blobRoot(), hexDigest))
	if err != nil {
		return nil, fmt.Errorf("read blob %s: %w", digest, err)
	}
	if got := digestBytes(data); got != digest {
		return nil, fmt.Errorf("blob %s digest mismatch: got %s", digest, got)
	}
	return data, nil
}

func (s store) hasBlob(digest string) bool {
	hexDigest, err := digestHex(digest)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(s.blobRoot(), hexDigest))
	return err == nil
}

type remoteBlobSource struct {
	Scheme   string `json:"scheme,omitempty"`
	Registry string `json:"registry"`
	Repo     string `json:"repo"`
	Digest   string `json:"digest"`
}

func (s store) writeRemoteBlobSource(source remoteBlobSource) error {
	hexDigest, err := digestHex(source.Digest)
	if err != nil {
		return err
	}
	path := filepath.Join(s.remoteRoot(), hexDigest+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(source, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(path, data)
}

func (s store) readRemoteBlobSource(digest string) (remoteBlobSource, error) {
	hexDigest, err := digestHex(digest)
	if err != nil {
		return remoteBlobSource{}, err
	}
	data, err := os.ReadFile(filepath.Join(s.remoteRoot(), hexDigest+".json"))
	if err != nil {
		return remoteBlobSource{}, err
	}
	var source remoteBlobSource
	if err := json.Unmarshal(data, &source); err != nil {
		return remoteBlobSource{}, fmt.Errorf("parse remote blob source %s: %w", digest, err)
	}
	return source, nil
}

func (s store) writeRef(name, digest string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	if _, err := digestHex(digest); err != nil {
		return err
	}
	path := filepath.Join(s.refsRoot(), safeRefName(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(digest+"\n"), 0o644)
}

func (s store) writeRefIfExpected(name, digest, expected string) error {
	if strings.TrimSpace(expected) == "" {
		return s.writeRef(name, digest)
	}
	current, err := s.resolveRef(name)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) && !strings.Contains(err.Error(), "no such file") {
			return err
		}
	} else if current != expected {
		return fmt.Errorf("branch conflict for %s: expected %s but current is %s", name, expected, current)
	}
	return s.writeRef(name, digest)
}

func (s store) resolveRef(ref string) (string, error) {
	if strings.HasPrefix(ref, "sha256:") {
		if _, err := digestHex(ref); err != nil {
			return "", err
		}
		return ref, nil
	}
	data, err := os.ReadFile(filepath.Join(s.refsRoot(), safeRefName(ref)))
	if err != nil {
		return "", fmt.Errorf("resolve ref %q: %w", ref, err)
	}
	digest := strings.TrimSpace(string(data))
	if _, err := digestHex(digest); err != nil {
		return "", fmt.Errorf("ref %q contains invalid digest: %w", ref, err)
	}
	return digest, nil
}

func (s store) listRefs() ([]Ref, error) {
	var refs []Ref
	if _, err := os.Stat(s.refsRoot()); errors.Is(err, fs.ErrNotExist) {
		return refs, nil
	}
	err := filepath.WalkDir(s.refsRoot(), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(s.refsRoot(), path)
		if err != nil {
			return err
		}
		refs = append(refs, Ref{Name: unsafeRefName(rel), Digest: strings.TrimSpace(string(data))})
		return nil
	})
	sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
	return refs, err
}

func (s store) loadManifest(ref string) (string, Manifest, AgentConfig, error) {
	digest, err := s.resolveRef(ref)
	if err != nil {
		return "", Manifest{}, AgentConfig{}, err
	}
	manifestData, err := s.readBlob(digest)
	if err != nil {
		return "", Manifest{}, AgentConfig{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return "", Manifest{}, AgentConfig{}, fmt.Errorf("parse manifest %s: %w", digest, err)
	}
	cfgData, err := s.readBlob(manifest.Config.Digest)
	if err != nil {
		return "", Manifest{}, AgentConfig{}, err
	}
	var cfg AgentConfig
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		return "", Manifest{}, AgentConfig{}, fmt.Errorf("parse config %s: %w", manifest.Config.Digest, err)
	}
	return digest, manifest, cfg, nil
}

type snapshotChainItem struct {
	Digest   string
	Manifest Manifest
	Config   AgentConfig
}

func (s store) snapshotChainWithDigests(digest string) ([]snapshotChainItem, error) {
	var chain []snapshotChainItem
	seen := map[string]bool{}
	for digest != "" {
		if seen[digest] {
			return nil, fmt.Errorf("snapshot parent cycle at %s", digest)
		}
		seen[digest] = true
		resolved, manifest, cfg, err := s.loadManifest(digest)
		if err != nil {
			return nil, err
		}
		chain = append(chain, snapshotChainItem{Digest: resolved, Manifest: manifest, Config: cfg})
		if cfg.Parent == nil {
			break
		}
		digest = cfg.Parent.Digest
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, nil
}

func readWorkspaceConfig(path string) (WorkspaceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return WorkspaceConfig{}, err
	}
	var cfg WorkspaceConfig
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return cfg, fmt.Errorf("invalid config line %q", line)
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"`)
		switch key {
		case "osix_version":
			cfg.OSIxVersion = val
		case "name":
			cfg.Name = val
		case "base":
			cfg.Base = val
		case "base_digest":
			cfg.BaseDigest = val
		case "state_ref":
			cfg.StateRef = val
		case "mount":
			cfg.Mount = val
		case "default_branch":
			cfg.DefaultBranch = val
		case "encrypt":
			cfg.Encrypt = val
		}
	}
	return cfg, nil
}

func writeWorkspaceConfig(path string, cfg WorkspaceConfig) error {
	text := fmt.Sprintf(`osix_version = %q
name = %q
base = %q
base_digest = %q
state_ref = %q
mount = %q
default_branch = %q
encrypt = %q
`, cfg.OSIxVersion, cfg.Name, cfg.Base, cfg.BaseDigest, cfg.StateRef, cfg.Mount, cfg.DefaultBranch, cfg.Encrypt)
	return os.WriteFile(path, []byte(text), 0o644)
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func digestHex(digest string) (string, error) {
	hexDigest := strings.TrimPrefix(digest, "sha256:")
	if len(hexDigest) != 64 {
		return "", fmt.Errorf("invalid sha256 digest %q", digest)
	}
	if _, err := hex.DecodeString(hexDigest); err != nil {
		return "", fmt.Errorf("invalid sha256 digest %q: %w", digest, err)
	}
	return hexDigest, nil
}

func safeRefName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, ":")
	name = strings.ReplaceAll(name, "/", "__")
	name = strings.ReplaceAll(name, ":", "__")
	return name
}

func unsafeRefName(name string) string {
	return strings.ReplaceAll(name, "__", "/")
}

func mountKey(target string) (string, error) {
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(digestBytes([]byte(abs)), "sha256:"), nil
}
