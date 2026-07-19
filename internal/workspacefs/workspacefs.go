package workspacefs

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FS is the canonical path resolver for Hecate-controlled workspace file
// operations. It rejects traversal and existing symlink components so callers
// do not each need to reimplement workspace-boundary checks.
type FS struct {
	root string
}

type DirEntry struct {
	Name  string
	Type  fs.FileMode
	IsDir bool
	Size  int64
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
	return &FS{root: root}, nil
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

// OpenReadNonBlocking opens a workspace path for inspection and returns
// metadata from that same opened handle. On Unix the nonblocking flag prevents
// a concurrent regular-file-to-FIFO replacement from trapping a task in
// open(2); regular files and directories retain ordinary read semantics.
func (fsys *FS) OpenReadNonBlocking(relativePath string) (*os.File, fs.FileInfo, string, error) {
	path, err := fsys.Resolve(relativePath)
	if err != nil {
		return nil, nil, "", err
	}
	rootDir, rel, err := fsys.openRootRelative(path)
	if err != nil {
		return nil, nil, "", err
	}
	defer rootDir.Close()
	file, info, err := openRootReadNonBlocking(rootDir, rel)
	if err != nil {
		return nil, nil, "", err
	}
	return file, info, path, nil
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
	if visit == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	startPath, err := fsys.Resolve(relativePath)
	if err != nil {
		return err
	}
	rootDir, startRel, err := fsys.openRootRelative(startPath)
	if err != nil {
		return err
	}
	defer rootDir.Close()
	return fsys.walkRootDir(ctx, rootDir, startRel, visit)
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
			return fmt.Errorf("workspace path uses symlink component %q", filepath.Join(root, current))
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

func (fsys *FS) walkRootDir(ctx context.Context, rootDir *os.Root, rel string, visit func(absPath, relPath string, entry DirEntry) error) error {
	type pendingDirectory struct {
		rel        string
		isRoot     bool
		needsVisit bool
	}

	pending := []pendingDirectory{{rel: rel, isRoot: true, needsVisit: true}}
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		last := len(pending) - 1
		current := pending[last]
		pending = pending[:last]

		dir, info, err := openRootReadNonBlocking(rootDir, current.rel)
		if err != nil {
			return err
		}
		name := filepath.Base(current.rel)
		absPath := filepath.Join(fsys.root, current.rel)
		if current.rel == "." {
			name = "."
			absPath = fsys.root
		}
		entry := dirEntryFromFileInfo(name, info)
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
				childEntry := dirEntryFromDirEntry(child)
				visitErr := visit(filepath.Join(fsys.root, childRel), childRel, childEntry)
				if err := ctx.Err(); err != nil {
					dir.Close()
					return err
				}
				switch {
				case visitErr == nil:
					if childEntry.IsDir {
						childDirectories = append(childDirectories, pendingDirectory{rel: childRel})
					}
				case visitErr == filepath.SkipDir:
					continue
				default:
					dir.Close()
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
		for index := len(childDirectories) - 1; index >= 0; index-- {
			pending = append(pending, childDirectories[index])
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
