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

	_, _, _, _, _, err = prepareKernelMountDirs(root, "missing-snapshot", target, MountOptions{})
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

	_, _, _, _, _, err = prepareKernelMountDirs(root, "missing-snapshot", target, MountOptions{})
	if err == nil || !strings.Contains(err.Error(), "prepare lowerdir") {
		t.Fatalf("expected lowerdir restore failure, got %v", err)
	}
	if data, readErr := os.ReadFile(sentinel); readErr != nil || string(data) != "keep\n" {
		t.Fatalf("failed mount prep should preserve preexisting runtime root, data=%q err=%v", data, readErr)
	}
}

func TestCleanupFreshKernelMountDirsPreservesPreexistingRoot(t *testing.T) {
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

	freshTarget := filepath.Join(root, "fresh")
	freshRoot, _, _, _, freshExisted, err := prepareKernelMountDirs(root, "snap-000001", freshTarget, MountOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if freshExisted {
		t.Fatalf("fresh runtime root should report rootExisted=false")
	}
	cleanupFreshKernelMountDirs(freshRoot, freshExisted)
	if _, err := os.Stat(freshRoot); !os.IsNotExist(err) {
		t.Fatalf("fresh runtime root should be removed after startup failure cleanup, stat err=%v", err)
	}

	existingTarget := filepath.Join(root, "existing")
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	mountID, err := mountKey(existingTarget)
	if err != nil {
		t.Fatal(err)
	}
	existingRoot := filepath.Join(s.mountsRoot(), mountID)
	sentinel := filepath.Join(existingRoot, "upper", "sentinel.txt")
	mustWrite(t, sentinel, "keep\n")

	preparedRoot, _, _, _, existingExisted, err := prepareKernelMountDirs(root, "snap-000001", existingTarget, MountOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if preparedRoot != existingRoot || !existingExisted {
		t.Fatalf("existing runtime root detection mismatch: root=%q existed=%v", preparedRoot, existingExisted)
	}
	cleanupFreshKernelMountDirs(preparedRoot, existingExisted)
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep\n" {
		t.Fatalf("preexisting runtime root should be preserved, data=%q err=%v", data, err)
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
	lower := filepath.Join(root, "lower")
	upper := filepath.Join(root, "upper")
	work := filepath.Join(root, "work")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "copied.txt"), "same\n")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "changed.txt"), "changed\n")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(lower, 0o700); err != nil {
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
		LowerDir:     lower,
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
	dirtyPath := filepath.Join(filepath.Dir(info.WorkDir), "dirty.json")
	if err := os.WriteFile(dirtyPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dirtyPath, 0o644); err != nil {
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
	dirty, err := os.ReadFile(dirtyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dirty), "changed.txt") {
		t.Fatalf("dirty index missing changed file: %s", dirty)
	}
	if strings.Contains(string(dirty), "copied.txt") {
		t.Fatalf("dirty index included copied-up unchanged file: %s", dirty)
	}
	dirtyStat, err := os.Stat(dirtyPath)
	if err != nil {
		t.Fatal(err)
	}
	if dirtyStat.Mode().Perm() != 0o600 {
		t.Fatalf("dirty index mode = %o, want 0600", dirtyStat.Mode().Perm())
	}
}

