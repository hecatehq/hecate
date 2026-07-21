//go:build windows

package agentadapters

import (
	"context"
	"fmt"
	"os/exec"

	"golang.org/x/sys/windows"
)

func waitAgentProcessExitWithoutReaping(ctx context.Context, cmd *exec.Cmd) error {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		status, err := windows.WaitForSingleObject(handle, 25)
		if err != nil {
			return err
		}
		switch status {
		case windows.WAIT_OBJECT_0:
			return nil
		case uint32(windows.WAIT_TIMEOUT):
			continue
		default:
			return fmt.Errorf("unexpected process wait status %d", status)
		}
	}
}
