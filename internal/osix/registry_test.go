package osix

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestPushPullSnapshotThroughOCIRegistry(t *testing.T) {
	reg := newFakeRegistry()
	server := httptest.NewServer(reg)
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo := u.Host + "/acme/agent-state"

	source := t.TempDir()
	if _, err := Init(source, InitOptions{
		Base:          "example/base:latest",
		Name:          "agent",
		StateRef:      repo,
		Mount:         filepath.Join(source, "agentfs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(source, "agentfs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "notes.md"), "v1\n")
	first, err := Snapshot(source, fs, SnapshotOptions{Tag: "snap-000001", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := PushSnapshot(source, repo, first.ManifestDigest, first.Tags); err != nil {
		t.Fatal(err)
	}

	mustWrite(t, filepath.Join(fs, "agent", "workspace", "notes.md"), "v2\n")
	second, err := Snapshot(source, fs, SnapshotOptions{Tag: "snap-000002", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := PushSnapshot(source, repo, second.ManifestDigest, second.Tags); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	if _, err := Init(dest, InitOptions{
		Base:          "example/base:latest",
		Name:          "agent",
		StateRef:      repo,
		Mount:         filepath.Join(dest, "agentfs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	pulled, err := PullSnapshot(dest, repo+":snap-000002", "main")
	if err != nil {
		t.Fatal(err)
	}
	if pulled != second.ManifestDigest {
		t.Fatalf("pulled digest mismatch: want %s got %s", second.ManifestDigest, pulled)
	}
	restoreDir := filepath.Join(dest, "restore")
	if err := Restore(dest, "main", restoreDir, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(restoreDir, "agent", "workspace", "notes.md"), "v2\n")
	refs, err := Refs(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRef(refs, "snap-000001") || !hasRef(refs, "snap-000002") || !hasRef(refs, "main") {
		t.Fatalf("expected pulled chain refs, got %#v", refs)
	}
}

func hasRef(refs []Ref, name string) bool {
	for _, ref := range refs {
		if ref.Name == name {
			return true
		}
	}
	return false
}

type fakeRegistry struct {
	blobs     map[string][]byte
	manifests map[string][]byte
	uploads   map[string]string
	nextID    int
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{
		blobs:     map[string][]byte{},
		manifests: map[string][]byte{},
		uploads:   map[string]string{},
	}
}

func (r *fakeRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
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
			digest := strings.Join(parts[i+1:], "/")
			r.handleBlob(w, req, repo, digest)
			return
		}
		if part == "manifests" && i+1 < len(parts) {
			repo := strings.Join(parts[:i], "/")
			ref := strings.Join(parts[i+1:], "/")
			r.handleManifest(w, req, repo, ref)
			return
		}
	}
	http.NotFound(w, req)
}

func (r *fakeRegistry) handleUploads(w http.ResponseWriter, req *http.Request, repo string, rest []string) {
	switch req.Method {
	case http.MethodPost:
		r.nextID++
		id := fmt.Sprintf("upload-%d", r.nextID)
		r.uploads[id] = repo
		w.Header().Set("Location", "/v2/"+repo+"/blobs/uploads/"+id)
		w.WriteHeader(http.StatusAccepted)
	case http.MethodPut:
		if len(rest) != 1 {
			http.NotFound(w, req)
			return
		}
		id := rest[0]
		if r.uploads[id] != repo {
			http.NotFound(w, req)
			return
		}
		digest := req.URL.Query().Get("digest")
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if got := digestBytes(body); got != digest {
			http.Error(w, "digest mismatch", http.StatusBadRequest)
			return
		}
		r.blobs[digest] = body
		delete(r.uploads, id)
		w.WriteHeader(http.StatusCreated)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (r *fakeRegistry) handleBlob(w http.ResponseWriter, req *http.Request, repo, digest string) {
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

func (r *fakeRegistry) handleManifest(w http.ResponseWriter, req *http.Request, repo, ref string) {
	key := repo + ":" + ref
	switch req.Method {
	case http.MethodPut:
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var manifest Manifest
		if err := json.Unmarshal(body, &manifest); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for _, desc := range append([]Descriptor{manifest.Config}, manifest.Layers...) {
			if _, ok := r.blobs[desc.Digest]; !ok {
				http.Error(w, "missing blob "+desc.Digest, http.StatusBadRequest)
				return
			}
		}
		digest := digestBytes(body)
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
		w.Header().Set("Content-Type", MediaTypeOCIManifest)
		w.Header().Set("Docker-Content-Digest", digestBytes(data))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func TestParseRegistryReference(t *testing.T) {
	cases := map[string]RegistryReference{
		"localhost:5000/acme/state:snap": {
			Registry:  "localhost:5000",
			Repo:      "acme/state",
			Reference: "snap",
		},
		"ghcr.io/acme/state@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": {
			Registry:  "ghcr.io",
			Repo:      "acme/state",
			Reference: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	}
	for input, want := range cases {
		got, err := ParseRegistryReference(input)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s: want %#v got %#v", input, want, got)
		}
	}
}
