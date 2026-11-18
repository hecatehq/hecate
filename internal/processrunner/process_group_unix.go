//go:build !windows

package processrunner

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureProcessGroupCancellation(cmd *exec.Cmd) {
	// Establish the group before exec, so cancellation also covers wrapper
	// startup before the wrapper has installed its parent-death handlers.
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
}
