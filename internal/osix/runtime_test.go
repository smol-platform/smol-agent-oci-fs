package osix

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMountRuntimeMaterializedStatusUnmountRecover(t *testing.T) {
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

	ctx := context.Background()
	mountDir := filepath.Join(root, "mounted")
	rt := NewMountRuntime(root, MountMaterialized)
	info, err := rt.Mount(ctx, "snap-000001", mountDir, MountOptions{Force: true, Branch: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode != MountMaterialized {
		t.Fatalf("mount mode = %q, want %q", info.Mode, MountMaterialized)
	}
	if info.UpperDir != mountDir || info.WorkDir == "" || info.State != "mounted" {
		t.Fatalf("unexpected mount info: %#v", info)
	}
	status, err := rt.Status(ctx, mountDir)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "mounted" || status.Mode != MountMaterialized {
		t.Fatalf("unexpected status: %#v", status)
	}
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.findMount(mountDir)
	if err != nil {
		t.Fatal(err)
	}
	stored.PID = 99999999
	if err := s.writeMount(stored); err != nil {
		t.Fatal(err)
	}
	status, err = NewMountRuntime(root, MountAuto).Status(ctx, mountDir)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "mounted" {
		t.Fatalf("materialized mount should ignore stale PID, got %q", status.State)
	}

	mustWrite(t, filepath.Join(mountDir, "agent", "workspace", "file.txt"), "v2\n")
	recovered, err := RecoverMount(root, mountDir)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != "recovered" {
		t.Fatalf("recover state = %q", recovered.State)
	}
	dirtyPath := filepath.Join(filepath.Dir(info.WorkDir), "dirty.json")
	dirty, err := os.ReadFile(dirtyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dirty), "agent/workspace/file.txt") {
		t.Fatalf("dirty index missing changed file: %s", dirty)
	}

	if err := rt.Unmount(ctx, mountDir, UnmountOptions{}); err != nil {
		t.Fatal(err)
	}
	status, err = rt.Status(ctx, mountDir)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "unmounted" {
		t.Fatalf("state after unmount = %q", status.State)
	}
}

func TestExplicitUnavailableMountModesReturnErrors(t *testing.T) {
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

	ctx := context.Background()
	if err := overlayAvailable(); err != nil {
		_, mountErr := NewMountRuntime(root, MountOverlay).Mount(ctx, "snap-000001", filepath.Join(root, "overlay"), MountOptions{Force: true})
		if mountErr == nil {
			t.Fatalf("expected overlay mount error when unavailable")
		}
	} else if runtime.GOOS != "linux" {
		t.Fatalf("overlay unexpectedly available on %s", runtime.GOOS)
	}
	if err := fuseAvailable(); err != nil {
		_, mountErr := NewMountRuntime(root, MountFUSE).Mount(ctx, "snap-000001", filepath.Join(root, "fuse"), MountOptions{Force: true})
		if mountErr == nil {
			t.Fatalf("expected fuse mount error when unavailable")
		}
	}
}

func TestMountRejectsNonPrivateRuntimeCache(t *testing.T) {
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
	cache := filepath.Join(root, "cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Mount(root, "snap-000001", filepath.Join(root, "mounted"), MountOptions{
		Force: true,
		Mode:  MountMaterialized,
		Cache: cache,
	})
	if err == nil || !strings.Contains(err.Error(), "non-private runtime cache") {
		t.Fatalf("expected cache permission error, got %v", err)
	}
}

func TestPrepareKernelMountDirsRollsBackFailedLowerRestore(t *testing.T) {
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
	target := filepath.Join(root, "merged")
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	mountID, err := mountKey(target)
	if err != nil {
		t.Fatal(err)
	}
	mountRoot := filepath.Join(s.mountsRoot(), mountID)

	_, _, _, _, err = prepareKernelMountDirs(root, "missing-snapshot", target, MountOptions{})
	if err == nil || !strings.Contains(err.Error(), "prepare lowerdir") {
		t.Fatalf("expected lowerdir restore failure, got %v", err)
	}
	if _, statErr := os.Stat(mountRoot); !os.IsNotExist(statErr) {
		t.Fatalf("failed mount prep should remove newly-created runtime root, stat err=%v", statErr)
	}
}

