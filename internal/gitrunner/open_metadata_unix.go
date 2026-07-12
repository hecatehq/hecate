//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package gitrunner

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openReadOnlyMetadata(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open %s", path)
	}
	return file, nil
}
