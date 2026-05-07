package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/hecate/agent-runtime/internal/agentadapters"
)

// ─── SSE event capture ───────────────────────────────────────────────────────
//
// The runtime publishes typed envelopes on /hecate/v1/agent-chat/sessions/{id}/stream.
// These tests connect a real HTTP client, parse the SSE frames, and assert
// approval.requested / approval.resolved arrive when the coordinator drives
// each terminal path (operator approve / cancel / timeout / grant short-circuit
// / mode=auto).

type sseFrame struct {
	Event string
	Data  string
}

// streamCollector connects an SSE client to the session stream and
// returns a channel of frames the test reads from. The cancel func
// closes the connection so the test can finish promptly.
type streamCollector struct {
	frames chan sseFrame
	cancel context.CancelFunc
	done   chan struct{}
}

func startStreamCollector(t *testing.T, baseURL, sessionID string) *streamCollector {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	url := fmt.Sprintf("%s/hecate/v1/agent-chat/sessions/%s/stream", baseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		t.Fatalf("new sse request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("sse request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()
		t.Fatalf("sse status = %d; body=%s", resp.StatusCode, body)
	}

	frames := make(chan sseFrame, 32)
	done := make(chan struct{})
	collector := &streamCollector{frames: frames, cancel: cancel, done: done}

	go func() {
		defer close(done)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		var event, data string
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			case line == "":
				if event != "" || data != "" {
					select {
					case frames <- sseFrame{Event: event, Data: data}:
					case <-ctx.Done():
						return
					}
					event, data = "", ""
				}
			}
			if ctx.Err() != nil {
				return
			}
		}
	}()

	t.Cleanup(func() {
		cancel()
		<-done
	})
	return collector
}

// awaitFrame blocks until an SSE frame matching `eventType` arrives,
// the deadline elapses, or the stream closes. Other event types are
// drained silently — heartbeats and unrelated session updates can
// appear interleaved with approvals.
func (s *streamCollector) awaitFrame(t *testing.T, eventType string, deadline time.Duration) sseFrame {
	t.Helper()
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for {
		select {
		case f, ok := <-s.frames:
			if !ok {
				t.Fatalf("sse stream closed before %q arrived", eventType)
			}
			if f.Event == eventType {
				return f
			}
			// Drain non-matching frames (heartbeats, session updates).
		case <-timer.C:
			t.Fatalf("timed out waiting %s for %q", deadline, eventType)
		}
	}
}

// drainFor collects every frame that arrives within the deadline so a
// test can assert how many of a given event type appeared. Returns
// the slice of all frames seen (not just matching).
func (s *streamCollector) drainFor(deadline time.Duration) []sseFrame {
	collected := make([]sseFrame, 0)
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for {
		select {
		case f, ok := <-s.frames:
			if !ok {
				return collected
			}
			collected = append(collected, f)
		case <-timer.C:
			return collected
		}
	}
}

// ─── approval.requested / approval.resolved happy paths ──────────────────────

