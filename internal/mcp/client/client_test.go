package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/mcp"
)

// memTransport is an in-memory Transport for tests. Frames flow
// through buffered channels; "the server" is a goroutine the test
// owns that reads from sendCh and writes to recvCh. This lets us
// drive the full Client request/response lifecycle without
// spawning a real subprocess.
type memTransport struct {
	sendCh chan []byte // frames written by Client → consumed by fake server
	recvCh chan []byte // frames written by fake server → consumed by Client
	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

func newMemTransport() *memTransport {
	return &memTransport{
		sendCh: make(chan []byte, 8),
		recvCh: make(chan []byte, 8),
		done:   make(chan struct{}),
	}
}

func (m *memTransport) Send(ctx context.Context, frame []byte) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrTransportClosed
	}
	m.mu.Unlock()
	// Copy so the caller can mutate `frame` without racing.
	cp := append([]byte(nil), frame...)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.done:
		return ErrTransportClosed
	case m.sendCh <- cp:
		return nil
	}
}

func (m *memTransport) Recv(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.done:
		return nil, ErrTransportClosed
	case frame, ok := <-m.recvCh:
		if !ok {
			return nil, io.EOF
		}
		return frame, nil
	}
}

func (m *memTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	close(m.done)
	// Close both directions so the fake server's read loop exits
	// and any pending Recv on the client side wakes up. Without
	// closing sendCh, a fake-server goroutine stuck inside a
	// hanging handler can prevent test cleanup.
	close(m.sendCh)
	close(m.recvCh)
	return nil
}

// fakeServer reads requests off sendCh, dispatches to the handler
// map, and writes responses to recvCh. Notifications (no ID) are
// passed to onNotification (or dropped if nil). The server stops
// when sendCh closes (transport closure) OR when the handler
// returns shouldStop=true (used to simulate mid-request crashes).
type fakeServer struct {
	t              *testing.T
	transport      *memTransport
	handlers       map[string]func(req mcp.Request) (any, *mcp.RPCError)
	onNotification func(method string, params json.RawMessage)
	wg             sync.WaitGroup
}

func newFakeServer(t *testing.T, transport *memTransport) *fakeServer {
	t.Helper()
	return &fakeServer{
		t:         t,
		transport: transport,
		handlers:  map[string]func(req mcp.Request) (any, *mcp.RPCError){},
	}
}

func (s *fakeServer) handle(method string, fn func(req mcp.Request) (any, *mcp.RPCError)) {
	s.handlers[method] = fn
}

func (s *fakeServer) start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for frame := range s.transport.sendCh {
			var req mcp.Request
			if err := json.Unmarshal(frame, &req); err != nil {
				s.t.Errorf("fake server received unparseable frame: %s (%v)", frame, err)
				continue
			}
			if req.IsNotification() {
				if s.onNotification != nil {
					s.onNotification(req.Method, req.Params)
				}
				continue
			}
			// Detach handler dispatch so a "never respond" handler
			// (used by close-unblock tests) doesn't block the
			// loop from servicing other requests or exiting on
			// transport close. Tests that rely on handler order
			// can use a sync.Mutex inside the handler.
			h, ok := s.handlers[req.Method]
			if !ok {
				s.respond(req, nil, mcp.NewError(mcp.ErrCodeMethodNotFound, "no handler: "+req.Method))
				continue
			}
			go func(req mcp.Request, h func(mcp.Request) (any, *mcp.RPCError)) {
				result, rpcErr := h(req)
				s.respond(req, result, rpcErr)
			}(req, h)
		}
	}()
}

// stop waits only for the dispatch loop to exit (which happens
// when the transport's sendCh closes). Detached handler goroutines
// running `select {}` in never-respond tests are intentionally
// leaked — Go's test runtime reaps them on process exit.
func (s *fakeServer) stop() { s.wg.Wait() }

func (s *fakeServer) respond(req mcp.Request, result any, rpcErr *mcp.RPCError) {
	resp := mcp.Response{JSONRPC: "2.0", ID: req.ID}
	if rpcErr != nil {
		resp.Error = rpcErr
	} else {
		raw, err := json.Marshal(result)
		if err != nil {
			s.t.Errorf("fake server: marshal result: %v", err)
			return
		}
		resp.Result = raw
	}
	frame, err := json.Marshal(resp)
	if err != nil {
		s.t.Errorf("fake server: marshal response: %v", err)
		return
	}
	// Best-effort send — if the client has Closed, recvCh is
	// closed and we drop the write.
	defer func() { _ = recover() }()
	s.transport.recvCh <- frame
}

