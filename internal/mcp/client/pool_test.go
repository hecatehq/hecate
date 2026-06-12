package client

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/mcp"
)

// poolHarness wires N memTransport-backed Clients into a pool without
// spawning subprocesses. We bypass NewPool (which uses StdioTransport)
// because the dispatch / namespacing / shutdown logic is what we want
// to pin here — subprocess startup is exercised in the agent_loop
// integration tests.
//
// Each entry in `serverTools` produces one fake server registered as
// `name`. The harness handles initialize + tools/list automatically;
// the test only writes per-tool handlers.
type poolHarness struct {
	t     *testing.T
	pool  *Pool
	stops []func()
}

func newPoolHarness(t *testing.T, serverTools map[string][]mcp.Tool, callHandlers map[string]map[string]func(json.RawMessage) (mcp.CallToolResult, *mcp.RPCError)) *poolHarness {
	return newPoolHarnessWithResources(t, serverTools, callHandlers, nil)
}

func newPoolHarnessWithResources(t *testing.T, serverTools map[string][]mcp.Tool, callHandlers map[string]map[string]func(json.RawMessage) (mcp.CallToolResult, *mcp.RPCError), resources map[string]map[string]mcp.ResourceContents) *poolHarness {
	t.Helper()
	h := &poolHarness{t: t}
	pool := &Pool{
		clients: make(map[string]*pooledClient, len(serverTools)),
		bind:    make(map[string]namespacedToolBinding),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	for name, tools := range serverTools {
		transport := newMemTransport()
		server := newFakeServer(t, transport)
		server.handle("initialize", func(req mcp.Request) (any, *mcp.RPCError) {
			return mcp.InitializeResult{
				ProtocolVersion: declaredClientProtocolVersion,
				Capabilities:    mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{}},
				ServerInfo:      mcp.ServerInfo{Name: name, Version: "0.0.0"},
			}, nil
		})
		toolsCopy := append([]mcp.Tool(nil), tools...)
		server.handle("tools/list", func(req mcp.Request) (any, *mcp.RPCError) {
			return mcp.ListToolsResult{Tools: toolsCopy}, nil
		})
		// Per-tool handlers — dispatch through tools/call.
		callHandlersForServer := callHandlers[name]
		server.handle("tools/call", func(req mcp.Request) (any, *mcp.RPCError) {
			var params mcp.CallToolParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return nil, mcp.NewError(mcp.ErrCodeInvalidParams, err.Error())
			}
			fn, ok := callHandlersForServer[params.Name]
			if !ok {
				return nil, mcp.NewError(mcp.ErrCodeInvalidParams, "unknown tool: "+params.Name)
			}
			res, rpcErr := fn(params.Arguments)
			if rpcErr != nil {
				return nil, rpcErr
			}
			return res, nil
		})
		resourcesForServer := resources[name]
		server.handle("resources/read", func(req mcp.Request) (any, *mcp.RPCError) {
			var params mcp.ReadResourceParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return nil, mcp.NewError(mcp.ErrCodeInvalidParams, err.Error())
			}
			content, ok := resourcesForServer[params.URI]
			if !ok {
				return nil, mcp.NewError(mcp.ErrCodeInvalidParams, "unknown resource: "+params.URI)
			}
			return mcp.ReadResourceResult{Contents: []mcp.ResourceContents{content}}, nil
		})
		server.start()
		h.stops = append(h.stops, server.stop)

		client := New(transport, mcp.ClientInfo{Name: "hecate-test", Version: "0.0.0"})
		if _, err := client.Initialize(ctx); err != nil {
			t.Fatalf("server %q initialize: %v", name, err)
		}
		serverTools, err := client.ListTools(ctx)
		if err != nil {
			t.Fatalf("server %q list tools: %v", name, err)
		}
		pool.clients[name] = &pooledClient{client: client}
		for _, tt := range serverTools {
			nt := namespacedToolFromMCP(name, tt)
			pool.allTools = append(pool.allTools, nt)
			if !nt.ModelVisible {
				continue
			}
			pool.bind[nt.Name] = namespacedToolBinding{serverName: name, toolName: tt.Name, tool: nt}
			pool.tools = append(pool.tools, nt)
		}
	}
	// Sort once after all servers are loaded — same invariant
	// production NewPool maintains.
	sortNamespacedTools(pool.tools)
	sortNamespacedTools(pool.allTools)
	h.pool = pool
	t.Cleanup(func() {
		_ = pool.Close()
		for _, s := range h.stops {
			s()
		}
	})
	return h
}

