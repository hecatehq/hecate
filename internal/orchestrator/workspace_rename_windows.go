//go:build windows

package orchestrator

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

type workspaceFileRenameInformation struct {
	replaceIfExists uint32
	rootDirectory   windows.Handle
	fileNameLength  uint32
	fileName        [1]uint16
}

func renameRootNoReplace(root *os.Root, oldName, newName string) error {
	parent, err := root.Open(".")
	if err != nil {
		return err
	}
	defer parent.Close()
	parentHandle := windows.Handle(parent.Fd())

	objectName, err := windows.NewNTUnicodeString(oldName)
	if err != nil {
		return err
	}
	objectAttributes := windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: parentHandle,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	var source windows.Handle
	var openStatus windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(
		&source,
		windows.SYNCHRONIZE|windows.DELETE,
		&objectAttributes,
		&openStatus,
		nil,
		0,
		windows.FILE_SHARE_DELETE|windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		0,
		0,
	)
	if err != nil {
		return windowsError(err)
	}
	defer windows.CloseHandle(source)

	newNameUTF16, err := windows.UTF16FromString(newName)
	if err != nil {
		return err
	}
	newNameUTF16 = newNameUTF16[:len(newNameUTF16)-1]
	var header workspaceFileRenameInformation
	buffer := make([]byte, int(unsafe.Offsetof(header.fileName))+len(newNameUTF16)*2)
	info := (*workspaceFileRenameInformation)(unsafe.Pointer(&buffer[0]))
	info.rootDirectory = parentHandle
	info.fileNameLength = uint32(len(newNameUTF16) * 2)
	copy(unsafe.Slice(&info.fileName[0], len(newNameUTF16)), newNameUTF16)

	var renameStatus windows.IO_STATUS_BLOCK
	err = windows.NtSetInformationFile(
		source,
		&renameStatus,
		&buffer[0],
		uint32(len(buffer)),
		windows.FileRenameInformation,
	)
	return windowsError(err)
}

func windowsError(err error) error {
	if status, ok := err.(windows.NTStatus); ok {
		return status.Errno()
	}
	return err
}
