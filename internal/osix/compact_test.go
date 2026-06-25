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

func TestCompactPrunesLocalRefsAndBlobsAfterCheckpointReplacement(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{Base: "base", Name: "agent", StateRef: "local/agent", Mount: filepath.Join(root, "fs"), DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "v1\n")
	first, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "v2\n")
	second, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000002", AlsoTag: "main"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Compact(root, second.ManifestDigest, CompactPolicy{
		SquashEvery:    2,
		CheckpointTag:  "checkpoint-main",
		CheckpointTags: []string{"main", "latest"},
		PruneLocal:     true,
		PruneSource:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.CheckpointDigest == "" || len(plan.PrunedRefs) == 0 || len(plan.PrunedBlobs) == 0 {
		t.Fatalf("expected checkpoint and local pruning: %#v", plan)
	}
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.resolveRef("main"); err != nil || got != plan.CheckpointDigest {
		t.Fatalf("main ref = %s err=%v, want checkpoint %s", got, err, plan.CheckpointDigest)
	}
	for _, ref := range []string{"snap-000001", "snap-000002"} {
		if _, err := s.resolveRef(ref); err == nil {
			t.Fatalf("expected %s ref to be pruned", ref)
		}
	}
	for _, digest := range []string{first.ManifestDigest, second.ManifestDigest} {
		if s.hasBlob(digest) {
			t.Fatalf("expected manifest blob %s to be pruned", digest)
		}
	}
	restore := filepath.Join(root, "restore")
	if err := Restore(root, "checkpoint-main", restore, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(restore, "agent", "workspace", "file.txt"), "v2\n")
}

func stringsJoin(items []string) string {
	out := ""
	for _, item := range items {
		out += item + "\n"
	}
	return out
}
