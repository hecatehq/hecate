//go:build linux

package agentadapters

import (
	"context"
	"errors"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

func waitAgentProcessExitWithoutReaping(ctx context.Context, cmd *exec.Cmd) error {
	const pollInterval = 10 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var info unix.Siginfo
		err := unix.Waitid(
			unix.P_PID,
			cmd.Process.Pid,
			&info,
			unix.WEXITED|unix.WNOHANG|unix.WNOWAIT,
			nil,
		)
		switch {
		case err == nil && info.Signo != 0:
			return nil
		case err == nil:
		case errors.Is(err, unix.EINTR):
			continue
		default:
			return err
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}
