package api

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/hecate/agent-runtime/pkg/types"
)

func TestHandleChatSessionsListReturnsCreatedSessionsWithSummary(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{name: "openai", response: &types.ChatResponse{}}
	handler := newTestHTTPHandler(logger, provider)
	client := newAPITestClient(t, handler)

	// Seed two sessions so the list reports both, in newest-first order.
	first := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", `{"title":"Session one"}`)
	second := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", `{"title":"Session two"}`)

	listed := mustRequestJSON[ChatSessionsResponse](client, http.MethodGet, "/hecate/v1/chat/sessions", "")
	if listed.Object != "chat_sessions" {
		t.Errorf("object = %q, want chat_sessions", listed.Object)
	}
	if len(listed.Data) != 2 {
		t.Fatalf("data length = %d, want 2; body=%+v", len(listed.Data), listed.Data)
	}
	// Order is store-defined; the one assertion that always holds is
	// that both seeded IDs come back. Sort-order assertions belong in
	// the chatstate package, not the handler test.
	ids := map[string]bool{listed.Data[0].ID: true, listed.Data[1].ID: true}
	if !ids[first.Data.ID] || !ids[second.Data.ID] {
		t.Errorf("got ids %+v; want both %q and %q", ids, first.Data.ID, second.Data.ID)
	}
	// hasMore is false because limit (default 50) >> 2.
	if listed.HasMore {
		t.Errorf("has_more = true, want false (only 2 sessions, default limit 50)")
	}
}

func TestHandleChatSessionsHonoursLimitAndHasMore(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestHTTPHandler(logger, &fakeProvider{name: "openai"})
	client := newAPITestClient(t, handler)

	// Seed three sessions and ask for two — has_more must be true.
	for i := 0; i < 3; i++ {
		client.mustRequest(http.MethodPost, "/hecate/v1/chat/sessions", `{"title":"x"}`)
	}
	listed := mustRequestJSON[ChatSessionsResponse](client, http.MethodGet, "/hecate/v1/chat/sessions?limit=2", "")
	if len(listed.Data) != 2 {
		t.Fatalf("data length = %d, want 2", len(listed.Data))
	}
	if !listed.HasMore {
		t.Errorf("has_more = false, want true (3 sessions, limit 2)")
	}
}

func TestHandleChatSessionsRejectsBadLimit(t *testing.T) {
	t.Parallel()
	handler := newTestHTTPHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), &fakeProvider{name: "openai"})
	client := newAPITestClient(t, handler)

	// Negative and non-numeric limits both 400 — same branch handles both.
	for _, q := range []string{"limit=-1", "limit=abc"} {
		rec := client.mustRequestStatus(http.StatusBadRequest, http.MethodGet, "/hecate/v1/chat/sessions?"+q, "")
		if msg := decodeErrorMessage(t, rec.Body.Bytes()); !strings.Contains(msg, "limit query parameter") {
			t.Errorf("limit=%q error = %q, want limit-related 400", q, msg)
		}
	}
}

func TestHandleChatSessionsRejectsBadOffset(t *testing.T) {
	t.Parallel()
	handler := newTestHTTPHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), &fakeProvider{name: "openai"})
	client := newAPITestClient(t, handler)

	rec := client.mustRequestStatus(http.StatusBadRequest, http.MethodGet, "/hecate/v1/chat/sessions?offset=-5", "")
	if msg := decodeErrorMessage(t, rec.Body.Bytes()); !strings.Contains(msg, "offset query parameter") {
		t.Errorf("error = %q, want offset-related 400", msg)
	}
}

// regression in the soft-vs-hard delete branch.
func TestHandleDeleteChatSessionRemovesFromStore(t *testing.T) {
	t.Parallel()
	handler := newTestHTTPHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), &fakeProvider{name: "openai"})
	client := newAPITestClient(t, handler)

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", `{"title":"To delete"}`)
	id := created.Data.ID

	// GET pre-delete: 200.
	client.mustRequest(http.MethodGet, "/hecate/v1/chat/sessions/"+id, "")

	// DELETE: must be 204 No Content (no body).
	rec := client.mustRequestStatus(http.StatusNoContent, http.MethodDelete, "/hecate/v1/chat/sessions/"+id, "")
	if body := rec.Body.String(); body != "" {
		t.Errorf("DELETE body = %q, want empty", body)
	}

	// GET post-delete: 404.
	client.mustRequestStatus(http.StatusNotFound, http.MethodGet, "/hecate/v1/chat/sessions/"+id, "")
}

func TestHandleDeleteChatSessionMissingIdReturns404(t *testing.T) {
	t.Parallel()
	handler := newTestHTTPHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), &fakeProvider{name: "openai"})
	client := newAPITestClient(t, handler)

	// Unknown id resolves through GetChatSession → not found.
	client.mustRequestStatus(http.StatusNotFound, http.MethodDelete, "/hecate/v1/chat/sessions/sess_does_not_exist", "")
}

// TestHandleTracesListsRecordedTraces creates a chat request (which
// records a trace) and verifies HandleTraces lists it with the
// route-report metadata. The /hecate/v1/traces endpoint is what the
// observability dashboard polls; a regression that breaks the list
// hides historical traces from operators investigating an incident.
func TestHandleTracesListsRecordedTraces(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:    "chatcmpl-trace",
			Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "ok"},
				FinishReason: "stop",
			}},
		},
	}
	handler := newTestHTTPHandler(logger, provider)
	client := newAPITestClient(t, handler)

	// Drive at least one chat request so the in-memory tracer captures a
	// `gateway.request` span. Without this seed the trace list would be
	// empty and the test would only check the wire-shape, not the
	// route-report rendering.
	client.mustRequest(http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`)

	listed := mustRequestJSON[TraceListResponse](client, http.MethodGet, "/hecate/v1/traces", "")
	if listed.Object != "list" {
		t.Errorf("object = %q, want list", listed.Object)
	}
	if len(listed.Data) == 0 {
		t.Fatalf("data is empty after a chat request; want at least one trace")
	}
	first := listed.Data[0]
	if first.RequestID == "" {
		t.Errorf("request_id empty; want a populated id")
	}
	// FinalProvider on the route summary mirrors the resolved provider.
	if first.Route.FinalProvider != "openai" {
		t.Errorf("route.final_provider = %q, want openai", first.Route.FinalProvider)
	}
}

func TestHandleTracesHonoursLimitClamp(t *testing.T) {
	t.Parallel()
	handler := newTestHTTPHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), &fakeProvider{name: "openai", response: &types.ChatResponse{}})
	client := newAPITestClient(t, handler)

	// limit=999 is silently ignored (out of allowed range 1..200) and
	// falls back to the default 50. Verify the request still 200s — the
	// alternative would be that callers passing a too-large limit see
	// 400 noise in their logs for what's a benign over-ask.
	rec := client.mustRequest(http.MethodGet, "/hecate/v1/traces?limit=999", "")
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}
