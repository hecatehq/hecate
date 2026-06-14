//go:build !windows

package terminal

import (
	"os"
	"strings"
)

func defaultShell() (string, []string, error) {
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return shell, nil, nil
	}
	return "/bin/sh", nil, nil
}