func TestSSEPublishesApprovalRequestedAndResolvedOnOperatorApprove(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "s")
	stream := startStreamCollector(t, f.server.URL, "s")

	// Prompt-mode RequestPermission blocks until the operator resolves
	// via HTTP. The coordinator publishes approval.requested as soon
	// as the row lands.
	respCh := make(chan acp.RequestPermissionResponse, 1)
	go func() {
		resp, _ := f.coord.RequestPermission(
			context.Background(),
			agentadapters.RecordingContext{SessionID: "s", AdapterID: "codex", Workspace: "/tmp/w"},
			acp.RequestPermissionRequest{
				Options:  defaultAllowDenyOptions(),
				ToolCall: acp.ToolCallUpdate{ToolCallId: "c"},
			},
		)
		respCh <- resp
	}()

	requested := stream.awaitFrame(t, "approval.requested", 2*time.Second)
	var reqPayload AgentChatApprovalRequestedEvent
	if err := json.Unmarshal([]byte(requested.Data), &reqPayload); err != nil {
		t.Fatalf("decode requested: %v", err)
	}
	if reqPayload.SessionID != "s" || reqPayload.AdapterID != "codex" {
		t.Fatalf("requested payload wrong: %+v", reqPayload)
	}
	if reqPayload.ApprovalID == "" {
		t.Fatal("approval id missing in requested event")
	}

	// Resolve via HTTP. The coordinator publishes approval.resolved
	// from the OnResolved hook fired inside Resolve.
	httpResp := postJSONApprovalEndpoint(t,
		f.server.URL+"/hecate/v1/agent-chat/sessions/s/approvals/"+reqPayload.ApprovalID+"/resolve",
		`{"decision":"approve","scope":"once"}`)
	httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		t.Fatalf("resolve status = %d", httpResp.StatusCode)
	}

	resolved := stream.awaitFrame(t, "approval.resolved", 2*time.Second)
	var resPayload AgentChatApprovalResolvedEvent
	if err := json.Unmarshal([]byte(resolved.Data), &resPayload); err != nil {
		t.Fatalf("decode resolved: %v", err)
	}
	if resPayload.ApprovalID != reqPayload.ApprovalID {
		t.Fatalf("resolved id = %q, want %q", resPayload.ApprovalID, reqPayload.ApprovalID)
	}
	if resPayload.Status != "approved" || resPayload.Decision != "approve" {
		t.Fatalf("resolved status/decision wrong: %+v", resPayload)
	}
	if resPayload.Path != "operator" {
		t.Fatalf("path = %q, want operator", resPayload.Path)
	}

	select {
	case <-respCh:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked RequestPermission did not return after resolve")
	}
}

func TestSSEPublishesResolvedOnOperatorCancel(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "s")
	stream := startStreamCollector(t, f.server.URL, "s")

	go func() {
		_, _ = f.coord.RequestPermission(
			context.Background(),
			agentadapters.RecordingContext{SessionID: "s", AdapterID: "codex"},
			acp.RequestPermissionRequest{Options: defaultAllowDenyOptions(), ToolCall: acp.ToolCallUpdate{ToolCallId: "c"}},
		)
	}()

	requested := stream.awaitFrame(t, "approval.requested", 2*time.Second)
	var reqPayload AgentChatApprovalRequestedEvent
	_ = json.Unmarshal([]byte(requested.Data), &reqPayload)

	httpResp := postJSONApprovalEndpoint(t,
		f.server.URL+"/hecate/v1/agent-chat/sessions/s/approvals/"+reqPayload.ApprovalID+"/cancel", "")
	httpResp.Body.Close()

	resolved := stream.awaitFrame(t, "approval.resolved", 2*time.Second)
	var resPayload AgentChatApprovalResolvedEvent
	_ = json.Unmarshal([]byte(resolved.Data), &resPayload)
	if resPayload.Status != "cancelled" || resPayload.Path != "operator" {
		t.Fatalf("cancel resolved payload wrong: %+v", resPayload)
	}
}

func TestSSEPublishesResolvedOnTimeout(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	// Drop the timeout to a few ms so the test doesn't have to wait
	// for the default 5s. Coordinator timeouts only matter relative
	// to the test deadline.
	f.swapCoordinatorTimeout(t, 30*time.Millisecond)
	f.seedSession(t, "s")
	stream := startStreamCollector(t, f.server.URL, "s")

	go func() {
		_, _ = f.coord.RequestPermission(
			context.Background(),
			agentadapters.RecordingContext{SessionID: "s", AdapterID: "codex"},
			acp.RequestPermissionRequest{Options: defaultAllowDenyOptions(), ToolCall: acp.ToolCallUpdate{ToolCallId: "c"}},
		)
	}()

	stream.awaitFrame(t, "approval.requested", 2*time.Second)
	resolved := stream.awaitFrame(t, "approval.resolved", 2*time.Second)
	var resPayload AgentChatApprovalResolvedEvent
	_ = json.Unmarshal([]byte(resolved.Data), &resPayload)
	if resPayload.Status != "timed_out" || resPayload.Path != "timeout" {
		t.Fatalf("timeout resolved payload wrong: %+v", resPayload)
	}
}

