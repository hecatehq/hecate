//go:build !windows

package agentadapters

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func prepareAgentProcessTree(cmd *exec.Cmd) (attach func() error, release func(), err error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
	return func() error { return nil }, func() {}, nil
}
