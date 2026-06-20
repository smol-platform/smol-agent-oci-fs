package osix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

type MountRuntime interface {
	Mount(ctx context.Context, sourceRef string, target string, opts MountOptions) (MountInfo, error)
	Unmount(ctx context.Context, target string, opts UnmountOptions) error
	Status(ctx context.Context, target string) (MountInfo, error)
	Diff(ctx context.Context, target string) ([]Change, error)
	Snapshot(ctx context.Context, target string, opts SnapshotOptions) (SnapshotResult, error)
}

func NewMountRuntime(workspaceRoot string, mode MountMode) MountRuntime {
	return mountRuntime{workspaceRoot: workspaceRoot, requestedMode: normalizeMountMode(mode)}
}

type mountRuntime struct {
	workspaceRoot  string
	requestedMode  MountMode
	selectedMode   MountMode
	materializedRT materializedRuntime
}

func (r mountRuntime) Mount(ctx context.Context, sourceRef string, target string, opts MountOptions) (MountInfo, error) {
	if err := prepareRuntimeCache(opts.Cache); err != nil {
		return MountInfo{}, err
	}
	requested := normalizeMountMode(opts.Mode)
	if requested == MountAuto && r.requestedMode != MountAuto {
		requested = r.requestedMode
	}
	mode, err := selectMountMode(requested, opts)
	if err != nil {
		return MountInfo{}, err
	}
	r.selectedMode = mode
	switch mode {
	case MountMaterialized:
		return r.materialized().Mount(ctx, sourceRef, target, opts)
	case MountOverlay:
		return overlayMount(ctx, r.workspaceRoot, sourceRef, target, opts)
	case MountFUSE:
		return fuseMount(ctx, r.workspaceRoot, sourceRef, target, opts)
	default:
		return MountInfo{}, fmt.Errorf("unsupported mount mode %q", mode)
	}
}

func (r mountRuntime) Unmount(ctx context.Context, target string, opts UnmountOptions) error {
	info, err := r.Status(ctx, target)
	if err != nil {
		return err
	}
	switch info.Mode {
	case MountOverlay:
		return overlayUnmount(ctx, r.workspaceRoot, target, info, opts)
	case MountFUSE:
		return fuseUnmount(ctx, r.workspaceRoot, target, info, opts)
	default:
		return markUnmounted(r.workspaceRoot, target)
	}
}

func (r mountRuntime) Status(ctx context.Context, target string) (MountInfo, error) {
	s, err := findStore(r.workspaceRoot)
	if err != nil {
		return MountInfo{}, err
	}
	info, err := s.findMount(target)
	if err != nil {
		return MountInfo{}, err
	}
	detectedState := detectMountState(info)
	if info.State != detectedState {
		info.State = detectedState
		info.UpdatedAt = time.Now().UTC().Truncate(time.Second)
		if err := s.writeMount(info); err != nil {
			return MountInfo{}, err
		}
	} else {
		info.State = detectedState
	}
	if info.Mode == MountOverlay || info.Mode == MountFUSE {
		if err := validateRuntimePermissions(info); err != nil {
			return MountInfo{}, err
		}
		if _, err := rebuildDirtyIndex(s, info); err != nil {
			return MountInfo{}, err
		}
	}
	return info, nil
}

func (r mountRuntime) Diff(ctx context.Context, target string) ([]Change, error) {
	return DiffMount(r.workspaceRoot, target)
}

func (r mountRuntime) Snapshot(ctx context.Context, target string, opts SnapshotOptions) (SnapshotResult, error) {
	return Snapshot(r.workspaceRoot, target, opts)
}

func (r mountRuntime) materialized() materializedRuntime {
	return materializedRuntime{workspaceRoot: r.workspaceRoot}
}

type materializedRuntime struct {
	workspaceRoot string
}

