//go:build linux

package gitrunner

import (
	"fmt"
	"os"

	"github.com/hecatehq/hecate/internal/localfs"
	"golang.org/x/sys/unix"
)

func openMetadataFinalAt(parentFD int, name string) (int, error) {
	probeFD, err := unix.Openat(parentFD, name, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, err
	}
	defer unix.Close(probeFD)
	var probeStat unix.Stat_t
	if err := unix.Fstat(probeFD, &probeStat); err != nil {
		return -1, err
	}
	if probeStat.Mode&unix.S_IFMT != unix.S_IFREG {
		return -1, fmt.Errorf("Git metadata path is not a regular file")
	}
	probeFileFD, err := unix.Dup(probeFD)
	if err != nil {
		return -1, err
	}
	probeFile := os.NewFile(uintptr(probeFileFD), name)
	if probeFile == nil {
		_ = unix.Close(probeFileFD)
		return -1, fmt.Errorf("open Git metadata probe")
	}
	defer probeFile.Close()
	if err := localfs.EnsureBoundedFile(probeFile); err != nil {
		return -1, err
	}
	readFD, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", probeFD), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return -1, err
	}
	var readStat unix.Stat_t
	if err := unix.Fstat(readFD, &readStat); err != nil {
		_ = unix.Close(readFD)
		return -1, err
	}
	if readStat.Mode&unix.S_IFMT != unix.S_IFREG || readStat.Dev != probeStat.Dev || readStat.Ino != probeStat.Ino {
		_ = unix.Close(readFD)
		return -1, fmt.Errorf("Git metadata path changed while it was opened")
	}
	return readFD, nil
}
