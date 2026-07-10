package csinode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smol-platform/smol-agent-oci-fs/internal/k8soperator"
)

type MountRecord struct {
	VolumeID      string                                  `json:"volumeID"`
	TargetPath    string                                  `json:"targetPath"`
	WorkspacePath string                                  `json:"workspacePath"`
	FileSystem    k8soperator.AgentOCIFileSystem          `json:"fileSystem"`
	Policy        *k8soperator.AgentOCISnapshotPolicySpec `json:"policy,omitempty"`
	AutoSnapshot  bool                                    `json:"autoSnapshot"`
	ReadOnly      bool                                    `json:"readOnly,omitempty"`
	PublishedAt   time.Time                               `json:"publishedAt"`
	UpdatedAt     time.Time                               `json:"updatedAt"`
}

func (n Node) recordsDir() string {
	return filepath.Join(n.WorkspaceRoot, "csi", "volumes")
}

func (n Node) reportsDir() string {
	return filepath.Join(n.WorkspaceRoot, "csi", "reports")
}

func (n Node) ReportsDir() string {
	return n.reportsDir()
}

func (n Node) recordPath(volumeID string) string {
	return filepath.Join(n.recordsDir(), safeFileName(volumeID)+".json")
}

func (n Node) writeMountRecord(record MountRecord) error {
	if record.VolumeID == "" {
		return fmt.Errorf("volume id is required")
	}
	now := time.Now().UTC().Truncate(time.Second)
	if record.PublishedAt.IsZero() {
		record.PublishedAt = now
	}
	record.UpdatedAt = now
	if err := os.MkdirAll(n.recordsDir(), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	path := n.recordPath(record.VolumeID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (n Node) removeMountRecord(volumeID string) error {
	if strings.TrimSpace(volumeID) == "" {
		return nil
	}
	err := os.Remove(n.recordPath(volumeID))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (n Node) readMountRecord(volumeID string) (MountRecord, error) {
	data, err := os.ReadFile(n.recordPath(volumeID))
	if err != nil {
		return MountRecord{}, err
	}
	var record MountRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return MountRecord{}, err
	}
	return record, nil
}

func (n Node) listMountRecords() ([]MountRecord, error) {
	entries, err := os.ReadDir(n.recordsDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []MountRecord
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(n.recordsDir(), entry.Name()))
		if err != nil {
			return nil, err
		}
		var record MountRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func safeFileName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "/", "-")
	value = strings.ReplaceAll(value, ":", "-")
	value = strings.ReplaceAll(value, string(os.PathSeparator), "-")
	if value == "" {
		return "volume"
	}
	return value
}