func TestSSEPublishesResolvedOnGrantShortCircuit(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "s")

	// Pre-create a grant; the coordinator's RequestPermission will
	// short-circuit with path=grant before any waiter is registered.
	now := time.Now().UTC()
	if _, err := f.store.CreateGrant(context.Background(), agentadapters.Grant{
		Scope:     agentadapters.ApprovalScopeAdapterTool,
		AdapterID: "codex",
		ToolKind:  "file_write",
		Decision:  agentadapters.ApprovalDecisionApprove,
		GrantedAt: now,
	}); err != nil {
		t.Fatalf("CreateGrant: %v", err)
	}

	stream := startStreamCollector(t, f.server.URL, "s")

	resp, err := f.coord.RequestPermission(
		context.Background(),
		agentadapters.RecordingContext{SessionID: "s", AdapterID: "codex"},
		acp.RequestPermissionRequest{Options: defaultAllowDenyOptions(), ToolCall: acp.ToolCallUpdate{ToolCallId: "c", Kind: toolKindEditPtr()}},
	)
	if err != nil {
		t.Fatalf("RequestPermission: %v", err)
	}
	if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId != "allow_once_id" {
		t.Fatalf("grant short-circuit returned wrong outcome: %+v", resp.Outcome)
	}

	stream.awaitFrame(t, "approval.requested", 2*time.Second)
	resolved := stream.awaitFrame(t, "approval.resolved", 2*time.Second)
	var resPayload AgentChatApprovalResolvedEvent
	_ = json.Unmarshal([]byte(resolved.Data), &resPayload)
	if resPayload.Path != "grant" {
		t.Fatalf("path = %q, want grant", resPayload.Path)
	}
	if resPayload.Status != "approved" {
		t.Fatalf("status = %q, want approved", resPayload.Status)
	}
}

func TestSSEPublishesResolvedOnDefaultModeAuto(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.swapCoordinatorMode(t, agentadapters.ModeAuto)
	f.seedSession(t, "s")
	stream := startStreamCollector(t, f.server.URL, "s")

	if _, err := f.coord.RequestPermission(
		context.Background(),
		agentadapters.RecordingContext{SessionID: "s", AdapterID: "codex"},
		acp.RequestPermissionRequest{Options: defaultAllowDenyOptions(), ToolCall: acp.ToolCallUpdate{ToolCallId: "c"}},
	); err != nil {
		t.Fatalf("RequestPermission: %v", err)
	}

	stream.awaitFrame(t, "approval.requested", 2*time.Second)
	resolved := stream.awaitFrame(t, "approval.resolved", 2*time.Second)
	var resPayload AgentChatApprovalResolvedEvent
	_ = json.Unmarshal([]byte(resolved.Data), &resPayload)
	if resPayload.Path != "default_mode" {
		t.Fatalf("path = %q, want default_mode", resPayload.Path)
	}
}

// ─── Isolation: other-session subscribers don't see this session's events ────

func TestSSEDoesNotLeakApprovalsAcrossSessions(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "session-a")
	f.seedSession(t, "session-b")

	// Subscribe to session A; fire an approval on session B; assert A
	// sees no approval.* events within a short deadline.
	streamA := startStreamCollector(t, f.server.URL, "session-a")

	go func() {
		_, _ = f.coord.RequestPermission(
			context.Background(),
			agentadapters.RecordingContext{SessionID: "session-b", AdapterID: "codex"},
			acp.RequestPermissionRequest{Options: defaultAllowDenyOptions(), ToolCall: acp.ToolCallUpdate{ToolCallId: "c"}},
		)
	}()

	// Drain whatever A receives in 300ms; assert no approval.* frames.
	frames := streamA.drainFor(300 * time.Millisecond)
	for _, f := range frames {
		if strings.HasPrefix(f.Event, "approval.") {
			t.Fatalf("session A saw approval event from session B: %+v", f)
		}
	}
}

// ─── Duplicate-resolved guard ────────────────────────────────────────────────

