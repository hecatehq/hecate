package workspacefs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hecatehq/hecate/internal/localfs"
)

var (
	ErrNotStableRegularFile  = errors.New("workspace path is not a stable regular file")
	ErrSymlinkComponent      = errors.New("workspace path uses a symlink component")
	ErrStableReadUnsupported = errors.New("stable no-follow workspace reads are unsupported on this platform")
)

// FS is the canonical path resolver for Hecate-controlled workspace file
// operations. It rejects traversal and existing symlink components so callers
// do not each need to reimplement workspace-boundary checks.
type FS struct {
	root           string
	stableRoot     string
	stableRootInfo fs.FileInfo
	inspector      *localfs.Inspector

	beforeStableDirectoryPostVerify func(string)
}

type DirEntry struct {
	Name             string
	Type             fs.FileMode
	IsDir            bool
	Size             int64
	TraversalBlocked bool
}

func New(root string) (*FS, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("workspace root is required")
	}
	root = filepath.Clean(root)
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	stableRoot := root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		stableRoot = filepath.Clean(resolved)
	}
	return &FS{root: root, stableRoot: stableRoot}, nil
}

// NewPinned returns a workspace filesystem whose stable reads remain bound to
// the directory identity observed at construction. It is intended for
// multi-step read-only reviews that must refuse a workspace-root replacement
// between inventory and preview.
func NewPinned(root string) (*FS, error) {
	inspector, err := localfs.NewInspector()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStableReadUnsupported, err)
	}
	if err := inspector.EnsurePath(root); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStableReadUnsupported, err)
	}
	fSys, err := New(root)
	if err != nil {
		return nil, err
	}
	if err := inspector.EnsurePath(fSys.stableRoot); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStableReadUnsupported, err)
	}
	info, err := os.Stat(fSys.stableRoot)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root is not a directory")
	}
	fSys.stableRootInfo = info
	fSys.inspector = inspector
	return fSys, nil
}

func (fsys *FS) Root() string {
	if fsys == nil {
		return ""
	}
	return fsys.root
}

func (fsys *FS) Resolve(relativePath string) (string, error) {
	if fsys == nil {
		return "", fmt.Errorf("workspace filesystem is not configured")
	}
	return SafeJoin(fsys.root, relativePath)
}

func (fsys *FS) ReadFile(relativePath string) ([]byte, string, error) {
	path, err := fsys.Resolve(relativePath)
	if err != nil {
		return nil, "", err
	}
	rootDir, rel, err := fsys.openRootRelative(path)
	if err != nil {
		return nil, "", err
	}
	defer rootDir.Close()
	data, err := rootDir.ReadFile(rel)
	return data, path, err
}

func (fsys *FS) Stat(relativePath string) (fs.FileInfo, string, error) {
	path, err := fsys.Resolve(relativePath)
	if err != nil {
		return nil, "", err
	}
	rootDir, rel, err := fsys.openRootRelative(path)
	if err != nil {
		return nil, "", err
	}
	defer rootDir.Close()
	info, err := rootDir.Stat(rel)
	return info, path, err
}

func (fsys *FS) Open(relativePath string) (*os.File, string, error) {
	path, err := fsys.Resolve(relativePath)
	if err != nil {
		return nil, "", err
	}
	rootDir, rel, err := fsys.openRootRelative(path)
	if err != nil {
		return nil, "", err
	}
	defer rootDir.Close()
	file, err := rootDir.Open(rel)
	return file, path, err
}

