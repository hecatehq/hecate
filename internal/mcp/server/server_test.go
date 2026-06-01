package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/mcp"
)

// rwPipe wires an io.Pipe with a closer hook so we can simulate stdin
// closing (which is how Serve unwinds in production when the parent
// process exits).
type rwPipe struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func newRWPipe() *rwPipe {
	r, w := io.Pipe()
	return &rwPipe{r: r, w: w}
}

func (p *rwPipe) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *rwPipe) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *rwPipe) Close() error                { return p.r.Close() }

// runServer starts a server in the background, feeds it the supplied
// JSON-RPC lines, returns the response lines (one per scan).
func runServer(t *testing.T, lines []string, register func(*Server)) []string {
	t.Helper()

	in := newRWPipe()
	var outBuf safeBuffer
	srv := NewServer("hecate-test", "0.0.0")
	if register != nil {
		register(srv)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ctx, in, &outBuf)
	}()

	for _, line := range lines {
		_, _ = in.Write([]byte(line + "\n"))
	}
	// Close the writer end so the scanner sees EOF and Serve returns.
	_ = in.w.Close()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Serve did not return within 2s")
	}

	out := strings.TrimRight(outBuf.String(), "\n")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// safeBuffer is bytes.Buffer with a mutex — Server.writeResponse may
// write concurrently from goroutines spawned per-message.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestServer_Initialize(t *testing.T) {
	resp := runServer(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"test","version":"1"}}}`,
	}, nil)
	if len(resp) != 1 {
		t.Fatalf("got %d responses, want 1", len(resp))
	}
	var r mcp.Response
	if err := json.Unmarshal([]byte(resp[0]), &r); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if r.Error != nil {
		t.Fatalf("initialize errored: %+v", r.Error)
	}
	var result mcp.InitializeResult
	if err := json.Unmarshal(r.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.ServerInfo.Name != "hecate-test" {
		t.Errorf("mcp.ServerInfo.Name = %q, want hecate-test", result.ServerInfo.Name)
	}
	if result.ProtocolVersion != "2025-11-25" {
		t.Errorf("ProtocolVersion = %q, want 2025-11-25", result.ProtocolVersion)
	}
	if result.Capabilities.Tools == nil {
		t.Errorf("Capabilities.Tools must be non-nil to advertise tool support")
	}
}

func TestServer_Initialize_AdvertisesResourcesAndPromptsWhenRegistered(t *testing.T) {
	register := func(s *Server) {
		s.RegisterResource(mcp.Resource{URI: "hecate://tasks/recent", Name: "recent_tasks"},
			func(ctx context.Context, uri string) (mcp.ReadResourceResult, error) {
				return mcp.ReadResourceResult{}, nil
			})
		s.RegisterPrompt(mcp.Prompt{Name: "operator_briefing"},
			func(ctx context.Context, args map[string]string) (mcp.GetPromptResult, error) {
				return mcp.GetPromptResult{}, nil
			})
	}
	resp := runServer(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"test","version":"1"}}}`,
	}, register)
	var r mcp.Response
	_ = json.Unmarshal([]byte(resp[0]), &r)
	var result mcp.InitializeResult
	if err := json.Unmarshal(r.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Capabilities.Resources == nil {
		t.Fatalf("Capabilities.Resources is nil, want advertised")
	}
	if result.Capabilities.Prompts == nil {
		t.Fatalf("Capabilities.Prompts is nil, want advertised")
	}
}

func TestServer_NotificationGetsNoResponse(t *testing.T) {
	// notifications/initialized is a notification (no id) — server
	// must process it but stay silent on the wire.
	resp := runServer(t, []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	}, nil)
	if len(resp) != 0 {
		t.Fatalf("notification got responses: %v", resp)
	}
}

func TestServer_UnknownMethodReturnsMethodNotFound(t *testing.T) {
	resp := runServer(t, []string{
		`{"jsonrpc":"2.0","id":7,"method":"does/not/exist"}`,
	}, nil)
	if len(resp) != 1 {
		t.Fatalf("got %d responses, want 1", len(resp))
	}
	var r mcp.Response
	_ = json.Unmarshal([]byte(resp[0]), &r)
	if r.Error == nil || r.Error.Code != mcp.ErrCodeMethodNotFound {
		t.Fatalf("got error %+v, want code %d", r.Error, mcp.ErrCodeMethodNotFound)
	}
}

func TestServer_ParseErrorRecovers(t *testing.T) {
	// A junk line must produce a parse-error response and the server
	// stays alive for the next message.
	resp := runServer(t, []string{
		`not json`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
	}, nil)
	if len(resp) != 2 {
		t.Fatalf("got %d responses, want 2 (parse error + ping)", len(resp))
	}
	var first, second mcp.Response
	_ = json.Unmarshal([]byte(resp[0]), &first)
	_ = json.Unmarshal([]byte(resp[1]), &second)
	// Order isn't guaranteed (each line dispatched in its own
	// goroutine) — just look for one parse error and one ping result.
	hasParseError := (first.Error != nil && first.Error.Code == mcp.ErrCodeParseError) ||
		(second.Error != nil && second.Error.Code == mcp.ErrCodeParseError)
	hasPingResult := first.Error == nil || second.Error == nil
	if !hasParseError || !hasPingResult {
		t.Fatalf("expected one parse error + one ping success, got %s and %s", resp[0], resp[1])
	}
}

