//go:build linux

package osix

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

type lazyFuseRoot struct {
	fs.Inode
	fsys *lazyFuseFS
}

type lazyFuseNode struct {
	fs.Inode
	fsys *lazyFuseFS
	path string
}

type lazyFuseFS struct {
	lower    *SnapshotLowerStore
	upper    string
	readOnly bool
}

func RunLazyFUSEServer(ctx context.Context, workspaceRoot, sourceRef, target, upper string, readOnly bool, opts ReadFileOptions) error {
	server, err := startLazyFUSEServer(ctx, workspaceRoot, sourceRef, target, upper, readOnly, opts)
	if err != nil {
		return err
	}
	server.Wait()
	return nil
}

func lazyFuseMount(ctx context.Context, workspaceRoot, sourceRef, target string, opts MountOptions) (MountInfo, error) {
	s, err := findStore(workspaceRoot)
	if err != nil {
		return MountInfo{}, err
	}
	digest, err := s.resolveRef(sourceRef)
	if err != nil {
		return MountInfo{}, err
	}
	root, lower, upper, work, rootExisted, err := prepareKernelMountDirsWithRestore(workspaceRoot, sourceRef, target, opts, false)
	if err != nil {
		return MountInfo{}, err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		cleanupFreshKernelMountDirs(root, rootExisted)
		return MountInfo{}, err
	}

	pid := os.Getpid()
	helper := lazyFuseHelperPath()
	if helper == "" {
		server, err := startLazyFUSEServer(ctx, workspaceRoot, digest, target, upper, opts.ReadOnly, ReadFileOptions{Decrypt: opts.Decrypt})
		if err != nil {
			cleanupFreshKernelMountDirs(root, rootExisted)
			return MountInfo{}, err
		}
		pid = os.Getpid()
		_ = server
	} else {
		logPath := filepath.Join(root, "lazy-fuse.log")
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			cleanupFreshKernelMountDirs(root, rootExisted)
			return MountInfo{}, err
		}
		args := []string{"__fuse-lazy-server", "--workspace-root", absPath(workspaceRoot), "--source-ref", digest, "--target", absPath(target), "--upper", upper}
		if opts.ReadOnly {
			args = append(args, "--read-only")
		}
		if opts.Decrypt != "" {
			args = append(args, "--decrypt", opts.Decrypt)
		}
		cmd := exec.CommandContext(ctx, helper, args...)
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		if err := cmd.Start(); err != nil {
			_ = logFile.Close()
			cleanupFreshKernelMountDirs(root, rootExisted)
			return MountInfo{}, fmt.Errorf("start lazy FUSE helper: %w", err)
		}
		pid = cmd.Process.Pid
		_ = writePrivateFile(filepath.Join(root, "lazy-fuse.pid"), []byte(strconv.Itoa(pid)+"\n"))
		go func() {
			_ = cmd.Wait()
			_ = logFile.Close()
		}()
		if err := waitForLazyFuseMount(ctx, target, cmd.Process, logPath); err != nil {
			cleanupFreshKernelMountDirs(root, rootExisted)
			return MountInfo{}, err
		}
	}

	now := time.Now().UTC().Truncate(time.Second)
	info := MountInfo{
		Target:       absPath(target),
		SourceRef:    sourceRef,
		SourceDigest: digest,
		Mode:         MountFUSE,
		Branch:       opts.Branch,
		RW:           mountAllowsWrites(opts),
		UpperDir:     upper,
		WorkDir:      work,
		LowerDir:     lower,
		State:        "mounted",
		PID:          pid,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	return persistMountedRuntime(s, root, info, []byte("fuse\nosix-lazy\n"), func() error {
		return lazyFuseUnmount(ctx, target, pid, true)
	})
}

func startLazyFUSEServer(ctx context.Context, workspaceRoot, sourceRef, target, upper string, readOnly bool, opts ReadFileOptions) (*fuse.Server, error) {
	lower, err := OpenSnapshotLowerStore(workspaceRoot, sourceRef, opts)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(upper, 0o700); err != nil {
		return nil, err
	}
	mountOptions := fuse.MountOptions{
		Name:    "osix-lazy",
		FsName:  "osix-lazy",
		Options: []string{"default_permissions"},
	}
	if readOnly {
		mountOptions.Options = append(mountOptions.Options, "ro")
	}
	server, err := fs.Mount(target, &lazyFuseRoot{fsys: &lazyFuseFS{lower: lower, upper: upper, readOnly: readOnly}}, &fs.Options{
		MountOptions: mountOptions,
	})
	if err != nil {
		return nil, fmt.Errorf("mount lazy FUSE: %w", err)
	}
	return server, nil
}