// OpenReadNonBlocking opens a regular file or directory for inspection and
// returns metadata from that same stable handle. Special files are rejected
// from metadata alone, before their device/FIFO open path can run.
func (fsys *FS) OpenReadNonBlocking(relativePath string) (*os.File, fs.FileInfo, string, error) {
	path, err := fsys.Resolve(relativePath)
	if err != nil {
		return nil, nil, "", err
	}
	local := filepath.FromSlash(relativePath)
	if relativePath == "" || !filepath.IsLocal(local) || filepath.ToSlash(filepath.Clean(local)) != relativePath {
		return nil, nil, "", fmt.Errorf("unsafe relative workspace path %q", relativePath)
	}
	stableRoot := fsys.stableRoot
	if stableRoot == "" {
		stableRoot = fsys.root
	}
	inspector, err := fsys.localInspector()
	if err != nil {
		return nil, nil, "", err
	}
	if err := inspector.EnsurePath(filepath.Join(stableRoot, local)); err != nil {
		return nil, nil, "", fmt.Errorf("%w: %v", ErrStableReadUnsupported, err)
	}
	rootDir, err := os.OpenRoot(stableRoot)
	if err != nil {
		return nil, nil, "", err
	}
	before, lstatErr := rootDir.Lstat(local)
	rootInfo, rootInfoErr := rootDir.Stat(".")
	_ = rootDir.Close()
	if lstatErr != nil {
		return nil, nil, "", lstatErr
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, nil, "", ErrSymlinkComponent
	}
	if fsys.stableRootInfo != nil && (rootInfoErr != nil || !os.SameFile(fsys.stableRootInfo, rootInfo)) {
		return nil, nil, "", ErrNotStableRegularFile
	}
	if before.Mode().IsRegular() {
		file, info, _, openErr := fsys.OpenStableRegularRead(relativePath)
		return file, info, path, openErr
	}
	if !before.IsDir() {
		return nil, nil, "", ErrNotStableRegularFile
	}
	file, info, openedRootInfo, openErr := openStableDirectoryRead(stableRoot, local)
	if openErr != nil {
		return nil, nil, "", openErr
	}
	if err := localfs.EnsureBoundedFile(file); err != nil {
		_ = file.Close()
		return nil, nil, "", fmt.Errorf("%w: %v", ErrStableReadUnsupported, err)
	}
	if fsys.stableRootInfo != nil && (openedRootInfo == nil || !os.SameFile(fsys.stableRootInfo, openedRootInfo)) {
		_ = file.Close()
		return nil, nil, "", ErrNotStableRegularFile
	}
	return file, info, path, nil
}

// OpenStableRegularRead opens a workspace-relative regular file without
// following its final symlink and verifies that the opened handle still names
// the entry inspected through the workspace root. It is intended for bounded
// passive previews where FIFOs, devices, sockets, symlinks, and replacement
// races must never become readable content.
func (fsys *FS) OpenStableRegularRead(relativePath string) (*os.File, fs.FileInfo, string, error) {
	if fsys == nil {
		return nil, nil, "", fmt.Errorf("workspace filesystem is not configured")
	}
	local := filepath.FromSlash(relativePath)
	if relativePath == "" || !filepath.IsLocal(local) || filepath.ToSlash(filepath.Clean(local)) != relativePath {
		return nil, nil, "", fmt.Errorf("unsafe relative workspace path %q", relativePath)
	}
	stableRoot := fsys.stableRoot
	if stableRoot == "" {
		stableRoot = fsys.root
	}
	inspector, err := fsys.localInspector()
	if err != nil {
		return nil, nil, "", err
	}
	if err := inspector.EnsurePath(filepath.Join(stableRoot, local)); err != nil {
		return nil, nil, "", fmt.Errorf("%w: %v", ErrStableReadUnsupported, err)
	}
	file, info, openedRootInfo, err := openStableRegularRead(stableRoot, local)
	if err != nil {
		return nil, nil, "", err
	}
	if fsys.stableRootInfo != nil && (openedRootInfo == nil || !os.SameFile(fsys.stableRootInfo, openedRootInfo)) {
		_ = file.Close()
		return nil, nil, "", ErrNotStableRegularFile
	}
	return file, info, filepath.Join(fsys.root, local), nil
}

func (fsys *FS) localInspector() (*localfs.Inspector, error) {
	if fsys != nil && fsys.inspector != nil {
		return fsys.inspector, nil
	}
	inspector, err := localfs.NewInspector()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStableReadUnsupported, err)
	}
	return inspector, nil
}

