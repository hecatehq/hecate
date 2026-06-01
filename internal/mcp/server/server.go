package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/hecatehq/hecate/internal/mcp"
)

// Server is the MCP server core. Wire it with RegisterTool, then call
// Serve to drive a stdio (or any io.Reader/io.Writer) loop. Concurrent
// requests share the dispatcher; each request handler runs in its own
// goroutine so a slow tool doesn't head-of-line block the next one.
//
// The server is designed to live as long as the stdio pair — there's
// no graceful-restart story. When stdin closes, Serve returns; when
// the parent process kills us, we exit. That matches how Claude
// Desktop / Cursor / Zed manage MCP subprocesses today.
type Server struct {
	info      mcp.ServerInfo
	tools     toolRegistry
	resources resourceRegistry
	prompts   promptRegistry

	// Mutex guards writes to the output stream — multiple goroutines
	// produce responses concurrently and JSON-RPC framing requires an
	// uninterrupted message per write.
	writeMu sync.Mutex
}

// NewServer constructs an MCP server with the given identity. The name
// is what shows up in MCP client UIs (Claude Desktop's connector list,
// Cursor's @-mention, etc.); pick something operators recognize.
func NewServer(name, version string) *Server {
	return &Server{
		info: mcp.ServerInfo{Name: name, Version: version},
		tools: toolRegistry{
			byName: make(map[string]registeredTool),
		},
		resources: resourceRegistry{
			byURI: make(map[string]registeredResource),
		},
		prompts: promptRegistry{
			byName: make(map[string]registeredPrompt),
		},
	}
}

// SetDescription attaches a human-readable description that's surfaced
// in the initialize response (per MCP 2025-11-25). Optional — clients
// that don't render it just ignore the field.
func (s *Server) SetDescription(d string) { s.info.Description = d }

// RegisterTool wires a tool into the server. Must be called before
// Serve; the registry is not safe for concurrent mutation while a
// dispatcher is active.
//
// schema must be a JSON Schema document (json.RawMessage) describing
// the tool's `arguments` shape — clients use it for autocomplete /
// validation. Pass json.RawMessage("{}") for "any object".
func (s *Server) RegisterTool(tool mcp.Tool, handler ToolHandler) {
	s.tools.byName[tool.Name] = registeredTool{
		descriptor: tool,
		handler:    handler,
	}
}

// RegisterResource wires a concrete read-only resource URI into the
// server. The descriptor is returned from resources/list; the handler
// is invoked for resources/read of the same URI.
func (s *Server) RegisterResource(resource mcp.Resource, handler ResourceHandler) {
	s.resources.byURI[resource.URI] = registeredResource{
		descriptor: resource,
		handler:    handler,
	}
}

// RegisterResourceTemplate advertises a URI template. The handler is
// tried for resources/read requests that were not handled by a concrete
// resource URI.
func (s *Server) RegisterResourceTemplate(template mcp.ResourceTemplate, handler ResourceHandler) {
	s.resources.templates = append(s.resources.templates, registeredResourceTemplate{
		descriptor: template,
		handler:    handler,
	})
}

// RegisterPrompt wires a user-invoked prompt template into the server.
func (s *Server) RegisterPrompt(prompt mcp.Prompt, handler PromptHandler) {
	s.prompts.byName[prompt.Name] = registeredPrompt{
		descriptor: prompt,
		handler:    handler,
	}
}

// Serve drives the JSON-RPC loop. Reads newline-delimited messages
// from in, writes responses to out. Returns when in is closed (EOF) or
// when ctx is cancelled — and only AFTER all in-flight handlers have
// produced their responses (we wait on a WaitGroup so a fast-closing
// stdin doesn't drop the last response).
//
// Reader buffer is bumped because tool arguments can carry sizable
// JSON. 1 MiB is enough headroom for any practical use case while
// still bounding memory if a client misbehaves.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	// Outer-context-cancellation wins over scanner.Scan blocking on
	// stdin: when ctx fires we ask the OS to close stdin, which makes
	// Scan return false and the loop unwinds.
	go func() {
		<-ctx.Done()
		if closer, ok := in.(io.Closer); ok {
			_ = closer.Close()
		}
	}()

	var wg sync.WaitGroup
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// Copy because scanner.Bytes is reused on the next call.
		msg := make([]byte, len(line))
		copy(msg, line)

		wg.Add(1)
		go func() {
			defer wg.Done()
			s.handleMessage(ctx, msg, out)
		}()
	}
	// Wait for in-flight handlers before returning. Without this,
	// closing stdin races with the last response write — the parent
	// process can lose the final reply.
	wg.Wait()

	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("mcp: scanner: %w", err)
	}
	return nil
}