func (r *lazyFuseRoot) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	entry, errno := r.fsys.entry("")
	if errno != 0 {
		return errno
	}
	setLazyFuseAttr(out, entry)
	return 0
}

func (r *lazyFuseRoot) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	return lazyFuseLookup(ctx, &r.Inode, r.fsys, "", name, out)
}

func (r *lazyFuseRoot) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	return lazyFuseReadDir(r.fsys, "")
}

func (r *lazyFuseRoot) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	return lazyFuseMkdir(ctx, &r.Inode, r.fsys, "", name, mode, out)
}

func (r *lazyFuseRoot) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	return lazyFuseCreate(ctx, &r.Inode, r.fsys, "", name, mode, out)
}

func (r *lazyFuseRoot) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	return lazyFuseSymlink(ctx, &r.Inode, r.fsys, "", target, name, out)
}

func (r *lazyFuseRoot) Unlink(ctx context.Context, name string) syscall.Errno {
	return r.fsys.unlink(filepath.ToSlash(name), false)
}

func (r *lazyFuseRoot) Rmdir(ctx context.Context, name string) syscall.Errno {
	return r.fsys.unlink(filepath.ToSlash(name), true)
}

func (r *lazyFuseRoot) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	return lazyFuseRename(r.fsys, "", name, newParent, newName, flags)
}

func (n *lazyFuseNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	entry, errno := n.fsys.entry(n.path)
	if errno != 0 {
		return errno
	}
	setLazyFuseAttr(out, entry)
	return 0
}

func (n *lazyFuseNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	entry, errno := n.fsys.entry(n.path)
	if errno != 0 {
		return nil, errno
	}
	if entry.Type != "dir" {
		return nil, syscall.ENOTDIR
	}
	return lazyFuseLookup(ctx, &n.Inode, n.fsys, n.path, name, out)
}

func (n *lazyFuseNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entry, errno := n.fsys.entry(n.path)
	if errno != 0 {
		return nil, errno
	}
	if entry.Type != "dir" {
		return nil, syscall.ENOTDIR
	}
	return lazyFuseReadDir(n.fsys, n.path)
}

func (n *lazyFuseNode) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	entry, errno := n.fsys.entry(n.path)
	if errno != 0 {
		return nil, errno
	}
	if entry.Type != "symlink" {
		return nil, syscall.EINVAL
	}
	if n.fsys.upperExists(n.path) {
		link, err := os.Readlink(n.fsys.upperPath(n.path))
		if err != nil {
			return nil, lazyFuseErrno(err)
		}
		return []byte(link), 0
	}
	return []byte(entry.Linkname), 0
}

func (n *lazyFuseNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	entry, errno := n.fsys.entry(n.path)
	if errno != 0 {
		return nil, 0, errno
	}
	if entry.Type != "file" {
		return nil, 0, syscall.EISDIR
	}
	if flags&syscall.O_ACCMODE != syscall.O_RDONLY {
		if n.fsys.readOnly {
			return nil, 0, syscall.EROFS
		}
		if err := n.fsys.copyUpFile(n.path); err != nil {
			return nil, 0, lazyFuseErrno(err)
		}
		if flags&syscall.O_TRUNC != 0 {
			if err := os.Truncate(n.fsys.upperPath(n.path), 0); err != nil {
				return nil, 0, lazyFuseErrno(err)
			}
		}
	}
	return nil, fuse.FOPEN_KEEP_CACHE, 0
}

