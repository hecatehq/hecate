//go:build linux

package sandbox

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestGNUTimeoutDefaultEscapesCallingShellProcessGroup(t *testing.T) {
	timeoutPath, err := exec.LookPath("timeout")
	if err != nil {
		t.Skip("GNU timeout is not installed")
	}
	version, err := exec.Command(timeoutPath, "--version").Output()
	if err != nil || !strings.Contains(string(version), "GNU coreutils") {
		t.Skip("installed timeout is not GNU coreutils")
	}
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("ps is not installed")
	}

	cmd := exec.Command("sh", "-c", `printf '%s\n' "$(ps -o pgid= -p $$)"; `+timeoutPath+` 5s sh -c 'ps -o pgid= -p $$'`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("observe timeout process groups: %v", err)
	}
	lines := strings.Fields(string(output))
	if len(lines) != 2 {
		t.Fatalf("process-group output = %q, want outer and managed group", output)
	}
	outer, err := strconv.Atoi(lines[0])
	if err != nil {
		t.Fatalf("parse outer process group %q: %v", lines[0], err)
	}
	managed, err := strconv.Atoi(lines[1])
	if err != nil {
		t.Fatalf("parse managed process group %q: %v", lines[1], err)
	}
	if outer == managed {
		t.Fatalf("GNU timeout managed process group = outer group = %d, expected default timeout separation", outer)
	}
}
