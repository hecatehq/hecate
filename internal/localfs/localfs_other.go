//go:build !darwin && !linux && !windows

package localfs

import "os"

type Inspector struct{}

func NewInspector() (*Inspector, error) {
	return nil, ErrUnboundedFilesystem
}

func EnsureBoundedPath(string) error {
	return ErrUnboundedFilesystem
}

func EnsureBoundedTree(string) error {
	return ErrUnboundedFilesystem
}

func (*Inspector) EnsurePath(string) error {
	return ErrUnboundedFilesystem
}

func (*Inspector) EnsureTree(string) error {
	return ErrUnboundedFilesystem
}

func EnsureBoundedFile(*os.File) error {
	return ErrUnboundedFilesystem
}
