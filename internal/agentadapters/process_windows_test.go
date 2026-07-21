//go:build windows

package agentadapters

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const agentProcessTreeHelperMode = "HECATE_AGENT_PROCESS_TREE_HELPER"
const agentProcessTreePIDFile = "HECATE_AGENT_PROCESS_TREE_PID_FILE"

func TestAgentProcessTreeTerminatesDescendants(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	cmd := exec.Command(os.Args[0], "-test.run=^TestAgentProcessTreeHelperProcess$")
	cmd.Env = append(os.Environ(),
		agentProcessTreeHelperMode+"=parent",
		agentProcessTreePIDFile+"="+pidFile,
	)
	attach, release, err := prepareAgentProcessTree(cmd)
	if err != nil {
		t.Fatalf("prepare process tree: %v", err)
	}
	defer release()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper parent: %v", err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Cancel()
			_ = cmd.Wait()
		}
	}()
	if err := attach(); err != nil {
		t.Fatalf("attach helper parent: %v", err)
	}

	childPID := waitForAgentProcessTreeChildPID(t, pidFile)
	child, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(childPID))
	if err != nil {
		t.Fatalf("open helper child %d: %v", childPID, err)
	}
	defer windows.CloseHandle(child)

	if err := cmd.Cancel(); err != nil {
		t.Fatalf("terminate process tree: %v", err)
	}
	_ = cmd.Wait()
	waited = true
	result, err := windows.WaitForSingleObject(child, 5_000)
	if err != nil {
		t.Fatalf("wait for helper child: %v", err)
	}
	if result != windows.WAIT_OBJECT_0 {
		t.Fatalf("helper child %d survived Job Object termination (wait result %#x)", childPID, result)
	}
}

func TestAgentProcessTreeHelperProcess(t *testing.T) {
	mode := strings.TrimSpace(os.Getenv(agentProcessTreeHelperMode))
	if mode == "" {
		return
	}
	if mode == "parent" {
		child := exec.Command(os.Args[0], "-test.run=^TestAgentProcessTreeHelperProcess$")
		child.Env = append(os.Environ(), agentProcessTreeHelperMode+"=child")
		if err := child.Start(); err != nil {
			panic(fmt.Sprintf("start helper child: %v", err))
		}
		pidFile := os.Getenv(agentProcessTreePIDFile)
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			panic(fmt.Sprintf("write helper child pid: %v", err))
		}
	}
	for {
		time.Sleep(time.Hour)
	}
}

func waitForAgentProcessTreeChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
			if parseErr != nil || pid <= 0 {
				t.Fatalf("parse helper child pid %q: %v", raw, parseErr)
			}
			return pid
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("helper child pid was not written to %s", path)
	return 0
}
