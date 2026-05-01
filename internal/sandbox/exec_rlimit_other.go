//go:build !linux && !darwin && !windows

package sandbox

// applyProcessResourceLimits is a no-op on platforms other than Linux,
// macOS, and Windows. Add a build-tagged implementation file for any
// additional platform that exposes setrlimit(2).
func applyProcessResourceLimits(_ ResourceLimits) {}
