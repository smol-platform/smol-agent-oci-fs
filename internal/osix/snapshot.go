package osix

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

func Snapshot(workspaceRoot, target string, opts SnapshotOptions) (SnapshotResult, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return SnapshotResult{}, err
	}
	ws, err := readWorkspaceConfig(s.configPath())
	if err != nil {
		return SnapshotResult{}, fmt.Errorf("read workspace config: %w", err)
	}
	if err := ValidateAgentState(target, PolicyOptions{SecretScan: opts.SecretScan}); err != nil {
		return SnapshotResult{}, err
	}

	var parent *ParentRef
	var parentTree []TreeEntry
	var sequence int64 = 1
	parentDigest := ""
	var mountInfo MountInfo
	mounted := false
	if !opts.Checkpoint {
		if info, err := s.findMount(target); err == nil && info.SourceDigest != "" {
			mountInfo = info
			mounted = true
			parentDigest = info.SourceDigest
		} else if digest, err := s.resolveRef("latest"); err == nil {
			parentDigest = digest
		}
	}
	if parentDigest != "" && !opts.Checkpoint {
		_, _, parentCfg, err := s.loadManifest(parentDigest)
		if err != nil {
			return SnapshotResult{}, err
		}
		parent = &ParentRef{Snapshot: parentCfg.Snapshot.ID, Digest: parentDigest}
		parentTree = parentCfg.Tree
		sequence = parentCfg.Snapshot.Sequence + 1
	}

	if mounted && (mountInfo.Mode == MountOverlay || mountInfo.Mode == MountFUSE) {
		if err := flushRuntimeForTarget(workspaceRoot, target); err != nil {
			return SnapshotResult{}, err
		}
	}

	tree, dirtyBytes, err := scanTree(target)
	if err != nil {
		return SnapshotResult{}, err
	}
	layerRoot := target
	layerEntries, whiteouts := diffLayerEntries(parentTree, tree)
	if mounted && (mountInfo.Mode == MountOverlay || mountInfo.Mode == MountFUSE) && mountInfo.UpperDir != "" {
		upperEntries, upperWhiteouts, _, err := scanOverlayUpper(mountInfo.UpperDir)
		if err != nil {
			return SnapshotResult{}, err
		}
		layerRoot = mountInfo.UpperDir
		layerEntries = changedOverlayEntries(parentTree, upperEntries)
		whiteouts = mergeWhiteouts(
			effectiveOverlayWhiteouts(parentTree, upperWhiteouts),
			typeChangeWhiteouts(parentTree, layerEntries),
		)
		dirtyBytes = dirtyBytesForEntries(layerEntries)
	}
	layerData, err := makeLayer(layerRoot, layerEntries, whiteouts)
	if err != nil {
		return SnapshotResult{}, err
	}
	encrypt := opts.Encrypt
	if encrypt == "" {
		encrypt = ws.Encrypt
	}
	encryptionAnnotations := map[string]string(nil)
	if encrypt != "" {
		layerData, encryptionAnnotations, err = encryptLayer(layerData, encrypt)
		if err != nil {
			return SnapshotResult{}, err
		}
	}
	layerDesc, err := s.writeBlob(layerData)
	if err != nil {
		return SnapshotResult{}, err
	}
	layerDesc.MediaType = MediaTypeLayer
	if encrypt != "" {
		layerDesc.MediaType = MediaTypeLayerEnc
	}
	layerDesc.Annotations = map[string]string{
		"com.osix.layer.kind":     "filesystem-diff",
		"com.osix.diff.algorithm": "overlayfs-whiteout-v1",
	}
	for key, value := range encryptionAnnotations {
		layerDesc.Annotations[key] = value
	}

	now := time.Now().UTC().Truncate(time.Second)
	snapshotID := opts.Tag
	if snapshotID == "" {
		snapshotID = fmt.Sprintf("snap-%06d", sequence)
	}
	createdBy := ""
	if u, err := user.Current(); err == nil {
		createdBy = u.Username
	}
	cfg := AgentConfig{
		OSIxVersion: Version,
		Agent: AgentIdentity{
			ID:        ws.Name,
			Name:      ws.Name,
			CreatedBy: createdBy,
		},
		Base: BaseRef{
			Image:  ws.Base,
			Digest: ws.BaseDigest,
		},
		Parent: parent,
		Runtime: RuntimeConfig{
			WorkingDir: "/agent/workspace",
			UID:        os.Getuid(),
			GID:        os.Getgid(),
		},
		StateRoots: []StateRoot{
			{Path: "/agent/workspace", Mode: "cow"},
			{Path: "/agent/memory", Mode: "versioned"},
			{Path: "/agent/skills", Mode: "signed-versioned"},
		},
		Snapshot: SnapshotMeta{
			ID:         snapshotID,
			Sequence:   sequence,
			CreatedAt:  now,
			Reason:     "manual",
			Message:    opts.Message,
			DirtyBytes: dirtyBytes,
		},
		Integrity: Integrity{
			MTreeDigest: digestTree(tree),
			LayerDigest: layerDesc.Digest,
		},
		Tree: tree,
	}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return SnapshotResult{}, err
	}
	cfgDesc, err := s.writeBlob(cfgData)
	if err != nil {
		return SnapshotResult{}, err
	}
	cfgDesc.MediaType = MediaTypeConfig

	annotations := map[string]string{
		"com.osix.snapshot.id": snapshotID,
		"com.osix.agent.id":    ws.Name,
		"com.osix.created":     now.Format(time.RFC3339),
		"com.osix.kind":        "delta",
		"com.osix.branch":      ws.DefaultBranch,
	}
	if opts.Checkpoint {
		annotations["com.osix.kind"] = "checkpoint"
	}
	if parent != nil {
		annotations["com.osix.parent"] = parent.Digest
	}
	manifest := Manifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCIManifest,
		ArtifactType:  ArtifactTypeSnapshot,
		Config:        cfgDesc,
		Layers:        []Descriptor{layerDesc},
		Annotations:   annotations,
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return SnapshotResult{}, err
	}
	manifestDesc, err := s.writeBlob(manifestData)
	if err != nil {
		return SnapshotResult{}, err
	}

	tags := uniqueTags([]string{snapshotID, opts.Tag, opts.AlsoTag, "latest"})
	for _, tag := range tags {
		expected := ""
		if opts.ExpectedParent != "" && tag != snapshotID {
			expected = opts.ExpectedParent
		}
		if err := s.writeRefIfExpected(tag, manifestDesc.Digest, expected); err != nil {
			return SnapshotResult{}, err
		}
	}
	if opts.Sign != "" {
		if _, err := SignSnapshot(workspaceRoot, manifestDesc.Digest, opts.Sign, opts.Attest); err != nil {
			return SnapshotResult{}, err
		}
	}
	return SnapshotResult{ManifestDigest: manifestDesc.Digest, Tags: tags}, nil
}

