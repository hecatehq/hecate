package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/mcp"
)

// mcpHTTPServer is a minimal in-process MCP server that responds to
// JSON-RPC requests over HTTP (application/json responses, not SSE).
// It routes by method name; unknown methods return -32601.
type mcpHTTPServer struct {
	handlers map[string]func(req mcp.Request) (any, *mcp.RPCError)
}

func newMCPHTTPServer() *mcpHTTPServer {
	return &mcpHTTPServer{handlers: make(map[string]func(mcp.Request) (any, *mcp.RPCError))}
}

func (s *mcpHTTPServer) handle(method string, fn func(mcp.Request) (any, *mcp.RPCError)) {
	s.handlers[method] = fn
}

func (s *mcpHTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req mcp.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	fn, ok := s.handlers[req.Method]
	if !ok {
		resp := mcp.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   mcp.NewError(mcp.ErrCodeMethodNotFound, "method not found: "+req.Method),
		}
		_ = json.NewEncoder(w).Encode(resp)
		return
	}
	result, rpcErr := fn(req)
	var resp mcp.Response
	resp.JSONRPC = "2.0"
	resp.ID = req.ID
	if rpcErr != nil {
		resp.Error = rpcErr
	} else {
		raw, _ := json.Marshal(result)
		resp.Result = raw
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func newTestMCPHTTPServer(t *testing.T) (*httptest.Server, *mcpHTTPServer) {
	t.Helper()
	srv := newMCPHTTPServer()
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)
	return hs, srv
}

// registerStandardHandlers sets up initialize + tools/list + tools/call
// on srv using the provided tool list and call handler table.
func registerStandardHandlers(
	srv *mcpHTTPServer,
	serverName string,
	tools []mcp.Tool,
	callHandlers map[string]func(json.RawMessage) mcp.CallToolResult,
) {
	srv.handle("initialize", func(req mcp.Request) (any, *mcp.RPCError) {
		return mcp.InitializeResult{
			ProtocolVersion: declaredClientProtocolVersion,
			Capabilities:    mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{}},
			ServerInfo:      mcp.ServerInfo{Name: serverName, Version: "0.0.0"},
		}, nil
	})
	srv.handle("notifications/initialized", func(req mcp.Request) (any, *mcp.RPCError) {
		return nil, nil
	})
	srv.handle("tools/list", func(req mcp.Request) (any, *mcp.RPCError) {
		return mcp.ListToolsResult{Tools: tools}, nil
	})
	srv.handle("ping", func(req mcp.Request) (any, *mcp.RPCError) {
		// Real MCP servers always answer ping with an empty result.
		// Tests that simulate a wedged server override this handler.
		return struct{}{}, nil
	})
	srv.handle("tools/call", func(req mcp.Request) (any, *mcp.RPCError) {
		var params mcp.CallToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, mcp.NewError(mcp.ErrCodeInvalidParams, err.Error())
		}
		fn, ok := callHandlers[params.Name]
		if !ok {
			return nil, mcp.NewError(mcp.ErrCodeInvalidParams, "unknown tool: "+params.Name)
		}
		return fn(params.Arguments), nil
	})
}

