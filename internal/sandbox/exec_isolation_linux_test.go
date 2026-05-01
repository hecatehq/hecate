//go:build linux

package sandbox

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// userNamespacesAvailable probes whether the running kernel allows
// unprivileged user namespaces by starting a minimal subprocess inside an
// isolated namespace. Returns false when CLONE_NEWUSER or CLONE_NEWNET is
// denied — common in Docker containers without --privileged and on kernels
// with unprivileged_userns_clone=0 (Debian/Ubuntu before kernel 5.11).
func userNamespacesAvailable() bool {
	cmd := exec.Command("true")
	applyProcessIsolation(cmd, IsolationConfig{DisableNetwork: true})
	return cmd.Run() == nil
}

// TestLocalExecutorDisableNetworkBlocksOutbound is a Linux-only integration
// test that verifies the CLONE_NEWNET namespace actually prevents outbound
// network access.  Inside an empty network namespace the kernel returns
// ENETUNREACH instantly (no interfaces ⇒ no route to any host), so curl
// fails in well under a second even though its --max-time is 2 s.
//
// Skips gracefully when:
//   - user namespaces are unavailable on the current kernel/container, or
//   - curl is not in PATH (unusual in CI but possible in stripped images).
func TestLocalExecutorDisableNetworkBlocksOutbound(t *testing.T) {
	if !userNamespacesAvailable() {
		t.Skip("unprivileged user namespaces unavailable on this kernel — skipping")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not in PATH — skipping")
	}
	t.Parallel()

	ex := NewLocalExecutor()
	start := time.Now()
	_, err := ex.Run(context.Background(), Command{
		// --max-time 2 is a safety net; inside a CLONE_NEWNET namespace
		// the kernel returns ENETUNREACH immediately (no interfaces at
		// all, not even lo), so curl should finish in < 100 ms.
		Command:   `curl -s --max-time 2 http://1.1.1.1`,
		Timeout:   10 * time.Second,
		Isolation: IsolationConfig{DisableNetwork: true},
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Run() returned nil error; expected curl to fail inside an isolated network namespace")
	}
	// A near-instant failure distinguishes "namespace blocked it" from
	// "1.1.1.1 was unreachable for other reasons and hit the 2 s max-time".
	if elapsed > 1*time.Second {
		t.Errorf("command took %v; want < 1s (kernel should return ENETUNREACH immediately in an isolated namespace)", elapsed)
	}
}
