//go:build !windows

package bootstrap

import "os"

func replaceFile(tmpPath, path string) error {
	return os.Rename(tmpPath, path)
}