func sortNamespacedTools(t []NamespacedTool) {
	// Insertion sort — len is tiny and we don't want to import sort
	// just for the test.
	for i := 1; i < len(t); i++ {
		for j := i; j > 0 && t[j-1].Name > t[j].Name; j-- {
			t[j-1], t[j] = t[j], t[j-1]
		}
	}
}

func TestPool_NamespacedToolName_RoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		server, tool string
	}{
		{"filesystem", "read_file"},
		{"github", "create_pull_request"},
		// Tool name with embedded `__` — first split wins, the
		// rest stays in the tool name.
		{"weird", "double__under"},
	}
	for _, c := range cases {
		ns := NamespacedToolName(c.server, c.tool)
		gotServer, gotTool, ok := SplitNamespacedToolName(ns)
		if !ok {
			t.Errorf("SplitNamespacedToolName(%q) = !ok", ns)
			continue
		}
		if gotServer != c.server || gotTool != c.tool {
			t.Errorf("round-trip %q → (%q, %q), want (%q, %q)", ns, gotServer, gotTool, c.server, c.tool)
		}
	}
	// Negative cases.
	for _, bad := range []string{
		"shell_exec",    // not namespaced
		"mcp__",         // missing server + tool
		"mcp__only",     // missing tool
		"mcp__server__", // empty tool
		"mcp____tool",   // empty server
	} {
		if _, _, ok := SplitNamespacedToolName(bad); ok {
			t.Errorf("SplitNamespacedToolName(%q) = ok; want !ok", bad)
		}
	}
}

