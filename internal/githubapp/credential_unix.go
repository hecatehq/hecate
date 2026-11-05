//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package githubapp

import (
	"os"

	"golang.org/x/sys/unix"
)

func openCredentialFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrCredential
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, ErrCredential
	}
	return file, nil
}
