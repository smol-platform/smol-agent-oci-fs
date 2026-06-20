package osix

import (
	"fmt"
	"os"
	"path/filepath"
)

func prepareKernelMountDirs(workspaceRoot, sourceRef, target string, opts MountOptions) (root, lower, upper, work string, rootExisted bool, err error) {
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
	cleanupRoot := root
	defer func() {
		if err != nil && !rootExisted {
			_ = os.RemoveAll(cleanupRoot)
		}
	}()
	for _, dir := range []string{lower, upper, work} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", "", "", "", rootExisted, err
		}
	}
	if err := Restore(workspaceRoot, sourceRef, lower, RestoreOptions{Force: true, Decrypt: opts.Decrypt}); err != nil {
		return "", "", "", "", rootExisted, fmt.Errorf("prepare lowerdir: %w", err)
	}
	return root, lower, upper, work, rootExisted, nil
}

func cleanupFreshKernelMountDirs(root string, rootExisted bool) {
	if root != "" && !rootExisted {
		_ = os.RemoveAll(root)
	}
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !os.IsNotExist(err)
}
