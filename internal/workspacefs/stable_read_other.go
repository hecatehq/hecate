//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !hurd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows

package workspacefs

import (
	"io/fs"
	"os"
)

func openStableRegularRead(string, string) (*os.File, fs.FileInfo, fs.FileInfo, error) {
	return nil, nil, nil, ErrStableReadUnsupported
}

func openStableDirectoryRead(string, string) (*os.File, fs.FileInfo, fs.FileInfo, error) {
	return nil, nil, nil, ErrStableReadUnsupported
}
