package browserrunner

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/target"
)

func TestChildTargetGuardObserveFailsClosedForRelatedTarget(t *testing.T) {
	t.Parallel()
	guard := newChildTargetGuard(target.ID("primary"))
	for _, event := range []any{
		nil,
		&target.EventAttachedToTarget{},
		&target.EventAttachedToTarget{TargetInfo: &target.Info{TargetID: target.ID("primary")}},
	} {
		if guard.observe(event) {
			t.Fatalf("observe(%#v) = true, want false", event)
		}
	}
	if !guard.observe(&target.EventAttachedToTarget{TargetInfo: &target.Info{TargetID: target.ID("popup")}}) {
		t.Fatal("observe(child target) = false, want fail-closed")
	}
}

func TestChromiumInspectorDeadlineSpansPreflightAndStartup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the slow browser fixture uses a POSIX shell script")
	}
	const (
		inspectionBudget = 750 * time.Millisecond
		lookupDelay      = 400 * time.Millisecond
		deadlineSlack    = 250 * time.Millisecond
	)
	slowBrowser := filepath.Join(t.TempDir(), "slow-browser")
	if err := os.WriteFile(slowBrowser, []byte("#!/bin/sh\nsleep 5\n"), 0o700); err != nil {
		t.Fatalf("write slow browser fixture: %v", err)
	}

	inspector := &ChromiumInspector{
		executablePath:  slowBrowser,
		timeout:         inspectionBudget,
		allowPrivateIPs: true,
		lookupIPAddrs: func(ctx context.Context, hostname string) ([]net.IPAddr, error) {
			if hostname != "example.test" {
				t.Fatalf("lookup hostname = %q, want example.test", hostname)
			}
			timer := time.NewTimer(lookupDelay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
				return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
			}
		},
	}

	startedAt := time.Now()
	_, err := inspector.Inspect(context.Background(), InspectRequest{
		URL:            "https://example.test/",
		AllowedOrigins: []string{"https://example.test"},
	})
	elapsed := time.Since(startedAt)
	if !errors.Is(err, ErrInspectionFailed) {
		t.Fatalf("Inspect() error = %v, want ErrInspectionFailed", err)
	}
	if elapsed < inspectionBudget-deadlineSlack {
		t.Fatalf("Inspect() returned in %s, want startup to consume the remaining inspection budget", elapsed)
	}
	if elapsed > inspectionBudget+deadlineSlack {
		t.Fatalf("Inspect() took %s, want one %s wall-clock deadline across lookup and startup", elapsed, inspectionBudget)
	}
}

func TestEventMonitorCancelsUnknownLengthResponseAtObservedDataThreshold(t *testing.T) {
	monitor := newEventMonitor(requestPolicy{})
	aborts := 0
	abort := func() { aborts++ }

	// There is deliberately no Content-Length event here: a chunked or
	// otherwise unknown-length response must trigger cancellation once its
	// streamed chunks exceed the observed-data threshold.
	monitor.observeEvent(context.Background(), &network.EventDataReceived{
		DataLength: browserResponseCancellationThresholdBytes - 1,
	}, abort)
	if aborts != 0 {
		t.Fatalf("abort count after in-limit chunk = %d, want 0", aborts)
	}
	monitor.observeEvent(context.Background(), &network.EventDataReceived{
		DataLength: 2,
	}, abort)
	monitor.observeEvent(context.Background(), &network.EventDataReceived{
		DataLength: 1,
	}, abort)
	if aborts != 1 {
		t.Fatalf("abort count after threshold-crossing streamed chunks = %d, want 1", aborts)
	}
	if monitor.responseBytes != browserResponseCancellationThresholdBytes-1 {
		t.Fatalf("accounted response bytes = %d, want %d", monitor.responseBytes, browserResponseCancellationThresholdBytes-1)
	}
}
