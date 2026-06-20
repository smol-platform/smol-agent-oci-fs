//go:build darwin

package osix

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const defaultDarwinFSKitBundleID = "io.github.smol-platform.smol-agent-oci-fs.fskit.extension"

func overlayAvailable() error {
	return darwinFSKitAvailable()
}

func fuseAvailable() error {
	return darwinFSKitAvailable()
}

func overlayMount(ctx context.Context, workspaceRoot, sourceRef, target string, opts MountOptions) (MountInfo, error) {
	return darwinFSKitMount(ctx, workspaceRoot, sourceRef, target, opts, MountOverlay)
}

func overlayUnmount(ctx context.Context, workspaceRoot, target string, info MountInfo, opts UnmountOptions) error {
	return darwinFSKitUnmount(ctx, workspaceRoot, target, info, opts)
}

func fuseMount(ctx context.Context, workspaceRoot, sourceRef, target string, opts MountOptions) (MountInfo, error) {
	return darwinFSKitMount(ctx, workspaceRoot, sourceRef, target, opts, MountFUSE)
}

func fuseUnmount(ctx context.Context, workspaceRoot, target string, info MountInfo, opts UnmountOptions) error {
	return darwinFSKitUnmount(ctx, workspaceRoot, target, info, opts)
}

func darwinFSKitAvailable() error {
	if _, err := os.Stat("/System/Library/Frameworks/FSKit.framework"); err != nil {
		return fmt.Errorf("macOS native runtime requires FSKit; use macOS 15.4 or newer: %w", err)
	}
	helper, err := darwinFSKitHelper()
	if err != nil {
		return err
	}
	if err := darwinFSKitDoctor(helper); err != nil {
		return err
	}
	return nil
}

func darwinFSKitHelper() (string, error) {
	if path := strings.TrimSpace(os.Getenv("OSIX_FSKIT_HELPER")); path != "" {
		if err := validateDarwinFSKitHelper(path); err != nil {
			return "", fmt.Errorf("OSIX_FSKIT_HELPER points to unavailable helper %q: %w", path, err)
		}
		return path, nil
	}
	if path, err := exec.LookPath("osix-fskitctl"); err == nil {
		return path, nil
	}
	for _, path := range []string{
		filepath.Join(".osix-tools", "bin", "osix-fskitctl"),
		filepath.Join("macos", "OSIxFSKit", ".build", "release", "osix-fskitctl"),
		filepath.Join("macos", "OSIxFSKit", ".build", "debug", "osix-fskitctl"),
	} {
		if err := validateDarwinFSKitHelper(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("macOS native runtime requires osix-fskitctl; build it with ./scripts/build-macos-fskit.sh or set OSIX_FSKIT_HELPER")
}

func validateDarwinFSKitHelper(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("is a directory")
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("is not executable")
	}
	return nil
}

func darwinFSKitBundleID() string {
	if bundleID := strings.TrimSpace(os.Getenv("OSIX_FSKIT_BUNDLE_ID")); bundleID != "" {
		return bundleID
	}
	return defaultDarwinFSKitBundleID
}

func darwinFSKitDoctor(helper string) error {
	var stderr bytes.Buffer
	cmd := exec.Command(helper, "doctor", "--bundle-id", darwinFSKitBundleID())
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("macOS native runtime requires an enabled FSKit extension: %s", msg)
	}
	return nil
}

func darwinFSKitMount(ctx context.Context, workspaceRoot, sourceRef, target string, opts MountOptions, mode MountMode) (MountInfo, error) {
	if err := darwinFSKitAvailable(); err != nil {
		return MountInfo{}, err
	}
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
	helper, err := darwinFSKitHelper()
	if err != nil {
		cleanupFreshKernelMountDirs(root, rootExisted)
		return MountInfo{}, err
	}
	args := []string{
		"mount",
		"--bundle-id", darwinFSKitBundleID(),
		"--workspace-root", absPath(workspaceRoot),
		"--source-ref", sourceRef,
		"--source-digest", digest,
		"--target", target,
		"--lower", lower,
		"--upper", upper,
		"--work", work,
		"--mode", string(mode),
	}
	cmd := exec.CommandContext(ctx, helper, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		cleanupFreshKernelMountDirs(root, rootExisted)
		return MountInfo{}, fmt.Errorf("mount macOS FSKit filesystem: %s: %w", strings.TrimSpace(string(out)), err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	info := MountInfo{
		Target:       absPath(target),
		SourceRef:    sourceRef,
		SourceDigest: digest,
		Mode:         mode,
		Branch:       opts.Branch,
		RW:           opts.RW,
		UpperDir:     upper,
		WorkDir:      work,
		LowerDir:     lower,
		State:        "mounted",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	return persistMountedRuntime(s, root, info, []byte(string(mode)+"\nfskit\n"+darwinFSKitBundleID()+"\n"), func() error {
		args := []string{"unmount", "--target", target, "--force"}
		if out, runErr := exec.CommandContext(ctx, helper, args...).CombinedOutput(); runErr != nil {
			return fmt.Errorf("unmount macOS FSKit filesystem after metadata failure: %s: %w", strings.TrimSpace(string(out)), runErr)
		}
		return nil
	})
}

func darwinFSKitUnmount(ctx context.Context, workspaceRoot, target string, info MountInfo, opts UnmountOptions) error {
	helper, err := darwinFSKitHelper()
	if err == nil {
		args := []string{"unmount", "--target", target}
		if opts.Force {
			args = append(args, "--force")
		}
		if out, runErr := exec.CommandContext(ctx, helper, args...).CombinedOutput(); runErr == nil {
			return markUnmounted(workspaceRoot, target)
		} else {
			err = fmt.Errorf("unmount macOS FSKit filesystem: %s: %w", strings.TrimSpace(string(out)), runErr)
		}
	}
	if opts.Force {
		_ = exec.CommandContext(ctx, "diskutil", "unmount", "force", target).Run()
		return markUnmounted(workspaceRoot, target)
	}
	return err
}

func isMounted(target string) bool {
	out, err := exec.Command("mount").Output()
	if err != nil {
		return false
	}
	target = absPath(target)
	return strings.Contains(string(out), " on "+target+" ")
}