func (n *lazyFuseNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	entry, errno := n.fsys.entry(n.path)
	if errno != 0 {
		return nil, errno
	}
	if entry.Type != "file" {
		return nil, syscall.EISDIR
	}
	if off >= entry.Size || len(dest) == 0 {
		return fuse.ReadResultData(nil), 0
	}
	if n.fsys.upperExists(n.path) {
		file, err := os.Open(n.fsys.upperPath(n.path))
		if err != nil {
			return nil, lazyFuseErrno(err)
		}
		defer file.Close()
		nread, err := file.ReadAt(dest, off)
		if err != nil && nread == 0 {
			return nil, lazyFuseErrno(err)
		}
		return fuse.ReadResultData(dest[:nread]), 0
	}
	length := int64(len(dest))
	if remaining := entry.Size - off; remaining < length {
		length = remaining
	}
	data, err := n.fsys.lower.ReadFileRange(n.path, off, length)
	if err != nil {
		return nil, syscall.EIO
	}
	return fuse.ReadResultData(data), 0
}

func (n *lazyFuseNode) Write(ctx context.Context, f fs.FileHandle, data []byte, off int64) (uint32, syscall.Errno) {
	if n.fsys.readOnly {
		return 0, syscall.EROFS
	}
	if err := n.fsys.copyUpFile(n.path); err != nil {
		return 0, lazyFuseErrno(err)
	}
	file, err := os.OpenFile(n.fsys.upperPath(n.path), os.O_WRONLY, 0)
	if err != nil {
		return 0, lazyFuseErrno(err)
	}
	defer file.Close()
	written, err := file.WriteAt(data, off)
	if err != nil {
		return uint32(written), lazyFuseErrno(err)
	}
	return uint32(written), 0
}

func (n *lazyFuseNode) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if n.fsys.readOnly {
		return syscall.EROFS
	}
	entry, errno := n.fsys.entry(n.path)
	if errno != 0 {
		return errno
	}
	if entry.Type == "file" {
		if err := n.fsys.copyUpFile(n.path); err != nil {
			return lazyFuseErrno(err)
		}
	} else if !n.fsys.upperExists(n.path) {
		if err := n.fsys.copyUpMetadata(n.path, entry); err != nil {
			return lazyFuseErrno(err)
		}
	}
	upperPath := n.fsys.upperPath(n.path)
	if mode, ok := in.GetMode(); ok {
		if err := os.Chmod(upperPath, os.FileMode(mode&0o777)); err != nil {
			return lazyFuseErrno(err)
		}
	}
	if size, ok := in.GetSize(); ok {
		if err := os.Truncate(upperPath, int64(size)); err != nil {
			return lazyFuseErrno(err)
		}
	}
	entry, errno = n.fsys.entry(n.path)
	if errno != 0 {
		return errno
	}
	setLazyFuseAttr(out, entry)
	return 0
}

func (n *lazyFuseNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	return lazyFuseMkdir(ctx, &n.Inode, n.fsys, n.path, name, mode, out)
}

func (n *lazyFuseNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	return lazyFuseCreate(ctx, &n.Inode, n.fsys, n.path, name, mode, out)
}

func (n *lazyFuseNode) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	return lazyFuseSymlink(ctx, &n.Inode, n.fsys, n.path, target, name, out)
}

func (n *lazyFuseNode) Unlink(ctx context.Context, name string) syscall.Errno {
	return n.fsys.unlink(filepath.ToSlash(filepath.Join(n.path, name)), false)
}

func (n *lazyFuseNode) Rmdir(ctx context.Context, name string) syscall.Errno {
	return n.fsys.unlink(filepath.ToSlash(filepath.Join(n.path, name)), true)
}

func (n *lazyFuseNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	return lazyFuseRename(n.fsys, n.path, name, newParent, newName, flags)
}

func lazyFuseLookup(ctx context.Context, parent *fs.Inode, fsys *lazyFuseFS, parentPath, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	childPath := filepath.ToSlash(filepath.Join(parentPath, name))
	entry, errno := fsys.entry(childPath)
	if errno != 0 {
		return nil, errno
	}
	setLazyFuseEntry(out, entry)
	node := &lazyFuseNode{fsys: fsys, path: entry.Path}
	return parent.NewInode(ctx, node, fs.StableAttr{Mode: lazyFuseMode(entry), Ino: lazyFuseInode(entry.Path)}), 0
}

