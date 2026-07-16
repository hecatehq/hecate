//go:build darwin

package orchestrator

import (
	"os"

	"golang.org/x/sys/unix"
)

func renameRootNoReplace(root *os.Root, oldName, newName string) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	fd := int(directory.Fd())
	return unix.RenameatxNp(fd, oldName, fd, newName, unix.RENAME_EXCL)
}
