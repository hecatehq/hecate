//go:build windows

package terminal

import (
	"errors"
	"os/exec"
)

func defaultShell() (string, []string, error) {
	for _, name := range []string{"pwsh.exe", "powershell.exe", "cmd.exe"} {
		path, err := exec.LookPath(name)
		if err == nil {
			return path, nil, nil
		}
	}
	return "", nil, errors.New("no supported shell found")
}
