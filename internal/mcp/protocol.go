// Package mcp implements an MCP (Model Context Protocol) server that
// exposes the Hecate gateway's task, session, and observability surfaces
// to MCP clients (Claude Desktop, Cursor, Zed, ...).
//
// We hand-roll the protocol — there's no battle-tested Go SDK we trust
// yet, the wire format is small enough to keep readable, and we want
// the freedom to track spec revisions on our own cadence.
//
// Transport: stdio with newline-delimited JSON messages. Each line is a
// complete JSON-RPC 2.0 envelope. Frames are NOT length-prefixed (LSP
// uses Content-Length headers; MCP-stdio doesn't). HTTP/SSE transport
// is planned and will share the dispatcher in server.go but have its
// own framing.
//
// Spec target: protocol version "2025-11-25" — the current MCP
// revision as of this writing. We track the breaking-change-free
// surface (initialize / tools/list / tools/call / notifications/initialized
// / ping) and adopt the additive bits that improve client UX:
//
//   - tool `title` field (2025-06-18): separates the programmatic
//     identifier (`name`) from the human-readable display label
//   - tool annotations (2025-03-26): hints like readOnlyHint that let
//     clients skip "are you sure?" prompts on safe tools
//   - input validation as tool errors (2025-11-25): bad argument JSON
//     becomes a CallToolResult with isError=true rather than a
//     JSON-RPC -32602, so the model can self-correct
//   - server description (2025-11-25 minor): optional human-readable
//     context exposed during initialize
//
// Currently out of scope: OAuth / Streamable HTTP / elicitation /
// tasks primitive / resource links / sampling. None apply to
// stdio-only, tools-only servers.
package mcp

import "encoding/json"

// DeclaredProtocolVersion is what the server reports during the
// initialize handshake. Clients negotiate down to a version they speak;
// if a client sends a different version, we still accept it and reply
// with our supported version. Exported so the server subpackage (which
// owns the handshake) can read it without an unexported-cross-package
// dance, and so the client subpackage can mirror it on its own side.
const DeclaredProtocolVersion = "2025-11-25"

// JSON-RPC 2.0 wire types.
//
// We define our own rather than pulling in net/rpc/jsonrpc because the
// stdlib variant is JSON-RPC 1.0 and doesn't speak the 2.0 envelope
// (no `jsonrpc: "2.0"` field, different error shape). A 100-line
// hand-roll is simpler than wrapping the stdlib.

// Request is a JSON-RPC 2.0 request OR notification. The distinction:
// requests carry an `id` and require a response; notifications omit
// `id` and the server stays silent. We use *RawMessage for ID so the
// caller's choice of string vs number ID round-trips byte-for-byte.
type Request struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

// IsNotification reports whether this Request is a notification (no ID,
// no response expected). Per JSON-RPC 2.0 §4.1.
func (r *Request) IsNotification() bool { return r.ID == nil }

// Response is a JSON-RPC 2.0 response. Either Result OR Error is set,
// never both. We marshal Result via RawMessage so handler code can
// build any shape and we don't double-encode.
type Response struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *RPCError        `json:"error,omitempty"`
}

// RPCError is the error envelope. Code values follow JSON-RPC 2.0 plus
// MCP-specific extensions in the application range (-32000 and below).
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return e.Message }

// JSON-RPC 2.0 standard error codes.
const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603
)

// MCP-specific error codes live in the JSON-RPC application range.
// We pick a small set rather than mirroring every HTTP status from the
// upstream — clients don't care about the gateway's internal code.
const (
	// ErrCodeUpstreamError covers any failure the MCP server hits
	// while talking to the Hecate HTTP API (network, 5xx, timeouts).
	// The Data payload carries the raw upstream error string for
	// debugging.
	ErrCodeUpstreamError = -32001
)

// NewError constructs an RPCError. Keeps call sites compact.
func NewError(code int, msg string) *RPCError {
	return &RPCError{Code: code, Message: msg}
}

// NewErrorWithData attaches an arbitrary data payload (anything that
// json.Marshal handles).
func NewErrorWithData(code int, msg string, data any) *RPCError {
	raw, err := json.Marshal(data)
	if err != nil {
		// Fall back to a code-only error if the payload can't marshal —
		// surfacing the original error is more useful than panicking.
		return &RPCError{Code: code, Message: msg}
	}
	return &RPCError{Code: code, Message: msg, Data: raw}
}

// ─── MCP-specific payload types ──────────────────────────────────────

// InitializeParams is the initialize request body. We accept arbitrary
// client capabilities — no need to validate them; an unknown capability
// just goes unused.
type InitializeParams struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities,omitempty"`
	ClientInfo      ClientInfo      `json:"clientInfo,omitempty"`
}

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is the initialize response. We declare the server
// features this process actually registered. The `logging` capability
// is not declared (we don't emit MCP-formatted log notifications yet);
// host stderr from the subprocess instead.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
}

