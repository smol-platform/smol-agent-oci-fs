package osix

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWatchDaemonRecordStatusAndStop(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{Base: "base", Name: "agent", StateRef: "local/agent", Mount: filepath.Join(root, "fs"), DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "fs")
	record, err := PrepareWatchDaemon(root, target, WatchOptions{Every: time.Second, MaxDirtyBytes: 10, Push: true})
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "starting" || record.RecordPath == "" || record.StopPath == "" || record.LogPath == "" {
		t.Fatalf("unexpected prepared record: %#v", record)
	}
	if err := MarkWatchDaemonRunning(record, 12345); err != nil {
		t.Fatal(err)
	}
	running, err := WatchDaemonStatus(root, target)
	if err != nil {
		t.Fatal(err)
	}
	if running.Status != "running" || running.PID != 12345 || !running.Push {
		t.Fatalf("unexpected running record: %#v", running)
	}
	stopping, err := StopWatchDaemon(root, target)
	if err != nil {
		t.Fatal(err)
	}
	if stopping.Status != "stopping" {
		t.Fatalf("unexpected stopping record: %#v", stopping)
	}
	if _, err := os.Stat(stopping.StopPath); err != nil {
		t.Fatal(err)
	}
}

func TestWatchDaemonStatusMarksStaleHeartbeat(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{Base: "base", Name: "agent", StateRef: "local/agent", Mount: filepath.Join(root, "fs"), DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "fs")
	record, err := PrepareWatchDaemon(root, target, WatchOptions{Every: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	record.Status = "running"
	record.PID = 12345
	record.UpdatedAt = time.Now().UTC().Add(-time.Minute)
	if err := writeWatchDaemonRecord(record); err != nil {
		t.Fatal(err)
	}
	stale, err := WatchDaemonStatus(root, target)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Status != "stale" || stale.LastError == "" {
		t.Fatalf("expected stale record, got %#v", stale)
	}
}

func TestPrepareWatchDaemonRestartReusesStaleRecordOptions(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{Base: "base", Name: "agent", StateRef: "local/agent", Mount: filepath.Join(root, "fs"), DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "fs")
	record, err := PrepareWatchDaemon(root, target, WatchOptions{
		Every:         2 * time.Second,
		MaxDirtyBytes: 42,
		Push:          true,
		Encrypt:       "gpg:test",
		Retention: WatchRetentionPolicy{
			CompactEvery:        2,
			SquashEvery:         3,
			CheckpointTagPrefix: "checkpoint",
			PruneLocal:          true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	record.Status = "running"
	record.PID = 12345
	record.UpdatedAt = time.Now().UTC().Add(-time.Minute)
	if err := writeWatchDaemonRecord(record); err != nil {
		t.Fatal(err)
	}

	restarted, err := PrepareWatchDaemonRestart(root, target)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Status != "starting" || restarted.Every != 2*time.Second || restarted.MaxDirtyBytes != 42 || !restarted.Push || restarted.Encrypt != "gpg:test" {
		t.Fatalf("restart did not preserve options: %#v", restarted)
	}
	if restarted.Retention.CompactEvery != 2 || restarted.Retention.SquashEvery != 3 || !restarted.Retention.PruneLocal {
		t.Fatalf("restart did not preserve retention options: %#v", restarted.Retention)
	}
}

func TestPrepareWatchDaemonRestartRejectsRunningRecord(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{Base: "base", Name: "agent", StateRef: "local/agent", Mount: filepath.Join(root, "fs"), DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "fs")
	record, err := PrepareWatchDaemon(root, target, WatchOptions{Every: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkWatchDaemonRunning(record, 12345); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareWatchDaemonRestart(root, target); err == nil {
		t.Fatal("expected restart to reject a fresh running record")
	}
}

func TestWatchDaemonListReturnsRecordsWithState(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{Base: "base", Name: "agent", StateRef: "local/agent", Mount: filepath.Join(root, "fs-a"), DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	targetA := filepath.Join(root, "fs-a")
	targetB := filepath.Join(root, "fs-b")
	recordA, err := PrepareWatchDaemon(root, targetA, WatchOptions{Every: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	recordB, err := PrepareWatchDaemon(root, targetB, WatchOptions{Every: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkWatchDaemonRunning(recordA, 111); err != nil {
		t.Fatal(err)
	}
	state := WatchState{
		Target:       recordA.Target,
		LastSnapshot: "sha256:abc",
		Iterations:   2,
		UpdatedAt:    time.Now().UTC().Truncate(time.Second),
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(recordA.StatePath, data); err != nil {
		t.Fatal(err)
	}
	recordB.Status = "running"
	recordB.PID = 222
	recordB.UpdatedAt = time.Now().UTC().Add(-time.Minute)
	if err := writeWatchDaemonRecord(recordB); err != nil {
		t.Fatal(err)
	}

	records, err := WatchDaemonList(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("expected two records, got %#v", records)
	}
	if records[0].Target != recordA.Target || records[0].LastSnapshot != "sha256:abc" || records[0].Iterations != 2 {
		t.Fatalf("first record did not include state: %#v", records[0])
	}
	if records[1].Target != recordB.Target || records[1].Status != "stale" || records[1].LastError == "" {
		t.Fatalf("second record was not marked stale: %#v", records[1])
	}
}

func TestWatchUntilStoppedHonorsStopPath(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{Base: "base", Name: "agent", StateRef: "local/agent", Mount: filepath.Join(root, "fs"), DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "fs")
	stopPath := filepath.Join(root, ".osix", "watch-stop")
	mustWrite(t, stopPath, "stop\n")
	result, err := Watch(root, target, WatchOptions{UntilStopped: true, StopPath: stopPath, Every: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Snapshots) != 0 {
		t.Fatalf("expected no snapshots after preexisting stop file, got %#v", result.Snapshots)
	}
}

func TestWatchSurfacesBackgroundPushFailureInState(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{
		Base:          "base",
		Name:          "agent",
		StateRef:      "not-a-registry-reference",
		Mount:         filepath.Join(root, "fs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(target, "agent", "workspace", "file.txt"), "push failure\n")

	result, err := Watch(root, target, WatchOptions{Once: true, Push: true})
	if err == nil || !strings.Contains(err.Error(), "registry repo must be REGISTRY/REPO") {
		t.Fatalf("expected push failure, got %v", err)
	}
	state, readErr := readWatchState(result.StatePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if state.LastError == "" || !strings.Contains(state.LastError, "registry repo must be REGISTRY/REPO") {
		t.Fatalf("watch state did not surface push failure: %#v", state)
	}
}