func Restore(workspaceRoot, ref, target string, opts RestoreOptions) error {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return err
	}
	digest, _, _, err := s.loadManifest(ref)
	if err != nil {
		return err
	}
	if err := ensureRestoreTarget(target, opts.Force); err != nil {
		return err
	}
	chain, err := s.snapshotChain(digest)
	if err != nil {
		return err
	}
	for _, manifest := range chain {
		if len(manifest.Layers) != 1 {
			return fmt.Errorf("local prototype expects exactly one layer, got %d", len(manifest.Layers))
		}
		layerDesc := manifest.Layers[0]
		layer, err := s.readBlob(layerDesc.Digest)
		if err != nil {
			return err
		}
		layer, err = decryptLayer(layer, layerDesc, opts.Decrypt)
		if err != nil {
			return err
		}
		if err := extractLayer(layer, target); err != nil {
			return err
		}
	}
	return writeReplayMarker(target)
}

func Mount(workspaceRoot, ref, target string, opts MountOptions) (MountInfo, error) {
	return NewMountRuntime(workspaceRoot, opts.Mode).Mount(context.Background(), ref, target, opts)
}

func Diff(workspaceRoot, leftRef, rightRef string) ([]Change, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return nil, err
	}
	_, _, left, err := s.loadManifest(leftRef)
	if err != nil {
		return nil, err
	}
	_, _, right, err := s.loadManifest(rightRef)
	if err != nil {
		return nil, err
	}
	return diffTrees(left.Tree, right.Tree), nil
}

