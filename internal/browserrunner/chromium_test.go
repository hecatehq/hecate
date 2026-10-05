package browserrunner

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

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
		inspectionBudget = 2 * time.Second
		lookupDelay      = 500 * time.Millisecond
		deadlineSlack    = 500 * time.Millisecond
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

func TestBrowserStartupTimeoutUsesRemainingCallBudget(t *testing.T) {
	t.Parallel()
	for _, remaining := range []time.Duration{time.Millisecond, 2 * time.Second, 20 * time.Second, 30 * time.Second} {
		if got := browserStartupTimeout(remaining); got != remaining {
			t.Errorf("browserStartupTimeout(%s) = %s, want remaining call budget", remaining, got)
		}
	}
}

func TestChromiumFlowDeadlineSpansPreflightAndStartup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the slow browser fixture uses a POSIX shell script")
	}
	slowBrowser := filepath.Join(t.TempDir(), "slow-browser")
	if err := os.WriteFile(slowBrowser, []byte("#!/bin/sh\nsleep 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	profileRoot := t.TempDir()
	inspector := &ChromiumInspector{
		executablePath: slowBrowser, timeout: 2 * time.Second, allowPrivateIPs: true,
		profileRoot: profileRoot,
		lookupIPAddrs: func(ctx context.Context, _ string) ([]net.IPAddr, error) {
			timer := time.NewTimer(500 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
				return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
			}
		},
	}
	started := time.Now()
	_, err := inspector.RunFlow(context.Background(), FlowRequest{
		URL: "https://example.test/", AllowedOrigin: "https://example.test",
		Actions: []FlowAction{{Kind: FlowActionWaitFor, Role: "status", Name: "Ready"}},
	})
	elapsed := time.Since(started)
	if !errors.Is(err, ErrInspectionFailed) {
		t.Fatalf("RunFlow() error = %v, want ErrInspectionFailed", err)
	}
	if elapsed < 1500*time.Millisecond || elapsed > 2500*time.Millisecond {
		t.Fatalf("RunFlow() took %s, want one 2s deadline across preflight and startup", elapsed)
	}
	entries, err := os.ReadDir(profileRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("profiles after startup timeout = %v, error = %v, want removed", entries, err)
	}
}

func TestChromiumInspectorSurfacesPathFreeProfileCleanupFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the failing browser fixture uses a POSIX shell script")
	}
	failingBrowser := filepath.Join(t.TempDir(), "failing-browser")
	if err := os.WriteFile(failingBrowser, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatalf("write failing browser fixture: %v", err)
	}
	profileRoot := t.TempDir()
	cleanupCalls := 0
	inspector := &ChromiumInspector{
		executablePath:  failingBrowser,
		timeout:         time.Second,
		allowPrivateIPs: true,
		profileRoot:     profileRoot,
		removeProfile: func(string) error {
			cleanupCalls++
			return errors.New("remove /private/operator/profile: permission denied")
		},
	}

	_, err := inspector.Inspect(context.Background(), InspectRequest{
		URL:            "http://127.0.0.1/",
		AllowedOrigins: []string{"http://127.0.0.1"},
	})
	if !errors.Is(err, ErrProfileCleanupFailed) {
		t.Fatalf("Inspect() error = %v, want ErrProfileCleanupFailed", err)
	}
	if !errors.Is(err, ErrInspectionFailed) {
		t.Fatalf("Inspect() error = %v, want original ErrInspectionFailed preserved", err)
	}
	if cleanupCalls != 3 {
		t.Fatalf("cleanup attempts = %d, want 3", cleanupCalls)
	}
	if strings.Contains(err.Error(), profileRoot) || strings.Contains(err.Error(), "operator") {
		t.Fatalf("cleanup error exposed a local path: %q", err)
	}
}

func TestChromiumProfileDirectoryUsesCanonicalBoundedBase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on some Windows hosts")
	}
	parent := t.TempDir()
	base := filepath.Join(parent, "profiles")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatalf("create profile base: %v", err)
	}
	alias := filepath.Join(parent, "profile-alias")
	if err := os.Symlink(base, alias); err != nil {
		t.Fatalf("create profile-base alias: %v", err)
	}
	inspector := &ChromiumInspector{profileRoot: alias}
	profileDir, err := inspector.createProfileDir("hecate-browser-test-")
	if err != nil {
		t.Fatalf("createProfileDir() error = %v", err)
	}
	defer os.RemoveAll(profileDir)
	canonicalBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatalf("resolve expected canonical base: %v", err)
	}
	if filepath.Dir(profileDir) != canonicalBase {
		t.Fatalf("profile directory parent = %q, want canonical base %q", filepath.Dir(profileDir), canonicalBase)
	}
	info, err := os.Stat(profileDir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("profile directory info = %+v, error=%v, want private directory", info, err)
	}
}