// TestClient_InitializeHandshake exercises the full initialize
// path: request → response → initialized notification. Pin both
// the response decoding and that the notification follows in the
// expected order.
func TestClient_InitializeHandshake(t *testing.T) {
	t.Parallel()
	transport := newMemTransport()
	server := newFakeServer(t, transport)
	notified := make(chan struct{})
	server.onNotification = func(method string, _ json.RawMessage) {
		if method == "notifications/initialized" {
			close(notified)
		}
	}
	server.handle("initialize", func(req mcp.Request) (any, *mcp.RPCError) {
		var params mcp.InitializeParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, mcp.NewError(mcp.ErrCodeInvalidParams, err.Error())
		}
		if params.ClientInfo.Name != "hecate-test" {
			t.Errorf("ClientInfo.Name = %q, want hecate-test", params.ClientInfo.Name)
		}
		return mcp.InitializeResult{
			ProtocolVersion: declaredClientProtocolVersion,
			Capabilities:    mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{}},
			ServerInfo: mcp.ServerInfo{
				Name: "test-mcp", Version: "0.0.1",
				Description: "fake server for client tests",
			},
		}, nil
	})
	server.start()

	c := New(transport, mcp.ClientInfo{Name: "hecate-test", Version: "0.0.0"})
	t.Cleanup(func() { _ = c.Close(); server.stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := c.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if res.ServerInfo.Name != "test-mcp" {
		t.Errorf("ServerInfo.Name = %q, want test-mcp", res.ServerInfo.Name)
	}
	if res.Capabilities.Tools == nil {
		t.Error("Capabilities.Tools is nil; server declared support")
	}
	select {
	case <-notified:
	case <-time.After(time.Second):
		t.Fatal("server never received notifications/initialized")
	}
}

// TestClient_InitializeIsCachedAcrossCalls — Initialize is a
// once-only handshake. Repeated calls return the cached result
// without re-handshaking; the spec doesn't allow re-init on a
// live connection.
func TestClient_InitializeIsCachedAcrossCalls(t *testing.T) {
	t.Parallel()
	transport := newMemTransport()
	server := newFakeServer(t, transport)
	var initCalls int
	var mu sync.Mutex
	server.handle("initialize", func(req mcp.Request) (any, *mcp.RPCError) {
		mu.Lock()
		initCalls++
		mu.Unlock()
		return mcp.InitializeResult{
			ProtocolVersion: declaredClientProtocolVersion,
			ServerInfo:      mcp.ServerInfo{Name: "x", Version: "0"},
		}, nil
	})
	server.start()
	c := New(transport, mcp.ClientInfo{Name: "h", Version: "0"})
	t.Cleanup(func() { _ = c.Close(); server.stop() })

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize 1: %v", err)
	}
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize 2: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if initCalls != 1 {
		t.Errorf("server saw %d initialize calls, want 1 (Initialize must be idempotent)", initCalls)
	}
}