func TestServer_ListTools(t *testing.T) {
	register := func(s *Server) {
		s.RegisterTool(mcp.Tool{
			Name:        "echo",
			Title:       "Echo back",
			Description: "echo back input",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: mcp.BoolPtr(true)},
		}, func(ctx context.Context, args json.RawMessage) (mcp.CallToolResult, error) {
			return mcp.CallToolResult{Content: mcp.TextContent(string(args))}, nil
		})
	}
	resp := runServer(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
	}, register)
	if len(resp) != 1 {
		t.Fatalf("got %d responses, want 1", len(resp))
	}
	var r mcp.Response
	_ = json.Unmarshal([]byte(resp[0]), &r)
	var result mcp.ListToolsResult
	_ = json.Unmarshal(r.Result, &result)
	if len(result.Tools) != 1 || result.Tools[0].Name != "echo" {
		t.Fatalf("tools = %+v, want one echo", result.Tools)
	}
	// 2025-06-18: title must be a separate field from name so clients
	// can render a friendly label without losing the programmatic id.
	if result.Tools[0].Title != "Echo back" {
		t.Errorf("Title = %q, want 'Echo back'", result.Tools[0].Title)
	}
	// 2025-03-26: annotations advertise behavioral hints; readOnlyHint
	// lets the client auto-approve safe inspection tools.
	if result.Tools[0].Annotations == nil ||
		result.Tools[0].Annotations.ReadOnlyHint == nil ||
		*result.Tools[0].Annotations.ReadOnlyHint != true {
		t.Errorf("Annotations.ReadOnlyHint not propagated: %+v", result.Tools[0].Annotations)
	}
}

func TestServer_ListAndReadResources(t *testing.T) {
	register := func(s *Server) {
		s.RegisterResource(mcp.Resource{
			URI:      "hecate://tasks/recent",
			Name:     "recent_tasks",
			Title:    "Recent tasks",
			MIMEType: "application/json",
		}, func(ctx context.Context, uri string) (mcp.ReadResourceResult, error) {
			return mcp.ReadResourceResult{Contents: []mcp.ResourceContents{{
				URI:      uri,
				MIMEType: "application/json",
				Text:     `{"ok":true}`,
			}}}, nil
		})
	}

	listResp := runServer(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
	}, register)
	var listRPC mcp.Response
	_ = json.Unmarshal([]byte(listResp[0]), &listRPC)
	var listResult mcp.ListResourcesResult
	if err := json.Unmarshal(listRPC.Result, &listResult); err != nil {
		t.Fatalf("decode resources/list: %v", err)
	}
	if len(listResult.Resources) != 1 || listResult.Resources[0].URI != "hecate://tasks/recent" {
		t.Fatalf("resources = %+v, want recent tasks", listResult.Resources)
	}

	readResp := runServer(t, []string{
		`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"hecate://tasks/recent"}}`,
	}, register)
	var readRPC mcp.Response
	_ = json.Unmarshal([]byte(readResp[0]), &readRPC)
	var readResult mcp.ReadResourceResult
	if err := json.Unmarshal(readRPC.Result, &readResult); err != nil {
		t.Fatalf("decode resources/read: %v", err)
	}
	if len(readResult.Contents) != 1 || readResult.Contents[0].Text != `{"ok":true}` {
		t.Fatalf("contents = %+v, want JSON text", readResult.Contents)
	}
}

