//go:build windows

package gitrunner

import "os"

func openReadOnlyMetadata(path string) (*os.File, error) {
	return os.Open(path)
}
