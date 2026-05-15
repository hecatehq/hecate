package acp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestDispatcherCall_RoundTripsResultPayload drives the happy path:
// the bridge emits an outbound request, the fake editor replies with
// a JSON result, Call returns that payload.
func TestDispatcherCall_RoundTripsResultPayload(t *testing.T) {
	t.Parallel()
	d := NewDispatcher(nil, NewSessionStore(), Config{})

	emitted := make(chan *Request, 1)
	d.SetEmitter(func(req *Request) { emitted <- req })

	resultCh := make(chan json.RawMessage, 1)
	errCh := make(chan error, 1)
	go func() {
		raw, err := d.Call(context.Background(), "fs/read", map[string]any{
			"path": "README.md",
		})
		errCh <- err
		resultCh <- raw
	}()

	// Editor side: receive the outbound request, send a response
	// with the matching id.
	var req *Request
	select {
	case req = <-emitted:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher never emitted the outbound request")
	}
	if req.Method != "fs/read" {
		t.Fatalf("emitted method = %q; want fs/read", req.Method)
	}
	if req.ID == nil {
		t.Fatal("outbound request must carry an id (not a notification)")
	}

	d.HandleResponse(context.Background(), &Response{
		JSONRPC: JSONRPCVersion,
		ID:      req.ID,
		Result:  json.RawMessage(`{"content":"file body"}`),
	})

	if err := <-errCh; err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	raw := <-resultCh
	if !strings.Contains(string(raw), "file body") {
		t.Fatalf("result = %s; want to contain 'file body'", raw)
	}
}

// TestDispatcherCall_ConvertsRPCErrorToCallError covers the failure
// path: editor replies with an error envelope, Call returns a typed
// *CallError that callers can errors.As against to inspect the code.
func TestDispatcherCall_ConvertsRPCErrorToCallError(t *testing.T) {
	t.Parallel()
	d := NewDispatcher(nil, NewSessionStore(), Config{})

	emitted := make(chan *Request, 1)
	d.SetEmitter(func(req *Request) { emitted <- req })

	errCh := make(chan error, 1)
	go func() {
		_, err := d.Call(context.Background(), "fs/write", map[string]any{"path": "x"})
		errCh <- err
	}()

	req := <-emitted
	d.HandleResponse(context.Background(), &Response{
		JSONRPC: JSONRPCVersion,
		ID:      req.ID,
		Error:   &RPCError{Code: -32603, Message: "permission denied"},
	})

	err := <-errCh
	var callErr *CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("Call error type = %T; want *CallError", err)
	}
	if callErr.Code != -32603 {
		t.Fatalf("CallError.Code = %d; want -32603", callErr.Code)
	}
	if !strings.Contains(callErr.Error(), "permission denied") {
		t.Fatalf("CallError.Error() = %q", callErr.Error())
	}
}

// TestDispatcherCall_HonorsContextCancellation confirms a cancelled
// context releases the caller without waiting for a response that
// may never come — important when an editor crashes mid-RPC.
func TestDispatcherCall_HonorsContextCancellation(t *testing.T) {
	t.Parallel()
	d := NewDispatcher(nil, NewSessionStore(), Config{})

	emitted := make(chan *Request, 1)
	d.SetEmitter(func(req *Request) { emitted <- req })

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := d.Call(ctx, "terminal/create", nil)
		errCh <- err
	}()
	<-emitted // ensure the request was emitted before cancelling

	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v; want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not release Call within 2s")
	}
}

// TestDispatcherCall_ConcurrentCallsRouteByID asserts that two
// concurrent outbound calls each receive their own matching response,
// not each other's. The dispatcher uses per-call channels keyed by
// id; this test pins that invariant.
func TestDispatcherCall_ConcurrentCallsRouteByID(t *testing.T) {
	t.Parallel()
	d := NewDispatcher(nil, NewSessionStore(), Config{})

	type emitted struct{ req *Request }
	emit := make(chan emitted, 4)
	d.SetEmitter(func(req *Request) { emit <- emitted{req} })

	type call struct {
		got json.RawMessage
		err error
	}
	var wg sync.WaitGroup
	wg.Add(2)
	results := make([]call, 2)
	for i := range results {
		i := i
		go func() {
			defer wg.Done()
			raw, err := d.Call(context.Background(), "fs/read", map[string]any{"path": i})
			results[i] = call{got: raw, err: err}
		}()
	}

	// Collect the two emitted requests, reply in reverse order to
	// make sure responses route by id, not by FIFO arrival.
	var first, second emitted
	first = <-emit
	second = <-emit
	d.HandleResponse(context.Background(), &Response{
		JSONRPC: JSONRPCVersion, ID: second.req.ID,
		Result: json.RawMessage(`{"answer":"second"}`),
	})
	d.HandleResponse(context.Background(), &Response{
		JSONRPC: JSONRPCVersion, ID: first.req.ID,
		Result: json.RawMessage(`{"answer":"first"}`),
	})

	wg.Wait()
	// Whichever goroutine emitted `first` should have received
	// `first`'s response and likewise for `second`. We don't know
	// goroutine order so just confirm both completed successfully
	// and got distinct payloads.
	if results[0].err != nil || results[1].err != nil {
		t.Fatalf("Call errors: %v / %v", results[0].err, results[1].err)
	}
	if string(results[0].got) == string(results[1].got) {
		t.Fatalf("concurrent calls got identical payloads %q — responses routed incorrectly", results[0].got)
	}
}

// Sanity guard: ensure permission-flow responses still route to the
// permission handler after the generic-call addition. Routes a
// response carrying a permission id through HandleResponse and
// confirms the pendingPermissions entry is consumed — which only
// happens when the permission branch (not the generic-call branch)
// matched the id.
func TestDispatcherCall_PermissionFlowStillRoutes(t *testing.T) {
	t.Parallel()
	d := NewDispatcher(nil, NewSessionStore(), Config{})

	id, ok := d.trackPendingPermission(PermissionRequestParams{
		SessionID:  "s1",
		TaskID:     "t1",
		RunID:      "r1",
		ApprovalID: "a1",
	})
	if !ok {
		t.Fatal("trackPendingPermission did not accept a fresh request")
	}
	if _, ok := d.takePendingPermission(id); !ok {
		t.Fatal("invariant: the entry should be present before HandleResponse")
	}
	// Re-track so HandleResponse has something to consume — and so
	// we can observe whether it consumed it. Re-tracking the same
	// approvalKey re-uses the existing id (dedupe behavior), which
	// is fine; we only care that the new pending entry is the one
	// HandleResponse takes.
	d.mu.Lock()
	d.pendingPermissions[id] = pendingPermission{SessionID: "s1", TaskID: "t1", RunID: "r1", ApprovalID: "a1"}
	d.mu.Unlock()

	idRaw, _ := json.Marshal(id)
	idMsg := json.RawMessage(idRaw)
	d.HandleResponse(context.Background(), &Response{
		JSONRPC: JSONRPCVersion,
		ID:      &idMsg,
		Error:   &RPCError{Code: -32603, Message: "denied"},
	})

	// After HandleResponse routes the response to the permission
	// branch, the pendingPermissions entry must be gone. If the
	// generic-call branch had grabbed the id instead (a bug),
	// pendingPermissions would still hold it.
	if _, ok := d.takePendingPermission(id); ok {
		t.Fatal("HandleResponse did not consume the permission entry — generic-call branch may have hijacked the id")
	}
}