func TestPool_TwoServersDistinctTools(t *testing.T) {
	t.Parallel()
	h := newPoolHarness(t,
		map[string][]mcp.Tool{
			"filesystem": {
				{Name: "read_file", Description: "read", InputSchema: json.RawMessage(`{"type":"object"}`)},
				{Name: "list_dir", Description: "list", InputSchema: json.RawMessage(`{"type":"object"}`)},
			},
			"github": {
				{Name: "create_issue", Description: "issue", InputSchema: json.RawMessage(`{"type":"object"}`)},
			},
		},
		map[string]map[string]func(json.RawMessage) (mcp.CallToolResult, *mcp.RPCError){
			"filesystem": {
				"read_file": func(args json.RawMessage) (mcp.CallToolResult, *mcp.RPCError) {
					return mcp.CallToolResult{Content: mcp.TextContent("file contents: " + string(args))}, nil
				},
				"list_dir": func(args json.RawMessage) (mcp.CallToolResult, *mcp.RPCError) {
					return mcp.CallToolResult{Content: mcp.TextContent("entries: a, b")}, nil
				},
			},
			"github": {
				"create_issue": func(args json.RawMessage) (mcp.CallToolResult, *mcp.RPCError) {
					return mcp.CallToolResult{Content: mcp.TextContent("issue #1")}, nil
				},
			},
		},
	)

	tools := h.pool.Tools()
	if len(tools) != 3 {
		t.Fatalf("Tools() len = %d, want 3", len(tools))
	}
	// Sorted by namespaced name → filesystem read_file, filesystem
	// list_dir, github create_issue → alpha order on the namespaced
	// name.
	wantNames := []string{
		"mcp__filesystem__list_dir",
		"mcp__filesystem__read_file",
		"mcp__github__create_issue",
	}
	for i, want := range wantNames {
		if tools[i].Name != want {
			t.Errorf("tools[%d].Name = %q, want %q", i, tools[i].Name, want)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	text, isErr, err := h.pool.Call(ctx, "mcp__filesystem__read_file", json.RawMessage(`{"path":"x"}`))
	if err != nil || isErr {
		t.Fatalf("Call read_file: err=%v isErr=%v", err, isErr)
	}
	if !strings.Contains(text, "file contents") {
		t.Errorf("read_file text = %q, want contains 'file contents'", text)
	}

	text, isErr, err = h.pool.Call(ctx, "mcp__github__create_issue", json.RawMessage(`{}`))
	if err != nil || isErr {
		t.Fatalf("Call create_issue: err=%v isErr=%v", err, isErr)
	}
	if !strings.Contains(text, "issue #1") {
		t.Errorf("create_issue text = %q, want contains 'issue #1'", text)
	}
}

func TestPool_MCPAppsVisibilityHidesAppOnlyToolsFromModel(t *testing.T) {
	t.Parallel()
	h := newPoolHarness(t,
		map[string][]mcp.Tool{
			"weather": {
				{
					Name:        "get_weather",
					InputSchema: json.RawMessage(`{"type":"object"}`),
					Meta:        json.RawMessage(`{"ui":{"resourceUri":"ui://weather/dashboard","visibility":["model","app"]}}`),
				},
				{
					Name:        "refresh_dashboard",
					InputSchema: json.RawMessage(`{"type":"object"}`),
					Meta:        json.RawMessage(`{"ui":{"resourceUri":"ui://weather/dashboard","visibility":["app"]}}`),
				},
			},
		},
		map[string]map[string]func(json.RawMessage) (mcp.CallToolResult, *mcp.RPCError){
			"weather": {
				"get_weather": func(json.RawMessage) (mcp.CallToolResult, *mcp.RPCError) {
					return mcp.CallToolResult{Content: mcp.TextContent("weather")}, nil
				},
				"refresh_dashboard": func(json.RawMessage) (mcp.CallToolResult, *mcp.RPCError) {
					return mcp.CallToolResult{Content: mcp.TextContent("refreshed")}, nil
				},
			},
		},
	)

	modelTools := h.pool.Tools()
	if len(modelTools) != 1 || modelTools[0].Name != "mcp__weather__get_weather" {
		t.Fatalf("model tools = %+v, want only get_weather", modelTools)
	}
	if !modelTools[0].ModelVisible {
		t.Fatalf("get_weather ModelVisible = false, want true")
	}
	if modelTools[0].UIResourceURI != "ui://weather/dashboard" {
		t.Fatalf("UIResourceURI = %q", modelTools[0].UIResourceURI)
	}

	allTools := h.pool.AllTools()
	if len(allTools) != 2 {
		t.Fatalf("all tools len = %d, want 2", len(allTools))
	}
	var appOnly NamespacedTool
	for _, tt := range allTools {
		if tt.Name == "mcp__weather__refresh_dashboard" {
			appOnly = tt
			break
		}
	}
	if appOnly.Name == "" {
		t.Fatalf("missing app-only tool in all tools: %+v", allTools)
	}
	if appOnly.ModelVisible {
		t.Fatalf("app-only ModelVisible = true, want false")
	}
	if len(appOnly.UIVisibility) != 1 || appOnly.UIVisibility[0] != "app" {
		t.Fatalf("app-only UIVisibility = %#v, want [app]", appOnly.UIVisibility)
	}

	_, _, err := h.pool.Call(context.Background(), "mcp__weather__refresh_dashboard", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("app-only tool call from model path succeeded; want unknown tool error")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("err = %v, want unknown tool", err)
	}
}

func TestPool_MCPAppsLegacyResourceURI(t *testing.T) {
	t.Parallel()
	tool := namespacedToolFromMCP("legacy", mcp.Tool{
		Name:        "render",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Meta:        json.RawMessage(`{"ui/resourceUri":"ui://legacy/view"}`),
	})
	if tool.UIResourceURI != "ui://legacy/view" {
		t.Fatalf("UIResourceURI = %q, want legacy URI", tool.UIResourceURI)
	}
	if !tool.ModelVisible {
		t.Fatal("legacy tool without visibility should default to model-visible")
	}
}

func TestPool_CallDetailedReadsMCPAppResource(t *testing.T) {
	t.Parallel()
	h := newPoolHarnessWithResources(t,
		map[string][]mcp.Tool{
			"weather": {{
				Name:        "get_weather",
				InputSchema: json.RawMessage(`{"type":"object"}`),
				Meta:        json.RawMessage(`{"ui":{"resourceUri":"ui://weather/dashboard","visibility":["model","app"]}}`),
			}},
		},
		map[string]map[string]func(json.RawMessage) (mcp.CallToolResult, *mcp.RPCError){
			"weather": {
				"get_weather": func(json.RawMessage) (mcp.CallToolResult, *mcp.RPCError) {
					return mcp.CallToolResult{
						Content:           mcp.TextContent("72F and clear"),
						StructuredContent: json.RawMessage(`{"temperature":72}`),
						Meta:              json.RawMessage(`{"source":"fake"}`),
					}, nil
				},
			},
		},
		map[string]map[string]mcp.ResourceContents{
			"weather": {
				"ui://weather/dashboard": {
					URI:      "ui://weather/dashboard",
					MIMEType: mcp.AppsResourceMIMEType,
					Text:     "<!doctype html><html><body>weather app</body></html>",
					Meta:     json.RawMessage(`{"ui":{"csp":{"resourceDomains":["https://cdn.example.com"]},"prefersBorder":true}}`),
				},
			},
		},
	)

	result, err := h.pool.CallDetailed(context.Background(), "mcp__weather__get_weather", json.RawMessage(`{"city":"Lisbon"}`))
	if err != nil {
		t.Fatalf("CallDetailed: %v", err)
	}
	if result.Text != "72F and clear" || result.IsError {
		t.Fatalf("result text/isError = %q/%v", result.Text, result.IsError)
	}
	if result.Tool.UIResourceURI != "ui://weather/dashboard" {
		t.Fatalf("tool UIResourceURI = %q", result.Tool.UIResourceURI)
	}
	if result.App == nil {
		t.Fatal("App = nil, want HTML resource")
	}
	if result.App.URI != "ui://weather/dashboard" || result.App.MIMEType != mcp.AppsResourceMIMEType {
		t.Fatalf("app URI/MIME = %q/%q", result.App.URI, result.App.MIMEType)
	}
	if !strings.Contains(result.App.HTML, "weather app") {
		t.Fatalf("app HTML = %q, want weather app", result.App.HTML)
	}
	if !strings.Contains(string(result.App.Meta), "resourceDomains") {
		t.Fatalf("app meta = %s, want resource CSP", result.App.Meta)
	}
}

func TestPool_UnknownToolReturnsError(t *testing.T) {
	t.Parallel()
	h := newPoolHarness(t,
		map[string][]mcp.Tool{
			"fs": {{Name: "read", InputSchema: json.RawMessage(`{}`)}},
		},
		map[string]map[string]func(json.RawMessage) (mcp.CallToolResult, *mcp.RPCError){
			"fs": {
				"read": func(json.RawMessage) (mcp.CallToolResult, *mcp.RPCError) {
					return mcp.CallToolResult{Content: mcp.TextContent("ok")}, nil
				},
			},
		},
	)
	_, _, err := h.pool.Call(context.Background(), "mcp__fs__nope", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("err = %v, want 'unknown tool'", err)
	}
}

func TestSanitizedStdioEnvKeepsRuntimeEssentialsOnly(t *testing.T) {
	t.Parallel()

	env := sanitizedStdioEnv([]string{
		"PATH=/bin",
		"HOME=/Users/alice",
		"TMPDIR=/tmp",
		"LANG=en_US.UTF-8",
		"XDG_CONFIG_HOME=/Users/alice/.config",
		"VOLTA_HOME=/Users/alice/.volta",
		"APPDATA=C:\\Users\\alice\\AppData\\Roaming",
		"LOCALAPPDATA=C:\\Users\\alice\\AppData\\Local",
		"SSL_CERT_FILE=/etc/ssl/corp.pem",
		"SSL_CERT_DIR=/etc/ssl/certs",
		"NODE_EXTRA_CA_CERTS=/etc/ssl/node-corp.pem",
		"HTTPS_PROXY=http://proxy.local:8080",
		"HECATE_CONTROL_PLANE_SECRET_KEY=secret",
		"PROVIDER_OPENAI_API_KEY=provider-secret",
		"OPENAI_API_KEY=openai-secret",
		"ANTHROPIC_API_KEY=anthropic-secret",
		"OTEL_EXPORTER_OTLP_HEADERS=authorization=Bearer secret",
	})

	got := map[string]bool{}
	for _, item := range env {
		got[item] = true
	}
	for _, want := range []string{
		"PATH=/bin",
		"HOME=/Users/alice",
		"TMPDIR=/tmp",
		"LANG=en_US.UTF-8",
		"XDG_CONFIG_HOME=/Users/alice/.config",
		"VOLTA_HOME=/Users/alice/.volta",
		"APPDATA=C:\\Users\\alice\\AppData\\Roaming",
		"LOCALAPPDATA=C:\\Users\\alice\\AppData\\Local",
		"SSL_CERT_FILE=/etc/ssl/corp.pem",
		"SSL_CERT_DIR=/etc/ssl/certs",
		"NODE_EXTRA_CA_CERTS=/etc/ssl/node-corp.pem",
	} {
		if !got[want] {
			t.Fatalf("missing runtime env %q in %#v", want, env)
		}
	}
	for _, leaked := range []string{
		"HECATE_CONTROL_PLANE_SECRET_KEY=secret",
		"PROVIDER_OPENAI_API_KEY=provider-secret",
		"OPENAI_API_KEY=openai-secret",
		"ANTHROPIC_API_KEY=anthropic-secret",
		"OTEL_EXPORTER_OTLP_HEADERS=authorization=Bearer secret",
		"HTTPS_PROXY=http://proxy.local:8080",
	} {
		if got[leaked] {
			t.Fatalf("secret env %q leaked into MCP stdio env: %#v", leaked, env)
		}
	}
}

func TestMergeEnvPreservesExplicitMCPSecrets(t *testing.T) {
	t.Parallel()

	env := mergeEnv(sanitizedStdioEnv([]string{
		"PATH=/bin",
		"PROVIDER_OPENAI_API_KEY=provider-secret",
	}), map[string]string{
		"OPENAI_API_KEY": "explicit-secret",
	})

	got := map[string]bool{}
	for _, item := range env {
		got[item] = true
	}
	if !got["PATH=/bin"] {
		t.Fatalf("missing PATH in %#v", env)
	}
	if !got["OPENAI_API_KEY=explicit-secret"] {
		t.Fatalf("missing explicit MCP env override in %#v", env)
	}
	if got["PROVIDER_OPENAI_API_KEY=provider-secret"] {
		t.Fatalf("provider-scoped gateway secret leaked into MCP stdio env: %#v", env)
	}
}

// TestPool_ToolErrorIsSurfacedDistinctly pins the contract that
// CallToolResult.IsError comes back via the isError return — distinct
// from a transport / protocol error. The agent loop relies on this
// split: tool errors get fed back to the LLM as a tool message with
// ToolError=true; protocol errors abort dispatch.
func TestPool_ToolErrorIsSurfacedDistinctly(t *testing.T) {
	t.Parallel()
	h := newPoolHarness(t,
		map[string][]mcp.Tool{
			"fs": {{Name: "read", InputSchema: json.RawMessage(`{}`)}},
		},
		map[string]map[string]func(json.RawMessage) (mcp.CallToolResult, *mcp.RPCError){
			"fs": {
				"read": func(json.RawMessage) (mcp.CallToolResult, *mcp.RPCError) {
					return mcp.CallToolResult{
						Content: mcp.TextContent("permission denied: /etc/shadow"),
						IsError: true,
					}, nil
				},
			},
		},
	)
	text, isErr, err := h.pool.Call(context.Background(), "mcp__fs__read", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected protocol error: %v", err)
	}
	if !isErr {
		t.Error("expected isError=true")
	}
	if !strings.Contains(text, "permission denied") {
		t.Errorf("text = %q, want contains 'permission denied'", text)
	}
}

func TestPool_CloseShutsDownAllClients(t *testing.T) {
	t.Parallel()
	h := newPoolHarness(t,
		map[string][]mcp.Tool{
			"a": {{Name: "tool", InputSchema: json.RawMessage(`{}`)}},
			"b": {{Name: "tool", InputSchema: json.RawMessage(`{}`)}},
		},
		map[string]map[string]func(json.RawMessage) (mcp.CallToolResult, *mcp.RPCError){
			"a": {"tool": func(json.RawMessage) (mcp.CallToolResult, *mcp.RPCError) {
				return mcp.CallToolResult{Content: mcp.TextContent("a")}, nil
			}},
			"b": {"tool": func(json.RawMessage) (mcp.CallToolResult, *mcp.RPCError) {
				return mcp.CallToolResult{Content: mcp.TextContent("b")}, nil
			}},
		},
	)
	if err := h.pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Idempotent — second close is a no-op.
	if err := h.pool.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	// Tools map cleared.
	if len(h.pool.Tools()) != 0 {
		t.Errorf("Tools() after Close should be empty")
	}
	// Subsequent Call returns unknown-tool error (not a transport
	// panic) since bind/clients were cleared.
	_, _, err := h.pool.Call(context.Background(), "mcp__a__tool", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("Call after Close err = %v, want 'unknown tool'", err)
	}
}

func TestPool_ConcurrentCallsAcrossServers(t *testing.T) {
	t.Parallel()
	h := newPoolHarness(t,
		map[string][]mcp.Tool{
			"a": {{Name: "echo", InputSchema: json.RawMessage(`{}`)}},
			"b": {{Name: "echo", InputSchema: json.RawMessage(`{}`)}},
		},
		map[string]map[string]func(json.RawMessage) (mcp.CallToolResult, *mcp.RPCError){
			"a": {"echo": func(args json.RawMessage) (mcp.CallToolResult, *mcp.RPCError) {
				return mcp.CallToolResult{Content: mcp.TextContent("a:" + string(args))}, nil
			}},
			"b": {"echo": func(args json.RawMessage) (mcp.CallToolResult, *mcp.RPCError) {
				return mcp.CallToolResult{Content: mcp.TextContent("b:" + string(args))}, nil
			}},
		},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	const N = 20
	var wg sync.WaitGroup
	wg.Add(N)
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			tool := "mcp__a__echo"
			wantPrefix := "a:"
			if i%2 == 0 {
				tool = "mcp__b__echo"
				wantPrefix = "b:"
			}
			text, isErr, err := h.pool.Call(ctx, tool, json.RawMessage(`{"i":"x"}`))
			if err != nil || isErr {
				errs <- err
				return
			}
			if !strings.HasPrefix(text, wantPrefix) {
				errs <- errors.New("wrong server: " + text)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent call: %v", err)
		}
	}
}