// TestClient_ListToolsAndCallTool exercises the post-handshake
// happy path: list the server's tools, invoke one, get a typed
// CallToolResult back.
func TestClient_ListToolsAndCallTool(t *testing.T) {
	t.Parallel()
	transport := newMemTransport()
	server := newFakeServer(t, transport)
	server.handle("initialize", func(_ mcp.Request) (any, *mcp.RPCError) {
		return mcp.InitializeResult{ProtocolVersion: declaredClientProtocolVersion, ServerInfo: mcp.ServerInfo{Name: "x"}}, nil
	})
	server.handle("tools/list", func(_ mcp.Request) (any, *mcp.RPCError) {
		return mcp.ListToolsResult{Tools: []mcp.Tool{
			{Name: "echo", Title: "Echo", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "time", InputSchema: json.RawMessage(`{"type":"object"}`)},
		}}, nil
	})
	server.handle("tools/call", func(req mcp.Request) (any, *mcp.RPCError) {
		var params mcp.CallToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, mcp.NewError(mcp.ErrCodeInvalidParams, err.Error())
		}
		if params.Name != "echo" {
			return nil, mcp.NewError(mcp.ErrCodeMethodNotFound, "no such tool")
		}
		var args struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(params.Arguments, &args)
		return mcp.CallToolResult{Content: mcp.TextContent("echo: " + args.Text)}, nil
	})
	server.start()

	c := New(transport, mcp.ClientInfo{Name: "h", Version: "0"})
	t.Cleanup(func() { _ = c.Close(); server.stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "echo" || tools[1].Name != "time" {
		t.Fatalf("tools = %+v, want [echo, time]", tools)
	}
	res, err := c.CallTool(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Content) != 1 || res.Content[0].Text != "echo: hi" {
		t.Fatalf("CallTool result = %+v, want echo: hi", res)
	}
	if res.IsError {
		t.Errorf("IsError = true on a happy-path result")
	}
}

// TestClient_CallToolSurfacesIsError — tool-level failures arrive
// as CallToolResult.IsError=true (NOT as a JSON-RPC error). The
// model uses this to decide whether to retry or give up.
func TestClient_CallToolSurfacesIsError(t *testing.T) {
	t.Parallel()
	transport := newMemTransport()
	server := newFakeServer(t, transport)
	server.handle("initialize", func(_ mcp.Request) (any, *mcp.RPCError) {
		return mcp.InitializeResult{ProtocolVersion: declaredClientProtocolVersion}, nil
	})
	server.handle("tools/call", func(_ mcp.Request) (any, *mcp.RPCError) {
		return mcp.CallToolResult{
			IsError: true,
			Content: mcp.TextContent("file not found: /etc/missing"),
		}, nil
	})
	server.start()
	c := New(transport, mcp.ClientInfo{Name: "h"})
	t.Cleanup(func() { _ = c.Close(); server.stop() })

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	res, err := c.CallTool(ctx, "read_file", json.RawMessage(`{"path":"/etc/missing"}`))
	if err != nil {
		t.Fatalf("CallTool returned a Go error for a tool-level failure: %v", err)
	}
	if !res.IsError {
		t.Error("IsError = false; want true for a tool-level failure")
	}
	if len(res.Content) == 0 || res.Content[0].Text == "" {
		t.Error("Content empty; expected the failure text")
	}
}

// TestClient_CallToolSurfacesRPCError — JSON-RPC protocol errors
// (the call itself failed to dispatch — unknown method, invalid
// params at the server) come back as a non-nil error wrapping
// *mcp.RPCError so callers can inspect Code via errors.As.
func TestClient_CallToolSurfacesRPCError(t *testing.T) {
	t.Parallel()
	transport := newMemTransport()
	server := newFakeServer(t, transport)
	server.handle("initialize", func(_ mcp.Request) (any, *mcp.RPCError) {
		return mcp.InitializeResult{ProtocolVersion: declaredClientProtocolVersion}, nil
	})
	server.handle("tools/call", func(_ mcp.Request) (any, *mcp.RPCError) {
		return nil, mcp.NewError(mcp.ErrCodeInvalidParams, "bad arguments")
	})
	server.start()
	c := New(transport, mcp.ClientInfo{Name: "h"})
	t.Cleanup(func() { _ = c.Close(); server.stop() })

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	_, err := c.CallTool(ctx, "x", nil)
	if err == nil {
		t.Fatal("CallTool err = nil, want JSON-RPC error")
	}
	var rpcErr *mcp.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("err = %v, want *mcp.RPCError in chain", err)
	}
	if rpcErr.Code != mcp.ErrCodeInvalidParams {
		t.Errorf("RPCError.Code = %d, want ErrCodeInvalidParams", rpcErr.Code)
	}
}

