package osix

import (
	"path/filepath"
	"testing"
)

func TestCompactDryRunAndCheckpoint(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{Base: "base", Name: "agent", StateRef: "local/agent", Mount: filepath.Join(root, "fs"), DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "v1\n")
	first, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001", Sign: "keyless"})
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "v2\n")
	if _, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000002"}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "v3\n")
	third, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000003"})
	if err != nil {
		t.Fatal(err)
	}
	dry, err := Compact(root, "snap-000003", CompactPolicy{SquashEvery: 2, PreserveSigned: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !dry.CreateCheckpoint || dry.ChainLength != 3 {
		t.Fatalf("unexpected dry-run plan: %#v", dry)
	}
	if len(dry.DeleteCandidates) == 0 {
		t.Fatalf("expected delete candidates in dry-run plan")
	}
	if !containsString(stringsJoin(dry.Keep), first.ManifestDigest) || !containsString(stringsJoin(dry.Keep), third.ManifestDigest) {
		t.Fatalf("expected signed first and head third to be kept: %#v", dry.Keep)
	}
	plan, err := Compact(root, "snap-000003", CompactPolicy{SquashEvery: 2, CheckpointTag: "checkpoint-000003"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.CheckpointDigest == "" {
		t.Fatalf("expected checkpoint digest: %#v", plan)
	}
	restore := filepath.Join(root, "restore")
	if err := Restore(root, "checkpoint-000003", restore, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(restore, "agent", "workspace", "file.txt"), "v3\n")
}

func stringsJoin(items []string) string {
	out := ""
	for _, item := range items {
		out += item + "\n"
	}
	return out
}
