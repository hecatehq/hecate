//go:build windows

package agentadapters

import "os/exec"

func configureCommandProcessGroup(cmd *exec.Cmd) {}
