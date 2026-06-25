package csinode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smol-platform/smol-agent-oci-fs/internal/k8soperator"
)

func TestKubernetesReporterCreatesSnapshotStatusAndEvent(t *testing.T) {
	var requests []recordedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, readRecordedRequest(t, r))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	reporter := testKubernetesReporter(t, server.URL)
	record := MountRecord{
		VolumeID: "pvc-report",
		FileSystem: k8soperator.NormalizeFileSystem(k8soperator.AgentOCIFileSystem{
			ObjectMeta: k8soperator.ObjectMeta{Name: "agent-report", Namespace: "agents", UID: "uid-1"},
			Spec: k8soperator.AgentOCIFileSystemSpec{
				BaseImage: "base",
				StateRef:  "registry.example/agents/report",
				Branch:    "main",
			},
		}),
	}
	result := SnapshotResult{SnapshotDigests: []string{"sha256:snapshot"}, CheckpointDigests: []string{"sha256:checkpoint"}}
	if err := reporter.ReportSnapshot(context.Background(), record, result, nil); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 {
		t.Fatalf("requests = %d, want snapshot/status/event: %#v", len(requests), requests)
	}
	assertRequest(t, requests[0], http.MethodPost, "/apis/agent.smol.ai/v1alpha1/namespaces/agents/agentocisnapshots")
	assertRequest(t, requests[1], http.MethodPatch, "/apis/agent.smol.ai/v1alpha1/namespaces/agents/agentocifilesystems/agent-report/status")
	assertRequest(t, requests[2], http.MethodPost, "/api/v1/namespaces/agents/events")
	if got := requests[1].Body["status"].(map[string]any)["lastSnapshotDigest"]; got != "sha256:snapshot" {
		t.Fatalf("status lastSnapshotDigest = %v", got)
	}
	if got := requests[2].Body["reason"]; got != "OSIxCheckpointCreated" {
		t.Fatalf("event reason = %v", got)
	}
}

func TestKubernetesReporterFailureRedactsStatusAndEvent(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodies = append(bodies, readRecordedRequest(t, r).Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	reporter := testKubernetesReporter(t, server.URL)
	record := MountRecord{VolumeID: "pvc-fail", FileSystem: testFileSystem("agent-fail")}
	err := reporter.ReportSnapshot(context.Background(), record, SnapshotResult{}, errString("push denied token=supersecret Authorization: Bearer abc.def"))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(bodies)
	if err != nil {
		t.Fatal(err)
	}
	payload := string(encoded)
	for _, leaked := range []string{"supersecret", "abc.def"} {
		if strings.Contains(payload, leaked) {
			t.Fatalf("report payload leaked %q: %s", leaked, payload)
		}
	}
	if !strings.Contains(payload, "REDACTED") {
		t.Fatalf("report payload did not contain redaction marker: %s", payload)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

type recordedRequest struct {
	Method string
	Path   string
	Body   map[string]any
}

func readRecordedRequest(t *testing.T, r *http.Request) recordedRequest {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
		t.Fatalf("Authorization header = %q", got)
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return recordedRequest{Method: r.Method, Path: r.URL.Path, Body: body}
}

func assertRequest(t *testing.T, request recordedRequest, method, path string) {
	t.Helper()
	if request.Method != method || request.Path != path {
		t.Fatalf("request = %s %s, want %s %s", request.Method, request.Path, method, path)
	}
}

func testKubernetesReporter(t *testing.T, host string) *KubernetesReporter {
	t.Helper()
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &KubernetesReporter{Host: host, TokenPath: tokenPath, Client: http.DefaultClient}
}
