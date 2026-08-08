//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package gitrunner

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hecatehq/hecate/internal/localfs"
	"golang.org/x/sys/unix"
)

func openReadOnlyMetadata(path string) (*os.File, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// Darwin exposes a few stable system aliases as root-level symlinks. Resolve
	// only those OS-owned prefixes; arbitrary intermediate symlinks remain
	// rejected by the component-wise O_NOFOLLOW walk below.
	if runtime.GOOS == "darwin" {
		for alias, target := range map[string]string{"/var": "/private/var", "/tmp": "/private/tmp", "/etc": "/private/etc"} {
			if absolute == alias || strings.HasPrefix(absolute, alias+string(os.PathSeparator)) {
				absolute = target + strings.TrimPrefix(absolute, alias)
				break
			}
		}
	}
	if err := localfs.EnsureBoundedPath(absolute); err != nil {
		return nil, err
	}
	currentFD, err := unix.Open(string(os.PathSeparator), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	owned := true
	defer func() {
		if owned {
			_ = unix.Close(currentFD)
		}
	}()
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(absolute), string(os.PathSeparator)), string(os.PathSeparator))
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, fmt.Errorf("unsafe Git metadata path component")
		}
		var nextFD int
		var openErr error
		if index < len(parts)-1 {
			nextFD, openErr = unix.Openat(currentFD, part, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_DIRECTORY, 0)
		} else {
			nextFD, openErr = openMetadataFinalAt(currentFD, part)
		}
		if openErr != nil {
			return nil, openErr
		}
		_ = unix.Close(currentFD)
		currentFD = nextFD
	}
	file := os.NewFile(uintptr(currentFD), absolute)
	if file == nil {
		return nil, fmt.Errorf("open Git metadata %s", path)
	}
	owned = false
	if err := localfs.EnsureBoundedFile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("open bounded local metadata %s: %w", path, err)
	}
	return file, nil
}
