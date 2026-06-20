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

func TestDarwinFSKitDoctorPassesBundleAndFSType(t *testing.T) {
	root := t.TempDir()
	argsFile := filepath.Join(root, "doctor-args.txt")
	helperFile := filepath.Join(root, "osix-fskitctl")
	helperScript := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > \"$OSIX_FSKIT_HELPER_ARGS\"\n" +
		"exit 0\n"
	if err := os.WriteFile(helperFile, []byte(helperScript), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OSIX_FSKIT_HELPER_ARGS", argsFile)
	t.Setenv("OSIX_FSKIT_BUNDLE_ID", "io.example.OSIxFSKit.Extension")
	t.Setenv("OSIX_FSKIT_TYPE", "ExampleFS")

	if err := darwinFSKitDoctor(helperFile); err != nil {
		t.Fatal(err)
	}
	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(argsData)), "\n")
	assertFlagValue(t, args, "--bundle-id", "io.example.OSIxFSKit.Extension")
	assertFlagValue(t, args, "--fstype", "ExampleFS")
}

func TestDarwinFSKitMountPassesAbsoluteTargetToHelper(t *testing.T) {
	if _, err := os.Stat("/System/Library/Frameworks/FSKit.framework"); err != nil {
		t.Skipf("macOS FSKit framework unavailable: %v", err)
	}
	root := t.TempDir()
	argsFile := filepath.Join(root, "helper-args.txt")
	helperFile := filepath.Join(root, "osix-fskitctl")
	helperScript := "#!/bin/sh\n" +
		"if [ \"$1\" = doctor ]; then exit 0; fi\n" +
		"printf '%s\\n' \"$@\" > \"$OSIX_FSKIT_HELPER_ARGS\"\n" +
		"exit 42\n"
	if err := os.WriteFile(helperFile, []byte(helperScript), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OSIX_FSKIT_HELPER", helperFile)
	t.Setenv("OSIX_FSKIT_HELPER_ARGS", argsFile)
	t.Setenv("OSIX_FSKIT_TYPE", "ExampleFS")

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

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	}()

	relativeTarget := "relative-mount"
	_, err = darwinFSKitMount(context.Background(), root, "snap-000001", relativeTarget, MountOptions{Force: true, Mode: MountOverlay}, MountOverlay)
	if err == nil {
		t.Fatalf("expected fake helper mount failure")
	}
	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(argsData)), "\n")
	assertFlagValue(t, args, "--target", absPath(relativeTarget))
	assertFlagValue(t, args, "--fstype", "ExampleFS")
}

func TestDarwinFSKitUnmountUsesStoredTarget(t *testing.T) {
	root := t.TempDir()
	argsFile := filepath.Join(root, "helper-unmount-args.txt")
	helperFile := filepath.Join(root, "osix-fskitctl")
	helperScript := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > \"$OSIX_FSKIT_HELPER_ARGS\"\n" +
		"exit 0\n"
	if err := os.WriteFile(helperFile, []byte(helperScript), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OSIX_FSKIT_HELPER", helperFile)
	t.Setenv("OSIX_FSKIT_HELPER_ARGS", argsFile)

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
	storedTarget := filepath.Join(root, "stored-target")
	info := MountInfo{
		Target:       storedTarget,
		SourceRef:    "snap-000001",
		SourceDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Mode:         MountOverlay,
		State:        "mounted",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := s.writeMount(info); err != nil {
		t.Fatal(err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	}()

	if err := darwinFSKitUnmount(context.Background(), root, "relative-target", info, UnmountOptions{}); err != nil {
		t.Fatal(err)
	}
	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(argsData)), "\n")
	assertFlagValue(t, args, "--target", storedTarget)
	stored, err := s.findMount(storedTarget)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != "unmounted" {
		t.Fatalf("mount state = %q, want unmounted", stored.State)
	}
}

func assertFlagValue(t *testing.T, args []string, flag, value string) {
	t.Helper()
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			if args[i+1] != value {
				t.Fatalf("helper %s = %q, want %q; args=%#v", flag, args[i+1], value, args)
			}
			return
		}
	}
	t.Fatalf("helper args missing %s: %#v", flag, args)
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