func lazyFuseMkdir(ctx context.Context, parent *fs.Inode, fsys *lazyFuseFS, parentPath, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if fsys.readOnly {
		return nil, syscall.EROFS
	}
	childPath := filepath.ToSlash(filepath.Join(parentPath, name))
	if err := fsys.ensureParentDir(childPath); err != nil {
		return nil, lazyFuseErrno(err)
	}
	if err := os.Remove(fsys.whiteoutPath(childPath)); err != nil && !os.IsNotExist(err) {
		return nil, lazyFuseErrno(err)
	}
	if err := os.Mkdir(fsys.upperPath(childPath), os.FileMode(mode&0o777)); err != nil {
		return nil, lazyFuseErrno(err)
	}
	entry, errno := fsys.entry(childPath)
	if errno != 0 {
		return nil, errno
	}
	setLazyFuseEntry(out, entry)
	node := &lazyFuseNode{fsys: fsys, path: childPath}
	return parent.NewInode(ctx, node, fs.StableAttr{Mode: lazyFuseMode(entry), Ino: lazyFuseInode(entry.Path)}), 0
}

func lazyFuseCreate(ctx context.Context, parent *fs.Inode, fsys *lazyFuseFS, parentPath, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	if fsys.readOnly {
		return nil, nil, 0, syscall.EROFS
	}
	childPath := filepath.ToSlash(filepath.Join(parentPath, name))
	if err := fsys.ensureParentDir(childPath); err != nil {
		return nil, nil, 0, lazyFuseErrno(err)
	}
	if err := os.Remove(fsys.whiteoutPath(childPath)); err != nil && !os.IsNotExist(err) {
		return nil, nil, 0, lazyFuseErrno(err)
	}
	file, err := os.OpenFile(fsys.upperPath(childPath), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(mode&0o777))
	if err != nil {
		return nil, nil, 0, lazyFuseErrno(err)
	}
	_ = file.Close()
	entry, errno := fsys.entry(childPath)
	if errno != 0 {
		return nil, nil, 0, errno
	}
	setLazyFuseEntry(out, entry)
	node := &lazyFuseNode{fsys: fsys, path: childPath}
	return parent.NewInode(ctx, node, fs.StableAttr{Mode: lazyFuseMode(entry), Ino: lazyFuseInode(entry.Path)}), nil, fuse.FOPEN_KEEP_CACHE, 0
}

func lazyFuseSymlink(ctx context.Context, parent *fs.Inode, fsys *lazyFuseFS, parentPath, target, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if fsys.readOnly {
		return nil, syscall.EROFS
	}
	childPath := filepath.ToSlash(filepath.Join(parentPath, name))
	if err := fsys.ensureParentDir(childPath); err != nil {
		return nil, lazyFuseErrno(err)
	}
	if err := os.Remove(fsys.whiteoutPath(childPath)); err != nil && !os.IsNotExist(err) {
		return nil, lazyFuseErrno(err)
	}
	if err := os.Symlink(target, fsys.upperPath(childPath)); err != nil {
		return nil, lazyFuseErrno(err)
	}
	entry, errno := fsys.entry(childPath)
	if errno != 0 {
		return nil, errno
	}
	setLazyFuseEntry(out, entry)
	node := &lazyFuseNode{fsys: fsys, path: childPath}
	return parent.NewInode(ctx, node, fs.StableAttr{Mode: lazyFuseMode(entry), Ino: lazyFuseInode(entry.Path)}), 0
}

func lazyFuseRename(fsys *lazyFuseFS, parentPath, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	if fsys.readOnly {
		return syscall.EROFS
	}
	if flags != 0 {
		return syscall.EINVAL
	}
	newParentPath, ok := lazyFuseNodePath(newParent)
	if !ok {
		return syscall.EXDEV
	}
	oldPath := filepath.ToSlash(filepath.Join(parentPath, name))
	newPath := filepath.ToSlash(filepath.Join(newParentPath, newName))
	oldEntry, oldFound, err := fsys.lookupVisible(oldPath)
	if err != nil {
		return lazyFuseErrno(err)
	}
	if !oldFound {
		return syscall.ENOENT
	}
	if oldEntry.Type == "file" {
		if err := fsys.copyUpFile(oldPath); err != nil {
			return lazyFuseErrno(err)
		}
	} else if !fsys.upperExists(oldPath) {
		if err := fsys.copyUpMetadata(oldPath, oldEntry); err != nil {
			return lazyFuseErrno(err)
		}
	}
	if err := fsys.ensureParentDir(newPath); err != nil {
		return lazyFuseErrno(err)
	}
	if _, destFound, err := fsys.lookupVisible(newPath); err != nil {
		return lazyFuseErrno(err)
	} else if destFound {
		if errno := fsys.unlink(newPath, oldEntry.Type == "dir"); errno != 0 && errno != syscall.ENOENT {
			return errno
		}
	}
	if err := os.Remove(fsys.whiteoutPath(newPath)); err != nil && !os.IsNotExist(err) {
		return lazyFuseErrno(err)
	}
	if err := os.Rename(fsys.upperPath(oldPath), fsys.upperPath(newPath)); err != nil {
		return lazyFuseErrno(err)
	}
	if _, found, err := fsys.lower.Lookup(oldPath); err != nil {
		return lazyFuseErrno(err)
	} else if found {
		if err := fsys.createWhiteout(oldPath); err != nil {
			return lazyFuseErrno(err)
		}
	}
	return 0
}

