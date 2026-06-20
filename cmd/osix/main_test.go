package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smol-platform/smol-agent-oci-fs/internal/osix"
)

func TestPushPullCommandsThroughOCIRegistry(t *testing.T) {
	reg := newCLIFakeRegistry()
	server := httptest.NewServer(reg)
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo := u.Host + "/acme/agent-state"

	source := t.TempDir()
	if _, err := osix.Init(source, osix.InitOptions{
		Base:          "example/base:latest",
		Name:          "agent",
		StateRef:      repo,
		Mount:         filepath.Join(source, "agentfs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	mustWriteCLI(t, filepath.Join(source, "agentfs", "agent", "workspace", "notes.md"), "v1\n")
	result, err := osix.Snapshot(source, filepath.Join(source, "agentfs"), osix.SnapshotOptions{Tag: "snap-000001", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}

	withWorkingDir(t, source, func() {
		if err := run([]string{"push", "main", "--tag", "release"}); err != nil {
			t.Fatal(err)
		}
	})

	dest := t.TempDir()
	if _, err := osix.Init(dest, osix.InitOptions{
		Base:          "example/base:latest",
		Name:          "agent",
		StateRef:      repo,
		Mount:         filepath.Join(dest, "agentfs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	withWorkingDir(t, dest, func() {
		if err := run([]string{"pull", repo + ":release", "--tag", "pulled"}); err != nil {
			t.Fatal(err)
		}
		if err := run([]string{"restore", "pulled", filepath.Join(dest, "restore")}); err != nil {
			t.Fatal(err)
		}
	})
	assertFileCLI(t, filepath.Join(dest, "restore", "agent", "workspace", "notes.md"), "v1\n")
	refs, err := osix.Refs(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCLIRef(refs, "pulled", result.ManifestDigest) {
		t.Fatalf("expected pulled ref for %s, got %#v", result.ManifestDigest, refs)
	}
}

func withWorkingDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatal(err)
		}
	}()
	fn()
}

func hasCLIRef(refs []osix.Ref, name, digest string) bool {
	for _, ref := range refs {
		if ref.Name == name && ref.Digest == digest {
			return true
		}
	}
	return false
}

func mustWriteCLI(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFileCLI(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s: want %q got %q", path, want, string(data))
	}
}

type cliFakeRegistry struct {
	blobs     map[string][]byte
	manifests map[string][]byte
	uploads   map[string]string
	nextID    int
}

func newCLIFakeRegistry() *cliFakeRegistry {
	return &cliFakeRegistry{
		blobs:     map[string][]byte{},
		manifests: map[string][]byte{},
		uploads:   map[string]string{},
	}
}

func (r *cliFakeRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/v2/" {
		w.WriteHeader(http.StatusOK)
		return
	}
	parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/v2/"), "/")
	for i, part := range parts {
		if part == "blobs" && i+1 < len(parts) {
			repo := strings.Join(parts[:i], "/")
			if parts[i+1] == "uploads" {
				r.handleUploads(w, req, repo, parts[i+2:])
				return
			}
			r.handleBlob(w, req, strings.Join(parts[i+1:], "/"))
			return
		}
		if part == "manifests" && i+1 < len(parts) {
			repo := strings.Join(parts[:i], "/")
			r.handleManifest(w, req, repo, strings.Join(parts[i+1:], "/"))
			return
		}
	}
	http.NotFound(w, req)
}

func (r *cliFakeRegistry) handleUploads(w http.ResponseWriter, req *http.Request, repo string, rest []string) {
	switch req.Method {
	case http.MethodPost:
		r.nextID++
		id := fmt.Sprintf("upload-%d", r.nextID)
		r.uploads[id] = repo
		w.Header().Set("Location", "/v2/"+repo+"/blobs/uploads/"+id)
		w.WriteHeader(http.StatusAccepted)
	case http.MethodPut:
		if len(rest) != 1 || r.uploads[rest[0]] != repo {
			http.NotFound(w, req)
			return
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		digest := req.URL.Query().Get("digest")
		if got := osixDigestBytes(body); got != digest {
			http.Error(w, "digest mismatch", http.StatusBadRequest)
			return
		}
		r.blobs[digest] = body
		delete(r.uploads, rest[0])
		w.WriteHeader(http.StatusCreated)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (r *cliFakeRegistry) handleBlob(w http.ResponseWriter, req *http.Request, digest string) {
	data, ok := r.blobs[digest]
	if !ok {
		http.NotFound(w, req)
		return
	}
	switch req.Method {
	case http.MethodHead:
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (r *cliFakeRegistry) handleManifest(w http.ResponseWriter, req *http.Request, repo, ref string) {
	key := repo + ":" + ref
	switch req.Method {
	case http.MethodPut:
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var manifest osix.Manifest
		if err := json.Unmarshal(body, &manifest); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for _, desc := range append([]osix.Descriptor{manifest.Config}, manifest.Layers...) {
			if _, ok := r.blobs[desc.Digest]; !ok {
				http.Error(w, "missing blob "+desc.Digest, http.StatusBadRequest)
				return
			}
		}
		digest := osixDigestBytes(body)
		r.manifests[key] = body
		r.manifests[repo+"@"+digest] = body
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusCreated)
	case http.MethodGet:
		data, ok := r.manifests[key]
		if !ok && strings.HasPrefix(ref, "sha256:") {
			data, ok = r.manifests[repo+"@"+ref]
		}
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Docker-Content-Digest", osixDigestBytes(data))
		w.Header().Set("Content-Type", osix.MediaTypeOCIManifest)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func osixDigestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
