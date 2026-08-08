//go:build aix || android || darwin || dragonfly || freebsd || hurd || illumos || ios || linux || netbsd || openbsd || solaris

package workspacefs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hecatehq/hecate/internal/localfs"
	"golang.org/x/sys/unix"
)

// openStableRegularRead pins every directory component with openat and refuses
// symlinks at every step. This prevents an in-root symlink replacement from
// redirecting a passive preview after lexical validation.
func openStableRegularRead(root, relativePath string) (*os.File, fs.FileInfo, fs.FileInfo, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, nil, err
	}
	rootFD, err := openAbsoluteDirectoryNoFollow(root)
	if err != nil {
		return nil, nil, nil, classifyNoFollowOpenError(err)
	}
	defer unix.Close(rootFD)
	rootInfo, err := fileInfoForFD(rootFD, root)
	if err != nil {
		return nil, nil, nil, err
	}
	parts := strings.Split(filepath.Clean(relativePath), string(os.PathSeparator))
	fileFD, err := openRelativeRegularNoFollow(rootFD, parts)
	if err != nil {
		return nil, nil, nil, classifyNoFollowOpenError(err)
	}
	closeFileFD := true
	defer func() {
		if closeFileFD {
			_ = unix.Close(fileFD)
		}
	}()

	var rootStat unix.Stat_t
	var fileStat unix.Stat_t
	if err := unix.Fstat(rootFD, &rootStat); err != nil {
		return nil, nil, nil, err
	}
	if err := unix.Fstat(fileFD, &fileStat); err != nil {
		return nil, nil, nil, err
	}
	if fileStat.Mode&unix.S_IFMT != unix.S_IFREG || fileStat.Nlink != 1 || fileStat.Dev != rootStat.Dev {
		return nil, nil, nil, fmt.Errorf("%w: mode=%#o links=%d device_matches_root=%t", ErrNotStableRegularFile, fileStat.Mode, fileStat.Nlink, fileStat.Dev == rootStat.Dev)
	}

	// Rewalk from the still-pinned root and require the path to resolve to the
	// same inode. This catches an intermediate directory rename/replacement
	// that occurs while the first component-by-component traversal is open.
	verifyFD, err := openRelativeRegularNoFollow(rootFD, parts)
	if err != nil {
		return nil, nil, nil, classifyNoFollowOpenError(err)
	}
	defer unix.Close(verifyFD)
	var verifyStat unix.Stat_t
	if err := unix.Fstat(verifyFD, &verifyStat); err != nil {
		return nil, nil, nil, err
	}
	if fileStat.Dev != verifyStat.Dev || fileStat.Ino != verifyStat.Ino || verifyStat.Mode&unix.S_IFMT != unix.S_IFREG || verifyStat.Nlink != 1 {
		return nil, nil, nil, fmt.Errorf("%w: path identity changed during open", ErrNotStableRegularFile)
	}

	file := os.NewFile(uintptr(fileFD), filepath.Join(root, relativePath))
	if file == nil {
		return nil, nil, nil, fmt.Errorf("%w: opened descriptor could not become a file", ErrNotStableRegularFile)
	}
	closeFileFD = false
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, nil, fmt.Errorf("%w: opened mode is %s", ErrNotStableRegularFile, info.Mode())
	}
	if err := localfs.EnsureBoundedFile(file); err != nil {
		_ = file.Close()
		return nil, nil, nil, fmt.Errorf("%w: %v", ErrStableReadUnsupported, err)
	}
	return file, info, rootInfo, nil
}

func openStableDirectoryRead(root, relativePath string) (*os.File, fs.FileInfo, fs.FileInfo, error) {
	return openStableDirectoryReadWithHook(root, relativePath, nil)
}

