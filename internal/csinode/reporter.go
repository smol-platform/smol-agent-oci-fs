package csinode

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/smol-platform/smol-agent-oci-fs/internal/k8soperator"
)

type SnapshotEvent struct {
	VolumeID            string                        `json:"volumeID"`
	FileSystemName      string                        `json:"fileSystemName"`
	FileSystemNamespace string                        `json:"fileSystemNamespace,omitempty"`
	SnapshotDigest      string                        `json:"snapshotDigest,omitempty"`
	CheckpointDigest    string                        `json:"checkpointDigest,omitempty"`
	StatePath           string                        `json:"statePath,omitempty"`
	Error               string                        `json:"error,omitempty"`
	Status              k8soperator.AgentOCIStatus    `json:"status"`
	AgentSnapshot       *k8soperator.AgentOCISnapshot `json:"agentSnapshot,omitempty"`
	ObservedAt          time.Time                     `json:"observedAt"`
}

type SnapshotReporter interface {
	ReportSnapshot(context.Context, MountRecord, SnapshotResult, error) error
}

type FileReporter struct {
	Root string
}

func (r FileReporter) ReportSnapshot(ctx context.Context, record MountRecord, result SnapshotResult, snapErr error) error {
	if err := os.MkdirAll(r.Root, 0o700); err != nil {
		return err
	}
	event := snapshotEvent(record, result, snapErr)
	data, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(r.Root, safeFileName(record.VolumeID)+"-last.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type MultiReporter []SnapshotReporter

func (m MultiReporter) ReportSnapshot(ctx context.Context, record MountRecord, result SnapshotResult, snapErr error) error {
	var first error
	for _, reporter := range m {
		if reporter == nil {
			continue
		}
		if err := reporter.ReportSnapshot(ctx, record, result, snapErr); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func snapshotEvent(record MountRecord, result SnapshotResult, snapErr error) SnapshotEvent {
	fs := k8soperator.NormalizeFileSystem(record.FileSystem)
	var status k8soperator.AgentOCIStatus
	var snapDigest string
	if len(result.SnapshotDigests) > 0 {
		snapDigest = result.SnapshotDigests[len(result.SnapshotDigests)-1]
	}
	var checkpointDigest string
	if len(result.CheckpointDigests) > 0 {
		checkpointDigest = result.CheckpointDigests[len(result.CheckpointDigests)-1]
	}
	redactedErr := redactError(snapErr)
	k8soperator.MarkSnapshotResult(&status, snapDigest, checkpointDigest, redactedErr)
	k8soperator.MarkRegistry(&status, snapErr == nil, "SnapshotPush", fs.Spec.StateRef)
	event := SnapshotEvent{
		VolumeID:            record.VolumeID,
		FileSystemName:      fs.ObjectMeta.Name,
		FileSystemNamespace: fs.ObjectMeta.Namespace,
		SnapshotDigest:      snapDigest,
		CheckpointDigest:    checkpointDigest,
		StatePath:           result.StatePath,
		Status:              status,
		ObservedAt:          time.Now().UTC().Truncate(time.Second),
	}
	if redactedErr != nil {
		event.Error = redactedErr.Error()
	}
	if snapErr == nil && (snapDigest != "" || checkpointDigest != "") {
		event.AgentSnapshot = &k8soperator.AgentOCISnapshot{
			TypeMeta: k8soperator.TypeMeta{APIVersion: k8soperator.APIVersion, Kind: k8soperator.KindAgentOCISnapshot},
			ObjectMeta: k8soperator.ObjectMeta{
				Name:      snapshotObjectName(record.VolumeID, snapDigest, checkpointDigest),
				Namespace: fs.ObjectMeta.Namespace,
				Labels: map[string]string{
					"agent.smol.ai/filesystem": fs.ObjectMeta.Name,
					"agent.smol.ai/volume-id":  record.VolumeID,
				},
			},
			Spec: k8soperator.AgentOCISnapshotSpec{
				FileSystemName:   fs.ObjectMeta.Name,
				FileSystemUID:    fs.ObjectMeta.UID,
				SnapshotDigest:   snapDigest,
				Branch:           fs.Spec.Branch,
				CheckpointDigest: checkpointDigest,
			},
			Status: status,
		}
	}
	return event
}

var sensitiveValuePattern = regexp.MustCompile(`(?i)(password|passwd|token|secret|authorization|credential|identityToken)(\s*[=:]\s*)[^\s,;]+`)
var bearerPattern = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]+`)

func redactError(err error) error {
	if err == nil {
		return nil
	}
	message := bearerPattern.ReplaceAllString(err.Error(), "Bearer REDACTED")
	message = sensitiveValuePattern.ReplaceAllString(message, `${1}${2}REDACTED`)
	return fmt.Errorf("%s", message)
}

func snapshotObjectName(volumeID, snapshotDigest, checkpointDigest string) string {
	digest := checkpointDigest
	if digest == "" {
		digest = snapshotDigest
	}
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) > 16 {
		digest = digest[:16]
	}
	if digest == "" {
		digest = fmt.Sprintf("%d", time.Now().UTC().Unix())
	}
	return safeFileName(volumeID) + "-" + digest
}

type KubernetesReporter struct {
	Host       string
	TokenPath  string
	CACertPath string
	Client     *http.Client
}

func NewInClusterKubernetesReporter() (*KubernetesReporter, bool) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, false
	}
	return &KubernetesReporter{
		Host:       "https://" + host + ":" + port,
		TokenPath:  "/var/run/secrets/kubernetes.io/serviceaccount/token",
		CACertPath: "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt",
	}, true
}

func (r *KubernetesReporter) ReportSnapshot(ctx context.Context, record MountRecord, result SnapshotResult, snapErr error) error {
	event := snapshotEvent(record, result, snapErr)
	namespace := event.FileSystemNamespace
	if namespace == "" {
		namespace = "default"
	}
	if event.AgentSnapshot != nil {
		if err := r.createOrPatch(ctx, fmt.Sprintf("/apis/agent.smol.ai/v1alpha1/namespaces/%s/agentocisnapshots", namespace), event.AgentSnapshot); err != nil {
			return err
		}
	}
	statusPatch := map[string]any{"status": event.Status}
	if err := r.mergePatch(ctx, fmt.Sprintf("/apis/agent.smol.ai/v1alpha1/namespaces/%s/agentocifilesystems/%s/status", namespace, event.FileSystemName), statusPatch); err != nil {
		return err
	}
	return r.createEvent(ctx, namespace, event)
}

func (r *KubernetesReporter) createOrPatch(ctx context.Context, path string, value any) error {
	if err := r.requestJSON(ctx, http.MethodPost, path, "application/json", value, nil); err == nil {
		return nil
	} else if !strings.Contains(err.Error(), "409") {
		return err
	}
	body, _ := value.(*k8soperator.AgentOCISnapshot)
	if body == nil {
		return nil
	}
	patch := map[string]any{"spec": body.Spec, "status": body.Status, "metadata": map[string]any{"labels": body.ObjectMeta.Labels}}
	namespace := body.ObjectMeta.Namespace
	if namespace == "" {
		namespace = "default"
	}
	return r.mergePatch(ctx, fmt.Sprintf("/apis/agent.smol.ai/v1alpha1/namespaces/%s/agentocisnapshots/%s", namespace, body.ObjectMeta.Name), patch)
}

func (r *KubernetesReporter) mergePatch(ctx context.Context, path string, value any) error {
	return r.requestJSON(ctx, http.MethodPatch, path, "application/merge-patch+json", value, nil)
}

func (r *KubernetesReporter) createEvent(ctx context.Context, namespace string, event SnapshotEvent) error {
	reason := "OSIxSnapshotSucceeded"
	eventType := "Normal"
	message := "automatic OSIx snapshot completed"
	if event.Error != "" {
		reason = "OSIxSnapshotFailed"
		eventType = "Warning"
		message = event.Error
	} else if event.CheckpointDigest != "" {
		reason = "OSIxCheckpointCreated"
		message = "automatic OSIx checkpoint completed"
	}
	now := time.Now().UTC()
	body := map[string]any{
		"apiVersion": "v1",
		"kind":       "Event",
		"metadata": map[string]any{
			"name":      eventName(event, now),
			"namespace": namespace,
		},
		"involvedObject": map[string]any{
			"apiVersion": k8soperator.APIVersion,
			"kind":       k8soperator.KindAgentOCIFileSystem,
			"name":       event.FileSystemName,
			"namespace":  namespace,
		},
		"type":           eventType,
		"reason":         reason,
		"message":        message,
		"firstTimestamp": now.Format(time.RFC3339),
		"lastTimestamp":  now.Format(time.RFC3339),
		"count":          1,
		"source": map[string]any{
			"component": "osix-csi-node",
		},
	}
	return r.requestJSON(ctx, http.MethodPost, fmt.Sprintf("/api/v1/namespaces/%s/events", namespace), "application/json", body, nil)
}

func eventName(event SnapshotEvent, observedAt time.Time) string {
	base := strings.ToLower(safeFileName(event.FileSystemName + "-" + event.VolumeID))
	base = regexp.MustCompile(`[^a-z0-9.-]+`).ReplaceAllString(base, "-")
	base = strings.Trim(base, "-.")
	if base == "" {
		base = "osix-snapshot"
	}
	suffix := fmt.Sprintf("%x", observedAt.UnixNano())
	maxBase := 63 - len(suffix) - 1
	if len(base) > maxBase {
		base = base[:maxBase]
		base = strings.Trim(base, "-.")
	}
	return base + "." + suffix
}

func (r *KubernetesReporter) requestJSON(ctx context.Context, method, path, contentType string, value any, out any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(r.Host, "/")+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	token, err := os.ReadFile(r.TokenPath)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	client := r.Client
	if client == nil {
		client = kubernetesHTTPClient(r.CACertPath)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("kubernetes %s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out != nil && len(respBody) > 0 {
		return json.Unmarshal(respBody, out)
	}
	return nil
}

func kubernetesHTTPClient(caCertPath string) *http.Client {
	rootCAs, _ := x509.SystemCertPool()
	if rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}
	if caCertPath != "" {
		if ca, err := os.ReadFile(caCertPath); err == nil {
			rootCAs.AppendCertsFromPEM(ca)
		}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: rootCAs}}}
}
