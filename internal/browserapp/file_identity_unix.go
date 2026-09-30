//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package browserapp

import (
	"fmt"
	"io/fs"
	"syscall"
)

func installationFileID(_ string, info fs.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return "", ErrCandidateChanged
	}
	return fmt.Sprintf("unix:%d:%d", uint64(stat.Dev), uint64(stat.Ino)), nil
}
