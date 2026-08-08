//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package gitrunner

import (
	"fmt"
	"io/fs"
	"syscall"
)

func workspaceRootIdentityMaterial(_ string, info fs.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return "", fmt.Errorf("workspace root has no stable filesystem identity")
	}
	return fmt.Sprintf("unix:%d:%d", uint64(stat.Dev), uint64(stat.Ino)), nil
}
