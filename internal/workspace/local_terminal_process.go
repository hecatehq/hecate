package workspace

import (
	"os/exec"
	"time"
)

const terminalProcessTreePollInterval = 10 * time.Millisecond

// terminalProcessTree owns the OS containment primitive for a terminal.
// attach must finish before OpenTerminal publishes the handle; wait returns
// only after the command leader and every contained descendant have exited.
type terminalProcessTree interface {
	attach(*exec.Cmd) error
	terminate() error
	forceKill() error
	wait()
	close()
}
