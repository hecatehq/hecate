//go:build windows

package browserapp

import (
	"fmt"
	"io/fs"

	"golang.org/x/sys/windows"
)

func installationFileID(path string, _ fs.FileInfo) (string, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	// Read attributes only, not browser bytes. Open the resolved target without
	// following another reparse point and allow ordinary browser updates.
	handle, err := windows.CreateFile(pathPtr, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return "", err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		return "", ErrCandidateChanged
	}
	return fmt.Sprintf("windows:%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}