func TestPrepareKernelMountDirsPreservesPreexistingRootOnFailure(t *testing.T) {
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
	target := filepath.Join(root, "merged")
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	mountID, err := mountKey(target)
	if err != nil {
		t.Fatal(err)
	}
	mountRoot := filepath.Join(s.mountsRoot(), mountID)
	sentinel := filepath.Join(mountRoot, "upper", "sentinel.txt")
	mustWrite(t, sentinel, "keep\n")

	_, _, _, _, err = prepareKernelMountDirs(root, "missing-snapshot", target, MountOptions{})
	if err == nil || !strings.Contains(err.Error(), "prepare lowerdir") {
		t.Fatalf("expected lowerdir restore failure, got %v", err)
	}
	if data, readErr := os.ReadFile(sentinel); readErr != nil || string(data) != "keep\n" {
		t.Fatalf("failed mount prep should preserve preexisting runtime root, data=%q err=%v", data, readErr)
	}
}

func TestRecoverRejectsWorldWritableUpperdir(t *testing.T) {
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
	target := filepath.Join(root, "merged")
	upper := filepath.Join(root, "upper")
	work := filepath.Join(root, "work")
	for _, dir := range []string{target, upper, work} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(upper, 0o777); err != nil {
		t.Fatal(err)
	}
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.writeMount(MountInfo{
		Target:       absPath(target),
		SourceRef:    "snap-000001",
		SourceDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Mode:         MountOverlay,
		UpperDir:     upper,
		WorkDir:      work,
		State:        "mounted",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = RecoverMount(root, target)
	if err == nil || !strings.Contains(err.Error(), "world-writable runtime directory") {
		t.Fatalf("expected upperdir permission error, got %v", err)
	}
}

func TestStatusRejectsWorldWritableUpperdir(t *testing.T) {
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
	target := filepath.Join(root, "merged")
	upper := filepath.Join(root, "upper")
	work := filepath.Join(root, "work")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "changed.txt"), "changed\n")
	for _, dir := range []string{target, work} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(upper, 0o777); err != nil {
		t.Fatal(err)
	}
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.writeMount(MountInfo{
		Target:       absPath(target),
		SourceRef:    "snap-000001",
		SourceDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Mode:         MountFUSE,
		RW:           true,
		UpperDir:     upper,
		WorkDir:      work,
		State:        "mounted",
		PID:          os.Getpid(),
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
		UpdatedAt:    time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	_, err = NewMountRuntime(root, MountAuto).Status(context.Background(), target)
	if err == nil || !strings.Contains(err.Error(), "world-writable runtime directory") {
		t.Fatalf("expected upperdir permission error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(work), "dirty.json")); !os.IsNotExist(err) {
		t.Fatalf("dirty index should not be written for unsafe runtime dirs, stat err=%v", err)
	}
}

func TestStatusRejectsNonDirectoryWorkdir(t *testing.T) {
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
	target := filepath.Join(root, "merged")
	upper := filepath.Join(root, "upper")
	work := filepath.Join(root, "work-file")
	for _, dir := range []string{target, upper} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(work, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.writeMount(MountInfo{
		Target:       absPath(target),
		SourceRef:    "snap-000001",
		SourceDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Mode:         MountOverlay,
		RW:           true,
		UpperDir:     upper,
		WorkDir:      work,
		State:        "mounted",
		PID:          os.Getpid(),
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
		UpdatedAt:    time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	_, err = NewMountRuntime(root, MountAuto).Status(context.Background(), target)
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected non-directory workdir error, got %v", err)
	}
}

func TestStatusPersistsStaleRuntimeStateAndRefreshesDirtyIndex(t *testing.T) {
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
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "copied.txt"), "same\n")
	first, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001"})
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(root, "merged")
	upper := filepath.Join(root, "upper")
	work := filepath.Join(root, "work")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "copied.txt"), "same\n")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "changed.txt"), "changed\n")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	info := MountInfo{
		Target:       absPath(target),
		SourceRef:    "snap-000001",
		SourceDigest: first.ManifestDigest,
		Mode:         MountFUSE,
		RW:           true,
		UpperDir:     upper,
		WorkDir:      work,
		State:        "mounted",
		PID:          99999999,
		CreatedAt:    time.Now().UTC().Add(-time.Minute).Truncate(time.Second),
		UpdatedAt:    time.Now().UTC().Add(-time.Minute).Truncate(time.Second),
	}
	if err := s.writeMount(info); err != nil {
		t.Fatal(err)
	}

	status, err := NewMountRuntime(root, MountAuto).Status(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "stale" {
		t.Fatalf("status state = %q, want stale", status.State)
	}
	stored, err := s.findMount(target)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != "stale" {
		t.Fatalf("persisted state = %q, want stale", stored.State)
	}
	if !stored.UpdatedAt.After(info.UpdatedAt) {
		t.Fatalf("status should refresh updatedAt on persisted state change")
	}
	dirty, err := os.ReadFile(filepath.Join(filepath.Dir(info.WorkDir), "dirty.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dirty), "changed.txt") {
		t.Fatalf("dirty index missing changed file: %s", dirty)
	}
	if strings.Contains(string(dirty), "copied.txt") {
		t.Fatalf("dirty index included copied-up unchanged file: %s", dirty)
	}
}

func TestStatusRejectsMissingUpperdir(t *testing.T) {
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
	first, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001"})
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(root, "merged")
	work := filepath.Join(root, "work")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	missingUpper := filepath.Join(root, "missing-upper")
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.writeMount(MountInfo{
		Target:       absPath(target),
		SourceRef:    "snap-000001",
		SourceDigest: first.ManifestDigest,
		Mode:         MountFUSE,
		RW:           true,
		UpperDir:     missingUpper,
		WorkDir:      work,
		State:        "mounted",
		PID:          os.Getpid(),
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
		UpdatedAt:    time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	_, err = NewMountRuntime(root, MountAuto).Status(context.Background(), target)
	if err == nil || !strings.Contains(err.Error(), "runtime directory") || !strings.Contains(err.Error(), "missing-upper") {
		t.Fatalf("expected runtime directory error for missing upperdir, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(work), "dirty.json")); !os.IsNotExist(err) {
		t.Fatalf("dirty index should not be written after validation failure, stat err=%v", err)
	}
}

func TestPersistMountedRuntimeRollsBackOnMetadataFailure(t *testing.T) {
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
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	badRoot := filepath.Join(root, "not-a-dir")
	if err := os.WriteFile(badRoot, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target")
	rolledBack := false
	_, err = persistMountedRuntime(s, badRoot, MountInfo{
		Target:       absPath(target),
		SourceRef:    "snap-000001",
		SourceDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Mode:         MountOverlay,
		State:        "mounted",
	}, []byte("overlay\n"), func() error {
		rolledBack = true
		return nil
	})
	if err == nil {
		t.Fatalf("expected metadata persistence error")
	}
	if !rolledBack {
		t.Fatalf("expected rollback callback")
	}
	if _, findErr := s.findMount(target); findErr == nil {
		t.Fatalf("mount metadata should not be written after persistence failure")
	}
}

func TestSnapshotUsesOverlayUpperdirWhiteouts(t *testing.T) {
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
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "old.txt"), "old\n")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "copied.txt"), "same\n")
	first, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001"})
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(root, "merged")
	mustWrite(t, filepath.Join(target, "agent", "workspace", "file.txt"), "v2\n")
	mustWrite(t, filepath.Join(target, "agent", "workspace", "new.txt"), "new\n")
	upper := filepath.Join(root, "upper")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "file.txt"), "v2\n")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "new.txt"), "new\n")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "copied.txt"), "same\n")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", ".wh.old.txt"), "")

	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	info := MountInfo{
		Target:       absPath(target),
		SourceRef:    "snap-000001",
		SourceDigest: first.ManifestDigest,
		Mode:         MountOverlay,
		RW:           true,
		UpperDir:     upper,
		WorkDir:      filepath.Join(root, "work"),
		State:        "mounted",
	}
	if err := os.MkdirAll(info.WorkDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.writeMount(info); err != nil {
		t.Fatal(err)
	}
	changes, err := DiffMount(root, target)
	if err != nil {
		t.Fatal(err)
	}
	wantChanges := []string{
		"M /agent/workspace/file.txt",
		"A /agent/workspace/new.txt",
		"D /agent/workspace/old.txt",
	}
	if got := changeStrings(changes); !reflect.DeepEqual(got, wantChanges) {
		t.Fatalf("upperdir changes mismatch\nwant: %#v\n got: %#v", wantChanges, got)
	}
	second, err := Snapshot(root, target, SnapshotOptions{Tag: "snap-000002"})
	if err != nil {
		t.Fatal(err)
	}
	assertLayerEntries(t, root, second.ManifestDigest, []string{
		"agent/workspace/.wh.old.txt",
		"agent/workspace/file.txt",
		"agent/workspace/new.txt",
	})
	_, _, cfg, err := s.loadManifest(second.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Snapshot.DirtyBytes != int64(len("v2\n")+len("new\n")) {
		t.Fatalf("dirty bytes = %d, want modified+new bytes", cfg.Snapshot.DirtyBytes)
	}
}

func TestSnapshotFlushesRuntimeBeforeDiff(t *testing.T) {
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
	first, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001"})
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(root, "merged")
	upper := filepath.Join(root, "upper")
	work := filepath.Join(root, "work")
	mustWrite(t, filepath.Join(target, "agent", "workspace", "file.txt"), "v2\n")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "file.txt"), "v2\n")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(upper, 0o777); err != nil {
		t.Fatal(err)
	}
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.writeMount(MountInfo{
		Target:       absPath(target),
		SourceRef:    "snap-000001",
		SourceDigest: first.ManifestDigest,
		Mode:         MountOverlay,
		RW:           true,
		UpperDir:     upper,
		WorkDir:      work,
		State:        "mounted",
	}); err != nil {
		t.Fatal(err)
	}

	_, err = Snapshot(root, target, SnapshotOptions{Tag: "snap-000002"})
	if err == nil || !strings.Contains(err.Error(), "world-writable runtime directory") {
		t.Fatalf("expected runtime flush permission error, got %v", err)
	}
}