// ReopenStableRegularRead reopens relativePath through the same stable-read
// boundary and requires it to name the original inode and metadata. Callers use
// this after a bounded read so a pathname replacement cannot make stale bytes
// look like the current workspace entry.
func (fsys *FS) ReopenStableRegularRead(relativePath string, original fs.FileInfo) (*os.File, fs.FileInfo, error) {
	if original == nil {
		return nil, nil, ErrNotStableRegularFile
	}
	file, current, _, err := fsys.OpenStableRegularRead(relativePath)
	if err != nil {
		return nil, nil, err
	}
	if !os.SameFile(original, current) || current.Mode() != original.Mode() || current.Size() != original.Size() || !current.ModTime().Equal(original.ModTime()) {
		_ = file.Close()
		return nil, nil, ErrNotStableRegularFile
	}
	return file, current, nil
}

func (fsys *FS) ReadDir(relativePath string) ([]DirEntry, string, error) {
	path, err := fsys.Resolve(relativePath)
	if err != nil {
		return nil, "", err
	}
	rootDir, rel, err := fsys.openRootRelative(path)
	if err != nil {
		return nil, "", err
	}
	defer rootDir.Close()
	entries, err := readDirFromRoot(rootDir, rel)
	if err != nil {
		return nil, "", err
	}
	result := make([]DirEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, dirEntryFromDirEntry(entry))
	}
	return result, path, nil
}

func (fsys *FS) WriteFile(relativePath string, data []byte, mode fs.FileMode) (string, error) {
	path, err := fsys.Resolve(relativePath)
	if err != nil {
		return "", err
	}
	rootDir, rel, err := fsys.openRootRelative(path)
	if err != nil {
		return "", err
	}
	defer rootDir.Close()
	if dir := filepath.Dir(rel); dir != "." {
		if err := rootDir.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	if err := rootDir.WriteFile(rel, data, mode); err != nil {
		return "", err
	}
	return path, nil
}

func (fsys *FS) AppendFile(relativePath string, data []byte, mode fs.FileMode) (string, error) {
	path, err := fsys.Resolve(relativePath)
	if err != nil {
		return "", err
	}
	rootDir, rel, err := fsys.openRootRelative(path)
	if err != nil {
		return "", err
	}
	defer rootDir.Close()
	if dir := filepath.Dir(rel); dir != "." {
		if err := rootDir.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	handle, err := rootDir.OpenFile(rel, os.O_CREATE|os.O_WRONLY|os.O_APPEND, mode)
	if err != nil {
		return "", err
	}
	defer handle.Close()
	_, err = handle.Write(data)
	if err != nil {
		return "", err
	}
	return path, nil
}

func (fsys *FS) Remove(relativePath string) (string, error) {
	path, err := fsys.Resolve(relativePath)
	if err != nil {
		return "", err
	}
	rootDir, rel, err := fsys.openRootRelative(path)
	if err != nil {
		return "", err
	}
	defer rootDir.Close()
	if err := rootDir.Remove(rel); err != nil {
		return "", err
	}
	return path, nil
}

func (fsys *FS) WalkDir(relativePath string, visit func(absPath, relPath string, entry DirEntry) error) error {
	return fsys.WalkDirContext(context.Background(), relativePath, visit)
}

// WalkDirContext is the cancellation-aware form of WalkDir. Directories are
// read in bounded batches, and traversal keeps at most one directory handle in
// addition to the workspace root so a deep tree cannot exhaust the process
// descriptor limit. Directory visitation order is intentionally unspecified.
func (fsys *FS) WalkDirContext(ctx context.Context, relativePath string, visit func(absPath, relPath string, entry DirEntry) error) error {
	if fsys == nil {
		return fmt.Errorf("workspace filesystem is not configured")
	}
	if visit == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	local := filepath.FromSlash(relativePath)
	if relativePath == "" || !filepath.IsLocal(local) || filepath.ToSlash(filepath.Clean(local)) != relativePath {
		return fmt.Errorf("unsafe relative workspace path %q", relativePath)
	}
	stableRoot := fsys.stableRoot
	if stableRoot == "" {
		stableRoot = fsys.root
	}
	// Preserve the general WalkDir contract for a file or special-file start
	// path without weakening recursive directory traversal. The API file-tree
	// route always starts at "."; this compatibility branch performs metadata
	// only and rejects existing symlink components before its nonblocking open.
	if local != "." {
		if err := localfs.EnsureBoundedPath(filepath.Join(stableRoot, local)); err != nil {
			return fmt.Errorf("%w: %v", ErrStableReadUnsupported, err)
		}
		if err := RejectExistingSymlinkComponents(stableRoot, local); err != nil {
			return err
		}
		rootDir, err := os.OpenRoot(stableRoot)
		if err != nil {
			return err
		}
		info, openErr := rootDir.Lstat(local)
		rootInfo, rootInfoErr := rootDir.Stat(".")
		_ = rootDir.Close()
		if openErr != nil {
			return openErr
		}
		if fsys.stableRootInfo != nil && (rootInfoErr != nil || !os.SameFile(fsys.stableRootInfo, rootInfo)) {
			return ErrNotStableRegularFile
		}
		if !info.IsDir() {
			visitErr := visit(filepath.Join(fsys.root, local), local, dirEntryFromFileInfo(filepath.Base(local), info))
			if err := ctx.Err(); err != nil {
				return err
			}
			return visitErr
		}
	}
	return fsys.walkRootDir(ctx, stableRoot, local, visit)
}

// SafeJoin resolves relativePath beneath root and rejects path traversal and
// existing symlink components. It intentionally does not require the final path
// to exist so callers can use it for both reads and writes.
func SafeJoin(root, relativePath string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("workspace root is required")
	}
	if !filepath.IsLocal(relativePath) {
		return "", fmt.Errorf("unsafe relative workspace path %q", relativePath)
	}
	root = filepath.Clean(root)
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	target := filepath.Clean(filepath.Join(root, relativePath))
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("workspace path escapes root: %q", relativePath)
	}
	if err := RejectExistingSymlinkComponents(root, rel); err != nil {
		return "", err
	}
	return target, nil
}

