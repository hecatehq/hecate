//go:build windows

package gitrunner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/hecatehq/hecate/internal/localfs"
	"golang.org/x/sys/windows"
)

func openReadOnlyMetadata(path string) (*os.File, error) {
	return openReadOnlyMetadataWithHook(path, nil)
}

func openReadOnlyMetadataWithHook(path string, beforeOpen func()) (*os.File, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := localfs.EnsureBoundedPath(absolute); err != nil {
		return nil, errors.New("Git metadata path must be on a local drive")
	}
	volume := filepath.VolumeName(absolute)
	// Repository-controlled config must not make the Hecate process contact a
	// UNC host or open a device/named-pipe namespace outside the Git sandbox.
	if len(volume) != 2 || volume[1] != ':' || strings.HasPrefix(absolute, `\\`) {
		return nil, errors.New("Git metadata path must be on a local drive")
	}
	root, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return nil, err
	}
	switch windows.GetDriveType(root) {
	case windows.DRIVE_FIXED, windows.DRIVE_RAMDISK:
	default:
		return nil, errors.New("Git metadata path must be on a local drive")
	}
	relative := strings.TrimLeft(absolute[len(volume):], `\/`)
	if relative == "" || !filepath.IsLocal(relative) {
		return nil, errors.New("Git metadata path must name a local regular file")
	}
	// Root.Open resolves every component relative to the already-open drive
	// root using O_NOFOLLOW_ANY on Windows. Unlike a validate-then-open walk,
	// this leaves no pathname race where an intermediate directory can become a
	// junction to a UNC share or device namespace before the final open.
	driveRoot, err := os.OpenRoot(volume + `\`)
	if err != nil {
		return nil, err
	}
	defer driveRoot.Close()
	if beforeOpen != nil {
		beforeOpen()
	}
	file, err := driveRoot.Open(relative)
	if err != nil {
		return nil, err
	}
	if fileType, typeErr := windows.GetFileType(windows.Handle(file.Fd())); typeErr != nil || fileType != windows.FILE_TYPE_DISK {
		_ = file.Close()
		if typeErr != nil {
			return nil, typeErr
		}
		return nil, errors.New("Git metadata path is not a disk file")
	}
	if err := localfs.EnsureBoundedFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
