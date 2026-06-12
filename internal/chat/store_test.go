package chat

import (
	"context"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentcontrols"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestMemoryStoreConformance(t *testing.T) {
	RunConformanceTests(t, "MemoryStore", func(*testing.T) Store { return NewMemoryStore() })
}

func TestContextPacketEmptyConsidersItems(t *testing.T) {
	packet := ContextPacket{
		Items: []ContextItem{{
			Kind:       "transcript",
			TrustLevel: "runtime_state",
			Origin:     "chat.transcript",
			Title:      "Chat transcript",
			Included:   true,
		}},
	}

	if packet.Empty() {
		t.Fatal("ContextPacket.Empty() = true for itemized packet, want false")
	}
}

func runStoreLifecycle(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()

	if store.Backend() == "" {
		t.Fatal("Backend() is empty")
	}

	created, err := store.Create(ctx, Session{
		ID:              "chat_1",
		Title:           "Review diff",
		ProjectID:       "proj_hecate",
		AgentID:         DefaultAgentID,
		TaskID:          "task_chat_1",
		LatestRunID:     "run_chat_1",
		Provider:        "openai",
		Model:           "gpt-4o-mini",
		Capabilities:    types.ModelCapabilities{ToolCalling: "basic", Streaming: true, MaxContextTokens: 128000, Source: "provider"},
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
	if created.ProjectID != "proj_hecate" {
		t.Fatalf("created project_id = %q, want proj_hecate", created.ProjectID)
	}
	if created.AgentID != DefaultAgentID || created.TaskID != "task_chat_1" || created.LatestRunID != "run_chat_1" {
		t.Fatalf("created linkage = agent %q task %q run %q", created.AgentID, created.TaskID, created.LatestRunID)
	}
	if created.Provider != "openai" || created.Model != "gpt-4o-mini" || created.Capabilities.ToolCalling != "basic" {
		t.Fatalf("created model snapshot = provider %q model %q caps %+v", created.Provider, created.Model, created.Capabilities)
	}

	if _, err := store.AppendMessage(ctx, created.ID, Message{
		ID:            "msg_user",
		ExecutionMode: ExecutionModeHecateTask,
		SegmentID:     "task:task_chat_1",
		TaskID:        "task_chat_1",
		Provider:      "openai",
		Model:         "gpt-4o-mini",
		Role:          "user",
		Content:       "review this",
	}); err != nil {
		t.Fatalf("AppendMessage(user): %v", err)
	}
	startedAt := time.Now().UTC().Add(-2 * time.Second)
	if _, err := store.AppendMessage(ctx, created.ID, Message{
		ID:            "msg_assistant",
		ExecutionMode: ExecutionModeHecateTask,
		SegmentID:     "task:task_chat_1",
		TaskID:        "task_chat_1",
		RunID:         "agent_run_1",
		Provider:      "openai",
		Model:         "gpt-4o-mini",
		Capabilities:  types.ModelCapabilities{ToolCalling: "basic", Streaming: true, Source: "provider"},
		Role:          "assistant",
		Content:       "running",
		AgentID:       DefaultAgentID,
		AgentName:     "Hecate",
		Status:        "running",
		CostMode:      "hecate",
		Workspace:     "/tmp/hecate",
		Context: ContextPacket{
			Version:              "chat.context.v1",
			ExecutionMode:        ExecutionModeHecateTask,
			Provider:             "openai",
			Model:                "gpt-4o-mini",
			Workspace:            "/tmp/hecate",
			SystemPromptIncluded: true,
			MessageCount:         2,
			Sources: []ContextSource{
				{
					Kind:   "workspace",
					Label:  "Workspace",
					Detail: "/tmp/hecate",
					Trust:  "workspace",
				},
				{
					Kind:   "task_runtime",
					Label:  "Hecate task runtime",
					Detail: "Continuing the existing task-backed agent loop",
					Trust:  "runtime",
				},
			},
			Items: []ContextItem{
				{
					Kind:            "workspace",
					TrustLevel:      "workspace_guidance",
					Origin:          "/tmp/hecate",
					Title:           "Workspace",
					BodyRef:         "/tmp/hecate",
					Included:        true,
					InclusionReason: "Workspace path selected for this task-backed turn",
				},
				{
					Kind:            "task_runtime",
					TrustLevel:      "runtime_state",
					Origin:          "hecate.task_runtime",
					Title:           "Hecate task runtime",
					Body:            "Continuing the existing task-backed agent loop",
					Included:        true,
					InclusionReason: "Task-backed Hecate Chat turn",
				},
			},
		},
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
	if got.ProjectID != "proj_hecate" {
		t.Fatalf("persisted project_id = %q, want proj_hecate", got.ProjectID)
	}
	if got.AgentID != DefaultAgentID || got.TaskID != "task_chat_1" || got.LatestRunID != "run_chat_1" {
		t.Fatalf("persisted linkage = agent %q task %q run %q", got.AgentID, got.TaskID, got.LatestRunID)
	}
	if got.Provider != "openai" || got.Model != "gpt-4o-mini" || got.Capabilities.ToolCalling != "basic" || got.Capabilities.Source != "provider" {
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
	if got.Messages[1].ExecutionMode != ExecutionModeHecateTask || got.Messages[1].SegmentID != "task:task_chat_1" || got.Messages[1].TaskID != "task_chat_1" {
		t.Fatalf("persisted message execution = mode %q segment %q task %q", got.Messages[1].ExecutionMode, got.Messages[1].SegmentID, got.Messages[1].TaskID)
	}
	if got.Messages[1].Provider != "openai" || got.Messages[1].Model != "gpt-4o-mini" || got.Messages[1].Capabilities.ToolCalling != "basic" {
		t.Fatalf("persisted message model snapshot = provider %q model %q caps %+v", got.Messages[1].Provider, got.Messages[1].Model, got.Messages[1].Capabilities)
	}
	if got.Messages[1].Context.Version != "chat.context.v1" || got.Messages[1].Context.MessageCount != 2 || len(got.Messages[1].Context.Sources) != 2 || len(got.Messages[1].Context.Items) != 2 {
		t.Fatalf("persisted context packet = %+v, want version/count/sources/items", got.Messages[1].Context)
	}
	got.Messages[1].Context.Sources[0].Detail = "mutated"
	got.Messages[1].Context.Items[0].Origin = "mutated"
	got, ok, err = store.Get(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("Get after context mutation: ok=%v err=%v", ok, err)
	}
	if got.Messages[1].Context.Sources[0].Detail != "/tmp/hecate" {
		t.Fatalf("context packet source mutated through get snapshot: %+v", got.Messages[1].Context.Sources[0])
	}
	if got.Messages[1].Context.Items[0].Origin != "/tmp/hecate" {
		t.Fatalf("context packet item mutated through get snapshot: %+v", got.Messages[1].Context.Items[0])
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("List = %+v, want created session", list)
	}
	if list[0].WorkspaceBranch != "main" || len(list[0].Messages) != 2 || list[0].AgentID != DefaultAgentID || list[0].TaskID != "task_chat_1" {
		t.Fatalf("List summary = %+v, want cached branch and message count", list[0])
	}
	if list[0].ProjectID != "proj_hecate" {
		t.Fatalf("List summary project_id = %q, want proj_hecate", list[0].ProjectID)
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

func runStoreToolsEnabledRoundTrip(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	created, err := store.Create(ctx, Session{
		ID:      "chat_tools_enabled",
		AgentID: DefaultAgentID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Two messages with explicit, opposite ToolsEnabled values to
	// verify the round-trip preserves each independently and the
	// boolean isn't being collapsed to a per-session signal.
	if _, err := store.AppendMessage(ctx, created.ID, Message{
		ID:            "msg_tools_on",
		ExecutionMode: ExecutionModeHecateTask,
		ToolsEnabled:  true,
		Role:          "user",
		Content:       "with tools",
	}); err != nil {
		t.Fatalf("AppendMessage(tools_on): %v", err)
	}
	if _, err := store.AppendMessage(ctx, created.ID, Message{
		ID:            "msg_tools_off",
		ExecutionMode: ExecutionModeHecateTask,
		ToolsEnabled:  false,
		Role:          "user",
		Content:       "no tools",
	}); err != nil {
		t.Fatalf("AppendMessage(tools_off): %v", err)
	}

	session, ok, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: ok = false")
	}
	byID := make(map[string]Message, len(session.Messages))
	for _, m := range session.Messages {
		byID[m.ID] = m
	}
	if got := byID["msg_tools_on"].ToolsEnabled; !got {
		t.Errorf("msg_tools_on.ToolsEnabled = false, want true")
	}
	if got := byID["msg_tools_off"].ToolsEnabled; got {
		t.Errorf("msg_tools_off.ToolsEnabled = true, want false")
	}

	// UpdateMessage flips the flag — verifies the write path preserves
	// the column on UPDATE, not just INSERT.
	if _, err := store.UpdateMessage(ctx, created.ID, "msg_tools_off", func(m *Message) {
		m.ToolsEnabled = true
	}); err != nil {
		t.Fatalf("UpdateMessage: %v", err)
	}
	session, _, err = store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	for _, m := range session.Messages {
		if m.ID == "msg_tools_off" && !m.ToolsEnabled {
			t.Errorf("msg_tools_off.ToolsEnabled after update = false, want true")
		}
	}
}

func runStoreDeepCopiesConfigOptions(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	created, err := store.Create(ctx, Session{
		ID:      "chat_config_options",
		AgentID: "codex",
		ConfigOptions: []agentcontrols.ConfigOption{
			{
				ID:           "model",
				Name:         "Model",
				Type:         agentcontrols.ConfigOptionTypeSelect,
				CurrentValue: "fast",
				Options: []agentcontrols.ConfigSelectOption{
					{Value: "fast", Name: "Fast"},
					{Value: "smart", Name: "Smart"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	created.ConfigOptions[0].CurrentValue = "mutated"
	created.ConfigOptions[0].Options[0].Name = "Mutated"

	got, ok, err := store.Get(ctx, "chat_config_options")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.ConfigOptions[0].CurrentValue != "fast" || got.ConfigOptions[0].Options[0].Name != "Fast" {
		t.Fatalf("stored options mutated through create snapshot: %#v", got.ConfigOptions)
	}
	got.ConfigOptions[0].CurrentValue = "again"
	got.ConfigOptions[0].Options[1].Name = "Again"

	got, ok, err = store.Get(ctx, "chat_config_options")
	if err != nil || !ok {
		t.Fatalf("Get after mutation: ok=%v err=%v", ok, err)
	}
	if got.ConfigOptions[0].CurrentValue != "fast" || got.ConfigOptions[0].Options[1].Name != "Smart" {
		t.Fatalf("stored options mutated through get snapshot: %#v", got.ConfigOptions)
	}
	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	list[0].ConfigOptions[0].Options[0].Name = "Listed"
	got, ok, err = store.Get(ctx, "chat_config_options")
	if err != nil || !ok {
		t.Fatalf("Get after list mutation: ok=%v err=%v", ok, err)
	}
	if got.ConfigOptions[0].Options[0].Name != "Fast" {
		t.Fatalf("stored options mutated through list snapshot: %#v", got.ConfigOptions)
	}
}

func runStoreAvailableCommandsRoundTrip(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	created, err := store.Create(ctx, Session{
		ID:        "chat_commands",
		Title:     "Commands",
		AgentID:   "codex",
		Workspace: "/tmp/hecate",
		AvailableCommands: []agentcontrols.Command{
			{Name: "web", Description: "Search the web", InputHint: "query"},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	created.AvailableCommands[0].Name = "mutated"
	got, ok, err := store.Get(ctx, "chat_commands")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if len(got.AvailableCommands) != 1 || got.AvailableCommands[0].Name != "web" || got.AvailableCommands[0].InputHint != "query" {
		t.Fatalf("stored commands = %#v, want web command", got.AvailableCommands)
	}
	got.AvailableCommands[0].Description = "mutated"
	again, _, err := store.Get(ctx, "chat_commands")
	if err != nil {
		t.Fatalf("Get again: %v", err)
	}
	if again.AvailableCommands[0].Description != "Search the web" {
		t.Fatalf("stored command mutated through get snapshot: %#v", again.AvailableCommands)
	}
	updated, err := store.UpdateSession(ctx, "chat_commands", func(item *Session) {
		item.AvailableCommands = []agentcontrols.Command{}
	})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if updated.AvailableCommands == nil || len(updated.AvailableCommands) != 0 {
		t.Fatalf("updated commands = %#v, want non-nil empty slice", updated.AvailableCommands)
	}
}

func runStoreDeleteByProjectID(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	seed := []Session{
		{ID: "chat_project_a", ProjectID: "proj_delete", AgentID: DefaultAgentID},
		{ID: "chat_project_b", ProjectID: "proj_delete", AgentID: "codex"},
		{ID: "chat_other", ProjectID: "proj_other", AgentID: DefaultAgentID},
		{ID: "chat_unprojected", AgentID: DefaultAgentID},
	}
	for _, session := range seed {
		if _, err := store.Create(ctx, session); err != nil {
			t.Fatalf("Create(%s): %v", session.ID, err)
		}
		if _, err := store.AppendMessage(ctx, session.ID, Message{
			ID:      "msg_" + session.ID,
			Role:    "user",
			Content: "hello",
		}); err != nil {
			t.Fatalf("AppendMessage(%s): %v", session.ID, err)
		}
	}

	if err := store.DeleteByProjectID(ctx, "proj_delete"); err != nil {
		t.Fatalf("DeleteByProjectID: %v", err)
	}
	for _, id := range []string{"chat_project_a", "chat_project_b"} {
		if _, ok, err := store.Get(ctx, id); err != nil || ok {
			t.Fatalf("Get(%s) after project delete: ok=%v err=%v, want missing", id, ok, err)
		}
	}
	for _, id := range []string{"chat_other", "chat_unprojected"} {
		got, ok, err := store.Get(ctx, id)
		if err != nil || !ok {
			t.Fatalf("Get(%s) after project delete: ok=%v err=%v, want retained", id, ok, err)
		}
		if len(got.Messages) != 1 {
			t.Fatalf("Get(%s) messages = %d, want retained message", id, len(got.Messages))
		}
	}
}

func runStoreDoesNotHydrateTaskIDForAnonymousAgentSegment(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	created, err := store.Create(ctx, Session{
		ID:          "chat_1",
		AgentID:     DefaultAgentID,
		TaskID:      "task_previous",
		LatestRunID: "run_previous",
		Provider:    "openai",
		Model:       "gpt-4o-mini",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := store.AppendMessage(ctx, created.ID, Message{
		ID:            "msg_new_segment",
		ExecutionMode: ExecutionModeHecateTask,
		SegmentID:     "segment_pending_new_task",
		Role:          "user",
		Content:       "tools again",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if got := updated.Messages[len(updated.Messages)-1]; got.TaskID != "" {
		t.Fatalf("anonymous agent segment task_id = %q, want empty until new task is assigned", got.TaskID)
	}

	updated, err = store.AppendMessage(ctx, created.ID, Message{
		ID:            "msg_existing_task",
		ExecutionMode: ExecutionModeHecateTask,
		SegmentID:     "task:task_previous",
		Role:          "assistant",
		Content:       "continuing previous task",
	})
	if err != nil {
		t.Fatalf("AppendMessage(existing task): %v", err)
	}
	if got := updated.Messages[len(updated.Messages)-1]; got.TaskID != "task_previous" {
		t.Fatalf("task segment task_id = %q, want hydrated previous task", got.TaskID)
	}
}

func runStoreReconcileInterruptedRuns(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	created, err := store.Create(ctx, Session{
		ID:        "chat_interrupted",
		Title:     "Interrupted",
		AgentID:   "codex",
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
		ID:            "msg_assistant",
		ExecutionMode: ExecutionModeExternalAgent,
		RunID:         "agent_run_interrupted",
		Role:          "assistant",
		Content:       "partial answer",
		AgentID:       "codex",
		AgentName:     "Codex",
		Status:        "running",
		CostMode:      "external",
		Workspace:     "/tmp/hecate",
		StartedAt:     time.Now().UTC().Add(-time.Minute),
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

	orphaned, err := store.Create(ctx, Session{
		ID:              "chat_orphaned_external",
		Title:           "Orphaned external run",
		AgentID:         "grok_build",
		DriverKind:      "acp",
		NativeSessionID: "native_orphaned",
		Workspace:       "/tmp/hecate",
		Status:          "running",
	})
	if err != nil {
		t.Fatalf("Create(orphaned): %v", err)
	}
	count, err = ReconcileInterruptedRuns(ctx, store, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("ReconcileInterruptedRuns orphaned: %v", err)
	}
	if count != 1 {
		t.Fatalf("orphaned reconciled count = %d, want 1", count)
	}
	got, ok, err = store.Get(ctx, orphaned.ID)
	if err != nil || !ok {
		t.Fatalf("Get(orphaned): ok=%v err=%v", ok, err)
	}
	if got.Status != "cancelled" {
		t.Fatalf("orphaned session status = %q, want cancelled", got.Status)
	}
}