func (r materializedRuntime) Mount(ctx context.Context, sourceRef string, target string, opts MountOptions) (MountInfo, error) {
	s, err := findStore(r.workspaceRoot)
	if err != nil {
		return MountInfo{}, err
	}
	digest, _, _, err := s.loadManifest(sourceRef)
	if err != nil {
		return MountInfo{}, err
	}
	if err := Restore(r.workspaceRoot, digest, target, RestoreOptions{Force: opts.Force, Decrypt: opts.Decrypt}); err != nil {
		return MountInfo{}, err
	}
	mountID, err := mountKey(target)
	if err != nil {
		return MountInfo{}, err
	}
	root := filepath.Join(s.mountsRoot(), mountID)
	info := MountInfo{
		Target:       absPath(target),
		SourceRef:    sourceRef,
		SourceDigest: digest,
		Mode:         MountMaterialized,
		Branch:       opts.Branch,
		RW:           opts.RW,
		UpperDir:     absPath(target),
		WorkDir:      filepath.Join(root, "work"),
		State:        "mounted",
		PID:          os.Getpid(),
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
		UpdatedAt:    time.Now().UTC().Truncate(time.Second),
	}
	if err := os.MkdirAll(info.WorkDir, 0o700); err != nil {
		return MountInfo{}, err
	}
	if err := s.writeMount(info); err != nil {
		return MountInfo{}, err
	}
	return info, nil
}

func normalizeMountMode(mode MountMode) MountMode {
	if mode == "" {
		return MountAuto
	}
	return mode
}

func selectMountMode(mode MountMode, opts MountOptions) (MountMode, error) {
	switch mode {
	case MountMaterialized:
		return MountMaterialized, nil
	case MountOverlay:
		if err := overlayAvailable(); err != nil {
			return "", err
		}
		return MountOverlay, nil
	case MountFUSE:
		if err := fuseAvailable(); err != nil {
			return "", err
		}
		return MountFUSE, nil
	case MountAuto:
		if err := overlayAvailable(); err == nil {
			return MountOverlay, nil
		}
		if err := fuseAvailable(); err == nil {
			return MountFUSE, nil
		}
		return MountMaterialized, nil
	default:
		return "", fmt.Errorf("unknown mount mode %q", mode)
	}
}

func detectMountState(info MountInfo) string {
	if info.State == "unmounted" {
		return "unmounted"
	}
	if info.Mode == MountMaterialized {
		return "mounted"
	}
	if info.PID > 0 && !processAlive(info.PID) {
		return "stale"
	}
	if isMounted(info.Target) {
		return "mounted"
	}
	return "stale"
}

func markUnmounted(workspaceRoot, target string) error {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return err
	}
	info, err := s.findMount(target)
	if err != nil {
		return err
	}
	info.State = "unmounted"
	info.UpdatedAt = time.Now().UTC().Truncate(time.Second)
	return s.writeMount(info)
}

func persistMountedRuntime(s store, root string, info MountInfo, modeMarker []byte, rollback func() error) (MountInfo, error) {
	if len(modeMarker) > 0 {
		if err := writePrivateFile(filepath.Join(root, "mode"), modeMarker); err != nil {
			return MountInfo{}, rollbackMountedRuntime(rollback, err)
		}
	}
	if err := s.writeMount(info); err != nil {
		return MountInfo{}, rollbackMountedRuntime(rollback, err)
	}
	return info, nil
}

func rollbackMountedRuntime(rollback func() error, cause error) error {
	if rollback == nil {
		return cause
	}
	if err := rollback(); err != nil {
		return errors.Join(cause, fmt.Errorf("rollback mounted runtime: %w", err))
	}
	return cause
}

func RecoverMount(workspaceRoot, target string) (MountInfo, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return MountInfo{}, err
	}
	info, err := s.findMount(target)
	if err != nil {
		return MountInfo{}, err
	}
	if err := validateRuntimePermissions(info); err != nil {
		return MountInfo{}, err
	}
	info.State = "recovered"
	info.UpdatedAt = time.Now().UTC().Truncate(time.Second)
	if _, err := rebuildDirtyIndex(s, info); err != nil {
		return MountInfo{}, err
	}
	if err := s.writeMount(info); err != nil {
		return MountInfo{}, err
	}
	return info, nil
}

