// Package acp implements an ACP (Agent Client Protocol) bridge that
// presents the Hecate gateway's agent_loop runtime to ACP-aware editors
// (Zed, JetBrains 2025.3+) over stdio JSON-RPC.
//
// Single-user mode: there's no auth and no tenant — the bridge connects
// to a loopback gateway and forwards everything as the implicit
// operator. The editor user's threat model is "trust your own machine"
// (same as `bun run dev`); the gateway binds to 127.0.0.1 by default.
//
// Transport: stdio with newline-delimited JSON. Each line is a
// complete JSON-RPC 2.0 envelope. No length prefix.
//
// Spec target: see DeclaredProtocolVersion below.
package acp

import "encoding/json"

// DeclaredProtocolVersion is what the bridge advertises during the
// `initialize` handshake.
const DeclaredProtocolVersion = "0.1"

// ACP method names. Defined as constants so typos surface at compile
// time rather than as silently-ignored RPCs.
const (
	MethodInitialize         = "initialize"
	MethodSessionNew         = "session/new"
	MethodSessionPrompt      = "session/prompt"
	MethodSessionCancel      = "session/cancel"
	MethodSessionUpdate      = "session/update"             // server → client notification
	MethodSessionRequestPerm = "session/request_permission" // server → client request
)

// Request is a JSON-RPC 2.0 request OR notification.
type Request struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

// IsNotification reports whether this Request is a notification.
func (r *Request) IsNotification() bool { return r.ID == nil }

// Response is a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *RPCError        `json:"error,omitempty"`
}

// RPCError is the JSON-RPC 2.0 error envelope.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Message
}

// Standard JSON-RPC 2.0 error codes plus ACP-specific extensions.
const (
	ErrorParse          = -32700
	ErrorInvalidRequest = -32600
	ErrorMethodNotFound = -32601
	ErrorInvalidParams  = -32602
	ErrorInternal       = -32603

	ErrorGatewayUnreachable = -32001 // network failure talking to gateway
	ErrorModelNotPermitted  = -32002 // requested model isn't in the gateway's model list
	ErrorSessionNotFound    = -32003 // session_id doesn't exist (or expired with bridge restart)
)

// JSONRPCVersion is the protocol version string set on every envelope.
const JSONRPCVersion = "2.0"