// handleMessage parses one JSON-RPC envelope and dispatches it. Errors
// at the parse layer become JSON-RPC error responses (or are silently
// dropped for notifications, per spec).
func (s *Server) handleMessage(ctx context.Context, raw []byte, out io.Writer) {
	var req mcp.Request
	if err := json.Unmarshal(raw, &req); err != nil {
		// Parse error — we don't know the ID so send a best-effort
		// error response with a null ID, per JSON-RPC §5.1.
		s.writeResponse(out, errorResponse(nil, mcp.NewError(mcp.ErrCodeParseError, "parse error: "+err.Error())))
		return
	}
	if req.JSONRPC != "2.0" {
		s.writeResponse(out, errorResponse(req.ID, mcp.NewError(mcp.ErrCodeInvalidRequest, "jsonrpc must be \"2.0\"")))
		return
	}

	result, rpcErr := s.dispatch(ctx, &req)

	// Notifications must not get a response, even on error.
	if req.IsNotification() {
		return
	}

	if rpcErr != nil {
		s.writeResponse(out, errorResponse(req.ID, rpcErr))
		return
	}
	s.writeResponse(out, successResponse(req.ID, result))
}

// dispatch routes the request to the right method handler.
//
// Methods we implement:
//   - initialize             → handshake; returns server capabilities
//   - notifications/initialized → ack from client; we don't need to
//     react but spec requires we accept it
//   - tools/list             → enumerate registered tools
//   - tools/call             → invoke a tool by name
//   - resources/list         → enumerate concrete resources
//   - resources/templates/list → enumerate URI templates
//   - resources/read         → read one resource URI
//   - prompts/list           → enumerate prompt templates
//   - prompts/get            → render one prompt template
//   - ping                   → liveness check; returns empty result
//
// Unknown methods get a -32601 (method not found) response so MCP
// clients that probe optional capabilities don't see hard failures.
func (s *Server) dispatch(ctx context.Context, req *mcp.Request) (any, *mcp.RPCError) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "notifications/initialized":
		// No-op ack.
		return nil, nil
	case "tools/list":
		return s.handleListTools()
	case "tools/call":
		return s.handleCallTool(ctx, req)
	case "resources/list":
		return s.handleListResources()
	case "resources/templates/list":
		return s.handleListResourceTemplates()
	case "resources/read":
		return s.handleReadResource(ctx, req)
	case "prompts/list":
		return s.handleListPrompts()
	case "prompts/get":
		return s.handleGetPrompt(ctx, req)
	case "ping":
		return struct{}{}, nil
	default:
		return nil, mcp.NewError(mcp.ErrCodeMethodNotFound, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func (s *Server) handleInitialize(req *mcp.Request) (any, *mcp.RPCError) {
	var params mcp.InitializeParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid initialize params: "+err.Error())
		}
	}
	// We accept whatever protocol version the client requested but
	// reply with our own — clients are expected to negotiate down if
	// they support multiple versions.
	return mcp.InitializeResult{
		ProtocolVersion: mcp.DeclaredProtocolVersion,
		Capabilities:    s.capabilities(),
		ServerInfo:      s.info,
	}, nil
}

func (s *Server) capabilities() mcp.ServerCapabilities {
	caps := mcp.ServerCapabilities{
		Tools: &mcp.ToolsCapability{},
	}
	if s.resources.hasAny() {
		caps.Resources = &mcp.ResourcesCapability{}
	}
	if s.prompts.hasAny() {
		caps.Prompts = &mcp.PromptsCapability{}
	}
	return caps
}

func (s *Server) handleListTools() (any, *mcp.RPCError) {
	return mcp.ListToolsResult{Tools: s.tools.list()}, nil
}