func TestWatchUsesRuntimeUpperDirtyBytes(t *testing.T) {
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
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "large.txt"), strings.Repeat("x", 2048))
	first, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001"})
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(root, "merged")
	mustWrite(t, filepath.Join(target, "agent", "workspace", "large.txt"), strings.Repeat("x", 2048))
	upper := filepath.Join(root, "upper")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "large.txt"), strings.Repeat("x", 2048))
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "tiny.txt"), "1")
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	info := MountInfo{
		Target:       absPath(target),
		SourceRef:    "snap-000001",
		SourceDigest: first.ManifestDigest,
		Mode:         MountFUSE,
		RW:           true,
		UpperDir:     upper,
		WorkDir:      filepath.Join(root, "work"),
		State:        "mounted",
	}
	if err := os.MkdirAll(info.WorkDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.writeMount(info); err != nil {
		t.Fatal(err)
	}

	result, err := Watch(root, target, WatchOptions{Iterations: 1, MaxDirtyBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Snapshots) != 0 {
		t.Fatalf("watch snapshot should use upper dirty bytes, got %#v", result.Snapshots)
	}
	if _, err := os.Stat(filepath.Join(root, ".osix", "mounts")); err != nil {
		t.Fatal(err)
	}
	dirtyPath := filepath.Join(filepath.Dir(info.WorkDir), "dirty.json")
	dirty, err := os.ReadFile(dirtyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dirty), "tiny.txt") {
		t.Fatalf("dirty index missing upper file: %s", dirty)
	}
	if strings.Contains(string(dirty), "large.txt") {
		t.Fatalf("dirty index included copied-up unchanged file: %s", dirty)
	}
}
