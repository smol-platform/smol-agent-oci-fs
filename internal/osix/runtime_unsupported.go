//go:build !linux && !darwin

package osix

import (
	"context"
	"fmt"
)

func overlayAvailable() error {
	return fmt.Errorf("overlayfs runtime is only available on Linux")
}

func fuseAvailable() error {
	return fmt.Errorf("fuse-overlayfs runtime is not available on this platform in the current build")
}

func overlayMount(ctx context.Context, workspaceRoot, sourceRef, target string, opts MountOptions) (MountInfo, error) {
	return MountInfo{}, overlayAvailable()
}

func overlayUnmount(ctx context.Context, workspaceRoot, target string, info MountInfo, opts UnmountOptions) error {
	return overlayAvailable()
}

func fuseMount(ctx context.Context, workspaceRoot, sourceRef, target string, opts MountOptions) (MountInfo, error) {
	return MountInfo{}, fuseAvailable()
}

func fuseUnmount(ctx context.Context, workspaceRoot, target string, info MountInfo, opts UnmountOptions) error {
	return fuseAvailable()
}

func isMounted(target string) bool {
	return false
}
