package osix

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func prepareKernelMountDirs(workspaceRoot, sourceRef, target string, opts MountOptions) (root, lower, upper, work string, rootExisted bool, err error) {
	return prepareKernelMountDirsWithRestore(workspaceRoot, sourceRef, target, opts, true)
}

func prepareKernelMountDirsWithRestore(workspaceRoot, sourceRef, target string, opts MountOptions, restoreLower bool) (root, lower, upper, work string, rootExisted bool, err error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return "", "", "", "", false, err
	}
	mountID, err := mountKey(target)
	if err != nil {
		return "", "", "", "", false, err
	}
	root = filepath.Join(s.mountsRoot(), mountID)
	lower = filepath.Join(root, "lower", "000000")
	upper = filepath.Join(root, "upper")
	work = filepath.Join(root, "work")
	rootExisted = pathExists(root)
	if rootExisted {
		if err := validateKernelMountRoot(root); err != nil {
			return "", "", "", "", rootExisted, err
		}
	}
	cleanupRoot := root
	defer func() {
		if err != nil && !rootExisted {
			_ = os.RemoveAll(cleanupRoot)
		}
	}()
	for _, dir := range []string{lower, upper, work} {
		if err := validateKernelMountPath(root, dir); err != nil {
			return "", "", "", "", rootExisted, err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", "", "", "", rootExisted, err
		}
	}
	if restoreLower {
		if err := Restore(workspaceRoot, sourceRef, lower, RestoreOptions{Force: true, Decrypt: opts.Decrypt}); err != nil {
			return "", "", "", "", rootExisted, fmt.Errorf("prepare lowerdir: %w", err)
		}
	}
	return root, lower, upper, work, rootExisted, nil
}

func cleanupFreshKernelMountDirs(root string, rootExisted bool) {
	if root != "" && !rootExisted {
		_ = os.RemoveAll(root)
	}
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil || !os.IsNotExist(err)
}

func validateKernelMountRoot(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("runtime root %s is not a directory", root)
	}
	return nil
}

func validateKernelMountPath(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	current := root
	if rel == "." {
		return validateKernelMountRoot(root)
	}
	for _, part := range splitPath(rel) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("runtime path %s contains non-directory component %s", path, current)
		}
	}
	return nil
}

func splitPath(path string) []string {
	var parts []string
	for _, part := range strings.Split(path, string(os.PathSeparator)) {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}
