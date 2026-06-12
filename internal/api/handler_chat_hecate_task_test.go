package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestHecateAgentTaskOrchestrator_StartCreatesTaskWithContextPacket(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := taskstate.NewMemoryStore()
	runner := &recordingHecateAgentTaskRunner{startRunID: "run_fixed"}
	now := time.Date(2026, 6, 5, 9, 30, 0, 0, time.UTC)
	orchestrator := hecateAgentTaskOrchestrator{
		store:      store,
		runner:     runner,
		taskID:     func() string { return "task_fixed" },
		resourceID: func(prefix string) string { return prefix + "_fixed" },
		now:        func() time.Time { return now },
	}

	task, run, err := orchestrator.StartOrContinue(ctx, hecateAgentTaskRunCommand{
		Session: chat.Session{
			ID:          "chat_start",
			ProjectID:   "proj_start",
			Workspace:   "/tmp/hecate-chat",
			Provider:    "openai",
			Model:       "gpt-4o",
			RTKEnabled:  true,
			Title:       " ",
			LatestRunID: "ignored",
		},
		Prompt:       "use tools",
		SystemPrompt: "  be concise  ",
		ForceNewTask: true,
		ContextPacket: chat.ContextPacket{
			Version: "chat_context_v1",
			Model:   "gpt-4o",
		},
	})
	if err != nil {
		t.Fatalf("StartOrContinue: %v", err)
	}
	if runner.startCalls != 1 || runner.continueCalls != 0 {
		t.Fatalf("runner calls = start %d continue %d, want start only", runner.startCalls, runner.continueCalls)
	}
	if task.ID != "task_fixed" || task.Title != "Hecate Chat" || task.Prompt != "use tools" || task.SystemPrompt != "be concise" {
		t.Fatalf("task identity/prompt = %+v, want fixed Hecate Chat task", task)
	}
	if task.ExecutionKind != "agent_loop" || task.ExecutionProfile != "chat_agent" || task.OriginKind != "chat" || task.OriginID != "chat_start" {
		t.Fatalf("task execution fields = %+v, want chat agent loop", task)
	}
	if task.WorkingDirectory != "/tmp/hecate-chat" || task.SandboxAllowedRoot != "/tmp/hecate-chat" || !task.RTKEnabled {
		t.Fatalf("task workspace/rtk = wd %q root %q rtk %v, want session workspace and RTK", task.WorkingDirectory, task.SandboxAllowedRoot, task.RTKEnabled)
	}
	if !task.CreatedAt.Equal(now) || !task.UpdatedAt.Equal(now) {
		t.Fatalf("task timestamps = created %v updated %v, want fixed now", task.CreatedAt, task.UpdatedAt)
	}
	if run.ID != "run_fixed" || run.TaskID != task.ID {
		t.Fatalf("run = %+v, want initialized run for task", run)
	}
	assertHecateAgentRunContextRefs(t, run.ContextPacket, chat.ContextRefs{
		SessionID: "chat_start",
		TaskID:    "task_fixed",
		RunID:     "run_fixed",
		ProjectID: "proj_start",
	})
}

func TestHecateAgentTaskOrchestrator_StartCreatesTaskWithMCPServers(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := taskstate.NewMemoryStore()
	runner := &recordingHecateAgentTaskRunner{startRunID: "run_mcp"}
	orchestrator := hecateAgentTaskOrchestrator{
		store:      store,
		runner:     runner,
		taskID:     func() string { return "task_mcp" },
		resourceID: func(prefix string) string { return prefix + "_mcp" },
		now:        func() time.Time { return time.Date(2026, 6, 12, 8, 0, 0, 0, time.UTC) },
	}

	_, _, err := orchestrator.StartOrContinue(ctx, hecateAgentTaskRunCommand{
		Session: chat.Session{
			ID:        "chat_mcp",
			Workspace: "/tmp/hecate-chat-mcp",
			Provider:  "openai",
			Model:     "gpt-4o",
		},
		Prompt:       "show the app",
		ForceNewTask: true,
		MCPServers: []types.MCPServerConfig{{
			Name:           "weather",
			Command:        "node",
			Args:           []string{"examples/mcp-weather-app-server.mjs"},
			ApprovalPolicy: types.MCPApprovalAuto,
		}},
		ContextPacket: chat.ContextPacket{
			Version: "chat_context_v1",
		},
	})
	if err != nil {
		t.Fatalf("StartOrContinue: %v", err)
	}
	if runner.startCalls != 1 {
		t.Fatalf("startCalls = %d, want 1", runner.startCalls)
	}
	if got := runner.startTask.MCPServers; len(got) != 1 || got[0].Name != "weather" || got[0].Command != "node" || got[0].ApprovalPolicy != types.MCPApprovalAuto {
		t.Fatalf("task MCPServers = %+v, want weather stdio server", got)
	}
	if got := runner.startTask.MCPServers[0].Args; len(got) != 1 || got[0] != "examples/mcp-weather-app-server.mjs" {
		t.Fatalf("task MCP args = %+v, want demo server arg", got)
	}
}

