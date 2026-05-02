package acp

import (
	"context"
	"encoding/json"
	"fmt"
)

// Dispatcher routes incoming JSON-RPC requests to typed handlers.
// One Dispatcher per bridge process.
type Dispatcher struct {
	sessions *SessionStore
	gateway  GatewayClient
	cfg      Config

	initialized bool
}

// Config is the bridge's install-time configuration.
type Config struct {
	AgentName     string
	AgentVersion  string
	WorkspaceMode string
	ApprovalRoute string
}

func NewDispatcher(gateway GatewayClient, sessions *SessionStore, cfg Config) *Dispatcher {
	if cfg.AgentName == "" {
		cfg.AgentName = "Hecate"
	}
	if cfg.WorkspaceMode == "" {
		cfg.WorkspaceMode = "hecate-owned"
	}
	if cfg.ApprovalRoute == "" {
		cfg.ApprovalRoute = "editor"
	}
	return &Dispatcher{
		sessions: sessions,
		gateway:  gateway,
		cfg:      cfg,
	}
}

// Handle dispatches one incoming request.
func (d *Dispatcher) Handle(ctx context.Context, req *Request) *Response {
	if req.IsNotification() {
		return nil
	}
	switch req.Method {
	case MethodInitialize:
		return d.handleInitialize(ctx, req)
	case MethodSessionNew:
		return d.handleSessionNew(ctx, req)
	case MethodSessionPrompt:
		return d.handleSessionPrompt(ctx, req)
	case MethodSessionCancel:
		return d.handleSessionCancel(ctx, req)
	default:
		return errorResponse(req.ID, ErrorMethodNotFound,
			fmt.Sprintf("method %q is not supported by this ACP bridge", req.Method), nil)
	}
}

func (d *Dispatcher) handleInitialize(ctx context.Context, req *Request) *Response {
	if d.initialized {
		return errorResponse(req.ID, ErrorInvalidRequest,
			"initialize called more than once on this bridge process", nil)
	}
	var params InitializeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, ErrorInvalidParams,
			"initialize params are not valid JSON: "+err.Error(), nil)
	}
	if params.ClientCaps.Permissions == nil {
		return errorResponse(req.ID, ErrorInvalidRequest,
			"editor must declare permissions capability — Hecate's approval gates require session/request_permission support", nil)
	}

	models, err := d.gateway.ListModels(ctx)
	if err != nil {
		return errorResponse(req.ID, ErrorGatewayUnreachable,
			"could not reach Hecate gateway: "+err.Error(), nil)
	}

	descriptions := make([]ModelDescription, 0, len(models))
	for _, m := range models {
		descriptions = append(descriptions, ModelDescription{ID: m, DisplayName: m})
	}

	result := InitializeResult{
		ProtocolVersion: DeclaredProtocolVersion,
		AgentCaps: AgentCapabilities{
			Prompt:      true,
			Cancel:      true,
			Permissions: d.cfg.ApprovalRoute == "editor",
		},
		AgentInfo: AgentInfo{
			Name:        d.cfg.AgentName,
			Version:     d.cfg.AgentVersion,
			Description: "Hecate agent — managed runtime, sandboxed tools, approval-gated, OTel-traced.",
		},
		AvailableModels: descriptions,
	}
	d.initialized = true
	return resultResponse(req.ID, result)
}

func (d *Dispatcher) handleSessionNew(_ context.Context, req *Request) *Response {
	if !d.initialized {
		return errorResponse(req.ID, ErrorInvalidRequest, "session/new called before initialize", nil)
	}
	return errorResponse(req.ID, ErrorInternal, "session/new is not yet implemented in this build of hecate-acp", nil)
}

func (d *Dispatcher) handleSessionPrompt(_ context.Context, req *Request) *Response {
	if !d.initialized {
		return errorResponse(req.ID, ErrorInvalidRequest, "session/prompt called before initialize", nil)
	}
	return errorResponse(req.ID, ErrorInternal, "session/prompt is not yet implemented in this build of hecate-acp", nil)
}

func (d *Dispatcher) handleSessionCancel(_ context.Context, req *Request) *Response {
	if !d.initialized {
		return errorResponse(req.ID, ErrorInvalidRequest, "session/cancel called before initialize", nil)
	}
	return errorResponse(req.ID, ErrorInternal, "session/cancel is not yet implemented in this build of hecate-acp", nil)
}

func resultResponse(id *json.RawMessage, result any) *Response {
	encoded, err := json.Marshal(result)
	if err != nil {
		panic("acp: result marshal failed for type " + fmt.Sprintf("%T: %v", result, err))
	}
	return &Response{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Result:  encoded,
	}
}

func errorResponse(id *json.RawMessage, code int, message string, data any) *Response {
	resp := &Response{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Error: &RPCError{
			Code:    code,
			Message: message,
		},
	}
	if data != nil {
		encoded, err := json.Marshal(data)
		if err != nil {
			return resp
		}
		resp.Error.Data = encoded
	}
	return resp
}