func TestSSEResolveViaHTTPPublishesExactlyOneResolvedEvenWithActiveWaiter(t *testing.T) {
	t.Parallel()
	// The waiter return path must NOT publish a second event when the
	// operator's HTTP Resolve already published one. Today the
	// coordinator's prompt-mode case `<-w.ch:` returns without calling
	// notifyResolved; this test pins that contract.
	f := newApprovalsHTTPFixture(t)
	f.seedSession(t, "s")
	stream := startStreamCollector(t, f.server.URL, "s")

	go func() {
		_, _ = f.coord.RequestPermission(
			context.Background(),
			agentadapters.RecordingContext{SessionID: "s", AdapterID: "codex"},
			acp.RequestPermissionRequest{Options: defaultAllowDenyOptions(), ToolCall: acp.ToolCallUpdate{ToolCallId: "c"}},
		)
	}()
	requested := stream.awaitFrame(t, "approval.requested", 2*time.Second)
	var reqPayload AgentChatApprovalRequestedEvent
	_ = json.Unmarshal([]byte(requested.Data), &reqPayload)

	httpResp := postJSONApprovalEndpoint(t,
		f.server.URL+"/hecate/v1/agent-chat/sessions/s/approvals/"+reqPayload.ApprovalID+"/resolve",
		`{"decision":"approve","scope":"once"}`)
	httpResp.Body.Close()

	stream.awaitFrame(t, "approval.resolved", 2*time.Second)

	// Drain another short window; assert NO second approval.resolved
	// arrives for the same approval id.
	extra := stream.drainFor(300 * time.Millisecond)
	for _, f := range extra {
		if f.Event == "approval.resolved" {
			t.Fatalf("duplicate approval.resolved: %s", f.Data)
		}
	}
}

// TestSSEResolveVsTimeoutPublishesExactlyOneResolved pins the
// store-race contract for resolveStore. When operator
// HTTP Resolve and prompt-mode timeout fire concurrently, the store's
// atomic UPDATE ... WHERE status='pending' picks one winner; the
// loser's resolveStore returns ErrApprovalAlreadyResolved and the
// caller suppresses notify. Frontends see exactly one approval.resolved.
func TestSSEResolveVsTimeoutPublishesExactlyOneResolved(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	// Short timeout: lose enough that the test can plausibly race
	// HTTP Resolve against the timer firing.
	f.swapCoordinatorTimeout(t, 50*time.Millisecond)
	f.seedSession(t, "s")
	stream := startStreamCollector(t, f.server.URL, "s")

	go func() {
		_, _ = f.coord.RequestPermission(
			context.Background(),
			agentadapters.RecordingContext{SessionID: "s", AdapterID: "codex"},
			acp.RequestPermissionRequest{Options: defaultAllowDenyOptions(), ToolCall: acp.ToolCallUpdate{ToolCallId: "c"}},
		)
	}()
	requested := stream.awaitFrame(t, "approval.requested", 2*time.Second)
	var reqPayload AgentChatApprovalRequestedEvent
	_ = json.Unmarshal([]byte(requested.Data), &reqPayload)

	// Fire HTTP Resolve as close to the timeout as we can. Both code
	// paths call store.ResolveApproval; only one wins. Whichever wins
	// publishes; the loser must suppress.
	go func() {
		httpResp := postJSONApprovalEndpoint(t,
			f.server.URL+"/hecate/v1/agent-chat/sessions/s/approvals/"+reqPayload.ApprovalID+"/resolve",
			`{"decision":"approve","scope":"once"}`)
		httpResp.Body.Close()
	}()

	// Wait for ANY resolved frame, then drain to make sure no second one arrives.
	stream.awaitFrame(t, "approval.resolved", 2*time.Second)
	extra := stream.drainFor(300 * time.Millisecond)
	for _, f := range extra {
		if f.Event == "approval.resolved" {
			t.Fatalf("duplicate approval.resolved on race: %s", f.Data)
		}
	}
}

// ─── Backpressure: slow consumer never blocks the coordinator ────────────────