func TestHecateAgentTaskOrchestrator_ContinueUsesExistingTaskRun(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := taskstate.NewMemoryStore()
	now := time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC)
	task, err := store.CreateTask(ctx, types.Task{
		ID:          "task_existing",
		Title:       "Existing chat",
		Status:      "completed",
		LatestRunID: "run_existing",
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, types.TaskRun{
		ID:        "run_existing",
		TaskID:    task.ID,
		Status:    "completed",
		StartedAt: now,
	}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	runner := &recordingHecateAgentTaskRunner{continueRunID: "run_next"}
	orchestrator := hecateAgentTaskOrchestrator{
		store:      store,
		runner:     runner,
		taskID:     func() string { return "task_should_not_be_used" },
		resourceID: func(prefix string) string { return prefix + "_fixed" },
		now:        func() time.Time { return now.Add(time.Hour) },
	}

	continuedTask, run, err := orchestrator.StartOrContinue(ctx, hecateAgentTaskRunCommand{
		Session: chat.Session{
			ID:          "chat_continue",
			ProjectID:   "proj_continue",
			TaskID:      task.ID,
			LatestRunID: "run_existing",
		},
		Prompt: "continue with tools",
		ContextPacket: chat.ContextPacket{
			Version: "chat_context_v1",
		},
	})
	if err != nil {
		t.Fatalf("StartOrContinue: %v", err)
	}
	if runner.startCalls != 0 || runner.continueCalls != 1 {
		t.Fatalf("runner calls = start %d continue %d, want continue only", runner.startCalls, runner.continueCalls)
	}
	if runner.continuePrompt != "continue with tools" {
		t.Fatalf("continue prompt = %q, want request prompt", runner.continuePrompt)
	}
	if runner.continueTask.ID != task.ID || runner.continueRun.ID != "run_existing" {
		t.Fatalf("continue input = task %q run %q, want existing task/run", runner.continueTask.ID, runner.continueRun.ID)
	}
	if continuedTask.ID != task.ID || run.ID != "run_next" || run.TaskID != task.ID {
		t.Fatalf("continued result = task %+v run %+v, want next run for existing task", continuedTask, run)
	}
	assertHecateAgentRunContextRefs(t, run.ContextPacket, chat.ContextRefs{
		SessionID: "chat_continue",
		TaskID:    task.ID,
		RunID:     "run_next",
		ProjectID: "proj_continue",
	})
}

type recordingHecateAgentTaskRunner struct {
	startRunID    string
	continueRunID string

	startCalls    int
	continueCalls int

	startTask      types.Task
	continueTask   types.Task
	continueRun    types.TaskRun
	continuePrompt string
}

func (r *recordingHecateAgentTaskRunner) StartTaskWithRunInitializer(_ context.Context, task types.Task, _ func(string) string, init func(*types.TaskRun)) (*orchestrator.StartTaskResult, error) {
	r.startCalls++
	r.startTask = task
	runID := r.startRunID
	if runID == "" {
		runID = "run_start"
	}
	run := types.TaskRun{ID: runID, TaskID: task.ID, Status: "queued"}
	init(&run)
	return &orchestrator.StartTaskResult{Task: task, Run: run}, nil
}

func (r *recordingHecateAgentTaskRunner) ContinueAgentTaskWithRunInitializer(_ context.Context, task types.Task, run types.TaskRun, prompt string, _ func(string) string, init func(*types.TaskRun)) (*orchestrator.StartTaskResult, error) {
	r.continueCalls++
	r.continueTask = task
	r.continueRun = run
	r.continuePrompt = prompt
	runID := r.continueRunID
	if runID == "" {
		runID = "run_continue"
	}
	nextRun := types.TaskRun{ID: runID, TaskID: task.ID, Status: "queued"}
	init(&nextRun)
	return &orchestrator.StartTaskResult{Task: task, Run: nextRun}, nil
}

func assertHecateAgentRunContextRefs(t *testing.T, raw json.RawMessage, refs chat.ContextRefs) {
	t.Helper()
	if len(raw) == 0 {
		t.Fatalf("run context packet is empty")
	}
	var packet chat.ContextPacket
	if err := json.Unmarshal(raw, &packet); err != nil {
		t.Fatalf("decode context packet: %v", err)
	}
	if packet.Refs == nil {
		t.Fatalf("context packet refs are nil: %+v", packet)
	}
	if packet.Refs.SessionID != refs.SessionID || packet.Refs.TaskID != refs.TaskID || packet.Refs.RunID != refs.RunID || packet.Refs.ProjectID != refs.ProjectID {
		t.Fatalf("context refs = %+v, want %+v", *packet.Refs, refs)
	}
}
