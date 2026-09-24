//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package agentadapters

import (
	"fmt"
	"io/fs"
	"os"
)

func openExecutableIdentityFile(path string) (*os.File, error) {
	return os.Open(path)
}

func executableFileIdentity(_ *os.File, _ fs.FileInfo) (string, error) {
	return "", fmt.Errorf("stable executable file identity is unavailable on this platform")
}
