//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/smol-platform/smol-agent-oci-fs/internal/osix"
)

func TestLazyFUSECLIHelperRemoteRuntimeIntegrationLinux(t *testing.T) {
	if _, err := os.Stat("/dev/fuse"); err != nil {
		t.Skipf("/dev/fuse unavailable: %v", err)
	}
	if _, err := exec.LookPath("fusermount3"); err != nil {
		if _, err := exec.LookPath("fusermount"); err != nil {
			t.Skipf("fusermount unavailable: %v", err)
		}
	}

	bin := buildOSIxBinary(t)
	reg := newCLIFakeRegistry()
	server := httptest.NewServer(reg)
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo := u.Host + "/acme/lazy-fuse-cli-agent-state"

	source := t.TempDir()
	runOSIxBinary(t, bin, source, "init", "example/base:latest", "--name", "agent", "--state", repo, "--mount", filepath.Join(source, "agentfs"))
	mustWriteCLI(t, filepath.Join(source, "agentfs", "agent", "workspace", "notes.md"), "lazy fuse cli\n")
	runOSIxBinary(t, bin, source, "snapshot", filepath.Join(source, "agentfs"), "--tag", "snap-lazy-fuse-cli", "--also-tag", "release")
	refs, err := osix.Refs(source)
	if err != nil {
		t.Fatal(err)
	}
	var manifestDigest string
	for _, ref := range refs {
		if ref.Name == "release" {
			manifestDigest = ref.Digest
			break
		}
	}
	if manifestDigest == "" {
		t.Fatalf("release ref not found: %#v", refs)
	}
	layerDigest := readCLISnapshotLayerDigest(t, source, manifestDigest)
	runOSIxBinary(t, bin, source, "push", "release")
	reg.blobGets[layerDigest] = 0

	dest := t.TempDir()
	runOSIxBinary(t, bin, dest, "init", "example/base:latest", "--name", "agent", "--state", repo, "--mount", filepath.Join(dest, "agentfs"))
	mountDir := filepath.Join(dest, "mount")
	runOSIxBinary(t, bin, dest, "mount", repo+":release", mountDir, "--mode", "fuse", "--force", "--lazy", "--rw=false", "--quiet")

	var info osix.MountInfo
	defer func() {
		_ = runOSIxBinaryAllowError(bin, dest, "unmount", mountDir, "--force")
	}()
	info, err = osix.NewMountRuntime(dest, osix.MountAuto).Status(context.Background(), mountDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.PID == 0 || info.PID == os.Getpid() {
		t.Fatalf("expected external lazy FUSE helper PID, got %d", info.PID)
	}
	if info.RW {
		t.Fatal("lazy FUSE CLI mount metadata RW = true")
	}
	if info.State != "mounted" {
		t.Fatalf("lazy FUSE CLI mount state = %q, want mounted", info.State)
	}
	if !processRunning(info.PID) {
		t.Fatalf("lazy FUSE helper pid %d is not running", info.PID)
	}
	assertMissingCLI(t, filepath.Join(info.LowerDir, "agent", "workspace", "notes.md"))
	if got := reg.blobGets[layerDigest]; got != 0 {
		t.Fatalf("lazy FUSE CLI mount fetched layer %s before file read: %d", layerDigest, got)
	}

	waitForCLIPath(t, filepath.Join(mountDir, "agent", "workspace", "notes.md"))
	data, err := os.ReadFile(filepath.Join(mountDir, "agent", "workspace", "notes.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "lazy fuse cli\n" {
		t.Fatalf("unexpected mounted file data: %q", data)
	}
	if got := reg.blobGets[layerDigest]; got != 1 {
		t.Fatalf("lazy FUSE CLI read fetched layer %s %d times, want 1", layerDigest, got)
	}
	assertMissingCLI(t, filepath.Join(info.LowerDir, "agent", "workspace", "notes.md"))

	runOSIxBinary(t, bin, dest, "unmount", mountDir, "--force")
	waitForProcessExit(t, info.PID)
}

func TestLazyFUSEWritableCLIHelperRemoteRuntimeIntegrationLinux(t *testing.T) {
	if _, err := os.Stat("/dev/fuse"); err != nil {
		t.Skipf("/dev/fuse unavailable: %v", err)
	}
	if _, err := exec.LookPath("fusermount3"); err != nil {
		if _, err := exec.LookPath("fusermount"); err != nil {
			t.Skipf("fusermount unavailable: %v", err)
		}
	}

	bin := buildOSIxBinary(t)
	reg := newCLIFakeRegistry()
	server := httptest.NewServer(reg)
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	repo := u.Host + "/acme/lazy-fuse-writable-cli-agent-state"

	source := t.TempDir()
	runOSIxBinary(t, bin, source, "init", "example/base:latest", "--name", "agent", "--state", repo, "--mount", filepath.Join(source, "agentfs"))
	mustWriteCLI(t, filepath.Join(source, "agentfs", "agent", "workspace", "file.txt"), "v1\n")
	mustWriteCLI(t, filepath.Join(source, "agentfs", "agent", "workspace", "remove.txt"), "remove\n")
	runOSIxBinary(t, bin, source, "snapshot", filepath.Join(source, "agentfs"), "--tag", "snap-lazy-fuse-writable-cli", "--also-tag", "release")
	refs, err := osix.Refs(source)
	if err != nil {
		t.Fatal(err)
	}
	var manifestDigest string
	for _, ref := range refs {
		if ref.Name == "release" {
			manifestDigest = ref.Digest
			break
		}
	}
	if manifestDigest == "" {
		t.Fatalf("release ref not found: %#v", refs)
	}
	layerDigest := readCLISnapshotLayerDigest(t, source, manifestDigest)
	runOSIxBinary(t, bin, source, "push", "release")
	reg.blobGets[layerDigest] = 0

	dest := t.TempDir()
	runOSIxBinary(t, bin, dest, "init", "example/base:latest", "--name", "agent", "--state", repo, "--mount", filepath.Join(dest, "agentfs"))
	mountDir := filepath.Join(dest, "mount")
	runOSIxBinary(t, bin, dest, "mount", repo+":release", mountDir, "--mode", "fuse", "--force", "--lazy", "--quiet")

	var info osix.MountInfo
	defer func() {
		_ = runOSIxBinaryAllowError(bin, dest, "unmount", mountDir, "--force")
	}()
	info, err = osix.NewMountRuntime(dest, osix.MountAuto).Status(context.Background(), mountDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.PID == 0 || info.PID == os.Getpid() {
		t.Fatalf("expected external lazy FUSE helper PID, got %d", info.PID)
	}
	if !info.RW {
		t.Fatal("lazy writable FUSE CLI mount metadata RW = false")
	}
	if !processRunning(info.PID) {
		t.Fatalf("lazy FUSE helper pid %d is not running", info.PID)
	}
	assertMissingCLI(t, filepath.Join(info.LowerDir, "agent", "workspace", "file.txt"))
	if got := reg.blobGets[layerDigest]; got != 0 {
		t.Fatalf("lazy writable FUSE CLI mount fetched layer %s before copy-up: %d", layerDigest, got)
	}

	mustWriteCLI(t, filepath.Join(mountDir, "agent", "workspace", "file.txt"), "v2\n")
	mustWriteCLI(t, filepath.Join(mountDir, "agent", "workspace", "new.txt"), "new\n")
	if err := os.Remove(filepath.Join(mountDir, "agent", "workspace", "remove.txt")); err != nil {
		t.Fatal(err)
	}
	if got := reg.blobGets[layerDigest]; got != 1 {
		t.Fatalf("lazy writable FUSE CLI copy-up fetched layer %s %d times, want 1", layerDigest, got)
	}
	assertFileCLI(t, filepath.Join(info.UpperDir, "agent", "workspace", "file.txt"), "v2\n")
	assertFileCLI(t, filepath.Join(info.UpperDir, "agent", "workspace", "new.txt"), "new\n")
	if _, err := os.Stat(filepath.Join(info.UpperDir, "agent", "workspace", ".wh.remove.txt")); err != nil {
		t.Fatalf("expected upper whiteout: %v", err)
	}
	assertMissingCLI(t, filepath.Join(info.LowerDir, "agent", "workspace", "file.txt"))

	runOSIxBinary(t, bin, dest, "snapshot", mountDir, "--tag", "lazy-fuse-writable-cli-2")
	restore := filepath.Join(dest, "restore")
	runOSIxBinary(t, bin, dest, "restore", "lazy-fuse-writable-cli-2", restore)
	assertFileCLI(t, filepath.Join(restore, "agent", "workspace", "file.txt"), "v2\n")
	assertFileCLI(t, filepath.Join(restore, "agent", "workspace", "new.txt"), "new\n")
	assertMissingCLI(t, filepath.Join(restore, "agent", "workspace", "remove.txt"))

	runOSIxBinary(t, bin, dest, "unmount", mountDir, "--force")
	waitForProcessExit(t, info.PID)
}

func buildOSIxBinary(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "osix")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/osix")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build osix binary: %v\n%s", err, out)
	}
	return bin
}

func runOSIxBinary(t *testing.T, bin, cwd string, args ...string) {
	t.Helper()
	if out, err := runOSIxBinaryOutput(bin, cwd, args...); err != nil {
		t.Fatalf("osix %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func runOSIxBinaryAllowError(bin, cwd string, args ...string) error {
	_, err := runOSIxBinaryOutput(bin, cwd, args...)
	return err
}

func runOSIxBinaryOutput(bin, cwd string, args ...string) ([]byte, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = cwd
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.Bytes(), err
}

func processRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		process, err := os.FindProcess(pid)
		if err != nil {
			return
		}
		err = process.Signal(syscall.Signal(0))
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d still running after unmount; signal(0)=%v", pid, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func waitForCLIPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
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

func assertMissingCLI(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Fatalf("expected %s to be absent", path)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", path, err)
	}
}
