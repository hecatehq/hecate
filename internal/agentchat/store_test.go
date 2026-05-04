package agentchat

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStoreLifecycle(t *testing.T) {
	t.Parallel()
	runStoreLifecycle(t, NewMemoryStore())
}

func runStoreLifecycle(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	if store.Backend() == "" {
		t.Fatal("Backend() is empty")
	}

	created, err := store.Create(ctx, Session{
		ID:              "agent_chat_1",
		Title:           "Review diff",
		AdapterID:       "codex",
		Workspace:       "/tmp/hecate",
		WorkspaceBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Status != "idle" {
		t.Fatalf("created status = %q, want idle", created.Status)
	}
	if created.WorkspaceBranch != "main" {
		t.Fatalf("created workspace branch = %q, want main", created.WorkspaceBranch)
	}

	if _, err := store.AppendMessage(ctx, created.ID, Message{
		ID:      "msg_user",
		Role:    "user",
		Content: "review this",
	}); err != nil {
		t.Fatalf("AppendMessage(user): %v", err)
	}
	startedAt := time.Now().UTC().Add(-2 * time.Second)
	if _, err := store.AppendMessage(ctx, created.ID, Message{
		ID:          "msg_assistant",
		RunID:       "agent_run_1",
		Role:        "assistant",
		Content:     "running",
		AdapterID:   "codex",
		AdapterName: "Codex",
		Status:      "running",
		CostMode:    "external",
		Workspace:   "/tmp/hecate",
	}); err != nil {
		t.Fatalf("AppendMessage(assistant): %v", err)
	}
	updated, err := store.UpdateMessage(ctx, created.ID, "msg_assistant", func(message *Message) {
		message.Content = "done"
		message.RawOutput = `{"type":"message","content":"done"}`
		message.RequestID = "req_agent"
		message.TraceID = "trace_agent"
		message.SpanID = "span_agent"
		message.Status = "completed"
		message.ExitCode = 0
		message.DiffStat = "1 file changed"
		message.Diff = "diff --git a/a b/a"
		message.StartedAt = startedAt
		message.CompletedAt = startedAt.Add(1500 * time.Millisecond)
		message.Usage = Usage{
			ContextSize:          200_000,
			ContextUsed:          42_000,
			ReportedCostAmount:   "0.1234",
			ReportedCostCurrency: "USD",
		}
		message.Activities = []Activity{
			{Type: "started", Status: "completed", Title: "Started external agent", CreatedAt: startedAt},
			{Type: "files_changed", Status: "completed", Title: "Files changed", Detail: "1 file changed", CreatedAt: startedAt.Add(time.Second)},
		}
	})
	if err != nil {
		t.Fatalf("UpdateMessage: %v", err)
	}
	if updated.Status != "completed" {
		t.Fatalf("updated session status = %q, want completed", updated.Status)
	}
	if len(updated.Messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(updated.Messages))
	}
	if got := updated.Messages[1]; got.Content != "done" || got.RawOutput == "" || got.TraceID != "trace_agent" || got.DiffStat != "1 file changed" || got.RunID != "agent_run_1" || got.CompletedAt.IsZero() || len(got.Activities) != 2 {
		t.Fatalf("assistant message not updated: %+v", got)
	}

	got, ok, err := store.Get(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Messages[1].Content != "done" {
		t.Fatalf("persisted assistant content = %q, want done", got.Messages[1].Content)
	}
	if got.WorkspaceBranch != "main" {
		t.Fatalf("persisted workspace branch = %q, want main", got.WorkspaceBranch)
	}
	if got.Messages[1].RawOutput == "" || got.Messages[1].TraceID != "trace_agent" || len(got.Messages[1].Activities) != 2 {
		t.Fatalf("persisted diagnostics missing: %+v", got.Messages[1])
	}
	if got.Messages[1].Usage.ContextSize != 200_000 || got.Messages[1].Usage.ContextUsed != 42_000 {
		t.Fatalf("persisted usage = %+v, want 42000/200000", got.Messages[1].Usage)
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("List = %+v, want created session", list)
	}
	if list[0].WorkspaceBranch != "main" || len(list[0].Messages) != 2 {
		t.Fatalf("List summary = %+v, want cached branch and message count", list[0])
	}

	if err := store.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, ok, err = store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if ok {
		t.Fatal("Get after delete: ok = true, want false")
	}
}
