package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatattachments"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/governor"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/projectruntime"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/retention"
	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestPostgresStoresMigrateWhenDatabaseURLProvided(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("HECATE_POSTGRES_TEST_URL"))
	if databaseURL == "" {
		t.Skip("set HECATE_POSTGRES_TEST_URL to run Postgres store migration smoke")
	}

	ctx := context.Background()
	client, err := storage.NewPostgresClient(ctx, storage.PostgresConfig{
		DatabaseURL: databaseURL,
		TablePrefix: "hecate_test",
	})
	if err != nil {
		t.Fatalf("NewPostgresClient: %v", err)
	}
	t.Cleanup(func() {
		_, _ = client.ClearData(context.Background())
		_ = client.Close()
	})
	if _, err := client.ClearData(ctx); err != nil {
		t.Fatalf("ClearData before smoke: %v", err)
	}

	controlPlaneStore, err := controlplane.NewPostgresStore(ctx, client, "control-plane")
	if err != nil {
		t.Fatalf("controlplane.NewPostgresStore: %v", err)
	}
	retentionStore, err := retention.NewPostgresHistoryStore(ctx, client, "retention_runs")
	if err != nil {
		t.Fatalf("retention.NewPostgresHistoryStore: %v", err)
	}
	providerHistoryStore, err := providers.NewPostgresHealthHistoryStore(ctx, client, "provider_health_history")
	if err != nil {
		t.Fatalf("providers.NewPostgresHealthHistoryStore: %v", err)
	}
	taskStore, err := taskstate.NewPostgresStore(ctx, client)
	if err != nil {
		t.Fatalf("taskstate.NewPostgresStore: %v", err)
	}
	runQueue, err := orchestrator.NewPostgresRunQueue(ctx, client, 30*time.Second)
	if err != nil {
		t.Fatalf("orchestrator.NewPostgresRunQueue: %v", err)
	}
	usageStore, err := governor.NewPostgresUsageStore(ctx, client)
	if err != nil {
		t.Fatalf("governor.NewPostgresUsageStore: %v", err)
	}
	projectRuntimeStore, err := projectruntime.NewPostgresStore(ctx, client)
	if err != nil {
		t.Fatalf("projectruntime.NewPostgresStore: %v", err)
	}
	agentProfileStore, err := agentprofiles.NewPostgresStore(ctx, client)
	if err != nil {
		t.Fatalf("agentprofiles.NewPostgresStore: %v", err)
	}
	chatStore, err := chat.NewPostgresStore(ctx, client)
	if err != nil {
		t.Fatalf("chat.NewPostgresStore: %v", err)
	}
	chatAttachmentStore, err := chatattachments.NewPostgresStore(ctx, client)
	if err != nil {
		t.Fatalf("chatattachments.NewPostgresStore: %v", err)
	}
	approvalStore, err := agentadapters.NewPostgresApprovalStore(ctx, client)
	if err != nil {
		t.Fatalf("agentadapters.NewPostgresApprovalStore: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	now := time.Now().UTC()
	projectID := "project-" + suffix

	providerID := "provider-" + suffix
	if _, err := controlPlaneStore.UpsertProvider(ctx, controlplane.Provider{
		ID:       providerID,
		Name:     "postgres-smoke",
		Kind:     "cloud",
		Protocol: "openai",
		BaseURL:  "https://example.invalid/v1",
		Enabled:  true,
	}, nil); err != nil {
		t.Fatalf("control plane upsert provider: %v", err)
	}
	if state, err := controlPlaneStore.Snapshot(ctx); err != nil {
		t.Fatalf("control plane snapshot: %v", err)
	} else if len(state.Providers) == 0 {
		t.Fatal("control plane snapshot has no providers")
	}

	if err := retentionStore.AppendRun(ctx, retention.HistoryRecord{
		StartedAt:  now.Format(time.RFC3339Nano),
		FinishedAt: now.Add(time.Second).Format(time.RFC3339Nano),
		Trigger:    "postgres_smoke",
		Actor:      "test",
		RequestID:  "request-" + suffix,
	}); err != nil {
		t.Fatalf("retention append run: %v", err)
	}
	if runs, err := retentionStore.ListRuns(ctx, 1); err != nil {
		t.Fatalf("retention list runs: %v", err)
	} else if len(runs) != 1 {
		t.Fatalf("retention list runs length = %d, want 1", len(runs))
	}

	if err := providerHistoryStore.Append(ctx, providers.HealthHistoryRecord{
		Provider:  providerID,
		Model:     "model",
		Event:     "success",
		Status:    "success",
		Available: true,
		Timestamp: now.Format(time.RFC3339Nano),
		OpenUntil: "",
	}); err != nil {
		t.Fatalf("provider history append: %v", err)
	}
	if records, err := providerHistoryStore.List(ctx, providers.HealthHistoryFilter{Provider: providerID, Limit: 1}); err != nil {
		t.Fatalf("provider history list: %v", err)
	} else if len(records) != 1 || !records[0].Available {
		t.Fatalf("provider history records = %#v, want one available record", records)
	}

	taskID := "task-" + suffix
	runID := "run-" + suffix
	if _, err := taskStore.CreateTask(ctx, types.Task{
		ID:        taskID,
		Title:     "Postgres smoke",
		Prompt:    "verify postgres",
		ProjectID: projectID,
		Status:    "queued",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("task create: %v", err)
	}
	if _, err := taskStore.CreateRun(ctx, types.TaskRun{
		ID:        runID,
		TaskID:    taskID,
		Number:    1,
		Status:    "running",
		StartedAt: now,
		RequestID: "request-" + suffix,
		TraceID:   "trace-" + suffix,
	}); err != nil {
		t.Fatalf("run create: %v", err)
	}
	if _, err := taskStore.AppendRunEvent(ctx, types.TaskRunEvent{
		TaskID:    taskID,
		RunID:     runID,
		EventType: "run.started",
		Data:      map[string]any{"ok": true},
		CreatedAt: now,
		RequestID: "request-" + suffix,
		TraceID:   "trace-" + suffix,
	}); err != nil {
		t.Fatalf("run event append: %v", err)
	}
	if tasks, err := taskStore.ListTasks(ctx, taskstate.TaskFilter{ProjectID: &projectID, Limit: 1}); err != nil {
		t.Fatalf("task list: %v", err)
	} else if len(tasks) != 1 || tasks[0].ID != taskID {
		t.Fatalf("task list = %#v, want task %q", tasks, taskID)
	}

	queueRunID := "queue-run-" + suffix
	if err := runQueue.Enqueue(ctx, orchestrator.QueueJob{TaskID: taskID, RunID: queueRunID}); err != nil {
		t.Fatalf("queue enqueue: %v", err)
	}
	claim, ok, err := runQueue.Claim(ctx, "worker-"+suffix, time.Second)
	if err != nil {
		t.Fatalf("queue claim: %v", err)
	}
	if !ok || claim.Job.RunID != queueRunID {
		t.Fatalf("queue claim = %#v ok=%v, want run %q", claim, ok, queueRunID)
	}
	if err := runQueue.Ack(ctx, claim.ClaimID); err != nil {
		t.Fatalf("queue ack: %v", err)
	}

	if _, err := usageStore.RecordUsage(ctx, governor.UsageEvent{
		UsageKey:   "usage-" + suffix,
		RequestID:  "request-" + suffix,
		Provider:   providerID,
		Model:      "model",
		Usage:      types.Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5},
		CostMicros: 42,
		OccurredAt: now,
	}); err != nil {
		t.Fatalf("usage record: %v", err)
	}
	if err := usageStore.AppendEvent(ctx, governor.UsageHistoryEvent{
		Key:             "usage-" + suffix,
		Type:            "completion",
		Provider:        providerID,
		Model:           "model",
		RequestID:       "request-" + suffix,
		AmountMicrosUSD: 42,
		PromptTokens:    2,
		TotalTokens:     5,
		OccurredAt:      now,
	}); err != nil {
		t.Fatalf("usage append event: %v", err)
	}
	if events, err := usageStore.ListEvents(ctx, "usage-"+suffix, 1); err != nil {
		t.Fatalf("usage list events: %v", err)
	} else if len(events) != 1 {
		t.Fatalf("usage events length = %d, want 1", len(events))
	}

	assignmentID := "assignment-" + suffix
	if _, err := projectRuntimeStore.Upsert(ctx, projectruntime.AssignmentRuntime{
		ProjectID:    projectID,
		AssignmentID: assignmentID,
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:   projectwork.AssignmentExecutionKindTaskRun,
			TaskID: taskID,
			RunID:  runID,
			Status: projectwork.AssignmentStatusRunning,
		},
		ContextPacket: []byte(`{"source":"postgres-smoke"}`),
		StartedAt:     now,
	}); err != nil {
		t.Fatalf("project runtime upsert: %v", err)
	}
	if runtime, ok, err := projectRuntimeStore.Get(ctx, projectID, assignmentID); err != nil {
		t.Fatalf("project runtime get: %v", err)
	} else if !ok || runtime.ExecutionRef.RunID != runID || string(runtime.ContextPacket) != `{"source":"postgres-smoke"}` {
		t.Fatalf("project runtime = %+v ok=%v, want task/run overlay", runtime, ok)
	}

	if _, err := agentProfileStore.Create(ctx, agentprofiles.Profile{
		ID:   "profile-" + suffix,
		Name: "Smoke profile",
	}); err != nil {
		t.Fatalf("agent profile create: %v", err)
	}

	sessionID := "session-" + suffix
	if _, err := chatStore.Create(ctx, chat.Session{
		ID:         sessionID,
		Title:      "Smoke chat",
		ProjectID:  projectID,
		RTKEnabled: true,
	}); err != nil {
		t.Fatalf("chat session create: %v", err)
	}
	if _, err := chatAttachmentStore.Create(ctx, chatattachments.StoredAttachment{
		Attachment: chatattachments.Attachment{
			ID:        "attachment_smoke",
			SessionID: sessionID,
			Filename:  "smoke.png",
			MediaType: "image/png",
			SizeBytes: 3,
			SHA256:    "smoke-digest",
		},
		Data: []byte("png"),
	}); err != nil {
		t.Fatalf("chatAttachmentStore.Create: %v", err)
	}
	if got, ok, err := chatAttachmentStore.Get(ctx, sessionID, "attachment_smoke"); err != nil || !ok || string(got.Data) != "png" {
		t.Fatalf("chatAttachmentStore.Get: ok=%v data=%q err=%v", ok, got.Data, err)
	}
	if _, err := chatStore.AppendMessage(ctx, sessionID, chat.Message{
		ID:           "message-" + suffix,
		Role:         "user",
		Content:      "hello postgres",
		ToolsEnabled: true,
		Status:       "completed",
		CreatedAt:    now,
	}); err != nil {
		t.Fatalf("chat append message: %v", err)
	}

	approval, err := approvalStore.CreateApproval(ctx, agentadapters.Approval{
		SessionID:  sessionID,
		AdapterID:  "codex",
		Workspace:  "/tmp/hecate-postgres-smoke-" + suffix,
		ToolKind:   "shell",
		ToolName:   "shell_exec",
		ACPPayload: []byte(`{"tool":"shell_exec"}`),
		ACPOptions: []agentadapters.ApprovalOption{{
			OptionID: "approve",
			Kind:     "allow",
			Name:     "Approve",
		}},
		ScopeChoices: []agentadapters.ApprovalScope{agentadapters.ApprovalScopeOnce},
		ExpiresAt:    now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("approval create: %v", err)
	}
	if _, err := approvalStore.ResolveApproval(ctx, approval.ID, agentadapters.ApprovalStatusApproved, agentadapters.ApprovalDecisionApprove, "approve", agentadapters.ApprovalScopeOnce, agentadapters.PathOperator, "ok", now); err != nil {
		t.Fatalf("approval resolve: %v", err)
	}
}
