package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/hecate/agent-runtime/internal/agentadapters"
	"github.com/hecate/agent-runtime/internal/chat"
	"github.com/hecate/agent-runtime/internal/profiler"
	"github.com/hecate/agent-runtime/internal/telemetry"
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
func agentChatTraceAttrs(session chat.Session, adapter agentadapters.Adapter, runID, messageID string, attrs map[string]any) map[string]any {
	out := map[string]any{
		telemetry.AttrHecateChatSessionID:       session.ID,
		telemetry.AttrHecateChatMessageID:       messageID,
		telemetry.AttrHecateRunID:               runID,
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
// agent_loop chats. The backing task/run trace carries the detailed queue,
// model, tool, approval, and artifact timings; these attrs make the chat
// wrapper itself visible in the same agent-chat dashboards.
func hecateAgentChatTraceAttrs(session chat.Session, taskID, runID, messageID string, attrs map[string]any) map[string]any {
	out := map[string]any{
		telemetry.AttrHecateChatSessionID:    session.ID,
		telemetry.AttrHecateChatMessageID:    messageID,
		telemetry.AttrHecateTaskID:           taskID,
		telemetry.AttrHecateRunID:            runID,
		telemetry.AttrHecateExecutionKind:    "chat",
		telemetry.AttrHecateAgentAdapterID:   "hecate",
		telemetry.AttrHecateAgentAdapterName: "Hecate Agent",
		telemetry.AttrHecateAgentDriverKind:  "hecate",
		telemetry.AttrHecateWorkspacePath:    session.Workspace,
		telemetry.AttrGenAIProviderName:      session.Provider,
		telemetry.AttrGenAIRequestModel:      session.Model,
		telemetry.AttrHecateResult:           telemetry.ResultSuccess,
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
