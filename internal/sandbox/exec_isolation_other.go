//go:build !linux && !darwin && !windows

package sandbox

import "os/exec"

// applyProcessIsolation is a no-op on platforms other than Linux, macOS,
// and Windows.  The string-match policy gate in validateCommand remains the
// only network check on these platforms.
func applyProcessIsolation(_ *exec.Cmd, _ IsolationConfig) {}
