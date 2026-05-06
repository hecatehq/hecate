package agentchat

import (
	"context"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/pkg/types"
)

func TestMemoryStoreLifecycle(t *testing.T) {
	t.Parallel()
	runStoreLifecycle(t, NewMemoryStore())
}

func TestMemoryStoreReconcileInterruptedRuns(t *testing.T) {
	t.Parallel()
	runStoreReconcileInterruptedRuns(t, NewMemoryStore())
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
		RuntimeKind:     "agent",
		TaskID:          "task_chat_1",
		LatestRunID:     "run_chat_1",
		Provider:        "openai",
		Model:           "gpt-4o-mini",
		Capabilities:    types.ModelCapabilities{ToolCalling: "basic", Streaming: true, MaxContextTokens: 128000, Source: "operator_override"},
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
	if created.RuntimeKind != "agent" || created.TaskID != "task_chat_1" || created.LatestRunID != "run_chat_1" {
		t.Fatalf("created linkage = runtime %q task %q run %q", created.RuntimeKind, created.TaskID, created.LatestRunID)
	}
	if created.Provider != "openai" || created.Model != "gpt-4o-mini" || created.Capabilities.ToolCalling != "basic" {
		t.Fatalf("created model snapshot = provider %q model %q caps %+v", created.Provider, created.Model, created.Capabilities)
	}

	if _, err := store.AppendMessage(ctx, created.ID, Message{
		ID:          "msg_user",
		SegmentID:   "task:task_chat_1",
		RuntimeKind: "agent",
		TaskID:      "task_chat_1",
		Provider:    "openai",
		Model:       "gpt-4o-mini",
		Role:        "user",
		Content:     "review this",
	}); err != nil {
		t.Fatalf("AppendMessage(user): %v", err)
	}
	startedAt := time.Now().UTC().Add(-2 * time.Second)
	if _, err := store.AppendMessage(ctx, created.ID, Message{
		ID:           "msg_assistant",
		RuntimeKind:  "agent",
		SegmentID:    "task:task_chat_1",
		TaskID:       "task_chat_1",
		RunID:        "agent_run_1",
		Provider:     "openai",
		Model:        "gpt-4o-mini",
		Capabilities: types.ModelCapabilities{ToolCalling: "basic", Streaming: true, Source: "operator_override"},
		Role:         "assistant",
		Content:      "running",
		AdapterID:    "codex",
		AdapterName:  "Codex",
		Status:       "running",
		CostMode:     "external",
		Workspace:    "/tmp/hecate",
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
		message.Timing = Timing{
			TotalMS:      1500,
			QueueMS:      20,
			ModelMS:      900,
			ToolMS:       200,
			OverheadMS:   380,
			TurnCount:    1,
			ToolCount:    1,
			Bottleneck:   "model",
			BottleneckMS: 900,
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
	if got.RuntimeKind != "agent" || got.TaskID != "task_chat_1" || got.LatestRunID != "run_chat_1" {
		t.Fatalf("persisted linkage = runtime %q task %q run %q", got.RuntimeKind, got.TaskID, got.LatestRunID)
	}
	if got.Provider != "openai" || got.Model != "gpt-4o-mini" || got.Capabilities.ToolCalling != "basic" || got.Capabilities.Source != "operator_override" {
		t.Fatalf("persisted model snapshot = provider %q model %q caps %+v", got.Provider, got.Model, got.Capabilities)
	}
	if got.Messages[1].RawOutput == "" || got.Messages[1].TraceID != "trace_agent" || len(got.Messages[1].Activities) != 2 {
		t.Fatalf("persisted diagnostics missing: %+v", got.Messages[1])
	}
	if got.Messages[1].Usage.ContextSize != 200_000 || got.Messages[1].Usage.ContextUsed != 42_000 {
		t.Fatalf("persisted usage = %+v, want 42000/200000", got.Messages[1].Usage)
	}
	if got.Messages[1].Timing.Bottleneck != "model" || got.Messages[1].Timing.ModelMS != 900 || got.Messages[1].Timing.TurnCount != 1 {
		t.Fatalf("persisted timing = %+v, want model bottleneck", got.Messages[1].Timing)
	}
	if got.Messages[1].RuntimeKind != "agent" || got.Messages[1].SegmentID != "task:task_chat_1" || got.Messages[1].TaskID != "task_chat_1" {
		t.Fatalf("persisted message runtime = runtime %q segment %q task %q", got.Messages[1].RuntimeKind, got.Messages[1].SegmentID, got.Messages[1].TaskID)
	}
	if got.Messages[1].Provider != "openai" || got.Messages[1].Model != "gpt-4o-mini" || got.Messages[1].Capabilities.ToolCalling != "basic" {
		t.Fatalf("persisted message model snapshot = provider %q model %q caps %+v", got.Messages[1].Provider, got.Messages[1].Model, got.Messages[1].Capabilities)
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("List = %+v, want created session", list)
	}
	if list[0].WorkspaceBranch != "main" || len(list[0].Messages) != 2 || list[0].RuntimeKind != "agent" || list[0].TaskID != "task_chat_1" {
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

func runStoreReconcileInterruptedRuns(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	created, err := store.Create(ctx, Session{
		ID:        "agent_chat_interrupted",
		Title:     "Interrupted",
		AdapterID: "codex",
		Workspace: "/tmp/hecate",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.AppendMessage(ctx, created.ID, Message{
		ID:        "msg_user",
		Role:      "user",
		Content:   "keep going",
		CreatedAt: time.Now().UTC().Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("AppendMessage(user): %v", err)
	}
	if _, err := store.AppendMessage(ctx, created.ID, Message{
		ID:          "msg_assistant",
		RunID:       "agent_run_interrupted",
		Role:        "assistant",
		Content:     "partial answer",
		AdapterID:   "codex",
		AdapterName: "Codex",
		Status:      "running",
		CostMode:    "external",
		Workspace:   "/tmp/hecate",
		StartedAt:   time.Now().UTC().Add(-time.Minute),
		Activities: []Activity{
			{Type: "running", Status: "running", Title: "Running"},
		},
	}); err != nil {
		t.Fatalf("AppendMessage(assistant): %v", err)
	}

	now := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	count, err := ReconcileInterruptedRuns(ctx, store, now)
	if err != nil {
		t.Fatalf("ReconcileInterruptedRuns: %v", err)
	}
	if count != 1 {
		t.Fatalf("reconciled count = %d, want 1", count)
	}

	got, ok, err := store.Get(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Status != "cancelled" {
		t.Fatalf("session status = %q, want cancelled", got.Status)
	}
	assistant := got.Messages[1]
	if assistant.Status != "cancelled" || assistant.Error != "interrupted by Hecate restart" || !assistant.CompletedAt.Equal(now) {
		t.Fatalf("assistant after reconcile = %+v", assistant)
	}
	if assistant.Content != "partial answer" {
		t.Fatalf("assistant content = %q, want preserved partial answer", assistant.Content)
	}
	if !activityTypeExists(assistant.Activities, "interrupted") {
		t.Fatalf("activities = %+v, want interrupted activity", assistant.Activities)
	}

	count, err = ReconcileInterruptedRuns(ctx, store, now.Add(time.Second))
	if err != nil {
		t.Fatalf("ReconcileInterruptedRuns second call: %v", err)
	}
	if count != 0 {
		t.Fatalf("second reconciled count = %d, want 0", count)
	}
}
