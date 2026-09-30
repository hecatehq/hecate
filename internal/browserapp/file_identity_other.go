//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package browserapp

import "io/fs"

func installationFileID(string, fs.FileInfo) (string, error) {
	return "", ErrCandidateChanged
}
