package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Dispatcher routes incoming JSON-RPC requests to typed handlers.
// One Dispatcher per bridge process.
type Dispatcher struct {
	sessions *SessionStore
	gateway  GatewayClient
	cfg      Config
	emit     func(*Request)

	initialized bool

	mu                     sync.Mutex
	nextPermissionSequence int64
	pendingPermissions     map[string]pendingPermission
	pendingApprovalIDs     map[string]string
}

type pendingPermission struct {
	SessionID  string
	TaskID     string
	RunID      string
	ApprovalID string
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
		sessions:           sessions,
		gateway:            gateway,
		cfg:                cfg,
		pendingPermissions: make(map[string]pendingPermission),
		pendingApprovalIDs: make(map[string]string),
	}
}

func (d *Dispatcher) SetEmitter(emit func(*Request)) {
	d.emit = emit
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

func (d *Dispatcher) HandleResponse(ctx context.Context, resp *Response) {
	id := responseIDKey(resp.ID)
	if id == "" {
		return
	}
	pending, ok := d.takePendingPermission(id)
	if !ok {
		return
	}
	decision, note, err := approvalDecisionFromResponse(resp)
	if err != nil {
		d.emitUpdate(SessionUpdateParams{
			SessionID: pending.SessionID,
			Kind:      "error",
			Status:    "failed",
			Message:   err.Error(),
			TaskID:    pending.TaskID,
			RunID:     pending.RunID,
		})
		return
	}
	if err := d.gateway.ResolveApproval(ctx, pending.TaskID, pending.RunID, pending.ApprovalID, decision); err != nil {
		d.emitUpdate(SessionUpdateParams{
			SessionID: pending.SessionID,
			Kind:      "error",
			Status:    "failed",
			Message:   "could not resolve Hecate approval: " + err.Error(),
			TaskID:    pending.TaskID,
			RunID:     pending.RunID,
			Data:      map[string]any{"approval_id": pending.ApprovalID, "note": note},
		})
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
	var params SessionNewParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errorResponse(req.ID, ErrorInvalidParams, "session/new params are not valid JSON: "+err.Error(), nil)
		}
	}
	cwd := firstNonEmpty(params.CWD, params.WorkingDirectory, ".")
	session := d.sessions.Create(params.Model, cwd)
	return resultResponse(req.ID, SessionNewResult{
		SessionID: session.ID,
		Model:     session.Model,
		CWD:       session.CWD,
	})
}

func (d *Dispatcher) handleSessionPrompt(ctx context.Context, req *Request) *Response {
	if !d.initialized {
		return errorResponse(req.ID, ErrorInvalidRequest, "session/prompt called before initialize", nil)
	}
	var params SessionPromptParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, ErrorInvalidParams, "session/prompt params are not valid JSON: "+err.Error(), nil)
	}
	if params.SessionID == "" {
		return errorResponse(req.ID, ErrorInvalidParams, "session_id is required", nil)
	}
	if params.Prompt == "" {
		return errorResponse(req.ID, ErrorInvalidParams, "prompt is required", nil)
	}
	session := d.sessions.Get(params.SessionID)
	if session == nil {
		return errorResponse(req.ID, ErrorSessionNotFound, "session not found", nil)
	}
	taskID := session.HecateTaskID
	runID := session.CurrentRunID
	if taskID == "" || runID == "" {
		result, err := d.gateway.CreateAgentLoopTask(ctx, CreateTaskRequest{
			Model:            session.Model,
			WorkingDirectory: session.CWD,
			Prompt:           params.Prompt,
		})
		if err != nil {
			return errorResponse(req.ID, ErrorGatewayUnreachable, "could not start Hecate task: "+err.Error(), nil)
		}
		taskID, runID = result.TaskID, result.RunID
	} else {
		nextRunID, err := d.gateway.ContinueAgentLoopTask(ctx, taskID, runID, params.Prompt)
		if err != nil {
			return errorResponse(req.ID, ErrorGatewayUnreachable, "could not continue Hecate task: "+err.Error(), nil)
		}
		runID = nextRunID
	}
	session, _ = d.sessions.UpdateRun(session.ID, taskID, runID)
	d.emitUpdate(SessionUpdateParams{
		SessionID: session.ID,
		Kind:      "runtime",
		Status:    "started",
		Message:   "Hecate task started",
		TaskID:    taskID,
		RunID:     runID,
	})
	go d.forwardRunEvents(ctx, session.ID, taskID, runID)
	return resultResponse(req.ID, SessionPromptResult{
		SessionID: session.ID,
		TaskID:    taskID,
		RunID:     runID,
	})
}

