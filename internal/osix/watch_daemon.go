package osix

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func PrepareWatchDaemon(workspaceRoot, target string, opts WatchOptions) (WatchDaemonRecord, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return WatchDaemonRecord{}, err
	}
	id, err := mountKey(target)
	if err != nil {
		return WatchDaemonRecord{}, err
	}
	dir := filepath.Join(s.root, "watch")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return WatchDaemonRecord{}, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	record := WatchDaemonRecord{
		ID:             id,
		Target:         absPath(target),
		Status:         "starting",
		StatePath:      filepath.Join(dir, id+".json"),
		RecordPath:     filepath.Join(dir, id+".daemon.json"),
		StopPath:       filepath.Join(dir, id+".stop"),
		LogPath:        filepath.Join(dir, id+".log"),
		Every:          opts.Every,
		MaxDirtyBytes:  opts.MaxDirtyBytes,
		OnTurnBoundary: opts.OnTurnBoundary,
		Push:           opts.Push,
		Encrypt:        opts.Encrypt,
		Retention:      opts.Retention,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	return record, writeWatchDaemonRecord(record)
}

func MarkWatchDaemonRunning(record WatchDaemonRecord, pid int) error {
	record.PID = pid
	record.Status = "running"
	record.UpdatedAt = time.Now().UTC().Truncate(time.Second)
	_ = os.Remove(record.StopPath)
	return writeWatchDaemonRecord(record)
}

func WatchDaemonStatus(workspaceRoot, target string) (WatchDaemonRecord, error) {
	record, err := readWatchDaemonRecord(workspaceRoot, target)
	if err != nil {
		return WatchDaemonRecord{}, err
	}
	return refreshWatchDaemonRecord(record), nil
}

func WatchDaemonList(workspaceRoot string) ([]WatchDaemonRecord, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.root, "watch")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []WatchDaemonRecord
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".daemon.json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var record WatchDaemonRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("parse watch daemon record %s: %w", path, err)
		}
		if record.RecordPath == "" {
			record.RecordPath = path
		}
		records = append(records, refreshWatchDaemonRecord(record))
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Target == records[j].Target {
			return records[i].ID < records[j].ID
		}
		return records[i].Target < records[j].Target
	})
	return records, nil
}

func refreshWatchDaemonRecord(record WatchDaemonRecord) WatchDaemonRecord {
	if state, err := readWatchState(record.StatePath); err == nil {
		record.LastSnapshot = state.LastSnapshot
		record.LastCompaction = state.LastCompaction
		record.LastError = state.LastError
		record.Iterations = state.Iterations
		record.UpdatedAt = state.UpdatedAt
	}
	if record.Status == "running" && watchDaemonStale(record) {
		record.Status = "stale"
		if record.LastError == "" {
			record.LastError = "watch daemon heartbeat is stale"
		}
		_ = writeWatchDaemonRecord(record)
	}
	return record
}

func StopWatchDaemon(workspaceRoot, target string) (WatchDaemonRecord, error) {
	record, err := readWatchDaemonRecord(workspaceRoot, target)
	if err != nil {
		return WatchDaemonRecord{}, err
	}
	if err := writePrivateFile(record.StopPath, []byte(time.Now().UTC().Format(time.RFC3339)+"\n")); err != nil {
		return WatchDaemonRecord{}, err
	}
	record.Status = "stopping"
	record.UpdatedAt = time.Now().UTC().Truncate(time.Second)
	return record, writeWatchDaemonRecord(record)
}

func PrepareWatchDaemonRestart(workspaceRoot, target string) (WatchDaemonRecord, error) {
	record, err := WatchDaemonStatus(workspaceRoot, target)
	if err != nil {
		return WatchDaemonRecord{}, err
	}
	if record.Status == "running" {
		return WatchDaemonRecord{}, fmt.Errorf("watch daemon %s is already running", record.ID)
	}
	return PrepareWatchDaemon(workspaceRoot, target, WatchOptions{
		Every:          record.Every,
		MaxDirtyBytes:  record.MaxDirtyBytes,
		OnTurnBoundary: record.OnTurnBoundary,
		Push:           record.Push,
		Encrypt:        record.Encrypt,
		Retention:      record.Retention,
	})
}

func CompleteWatchDaemon(record WatchDaemonRecord, result WatchResult, runErr error) error {
	record.Status = "stopped"
	record.UpdatedAt = time.Now().UTC().Truncate(time.Second)
	if len(result.Snapshots) > 0 {
		record.LastSnapshot = result.Snapshots[len(result.Snapshots)-1].ManifestDigest
	}
	if len(result.Compactions) > 0 {
		record.LastCompaction = &result.Compactions[len(result.Compactions)-1]
	}
	if runErr != nil {
		record.Status = "failed"
		record.LastError = runErr.Error()
	}
	return writeWatchDaemonRecord(record)
}

func readWatchDaemonRecord(workspaceRoot, target string) (WatchDaemonRecord, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return WatchDaemonRecord{}, err
	}
	id, err := mountKey(target)
	if err != nil {
		return WatchDaemonRecord{}, err
	}
	path := filepath.Join(s.root, "watch", id+".daemon.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return WatchDaemonRecord{}, err
	}
	var record WatchDaemonRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return WatchDaemonRecord{}, fmt.Errorf("parse watch daemon record: %w", err)
	}
	if record.RecordPath == "" {
		record.RecordPath = path
	}
	return record, nil
}

func writeWatchDaemonRecord(record WatchDaemonRecord) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(record.RecordPath, data)
}

func readWatchState(path string) (WatchState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return WatchState{}, err
	}
	var state WatchState
	if err := json.Unmarshal(data, &state); err != nil {
		return WatchState{}, err
	}
	return state, nil
}

func watchDaemonStale(record WatchDaemonRecord) bool {
	interval := record.Every
	if interval <= 0 {
		interval = time.Minute
	}
	threshold := 3 * interval
	if threshold < 30*time.Second {
		threshold = 30 * time.Second
	}
	return time.Since(record.UpdatedAt) > threshold
}