func TestStatusRejectsMissingParentSnapshotForDirtyIndex(t *testing.T) {
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
	lower := filepath.Join(root, "lower")
	upper := filepath.Join(root, "upper")
	work := filepath.Join(root, "work")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "changed.txt"), "changed\n")
	for _, dir := range []string{target, lower, work} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	s, err := findStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.writeMount(MountInfo{
		Target:       absPath(target),
		SourceRef:    "missing-parent",
		SourceDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Mode:         MountFUSE,
		RW:           true,
		LowerDir:     lower,
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
	if err == nil || !strings.Contains(err.Error(), "load parent snapshot for dirty index") {
		t.Fatalf("expected parent snapshot dirty-index error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(work), "dirty.json")); !os.IsNotExist(err) {
		t.Fatalf("dirty index should not be written after parent load failure, stat err=%v", err)
	}
}

func TestWriteMountEnforcesPrivatePermissions(t *testing.T) {
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
	target := filepath.Join(root, "mounted")
	key, err := mountKey(target)
	if err != nil {
		t.Fatal(err)
	}
	mountDir := filepath.Join(s.mountsRoot(), key)
	if err := os.MkdirAll(mountDir, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		filepath.Join(mountDir, "mount.json"),
		filepath.Join(s.mountsRoot(), key+".json"),
	}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.writeMount(MountInfo{
		Target:       absPath(target),
		SourceRef:    "snap-000001",
		SourceDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Mode:         MountMaterialized,
		State:        "mounted",
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
		UpdatedAt:    time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 0600", path, st.Mode().Perm())
		}
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

func TestStatusRejectsMissingWorkdirMetadata(t *testing.T) {
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
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "file.txt"), "v2\n")
	if err := os.MkdirAll(target, 0o700); err != nil {
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
		State:        "mounted",
		PID:          os.Getpid(),
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
		UpdatedAt:    time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	_, err = NewMountRuntime(root, MountAuto).Status(context.Background(), target)
	if err == nil || !strings.Contains(err.Error(), "runtime metadata missing work directory") {
		t.Fatalf("expected missing workdir metadata error, got %v", err)
	}
}

func TestStatusRejectsMissingLowerdirMetadata(t *testing.T) {
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
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "file.txt"), "v2\n")
	for _, dir := range []string{target, work} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
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
	if err == nil || !strings.Contains(err.Error(), "runtime metadata missing lower directory") {
		t.Fatalf("expected missing lowerdir metadata error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(work), "dirty.json")); !os.IsNotExist(err) {
		t.Fatalf("dirty index should not be written after lowerdir validation failure, stat err=%v", err)
	}
}

func TestStatusRejectsSymlinkRuntimeDirectory(t *testing.T) {
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
	realLower := filepath.Join(root, "real-lower")
	lowerLink := filepath.Join(root, "lower-link")
	upper := filepath.Join(root, "upper")
	work := filepath.Join(root, "work")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "file.txt"), "v2\n")
	for _, dir := range []string{target, realLower, work} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(realLower, lowerLink); err != nil {
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
		LowerDir:     lowerLink,
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
	if err == nil || !strings.Contains(err.Error(), "runtime directory") || !strings.Contains(err.Error(), "not a directory") || !strings.Contains(err.Error(), "lower-link") {
		t.Fatalf("expected symlink runtime directory error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(work), "dirty.json")); !os.IsNotExist(err) {
		t.Fatalf("dirty index should not be written after symlink validation failure, stat err=%v", err)
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
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "hidden-dir", "child.txt"), "parent child\n")
	mustWrite(t, filepath.Join(fs, "agent", "workspace", "opaque-dir", "old.txt"), "opaque old\n")
	first, err := Snapshot(root, fs, SnapshotOptions{Tag: "snap-000001"})
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(root, "merged")
	mustWrite(t, filepath.Join(target, "agent", "workspace", "file.txt"), "v2\n")
	mustWrite(t, filepath.Join(target, "agent", "workspace", "new.txt"), "new\n")
	lower := filepath.Join(root, "lower")
	upper := filepath.Join(root, "upper")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "file.txt"), "v2\n")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "new.txt"), "new\n")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "copied.txt"), "same\n")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", ".wh.old.txt"), "")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "old.txt"), "hidden\n")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", ".wh.hidden-dir"), "")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "hidden-dir", "child.txt"), "hidden child\n")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "opaque-dir", ".wh..wh..opq"), "")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "opaque-dir", "new.txt"), "opaque new\n")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", ".wh.never-existed.txt"), "")
	mustWrite(t, filepath.Join(upper, ".wh..env"), "")
	mustWrite(t, filepath.Join(upper, ".osix", ".wh.mount.json"), "")
	mustWrite(t, filepath.Join(upper, "agent", "tmp", ".wh.scratch.txt"), "")
	if err := os.MkdirAll(lower, 0o700); err != nil {
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
		Mode:         MountOverlay,
		RW:           true,
		LowerDir:     lower,
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
		"D /agent/workspace/hidden-dir",
		"A /agent/workspace/new.txt",
		"D /agent/workspace/old.txt",
		"D /agent/workspace/opaque-dir",
		"A /agent/workspace/opaque-dir/new.txt",
	}
	if got := changeStrings(changes); !reflect.DeepEqual(got, wantChanges) {
		t.Fatalf("upperdir changes mismatch\nwant: %#v\n got: %#v", wantChanges, got)
	}
	dirtyData, err := os.ReadFile(filepath.Join(filepath.Dir(info.WorkDir), "dirty.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(dirtyData), "never-existed.txt") {
		t.Fatalf("dirty index included no-op whiteout: %s", dirtyData)
	}
	second, err := Snapshot(root, target, SnapshotOptions{Tag: "snap-000002"})
	if err != nil {
		t.Fatal(err)
	}
	assertLayerEntries(t, root, second.ManifestDigest, []string{
		"agent/workspace/.wh.hidden-dir",
		"agent/workspace/.wh.old.txt",
		"agent/workspace/.wh.opaque-dir",
		"agent/workspace/file.txt",
		"agent/workspace/new.txt",
		"agent/workspace/opaque-dir/new.txt",
	})
	restore := filepath.Join(root, "restore-overlay")
	if err := Restore(root, second.ManifestDigest, restore, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	assertMissing(t, filepath.Join(restore, "agent", "workspace", "opaque-dir", "old.txt"))
	assertFile(t, filepath.Join(restore, "agent", "workspace", "opaque-dir", "new.txt"), "opaque new\n")
	_, _, cfg, err := s.loadManifest(second.ManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Snapshot.DirtyBytes != int64(len("v2\n")+len("new\n")+len("opaque new\n")) {
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
	lower := filepath.Join(root, "lower")
	upper := filepath.Join(root, "upper")
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "large.txt"), strings.Repeat("x", 2048))
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "tiny.txt"), "1")
	if err := os.MkdirAll(lower, 0o700); err != nil {
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
		LowerDir:     lower,
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

func TestWatchRejectsUnsafeRuntimeDirsBeforeDirtyIndex(t *testing.T) {
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
	mustWrite(t, filepath.Join(upper, "agent", "workspace", "file.txt"), "v2\n")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
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
	info := MountInfo{
		Target:       absPath(target),
		SourceRef:    "snap-000001",
		SourceDigest: first.ManifestDigest,
		Mode:         MountFUSE,
		RW:           true,
		UpperDir:     upper,
		WorkDir:      work,
		State:        "mounted",
	}
	if err := s.writeMount(info); err != nil {
		t.Fatal(err)
	}

	result, err := Watch(root, target, WatchOptions{Iterations: 1, MaxDirtyBytes: 1024})
	if err == nil || !strings.Contains(err.Error(), "world-writable runtime directory") {
		t.Fatalf("expected watch runtime permission error, got result=%#v err=%v", result, err)
	}
	dirtyPath := filepath.Join(filepath.Dir(info.WorkDir), "dirty.json")
	if _, err := os.Stat(dirtyPath); !os.IsNotExist(err) {
		t.Fatalf("dirty index should not be written for unsafe runtime dirs, stat err=%v", err)
	}
	stateData, err := os.ReadFile(result.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stateData), "world-writable runtime directory") {
		t.Fatalf("watch state missing runtime permission error: %s", stateData)
	}
}