func validateRuntimePermissions(info MountInfo) error {
	for _, dir := range []struct {
		name     string
		path     string
		required bool
	}{
		{name: "upper", path: info.UpperDir, required: info.Mode == MountOverlay || info.Mode == MountFUSE},
		{name: "work", path: info.WorkDir, required: info.Mode == MountOverlay || info.Mode == MountFUSE},
		{name: "lower", path: info.LowerDir, required: info.Mode == MountOverlay || info.Mode == MountFUSE},
	} {
		if strings.TrimSpace(dir.path) == "" {
			if dir.required {
				return fmt.Errorf("runtime metadata missing %s directory", dir.name)
			}
			continue
		}
		st, err := os.Lstat(dir.path)
		if err != nil {
			return fmt.Errorf("runtime directory %s is unavailable: %w", dir.path, err)
		}
		if !st.IsDir() {
			return fmt.Errorf("runtime directory %s is not a directory", dir.path)
		}
		if st.Mode().Perm()&0o002 != 0 {
			return fmt.Errorf("refusing world-writable runtime directory %s", dir.path)
		}
	}
	return nil
}

func prepareRuntimeCache(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("runtime cache %s is not a directory", path)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("refusing non-private runtime cache %s; expected permissions no broader than 0700", path)
	}
	return nil
}

func rebuildDirtyIndex(s store, info MountInfo) (int64, error) {
	if info.UpperDir == "" || info.WorkDir == "" {
		return 0, nil
	}
	tree, whiteouts, dirtyBytes, err := overlayDirtyState(s, info)
	if err != nil {
		return 0, err
	}
	paths := map[string]string{}
	for _, entry := range tree {
		paths[entry.Path] = "modified"
	}
	for _, path := range whiteouts {
		paths[path] = "deleted"
	}
	data, err := json.MarshalIndent(map[string]any{
		"dirtyBytes": dirtyBytes,
		"paths":      paths,
		"updatedAt":  time.Now().UTC().Truncate(time.Second),
	}, "", "  ")
	if err != nil {
		return 0, err
	}
	return dirtyBytes, writePrivateFile(filepath.Join(filepath.Dir(info.WorkDir), "dirty.json"), data)
}

func overlayDirtyState(s store, info MountInfo) ([]TreeEntry, []string, int64, error) {
	tree, whiteouts, _, err := scanOverlayUpper(info.UpperDir)
	if err != nil {
		return nil, nil, 0, err
	}
	if info.SourceDigest != "" {
		if _, _, parentCfg, err := s.loadManifest(info.SourceDigest); err != nil {
			return nil, nil, 0, fmt.Errorf("load parent snapshot for dirty index: %w", err)
		} else {
			tree = changedOverlayEntries(parentCfg.Tree, tree)
			whiteouts = effectiveOverlayWhiteouts(parentCfg.Tree, whiteouts)
		}
	}
	return tree, whiteouts, dirtyBytesForEntries(tree), nil
}

func dirtyBytesForTarget(workspaceRoot, target string) (int64, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return 0, err
	}
	info, err := s.findMount(target)
	if err == nil && (info.Mode == MountOverlay || info.Mode == MountFUSE) && info.UpperDir != "" {
		if err := validateRuntimePermissions(info); err != nil {
			return 0, err
		}
		return rebuildDirtyIndex(s, info)
	}
	_, dirtyBytes, err := scanTree(target)
	return dirtyBytes, err
}

func flushRuntimeForTarget(workspaceRoot, target string) error {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return err
	}
	info, err := s.findMount(target)
	if err != nil {
		return nil
	}
	if info.Mode != MountOverlay && info.Mode != MountFUSE {
		return nil
	}
	if err := validateRuntimePermissions(info); err != nil {
		return err
	}
	_, err = rebuildDirtyIndex(s, info)
	return err
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
