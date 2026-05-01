//go:build darwin

package sandbox

import (
	"os"
	"os/exec"
)

// sandboxExecPath is the path to the Apple Seatbelt wrapper binary.
// It is present on all macOS versions this codebase targets (deprecated in
// SDK headers but still ships through macOS 14+).
const sandboxExecPath = "/usr/bin/sandbox-exec"

// darwinNetworkDenyProfile is a minimal Seatbelt SBPL profile that denies
// all network operations (outbound, inbound, bind) while leaving everything
// else allowed.  "deny network*" is a wildcard that covers all network
// sub-operations defined by the Seatbelt framework.
const darwinNetworkDenyProfile = `(version 1)(deny network*)(allow default)`

// applyProcessIsolation wraps cmd with sandbox-exec before it is started.
// sandbox-exec applies the Seatbelt profile to the child process and all of
// its descendants, enforcing the network denial at the kernel level.
//
// If the sandbox-exec binary is absent (e.g. the operator stripped it from
// a hardened image) the function silently returns and the command runs
// without OS-level network isolation; the string-match policy gate in
// validateCommand remains the only check.  This matches the best-effort
// contract stated in IsolationConfig.
func applyProcessIsolation(cmd *exec.Cmd, cfg IsolationConfig) {
	if !cfg.DisableNetwork {
		return
	}
	if _, err := os.Stat(sandboxExecPath); err != nil {
		// Binary absent — fall through; best-effort.
		return
	}
	// Prepend sandbox-exec to the argument list.  cmd.Args[0] is
	// conventionally the program name ("sh") so the resulting args are:
	//   ["sandbox-exec", "-p", <profile>, "sh", "-lc", <command>]
	cmd.Args = append([]string{sandboxExecPath, "-p", darwinNetworkDenyProfile}, cmd.Args...)
	cmd.Path = sandboxExecPath
}
