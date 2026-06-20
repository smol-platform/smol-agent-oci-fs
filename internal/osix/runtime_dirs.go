package osix

import (
	"fmt"
	"os"
	"path/filepath"
)

func prepareKernelMountDirs(workspaceRoot, sourceRef, target string, opts MountOptions) (root, lower, upper, work string, err error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return "", "", "", "", err
	}
	mountID, err := mountKey(target)
	if err != nil {
		return "", "", "", "", err
	}
	root = filepath.Join(s.mountsRoot(), mountID)
	lower = filepath.Join(root, "lower", "000000")
	upper = filepath.Join(root, "upper")
	work = filepath.Join(root, "work")
	for _, dir := range []string{lower, upper, work} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", "", "", "", err
		}
	}
	if err := Restore(workspaceRoot, sourceRef, lower, RestoreOptions{Force: true, Decrypt: opts.Decrypt}); err != nil {
		return "", "", "", "", fmt.Errorf("prepare lowerdir: %w", err)
	}
	return root, lower, upper, work, nil
}
