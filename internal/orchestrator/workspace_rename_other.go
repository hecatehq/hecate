//go:build !darwin && !linux && !windows

package orchestrator

import (
	"fmt"
	"os"
	"runtime"
)

func renameRootNoReplace(_ *os.Root, _, _ string) error {
	return fmt.Errorf("atomic no-replace workspace placement is unsupported on %s", runtime.GOOS)
}
