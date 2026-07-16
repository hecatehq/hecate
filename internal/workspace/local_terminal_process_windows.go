//go:build windows

package workspace

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsTerminalProcessTree struct {
	mu  sync.Mutex
	job windows.Handle
}

type windowsJobBasicAccountingInformation struct {
	totalUserTime             int64
	totalKernelTime           int64
	thisPeriodTotalUserTime   int64
	thisPeriodTotalKernelTime int64
	totalPageFaultCount       uint32
	totalProcesses            uint32
	activeProcesses           uint32
	totalTerminatedProcesses  uint32
}

func prepareTerminalProcessTree(cmd *exec.Cmd) (terminalProcessTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// os/exec does not expose CreateProcess's Job List attribute. Starting
	// suspended closes the otherwise exploitable window between Start and
	// AssignProcessToJobObject: user code cannot spawn an escaping child
	// until the terminal owns the process and explicitly resumes it.
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	return &windowsTerminalProcessTree{job: job}, nil
}

func (t *windowsTerminalProcessTree) attach(cmd *exec.Cmd) error {
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(t.job, process); err != nil {
		return err
	}
	if err := resumeSuspendedTerminalProcess(uint32(cmd.Process.Pid)); err != nil {
		return fmt.Errorf("resume suspended process: %w", err)
	}
	return nil
}

func resumeSuspendedTerminalProcess(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)

	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	for {
		if entry.OwnerProcessID == pid {
			thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err != nil {
				return err
			}
			_, resumeErr := windows.ResumeThread(thread)
			_ = windows.CloseHandle(thread)
			return resumeErr
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return fmt.Errorf("primary thread for process %d not found", pid)
			}
			return err
		}
	}
}

func (t *windowsTerminalProcessTree) terminate() error {
	// Windows has no reliable tree-wide SIGTERM equivalent. Job
	// termination is therefore both the normal and forced stop path.
	return t.forceKill()
}

func (t *windowsTerminalProcessTree) forceKill() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job == 0 {
		return nil
	}
	return windows.TerminateJobObject(t.job, 1)
}

func (t *windowsTerminalProcessTree) wait() {
	ticker := time.NewTicker(terminalProcessTreePollInterval)
	defer ticker.Stop()
	for {
		active, err := t.activeProcessCount()
		if err == nil && active == 0 {
			return
		}
		// Query errors are treated conservatively as still active. Releasing
		// a workspace lease without proving the job empty is less safe than
		// waiting for Close to terminate the owned job.
		<-ticker.C
	}
}

func (t *windowsTerminalProcessTree) activeProcessCount() (uint32, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job == 0 {
		return 0, nil
	}
	var info windowsJobBasicAccountingInformation
	if err := windows.QueryInformationJobObject(
		t.job,
		windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
		nil,
	); err != nil {
		return 0, err
	}
	return info.activeProcesses, nil
}

func (t *windowsTerminalProcessTree) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job == 0 {
		return
	}
	_ = windows.CloseHandle(t.job)
	t.job = 0
}
