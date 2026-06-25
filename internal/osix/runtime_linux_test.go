//go:build linux

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

func TestLazyFUSEAutoSelectsNativeRuntimeIntegrationLinux(t *testing.T) {
	if err := lazyFuseAvailable(); err != nil {
		t.Skipf("native FUSE unavailable: %v", err)
	}
	mode, err := selectMountMode(t.TempDir(), MountAuto, MountOptions{Lazy: true, RW: true})
	if err != nil {
		t.Fatal(err)
	}
	if mode != MountFUSE {
		t.Fatalf("lazy auto mode = %s, want %s", mode, MountFUSE)
	}
}

func TestLazyFUSEOverlayModeRejectedRuntimeIntegrationLinux(t *testing.T) {
	_, err := selectMountMode(t.TempDir(), MountOverlay, MountOptions{Lazy: true})
	if err == nil || !strings.Contains(err.Error(), "lazy overlay mode is not supported") {
		t.Fatalf("expected lazy overlay rejection, got %v", err)
	}
}

func TestLazyFUSEReadOnlyRuntimeIntegrationLinux(t *testing.T) {
	if err := lazyFuseAvailable(); err != nil {
		t.Skipf("native FUSE unavailable: %v", err)
	}
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
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "file.txt"), "lazy fuse\n")
	if err := os.Symlink("../workspace/file.txt", filepath.Join(fs, "agent", "workspace", "link.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-lazy-fuse"}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	target := filepath.Join(root, "mount")
	rt := NewMountRuntime(root, MountFUSE)
	info, err := rt.Mount(ctx, "snap-lazy-fuse", target, MountOptions{Force: true, Mode: MountFUSE, Lazy: true, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = rt.Unmount(ctx, target, UnmountOptions{Force: true})
	}()
	if info.RW {
		t.Fatal("lazy read-only FUSE mount metadata RW = true")
	}
	assertMissing(t, filepath.Join(info.LowerDir, "agent", "workspace", "file.txt"))
	waitForPath(t, filepath.Join(target, "agent", "workspace", "file.txt"))
	assertFile(t, filepath.Join(target, "agent", "workspace", "file.txt"), "lazy fuse\n")
	linkTarget, err := os.Readlink(filepath.Join(target, "agent", "workspace", "link.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if linkTarget != "../workspace/file.txt" {
		t.Fatalf("unexpected symlink target %q", linkTarget)
	}
	if err := os.WriteFile(filepath.Join(target, "agent", "workspace", "file.txt"), []byte("mutate\n"), 0o644); err == nil {
		t.Fatal("write to lazy read-only FUSE mount succeeded")
	}
	assertMissing(t, filepath.Join(info.LowerDir, "agent", "workspace", "file.txt"))
}

func TestLazyFUSEWritableRuntimeIntegrationLinux(t *testing.T) {
	if err := lazyFuseAvailable(); err != nil {
		t.Skipf("native FUSE unavailable: %v", err)
	}
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
	if _, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-lazy-fuse-writable"}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	target := filepath.Join(root, "mount")
	rt := NewMountRuntime(root, MountFUSE)
	info, err := rt.Mount(ctx, "snap-lazy-fuse-writable", target, MountOptions{Force: true, Mode: MountFUSE, Lazy: true, RW: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = rt.Unmount(ctx, target, UnmountOptions{Force: true})
	}()
	if !info.RW {
		t.Fatal("lazy writable FUSE mount metadata RW = false")
	}
	assertMissing(t, filepath.Join(info.LowerDir, "agent", "workspace", "file.txt"))
	waitForPath(t, filepath.Join(target, "agent", "workspace", "file.txt"))
	assertFile(t, filepath.Join(target, "agent", "workspace", "file.txt"), "v1\n")

	mustWrite(t, filepath.Join(target, "agent", "workspace", "file.txt"), "v2\n")
	mustWrite(t, filepath.Join(target, "agent", "workspace", "new.txt"), "new\n")
	if err := os.Remove(filepath.Join(target, "agent", "workspace", "remove.txt")); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(info.UpperDir, "agent", "workspace", "file.txt"), "v2\n")
	assertFile(t, filepath.Join(info.UpperDir, "agent", "workspace", "new.txt"), "new\n")
	if _, err := os.Stat(filepath.Join(info.UpperDir, "agent", "workspace", ".wh.remove.txt")); err != nil {
		t.Fatalf("expected upper whiteout: %v", err)
	}
	assertMissing(t, filepath.Join(info.LowerDir, "agent", "workspace", "file.txt"))

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
	snap, err := rt.Snapshot(ctx, target, SnapshotOptions{Tag: "snap-lazy-fuse-writable-2"})
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

func TestLazyFUSEWritableEncryptedRuntimeIntegrationLinux(t *testing.T) {
	if err := lazyFuseAvailable(); err != nil {
		t.Skipf("native FUSE unavailable: %v", err)
	}
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
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "secret.txt"), "encrypted lower\n")
	snap, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-lazy-fuse-encrypted", Encrypt: "gpg:test-recipient"})
	if err != nil {
		t.Fatal(err)
	}
	s := removeWholeLayerBlob(t, root, snap.ManifestDigest)
	_, manifest, _, err := s.loadManifest(snap.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	layerDigest := manifest.Layers[0].Digest

	ctx := context.Background()
	wrongTarget := filepath.Join(root, "wrong-mount")
	wrongRT := NewMountRuntime(root, MountFUSE)
	wrongInfo, err := wrongRT.Mount(ctx, snap.ManifestDigest, wrongTarget, MountOptions{Force: true, Mode: MountFUSE, Lazy: true, RW: true, Decrypt: "gpg:other-recipient"})
	if err != nil {
		t.Fatal(err)
	}
	waitForPath(t, filepath.Join(wrongTarget, "agent", "workspace", "secret.txt"))
	if _, err := os.ReadFile(filepath.Join(wrongTarget, "agent", "workspace", "secret.txt")); err == nil {
		_ = wrongRT.Unmount(ctx, wrongTarget, UnmountOptions{Force: true})
		t.Fatal("encrypted lazy FUSE read succeeded with wrong decrypt material")
	}
	if s.hasBlob(layerDigest) {
		_ = wrongRT.Unmount(ctx, wrongTarget, UnmountOptions{Force: true})
		t.Fatal("wrong decrypt material restored the whole encrypted layer")
	}
	if err := wrongRT.Unmount(ctx, wrongTarget, UnmountOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	assertMissing(t, filepath.Join(wrongInfo.LowerDir, "agent", "workspace", "secret.txt"))

	target := filepath.Join(root, "mount")
	rt := NewMountRuntime(root, MountFUSE)
	info, err := rt.Mount(ctx, snap.ManifestDigest, target, MountOptions{Force: true, Mode: MountFUSE, Lazy: true, RW: true, Decrypt: "gpg:test-recipient"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = rt.Unmount(ctx, target, UnmountOptions{Force: true})
	}()
	waitForPath(t, filepath.Join(target, "agent", "workspace", "secret.txt"))
	assertFile(t, filepath.Join(target, "agent", "workspace", "secret.txt"), "encrypted lower\n")
	if s.hasBlob(layerDigest) {
		t.Fatal("encrypted lazy FUSE read restored the whole encrypted layer")
	}
	assertMissing(t, filepath.Join(info.LowerDir, "agent", "workspace", "secret.txt"))

	mustWrite(t, filepath.Join(target, "agent", "workspace", "secret.txt"), "plaintext upper\n")
	assertFile(t, filepath.Join(info.UpperDir, "agent", "workspace", "secret.txt"), "plaintext upper\n")
	if s.hasBlob(layerDigest) {
		t.Fatal("encrypted lazy FUSE copy-up restored the whole encrypted layer")
	}
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