// TestPool_HTTPTransportRoundTrip exercises the full NewPool path with an
// HTTP transport: initialize handshake, tool listing, and a tool call all
// happen over a real httptest.Server rather than in-process stdio pipes.
func TestPool_HTTPTransportRoundTrip(t *testing.T) {
	t.Parallel()

	hs, srv := newTestMCPHTTPServer(t)
	registerStandardHandlers(srv, "remote", []mcp.Tool{
		{Name: "echo", Description: "echo the input", InputSchema: json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}}}`)},
	}, map[string]func(json.RawMessage) mcp.CallToolResult{
		"echo": func(args json.RawMessage) mcp.CallToolResult {
			return mcp.CallToolResult{Content: mcp.TextContent("echo: " + string(args))}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := NewPool(ctx, mcp.ClientInfo{Name: "test", Version: "0"}, []ServerConfig{
		{Name: "remote", URL: hs.URL},
	})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	// Tool catalog should contain exactly the one namespaced tool.
	tools := pool.Tools()
	if len(tools) != 1 {
		t.Fatalf("Tools() len = %d, want 1", len(tools))
	}
	if tools[0].Name != "mcp__remote__echo" {
		t.Errorf("tool name = %q, want mcp__remote__echo", tools[0].Name)
	}

	// Call the tool and verify the round-trip.
	text, isErr, err := pool.Call(ctx, "mcp__remote__echo", json.RawMessage(`{"msg":"hello"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if isErr {
		t.Errorf("Call returned isError=true")
	}
	if !strings.Contains(text, "echo:") {
		t.Errorf("text = %q, want contains 'echo:'", text)
	}
}

// TestPool_HTTPTransportAuthHeaderForwarded verifies that the Headers
// field on ServerConfig is forwarded on every MCP request — critical for
// bearer-token auth against cloud MCP servers.
func TestPool_HTTPTransportAuthHeaderForwarded(t *testing.T) {
	t.Parallel()

	const wantToken = "Bearer pool-secret-token"
	gotTokens := make(chan string, 8) // buffer so the handler never blocks

	hs, srv := newTestMCPHTTPServer(t)

	// Intercept every request to capture the Authorization header.
	origServeHTTP := srv.ServeHTTP
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTokens <- r.Header.Get("Authorization")
		origServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)

	registerStandardHandlers(srv, "secure", []mcp.Tool{
		{Name: "ping", InputSchema: json.RawMessage(`{}`)},
	}, map[string]func(json.RawMessage) mcp.CallToolResult{
		"ping": func(json.RawMessage) mcp.CallToolResult {
			return mcp.CallToolResult{Content: mcp.TextContent("pong")}
		},
	})
	_ = hs // shut up the "unused" linter — we registered handlers on srv

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := NewPool(ctx, mcp.ClientInfo{Name: "test", Version: "0"}, []ServerConfig{
		{Name: "secure", URL: ts.URL, Headers: map[string]string{"Authorization": wantToken}},
	})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	// Drain the tokens collected during initialize + tools/list.
	// We don't care about those — only assert the tool call carries the header.
	// Drain up to 4 requests (initialize, notifications/initialized, tools/list, etc.)
	for i := 0; i < 3; i++ {
		select {
		case tok := <-gotTokens:
			if tok != wantToken {
				t.Errorf("request %d Authorization = %q, want %q", i+1, tok, wantToken)
			}
		default:
		}
	}

	_, _, err = pool.Call(ctx, "mcp__secure__ping", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	select {
	case tok := <-gotTokens:
		if tok != wantToken {
			t.Errorf("tool call Authorization = %q, want %q", tok, wantToken)
		}
	case <-time.After(time.Second):
		t.Error("no Authorization header captured for tool call")
	}
}

// TestPool_HTTPTransportMutualExclusivity verifies that NewPool rejects
// a config that sets both Command and URL.
func TestPool_HTTPTransportMutualExclusivity(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := NewPool(ctx, mcp.ClientInfo{}, []ServerConfig{
		{Name: "bad", Command: "npx", URL: "https://example.com/mcp"},
	})
	if err == nil {
		t.Fatal("expected error for command+url, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("err = %v, want 'mutually exclusive'", err)
	}
}

// TestPool_WithCache_SharesClientAcrossPools pins the headline win
// of cached pools: two pools built in sequence with the same upstream
// config share one Client (and thus one initialize handshake on the
// upstream). The first pool's Close releases the Client back to the
// cache; the second pool's NewPoolWithCache acquires it without
// re-initializing.
func TestPool_WithCache_SharesClientAcrossPools(t *testing.T) {
	t.Parallel()

	hs, srv := newTestMCPHTTPServer(t)
	registerStandardHandlers(srv, "shared", []mcp.Tool{
		{Name: "ping", InputSchema: json.RawMessage(`{}`)},
	}, map[string]func(json.RawMessage) mcp.CallToolResult{
		"ping": func(json.RawMessage) mcp.CallToolResult {
			return mcp.CallToolResult{Content: mcp.TextContent("pong")}
		},
	})
	// Replace the default initialize handler with a counting variant
	// so we can assert it fires exactly once across both pools.
	var initCount int
	srv.handle("initialize", func(req mcp.Request) (any, *mcp.RPCError) {
		initCount++
		return mcp.InitializeResult{
			ProtocolVersion: declaredClientProtocolVersion,
			Capabilities:    mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{}},
			ServerInfo:      mcp.ServerInfo{Name: "shared", Version: "0"},
		}, nil
	})

	cache := NewSharedClientCache(time.Minute, mcp.ClientInfo{Name: "test", Version: "0"})
	t.Cleanup(func() { _ = cache.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := []ServerConfig{{Name: "shared", URL: hs.URL}}

	pool1, err := NewPoolWithCache(ctx, cfg, cache)
	if err != nil {
		t.Fatalf("NewPoolWithCache 1: %v", err)
	}
	if _, _, err := pool1.Call(ctx, "mcp__shared__ping", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("pool1 Call: %v", err)
	}
	if err := pool1.Close(); err != nil {
		t.Fatalf("pool1 Close: %v", err)
	}

	pool2, err := NewPoolWithCache(ctx, cfg, cache)
	if err != nil {
		t.Fatalf("NewPoolWithCache 2: %v", err)
	}
	t.Cleanup(func() { _ = pool2.Close() })
	if _, _, err := pool2.Call(ctx, "mcp__shared__ping", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("pool2 Call: %v", err)
	}

	if initCount != 1 {
		t.Errorf("server saw %d initialize calls across two pools, want 1 (cache should reuse)", initCount)
	}
}

// TestPool_WithCache_DifferentConfigsDoNotShare verifies the
// counter-invariant: two pools targeting different upstream configs
// each get their own Client even when sharing one cache. Otherwise
// cache key collisions would silently route a tool call to the wrong
// upstream — a security concern, not just a perf one.
func TestPool_WithCache_DifferentConfigsDoNotShare(t *testing.T) {
	t.Parallel()

	hsA, srvA := newTestMCPHTTPServer(t)
	registerStandardHandlers(srvA, "a", []mcp.Tool{{Name: "ping", InputSchema: json.RawMessage(`{}`)}}, nil)
	hsB, srvB := newTestMCPHTTPServer(t)
	registerStandardHandlers(srvB, "b", []mcp.Tool{{Name: "ping", InputSchema: json.RawMessage(`{}`)}}, nil)

	var initA, initB int
	srvA.handle("initialize", func(req mcp.Request) (any, *mcp.RPCError) {
		initA++
		return mcp.InitializeResult{ProtocolVersion: declaredClientProtocolVersion, Capabilities: mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{}}, ServerInfo: mcp.ServerInfo{Name: "a", Version: "0"}}, nil
	})
	srvB.handle("initialize", func(req mcp.Request) (any, *mcp.RPCError) {
		initB++
		return mcp.InitializeResult{ProtocolVersion: declaredClientProtocolVersion, Capabilities: mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{}}, ServerInfo: mcp.ServerInfo{Name: "b", Version: "0"}}, nil
	})

	cache := NewSharedClientCache(time.Minute, mcp.ClientInfo{Name: "test", Version: "0"})
	t.Cleanup(func() { _ = cache.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	poolA, err := NewPoolWithCache(ctx, []ServerConfig{{Name: "fs", URL: hsA.URL}}, cache)
	if err != nil {
		t.Fatalf("NewPoolWithCache A: %v", err)
	}
	t.Cleanup(func() { _ = poolA.Close() })
	poolB, err := NewPoolWithCache(ctx, []ServerConfig{{Name: "fs", URL: hsB.URL}}, cache)
	if err != nil {
		t.Fatalf("NewPoolWithCache B: %v", err)
	}
	t.Cleanup(func() { _ = poolB.Close() })

	if initA != 1 {
		t.Errorf("server A initialize count = %d, want 1", initA)
	}
	if initB != 1 {
		t.Errorf("server B initialize count = %d, want 1 (different URL must not share with A)", initB)
	}
	// Cache should hold two distinct entries.
	if got := cache.Stats().Entries; got != 2 {
		t.Errorf("Stats.Entries = %d, want 2", got)
	}
}

// TestPool_WithCache_CloseReleasesNotShutsDown: Pool.Close on a cached
// pool MUST NOT close the underlying Client — that would yank it out
// from under any other pool that's currently sharing it. We verify by
// holding a second pool, closing the first, and observing the cached
// entry remains usable.
func TestPool_WithCache_CloseReleasesNotShutsDown(t *testing.T) {
	t.Parallel()

	hs, srv := newTestMCPHTTPServer(t)
	registerStandardHandlers(srv, "fs", []mcp.Tool{
		{Name: "ping", InputSchema: json.RawMessage(`{}`)},
	}, map[string]func(json.RawMessage) mcp.CallToolResult{
		"ping": func(json.RawMessage) mcp.CallToolResult {
			return mcp.CallToolResult{Content: mcp.TextContent("pong")}
		},
	})

	cache := NewSharedClientCache(time.Minute, mcp.ClientInfo{Name: "test", Version: "0"})
	t.Cleanup(func() { _ = cache.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := []ServerConfig{{Name: "fs", URL: hs.URL}}

	pool1, err := NewPoolWithCache(ctx, cfg, cache)
	if err != nil {
		t.Fatalf("NewPoolWithCache 1: %v", err)
	}
	pool2, err := NewPoolWithCache(ctx, cfg, cache)
	if err != nil {
		t.Fatalf("NewPoolWithCache 2: %v", err)
	}
	t.Cleanup(func() { _ = pool2.Close() })

	// Close pool1. pool2 must still be able to make calls — it shares
	// the same cached Client, and pool1's release should NOT have
	// shut it down.
	if err := pool1.Close(); err != nil {
		t.Fatalf("pool1 Close: %v", err)
	}

	if _, _, err := pool2.Call(ctx, "mcp__fs__ping", json.RawMessage(`{}`)); err != nil {
		t.Errorf("pool2 Call after pool1 Close: %v (cached client was likely shut down by pool1)", err)
	}
}

// TestPool_HTTPTransportNeitherCommandNorURL verifies that NewPool
// rejects a config where neither Command nor URL is set.
func TestPool_HTTPTransportNeitherCommandNorURL(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := NewPool(ctx, mcp.ClientInfo{}, []ServerConfig{
		{Name: "empty"},
	})
	if err == nil {
		t.Fatal("expected error for no command and no url, got nil")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("err = %v, want 'required'", err)
	}
}