func DiffMount(workspaceRoot, target string) ([]Change, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return nil, err
	}
	info, err := s.findMount(target)
	if err != nil {
		return nil, err
	}
	_, _, parentCfg, err := s.loadManifest(info.SourceDigest)
	if err != nil {
		return nil, err
	}
	if (info.Mode == MountOverlay || info.Mode == MountFUSE) && info.UpperDir != "" {
		if err := flushRuntimeForTarget(workspaceRoot, target); err != nil {
			return nil, err
		}
		upperEntries, whiteouts, _, err := scanOverlayUpper(info.UpperDir)
		if err != nil {
			return nil, err
		}
		return diffOverlayUpper(parentCfg.Tree, upperEntries, whiteouts), nil
	}
	currentTree, _, err := scanTree(target)
	if err != nil {
		return nil, err
	}
	return diffTrees(parentCfg.Tree, currentTree), nil
}

func diffOverlayUpper(parent, upper []TreeEntry, whiteouts []string) []Change {
	parentMap := treeMap(parent)
	seen := map[string]bool{}
	var changes []Change
	for _, entry := range upper {
		if seen[entry.Path] {
			continue
		}
		seen[entry.Path] = true
		kind := "A"
		if parentEntry, ok := parentMap[entry.Path]; ok {
			if parentEntry == entry {
				continue
			}
			kind = "M"
		}
		changes = append(changes, Change{Kind: kind, Path: "/" + entry.Path})
	}
	for _, path := range effectiveOverlayWhiteouts(parent, whiteouts) {
		seen[path] = true
		changes = append(changes, Change{Kind: "D", Path: "/" + path})
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Path == changes[j].Path {
			return changes[i].Kind < changes[j].Kind
		}
		return changes[i].Path < changes[j].Path
	})
	return changes
}

func effectiveOverlayWhiteouts(parent []TreeEntry, whiteouts []string) []string {
	parentMap := treeMap(parent)
	filtered := make([]string, 0, len(whiteouts))
	for _, path := range whiteouts {
		if _, ok := parentMap[path]; ok {
			filtered = append(filtered, path)
		}
	}
	sort.Strings(filtered)
	return filtered
}

func changedOverlayEntries(parent, upper []TreeEntry) []TreeEntry {
	parentMap := treeMap(parent)
	changed := make([]TreeEntry, 0, len(upper))
	for _, entry := range upper {
		if parentEntry, ok := parentMap[entry.Path]; ok && parentEntry == entry {
			continue
		}
		changed = append(changed, entry)
	}
	return changed
}

func dirtyBytesForEntries(entries []TreeEntry) int64 {
	var total int64
	for _, entry := range entries {
		if entry.Type == "file" {
			total += entry.Size
		}
	}
	return total
}

func diffTrees(left, right []TreeEntry) []Change {
	leftMap := treeMap(left)
	rightMap := treeMap(right)
	seen := map[string]bool{}
	var changes []Change
	for path, rightEntry := range rightMap {
		seen[path] = true
		leftEntry, ok := leftMap[path]
		if !ok {
			changes = append(changes, Change{Kind: "A", Path: "/" + path})
			continue
		}
		if leftEntry != rightEntry {
			changes = append(changes, Change{Kind: "M", Path: "/" + path})
		}
	}
	for path := range leftMap {
		if !seen[path] {
			changes = append(changes, Change{Kind: "D", Path: "/" + path})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Path == changes[j].Path {
			return changes[i].Kind < changes[j].Kind
		}
		return changes[i].Path < changes[j].Path
	})
	return changes
}

func Fork(workspaceRoot, sourceRef, targetTag string) (string, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return "", err
	}
	digest, err := s.resolveRef(sourceRef)
	if err != nil {
		return "", err
	}
	if err := ValidateChain(workspaceRoot, digest); err != nil {
		return "", err
	}
	if targetTag == "" || strings.HasPrefix(targetTag, "sha256:") {
		return "", fmt.Errorf("target must be a mutable tag name")
	}
	if err := s.writeRef(targetTag, digest); err != nil {
		return "", err
	}
	return digest, nil
}

