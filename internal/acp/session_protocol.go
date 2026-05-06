package acp

import "encoding/json"

type SessionNewParams struct {
	Model            string `json:"model,omitempty"`
	CWD              string `json:"cwd,omitempty"`
	WorkingDirectory string `json:"working_directory,omitempty"`
}

type SessionNewResult struct {
	SessionID string `json:"session_id"`
	Model     string `json:"model,omitempty"`
	CWD       string `json:"cwd,omitempty"`
}

type SessionPromptParams struct {
	SessionID string `json:"session_id"`
	Prompt    string `json:"prompt"`
}

type SessionPromptResult struct {
	SessionID string `json:"session_id"`
	TaskID    string `json:"task_id"`
	RunID     string `json:"run_id"`
}

type SessionCancelParams struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason,omitempty"`
}

type SessionCancelResult struct {
	SessionID string `json:"session_id"`
	TaskID    string `json:"task_id,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	Cancelled bool   `json:"cancelled"`
}

type PermissionRequestParams struct {
	SessionID  string         `json:"session_id"`
	TaskID     string         `json:"task_id"`
	RunID      string         `json:"run_id"`
	ApprovalID string         `json:"approval_id"`
	Kind       string         `json:"kind,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	Message    string         `json:"message"`
	Data       map[string]any `json:"data,omitempty"`
}

type PermissionResponseResult struct {
	Decision string `json:"decision,omitempty"`
	Note     string `json:"note,omitempty"`
	Allowed  *bool  `json:"allowed,omitempty"`
}

