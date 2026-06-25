//go:build !linux

package osix

import (
	"context"
	"fmt"
)

func lazyFuseAvailable() error {
	return fmt.Errorf("native lazy FUSE runtime is only available on Linux")
}

func RunLazyFUSEServer(ctx context.Context, workspaceRoot, sourceRef, target, upper string, readOnly bool, opts ReadFileOptions) error {
	return lazyFuseAvailable()
}
