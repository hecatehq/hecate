//go:build linux

package workspacefs

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// openStableRegularFinalAt probes the final entry without invoking a device
// driver's read-open path, then obtains a readable descriptor for that pinned
// regular inode through procfs. The caller performs the remaining topology and
// filesystem checks on the returned handle.
func openStableRegularFinalAt(parentFD int, name string) (int, error) {
	probeFD, err := unix.Openat(parentFD, name, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, err
	}
	defer unix.Close(probeFD)
	var probe unix.Stat_t
	if err := unix.Fstat(probeFD, &probe); err != nil {
		return -1, err
	}
	if probe.Mode&unix.S_IFMT == unix.S_IFLNK {
		return -1, ErrSymlinkComponent
	}
	if probe.Mode&unix.S_IFMT != unix.S_IFREG {
		return -1, ErrNotStableRegularFile
	}
	readFD, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", probeFD), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return -1, err
	}
	var opened unix.Stat_t
	if err := unix.Fstat(readFD, &opened); err != nil {
		_ = unix.Close(readFD)
		return -1, err
	}
	if opened.Mode&unix.S_IFMT != unix.S_IFREG || opened.Dev != probe.Dev || opened.Ino != probe.Ino {
		_ = unix.Close(readFD)
		return -1, ErrNotStableRegularFile
	}
	return readFD, nil
}
