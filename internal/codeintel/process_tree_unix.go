//go:build !windows

package codeintel

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type lspProcessTree struct{}

func prepareLSPProcess(cmd *exec.Cmd) (*lspProcessTree, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0
	tree := &lspProcessTree{}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return tree, nil
}

func (t *lspProcessTree) attach(*exec.Cmd) error { return nil }

func (t *lspProcessTree) forceKill(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (t *lspProcessTree) close() {}
