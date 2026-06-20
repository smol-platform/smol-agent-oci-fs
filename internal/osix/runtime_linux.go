//go:build linux

package osix

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func overlayAvailable() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("overlayfs requires root or mount namespace privileges")
	}
	if _, err := os.Stat("/proc/filesystems"); err == nil {
		data, _ := os.ReadFile("/proc/filesystems")
		if !strings.Contains(string(data), "overlay") {
			return fmt.Errorf("kernel overlayfs support not listed in /proc/filesystems")
		}
	}
	return nil
}

func fuseAvailable() error {
	if _, err := exec.LookPath("fuse-overlayfs"); err != nil {
		return fmt.Errorf("fuse-overlayfs not found in PATH")
	}
	if _, err := os.Stat("/dev/fuse"); err != nil {
		return fmt.Errorf("/dev/fuse unavailable: %w", err)
	}
	return nil
}

func overlayMount(ctx context.Context, workspaceRoot, sourceRef, target string, opts MountOptions) (MountInfo, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return MountInfo{}, err
	}
	digest, err := s.resolveRef(sourceRef)
	if err != nil {
		return MountInfo{}, err
	}
	root, lower, upper, work, rootExisted, err := prepareKernelMountDirs(workspaceRoot, sourceRef, target, opts)
	if err != nil {
		return MountInfo{}, err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		cleanupFreshKernelMountDirs(root, rootExisted)
		return MountInfo{}, err
	}
	options := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lower, upper, work)
	if err := unix.Mount("overlay", target, "overlay", 0, options); err != nil {
		cleanupFreshKernelMountDirs(root, rootExisted)
		return MountInfo{}, fmt.Errorf("overlay mount: %w", err)
	}
	info := MountInfo{
		Target:       absPath(target),
		SourceRef:    sourceRef,
		SourceDigest: digest,
		Mode:         MountOverlay,
		Branch:       opts.Branch,
		RW:           opts.RW,
		UpperDir:     upper,
		WorkDir:      work,
		LowerDir:     lower,
		State:        "mounted",
		PID:          os.Getpid(),
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
		UpdatedAt:    time.Now().UTC().Truncate(time.Second),
	}
	return persistMountedRuntime(s, root, info, []byte("overlay\n"), func() error {
		return unix.Unmount(target, 0)
	})
}

func overlayUnmount(ctx context.Context, workspaceRoot, target string, info MountInfo, opts UnmountOptions) error {
	flags := 0
	if opts.Force {
		flags = unix.MNT_FORCE
	}
	if err := unix.Unmount(target, flags); err != nil && !opts.Force {
		return err
	}
	return markUnmounted(workspaceRoot, target)
}

func fuseMount(ctx context.Context, workspaceRoot, sourceRef, target string, opts MountOptions) (MountInfo, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return MountInfo{}, err
	}
	digest, err := s.resolveRef(sourceRef)
	if err != nil {
		return MountInfo{}, err
	}
	root, lower, upper, work, rootExisted, err := prepareKernelMountDirs(workspaceRoot, sourceRef, target, opts)
	if err != nil {
		return MountInfo{}, err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		cleanupFreshKernelMountDirs(root, rootExisted)
		return MountInfo{}, err
	}
	cmd := exec.CommandContext(ctx, "fuse-overlayfs", "-o", fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lower, upper, work), target)
	if err := cmd.Start(); err != nil {
		cleanupFreshKernelMountDirs(root, rootExisted)
		return MountInfo{}, fmt.Errorf("start fuse-overlayfs: %w", err)
	}
	pid := cmd.Process.Pid
	_ = os.WriteFile(filepath.Join(root, "fuse-overlayfs.pid"), []byte(strconv.Itoa(pid)+"\n"), 0o600)
	go cmd.Wait()
	info := MountInfo{
		Target:       absPath(target),
		SourceRef:    sourceRef,
		SourceDigest: digest,
		Mode:         MountFUSE,
		Branch:       opts.Branch,
		RW:           opts.RW,
		UpperDir:     upper,
		WorkDir:      work,
		LowerDir:     lower,
		State:        "mounted",
		PID:          pid,
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
		UpdatedAt:    time.Now().UTC().Truncate(time.Second),
	}
	return persistMountedRuntime(s, root, info, []byte("fuse\nfuse-overlayfs\n"), func() error {
		if path, err := exec.LookPath("fusermount3"); err == nil {
			_ = exec.CommandContext(ctx, path, "-u", target).Run()
		} else if path, err := exec.LookPath("fusermount"); err == nil {
			_ = exec.CommandContext(ctx, path, "-u", target).Run()
		} else {
			_ = unix.Unmount(target, 0)
		}
		if p, err := os.FindProcess(pid); err == nil {
			return p.Kill()
		}
		return nil
	})
}

func fuseUnmount(ctx context.Context, workspaceRoot, target string, info MountInfo, opts UnmountOptions) error {
	if path, err := exec.LookPath("fusermount3"); err == nil {
		_ = exec.CommandContext(ctx, path, "-u", target).Run()
	} else if path, err := exec.LookPath("fusermount"); err == nil {
		_ = exec.CommandContext(ctx, path, "-u", target).Run()
	} else {
		_ = unix.Unmount(target, 0)
	}
	if info.PID > 0 {
		if p, err := os.FindProcess(info.PID); err == nil && opts.Force {
			_ = p.Kill()
		}
	}
	return markUnmounted(workspaceRoot, target)
}

func isMounted(target string) bool {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	target = absPath(target)
	return strings.Contains(string(data), " "+target+" ")
}
