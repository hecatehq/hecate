package chatstate

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/storage"
	"github.com/hecate/agent-runtime/pkg/types"
)

func newSQLiteTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        filepath.Join(dir, "chatstate.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	store, err := NewSQLiteStore(context.Background(), client)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	return store
}

func TestSQLiteStoreCreateAndGetSession(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	if got := store.Backend(); got != "sqlite" {
		t.Fatalf("Backend() = %q, want sqlite", got)
	}

	created, err := store.CreateSession(ctx, types.ChatSession{
		ID:           "s1",
		Title:        "first session",
		SystemPrompt: "be terse",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if created.ID != "s1" || created.Title != "first session" {
		t.Fatalf("created mismatch: %+v", created)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not populated: %+v", created)
	}

	got, ok, err := store.GetSession(ctx, "s1")
	if err != nil || !ok {
		t.Fatalf("GetSession: ok=%v err=%v", ok, err)
	}
	if got.SystemPrompt != "be terse" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if len(got.Messages) != 0 || len(got.ProviderCalls) != 0 {
		t.Fatalf("new session has messages/calls: %+v", got)
	}

	// Missing id -> ok=false, err=nil.
	_, ok, err = store.GetSession(ctx, "missing")
	if err != nil {
		t.Fatalf("GetSession(missing): err = %v", err)
	}
	if ok {
		t.Fatal("GetSession(missing): ok = true, want false")
	}
}

func TestSQLiteStoreAppendExchange(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	if _, err := store.CreateSession(ctx, types.ChatSession{ID: "s1", Title: "t"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	call := types.ChatProviderCall{
		ID:                "call-1",
		RequestID:         "req-1",
		RequestedProvider: "openai",
		Provider:          "openai",
		ProviderKind:      "chat",
		RequestedModel:    "gpt-4o",
		Model:             "gpt-4o",
		CostMicrosUSD:     1234,
		PromptTokens:      10,
		CompletionTokens:  5,
		TotalTokens:       15,
	}
	messages := []types.ChatSessionMessage{
		{ID: "msg-u1", Message: types.Message{Role: "user", Content: "hi"}},
		{ID: "msg-a1", ProducedByCallID: "call-1", Message: types.Message{Role: "assistant", Content: "hello"}},
	}
	updated, err := store.AppendExchange(ctx, "s1", messages, call)
	if err != nil {
		t.Fatalf("AppendExchange: %v", err)
	}
	if len(updated.Messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(updated.Messages))
	}
	if len(updated.ProviderCalls) != 1 {
		t.Fatalf("provider call count = %d, want 1", len(updated.ProviderCalls))
	}
	if updated.Messages[0].Sequence != 0 || updated.Messages[1].Sequence != 1 {
		t.Fatalf("sequences not assigned monotonically: %+v", updated.Messages)
	}
	if updated.Messages[1].ProducedByCallID != "call-1" {
		t.Fatalf("ProducedByCallID lost: %+v", updated.Messages[1])
	}
	got := updated.ProviderCalls[0]
	if got.ID != "call-1" || got.RequestID != "req-1" {
		t.Fatalf("call round-trip mismatch: %+v", got)
	}
	if got.TotalTokens != 15 || got.CostMicrosUSD != 1234 {
		t.Fatalf("numeric round-trip mismatch: %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("call CreatedAt not populated")
	}
	if updated.Messages[0].Message.Content != "hi" || updated.Messages[1].Message.Content != "hello" {
		t.Fatalf("message body round-trip mismatch: %+v", updated.Messages)
	}

	// Appending updates the parent session's UpdatedAt.
	if !updated.UpdatedAt.Equal(got.CreatedAt) {
		t.Fatalf("session UpdatedAt = %v, want %v", updated.UpdatedAt, got.CreatedAt)
	}
}

func TestSQLiteStoreAppendExchangeAssignsMonotonicSequences(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	if _, err := store.CreateSession(ctx, types.ChatSession{ID: "s1", Title: "t"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	for i, callID := range []string{"call-1", "call-2", "call-3"} {
		_, err := store.AppendExchange(ctx, "s1",
			[]types.ChatSessionMessage{
				{ID: "msg-u-" + callID, Message: types.Message{Role: "user", Content: "u"}},
				{ID: "msg-a-" + callID, ProducedByCallID: callID, Message: types.Message{Role: "assistant", Content: "a"}},
			},
			types.ChatProviderCall{ID: callID, RequestID: callID, Provider: "openai", Model: "gpt-4o"},
		)
		if err != nil {
			t.Fatalf("AppendExchange %d: %v", i, err)
		}
	}

	got, ok, err := store.GetSession(ctx, "s1")
	if err != nil || !ok {
		t.Fatalf("GetSession: ok=%v err=%v", ok, err)
	}
	if len(got.Messages) != 6 {
		t.Fatalf("message count = %d, want 6", len(got.Messages))
	}
	for i, msg := range got.Messages {
		if msg.Sequence != i {
			t.Fatalf("messages[%d].Sequence = %d, want %d", i, msg.Sequence, i)
		}
	}
}

func TestSQLiteStoreAppendExchangePreservesContentBlocksAndToolCalls(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	if _, err := store.CreateSession(ctx, types.ChatSession{ID: "s1", Title: "t"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	assistantMessage := types.Message{
		Role:    "assistant",
		Content: "thinking...",
		ContentBlocks: []types.ContentBlock{
			{Type: "thinking", Thinking: "let me check", Signature: "sig"},
			{Type: "text", Text: "thinking..."},
		},
		ToolCalls: []types.ToolCall{
			{ID: "tc-1", Type: "function", Function: types.ToolCallFunction{Name: "get_weather", Arguments: `{"city":"Paris"}`}},
		},
	}
	toolMessage := types.Message{
		Role:       "tool",
		Content:    "rainy",
		ToolCallID: "tc-1",
		ToolError:  true,
	}
	_, err := store.AppendExchange(ctx, "s1",
		[]types.ChatSessionMessage{
			{ID: "msg-u1", Message: types.Message{Role: "user", Content: "weather?"}},
			{ID: "msg-a1", ProducedByCallID: "call-1", Message: assistantMessage},
			{ID: "msg-t1", Message: toolMessage},
		},
		types.ChatProviderCall{ID: "call-1", RequestID: "req-1", Provider: "anthropic", Model: "claude"},
	)
	if err != nil {
		t.Fatalf("AppendExchange: %v", err)
	}

	got, _, err := store.GetSession(ctx, "s1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("message count = %d, want 3", len(got.Messages))
	}
	a := got.Messages[1].Message
	if len(a.ContentBlocks) != 2 || a.ContentBlocks[0].Type != "thinking" || a.ContentBlocks[0].Thinking != "let me check" {
		t.Fatalf("ContentBlocks lost on round-trip: %+v", a.ContentBlocks)
	}
	if len(a.ToolCalls) != 1 || a.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("ToolCalls lost on round-trip: %+v", a.ToolCalls)
	}
	tm := got.Messages[2].Message
	if tm.ToolCallID != "tc-1" || !tm.ToolError {
		t.Fatalf("tool message metadata lost: %+v", tm)
	}
}

func TestSQLiteStoreListSessionsPagination(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Second)
	// Create three sessions with strictly increasing CreatedAt/UpdatedAt
	// so ORDER BY updated_at DESC, created_at DESC is deterministic.
	for i, id := range []string{"s1", "s2", "s3"} {
		ts := base.Add(time.Duration(i) * time.Minute)
		if _, err := store.CreateSession(ctx, types.ChatSession{
			ID:        id,
			Title:     id,
			CreatedAt: ts,
		}); err != nil {
			t.Fatalf("CreateSession(%s): %v", id, err)
		}
	}

	all, err := store.ListSessions(ctx, Filter{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListSessions: got %d, want 3", len(all))
	}
	// Newest first.
	if all[0].ID != "s3" || all[1].ID != "s2" || all[2].ID != "s1" {
		t.Fatalf("ordering: got %s,%s,%s want s3,s2,s1", all[0].ID, all[1].ID, all[2].ID)
	}

	page1, err := store.ListSessions(ctx, Filter{Limit: 2})
	if err != nil {
		t.Fatalf("ListSessions(limit=2): %v", err)
	}
	if len(page1) != 2 || page1[0].ID != "s3" || page1[1].ID != "s2" {
		t.Fatalf("limit=2 page: %+v", page1)
	}

	page2, err := store.ListSessions(ctx, Filter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("ListSessions(limit=2,offset=2): %v", err)
	}
	if len(page2) != 1 || page2[0].ID != "s1" {
		t.Fatalf("offset page: %+v", page2)
	}

	// Offset without explicit limit still works (LIMIT -1 fallback).
	page3, err := store.ListSessions(ctx, Filter{Offset: 1})
	if err != nil {
		t.Fatalf("ListSessions(offset=1,no limit): %v", err)
	}
	if len(page3) != 2 || page3[0].ID != "s2" || page3[1].ID != "s1" {
		t.Fatalf("offset-only page: %+v", page3)
	}
}

func TestSQLiteStoreListSessionsAttachesLatestCall(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	if _, err := store.CreateSession(ctx, types.ChatSession{ID: "s1", Title: "t"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for i, callID := range []string{"call-1", "call-2"} {
		_, err := store.AppendExchange(ctx, "s1",
			[]types.ChatSessionMessage{
				{ID: "msg-u-" + callID, Message: types.Message{Role: "user", Content: "u"}},
				{ID: "msg-a-" + callID, ProducedByCallID: callID, Message: types.Message{Role: "assistant", Content: "a"}},
			},
			types.ChatProviderCall{
				ID:        callID,
				RequestID: callID,
				Provider:  "openai",
				Model:     "gpt-4o",
				// Stagger so created_at ordering is deterministic.
				CreatedAt: time.Now().UTC().Add(time.Duration(i) * time.Second),
			},
		)
		if err != nil {
			t.Fatalf("AppendExchange %d: %v", i, err)
		}
	}

	listed, err := store.ListSessions(ctx, Filter{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("session count = %d, want 1", len(listed))
	}
	if len(listed[0].ProviderCalls) != 1 {
		t.Fatalf("expected exactly one latest provider call attached, got %d", len(listed[0].ProviderCalls))
	}
	if listed[0].ProviderCalls[0].ID != "call-2" {
		t.Fatalf("latest call = %q, want call-2", listed[0].ProviderCalls[0].ID)
	}
	// Messages are NOT carried in the list view.
	if len(listed[0].Messages) != 0 {
		t.Fatalf("list view should not carry messages, got %d", len(listed[0].Messages))
	}
}

func TestSQLiteStoreDeleteSessionCascadesChildren(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	if _, err := store.CreateSession(ctx, types.ChatSession{ID: "s1", Title: "t"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for _, callID := range []string{"call-1", "call-2"} {
		if _, err := store.AppendExchange(ctx, "s1",
			[]types.ChatSessionMessage{
				{ID: "msg-u-" + callID, Message: types.Message{Role: "user", Content: "u"}},
				{ID: "msg-a-" + callID, ProducedByCallID: callID, Message: types.Message{Role: "assistant", Content: "a"}},
			},
			types.ChatProviderCall{ID: callID, RequestID: callID, Provider: "openai", Model: "gpt-4o"},
		); err != nil {
			t.Fatalf("AppendExchange(%s): %v", callID, err)
		}
	}

	if err := store.DeleteSession(ctx, "s1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// Session is gone.
	_, ok, err := store.GetSession(ctx, "s1")
	if err != nil {
		t.Fatalf("GetSession after delete: %v", err)
	}
	if ok {
		t.Fatal("session still present after delete")
	}

	// Children are gone.
	for _, table := range []string{store.messagesTable, store.providerCallsTable} {
		var count int
		if err := store.client.DB().QueryRowContext(
			ctx,
			"SELECT COUNT(*) FROM "+table+" WHERE session_id = ?",
			"s1",
		).Scan(&count); err != nil {
			t.Fatalf("count rows in %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("rows remaining in %s after cascade delete: %d", table, count)
		}
	}
}

func TestSQLiteStoreUpdateSessionTitle(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	if _, err := store.CreateSession(ctx, types.ChatSession{
		ID:           "s1",
		Title:        "original",
		SystemPrompt: "keep me",
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	updated, err := store.UpdateSession(ctx, "s1", "renamed")
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if updated.Title != "renamed" {
		t.Fatalf("Title = %q, want renamed", updated.Title)
	}
	if updated.SystemPrompt != "keep me" {
		t.Fatalf("rename clobbered SystemPrompt: %q", updated.SystemPrompt)
	}

	got, ok, err := store.GetSession(ctx, "s1")
	if err != nil || !ok {
		t.Fatalf("GetSession: ok=%v err=%v", ok, err)
	}
	if got.Title != "renamed" {
		t.Fatalf("persisted Title = %q, want renamed", got.Title)
	}
}

func TestSQLiteStoreUpdateSessionSystemPrompt(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	if _, err := store.CreateSession(ctx, types.ChatSession{
		ID:    "s1",
		Title: "first",
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	updated, err := store.UpdateSessionSystemPrompt(ctx, "s1", "be terse and helpful")
	if err != nil {
		t.Fatalf("UpdateSessionSystemPrompt: %v", err)
	}
	if updated.SystemPrompt != "be terse and helpful" {
		t.Fatalf("SystemPrompt = %q", updated.SystemPrompt)
	}
	if updated.Title != "first" {
		t.Fatalf("Title clobbered by SystemPrompt update: %q", updated.Title)
	}
}

func TestNewSQLiteStoreRejectsNilClient(t *testing.T) {
	t.Parallel()
	if _, err := NewSQLiteStore(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil client")
	}
}
