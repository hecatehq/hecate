//go:build windows

package gitrunner

import (
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

func workspaceRootIdentityMaterial(path string, info fs.FileInfo) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(info, after) {
		return "", fmt.Errorf("workspace root identity changed")
	}
	var handleInfo windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &handleInfo); err != nil {
		return "", err
	}
	return fmt.Sprintf("windows:%d:%d:%d", handleInfo.VolumeSerialNumber, handleInfo.FileIndexHigh, handleInfo.FileIndexLow), nil
}