type ServerCapabilities struct {
	// Empty object signals "this server supports tools/list and
	// tools/call but doesn't broadcast list-changed notifications".
	// MCP wire format treats `{}` and `null` differently here.
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
	Prompts   *PromptsCapability   `json:"prompts,omitempty"`
}

type ToolsCapability struct {
	// ListChanged advertises that we'll emit `notifications/tools/list_changed`
	// when the tool set mutates. We don't (the set is fixed at startup),
	// so we leave this false.
	ListChanged bool `json:"listChanged,omitempty"`
}

type ResourcesCapability struct {
	// ListChanged advertises notifications/resources/list_changed. The
	// Hecate stdio server exposes a stable catalog and does not emit
	// list-change notifications.
	ListChanged bool `json:"listChanged,omitempty"`
	// Subscribe advertises resources/subscribe support. Hecate
	// resources are read-on-demand snapshots, so subscriptions stay off.
	Subscribe bool `json:"subscribe,omitempty"`
}

type PromptsCapability struct {
	// ListChanged advertises notifications/prompts/list_changed. The
	// Hecate prompt catalog is fixed for the process lifetime.
	ListChanged bool `json:"listChanged,omitempty"`
}

// ServerInfo is sent in the initialize response. The optional
// Description field was added in 2025-11-25 (minor change #2 — aligns
// with the MCP registry server.json format) for human-readable context
// during connection.
type ServerInfo struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

// Tool is the MCP tool descriptor returned by tools/list.
//
// Title is a human-friendly display label, separated from Name in
// 2025-06-18 so Name can be a stable programmatic identifier. Clients
// fall back to Name when Title is absent — backward compatible.
//
// Annotations carry behavioral hints (added in 2025-03-26) that let
// clients optimize UX: a readOnlyHint tool can skip an "are you sure?"
// confirmation; a destructiveHint tool gets one regardless of user
// preference. All optional.
type Tool struct {
	Name        string           `json:"name"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	InputSchema json.RawMessage  `json:"inputSchema"`
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
}

// ToolAnnotations is the optional behavioral-hint envelope on a tool.
// Every field is a *bool so we can distinguish "unset" (omit on the
// wire) from "explicitly false" (some clients treat unset and false
// differently — readOnlyHint=false is a stronger signal than absent).
type ToolAnnotations struct {
	// Title overrides Tool.Title at the annotation level. Clients
	// that don't read annotations still see Tool.Title; we duplicate
	// to be safe across SDK versions.
	Title string `json:"title,omitempty"`
	// ReadOnlyHint: this tool only reads, never mutates. Safe by
	// default; clients may auto-approve.
	ReadOnlyHint *bool `json:"readOnlyHint,omitempty"`
	// DestructiveHint: this tool may make irreversible changes.
	// Clients should always confirm. (Only meaningful when not
	// read-only.)
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	// IdempotentHint: repeating the call has no extra effect. Useful
	// for retry logic. (Only meaningful when not read-only.)
	IdempotentHint *bool `json:"idempotentHint,omitempty"`
	// OpenWorldHint: the tool reaches into an open universe (the web,
	// external APIs) rather than a closed local environment.
	OpenWorldHint *bool `json:"openWorldHint,omitempty"`
}

// BoolPtr is a small helper for ToolAnnotations callers — Go's
// composite literal syntax makes &true awkward enough to warrant a
// helper.
func BoolPtr(v bool) *bool { return &v }

// CallToolParams is the body of a tools/call request.
type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// CallToolResult is the body of the tools/call response. MCP allows
// rich content blocks (text, image, resource); we emit text-only
// because every tool we ship returns string output.
type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	// IsError surfaces tool-level failures (the call dispatched but the
	// tool itself errored). Distinct from JSON-RPC errors which are
	// reserved for protocol failures.
	IsError bool `json:"isError,omitempty"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// TextContent is the conventional shape of a tools/call result.
func TextContent(text string) []ContentBlock {
	return []ContentBlock{{Type: "text", Text: text}}
}

// ListToolsResult is the body of tools/list.
type ListToolsResult struct {
	Tools []Tool `json:"tools"`
}

// ─── Resources ──────────────────────────────────────────────────────

type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

type ResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

type ListResourcesResult struct {
	Resources []Resource `json:"resources"`
}

type ListResourceTemplatesResult struct {
	ResourceTemplates []ResourceTemplate `json:"resourceTemplates"`
}

type ReadResourceParams struct {
	URI string `json:"uri"`
}

type ReadResourceResult struct {
	Contents []ResourceContents `json:"contents"`
}

type ResourceContents struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// ─── Prompts ────────────────────────────────────────────────────────

type Prompt struct {
	Name        string           `json:"name"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

type PromptArgument struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type ListPromptsResult struct {
	Prompts []Prompt `json:"prompts"`
}

type GetPromptParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

type GetPromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

type PromptMessage struct {
	Role    string       `json:"role"`
	Content ContentBlock `json:"content"`
}
