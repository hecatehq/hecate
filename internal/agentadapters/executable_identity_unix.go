//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package agentadapters

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

func executableFileIdentity(_ *os.File, info fs.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return "", fmt.Errorf("executable has no stable filesystem identity")
	}
	return fmt.Sprintf("unix:%d:%d", uint64(stat.Dev), uint64(stat.Ino)), nil
}