func ValidateChain(workspaceRoot, ref string) error {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return err
	}
	digest, err := s.resolveRef(ref)
	if err != nil {
		return err
	}
	chain, err := s.snapshotChainWithDigests(digest)
	if err != nil {
		return err
	}
	if len(chain) == 0 {
		return fmt.Errorf("empty snapshot chain")
	}
	base := chain[0].Config.Base.Digest
	var prevSeq int64
	for i, item := range chain {
		if item.Config.Base.Digest != base {
			return fmt.Errorf("snapshot %s base mismatch: %s != %s", item.Digest, item.Config.Base.Digest, base)
		}
		if i == 0 {
			if item.Config.Parent != nil {
				return fmt.Errorf("root snapshot %s unexpectedly has parent %s", item.Digest, item.Config.Parent.Digest)
			}
		} else {
			parentDigest := chain[i-1].Digest
			if item.Config.Parent == nil || item.Config.Parent.Digest != parentDigest {
				return fmt.Errorf("snapshot %s parent mismatch", item.Digest)
			}
			if item.Config.Snapshot.Sequence <= prevSeq {
				return fmt.Errorf("snapshot %s sequence is not increasing", item.Digest)
			}
		}
		prevSeq = item.Config.Snapshot.Sequence
	}
	return nil
}

func Show(workspaceRoot, ref string) (string, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return "", err
	}
	digest, manifest, cfg, err := s.loadManifest(ref)
	if err != nil {
		return "", err
	}
	parent := ""
	if cfg.Parent != nil {
		parent = cfg.Parent.Digest
	}
	var b strings.Builder
	fmt.Fprintf(&b, "digest:   %s\n", digest)
	fmt.Fprintf(&b, "snapshot: %s\n", cfg.Snapshot.ID)
	fmt.Fprintf(&b, "sequence: %d\n", cfg.Snapshot.Sequence)
	fmt.Fprintf(&b, "agent:    %s\n", cfg.Agent.ID)
	fmt.Fprintf(&b, "base:     %s\n", cfg.Base.Image)
	if parent != "" {
		fmt.Fprintf(&b, "parent:   %s\n", parent)
	}
	fmt.Fprintf(&b, "created:  %s\n", cfg.Snapshot.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "files:    %d\n", len(cfg.Tree))
	fmt.Fprintf(&b, "layer:    %s\n", manifest.Layers[0].Digest)
	return b.String(), nil
}

func Refs(workspaceRoot string) ([]Ref, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return nil, err
	}
	return s.listRefs()
}

func (s store) writeMount(info MountInfo) error {
	key, err := mountKey(info.Target)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Join(s.mountsRoot(), key)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := writePrivateFile(filepath.Join(dir, "mount.json"), data); err != nil {
		return err
	}
	return writePrivateFile(filepath.Join(s.mountsRoot(), key+".json"), data)
}

func (s store) findMount(target string) (MountInfo, error) {
	key, err := mountKey(target)
	if err != nil {
		return MountInfo{}, err
	}
	data, err := os.ReadFile(filepath.Join(s.mountsRoot(), key, "mount.json"))
	if os.IsNotExist(err) {
		data, err = os.ReadFile(filepath.Join(s.mountsRoot(), key+".json"))
	}
	if err != nil {
		return MountInfo{}, err
	}
	var info MountInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return MountInfo{}, err
	}
	return info, nil
}

func absPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func scanTree(root string) ([]TreeEntry, int64, error) {
	var entries []TreeEntry
	var dirtyBytes int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if shouldExclude(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		entry := TreeEntry{
			Path: rel,
			Mode: int64(info.Mode().Perm()),
		}
		switch {
		case info.Mode().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entry.Type = "file"
			entry.Size = info.Size()
			entry.Digest = digestBytes(data)
			dirtyBytes += info.Size()
		case info.IsDir():
			entry.Type = "dir"
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			entry.Type = "symlink"
			entry.Linkname = link
			entry.Digest = digestBytes([]byte(link))
		default:
			return nil
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, dirtyBytes, nil
}

func makeLayer(root string, tree []TreeEntry, whiteouts []string) ([]byte, error) {
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	for _, target := range whiteouts {
		whiteoutPath := whiteoutName(target)
		hdr := &tar.Header{
			Name:       whiteoutPath,
			Mode:       0,
			Size:       0,
			Typeflag:   tar.TypeReg,
			ModTime:    time.Unix(0, 0).UTC(),
			AccessTime: time.Unix(0, 0).UTC(),
			ChangeTime: time.Unix(0, 0).UTC(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
	}
	for _, entry := range tree {
		path := filepath.Join(root, filepath.FromSlash(entry.Path))
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(path)
			if err != nil {
				return nil, err
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return nil, err
		}
		var fileData []byte
		if info.Mode().IsRegular() && shouldRedact(entry.Path) {
			fileData, err = os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			fileData = redactLog(fileData)
			hdr.Size = int64(len(fileData))
		}
		hdr.Name = entry.Path
		hdr.ModTime = time.Unix(0, 0).UTC()
		hdr.AccessTime = time.Unix(0, 0).UTC()
		hdr.ChangeTime = time.Unix(0, 0).UTC()
		hdr.Uid = 0
		hdr.Gid = 0
		hdr.Uname = ""
		hdr.Gname = ""
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if info.Mode().IsRegular() {
			if fileData != nil {
				if _, err := tw.Write(fileData); err != nil {
					return nil, err
				}
			} else {
				f, err := os.Open(path)
				if err != nil {
					return nil, err
				}
				_, copyErr := io.Copy(tw, f)
				closeErr := f.Close()
				if copyErr != nil {
					return nil, copyErr
				}
				if closeErr != nil {
					return nil, closeErr
				}
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	var zstdBuf bytes.Buffer
	zw, err := zstd.NewWriter(&zstdBuf)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(tarBuf.Bytes()); err != nil {
		zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return zstdBuf.Bytes(), nil
}

func extractLayer(data []byte, target string) error {
	zr, err := zstd.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(hdr.Name)
		if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
			return fmt.Errorf("unsafe tar path %q", hdr.Name)
		}
		path := filepath.Join(target, clean)
		if strings.HasPrefix(filepath.Base(clean), ".wh.") {
			if err := applyWhiteout(target, clean); err != nil {
				return err
			}
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, fs.FileMode(hdr.Mode)); err != nil {
				return err
			}
			if err := os.Chmod(path, fs.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fs.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(f, tr)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if err := os.Chmod(path, fs.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.RemoveAll(path); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, path); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported tar entry %q type %v", hdr.Name, hdr.Typeflag)
		}
	}
	return nil
}

func (s store) snapshotChain(digest string) ([]Manifest, error) {
	var chain []Manifest
	seen := map[string]bool{}
	for digest != "" {
		if seen[digest] {
			return nil, fmt.Errorf("snapshot parent cycle at %s", digest)
		}
		seen[digest] = true
		_, manifest, cfg, err := s.loadManifest(digest)
		if err != nil {
			return nil, err
		}
		chain = append(chain, manifest)
		if cfg.Parent == nil {
			break
		}
		digest = cfg.Parent.Digest
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, nil
}

func ensureRestoreTarget(target string, force bool) error {
	entries, err := os.ReadDir(target)
	if errors.Is(err, fs.ErrNotExist) {
		return os.MkdirAll(target, 0o755)
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		if !force {
			return fmt.Errorf("target %s is not empty; pass --force to replace it", target)
		}
		if err := os.RemoveAll(target); err != nil {
			return err
		}
		return os.MkdirAll(target, 0o755)
	}
	return nil
}

func treeMap(entries []TreeEntry) map[string]TreeEntry {
	m := make(map[string]TreeEntry, len(entries))
	for _, entry := range entries {
		m[entry.Path] = entry
	}
	return m
}

func diffLayerEntries(parent, current []TreeEntry) ([]TreeEntry, []string) {
	parentMap := treeMap(parent)
	currentMap := treeMap(current)
	var entries []TreeEntry
	var whiteouts []string
	for _, entry := range current {
		if old, ok := parentMap[entry.Path]; !ok || old != entry {
			entries = append(entries, entry)
		}
	}
	for _, entry := range parent {
		if _, ok := currentMap[entry.Path]; !ok {
			whiteouts = append(whiteouts, entry.Path)
		}
	}
	whiteouts = mergeWhiteouts(whiteouts, typeChangeWhiteouts(parent, entries))
	return entries, whiteouts
}

func typeChangeWhiteouts(parent, changed []TreeEntry) []string {
	parentMap := treeMap(parent)
	var whiteouts []string
	for _, entry := range changed {
		if parentEntry, ok := parentMap[entry.Path]; ok && parentEntry.Type != entry.Type {
			whiteouts = append(whiteouts, entry.Path)
		}
	}
	return whiteouts
}

func mergeWhiteouts(groups ...[]string) []string {
	seen := map[string]bool{}
	var merged []string
	for _, group := range groups {
		for _, path := range group {
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			merged = append(merged, path)
		}
	}
	sort.Strings(merged)
	pruned := merged[:0]
	for _, path := range merged {
		covered := false
		for _, parent := range pruned {
			if strings.HasPrefix(path, parent+"/") {
				covered = true
				break
			}
		}
		if !covered {
			pruned = append(pruned, path)
		}
	}
	return pruned
}

func scanOverlayUpper(root string) ([]TreeEntry, []string, int64, error) {
	entries, dirtyBytes, err := scanTree(root)
	if err != nil {
		return nil, nil, 0, err
	}
	whiteoutSet := map[string]bool{}
	opaqueSet := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if shouldExclude(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := overlayWhiteoutTarget(rel)
		if target != "" && !shouldExclude(target) {
			whiteoutSet[target] = true
		}
		opaqueTarget := overlayOpaqueTarget(rel)
		if opaqueTarget != "" && !shouldExclude(opaqueTarget) {
			opaqueSet[opaqueTarget] = true
		}
		return nil
	})
	if err != nil {
		return nil, nil, 0, err
	}
	var filtered []TreeEntry
	for _, entry := range entries {
		if isOverlayWhiteoutMarker(entry.Path) || isCoveredByWhiteout(entry.Path, whiteoutSet) {
			continue
		}
		filtered = append(filtered, entry)
	}
	whiteouts := make([]string, 0, len(whiteoutSet)+len(opaqueSet))
	for target := range whiteoutSet {
		whiteouts = append(whiteouts, target)
	}
	for target := range opaqueSet {
		whiteouts = append(whiteouts, target)
	}
	sort.Strings(whiteouts)
	return filtered, whiteouts, dirtyBytes, nil
}

func isCoveredByWhiteout(path string, whiteouts map[string]bool) bool {
	path = filepath.ToSlash(path)
	for target := range whiteouts {
		if path == target || strings.HasPrefix(path, target+"/") {
			return true
		}
	}
	return false
}

func overlayWhiteoutTarget(path string) string {
	dir, base := filepath.Split(filepath.ToSlash(path))
	if base == ".wh..wh..opq" {
		return ""
	}
	if !strings.HasPrefix(base, ".wh.") {
		return ""
	}
	target := strings.TrimPrefix(base, ".wh.")
	if target == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Join(dir, target))
}

func overlayOpaqueTarget(path string) string {
	dir, base := filepath.Split(filepath.ToSlash(path))
	if base != ".wh..wh..opq" {
		return ""
	}
	target := strings.TrimSuffix(filepath.ToSlash(dir), "/")
	if target == "" || target == "." {
		return ""
	}
	return target
}

func isOverlayWhiteoutMarker(path string) bool {
	_, base := filepath.Split(filepath.ToSlash(path))
	return strings.HasPrefix(base, ".wh.")
}

func whiteoutName(target string) string {
	dir, base := filepath.Split(filepath.ToSlash(target))
	return filepath.ToSlash(filepath.Join(dir, ".wh."+base))
}

func applyWhiteout(root, whiteout string) error {
	dir, base := filepath.Split(whiteout)
	targetName := strings.TrimPrefix(base, ".wh.")
	if targetName == "" || targetName == base {
		return fmt.Errorf("invalid whiteout %q", whiteout)
	}
	return os.RemoveAll(filepath.Join(root, dir, targetName))
}

func digestTree(entries []TreeEntry) string {
	data, _ := json.Marshal(entries)
	return digestBytes(data)
}

func shouldExclude(rel string) bool {
	rel = filepath.ToSlash(strings.TrimPrefix(rel, "/"))
	exact := map[string]bool{
		".osix":                    true,
		".osix-replay-policy.json": true,
		".osix-turn-boundary":      true,
		".env":                     true,
		"agent/secrets":            true,
		"secrets":                  true,
		"agent/tmp":                true,
		"tmp":                      true,
		"agent/cache":              true,
		"cache":                    true,
	}
	if exact[rel] {
		return true
	}
	prefixes := []string{
		".osix/",
		"agent/secrets/",
		"secrets/",
		"agent/tmp/",
		"tmp/",
		"agent/cache/",
		"cache/",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return strings.Contains(rel, "/node_modules/.cache/") ||
		strings.Contains(rel, "/__pycache__/") ||
		strings.HasSuffix(rel, "/.env") ||
		strings.HasSuffix(rel, "/id_rsa") ||
		strings.HasSuffix(rel, "/id_ed25519")
}

func uniqueTags(tags []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out
}
