//go:build windows

package bootstrap

import "os"

func bootstrapFileNeedsModeRepair(mode os.FileMode) bool {
	return false
}
