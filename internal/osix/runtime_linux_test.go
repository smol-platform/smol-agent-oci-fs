//go:build linux

package osix

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestOverlayRuntimeIntegrationLinux(t *testing.T) {
	if err := overlayAvailable(); err != nil {
		t.Skipf("overlayfs unavailable: %v", err)
	}
	testKernelRuntimeIntegration(t, MountOverlay)
}

func TestFUSERuntimeIntegrationLinux(t *testing.T) {
	if err := fuseAvailable(); err != nil {
		t.Skipf("fuse-overlayfs unavailable: %v", err)
	}
	testKernelRuntimeIntegration(t, MountFUSE)
}

func testKernelRuntimeIntegration(t *testing.T, mode MountMode) {
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
	target := filepath.Join(root, "mount")
	rt := NewMountRuntime(root, mode)
	info, err := rt.Mount(ctx, "snap-000001", target, MountOptions{Force: true, RW: true, Mode: mode})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = rt.Unmount(ctx, target, UnmountOptions{Force: true})
	}()
	waitForPath(t, filepath.Join(target, "agent", "workspace", "file.txt"))
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

func waitForPath(t *testing.T, path string) {
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