func dirEntryFromDirEntry(entry fs.DirEntry) DirEntry {
	result := DirEntry{Name: entry.Name(), Type: entry.Type(), IsDir: entry.IsDir()}
	if info, err := entry.Info(); err == nil {
		result.Size = info.Size()
	}
	return result
}

func dirEntryFromFileInfo(name string, info fs.FileInfo) DirEntry {
	return DirEntry{Name: name, Type: info.Mode().Type(), IsDir: info.IsDir(), Size: info.Size()}
}

func RejectExistingSymlinkComponents(root, relativePath string) error {
	rootDir, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer rootDir.Close()

	current := "."
	for _, segment := range strings.Split(relativePath, string(os.PathSeparator)) {
		if segment == "" || segment == "." {
			continue
		}
		current = filepath.Join(current, segment)
		info, err := rootDir.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w %q", ErrSymlinkComponent, filepath.Join(root, current))
		}
	}
	return nil
}

func readDirFromRoot(rootDir *os.Root, rel string) ([]fs.DirEntry, error) {
	dir, err := rootDir.Open(rel)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return dir.ReadDir(-1)
}

const walkDirBatchSize = 256

func (fsys *FS) walkRootDir(ctx context.Context, stableRoot, rel string, visit func(absPath, relPath string, entry DirEntry) error) error {
	type pendingDirectory struct {
		rel        string
		isRoot     bool
		needsVisit bool
	}

	inspector, err := fsys.localInspector()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStableReadUnsupported, err)
	}
	pending := []pendingDirectory{{rel: rel, isRoot: true, needsVisit: true}}
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		last := len(pending) - 1
		current := pending[last]
		pending = pending[:last]

		name := filepath.Base(current.rel)
		absPath := filepath.Join(fsys.root, current.rel)
		stableAbsPath := filepath.Join(stableRoot, current.rel)
		if current.rel == "." {
			name = "."
			absPath = fsys.root
			stableAbsPath = stableRoot
		}
		if err := inspector.EnsurePath(stableAbsPath); err != nil {
			return fmt.Errorf("%w: %v", ErrStableReadUnsupported, err)
		}
		dir, info, openedRootInfo, err := openStableDirectoryRead(stableRoot, current.rel)
		if err != nil {
			return err
		}
		if fsys.stableRootInfo != nil && (openedRootInfo == nil || !os.SameFile(fsys.stableRootInfo, openedRootInfo)) {
			dir.Close()
			return ErrNotStableRegularFile
		}
		if err := localfs.EnsureBoundedFile(dir); err != nil {
			dir.Close()
			return fmt.Errorf("%w: %v", ErrStableReadUnsupported, err)
		}
		entry := dirEntryFromFileInfo(name, info)
		verifyCurrentDirectory := func() error {
			if fsys.beforeStableDirectoryPostVerify != nil {
				fsys.beforeStableDirectoryPostVerify(current.rel)
			}
			verifyDir, verifyInfo, verifyRootInfo, verifyErr := openStableDirectoryRead(stableRoot, current.rel)
			if verifyErr != nil {
				return verifyErr
			}
			if err := localfs.EnsureBoundedFile(verifyDir); err != nil {
				_ = verifyDir.Close()
				return fmt.Errorf("%w: %v", ErrStableReadUnsupported, err)
			}
			_ = verifyDir.Close()
			if !os.SameFile(info, verifyInfo) || (fsys.stableRootInfo != nil && (verifyRootInfo == nil || !os.SameFile(fsys.stableRootInfo, verifyRootInfo))) {
				return ErrNotStableRegularFile
			}
			return nil
		}
		if current.needsVisit {
			visitErr := visit(absPath, current.rel, entry)
			if err := ctx.Err(); err != nil {
				dir.Close()
				return err
			}
			if visitErr != nil {
				dir.Close()
				switch {
				case visitErr == filepath.SkipDir && entry.IsDir:
					continue
				case visitErr == filepath.SkipDir && !current.isRoot:
					continue
				default:
					return visitErr
				}
			}
		}
		if !entry.IsDir {
			dir.Close()
			continue
		}

		childDirectories := make([]pendingDirectory, 0)
		for {
			if err := ctx.Err(); err != nil {
				dir.Close()
				return err
			}
			entries, readErr := dir.ReadDir(walkDirBatchSize)
			for _, child := range entries {
				if err := ctx.Err(); err != nil {
					dir.Close()
					return err
				}
				childRel := filepath.Join(current.rel, child.Name())
				childAbs := filepath.Join(fsys.root, childRel)
				childStableAbs := filepath.Join(stableRoot, childRel)
				boundedChild := inspector.EnsurePath(childStableAbs) == nil
				childEntry := DirEntry{Name: child.Name(), Type: child.Type(), IsDir: child.IsDir(), TraversalBlocked: !boundedChild}
				if boundedChild {
					childEntry = dirEntryFromDirEntry(child)
				}
				visitErr := visit(childAbs, childRel, childEntry)
				if err := ctx.Err(); err != nil {
					dir.Close()
					return err
				}
				switch {
				case visitErr == nil:
					if childEntry.IsDir && boundedChild {
						childDirectories = append(childDirectories, pendingDirectory{rel: childRel})
					}
				case visitErr == filepath.SkipDir:
					continue
				default:
					dir.Close()
					if verifyErr := verifyCurrentDirectory(); verifyErr != nil {
						return verifyErr
					}
					return visitErr
				}
			}
			switch {
			case readErr == nil:
				continue
			case readErr == io.EOF:
				dir.Close()
			default:
				dir.Close()
				return readErr
			}
			break
		}
		if err := verifyCurrentDirectory(); err != nil {
			return err
		}
		for index := len(childDirectories) - 1; index >= 0; index-- {
			pending = append(pending, childDirectories[index])
		}
	}
	if fsys.stableRootInfo != nil {
		rootDir, rootInfo, _, err := openStableDirectoryRead(stableRoot, ".")
		if err != nil {
			return err
		}
		_ = rootDir.Close()
		if !os.SameFile(fsys.stableRootInfo, rootInfo) {
			return ErrNotStableRegularFile
		}
	}
	return nil
}

func openRootReadNonBlocking(rootDir *os.Root, rel string) (*os.File, fs.FileInfo, error) {
	file, err := rootDir.OpenFile(rel, os.O_RDONLY|nonBlockingReadFlag, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, info, nil
}

func (fsys *FS) openRootRelative(path string) (*os.Root, string, error) {
	rootDir, err := os.OpenRoot(fsys.root)
	if err != nil {
		return nil, "", err
	}
	rel, err := filepath.Rel(fsys.root, path)
	if err != nil {
		rootDir.Close()
		return nil, "", err
	}
	return rootDir, rel, nil
}
