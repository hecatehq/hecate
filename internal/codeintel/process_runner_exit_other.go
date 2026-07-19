//go:build !darwin && !linux && !windows

package codeintel

import (
	"context"
	"fmt"
	"os/exec"
)

func waitProcessExitWithoutReaping(context.Context, *exec.Cmd) error {
	return fmt.Errorf("safe process-exit observation is unavailable on this platform")
}