func openStableDirectoryReadWithHook(root, relativePath string, beforeVerify func()) (*os.File, fs.FileInfo, fs.FileInfo, error) {
	rootFD, err := openAbsoluteDirectoryNoFollow(root)
	if err != nil {
		return nil, nil, nil, classifyNoFollowOpenError(err)
	}
	defer unix.Close(rootFD)
	rootInfo, err := fileInfoForFD(rootFD, root)
	if err != nil {
		return nil, nil, nil, err
	}
	currentFD, err := openRelativeDirectoryNoFollow(rootFD, relativePath)
	if err != nil {
		return nil, nil, nil, classifyNoFollowOpenError(err)
	}
	if beforeVerify != nil {
		beforeVerify()
	}
	verifyFD, err := openRelativeDirectoryNoFollow(rootFD, relativePath)
	if err != nil {
		_ = unix.Close(currentFD)
		return nil, nil, nil, classifyNoFollowOpenError(err)
	}
	defer unix.Close(verifyFD)
	var currentStat unix.Stat_t
	var verifyStat unix.Stat_t
	if err := unix.Fstat(currentFD, &currentStat); err != nil {
		_ = unix.Close(currentFD)
		return nil, nil, nil, err
	}
	if err := unix.Fstat(verifyFD, &verifyStat); err != nil {
		_ = unix.Close(currentFD)
		return nil, nil, nil, err
	}
	if currentStat.Dev != verifyStat.Dev || currentStat.Ino != verifyStat.Ino || currentStat.Mode&unix.S_IFMT != unix.S_IFDIR || verifyStat.Mode&unix.S_IFMT != unix.S_IFDIR {
		_ = unix.Close(currentFD)
		return nil, nil, nil, ErrNotStableRegularFile
	}
	file := os.NewFile(uintptr(currentFD), filepath.Join(root, relativePath))
	if file == nil {
		_ = unix.Close(currentFD)
		return nil, nil, nil, ErrNotStableRegularFile
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
	return file, info, rootInfo, nil
}

func openRelativeDirectoryNoFollow(rootFD int, relativePath string) (int, error) {
	currentFD, err := unix.Dup(rootFD)
	if err != nil {
		return -1, err
	}
	for _, part := range strings.Split(filepath.Clean(relativePath), string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		nextFD, openErr := unix.Openat(currentFD, part, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_DIRECTORY|unix.O_NONBLOCK, 0)
		_ = unix.Close(currentFD)
		if openErr != nil {
			return -1, openErr
		}
		currentFD = nextFD
	}
	return currentFD, nil
}

func fileInfoForFD(fd int, name string) (fs.FileInfo, error) {
	copyFD, err := unix.Dup(fd)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(copyFD), name)
	if file == nil {
		_ = unix.Close(copyFD)
		return nil, fmt.Errorf("%w: duplicated root descriptor could not become a file", ErrNotStableRegularFile)
	}
	defer file.Close()
	return file.Stat()
}

func openRelativeRegularNoFollow(rootFD int, parts []string) (resultFD int, returnErr error) {
	currentFD := rootFD
	ownedCurrent := false
	defer func() {
		if returnErr != nil && ownedCurrent {
			_ = unix.Close(currentFD)
		}
	}()
	for index, part := range parts {
		var nextFD int
		var err error
		if index == len(parts)-1 {
			nextFD, err = openStableRegularFinalAt(currentFD, part)
		} else {
			nextFD, err = unix.Openat(currentFD, part, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_DIRECTORY, 0)
		}
		if err != nil {
			return -1, err
		}
		if ownedCurrent {
			_ = unix.Close(currentFD)
		}
		currentFD = nextFD
		ownedCurrent = true
	}
	return currentFD, nil
}

func openAbsoluteDirectoryNoFollow(path string) (int, error) {
	currentFD, err := unix.Open(string(os.PathSeparator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(path), string(os.PathSeparator)), string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		nextFD, openErr := unix.Openat(currentFD, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if openErr != nil {
			_ = unix.Close(currentFD)
			return -1, openErr
		}
		_ = unix.Close(currentFD)
		currentFD = nextFD
	}
	return currentFD, nil
}

func classifyNoFollowOpenError(err error) error {
	if errors.Is(err, unix.ELOOP) {
		return ErrSymlinkComponent
	}
	if errors.Is(err, unix.ENOTDIR) {
		return fmt.Errorf("%w: no-follow open returned %v", ErrNotStableRegularFile, err)
	}
	if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENXIO) {
		return fmt.Errorf("%w: special-file open returned %v", ErrNotStableRegularFile, err)
	}
	return err
}
