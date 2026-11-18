//go:build windows

package processrunner

import "os/exec"

func configureProcessGroupCancellation(*exec.Cmd) {}
