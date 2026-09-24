//go:build windows

package agentadapters

import (
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

func executableFileIdentity(file *os.File, _ fs.FileInfo) (string, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return "", err
	}
	return fmt.Sprintf("windows:%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}