func (d *Dispatcher) handleSessionCancel(ctx context.Context, req *Request) *Response {
	if !d.initialized {
		return errorResponse(req.ID, ErrorInvalidRequest, "session/cancel called before initialize", nil)
	}
	var params SessionCancelParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, ErrorInvalidParams, "session/cancel params are not valid JSON: "+err.Error(), nil)
	}
	session := d.sessions.Get(params.SessionID)
	if session == nil {
		return errorResponse(req.ID, ErrorSessionNotFound, "session not found", nil)
	}
	if session.HecateTaskID == "" || session.CurrentRunID == "" {
		return resultResponse(req.ID, SessionCancelResult{SessionID: session.ID, Cancelled: false})
	}
	if err := d.gateway.CancelRun(ctx, session.HecateTaskID, session.CurrentRunID, params.Reason); err != nil {
		return errorResponse(req.ID, ErrorGatewayUnreachable, "could not cancel Hecate run: "+err.Error(), nil)
	}
	return resultResponse(req.ID, SessionCancelResult{
		SessionID: session.ID,
		TaskID:    session.HecateTaskID,
		RunID:     session.CurrentRunID,
		Cancelled: true,
	})
}

func (d *Dispatcher) forwardRunEvents(ctx context.Context, sessionID, taskID, runID string) {
	events, err := d.gateway.StreamRunEvents(ctx, taskID, runID)
	if err != nil {
		d.emitUpdate(SessionUpdateParams{
			SessionID: sessionID,
			Kind:      "error",
			Status:    "failed",
			Message:   err.Error(),
			TaskID:    taskID,
			RunID:     runID,
			Terminal:  true,
		})
		return
	}
	for event := range events {
		update := RunEventToSessionUpdate(sessionID, taskID, runID, event)
		d.emitUpdate(update)
		if d.cfg.ApprovalRoute == "editor" {
			d.emitPermissionRequest(update)
		}
	}
}

func (d *Dispatcher) emitPermissionRequest(update SessionUpdateParams) {
	params, ok := PendingPermissionFromSessionUpdate(update)
	if !ok {
		return
	}
	id, ok := d.trackPendingPermission(params)
	if !ok {
		return
	}
	if d.emit != nil {
		d.emit(SessionRequestPermission(id, params))
	}
}

func (d *Dispatcher) trackPendingPermission(params PermissionRequestParams) (string, bool) {
	approvalKey := params.TaskID + "/" + params.RunID + "/" + params.ApprovalID
	d.mu.Lock()
	defer d.mu.Unlock()
	if id := d.pendingApprovalIDs[approvalKey]; id != "" {
		return id, false
	}
	d.nextPermissionSequence++
	id := fmt.Sprintf("permission-%d", d.nextPermissionSequence)
	d.pendingApprovalIDs[approvalKey] = id
	d.pendingPermissions[id] = pendingPermission{
		SessionID:  params.SessionID,
		TaskID:     params.TaskID,
		RunID:      params.RunID,
		ApprovalID: params.ApprovalID,
	}
	return id, true
}

func (d *Dispatcher) takePendingPermission(id string) (pendingPermission, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	pending, ok := d.pendingPermissions[id]
	if !ok {
		return pendingPermission{}, false
	}
	delete(d.pendingPermissions, id)
	delete(d.pendingApprovalIDs, pending.TaskID+"/"+pending.RunID+"/"+pending.ApprovalID)
	return pending, true
}

func (d *Dispatcher) emitUpdate(update SessionUpdateParams) {
	if d.emit == nil {
		return
	}
	d.emit(SessionUpdateNotification(update))
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func responseIDKey(id *json.RawMessage) string {
	if id == nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(*id, &s); err == nil {
		return s
	}
	return strings.TrimSpace(string(*id))
}

func approvalDecisionFromResponse(resp *Response) (ApprovalDecision, string, error) {
	if resp.Error != nil {
		return ApprovalDeny, resp.Error.Message, fmt.Errorf("permission request failed: %s", resp.Error.Message)
	}
	var result PermissionResponseResult
	if len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return "", "", fmt.Errorf("permission response result is not valid JSON: %w", err)
		}
	}
	if result.Allowed != nil {
		if *result.Allowed {
			return ApprovalAllow, result.Note, nil
		}
		return ApprovalDeny, result.Note, nil
	}
	switch strings.ToLower(strings.TrimSpace(result.Decision)) {
	case "allow", "approve", "approved", "yes":
		return ApprovalAllow, result.Note, nil
	case "deny", "reject", "rejected", "no":
		return ApprovalDeny, result.Note, nil
	default:
		return "", result.Note, fmt.Errorf("permission response decision must be allow or deny")
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