func TestChromiumProfileDirectoryRejectsNonDirectoryBase(t *testing.T) {
	base := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(base, []byte("x"), 0o600); err != nil {
		t.Fatalf("write invalid profile base: %v", err)
	}
	inspector := &ChromiumInspector{profileRoot: base}
	if _, err := inspector.createProfileDir("hecate-browser-test-"); !errors.Is(err, ErrInspectionFailed) {
		t.Fatalf("createProfileDir() error = %v, want ErrInspectionFailed", err)
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

func TestEventMonitorCapsRequestInventory(t *testing.T) {
	t.Parallel()
	monitor := newEventMonitor(requestPolicy{})
	aborts := 0
	for index := 0; index <= maxObservedRequests; index++ {
		monitor.observeEvent(context.Background(), &network.EventRequestWillBeSent{}, func() { aborts++ })
	}
	if aborts != 1 {
		t.Fatalf("abort count = %d, want one cancellation after %d requests", aborts, maxObservedRequests)
	}
	if got := monitor.networkSummary().Requests; got != maxObservedRequests+1 {
		t.Fatalf("request count = %d", got)
	}
}

func TestFlowEventMonitorTreatsBrowserBlockedRequestAsPolicyViolation(t *testing.T) {
	t.Parallel()
	monitor := newFlowEventMonitor(requestPolicy{})
	aborts := 0
	event := &network.EventLoadingFailed{
		RequestID:     network.RequestID("blocked-1"),
		BlockedReason: network.BlockedReasonInspector,
	}
	monitor.observeEvent(context.Background(), event, func() { aborts++ })
	monitor.observeEvent(context.Background(), event, func() { aborts++ })
	if aborts != 1 || !monitor.policyWasViolated() {
		t.Fatalf("flow blocked request aborts=%d policy=%v", aborts, monitor.policyWasViolated())
	}
	if got := monitor.networkSummary().BlockedRequests; got != 1 {
		t.Fatalf("blocked request count = %d, want deduplicated 1", got)
	}
}

func TestFlowEventMonitorTreatsAttachmentResponseAsPolicyViolation(t *testing.T) {
	t.Parallel()
	monitor := newFlowEventMonitor(requestPolicy{})
	aborts := 0
	monitor.observeEvent(context.Background(), &network.EventResponseReceived{Response: &network.Response{
		Headers: network.Headers{"Content-Disposition": `attachment; filename="report.txt"`},
	}}, func() { aborts++ })
	if aborts != 1 || !monitor.policyWasViolated() {
		t.Fatalf("attachment response aborts=%d policy=%v", aborts, monitor.policyWasViolated())
	}

	staticMonitor := newEventMonitor(requestPolicy{})
	staticAborts := 0
	staticMonitor.observeEvent(context.Background(), &network.EventResponseReceived{Response: &network.Response{
		Headers: network.Headers{"content-disposition": "inline"},
	}}, func() { staticAborts++ })
	if staticAborts != 0 || staticMonitor.policyWasViolated() {
		t.Fatalf("static inline response aborts=%d policy=%v", staticAborts, staticMonitor.policyWasViolated())
	}
}

func TestFlowTerminalErrorIncludesEventsObservedDuringTeardown(t *testing.T) {
	t.Parallel()
	monitor := newFlowEventMonitor(requestPolicy{})
	monitor.failClosed(func() {})
	if err := flowTerminalError(monitor, false, nil); !errors.Is(err, ErrInspectionFailed) {
		t.Fatalf("flowTerminalError(generic failure) = %v", err)
	}
	monitor.markPolicyViolation()
	if err := flowTerminalError(monitor, false, nil); !errors.Is(err, ErrFlowPolicyViolation) {
		t.Fatalf("flowTerminalError(policy failure) = %v", err)
	}
	want := errors.New("existing failure")
	if err := flowTerminalError(monitor, false, want); !errors.Is(err, ErrFlowPolicyViolation) {
		t.Fatalf("flowTerminalError(existing + policy) = %v, want policy precedence", err)
	}
	if err := flowTerminalError(newFlowEventMonitor(requestPolicy{}), true, nil); !errors.Is(err, ErrInspectionFailed) {
		t.Fatalf("flowTerminalError(relay failure) = %v, want inspection failure", err)
	}
	policyMonitor := newFlowEventMonitor(requestPolicy{})
	policyMonitor.markPolicyViolation()
	if err := flowTerminalError(policyMonitor, true, nil); !errors.Is(err, ErrFlowPolicyViolation) {
		t.Fatalf("flowTerminalError(relay + policy) = %v, want policy precedence", err)
	}
}

func TestListenerDrainOrdersTerminalPolicyDecisionAfterCallbacks(t *testing.T) {
	t.Parallel()
	monitor := newFlowEventMonitor(requestPolicy{})
	drain := newListenerDrain()
	entered := make(chan struct{})
	release := make(chan struct{})
	listener := drain.wrap(func(any) {
		close(entered)
		<-release
		monitor.markPolicyViolation()
	})
	go listener(struct{}{})
	<-entered

	done := make(chan error, 1)
	go func() {
		drain.stop()
		drain.wait()
		done <- flowTerminalError(monitor, false, nil)
	}()
	select {
	case err := <-done:
		t.Fatalf("listener drain returned before the admitted callback: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-done; !errors.Is(err, ErrFlowPolicyViolation) {
		t.Fatalf("terminal error after listener drain = %v, want policy violation", err)
	}

	calledAfterStop := false
	drain.wrap(func(any) { calledAfterStop = true })(struct{}{})
	if calledAfterStop {
		t.Fatal("listener drain admitted a callback after stop")
	}
}

func TestSanitizeEvidenceTextIsBoundedSingleLineAndControlSafe(t *testing.T) {
	t.Parallel()
	raw := "Report\nActions:\t\x1b[31m\u0085\u202Espoof\u2066" + strings.Repeat("x", 1<<20)
	got := SanitizeEvidenceText(raw)
	if len(got) > maxEvidenceTextBytes || !utf8.ValidString(got) {
		t.Fatalf("sanitized evidence length/UTF-8 = %d/%v", len(got), utf8.ValidString(got))
	}
	if !strings.HasPrefix(got, "Report Actions:") {
		t.Fatalf("sanitized evidence prefix = %q", got[:min(len(got), 64)])
	}
	for _, r := range got {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			t.Fatalf("sanitized evidence retained control/format rune %U", r)
		}
	}
}
