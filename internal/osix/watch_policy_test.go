package osix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWatchCreatesSnapshotAndState(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{Base: "base", Name: "agent", StateRef: "local/agent", Mount: filepath.Join(root, "fs"), DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "watch\n")
	result, err := Watch(root, fs, WatchOptions{Once: true, MaxDirtyBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Snapshots) != 1 || result.StatePath == "" {
		t.Fatalf("unexpected watch result: %#v", result)
	}
	if _, err := os.Stat(result.StatePath); err != nil {
		t.Fatal(err)
	}
	stateInfo, err := os.Stat(result.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if stateInfo.Mode().Perm() != 0o600 {
		t.Fatalf("watch state mode = %o, want 0600", stateInfo.Mode().Perm())
	}
	watchDirInfo, err := os.Stat(filepath.Dir(result.StatePath))
	if err != nil {
		t.Fatal(err)
	}
	if watchDirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("watch state dir mode = %o, want 0700", watchDirInfo.Mode().Perm())
	}
}

func TestWatchRetentionCreatesCheckpointAndRecordsState(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{Base: "base", Name: "agent", StateRef: "local/agent", Mount: filepath.Join(root, "fs"), DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "v1\n")
	if _, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001", AlsoTag: "main"}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "v2\n")
	result, err := Watch(root, fs, WatchOptions{
		Once:          true,
		MaxDirtyBytes: 1,
		Retention: WatchRetentionPolicy{
			CompactEvery:        1,
			SquashEvery:         2,
			CheckpointTagPrefix: "checkpoint",
			PruneLocal:          true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Snapshots) != 1 || len(result.Compactions) != 1 {
		t.Fatalf("unexpected watch retention result: %#v", result)
	}
	plan := result.Compactions[0]
	if plan.CheckpointDigest == "" || plan.CheckpointTag != "checkpoint-000002" {
		t.Fatalf("unexpected compaction plan: %#v", plan)
	}
	state, err := readWatchState(result.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.LastCompaction == nil || state.LastCompaction.CheckpointDigest != plan.CheckpointDigest {
		t.Fatalf("watch state missing compaction: %#v", state)
	}
	restore := filepath.Join(root, "restore")
	if err := Restore(root, "checkpoint-000002", restore, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(restore, "agent", "workspace", "file.txt"), "v2\n")
}

func TestWatchTurnBoundaryHook(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{Base: "base", Name: "agent", StateRef: "local/agent", Mount: filepath.Join(root, "fs"), DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, ".osix-turn-boundary"), "")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "boundary\n")
	if _, err := Watch(root, fs, WatchOptions{Once: true, OnTurnBoundary: true}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentPolicyLedgerSecretScanReplayAndRedaction(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{Base: "base", Name: "agent", StateRef: "local/agent", Mount: filepath.Join(root, "fs"), DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "ok\n")
	mustWrite(t, filepath.Join(fs, "agent", "side-effects", "ledger.jsonl"), `{"turn":1,"tool":"github.create_issue","idempotencyKey":"k","requestDigest":"sha256:req","responseDigest":"sha256:resp","externalResource":"github:repo/issues/1","replayPolicy":"mock-by-default"}`+"\n")
	mustWrite(t, filepath.Join(fs, "agent", "logs", "tool.jsonl"), `{"api_key":"secret-value","message":"keep"}`+"\n")
	mustWrite(t, filepath.Join(fs, ".env"), "TOKEN=secret\n")
	if _, err := Snapshot(root, fs, SnapshotOptions{Tag: "blocked", SecretScan: "block"}); err == nil {
		t.Fatalf("expected secret scan block")
	}
	if err := os.Remove(filepath.Join(fs, ".env")); err != nil {
		t.Fatal(err)
	}
	if _, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001", SecretScan: "block"}); err != nil {
		t.Fatal(err)
	}
	restore := filepath.Join(root, "restore")
	if err := Restore(root, "snap-000001", restore, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(filepath.Join(restore, ".osix-replay-policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(string(marker), `"mode": "require-approval"`) {
		t.Fatalf("unexpected replay marker: %s", marker)
	}
	logData, err := os.ReadFile(filepath.Join(restore, "agent", "logs", "tool.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(logData) == `{"api_key":"secret-value","message":"keep"}`+"\n" {
		t.Fatalf("log was not redacted")
	}
}

func containsString(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
