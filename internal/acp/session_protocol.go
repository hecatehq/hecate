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
	case "assistant.message.delta", "assistant.message.completed", "assistant.final":
		return "text"
	case "assistant.thinking.delta", "assistant.thinking.completed":
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
	for _, key := range []string{"text", "content", "message", "error", "reason"} {
		if value, ok := data[key].(string); ok && value != "" {
			return value
		}
	}
	return eventType
}

func terminalRunEvent(eventType string) bool {
	return eventType == "run.finished" || eventType == "run.failed" || eventType == "run.cancelled"
}