func TestServer_ListResourceTemplates(t *testing.T) {
	register := func(s *Server) {
		s.RegisterResourceTemplate(mcp.ResourceTemplate{
			URITemplate: "hecate://tasks/{task_id}",
			Name:        "task_detail",
		}, func(ctx context.Context, uri string) (mcp.ReadResourceResult, error) {
			return mcp.ReadResourceResult{}, errResourceNoMatch
		})
	}
	resp := runServer(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"resources/templates/list"}`,
	}, register)
	var r mcp.Response
	_ = json.Unmarshal([]byte(resp[0]), &r)
	var result mcp.ListResourceTemplatesResult
	if err := json.Unmarshal(r.Result, &result); err != nil {
		t.Fatalf("decode templates/list: %v", err)
	}
	if len(result.ResourceTemplates) != 1 || result.ResourceTemplates[0].URITemplate != "hecate://tasks/{task_id}" {
		t.Fatalf("templates = %+v, want task template", result.ResourceTemplates)
	}
}

func TestServer_ListAndGetPrompts(t *testing.T) {
	register := func(s *Server) {
		s.RegisterPrompt(mcp.Prompt{
			Name:        "investigate_task",
			Title:       "Investigate task",
			Description: "Inspect a task",
			Arguments:   []mcp.PromptArgument{{Name: "task_id", Required: true}},
		}, func(ctx context.Context, args map[string]string) (mcp.GetPromptResult, error) {
			return mcp.GetPromptResult{
				Description: "Investigate",
				Messages: []mcp.PromptMessage{{
					Role:    "user",
					Content: mcp.ContentBlock{Type: "text", Text: "Inspect " + args["task_id"]},
				}},
			}, nil
		})
	}

	listResp := runServer(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`,
	}, register)
	var listRPC mcp.Response
	_ = json.Unmarshal([]byte(listResp[0]), &listRPC)
	var listResult mcp.ListPromptsResult
	if err := json.Unmarshal(listRPC.Result, &listResult); err != nil {
		t.Fatalf("decode prompts/list: %v", err)
	}
	if len(listResult.Prompts) != 1 || listResult.Prompts[0].Name != "investigate_task" {
		t.Fatalf("prompts = %+v, want investigate_task", listResult.Prompts)
	}

	getResp := runServer(t, []string{
		`{"jsonrpc":"2.0","id":2,"method":"prompts/get","params":{"name":"investigate_task","arguments":{"task_id":"task-1"}}}`,
	}, register)
	var getRPC mcp.Response
	_ = json.Unmarshal([]byte(getResp[0]), &getRPC)
	var getResult mcp.GetPromptResult
	if err := json.Unmarshal(getRPC.Result, &getResult); err != nil {
		t.Fatalf("decode prompts/get: %v", err)
	}
	if len(getResult.Messages) != 1 || getResult.Messages[0].Content.Text != "Inspect task-1" {
		t.Fatalf("messages = %+v, want rendered prompt", getResult.Messages)
	}
}

func TestServer_InitializeResponse_HasDescription(t *testing.T) {
	// 2025-11-25 minor #2: ServerInfo.Description is optional but
	// surfaces during initialize so clients can render context. We
	// pin that SetDescription wires through to the wire shape.
	in := newRWPipe()
	var outBuf safeBuffer
	srv := NewServer("hecate-test", "0.0.0")
	srv.SetDescription("a test server")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, in, &outBuf) }()
	_, _ = in.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"t","version":"1"}}}` + "\n"))
	_ = in.w.Close()
	<-done

	var r mcp.Response
	_ = json.Unmarshal([]byte(outBuf.String()), &r)
	var result mcp.InitializeResult
	_ = json.Unmarshal(r.Result, &result)
	if result.ServerInfo.Description != "a test server" {
		t.Fatalf("mcp.ServerInfo.Description = %q, want 'a test server'", result.ServerInfo.Description)
	}
}

func TestServer_CallTool_Success(t *testing.T) {
	register := func(s *Server) {
		s.RegisterTool(mcp.Tool{Name: "ping-tool", InputSchema: json.RawMessage(`{}`)},
			func(ctx context.Context, args json.RawMessage) (mcp.CallToolResult, error) {
				return mcp.CallToolResult{Content: mcp.TextContent("pong")}, nil
			})
	}
	resp := runServer(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping-tool","arguments":{}}}`,
	}, register)
	if len(resp) != 1 {
		t.Fatalf("got %d responses, want 1", len(resp))
	}
	var r mcp.Response
	_ = json.Unmarshal([]byte(resp[0]), &r)
	var result mcp.CallToolResult
	_ = json.Unmarshal(r.Result, &result)
	if len(result.Content) != 1 || result.Content[0].Text != "pong" {
		t.Fatalf("Content = %+v, want one text block 'pong'", result.Content)
	}
	if result.IsError {
		t.Fatalf("IsError = true on success path")
	}
}

func TestServer_CallTool_HandlerErrorIsToolLevel(t *testing.T) {
	// Handler errors must NOT bubble up as JSON-RPC errors — they're
	// returned as CallToolResult with isError=true. This is the MCP
	// contract: protocol errors and tool errors are different things.
	register := func(s *Server) {
		s.RegisterTool(mcp.Tool{Name: "boom", InputSchema: json.RawMessage(`{}`)},
			func(ctx context.Context, args json.RawMessage) (mcp.CallToolResult, error) {
				return mcp.CallToolResult{}, errors.New("upstream is down")
			})
	}
	resp := runServer(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"boom","arguments":{}}}`,
	}, register)
	var r mcp.Response
	_ = json.Unmarshal([]byte(resp[0]), &r)
	if r.Error != nil {
		t.Fatalf("handler error became JSON-RPC error: %+v", r.Error)
	}
	var result mcp.CallToolResult
	_ = json.Unmarshal(r.Result, &result)
	if !result.IsError {
		t.Fatalf("IsError should be true; got %+v", result)
	}
	if result.Content[0].Text != "upstream is down" {
		t.Fatalf("Content = %+v, want error message", result.Content)
	}
}

func TestServer_CallTool_UnknownToolIsInvalidParams(t *testing.T) {
	resp := runServer(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"missing","arguments":{}}}`,
	}, nil)
	var r mcp.Response
	_ = json.Unmarshal([]byte(resp[0]), &r)
	if r.Error == nil || r.Error.Code != mcp.ErrCodeInvalidParams {
		t.Fatalf("got %+v, want code %d", r.Error, mcp.ErrCodeInvalidParams)
	}
}
