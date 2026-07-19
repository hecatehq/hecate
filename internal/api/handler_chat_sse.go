package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/telemetry"
)

// startAgentChatTrace creates a tracer-backed root span for the
// current request, surfaces X-Trace-Id / X-Span-Id headers, and
// returns the trace plus a request context that carries the trace
// IDs for downstream telemetry calls.
func (h *Handler) startAgentChatTrace(w http.ResponseWriter, r *http.Request) (*profiler.Trace, context.Context) {
	requestID := RequestIDFromContext(r.Context())
	if requestID == "" {
		requestID = newRequestID()
	}
	trace := h.tracer.Start(requestID)
	ctx := telemetry.WithTraceIDs(r.Context(), trace.TraceID, trace.RootSpanID())
	w.Header().Set("X-Trace-Id", trace.TraceID)
	w.Header().Set("X-Span-Id", trace.RootSpanID())
	return trace, ctx
}

// agentChatTraceAttrs builds the OTel attribute bag attached to every
// agent-chat trace event. Callers pass per-event attrs in the final
// map and they overwrite any defaults.
func agentChatTraceAttrs(session chat.Session, adapter agentadapters.Adapter, turnID, messageID string, attrs map[string]any) map[string]any {
	out := map[string]any{
		telemetry.AttrHecateChatSessionID:       session.ID,
		telemetry.AttrHecateChatMessageID:       messageID,
		telemetry.AttrHecateChatTurnID:          turnID,
		telemetry.AttrHecateExecutionKind:       "chat",
		telemetry.AttrHecateAgentAdapterID:      adapter.ID,
		telemetry.AttrHecateAgentAdapterName:    adapter.Name,
		telemetry.AttrHecateAgentAdapterCommand: adapter.Command,
		telemetry.AttrHecateAgentDriverKind:     adapter.Kind,
		telemetry.AttrHecateWorkspacePath:       session.Workspace,
		telemetry.AttrHecateResult:              telemetry.ResultSuccess,
	}
	if session.NativeSessionID != "" {
		out[telemetry.AttrHecateAgentNativeSessionID] = session.NativeSessionID
	}
	for key, value := range attrs {
		out[key] = value
	}
	return out
}

// hecateAgentChatTraceAttrs mirrors agentChatTraceAttrs for Hecate-owned
// turns. A direct-model turn adds its gateway routing spans to this trace; a
// task-backed turn links the wrapper to the detailed Task Run trace through
// the optional task and run IDs.
func hecateAgentChatTraceAttrs(session chat.Session, turnID, taskID, runID, messageID string, attrs map[string]any) map[string]any {
	out := map[string]any{
		telemetry.AttrHecateChatSessionID:    session.ID,
		telemetry.AttrHecateChatMessageID:    messageID,
		telemetry.AttrHecateChatTurnID:       turnID,
		telemetry.AttrHecateExecutionKind:    "chat",
		telemetry.AttrHecateAgentAdapterID:   "hecate",
		telemetry.AttrHecateAgentAdapterName: "Hecate Chat",
		telemetry.AttrHecateAgentDriverKind:  "hecate",
		telemetry.AttrHecateWorkspacePath:    session.Workspace,
		telemetry.AttrGenAIProviderName:      session.Provider,
		telemetry.AttrGenAIRequestModel:      session.Model,
		telemetry.AttrHecateResult:           telemetry.ResultSuccess,
	}
	if taskID != "" {
		out[telemetry.AttrHecateTaskID] = taskID
	}
	if runID != "" {
		out[telemetry.AttrHecateRunID] = runID
	}
	for key, value := range attrs {
		out[key] = value
	}
	return out
}

// sendAgentChatSSE writes one SSE event to the response. The payload
// is marshalled to JSON; on encode failure we emit an `event: error`
// frame so subscribers see something rather than silently stalling.
// Accepts any payload so the same writer can carry session updates
// (ChatSessionResponse) and approval events
// (ChatApprovalRequestedEvent / ChatApprovalResolvedEvent).
func sendAgentChatSSE(w http.ResponseWriter, flusher http.Flusher, event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: {\"error\":{\"message\":%q}}\n\n", err.Error())
		flusher.Flush()
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	flusher.Flush()
}
