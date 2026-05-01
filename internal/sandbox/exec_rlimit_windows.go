//go:build windows

package sandbox

// applyProcessResourceLimits is a no-op on Windows. Full resource isolation
// via Windows Job Objects is tracked as Layer 2 OS-level isolation in
// docs/sandbox.md.
func applyProcessResourceLimits(_ ResourceLimits) {}
