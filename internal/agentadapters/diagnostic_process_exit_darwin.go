//go:build darwin

package agentadapters

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

func waitAgentProcessExitWithoutReaping(ctx context.Context, cmd *exec.Cmd) error {
	kqueue, err := unix.Kqueue()
	if err != nil {
		return err
	}
	defer unix.Close(kqueue)

	change := unix.Kevent_t{
		Ident:  uint64(cmd.Process.Pid),
		Filter: unix.EVFILT_PROC,
		Flags:  unix.EV_ADD | unix.EV_ONESHOT,
		Fflags: unix.NOTE_EXIT,
	}
	if _, err := unix.Kevent(kqueue, []unix.Kevent_t{change}, nil, nil); errors.Is(err, unix.ESRCH) {
		// A short-lived child can exit before registration. It remains
		// unreaped, so its PID cannot have been reused.
		return nil
	} else if err != nil {
		return err
	}

	events := make([]unix.Kevent_t, 1)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		timeout := unix.NsecToTimespec((25 * time.Millisecond).Nanoseconds())
		count, err := unix.Kevent(kqueue, nil, events, &timeout)
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case err != nil:
			return err
		case count == 0:
			continue
		case events[0].Flags&unix.EV_ERROR != 0 && events[0].Data == int64(unix.ESRCH):
			return nil
		case events[0].Flags&unix.EV_ERROR != 0:
			return fmt.Errorf("kqueue process-exit event failed with errno %d", events[0].Data)
		default:
			return nil
		}
	}
}
