//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package githubapp

import "os"

func openCredentialFile(string) (*os.File, error) {
	return nil, ErrCredential
}
