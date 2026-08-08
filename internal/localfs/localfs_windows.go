//go:build windows

package localfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

type Inspector struct{}

func NewInspector() (*Inspector, error) {
	return &Inspector{}, nil
}

func EnsureBoundedPath(path string) error {
	return (&Inspector{}).EnsurePath(path)
}

// EnsureBoundedTree verifies the selected Windows volume. Reparse-point
// confinement remains the responsibility of the handle-relative caller; Git
// subprocesses remain bounded by their process deadline.
func EnsureBoundedTree(path string) error {
	return (&Inspector{}).EnsureTree(path)
}

func (*Inspector) EnsurePath(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(absolute)
	if len(volume) != 2 || volume[1] != ':' || strings.HasPrefix(absolute, `\\`) {
		return ErrUnboundedFilesystem
	}
	root, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return err
	}
	switch windows.GetDriveType(root) {
	case windows.DRIVE_FIXED, windows.DRIVE_RAMDISK:
		return nil
	default:
		return fmt.Errorf("%w: drive is not fixed local storage", ErrUnboundedFilesystem)
	}
}

func (inspector *Inspector) EnsureTree(path string) error {
	return inspector.EnsurePath(path)
}

func EnsureBoundedFile(file *os.File) error {
	if file == nil {
		return ErrUnboundedFilesystem
	}
	fileType, err := windows.GetFileType(windows.Handle(file.Fd()))
	if err != nil {
		return err
	}
	if fileType != windows.FILE_TYPE_DISK {
		return ErrUnboundedFilesystem
	}
	return nil
}
