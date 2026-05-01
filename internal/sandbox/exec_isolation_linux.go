//go:build linux

package sandbox

import (
	"os"
	"os/exec"
	"syscall"
)

// applyProcessIsolation configures OS-level isolation on cmd before it is
// started. On Linux the implementation creates three namespaces:
//
//   - CLONE_NEWNET  — a new, empty network namespace; the process sees no
//     network interfaces, turning network-policy violations from a
//     best-effort string check into a kernel guarantee.
//   - CLONE_NEWUSER — an unprivileged user namespace required to create other
//     namespaces without CAP_SYS_ADMIN.  UID/GID are mapped 1-to-1 so the
//     worker retains its own identity inside the namespace.
//   - CLONE_NEWPID  — a private PID tree; the process cannot signal processes
//     outside the namespace by PID.
//
// Errors are not returned: if namespace creation fails (e.g. kernel built
// without CONFIG_USER_NS, or container runtime that restricts unshare) cmd
// starts without isolation and the string-match policy gate in
// validateCommand remains the only network check.  This matches the
// best-effort contract stated in IsolationConfig.
func applyProcessIsolation(cmd *exec.Cmd, cfg IsolationConfig) {
	if !cfg.DisableNetwork {
		return
	}
	uid := os.Getuid()
	gid := os.Getgid()
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Cloneflags |= syscall.CLONE_NEWNET | syscall.CLONE_NEWUSER | syscall.CLONE_NEWPID
	cmd.SysProcAttr.UidMappings = []syscall.SysProcIDMap{
		{ContainerID: uid, HostID: uid, Size: 1},
	}
	cmd.SysProcAttr.GidMappings = []syscall.SysProcIDMap{
		{ContainerID: gid, HostID: gid, Size: 1},
	}
}
