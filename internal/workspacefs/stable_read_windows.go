//go:build windows

package workspacefs

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hecatehq/hecate/internal/localfs"
	"golang.org/x/sys/windows"
)

// openStableRegularRead uses os.Root so Windows reparse-point traversal cannot
// escape the pinned workspace root. Explicit Lstat checks keep symlinks and
// junction-like path components out of passive previews even when they resolve
// to another location inside the workspace.
func openStableRegularRead(root, relativePath string) (*os.File, fs.FileInfo, fs.FileInfo, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, nil, err
	}
	pinned, err := os.OpenRoot(root)
	if err != nil {
		return nil, nil, nil, err
	}
	defer pinned.Close()
	rootInfo, err := pinned.Stat(".")
	if err != nil {
		return nil, nil, nil, err
	}

	parts := strings.Split(filepath.Clean(relativePath), string(os.PathSeparator))
	prefix := ""
	var before fs.FileInfo
	for index, part := range parts {
		prefix = filepath.Join(prefix, part)
		info, statErr := pinned.Lstat(prefix)
		if statErr != nil {
			return nil, nil, nil, statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, nil, ErrSymlinkComponent
		}
		if index < len(parts)-1 && !info.IsDir() {
			return nil, nil, nil, ErrNotStableRegularFile
		}
		before = info
	}
	if before == nil || !before.Mode().IsRegular() {
		return nil, nil, nil, ErrNotStableRegularFile
	}
	file, err := pinned.Open(relativePath)
	if err != nil {
		return nil, nil, nil, err
	}
	after, statErr := file.Stat()
	current, lstatErr := pinned.Lstat(relativePath)
	if statErr != nil || lstatErr != nil || current.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() || !os.SameFile(before, after) || !os.SameFile(after, current) {
		_ = file.Close()
		return nil, nil, nil, ErrNotStableRegularFile
	}
	var handleInfo windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &handleInfo); err != nil {
		_ = file.Close()
		return nil, nil, nil, err
	}
	// A workspace hardlink may alias a same-volume file outside the workspace.
	// Refuse multiply linked files because the handle API cannot identify every
	// directory entry that names the file without an unbounded volume search.
	if handleInfo.NumberOfLinks != 1 {
		_ = file.Close()
		return nil, nil, nil, ErrNotStableRegularFile
	}
	if err := localfs.EnsureBoundedFile(file); err != nil {
		_ = file.Close()
		return nil, nil, nil, ErrStableReadUnsupported
	}
	return file, after, rootInfo, nil
}

func openStableDirectoryRead(root, relativePath string) (*os.File, fs.FileInfo, fs.FileInfo, error) {
	pinned, err := os.OpenRoot(root)
	if err != nil {
		return nil, nil, nil, err
	}
	defer pinned.Close()
	rootInfo, err := pinned.Stat(".")
	if err != nil {
		return nil, nil, nil, err
	}
	file, err := pinned.Open(relativePath)
	if err != nil {
		return nil, nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, nil, err
	}
	if !info.IsDir() {
		_ = file.Close()
		return nil, nil, nil, ErrNotStableRegularFile
	}
	verify, err := pinned.Open(relativePath)
	if err != nil {
		_ = file.Close()
		return nil, nil, nil, err
	}
	verifyInfo, verifyErr := verify.Stat()
	_ = verify.Close()
	if verifyErr != nil || !verifyInfo.IsDir() || !os.SameFile(info, verifyInfo) {
		_ = file.Close()
		return nil, nil, nil, ErrNotStableRegularFile
	}
	return file, info, rootInfo, nil
}
