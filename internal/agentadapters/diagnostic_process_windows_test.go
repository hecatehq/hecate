//go:build windows

package agentadapters

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

func waitForAgentDiagnosticProcessExit(t *testing.T, pid int) {
	t.Helper()
	process, err := windows.OpenProcess(
		windows.SYNCHRONIZE|windows.PROCESS_TERMINATE,
		false,
		uint32(pid),
	)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return
	}
	if err != nil {
		t.Fatalf("open diagnostic child %d: %v", pid, err)
	}
	defer windows.CloseHandle(process)
	result, err := windows.WaitForSingleObject(process, 3_000)
	if err != nil {
		t.Fatalf("wait for diagnostic child %d: %v", pid, err)
	}
	if result != windows.WAIT_OBJECT_0 {
		_ = windows.TerminateProcess(process, 1)
		t.Fatalf("diagnostic child %d survived leader exit (wait result %#x)", pid, result)
	}
}