func (s *Server) handleCallTool(ctx context.Context, req *mcp.Request) (any, *mcp.RPCError) {
	// A malformed tools/call envelope itself (e.g. completely invalid
	// JSON in `params`) is a protocol error — the client got the
	// shape wrong, not the model. Keep this as JSON-RPC -32602.
	var params mcp.CallToolParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid tools/call params: "+err.Error())
	}
	tool, ok := s.tools.byName[params.Name]
	if !ok {
		return nil, mcp.NewError(mcp.ErrCodeInvalidParams, fmt.Sprintf("unknown tool: %s", params.Name))
	}
	// Tool-level error → CallToolResult with isError=true.
	// Per MCP 2025-11-25 (minor change #5): "input validation errors
	// should be returned as Tool Execution Errors rather than Protocol
	// Errors to enable model self-correction." So a handler that
	// returns an error (including bad-arguments-JSON inside the
	// handler) becomes a tool result the model can read and adjust.
	result, err := tool.handler(ctx, params.Arguments)
	if err != nil {
		return mcp.CallToolResult{
			Content: mcp.TextContent(err.Error()),
			IsError: true,
		}, nil
	}
	return result, nil
}

func (s *Server) handleListResources() (any, *mcp.RPCError) {
	return mcp.ListResourcesResult{Resources: s.resources.list()}, nil
}

func (s *Server) handleListResourceTemplates() (any, *mcp.RPCError) {
	return mcp.ListResourceTemplatesResult{ResourceTemplates: s.resources.listTemplates()}, nil
}

func (s *Server) handleReadResource(ctx context.Context, req *mcp.Request) (any, *mcp.RPCError) {
	var params mcp.ReadResourceParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid resources/read params: "+err.Error())
	}
	if params.URI == "" {
		return nil, mcp.NewError(mcp.ErrCodeInvalidParams, "uri is required")
	}
	result, err := s.resources.read(ctx, params.URI)
	if err != nil {
		return nil, mcp.NewError(mcp.ErrCodeInvalidParams, err.Error())
	}
	return result, nil
}

func (s *Server) handleListPrompts() (any, *mcp.RPCError) {
	return mcp.ListPromptsResult{Prompts: s.prompts.list()}, nil
}

func (s *Server) handleGetPrompt(ctx context.Context, req *mcp.Request) (any, *mcp.RPCError) {
	var params mcp.GetPromptParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, mcp.NewError(mcp.ErrCodeInvalidParams, "invalid prompts/get params: "+err.Error())
	}
	prompt, ok := s.prompts.byName[params.Name]
	if !ok {
		return nil, mcp.NewError(mcp.ErrCodeInvalidParams, fmt.Sprintf("unknown prompt: %s", params.Name))
	}
	result, err := prompt.handler(ctx, params.Arguments)
	if err != nil {
		return nil, mcp.NewError(mcp.ErrCodeInvalidParams, err.Error())
	}
	return result, nil
}

// ─── Output ──────────────────────────────────────────────────────────

