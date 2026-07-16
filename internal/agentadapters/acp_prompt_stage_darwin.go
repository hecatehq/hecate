//go:build darwin

package agentadapters

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"unsafe"

	"golang.org/x/sys/unix"
)

var darwinACLEntryLine = regexp.MustCompile(`(?m)^\s*[0-9]+:`)

func localACPPromptDarwinFilesystem(flags uint32) bool {
	return flags&uint32(unix.MNT_LOCAL) != 0
}

func verifyPrivateACPPromptDarwinFilesystem(file *os.File) error {
	if file == nil {
		return errors.New("private staged prompt input is nil")
	}
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &stat); err != nil {
		return errors.New("inspect private staged prompt input filesystem")
	}
	if !localACPPromptDarwinFilesystem(stat.Flags) {
		return errors.New("private staged prompt input requires a local Darwin filesystem")
	}
	return nil
}

func stripPrivateACPPromptStageACL(file *os.File, _ bool) error {
	if file == nil {
		return errors.New("private staged prompt input is nil")
	}
	if err := verifyPrivateACPPromptDarwinFilesystem(file); err != nil {
		return err
	}
	path, err := darwinACPPromptPathForFile(file)
	if err != nil {
		return errors.New("resolve private staged prompt input for ACL removal")
	}
	info, err := file.Stat()
	if err != nil {
		return errors.New("inspect private staged prompt input identity")
	}
	cmd := exec.Command("/bin/chmod", "-N", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		_ = output
		return errors.New("strip private staged prompt input ACL")
	}
	if err := verifyPathMatchesOpenFile(path, file, info); err != nil {
		return errors.New("private staged prompt input identity changed during ACL removal")
	}
	return verifyPrivateACPPromptStageACL(file, false)
}

func verifyPrivateACPPromptStageACL(file *os.File, _ bool) error {
	if file == nil {
		return errors.New("private staged prompt input is nil")
	}
	if err := verifyPrivateACPPromptDarwinFilesystem(file); err != nil {
		return err
	}
	path, err := darwinACPPromptPathForFile(file)
	if err != nil {
		return errors.New("resolve private staged prompt input for ACL inspection")
	}
	info, err := file.Stat()
	if err != nil {
		return errors.New("inspect private staged prompt input identity")
	}
	cmd := exec.Command("/bin/ls", "-lde", path)
	output, err := cmd.Output()
	if err != nil {
		return errors.New("inspect private staged prompt input ACL")
	}
	if err := verifyPathMatchesOpenFile(path, file, info); err != nil {
		return errors.New("private staged prompt input identity changed during ACL inspection")
	}
	if darwinACLEntryLine.Match(bytes.TrimSpace(output)) {
		return errors.New("private staged prompt input has an extended ACL")
	}
	return nil
}

func darwinACPPromptPathForFile(file *os.File) (string, error) {
	if file == nil {
		return "", errors.New("private staged prompt input is nil")
	}
	buffer := make([]byte, 4096)
	_, _, errno := unix.Syscall(
		unix.SYS_FCNTL,
		file.Fd(),
		uintptr(unix.F_GETPATH),
		uintptr(unsafe.Pointer(&buffer[0])),
	)
	if errno != 0 {
		return "", errno
	}
	end := bytes.IndexByte(buffer, 0)
	if end <= 0 {
		return "", errors.New("private staged prompt input path is unavailable")
	}
	path := string(buffer[:end])
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if err := verifyPathMatchesOpenFile(path, file, info); err != nil {
		return "", err
	}
	return path, nil
}

func renamePrivateACPPromptStageNoReplace(parentFD int, oldName, newName string) error {
	return unix.RenameatxNp(parentFD, oldName, parentFD, newName, unix.RENAME_EXCL)
}