// TestClient_ConcurrentCallsCorrelateByID — the request demuxer
// must route responses by ID even when many requests are
// in-flight. We fire 10 calls concurrently and verify each gets
// the right response back (server replies with a marker derived
// from the request).
func TestClient_ConcurrentCallsCorrelateByID(t *testing.T) {
	t.Parallel()
	transport := newMemTransport()
	server := newFakeServer(t, transport)
	server.handle("initialize", func(_ mcp.Request) (any, *mcp.RPCError) {
		return mcp.InitializeResult{ProtocolVersion: declaredClientProtocolVersion}, nil
	})
	server.handle("tools/call", func(req mcp.Request) (any, *mcp.RPCError) {
		var params mcp.CallToolParams
		_ = json.Unmarshal(req.Params, &params)
		// Echo back the tool name in the result text so the
		// caller can verify their response matched their request.
		return mcp.CallToolResult{Content: mcp.TextContent("ok:" + params.Name)}, nil
	})
	server.start()
	c := New(transport, mcp.ClientInfo{Name: "h"})
	t.Cleanup(func() { _ = c.Close(); server.stop() })

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	const N = 10
	results := make([]string, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "tool-" + string(rune('a'+i))
			res, err := c.CallTool(ctx, name, nil)
			if err != nil {
				t.Errorf("call %d: %v", i, err)
				return
			}
			if len(res.Content) > 0 {
				results[i] = res.Content[0].Text
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < N; i++ {
		want := "ok:tool-" + string(rune('a'+i))
		if results[i] != want {
			t.Errorf("results[%d] = %q, want %q (correlation broke)", i, results[i], want)
		}
	}
}

// TestClient_CloseUnblocksPendingCalls — after Close, any in-flight
// CallTool returns ErrClientClosed rather than hanging. Critical
// for clean shutdown when an MCP server stops responding.
func TestClient_CloseUnblocksPendingCalls(t *testing.T) {
	t.Parallel()
	transport := newMemTransport()
	server := newFakeServer(t, transport)
	server.handle("initialize", func(_ mcp.Request) (any, *mcp.RPCError) {
		return mcp.InitializeResult{ProtocolVersion: declaredClientProtocolVersion}, nil
	})
	// Don't register tools/call — calls will hang forever (server
	// never responds), giving us a stable target for the
	// close-unblock test.
	gotCall := make(chan struct{})
	server.handle("tools/call", func(_ mcp.Request) (any, *mcp.RPCError) {
		// Signal we received the request, then never respond.
		close(gotCall)
		select {} // block forever
	})
	server.start()

	c := New(transport, mcp.ClientInfo{Name: "h"})
	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := c.CallTool(ctx, "x", nil)
		errCh <- err
	}()
	// Wait until the server has actually received the call before
	// closing — otherwise we'd race on whether Close ran first.
	<-gotCall
	if err := c.Close(); err != nil {
		// Expected: io.EOF or similar. We don't fail — Close()
		// surfacing an error is OK; what matters is the pending
		// call unblocks.
		t.Logf("Close returned: %v (acceptable)", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrClientClosed) {
			t.Errorf("CallTool err = %v, want ErrClientClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CallTool never returned after Close — pending demuxer leaked")
	}
}

// TestClient_ContextCancelUnblocksCall — cancelling the context
// passed to CallTool unblocks it cleanly with ctx.Err(). Doesn't
// shut down the client (other in-flight calls keep running).
func TestClient_ContextCancelUnblocksCall(t *testing.T) {
	t.Parallel()
	transport := newMemTransport()
	server := newFakeServer(t, transport)
	server.handle("initialize", func(_ mcp.Request) (any, *mcp.RPCError) {
		return mcp.InitializeResult{ProtocolVersion: declaredClientProtocolVersion}, nil
	})
	server.handle("tools/call", func(_ mcp.Request) (any, *mcp.RPCError) {
		select {} // never respond
	})
	server.start()
	c := New(transport, mcp.ClientInfo{Name: "h"})
	t.Cleanup(func() { _ = c.Close(); server.stop() })

	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := c.CallTool(ctx, "x", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("CallTool err = %v, want context.DeadlineExceeded", err)
	}
}

// TestClient_PingSuccess exercises the happy path: the server
// answers a ping with an empty result and the client returns nil.
// The cache's health-check loop relies on this round-trip to
// detect wedged subprocesses.
func TestClient_PingSuccess(t *testing.T) {
	t.Parallel()
	transport := newMemTransport()
	server := newFakeServer(t, transport)
	server.handle("initialize", func(_ mcp.Request) (any, *mcp.RPCError) {
		return mcp.InitializeResult{
			ProtocolVersion: declaredClientProtocolVersion,
			Capabilities:    mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{}},
			ServerInfo:      mcp.ServerInfo{Name: "fake", Version: "0"},
		}, nil
	})
	pinged := 0
	server.handle("ping", func(_ mcp.Request) (any, *mcp.RPCError) {
		pinged++
		return struct{}{}, nil // MCP spec: empty object on success
	})
	server.start()
	t.Cleanup(server.stop)

	c := New(transport, mcp.ClientInfo{Name: "hecate-test", Version: "0"})
	t.Cleanup(func() { _ = c.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := c.Ping(ctx); err != nil {
		t.Errorf("Ping: %v", err)
	}
	if pinged != 1 {
		t.Errorf("server saw %d pings, want 1", pinged)
	}
}

// TestClient_PingTimeoutReturnsErr: a server whose ping handler
// hangs must surface as context.DeadlineExceeded from Ping. This is
// the wedge signal the cache's health-check loop relies on to
// evict — without it, a stuck subprocess would just hold its slot
// until TTL.
//
// We register a never-returning handler (rather than leaving ping
// unhandled) because the fakeServer's default for unknown methods
// is method-not-found, which would surface as an RPC error rather
// than a deadline.
func TestClient_PingTimeoutReturnsErr(t *testing.T) {
	t.Parallel()
	transport := newMemTransport()
	server := newFakeServer(t, transport)
	server.handle("initialize", func(_ mcp.Request) (any, *mcp.RPCError) {
		return mcp.InitializeResult{
			ProtocolVersion: declaredClientProtocolVersion,
			Capabilities:    mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{}},
			ServerInfo:      mcp.ServerInfo{Name: "fake", Version: "0"},
		}, nil
	})
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	server.handle("ping", func(_ mcp.Request) (any, *mcp.RPCError) {
		<-stuck
		return struct{}{}, nil
	})
	server.start()
	t.Cleanup(server.stop)

	c := New(transport, mcp.ClientInfo{Name: "hecate-test", Version: "0"})
	t.Cleanup(func() { _ = c.Close() })

	initCtx, cancelInit := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelInit()
	if _, err := c.Initialize(initCtx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	pingCtx, cancelPing := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelPing()
	err := c.Ping(pingCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Ping err = %v, want context.DeadlineExceeded", err)
	}
}
