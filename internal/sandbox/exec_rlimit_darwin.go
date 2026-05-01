//go:build darwin

package sandbox

import "syscall"

// applyProcessResourceLimits lowers resource limits on the current process
// (the sandboxd worker) before the shell subprocess is spawned. Because each
// worker handles exactly one command, limiting the worker is equivalent to
// limiting the command it runs: the child inherits all rlimits.
//
// Errors are silently ignored — we can only lower limits, not raise them, and
// a failure means the system hard limit remains in force, which is still safe.
func applyProcessResourceLimits(limits ResourceLimits) {
	if limits.MaxCPUSeconds > 0 {
		_ = syscall.Setrlimit(syscall.RLIMIT_CPU, &syscall.Rlimit{
			Cur: limits.MaxCPUSeconds,
			Max: limits.MaxCPUSeconds,
		})
	}
	if limits.MaxOpenFiles > 0 {
		_ = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &syscall.Rlimit{
			Cur: limits.MaxOpenFiles,
			Max: limits.MaxOpenFiles,
		})
	}
	if limits.MaxAddressSpaceBytes > 0 {
		_ = syscall.Setrlimit(syscall.RLIMIT_AS, &syscall.Rlimit{
			Cur: limits.MaxAddressSpaceBytes,
			Max: limits.MaxAddressSpaceBytes,
		})
	}
}
