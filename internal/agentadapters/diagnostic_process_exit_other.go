//go:build !darwin && !linux && !windows

package agentadapters

import (
	"context"
	"fmt"
	"os/exec"
)

func waitAgentProcessExitWithoutReaping(context.Context, *exec.Cmd) error {
	return fmt.Errorf("safe process-exit observation is unavailable on this platform")
}
