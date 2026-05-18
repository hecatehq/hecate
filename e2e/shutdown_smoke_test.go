//go:build e2e

// Package e2e — gateway graceful-shutdown smoke.
//
// Catches the wiring gap between the API handler and cmd/hecate/main.go:
// unit tests (internal/api/server_test.go) verify the handler hands its
// quit signal off to the closure passed to SetQuitFunc, but they don't
// run main.go's select { case <-quit }. If a refactor silently drops
// the channel from the select — or wires it to a no-op — every unit
// test still passes, and only an integration test that POSTs and then
// observes the actual process exit catches the regression.
//
// The Tauri close-window flow uses POST /hecate/v1/system/shutdown
// for exactly this purpose: drive the same SIGINT/SIGTERM drain path
// without sending an OS signal to the child. This test exercises that
// contract.
//
// Cheap (~2s) and cross-platform — runs in `just verify` alongside the
// non-tagged e2e suite.
//
// Run with: go test -tags e2e -count=1 ./e2e/ -run TestSystemShutdownSmoke

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestSystemShutdownSmokeExitsCleanly verifies the full
// endpoint → main.go select → drain path:
//   - POST /hecate/v1/system/shutdown returns 202 with the documented body.
//   - The gateway process exits within a reasonable deadline (5s; the
//     drain has its own 10s budget but on an idle gateway it finishes
//     in milliseconds).
//   - Exit code is 0 — the gateway returned from main, not SIGKILL'd.
//   - The "gateway shutting down" log line carries
//     trigger=system_shutdown_endpoint, proving the quit channel
//     reached main.go's select (and not a stray signal handler).
func TestSystemShutdownSmokeExitsCleanly(t *testing.T) {
	bin := hecateBinary(t)
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	baseURL := "http://" + addr

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"GATEWAY_ADDRESS="+addr,
		"GATEWAY_DATA_DIR="+t.TempDir(),
	)
	output := newTailBuffer(64 * 1024)
	cmd.Stdout = output
	cmd.Stderr = output

	if err := cmd.Start(); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	// Belt-and-suspenders: if the test fails before the graceful path
	// completes, kill the process so the test binary doesn't hang.
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	waitHealthyProcess(t, baseURL, gatewayStartupTimeout, cmd, output)

	// POST /system/shutdown and verify the 202 + documented body.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, baseURL+"/hecate/v1/system/shutdown", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /system/shutdown: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /system/shutdown status = %d, want 202\n--- gateway output ---\n%s", resp.StatusCode, output.String())
	}
	body := readBody(t, resp)
	if !strings.Contains(body, `"object":"system_shutdown"`) {
		t.Errorf("response body = %q, want object=system_shutdown", body)
	}

	// Wait for the gateway to actually exit. The shutdown handler fires
	// the quit signal ~50ms after returning 202, then main.go's drain
	// runs with a 10s budget; on an idle gateway the whole thing is
	// well under a second.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	const exitDeadline = 5 * time.Second
	select {
	case err := <-done:
		if err != nil {
			// Non-nil error here means a non-zero exit code, a signal,
			// or some other abnormal termination. Anything but a clean
			// main-returned-0 is a regression.
			t.Fatalf("gateway exited with error after /system/shutdown: %v\n--- gateway output ---\n%s", err, output.String())
		}
	case <-time.After(exitDeadline):
		t.Fatalf("gateway did not exit within %s after /system/shutdown — main.go may not be selecting on the quit channel\n--- gateway output ---\n%s", exitDeadline, output.String())
	}

	// Verify the shutdown trigger came through the quit endpoint, not
	// some stray signal. cmd/hecate/main.go logs the trigger when it
	// drops out of its select, so the log carries the source.
	logs := output.String()
	if !strings.Contains(logs, "gateway shutting down") {
		t.Errorf("missing 'gateway shutting down' log line\n--- gateway output ---\n%s", logs)
	}
	if !strings.Contains(logs, "trigger=system_shutdown_endpoint") &&
		!strings.Contains(logs, `"trigger":"system_shutdown_endpoint"`) {
		// Accept either logfmt or JSON output shape since slog's
		// handler is configurable; both forms encode the same field.
		t.Errorf("shutdown trigger is not system_shutdown_endpoint — the quit channel may have been replaced or a signal beat it to the select\n--- gateway output ---\n%s", logs)
	}
}