func lazyFuseNodePath(node fs.InodeEmbedder) (string, bool) {
	switch n := node.(type) {
	case *lazyFuseRoot:
		return "", true
	case *lazyFuseNode:
		return n.path, true
	default:
		return "", false
	}
}

func lazyFuseReadDir(fsys *lazyFuseFS, path string) (fs.DirStream, syscall.Errno) {
	entry, errno := fsys.entry(path)
	if errno != 0 {
		return nil, errno
	}
	if entry.Type != "dir" {
		return nil, syscall.ENOTDIR
	}
	children := map[string]TreeEntry{}
	if !fsys.hasOpaqueWhiteout(path) {
		lowerChildren, err := fsys.lower.ReadDir(path)
		if err != nil {
			return nil, syscall.ENOENT
		}
		for _, child := range lowerChildren {
			children[filepath.Base(child.Path)] = child
		}
	}
	upperDir := fsys.upperPath(path)
	if entries, err := os.ReadDir(upperDir); err == nil {
		for _, dirEntry := range entries {
			name := dirEntry.Name()
			if name == ".wh..wh..opq" {
				continue
			}
			if strings.HasPrefix(name, ".wh.") {
				delete(children, strings.TrimPrefix(name, ".wh."))
				continue
			}
			childPath := filepath.ToSlash(filepath.Join(path, name))
			child, errno := fsys.entry(childPath)
			if errno == 0 {
				children[name] = child
			}
		}
	}
	names := make([]string, 0, len(children))
	for name := range children {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]fuse.DirEntry, 0, len(names))
	for _, name := range names {
		child := children[name]
		entries = append(entries, fuse.DirEntry{
			Name: name,
			Ino:  lazyFuseInode(child.Path),
			Mode: lazyFuseMode(child),
		})
	}
	return fs.NewListDirStream(entries), 0
}

func (f *lazyFuseFS) entry(path string) (TreeEntry, syscall.Errno) {
	if path == "" {
		if st, err := os.Lstat(f.upperPath("")); err == nil {
			return lazyFuseEntryFromInfo("", st, f.upperPath("")), 0
		}
		return TreeEntry{Path: "", Type: "dir", Mode: 0o755}, 0
	}
	entry, found, err := f.lookupVisible(path)
	if err != nil {
		return TreeEntry{}, lazyFuseErrno(err)
	}
	if !found {
		return TreeEntry{}, syscall.ENOENT
	}
	return entry, 0
}

func (f *lazyFuseFS) lookupVisible(path string) (TreeEntry, bool, error) {
	path, err := canonicalLowerStorePath(path)
	if err != nil {
		return TreeEntry{}, false, err
	}
	if path == "" {
		return TreeEntry{Path: "", Type: "dir", Mode: 0o755}, true, nil
	}
	if f.hasWhiteout(path) {
		return TreeEntry{}, false, nil
	}
	if st, err := os.Lstat(f.upperPath(path)); err == nil {
		return lazyFuseEntryFromInfo(path, st, f.upperPath(path)), true, nil
	} else if err != nil && !os.IsNotExist(err) {
		return TreeEntry{}, false, err
	}
	entry, found, err := f.lower.Lookup(path)
	if err != nil {
		return TreeEntry{}, false, err
	}
	return entry, found, nil
}

