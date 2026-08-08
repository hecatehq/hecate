//go:build (aix || android || darwin || dragonfly || freebsd || hurd || illumos || ios || netbsd || openbsd || solaris) && !linux

package workspacefs

import "golang.org/x/sys/unix"

// Platforms without Linux O_PATH first classify the directory entry without
// following it, then retain the caller's post-open inode comparison. This keeps
// static devices and FIFOs out of the read-open path.
func openStableRegularFinalAt(parentFD int, name string) (int, error) {
	var before unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return -1, err
	}
	if before.Mode&unix.S_IFMT == unix.S_IFLNK {
		return -1, ErrSymlinkComponent
	}
	if before.Mode&unix.S_IFMT != unix.S_IFREG {
		return -1, ErrNotStableRegularFile
	}
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return -1, err
	}
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	if opened.Mode&unix.S_IFMT != unix.S_IFREG || opened.Dev != before.Dev || opened.Ino != before.Ino {
		_ = unix.Close(fd)
		return -1, ErrNotStableRegularFile
	}
	return fd, nil
}
