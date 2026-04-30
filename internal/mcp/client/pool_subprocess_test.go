package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/mcp"
)

// fixtureEnvKey is the env-var sentinel that diverts the test binary
// into the MCP fixture instead of running the test suite. The test
// re-execs itself with this variable set so we can exercise the real
// subprocess + StdioTransport + framing path without shipping a
// separate fixture binary.
const fixtureEnvKey = "HECATE_MCP_FIXTURE"

// TestMain diverts to the MCP fixture when the env sentinel is set.
// Standard Go pattern (used by os/exec's tests, etc.) for spinning up
// a child process from inside a test binary. The divert happens before
// the test machinery runs, so the fixture's stdout stays a clean
// JSON-RPC channel — no test output corrupts the wire.
func TestMain(m *testing.M) {
	if os.Getenv(fixtureEnvKey) == "1" {
		mcpFixtureMain()
		// mcpFixtureMain exits the process; the os.Exit below is a
		// safety net for a clean return path.
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// mcpFixtureMain runs a minimal MCP-stdio server: handshake, one
// canned tool ("echo"), one canned tool result. Exits cleanly when
// stdin closes. This is the smallest possible MCP server that
// exercises every code path the production client cares about
// (initialize, tools/list, tools/call, EOF on close).
func mcpFixtureMain() {
	in := bufio.NewReader(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		line, err := in.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			fmt.Fprintln(os.Stderr, "mcp fixture: read:", err)
			return
		}
		var req mcp.Request
		if err := json.Unmarshal(line, &req); err != nil {
			// Malformed input — skip rather than crash, mirrors a
			// well-behaved MCP server.
			continue
		}
		// Notifications (e.g. notifications/initialized) get no
		// response, per spec.
		if req.IsNotification() {
			continue
		}
		var (
			result any
			rpcErr *mcp.RPCError
		)
		switch req.Method {
		case "initialize":
			result = mcp.InitializeResult{
				ProtocolVersion: mcp.DeclaredProtocolVersion,
				Capabilities:    mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{}},
				ServerInfo:      mcp.ServerInfo{Name: "fixture", Version: "test"},
			}
		case "tools/list":
			result = mcp.ListToolsResult{Tools: []mcp.Tool{
				{
					Name:        "echo",
					Description: "Echo a message back. Used by the subprocess round-trip test.",
					InputSchema: json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}`),
				},
				{
					Name:        "fail",
					Description: "Always returns is_error=true. Pins the IsError surface.",
					InputSchema: json.RawMessage(`{"type":"object"}`),
				},
			}}
		case "tools/call":
			var p mcp.CallToolParams
			if err := json.Unmarshal(req.Params, &p); err != nil {
				rpcErr = mcp.NewError(mcp.ErrCodeInvalidParams, err.Error())
				break
			}
			switch p.Name {
			case "echo":
				result = mcp.CallToolResult{
					Content: mcp.TextContent("echo: " + string(p.Arguments)),
				}
			case "fail":
				result = mcp.CallToolResult{
					Content: mcp.TextContent("fixture-induced failure"),
					IsError: true,
				}
			default:
				rpcErr = mcp.NewError(mcp.ErrCodeInvalidParams, "unknown tool: "+p.Name)
			}
		default:
			rpcErr = mcp.NewError(mcp.ErrCodeMethodNotFound, req.Method)
		}

		resp := mcp.Response{JSONRPC: "2.0", ID: req.ID}
		if rpcErr != nil {
			resp.Error = rpcErr
		} else {
			raw, err := json.Marshal(result)
			if err != nil {
				fmt.Fprintln(os.Stderr, "mcp fixture: marshal:", err)
				continue
			}
			resp.Result = raw
		}
		if err := enc.Encode(&resp); err != nil {
			fmt.Fprintln(os.Stderr, "mcp fixture: write:", err)
			return
		}
	}
}

// TestPool_RealSubprocess_StdioRoundTrip exercises the full path that
// pure-unit tests can't: spawn an actual subprocess, frame JSON-RPC
// over its stdin/stdout, run initialize + tools/list + tools/call,
// shut it down cleanly. Catches framing bugs (newline handling,
// flush behavior), close-on-EOF semantics, and stderr capture in a
// way the in-memory transport can't.
func TestPool_RealSubprocess_StdioRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a real subprocess; skipped under -short")
	}
	cfg := ServerConfig{
		Name:    "fixture",
		Command: os.Args[0],
		// Match no real test — we divert in TestMain before m.Run
		// fires, so the regex never gets consulted in fixture mode.
		// Keep it set anyway so the parent's go-test isn't tempted
		// to interpret stray flag output.
		Args: []string{"-test.run=^$"},
		Env:  map[string]string{fixtureEnvKey: "1"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := NewPool(ctx, mcp.ClientInfo{Name: "hecate-subprocess-test", Version: "0.0.0"}, []ServerConfig{cfg})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	tools := pool.Tools()
	if len(tools) != 2 {
		t.Fatalf("Tools() len = %d, want 2; got %+v", len(tools), tools)
	}
	wantNames := map[string]bool{
		"mcp__fixture__echo": false,
		"mcp__fixture__fail": false,
	}
	for _, tool := range tools {
		if _, ok := wantNames[tool.Name]; !ok {
			t.Errorf("unexpected tool: %q", tool.Name)
			continue
		}
		wantNames[tool.Name] = true
	}
	for name, seen := range wantNames {
		if !seen {
			t.Errorf("missing tool: %q", name)
		}
	}

	// Happy path — echo round-trip.
	text, isErr, err := pool.Call(ctx, "mcp__fixture__echo", json.RawMessage(`{"msg":"hello"}`))
	if err != nil {
		t.Fatalf("Call echo: %v", err)
	}
	if isErr {
		t.Errorf("echo returned isError=true unexpectedly; text=%q", text)
	}
	if !strings.Contains(text, "echo:") || !strings.Contains(text, "hello") {
		t.Errorf("echo text = %q, want 'echo:' + 'hello'", text)
	}

	// IsError surface — the fail tool returns is_error=true. Pin
	// that this comes back via the isError return, not as a
	// transport-level err.
	text, isErr, err = pool.Call(ctx, "mcp__fixture__fail", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Call fail: %v (should be tool-level, not transport)", err)
	}
	if !isErr {
		t.Error("fail tool did not return isError=true")
	}
	if !strings.Contains(text, "fixture-induced failure") {
		t.Errorf("fail text = %q, want fixture diagnostic", text)
	}

	// Close shuts the subprocess down cleanly. cmd.Wait inside
	// StdioTransport.Close blocks on the child exit, so a missing
	// EOF-on-stdin-close path would deadlock the test rather than
	// passing through.
	closeDone := make(chan error, 1)
	go func() { closeDone <- pool.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked > 5s — subprocess didn't exit on stdin close")
	}
}
