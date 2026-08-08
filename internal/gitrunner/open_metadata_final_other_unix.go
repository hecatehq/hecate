//go:build (aix || darwin || dragonfly || freebsd || netbsd || openbsd || solaris) && !linux

package gitrunner

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func openMetadataFinalAt(parentFD int, name string) (int, error) {
	var before unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return -1, err
	}
	if before.Mode&unix.S_IFMT != unix.S_IFREG {
		return -1, fmt.Errorf("Git metadata path is not a regular file")
	}
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return -1, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Dev != before.Dev || stat.Ino != before.Ino {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("Git metadata path changed while it was opened")
	}
	return fd, nil
}