func TestSSEDropsOnSlowConsumerWithoutBlockingCoordinator(t *testing.T) {
	t.Parallel()
	f := newApprovalsHTTPFixture(t)
	f.swapCoordinatorMode(t, agentadapters.ModeAuto)
	f.seedSession(t, "s")

	updates, unsubscribe := f.handler.agentChatLive.subscribe("s")
	defer unsubscribe()

	// Fill the subscriber buffer and deliberately do not drain it
	// while the coordinator publishes. This simulates a stalled UI and
	// exercises the drop-on-full branch for approval events.
	for i := 0; i < cap(updates); i++ {
		f.handler.agentChatLive.publishApprovalRequested(AgentChatApprovalRequestedEvent{
			ApprovalID: fmt.Sprintf("seed_%d", i),
			SessionID:  "s",
			AdapterID:  "codex",
			CreatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
			ExpiresAt:  time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano),
		})
	}

	// Fire 100 RequestPermission calls back-to-back with a full,
	// undrained subscriber buffer. A slow/stalled UI must NOT block
	// the coordinator's OnRequested / OnResolved hooks. We assert by
	// wall clock that all calls complete well under what a blocking
	// publish would take.
	const n = 100
	var completed atomic.Int32
	var wg sync.WaitGroup
	wg.Add(n)
	deadline := time.Now().Add(3 * time.Second)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, _ = f.coord.RequestPermission(
				context.Background(),
				agentadapters.RecordingContext{SessionID: "s", AdapterID: "codex"},
				acp.RequestPermissionRequest{Options: defaultAllowDenyOptions(), ToolCall: acp.ToolCallUpdate{ToolCallId: "c"}},
			)
			completed.Add(1)
		}()
	}

	doneCh := make(chan struct{})
	go func() { wg.Wait(); close(doneCh) }()
	select {
	case <-doneCh:
	case <-time.After(time.Until(deadline)):
		t.Fatalf("coordinator blocked: only %d/%d RequestPermission calls completed in 3s", completed.Load(), n)
	}
	if completed.Load() != n {
		t.Fatalf("completed = %d, want %d", completed.Load(), n)
	}
	// The point is "didn't block." We don't assert anything about
	// dropped events — a future telemetry counter would, but for now
	// the wall-clock liveness check is the load-bearing assertion.
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// swapCoordinatorMode rebuilds the coordinator with a different mode,
// preserving the same store + bus + telemetry. Lets tests exercise
// auto / deny / prompt without rebuilding the whole fixture.
func (f *approvalsHTTPFixture) swapCoordinatorMode(t *testing.T, mode agentadapters.ApprovalMode) {
	t.Helper()
	f.swapCoordinator(t, mode, 5*time.Second)
}

// swapCoordinatorTimeout is the same as swapCoordinatorMode but
// changes the timeout instead. Tests use a tiny timeout to drive the
// timeout/race paths without slowing the suite.
func (f *approvalsHTTPFixture) swapCoordinatorTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	f.swapCoordinator(t, agentadapters.ModePrompt, timeout)
}

func (f *approvalsHTTPFixture) swapCoordinator(t *testing.T, mode agentadapters.ApprovalMode, timeout time.Duration) {
	t.Helper()
	mgr, ok := f.handler.agentChatRunner.(*agentadapters.SessionManager)
	if !ok {
		t.Fatal("agentChatRunner is not a *SessionManager")
	}
	hooks := buildApprovalCoordinatorHooks(mode, f.handler.approvalConfig.metrics, f.handler.approvalConfig.live)
	coord := agentadapters.NewApprovalCoordinator(agentadapters.CoordinatorOptions{
		Mode:    mode,
		Timeout: timeout,
		Store:   f.store,
		Hooks:   hooks,
	})
	mgr.SetApprovalCoordinator(coord)
	f.coord = coord
}

func toolKindEditPtr() *acp.ToolKind {
	k := acp.ToolKindEdit
	return &k
}

// errorString helper used in tests where errors.Is needs a sentinel.
var _ = errors.Is