type SessionUpdateParams struct {
	SessionID string         `json:"session_id"`
	Kind      string         `json:"kind"`
	Status    string         `json:"status,omitempty"`
	Message   string         `json:"message,omitempty"`
	TaskID    string         `json:"task_id,omitempty"`
	RunID     string         `json:"run_id,omitempty"`
	EventType string         `json:"event_type,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	Terminal  bool           `json:"terminal,omitempty"`
}

func SessionRequestPermission(id string, params PermissionRequestParams) *Request {
	rawID, err := json.Marshal(id)
	if err != nil {
		panic("acp: permission request id marshal failed: " + err.Error())
	}
	rawParams, err := json.Marshal(params)
	if err != nil {
		panic("acp: permission request params marshal failed: " + err.Error())
	}
	idMsg := json.RawMessage(rawID)
	return &Request{
		JSONRPC: JSONRPCVersion,
		ID:      &idMsg,
		Method:  MethodSessionRequestPerm,
		Params:  rawParams,
	}
}

func SessionUpdateNotification(update SessionUpdateParams) *Request {
	raw, err := json.Marshal(update)
	if err != nil {
		panic("acp: session/update marshal failed: " + err.Error())
	}
	return &Request{
		JSONRPC: JSONRPCVersion,
		Method:  MethodSessionUpdate,
		Params:  raw,
	}
}

func PendingPermissionFromSessionUpdate(update SessionUpdateParams) (PermissionRequestParams, bool) {
	if update.EventType != "approval.requested" && update.EventType != "snapshot" {
		return PermissionRequestParams{}, false
	}
	if params, ok := pendingPermissionFromDirectApprovalEvent(update); ok {
		return params, true
	}
	rawApprovals, ok := update.Data["approvals"]
	if !ok {
		return PermissionRequestParams{}, false
	}
	raw, err := json.Marshal(rawApprovals)
	if err != nil {
		return PermissionRequestParams{}, false
	}
	var approvals []struct {
		ID     string         `json:"id"`
		Kind   string         `json:"kind"`
		Reason string         `json:"reason"`
		Status string         `json:"status"`
		Data   map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &approvals); err != nil {
		return PermissionRequestParams{}, false
	}
	for _, approval := range approvals {
		if approval.ID == "" || approval.Status != "pending" {
			continue
		}
		message := approval.Reason
		if message == "" {
			message = "Hecate requests permission to continue this run."
		}
		return PermissionRequestParams{
			SessionID:  update.SessionID,
			TaskID:     update.TaskID,
			RunID:      update.RunID,
			ApprovalID: approval.ID,
			Kind:       approval.Kind,
			Reason:     approval.Reason,
			Message:    message,
			Data:       approval.Data,
		}, true
	}
	return PermissionRequestParams{}, false
}

func pendingPermissionFromDirectApprovalEvent(update SessionUpdateParams) (PermissionRequestParams, bool) {
	if update.EventType != "approval.requested" {
		return PermissionRequestParams{}, false
	}
	approvalID, _ := update.Data["approval_id"].(string)
	if approvalID == "" {
		approvalID, _ = update.Data["id"].(string)
	}
	status, _ := update.Data["status"].(string)
	if approvalID == "" || status != "pending" {
		return PermissionRequestParams{}, false
	}
	kind, _ := update.Data["kind"].(string)
	reason, _ := update.Data["policy_reason"].(string)
	if reason == "" {
		reason, _ = update.Data["reason"].(string)
	}
	message := reason
	if message == "" {
		message = update.Message
	}
	if message == "" {
		message = "Hecate requests permission to continue this run."
	}
	data := make(map[string]any, len(update.Data))
	for key, value := range update.Data {
		data[key] = value
	}
	return PermissionRequestParams{
		SessionID:  update.SessionID,
		TaskID:     update.TaskID,
		RunID:      update.RunID,
		ApprovalID: approvalID,
		Kind:       kind,
		Reason:     reason,
		Message:    message,
		Data:       data,
	}, true
}

func RunEventToSessionUpdate(sessionID, taskID, runID string, event RunEvent) SessionUpdateParams {
	if event.Err != nil {
		return SessionUpdateParams{
			SessionID: sessionID,
			Kind:      "error",
			Status:    "failed",
			Message:   event.Err.Error(),
			TaskID:    taskID,
			RunID:     runID,
			Terminal:  true,
		}
	}
	data := map[string]any{}
	if len(event.Data) > 0 {
		_ = json.Unmarshal(event.Data, &data)
	}
	update := SessionUpdateParams{
		SessionID: sessionID,
		Kind:      kindForRunEvent(event.Type),
		Status:    statusForRunEvent(event.Type, data),
		Message:   messageForRunEvent(event.Type, data),
		TaskID:    taskID,
		RunID:     runID,
		EventType: event.Type,
		Data:      data,
		Terminal:  terminalRunEvent(event.Type),
	}
	return update
}

func kindForRunEvent(eventType string) string {
	switch eventType {
	case "assistant.text_delta", "assistant.text_complete", "assistant.final_answer":
		return "text"
	case "assistant.thinking_delta", "assistant.thinking_complete":
		return "thinking"
	case "assistant.tool_call_proposed", "tool.started", "tool.completed", "tool.failed", "tool.file.patch", "tool.file.applied", "tool.file.reverted":
		return "tool_call"
	case "approval.requested":
		return "permission"
	case "approval.resolved":
		return "permission_result"
	case "run.finished":
		return "stop"
	case "run.failed":
		return "error"
	case "run.cancelled":
		return "cancelled"
	default:
		return "runtime"
	}
}

func statusForRunEvent(eventType string, data map[string]any) string {
	if value, ok := data["status"].(string); ok && value != "" {
		return value
	}
	switch eventType {
	case "run.finished":
		return "completed"
	case "run.failed":
		return "failed"
	case "run.cancelled":
		return "cancelled"
	case "approval.requested":
		return "pending"
	case "approval.resolved":
		if value, ok := data["decision"].(string); ok && value != "" {
			return value
		}
		return "resolved"
	default:
		return ""
	}
}

func messageForRunEvent(eventType string, data map[string]any) string {
	for _, key := range []string{"delta", "summary", "text", "content", "message", "tool_name", "status", "error", "reason"} {
		if value, ok := data[key].(string); ok && value != "" {
			return value
		}
	}
	return eventType
}

func terminalRunEvent(eventType string) bool {
	return eventType == "run.finished" || eventType == "run.failed" || eventType == "run.cancelled"
}
