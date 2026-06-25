package osix

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type SnapshotLowerStore struct {
	workspaceRoot string
	ref           string
	digest        string
	tree          []TreeEntry
	opts          ReadFileOptions
}

func OpenSnapshotLowerStore(workspaceRoot, ref string, opts ReadFileOptions) (*SnapshotLowerStore, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return nil, err
	}
	digest, _, cfg, err := s.loadManifest(ref)
	if err != nil {
		return nil, err
	}
	return &SnapshotLowerStore{
		workspaceRoot: workspaceRoot,
		ref:           ref,
		digest:        digest,
		tree:          cfg.Tree,
		opts:          opts,
	}, nil
}

func (s *SnapshotLowerStore) Digest() string {
	return s.digest
}

func (s *SnapshotLowerStore) Ref() string {
	return s.ref
}

func (s *SnapshotLowerStore) Lookup(name string) (TreeEntry, bool, error) {
	name, err := canonicalLowerStorePath(name)
	if err != nil {
		return TreeEntry{}, false, err
	}
	if name == "" {
		return TreeEntry{Path: "", Type: "dir", Mode: 0o755}, true, nil
	}
	for _, entry := range s.tree {
		if entry.Path == name {
			return entry, true, nil
		}
	}
	return TreeEntry{}, false, nil
}

func (s *SnapshotLowerStore) ReadDir(name string) ([]TreeEntry, error) {
	name, err := canonicalLowerStorePath(name)
	if err != nil {
		return nil, err
	}
	if name != "" {
		entry, found, err := s.Lookup(name)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("directory %s not found", name)
		}
		if entry.Type != "dir" {
			return nil, fmt.Errorf("%s is not a directory", name)
		}
	}
	children := map[string]TreeEntry{}
	for _, entry := range s.tree {
		parent, base := lowerStoreParent(entry.Path)
		if parent != name || base == "" {
			continue
		}
		children[base] = entry
	}
	out := make([]TreeEntry, 0, len(children))
	for _, entry := range children {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (s *SnapshotLowerStore) ReadFile(name string) ([]byte, error) {
	name, err := canonicalLowerStorePath(name)
	if err != nil {
		return nil, err
	}
	if err := s.requireReadableFile(name); err != nil {
		return nil, err
	}
	return ReadSnapshotFile(s.workspaceRoot, s.digest, name, s.opts)
}

func (s *SnapshotLowerStore) ReadFileRange(name string, offset, length int64) ([]byte, error) {
	name, err := canonicalLowerStorePath(name)
	if err != nil {
		return nil, err
	}
	if err := s.requireReadableFile(name); err != nil {
		return nil, err
	}
	return ReadSnapshotFileRange(s.workspaceRoot, s.digest, name, offset, length, s.opts)
}

func (s *SnapshotLowerStore) requireReadableFile(name string) error {
	if name == "" {
		return fmt.Errorf("path is a directory")
	}
	entry, found, err := s.Lookup(name)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("file %s not found", name)
	}
	if entry.Type != "file" && entry.Type != "symlink" {
		return fmt.Errorf("%s is not a file", name)
	}
	return nil
}

func canonicalLowerStorePath(name string) (string, error) {
	name = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(name)), "/")
	if name == "" || name == "." {
		return "", nil
	}
	return canonicalLayerPath(name)
}

func lowerStoreParent(name string) (string, string) {
	dir, base := filepath.Split(filepath.ToSlash(name))
	dir = strings.TrimSuffix(dir, "/")
	return dir, base
}
