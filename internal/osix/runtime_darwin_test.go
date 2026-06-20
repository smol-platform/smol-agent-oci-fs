//go:build darwin

package osix

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDarwinFSKitPrerequisiteError(t *testing.T) {
	t.Setenv("OSIX_FSKIT_HELPER", filepath.Join(t.TempDir(), "missing-osix-fskitctl"))
	err := darwinFSKitAvailable()
	if err == nil {
		t.Fatalf("expected FSKit prerequisite error")
	}
	if !strings.Contains(err.Error(), "OSIX_FSKIT_HELPER") {
		t.Fatalf("expected helper prerequisite error, got %v", err)
	}
	if err := overlayAvailable(); err == nil {
		t.Fatalf("expected overlay prerequisite error")
	}
	if err := fuseAvailable(); err == nil {
		t.Fatalf("expected fuse prerequisite error")
	}
}

func TestDarwinFSKitHelperRequiresExecutableFile(t *testing.T) {
	root := t.TempDir()
	helperFile := filepath.Join(root, "osix-fskitctl")
	if err := os.WriteFile(helperFile, []byte("#!/bin/sh\nexit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OSIX_FSKIT_HELPER", helperFile)
	if _, err := darwinFSKitHelper(); err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("expected non-executable helper error, got %v", err)
	}

	if err := os.Chmod(helperFile, 0o700); err != nil {
		t.Fatal(err)
	}
	if helper, err := darwinFSKitHelper(); err != nil || helper != helperFile {
		t.Fatalf("expected executable helper %q, got helper=%q err=%v", helperFile, helper, err)
	}

	helperDir := filepath.Join(root, "helper-dir")
	if err := os.Mkdir(helperDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OSIX_FSKIT_HELPER", helperDir)
	if _, err := darwinFSKitHelper(); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("expected directory helper error, got %v", err)
	}
}

func TestDarwinAutoFallsBackToMaterializedWhenFSKitUnavailable(t *testing.T) {
	t.Setenv("OSIX_FSKIT_HELPER", filepath.Join(t.TempDir(), "missing-osix-fskitctl"))
	root := t.TempDir()
	if _, err := Init(root, InitOptions{
		Base:          "example/base:latest",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "fs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "v1\n")
	if _, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001"}); err != nil {
		t.Fatal(err)
	}
	info, err := Mount(root, "snap-000001", filepath.Join(root, "mounted"), MountOptions{Force: true, Mode: MountAuto})
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode != MountMaterialized {
		t.Fatalf("auto mode = %q, want materialized fallback", info.Mode)
	}
}

func TestDarwinFSKitIntegration(t *testing.T) {
	if err := darwinFSKitAvailable(); err != nil {
		t.Skipf("macOS FSKit prerequisites unavailable: %v", err)
	}
	testDarwinFSKitRuntimeIntegration(t, MountOverlay)
	testDarwinFSKitRuntimeIntegration(t, MountFUSE)
}

func testDarwinFSKitRuntimeIntegration(t *testing.T, mode MountMode) {
	t.Helper()
	root := t.TempDir()
	if _, err := Init(root, InitOptions{
		Base:          "example/base:latest",
		Name:          "agent",
		StateRef:      "local/agent",
		Mount:         filepath.Join(root, "fs"),
		DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	fs := filepath.Join(root, "fs")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "v1\n")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "remove.txt"), "remove\n")
	if _, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001"}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	target := filepath.Join(root, "mount-"+string(mode))
	rt := NewMountRuntime(root, mode)
	info, err := rt.Mount(ctx, "snap-000001", target, MountOptions{Force: true, RW: true, Mode: mode})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = rt.Unmount(ctx, target, UnmountOptions{Force: true})
	}()
	waitForDarwinPath(t, filepath.Join(target, "agent", "workspace", "file.txt"))
	assertFile(t, filepath.Join(target, "agent", "workspace", "file.txt"), "v1\n")
	mustWrite(t, filepath.Join(target, "agent", "workspace", "file.txt"), "v2\n")
	mustWrite(t, filepath.Join(target, "agent", "workspace", "new.txt"), "new\n")
	if err := os.Remove(filepath.Join(target, "agent", "workspace", "remove.txt")); err != nil {
		t.Fatal(err)
	}
	changes, err := rt.Diff(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	wantChanges := []string{
		"M /agent/workspace/file.txt",
		"A /agent/workspace/new.txt",
		"D /agent/workspace/remove.txt",
	}
	if got := changeStrings(changes); !reflect.DeepEqual(got, wantChanges) {
		t.Fatalf("changes mismatch\nwant: %#v\n got: %#v", wantChanges, got)
	}
	if _, err := rt.Status(ctx, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(info.WorkDir), "dirty.json")); err != nil {
		t.Fatal(err)
	}
	watch, err := Watch(root, target, WatchOptions{Iterations: 1, MaxDirtyBytes: 1, TagPrefix: "darwin-watch-" + string(mode)})
	if err != nil {
		t.Fatal(err)
	}
	if len(watch.Snapshots) != 1 || watch.StatePath == "" {
		t.Fatalf("unexpected watch result: %#v", watch)
	}
	watchRestore := filepath.Join(root, "watch-restore-"+string(mode))
	if err := Restore(root, watch.Snapshots[0].ManifestDigest, watchRestore, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(watchRestore, "agent", "workspace", "file.txt"), "v2\n")
	assertFile(t, filepath.Join(watchRestore, "agent", "workspace", "new.txt"), "new\n")
	assertMissing(t, filepath.Join(watchRestore, "agent", "workspace", "remove.txt"))

	snap, err := rt.Snapshot(ctx, target, SnapshotOptions{Tag: "snap-000002"})
	if err != nil {
		t.Fatal(err)
	}
	restore := filepath.Join(root, "restore")
	if err := Restore(root, snap.ManifestDigest, restore, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(restore, "agent", "workspace", "file.txt"), "v2\n")
	assertFile(t, filepath.Join(restore, "agent", "workspace", "new.txt"), "new\n")
	assertMissing(t, filepath.Join(restore, "agent", "workspace", "remove.txt"))
}

func waitForDarwinPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