func (s *Server) writeResponse(out io.Writer, resp mcp.Response) {
	body, err := json.Marshal(resp)
	if err != nil {
		// Should never happen — every field is JSON-marshalable by
		// construction. Log to stderr (the dispatcher's logger isn't
		// available here) and drop.
		fmt.Fprintf(out, `{"jsonrpc":"2.0","id":null,"error":{"code":%d,"message":%q}}`+"\n",
			mcp.ErrCodeInternalError, "internal: marshal response: "+err.Error())
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = out.Write(body)
	_, _ = out.Write([]byte("\n"))
}

func successResponse(id *json.RawMessage, result any) mcp.Response {
	raw, err := json.Marshal(result)
	if err != nil {
		return errorResponse(id, mcp.NewError(mcp.ErrCodeInternalError, "marshal result: "+err.Error()))
	}
	return mcp.Response{JSONRPC: "2.0", ID: id, Result: raw}
}

func errorResponse(id *json.RawMessage, e *mcp.RPCError) mcp.Response {
	return mcp.Response{JSONRPC: "2.0", ID: id, Error: e}
}

// ─── Tool registry ───────────────────────────────────────────────────

// ToolHandler is the function signature for a tool implementation.
// Args is the raw JSON-encoded arguments object — handlers unmarshal
// into their own typed shape. Returning a non-nil error becomes a
// tool-level failure (CallToolResult with isError=true); returning a
// CallToolResult lets the handler set the content blocks directly.
type ToolHandler func(ctx context.Context, args json.RawMessage) (mcp.CallToolResult, error)

type registeredTool struct {
	descriptor mcp.Tool
	handler    ToolHandler
}

type toolRegistry struct {
	byName map[string]registeredTool
}

// list returns descriptors in registration order. We use a separate
// `order` slice rather than relying on map iteration so the wire
// output is stable across runs — clients cache lists and a churning
// order would invalidate caches needlessly.
func (r toolRegistry) list() []mcp.Tool {
	out := make([]mcp.Tool, 0, len(r.byName))
	for _, t := range r.byName {
		out = append(out, t.descriptor)
	}
	// Sort by name for deterministic ordering. Map iteration is random
	// in Go; without a sort the same client would see a different list
	// order on each connect, which makes change-detection lossy.
	sortToolsByName(out)
	return out
}

func sortToolsByName(tools []mcp.Tool) {
	for i := 1; i < len(tools); i++ {
		for j := i; j > 0 && tools[j-1].Name > tools[j].Name; j-- {
			tools[j-1], tools[j] = tools[j], tools[j-1]
		}
	}
}

// ─── Resource registry ──────────────────────────────────────────────

type ResourceHandler func(ctx context.Context, uri string) (mcp.ReadResourceResult, error)

type registeredResource struct {
	descriptor mcp.Resource
	handler    ResourceHandler
}

type registeredResourceTemplate struct {
	descriptor mcp.ResourceTemplate
	handler    ResourceHandler
}

type resourceRegistry struct {
	byURI     map[string]registeredResource
	templates []registeredResourceTemplate
}

var errResourceNoMatch = errors.New("resource template did not match")

func (r resourceRegistry) hasAny() bool {
	return len(r.byURI) > 0 || len(r.templates) > 0
}

func (r resourceRegistry) list() []mcp.Resource {
	out := make([]mcp.Resource, 0, len(r.byURI))
	for _, resource := range r.byURI {
		out = append(out, resource.descriptor)
	}
	sortResourcesByURI(out)
	return out
}

func (r resourceRegistry) listTemplates() []mcp.ResourceTemplate {
	out := make([]mcp.ResourceTemplate, 0, len(r.templates))
	for _, template := range r.templates {
		out = append(out, template.descriptor)
	}
	sortResourceTemplatesByURI(out)
	return out
}

func (r resourceRegistry) read(ctx context.Context, uri string) (mcp.ReadResourceResult, error) {
	if resource, ok := r.byURI[uri]; ok {
		return resource.handler(ctx, uri)
	}
	for _, template := range r.templates {
		result, err := template.handler(ctx, uri)
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, errResourceNoMatch) {
			return mcp.ReadResourceResult{}, err
		}
	}
	return mcp.ReadResourceResult{}, fmt.Errorf("resource not found: %s", uri)
}

func sortResourcesByURI(resources []mcp.Resource) {
	for i := 1; i < len(resources); i++ {
		for j := i; j > 0 && resources[j-1].URI > resources[j].URI; j-- {
			resources[j-1], resources[j] = resources[j], resources[j-1]
		}
	}
}

func sortResourceTemplatesByURI(templates []mcp.ResourceTemplate) {
	for i := 1; i < len(templates); i++ {
		for j := i; j > 0 && templates[j-1].URITemplate > templates[j].URITemplate; j-- {
			templates[j-1], templates[j] = templates[j], templates[j-1]
		}
	}
}

// ─── Prompt registry ────────────────────────────────────────────────

type PromptHandler func(ctx context.Context, args map[string]string) (mcp.GetPromptResult, error)

type registeredPrompt struct {
	descriptor mcp.Prompt
	handler    PromptHandler
}

type promptRegistry struct {
	byName map[string]registeredPrompt
}

func (r promptRegistry) hasAny() bool {
	return len(r.byName) > 0
}

func (r promptRegistry) list() []mcp.Prompt {
	out := make([]mcp.Prompt, 0, len(r.byName))
	for _, prompt := range r.byName {
		out = append(out, prompt.descriptor)
	}
	sortPromptsByName(out)
	return out
}

func sortPromptsByName(prompts []mcp.Prompt) {
	for i := 1; i < len(prompts); i++ {
		for j := i; j > 0 && prompts[j-1].Name > prompts[j].Name; j-- {
			prompts[j-1], prompts[j] = prompts[j], prompts[j-1]
		}
	}
}
