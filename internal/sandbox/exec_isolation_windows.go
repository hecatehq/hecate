//go:build windows

package sandbox

import "os/exec"

// applyProcessIsolation is a no-op on Windows.  Network isolation via the
// Windows Filtering Platform (WFP) requires elevated privileges that the
// sandboxd worker does not hold.  The string-match policy gate in
// validateCommand remains the only network check on this platform.
func applyProcessIsolation(_ *exec.Cmd, _ IsolationConfig) {}