func (f *lazyFuseFS) unlink(path string, dir bool) syscall.Errno {
	if f.readOnly {
		return syscall.EROFS
	}
	entry, found, err := f.lookupVisible(path)
	if err != nil {
		return lazyFuseErrno(err)
	}
	if !found {
		return syscall.ENOENT
	}
	if dir && entry.Type != "dir" {
		return syscall.ENOTDIR
	}
	if !dir && entry.Type == "dir" {
		return syscall.EISDIR
	}
	if f.upperExists(path) {
		if entry.Type == "dir" {
			if err := os.RemoveAll(f.upperPath(path)); err != nil {
				return lazyFuseErrno(err)
			}
		} else if err := os.Remove(f.upperPath(path)); err != nil {
			return lazyFuseErrno(err)
		}
	}
	if _, lowerFound, err := f.lower.Lookup(path); err != nil {
		return lazyFuseErrno(err)
	} else if lowerFound {
		if err := f.createWhiteout(path); err != nil {
			return lazyFuseErrno(err)
		}
	}
	return 0
}

func (f *lazyFuseFS) copyUpFile(path string) error {
	path, err := canonicalLowerStorePath(path)
	if err != nil {
		return err
	}
	if f.upperExists(path) {
		return nil
	}
	entry, found, err := f.lower.Lookup(path)
	if err != nil {
		return err
	}
	if !found {
		return os.ErrNotExist
	}
	if entry.Type != "file" {
		return fmt.Errorf("%s is not a file", path)
	}
	if err := f.ensureParentDir(path); err != nil {
		return err
	}
	data, err := f.lower.ReadFile(path)
	if err != nil {
		return err
	}
	if err := os.WriteFile(f.upperPath(path), data, os.FileMode(entry.Mode&0o777)); err != nil {
		return err
	}
	if err := os.Remove(f.whiteoutPath(path)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (f *lazyFuseFS) copyUpMetadata(path string, entry TreeEntry) error {
	if f.upperExists(path) {
		return nil
	}
	if err := f.ensureParentDir(path); err != nil {
		return err
	}
	switch entry.Type {
	case "dir":
		if err := os.MkdirAll(f.upperPath(path), os.FileMode(entry.Mode&0o777)); err != nil {
			return err
		}
		if err := os.Remove(f.whiteoutPath(path)); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	case "symlink":
		if err := os.Symlink(entry.Linkname, f.upperPath(path)); err != nil {
			return err
		}
		if err := os.Remove(f.whiteoutPath(path)); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	default:
		return fmt.Errorf("%s is not metadata-only", path)
	}
}

func (f *lazyFuseFS) ensureParentDir(path string) error {
	parent := filepath.ToSlash(filepath.Dir(path))
	if parent == "." {
		parent = ""
	}
	if parent != "" {
		entry, found, err := f.lookupVisible(parent)
		if err != nil {
			return err
		}
		if !found || entry.Type != "dir" {
			return syscall.ENOTDIR
		}
		if !f.upperExists(parent) {
			if err := f.copyUpMetadata(parent, entry); err != nil {
				return err
			}
		}
	}
	return os.MkdirAll(filepath.Dir(f.upperPath(path)), 0o700)
}

func (f *lazyFuseFS) upperPath(path string) string {
	path = filepath.FromSlash(strings.TrimPrefix(path, "/"))
	if path == "" {
		return f.upper
	}
	return filepath.Join(f.upper, path)
}

func (f *lazyFuseFS) upperExists(path string) bool {
	_, err := os.Lstat(f.upperPath(path))
	return err == nil
}

func (f *lazyFuseFS) whiteoutPath(path string) string {
	dir, base := filepath.Split(filepath.ToSlash(path))
	return filepath.Join(f.upperPath(strings.TrimSuffix(dir, "/")), ".wh."+base)
}

func (f *lazyFuseFS) hasWhiteout(path string) bool {
	_, err := os.Lstat(f.whiteoutPath(path))
	return err == nil
}

func (f *lazyFuseFS) hasOpaqueWhiteout(path string) bool {
	_, err := os.Lstat(filepath.Join(f.upperPath(path), ".wh..wh..opq"))
	return err == nil
}

func (f *lazyFuseFS) createWhiteout(path string) error {
	if err := f.ensureParentDir(path); err != nil {
		return err
	}
	file, err := os.OpenFile(f.whiteoutPath(path), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return file.Close()
}

func lazyFuseEntryFromInfo(path string, st os.FileInfo, physical string) TreeEntry {
	entry := TreeEntry{
		Path: path,
		Mode: int64(st.Mode().Perm()),
	}
	switch {
	case st.Mode().IsRegular():
		entry.Type = "file"
		entry.Size = st.Size()
	case st.IsDir():
		entry.Type = "dir"
	case st.Mode()&os.ModeSymlink != 0:
		entry.Type = "symlink"
		entry.Mode = 0o777
		if link, err := os.Readlink(physical); err == nil {
			entry.Linkname = link
		}
	default:
		entry.Type = "file"
	}
	return entry
}

func lazyFuseErrno(err error) syscall.Errno {
	if err == nil {
		return 0
	}
	if errors.Is(err, os.ErrNotExist) {
		return syscall.ENOENT
	}
	if errors.Is(err, syscall.ENOTDIR) {
		return syscall.ENOTDIR
	}
	if errors.Is(err, syscall.EISDIR) {
		return syscall.EISDIR
	}
	if errno, ok := err.(syscall.Errno); ok {
		return errno
	}
	return syscall.EIO
}

func setLazyFuseEntry(out *fuse.EntryOut, entry TreeEntry) {
	out.Mode = lazyFuseMode(entry)
	out.Size = uint64(entry.Size)
	out.Ino = lazyFuseInode(entry.Path)
}

func setLazyFuseAttr(out *fuse.AttrOut, entry TreeEntry) {
	out.Mode = lazyFuseMode(entry)
	out.Size = uint64(entry.Size)
	out.Ino = lazyFuseInode(entry.Path)
}

func lazyFuseMode(entry TreeEntry) uint32 {
	perm := uint32(entry.Mode & 0o777)
	if perm == 0 {
		switch entry.Type {
		case "dir":
			perm = 0o755
		case "symlink":
			perm = 0o777
		default:
			perm = 0o644
		}
	}
	switch entry.Type {
	case "dir":
		return fuse.S_IFDIR | perm
	case "symlink":
		return fuse.S_IFLNK | perm
	default:
		return fuse.S_IFREG | perm
	}
}

func lazyFuseInode(path string) uint64 {
	if path == "" {
		return 1
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(path))
	return h.Sum64()
}

func lazyFuseHelperPath() string {
	if helper := strings.TrimSpace(os.Getenv("OSIX_FUSE_LAZY_HELPER")); helper != "" {
		return helper
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if filepath.Base(exe) != "osix" {
		return ""
	}
	return exe
}

func waitForLazyFuseMount(ctx context.Context, target string, proc *os.Process, logPath string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if isMounted(target) {
			return nil
		}
		if proc != nil && !processAlive(proc.Pid) {
			detail, _ := os.ReadFile(logPath)
			return fmt.Errorf("lazy FUSE helper exited before mount became ready: %s", strings.TrimSpace(string(detail)))
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for lazy FUSE mount at %s", target)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func lazyFuseUnmount(ctx context.Context, target string, pid int, force bool) error {
	if path, err := exec.LookPath("fusermount3"); err == nil {
		_ = exec.CommandContext(ctx, path, "-u", target).Run()
	} else if path, err := exec.LookPath("fusermount"); err == nil {
		_ = exec.CommandContext(ctx, path, "-u", target).Run()
	} else {
		_ = syscall.Unmount(target, 0)
	}
	if force && pid > 0 && pid != os.Getpid() {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	}
	return nil
}

var _ fs.NodeGetattrer = (*lazyFuseRoot)(nil)
var _ fs.NodeLookuper = (*lazyFuseRoot)(nil)
var _ fs.NodeReaddirer = (*lazyFuseRoot)(nil)
var _ fs.NodeGetattrer = (*lazyFuseNode)(nil)
var _ fs.NodeLookuper = (*lazyFuseNode)(nil)
var _ fs.NodeReaddirer = (*lazyFuseNode)(nil)
var _ fs.NodeReadlinker = (*lazyFuseNode)(nil)
var _ fs.NodeOpener = (*lazyFuseNode)(nil)
var _ fs.NodeReader = (*lazyFuseNode)(nil)
