package osix

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// ExtractOptions controls extraction of one path from a snapshot image.
type ExtractOptions struct {
	Force   bool
	Decrypt string
}

// ExtractResult describes the data materialized by ExtractSnapshotPath.
type ExtractResult struct {
	SourceDigest string
	SourcePath   string
	Destination  string
	Entries      int
	Files        int
	Bytes        int64
}

// SnapshotTree returns the resolved image digest and its final filesystem tree.
// The returned tree comes from the content-addressed snapshot config, so callers
// can browse an image without downloading or expanding its filesystem layers.
func SnapshotTree(workspaceRoot, ref string) (string, []TreeEntry, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return "", nil, err
	}
	digest, _, cfg, err := s.loadManifest(ref)
	if err != nil {
		return "", nil, err
	}
	return digest, append([]TreeEntry(nil), cfg.Tree...), nil
}

// ListSnapshotDirectory returns the immediate children of dir in a snapshot.
// An empty path or "/" selects the image root.
func ListSnapshotDirectory(workspaceRoot, ref, dir string) (string, []TreeEntry, error) {
	digest, tree, err := SnapshotTree(workspaceRoot, ref)
	if err != nil {
		return "", nil, err
	}
	dir, err = canonicalSnapshotSelectionPath(dir)
	if err != nil {
		return "", nil, err
	}
	if dir != "" {
		entry, ok := snapshotTreeEntry(tree, dir)
		if !ok {
			return "", nil, fmt.Errorf("path %s not found in snapshot %s", dir, digest)
		}
		if entry.Type != "dir" {
			return "", nil, fmt.Errorf("path %s is not a directory", dir)
		}
	}
	children := make([]TreeEntry, 0)
	for _, entry := range tree {
		parent := path.Dir(entry.Path)
		if parent == "." {
			parent = ""
		}
		if parent == dir {
			children = append(children, entry)
		}
	}
	return digest, children, nil
}

// ExtractSnapshotPath atomically materializes sourcePath at destination. A
// directory destination contains the selected directory's children rather than
// recreating its full path from the image root.
func ExtractSnapshotPath(workspaceRoot, ref, sourcePath, destination string, opts ExtractOptions) (ExtractResult, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return ExtractResult{}, err
	}
	digest, _, cfg, err := s.loadManifest(ref)
	if err != nil {
		return ExtractResult{}, err
	}
	sourcePath, err = canonicalSnapshotSelectionPath(sourcePath)
	if err != nil {
		return ExtractResult{}, err
	}
	if sourcePath != "" {
		if _, ok := snapshotTreeEntry(cfg.Tree, sourcePath); !ok {
			return ExtractResult{}, fmt.Errorf("path %s not found in snapshot %s", sourcePath, digest)
		}
	}
	if strings.TrimSpace(destination) == "" {
		return ExtractResult{}, fmt.Errorf("extraction destination is required")
	}
	absDestination, err := filepath.Abs(destination)
	if err != nil {
		return ExtractResult{}, err
	}

	lockValue, _ := restoreTargetLocks.LoadOrStore(absDestination, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	if err := recoverExtractTarget(absDestination); err != nil {
		return ExtractResult{}, err
	}
	if err := validateExtractTarget(absDestination, opts.Force); err != nil {
		return ExtractResult{}, err
	}
	parent := filepath.Dir(absDestination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return ExtractResult{}, err
	}
	stage, err := os.MkdirTemp(parent, "."+filepath.Base(absDestination)+".osix-extract-")
	if err != nil {
		return ExtractResult{}, err
	}
	defer removeRestoreTree(stage)

	imageRoot := filepath.Join(stage, "image")
	if err := os.Mkdir(imageRoot, 0o755); err != nil {
		return ExtractResult{}, err
	}
	if err := restoreIntoTarget(s, workspaceRoot, digest, cfg, imageRoot, RestoreOptions{Force: true, Decrypt: opts.Decrypt}); err != nil {
		return ExtractResult{}, err
	}
	if err := verifyRestoredTree(imageRoot, cfg); err != nil {
		return ExtractResult{}, err
	}

	materializedSource := imageRoot
	if sourcePath != "" {
		materializedSource = filepath.Join(imageRoot, filepath.FromSlash(sourcePath))
	}
	payload := filepath.Join(stage, "payload")
	if err := os.Rename(materializedSource, payload); err != nil {
		return ExtractResult{}, err
	}
	if err := commitExtractTarget(payload, absDestination, opts.Force); err != nil {
		return ExtractResult{}, err
	}

	result := ExtractResult{
		SourceDigest: digest,
		SourcePath:   sourcePath,
		Destination:  absDestination,
	}
	for _, entry := range cfg.Tree {
		if !snapshotPathSelected(entry.Path, sourcePath) {
			continue
		}
		result.Entries++
		if entry.Type == "file" {
			result.Files++
			result.Bytes += entry.Size
		}
	}
	return result, nil
}

func canonicalSnapshotSelectionPath(name string) (string, error) {
	name = filepath.ToSlash(strings.TrimSpace(name))
	if name == "" || name == "/" {
		return "", nil
	}
	name = strings.TrimPrefix(name, "/")
	name = strings.TrimRight(name, "/")
	if name == "" {
		return "", nil
	}
	return canonicalLayerPath(name)
}

func snapshotTreeEntry(tree []TreeEntry, name string) (TreeEntry, bool) {
	for _, entry := range tree {
		if entry.Path == name {
			return entry, true
		}
	}
	return TreeEntry{}, false
}

func snapshotPathSelected(name, selected string) bool {
	return selected == "" || name == selected || strings.HasPrefix(name, selected+"/")
}

func validateExtractTarget(target string, force bool) error {
	_, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !force {
		return fmt.Errorf("target %s already exists; pass --force to replace it", target)
	}
	return nil
}

func extractBackupPath(target string) string {
	return filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".osix-extract-backup")
}

func recoverExtractTarget(target string) error {
	backup := extractBackupPath(target)
	_, backupErr := os.Lstat(backup)
	if errors.Is(backupErr, fs.ErrNotExist) {
		return nil
	}
	if backupErr != nil {
		return backupErr
	}
	_, targetErr := os.Lstat(target)
	if errors.Is(targetErr, fs.ErrNotExist) {
		if err := os.Rename(backup, target); err != nil {
			return fmt.Errorf("recover interrupted extraction target: %w", err)
		}
		return nil
	}
	if targetErr != nil {
		return targetErr
	}
	return fmt.Errorf("extraction target %s and recovery backup %s both exist; move or remove the backup before retrying", target, backup)
}

func commitExtractTarget(payload, target string, force bool) error {
	_, statErr := os.Lstat(target)
	if errors.Is(statErr, fs.ErrNotExist) {
		return os.Rename(payload, target)
	}
	if statErr != nil {
		return statErr
	}
	if !force {
		return fmt.Errorf("target %s already exists; pass --force to replace it", target)
	}
	backup := extractBackupPath(target)
	if _, err := os.Lstat(backup); err == nil {
		return fmt.Errorf("extraction recovery backup %s already exists", backup)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Rename(target, backup); err != nil {
		return err
	}
	if err := os.Rename(payload, target); err != nil {
		if rollbackErr := os.Rename(backup, target); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("restore original extraction target: %w", rollbackErr))
		}
		return err
	}
	return removeRestoreTree(backup)
}
