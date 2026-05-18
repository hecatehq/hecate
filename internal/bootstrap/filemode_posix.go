//go:build !windows

package bootstrap

import "os"

func bootstrapFileNeedsModeRepair(mode os.FileMode) bool {
	return mode&0o077 != 0
}
