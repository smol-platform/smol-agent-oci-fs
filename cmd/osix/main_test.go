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

func TestRegistryProbeCommand(t *testing.T) {
	reg := newCLIFakeRegistry()
	server := httptest.NewServer(reg)
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo := u.Host + "/acme/probe-agent-state"
	var out string
	out, err = captureStdout(t, func() error {
		return run([]string{"registry", "probe", repo, "--tag", "probe-test", "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	var result osix.RegistryProbeResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("parse probe JSON: %v\n%s", err, out)
	}
	if result.Repository != repo || result.RegistryHost != u.Host || result.Tag != "probe-test" {
		t.Fatalf("unexpected probe result: %#v", result)
	}
	if result.Result != "passed" {
		t.Fatalf("probe result = %q, want passed", result.Result)
	}
	if _, ok := reg.manifests["acme/probe-agent-state:probe-test"]; !ok {
		t.Fatalf("expected probe manifest tag to be written")
	}
	if result.LayerDigest == "" || reg.blobGets[result.LayerDigest] != 1 {
		t.Fatalf("expected probe layer %s to be read once, gets=%#v", result.LayerDigest, reg.blobGets)
	}

	out, err = captureStdout(t, func() error {
		return run([]string{"registry", "probe", repo, "--tag", "probe-human"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "registry probe passed for "+repo+":probe-human") {
		t.Fatalf("unexpected human probe output: %q", out)
	}
}

func TestMountRemoteLazyPullRecordsRemoteSource(t *testing.T) {
	reg := newCLIFakeRegistry()
	server := httptest.NewServer(reg)
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo := u.Host + "/acme/lazy-mount-agent-state"

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
	mustWriteCLI(t, filepath.Join(source, "agentfs", "agent", "workspace", "notes.md"), "lazy mount\n")
	result, err := osix.Snapshot(source, filepath.Join(source, "agentfs"), osix.SnapshotOptions{Tag: "snap-lazy-mount", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}
	layerDigest := readCLISnapshotLayerDigest(t, source, result.ManifestDigest)
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
	mountDir := filepath.Join(dest, "mounted")
	withWorkingDir(t, dest, func() {
		if err := run([]string{"mount", repo + ":release", mountDir, "--mode", "materialized", "--force", "--lazy", "--quiet"}); err != nil {
			t.Fatal(err)
		}
	})
	assertFileCLI(t, filepath.Join(mountDir, "agent", "workspace", "notes.md"), "lazy mount\n")
	remoteSourcePath := filepath.Join(dest, ".osix", "remotes", "sha256", strings.TrimPrefix(layerDigest, "sha256:")+".json")
	if _, err := os.Stat(remoteSourcePath); err != nil {
		t.Fatalf("expected lazy remote source record for %s at %s: %v", layerDigest, remoteSourcePath, err)
	}
	if got := reg.blobGets[layerDigest]; got != 1 {
		t.Fatalf("remote lazy mount fetched layer %s %d times, want 1", layerDigest, got)
	}
}

func TestSideEffectCheckCommandBlocksUnsafeReplay(t *testing.T) {
	root := t.TempDir()
	mustWriteCLI(t, filepath.Join(root, ".osix-replay-policy.json"), `{"mode":"require-approval","createdAt":"2026-06-22T00:00:00Z"}`)
	mustWriteCLI(t, filepath.Join(root, "agent", "side-effects", "ledger.jsonl"),
		`{"turn":1,"tool":"github.create_issue","idempotencyKey":"create-1","requestDigest":"sha256:req","responseDigest":"sha256:resp","externalResource":"github:acme/repo/issues/1","replayPolicy":"mock-by-default"}`+"\n"+
			`{"turn":2,"tool":"gmail.send","idempotencyKey":"mail-1","requestDigest":"sha256:req2","responseDigest":"sha256:resp2","externalResource":"gmail:message/1","replayPolicy":"never-replay"}`+"\n")
	if err := run([]string{"side-effect", "check", root, "--tool", "github.create_issue", "--resource", "github:acme/repo/issues/1", "--operation", "write"}); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"side-effect", "check", root, "--tool", "gmail.send", "--resource", "gmail:message/1", "--operation", "write"})
	if err == nil || !strings.Contains(err.Error(), "side-effect action deny") {
		t.Fatalf("expected deny error, got %v", err)
	}
}

func TestReadCommandSupportsByteRange(t *testing.T) {
	root := t.TempDir()
	if _, err := osix.Init(root, osix.InitOptions{
		Base:          "base",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "agentfs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	mustWriteCLI(t, filepath.Join(root, "agentfs", "agent", "workspace", "range.txt"), "0123456789abcdef")
	if _, err := osix.Snapshot(root, filepath.Join(root, "agentfs"), osix.SnapshotOptions{Tag: "range"}); err != nil {
		t.Fatal(err)
	}
	var out string
	withWorkingDir(t, root, func() {
		var err error
		out, err = captureStdout(t, func() error {
			return run([]string{"read", "range", "agent/workspace/range.txt", "--offset", "4", "--length", "6"})
		})
		if err != nil {
			t.Fatal(err)
		}
		err = run([]string{"read", "range", "agent/workspace/range.txt", "--offset", "1"})
		if err == nil || !strings.Contains(err.Error(), "--length is required") {
			t.Fatalf("expected missing length error, got %v", err)
		}
	})
	if out != "456789" {
		t.Fatalf("range read output = %q, want %q", out, "456789")
	}
}

func TestExitCodeReportsRemoteBranchConflict(t *testing.T) {
	err := osix.RemoteBranchConflictError{
		Tag:      "main",
		Expected: "sha256:old",
		Current:  "sha256:new",
	}
	if got := exitCode(err); got != 3 {
		t.Fatalf("remote branch conflict exit code = %d, want 3", got)
	}
	if got := exitCode(fmt.Errorf("plain failure")); got != 1 {
		t.Fatalf("plain failure exit code = %d, want 1", got)
	}
}

func TestWatchStatusCommandReadsDaemonRecord(t *testing.T) {
	root := t.TempDir()
	if _, err := osix.Init(root, osix.InitOptions{
		Base:          "base",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "agentfs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "agentfs")
	if _, err := osix.PrepareWatchDaemon(root, target, osix.WatchOptions{}); err != nil {
		t.Fatal(err)
	}
	withWorkingDir(t, root, func() {
		if err := run([]string{"watch", "status", target}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestWatchListCommandReadsDaemonRecords(t *testing.T) {
	root := t.TempDir()
	if _, err := osix.Init(root, osix.InitOptions{
		Base:          "base",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "agentfs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "agentfs")
	if _, err := osix.PrepareWatchDaemon(root, target, osix.WatchOptions{}); err != nil {
		t.Fatal(err)
	}
	withWorkingDir(t, root, func() {
		if err := run([]string{"watch", "list"}); err != nil {
			t.Fatal(err)
		}
	})
	records, err := osix.WatchDaemonList(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Target != target {
		t.Fatalf("expected listed watch for %s, got %#v", target, records)
	}
}

func TestWatchRestartCommandRejectsRunningRecord(t *testing.T) {
	root := t.TempDir()
	if _, err := osix.Init(root, osix.InitOptions{
		Base:          "base",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "agentfs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "agentfs")
	record, err := osix.PrepareWatchDaemon(root, target, osix.WatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := osix.MarkWatchDaemonRunning(record, 12345); err != nil {
		t.Fatal(err)
	}
	withWorkingDir(t, root, func() {
		err := run([]string{"watch", "restart", target})
		if err == nil || !strings.Contains(err.Error(), "already running") {
			t.Fatalf("expected running restart rejection, got %v", err)
		}
	})
}

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	oldStdout := os.Stdout
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writeEnd
	runErr := fn()
	if closeErr := writeEnd.Close(); closeErr != nil && runErr == nil {
		runErr = closeErr
	}
	os.Stdout = oldStdout
	data, readErr := io.ReadAll(readEnd)
	if closeErr := readEnd.Close(); closeErr != nil && readErr == nil {
		readErr = closeErr
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(data), runErr
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

func readCLISnapshotLayerDigest(t *testing.T, root, manifestDigest string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".osix", "blobs", "sha256", strings.TrimPrefix(manifestDigest, "sha256:")))
	if err != nil {
		t.Fatal(err)
	}
	var manifest osix.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Layers) != 1 {
		t.Fatalf("expected one layer, got %#v", manifest.Layers)
	}
	return manifest.Layers[0].Digest
}

type cliFakeRegistry struct {
	blobs     map[string][]byte
	manifests map[string][]byte
	uploads   map[string]string
	blobGets  map[string]int
	nextID    int
}

func newCLIFakeRegistry() *cliFakeRegistry {
	return &cliFakeRegistry{
		blobs:     map[string][]byte{},
		manifests: map[string][]byte{},
		uploads:   map[string]string{},
		blobGets:  map[string]int{},
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
		r.blobGets[digest]++
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
