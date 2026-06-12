package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestHecateAgentChatCreatesVisibleTaskAndContinuesSameTask(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-hecate-agent",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Now().UTC(),
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "Hecate Chat final answer."},
				FinishReason: "stop",
			}},
			Usage: types.Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14},
		},
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	handler := NewServer(logger, apiHandler)
	client := newTaskTestClient(t, handler)
	workspace := t.TempDir()

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		fmt.Sprintf(`{"agent_id":"hecate","title":"Refactor chat","workspace":%q,"provider":"openai","model":"gpt-4o-mini","rtk_enabled":true}`, workspace))
	if session.Data.AgentID != chat.DefaultAgentID {
		t.Fatalf("agent_id = %q, want hecate", session.Data.AgentID)
	}
	if !session.Data.RTKEnabled {
		t.Fatal("rtk_enabled = false, want true")
	}
	if session.Data.Capabilities.ToolCalling != modelcaps.ToolCallingParallel {
		t.Fatalf("capabilities = %+v, want parallel catalog capabilities", session.Data.Capabilities)
	}

	first := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","content":"inspect the repo","system_prompt":"Prefer small, reviewable diffs."}`)
	if first.Data.TaskID == "" || first.Data.LatestRunID == "" {
		t.Fatalf("first response missing task/run linkage: %+v", first.Data)
	}
	backingTask, found, err := apiHandler.taskStore.GetTask(context.Background(), first.Data.TaskID)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if !found || !backingTask.RTKEnabled {
		t.Fatalf("backing task RTKEnabled = %v, found %v; want true", backingTask.RTKEnabled, found)
	}
	if first.Data.Status != "completed" {
		t.Fatalf("first status = %q, want completed", first.Data.Status)
	}
	if len(first.Data.Messages) < 2 || !strings.Contains(first.Data.Messages[len(first.Data.Messages)-1].Content, "Hecate Chat final answer") {
		t.Fatalf("first transcript = %+v", first.Data.Messages)
	}
	assistant := first.Data.Messages[len(first.Data.Messages)-1]
	if assistant.ExecutionMode != chat.ExecutionModeHecateTask || assistant.TaskID != first.Data.TaskID || assistant.SegmentID != "task:"+first.Data.TaskID {
		t.Fatalf("assistant message execution snapshot = mode %q segment %q task %q", assistant.ExecutionMode, assistant.SegmentID, assistant.TaskID)
	}
	if assistant.Provider != "openai" || assistant.Model != "gpt-4o-mini" || assistant.Capabilities.ToolCalling != modelcaps.ToolCallingParallel {
		t.Fatalf("assistant message model snapshot = provider %q model %q caps %+v", assistant.Provider, assistant.Model, assistant.Capabilities)
	}
	if first.Data.Messages[0].SegmentID != assistant.SegmentID || first.Data.Messages[0].TaskID != first.Data.TaskID {
		t.Fatalf("user message segment/task = %q/%q, want %q/%q", first.Data.Messages[0].SegmentID, first.Data.Messages[0].TaskID, assistant.SegmentID, first.Data.TaskID)
	}
	if !agentChatMessageHasActivity(assistant, "thinking") {
		t.Fatalf("assistant activities missing projected task thinking activity: %+v", assistant.Activities)
	}
	if !agentChatMessageHasActivity(assistant, "run_result") {
		t.Fatalf("assistant activities missing projected task run result activity: %+v", assistant.Activities)
	}
	if assistant.Timing == nil || assistant.Timing.TurnCount == 0 || assistant.Timing.Bottleneck == "" {
		t.Fatalf("assistant timing = %+v, want persisted Hecate Chat run timing", assistant.Timing)
	}

	taskResponse := mustRequestJSON[TaskResponse](client, http.MethodGet, "/hecate/v1/tasks/"+first.Data.TaskID, "")
	if taskResponse.Data.ExecutionKind != "agent_loop" || taskResponse.Data.ExecutionProfile != "chat_agent" {
		t.Fatalf("task execution fields = kind %q profile %q", taskResponse.Data.ExecutionKind, taskResponse.Data.ExecutionProfile)
	}
	if taskResponse.Data.SystemPrompt != "Prefer small, reviewable diffs." {
		t.Fatalf("task system_prompt = %q, want Hecate Chat instructions", taskResponse.Data.SystemPrompt)
	}
	if taskResponse.Data.OriginKind != "chat" || taskResponse.Data.OriginID != session.Data.ID {
		t.Fatalf("task origin = %q/%q, want chat/%s", taskResponse.Data.OriginKind, taskResponse.Data.OriginID, session.Data.ID)
	}
	settings := mustRequestJSON[ChatSessionResponse](client, http.MethodPatch, "/hecate/v1/chat/sessions/"+session.Data.ID+"/settings",
		`{"rtk_enabled":false}`)
	if settings.Data.RTKEnabled {
		t.Fatal("settings rtk_enabled = true, want false")
	}
	updatedBackingTask, found, err := apiHandler.taskStore.GetTask(context.Background(), first.Data.TaskID)
	if err != nil {
		t.Fatalf("GetTask(updated) error = %v", err)
	}
	if !found || updatedBackingTask.RTKEnabled {
		t.Fatalf("updated backing task RTKEnabled = %v, found %v; want false", updatedBackingTask.RTKEnabled, found)
	}

	second := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","content":"continue from there"}`)
	if second.Data.TaskID != first.Data.TaskID {
		t.Fatalf("second task_id = %q, want same task %q", second.Data.TaskID, first.Data.TaskID)
	}
	if second.Data.LatestRunID == "" || second.Data.LatestRunID == first.Data.LatestRunID {
		t.Fatalf("second latest_run_id = %q, want new continued run distinct from %q", second.Data.LatestRunID, first.Data.LatestRunID)
	}
	secondAssistant := second.Data.Messages[len(second.Data.Messages)-1]
	if secondAssistant.SegmentID != "task:"+first.Data.TaskID || secondAssistant.TaskID != first.Data.TaskID || secondAssistant.Model != "gpt-4o-mini" {
		t.Fatalf("second assistant runtime snapshot = segment %q task %q model %q", secondAssistant.SegmentID, secondAssistant.TaskID, secondAssistant.Model)
	}
	runs := mustRequestJSON[TaskRunsResponse](client, http.MethodGet, "/hecate/v1/tasks/"+first.Data.TaskID+"/runs", "")
	if len(runs.Data) != 2 {
		t.Fatalf("runs = %d, want 2 continued runs: %+v", len(runs.Data), runs.Data)
	}
	firstRun := findTaskRunItem(runs.Data, first.Data.LatestRunID)
	if firstRun.ID == "" {
		t.Fatalf("first run %q not found in runs: %+v", first.Data.LatestRunID, runs.Data)
	}
	if assistant.RequestID != firstRun.RequestID || assistant.TraceID != firstRun.TraceID || assistant.SpanID != firstRun.RootSpanID {
		t.Fatalf("assistant trace linkage = request %q trace %q span %q, want backing run request %q trace %q span %q",
			assistant.RequestID, assistant.TraceID, assistant.SpanID, firstRun.RequestID, firstRun.TraceID, firstRun.RootSpanID)
	}
	secondRun := findTaskRunItem(runs.Data, second.Data.LatestRunID)
	if secondRun.ID == "" {
		t.Fatalf("second run %q not found in runs: %+v", second.Data.LatestRunID, runs.Data)
	}
	if secondAssistant.RequestID != secondRun.RequestID || secondAssistant.TraceID != secondRun.TraceID || secondAssistant.SpanID != secondRun.RootSpanID {
		t.Fatalf("second assistant trace linkage = request %q trace %q span %q, want backing run request %q trace %q span %q",
			secondAssistant.RequestID, secondAssistant.TraceID, secondAssistant.SpanID, secondRun.RequestID, secondRun.TraceID, secondRun.RootSpanID)
	}
}

func TestHecateAgentChatProjectSessionInjectsProposalGuidance(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-hecate-project-agent",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Now().UTC(),
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "I will keep project changes reviewable."},
				FinishReason: "stop",
			}},
		},
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	handler := NewServer(logger, apiHandler)
	client := newTaskTestClient(t, handler)
	workspace := t.TempDir()

	project, err := apiHandler.projects.Create(context.Background(), projects.Project{
		ID:          "proj_hecate",
		Name:        "Hecate",
		Description: "Local AI operations console.",
	})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := apiHandler.projectWork.CreateRole(context.Background(), projectwork.AgentRoleProfile{
		ID:          "role_planner",
		ProjectID:   project.ID,
		Name:        "A Project Planner",
		Description: "Shapes reviewable project work.",
	}); err != nil {
		t.Fatalf("Create role: %v", err)
	}
	if _, err := apiHandler.memory.Create(context.Background(), memory.Entry{
		ID:         "mem_boundary",
		Scope:      memory.ScopeProject,
		ProjectID:  project.ID,
		Title:      "Project Assistant boundary",
		Body:       "Project changes should be drafted as typed proposals and applied only after operator review.",
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Create memory: %v", err)
	}

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		fmt.Sprintf(`{"agent_id":"hecate","title":"Project chat","project_id":%q,"workspace":%q,"provider":"openai","model":"gpt-4o-mini"}`, project.ID, workspace))
	started := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","content":"split this into backend and UI work","system_prompt":"Prefer concise answers."}`)
	if started.Data.TaskID == "" {
		t.Fatalf("started chat missing task id: %+v", started.Data)
	}
	backingTask, found, err := apiHandler.taskStore.GetTask(context.Background(), started.Data.TaskID)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if !found {
		t.Fatalf("backing task %q not found", started.Data.TaskID)
	}
	for _, want := range []string{
		"Project chat guidance",
		"Project: Hecate (proj_hecate)",
		"Project workflow boundary:",
		"Project Assistant is a proposal author only.",
		"Do not create or start chats, tasks, runs, external-agent sessions, promoted memory, or durable project records through generic tools or direct API calls.",
		"Assignments proposed from chat must stay queued and unstarted.",
		"Role hints:",
		"A Project Planner (role_planner): Shapes reviewable project work.",
		"Accepted project memory:",
		"Project memory: Project Assistant boundary\nID: mem_boundary\nTrust: operator_memory",
		"Project changes should be drafted as typed proposals and applied only after operator review.",
		"Operator system prompt:\nPrefer concise answers.",
	} {
		if !strings.Contains(backingTask.SystemPrompt, want) {
			t.Fatalf("task system_prompt missing %q:\n%s", want, backingTask.SystemPrompt)
		}
	}
}

func TestDirectHecateChatProjectSessionInjectsProposalGuidance(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-hecate-project-direct",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Now().UTC(),
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "Project changes should stay reviewable."},
				FinishReason: "stop",
			}},
		},
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	handler := NewServer(logger, apiHandler)
	client := newTaskTestClient(t, handler)

	project, err := apiHandler.projects.Create(context.Background(), projects.Project{
		ID:   "proj_direct",
		Name: "Direct Project",
	})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		fmt.Sprintf(`{"agent_id":"hecate","project_id":%q,"provider":"openai","model":"gpt-4o-mini"}`, project.ID))
	_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"content":"what should we plan?","system_prompt":"Answer plainly."}`)

	request := provider.LastRequest()
	if len(request.Messages) < 2 {
		t.Fatalf("provider messages = %+v, want system and user messages", request.Messages)
	}
	if request.Messages[0].Role != "system" {
		t.Fatalf("first provider message role = %q, want system", request.Messages[0].Role)
	}
	for _, want := range []string{
		"Project chat guidance",
		"Project: Direct Project (proj_direct)",
		"Project Assistant is a proposal author only.",
		"Operator system prompt:\nAnswer plainly.",
	} {
		if !strings.Contains(request.Messages[0].Content, want) {
			t.Fatalf("direct model system prompt missing %q:\n%s", want, request.Messages[0].Content)
		}
	}
}

func TestChatSessionsProjectID(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{name: "openai"}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	handler := NewServer(logger, apiHandler)
	client := newAPITestClient(t, handler)

	project, err := apiHandler.projects.Create(context.Background(), projects.Project{
		ID:   "proj_hecate",
		Name: "Hecate",
	})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		fmt.Sprintf(`{"agent_id":"hecate","project_id":%q,"provider":"openai","model":"gpt-4o-mini"}`, project.ID))
	if created.Data.ProjectID != project.ID {
		t.Fatalf("created project_id = %q, want %q", created.Data.ProjectID, project.ID)
	}

	list := mustRequestJSON[ChatSessionsResponse](client, http.MethodGet, "/hecate/v1/chat/sessions", "")
	if len(list.Data) != 1 || list.Data[0].ProjectID != project.ID {
		t.Fatalf("listed chat sessions = %+v, want one session for project %q", list.Data, project.ID)
	}

	recorder := client.mustRequestStatus(http.StatusNotFound, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","project_id":"proj_missing","provider":"openai","model":"gpt-4o-mini"}`)
	if !strings.Contains(recorder.Body.String(), "project not found") {
		t.Fatalf("missing project response = %s, want project not found", recorder.Body.String())
	}
}

func TestHecateAgentChatCreateDefaultsTitle(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-hecate-agent-title",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Now().UTC(),
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "ready"},
				FinishReason: "stop",
			}},
		},
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	handler := NewServer(logger, apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"openai","model":"gpt-4o-mini"}`)
	if session.Data.Title != "Hecate Chat" {
		t.Fatalf("title = %q, want Hecate Chat", session.Data.Title)
	}
}

func findTaskRunItem(items []TaskRunItem, id string) TaskRunItem {
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	return TaskRunItem{}
}

func TestHecateAgentTimingFromRunState(t *testing.T) {
	base := time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)
	run := types.TaskRun{ID: "run_1", TaskID: "task_1", StartedAt: base.Add(100 * time.Millisecond), FinishedAt: base.Add(7 * time.Second)}
	timing := hecateAgentTimingFromRunState(run, []types.TaskStep{
		{
			ID:         "step_model",
			Kind:       "model",
			ToolName:   "builtin.agent_loop_llm",
			StartedAt:  base.Add(200 * time.Millisecond),
			FinishedAt: base.Add(2200 * time.Millisecond),
		},
		{
			ID:         "step_tool",
			Kind:       "tool",
			ToolName:   "git_exec",
			StartedAt:  base.Add(2300 * time.Millisecond),
			FinishedAt: base.Add(2800 * time.Millisecond),
		},
		{
			ID:         "step_other_run",
			Kind:       "tool",
			ToolName:   "shell_exec",
			StartedAt:  base.Add(3 * time.Second),
			FinishedAt: base.Add(4 * time.Second),
			RunID:      "other_run",
		},
	}, []types.TaskApproval{
		{ID: "appr_1", RunID: "run_1", CreatedAt: base.Add(3 * time.Second), ResolvedAt: base.Add(6 * time.Second)},
		{ID: "appr_other", RunID: "other_run", CreatedAt: base, ResolvedAt: base.Add(time.Hour)},
	}, []types.TaskRunEvent{
		{EventType: "run.started", CreatedAt: base.Add(100 * time.Millisecond)},
	}, base, base.Add(7*time.Second))

	if timing.TotalMS != 7000 || timing.QueueMS != 100 || timing.ModelMS != 2000 || timing.ToolMS != 500 || timing.ApprovalWaitMS != 3000 {
		t.Fatalf("timing buckets = %+v", timing)
	}
	if timing.OverheadMS != 1400 {
		t.Fatalf("overhead_ms = %d, want 1400", timing.OverheadMS)
	}
	if timing.TurnCount != 1 || timing.ToolCount != 1 {
		t.Fatalf("counts = turns %d tools %d, want 1/1", timing.TurnCount, timing.ToolCount)
	}
	if timing.Bottleneck != "approval" || timing.BottleneckMS != 3000 {
		t.Fatalf("bottleneck = %s/%d, want approval/3000", timing.Bottleneck, timing.BottleneckMS)
	}
}

func TestHecateAgentChatPublishesLiveAssistantContent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	taskStore := taskstate.NewMemoryStore()
	chatStore := chat.NewMemoryStore()
	live := newAgentChatLive(agentChatSnapshotConfig{})
	handler := &Handler{
		taskStore:     taskStore,
		agentChat:     chatStore,
		agentChatLive: live,
	}
	now := time.Now().UTC()
	task, err := taskStore.CreateTask(ctx, types.Task{
		ID:            "task_live",
		Title:         "Live chat",
		ExecutionKind: "agent_loop",
		Status:        "running",
		CreatedAt:     now,
		UpdatedAt:     now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run, err := taskStore.CreateRun(ctx, types.TaskRun{
		ID:        "run_live",
		TaskID:    task.ID,
		Status:    "running",
		Model:     "gpt-4o-mini",
		Provider:  "openai",
		StartedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	session, err := chatStore.Create(ctx, chat.Session{
		ID:          "chat_live",
		Title:       "Live chat",
		AgentID:     chat.DefaultAgentID,
		TaskID:      task.ID,
		LatestRunID: run.ID,
		Provider:    "openai",
		Model:       "gpt-4o-mini",
		Workspace:   t.TempDir(),
		Status:      "running",
		CreatedAt:   now,
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	if _, err := chatStore.AppendMessage(ctx, session.ID, chat.Message{
		ID:            "msg_assistant",
		ExecutionMode: chat.ExecutionModeHecateTask,
		SegmentID:     "task:" + task.ID,
		TaskID:        task.ID,
		RunID:         run.ID,
		Role:          "assistant",
		Status:        "running",
		Content:       "",
		CreatedAt:     now,
		StartedAt:     now,
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	updates, unsubscribe := live.subscribe(session.ID)
	defer unsubscribe()
	done := make(chan error, 1)
	go func() {
		_, err := handler.waitForHecateAgentRun(ctx, task.ID, run.ID, session.ID, "msg_assistant")
		done <- err
	}()

	conversation, err := json.Marshal([]types.Message{
		{Role: "user", Content: "show the diff"},
		{Role: "assistant", Content: "I can see the diff now."},
	})
	if err != nil {
		t.Fatalf("marshal conversation: %v", err)
	}
	if _, err := taskStore.CreateArtifact(ctx, types.TaskArtifact{
		ID:          "convo-" + run.ID,
		TaskID:      task.ID,
		RunID:       run.ID,
		Kind:        "agent_conversation",
		Name:        "agent-conversation.json",
		ContentText: string(conversation),
		Status:      "ready",
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	snapshot := awaitAgentChatLiveSession(t, updates, 2*time.Second, func(item ChatSessionItem) bool {
		if len(item.Messages) == 0 {
			return false
		}
		last := item.Messages[len(item.Messages)-1]
		return last.ID == "msg_assistant" && last.Status == "running" && strings.Contains(last.Content, "I can see the diff now.")
	})
	last := snapshot.Messages[len(snapshot.Messages)-1]
	if !strings.Contains(last.Content, "I can see the diff now.") {
		t.Fatalf("live content = %q, want streamed assistant artifact text", last.Content)
	}

	run.Status = "completed"
	run.FinishedAt = time.Now().UTC()
	if _, err := taskStore.UpdateRun(ctx, run); err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("waitForHecateAgentRun returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waitForHecateAgentRun did not finish after run completion")
	}
}

func awaitAgentChatLiveSession(t *testing.T, updates <-chan AgentChatLiveEvent, timeout time.Duration, matches func(ChatSessionItem) bool) ChatSessionItem {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-updates:
			if !ok {
				t.Fatal("agent chat live channel closed before matching session update")
			}
			if event.Type != AgentChatLiveEventSessionUpdate || event.SessionUpdate == nil {
				continue
			}
			if matches(event.SessionUpdate.Data) {
				return event.SessionUpdate.Data
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for matching agent chat live session update")
		}
	}
}

func TestHecateChatCanSwitchBetweenModelAndToolsSegments(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-hecate-chat",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Now().UTC(),
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "Segment answer."},
				FinishReason: "stop",
			}},
			Usage: types.Usage{PromptTokens: 8, CompletionTokens: 3, TotalTokens: 11},
		},
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	handler := NewServer(logger, apiHandler)
	client := newTaskTestClient(t, handler)
	workspace := t.TempDir()

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","title":"Mixed chat","provider":"openai","model":"gpt-4o-mini"}`)
	if session.Data.AgentID != chat.DefaultAgentID || session.Data.TaskID != "" {
		t.Fatalf("created session = agent %q task %q, want hecate/no task", session.Data.AgentID, session.Data.TaskID)
	}

	modelTurn := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"openai","model":"gpt-4o-mini","content":"answer directly"}`)
	if len(modelTurn.Data.Messages) != 2 {
		t.Fatalf("model messages = %d, want 2", len(modelTurn.Data.Messages))
	}
	modelAssistant := modelTurn.Data.Messages[1]
	if modelAssistant.ExecutionMode != chat.ExecutionModeHecateTask || modelAssistant.TaskID != "" || modelAssistant.Model != "gpt-4o-mini" {
		t.Fatalf("model assistant snapshot = execution_mode %q task %q model %q", modelAssistant.ExecutionMode, modelAssistant.TaskID, modelAssistant.Model)
	}
	if modelAssistant.TurnKind != chat.TurnKindDirectModel {
		t.Fatalf("model assistant turn_kind = %q, want %q", modelAssistant.TurnKind, chat.TurnKindDirectModel)
	}
	if modelAssistant.ToolsEnabled {
		t.Errorf("model assistant tools_enabled = true, want false (hecate_task dispatch records tools-off)")
	}
	if !strings.Contains(modelAssistant.Content, "Segment answer") {
		t.Fatalf("model assistant content = %q", modelAssistant.Content)
	}

	toolsTurn := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		fmt.Sprintf(`{"execution_mode":"hecate_task","provider":"openai","model":"gpt-4o-mini","workspace":%q,"content":"use tools"}`, workspace))
	if toolsTurn.Data.TaskID == "" || toolsTurn.Data.LatestRunID == "" {
		t.Fatalf("tools turn missing task/run: %+v", toolsTurn.Data)
	}
	firstTaskID := toolsTurn.Data.TaskID
	toolsAssistant := toolsTurn.Data.Messages[len(toolsTurn.Data.Messages)-1]
	if toolsAssistant.ExecutionMode != chat.ExecutionModeHecateTask || toolsAssistant.TaskID != firstTaskID || toolsAssistant.SegmentID != "task:"+firstTaskID {
		t.Fatalf("tools assistant snapshot = execution_mode %q task %q segment %q", toolsAssistant.ExecutionMode, toolsAssistant.TaskID, toolsAssistant.SegmentID)
	}
	if toolsAssistant.TurnKind != chat.TurnKindHecateTask {
		t.Fatalf("tools assistant turn_kind = %q, want %q", toolsAssistant.TurnKind, chat.TurnKindHecateTask)
	}
	if !toolsAssistant.ToolsEnabled {
		t.Errorf("tools assistant tools_enabled = false, want true (hecate_task dispatch records tools-on)")
	}

	secondModel := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"openai","model":"gpt-4o-mini","content":"back to direct chat"}`)
	if secondModel.Data.TaskID != firstTaskID {
		t.Fatalf("model segment should preserve latest task pointer, got %q want %q", secondModel.Data.TaskID, firstTaskID)
	}
	secondModelAssistant := secondModel.Data.Messages[len(secondModel.Data.Messages)-1]
	if secondModelAssistant.ExecutionMode != chat.ExecutionModeHecateTask || secondModelAssistant.TaskID != "" {
		t.Fatalf("second model assistant snapshot = execution_mode %q task %q", secondModelAssistant.ExecutionMode, secondModelAssistant.TaskID)
	}
	if secondModelAssistant.TurnKind != chat.TurnKindDirectModel {
		t.Fatalf("second model assistant turn_kind = %q, want %q", secondModelAssistant.TurnKind, chat.TurnKindDirectModel)
	}
	if secondModelAssistant.ToolsEnabled {
		t.Fatalf("second model assistant tools_enabled = true, want false (direct-model turn)")
	}

	secondTools := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		fmt.Sprintf(`{"execution_mode":"hecate_task","provider":"openai","model":"gpt-4o-mini","workspace":%q,"content":"tools again"}`, workspace))
	if secondTools.Data.TaskID == "" || secondTools.Data.TaskID == firstTaskID {
		t.Fatalf("tools re-entry task_id = %q, want new task distinct from %q", secondTools.Data.TaskID, firstTaskID)
	}
	secondToolsAssistant := secondTools.Data.Messages[len(secondTools.Data.Messages)-1]
	if secondToolsAssistant.ExecutionMode != chat.ExecutionModeHecateTask || secondToolsAssistant.TaskID != secondTools.Data.TaskID || secondToolsAssistant.SegmentID != "task:"+secondTools.Data.TaskID {
		t.Fatalf("second tools assistant snapshot = execution_mode %q task %q segment %q", secondToolsAssistant.ExecutionMode, secondToolsAssistant.TaskID, secondToolsAssistant.SegmentID)
	}
	if secondToolsAssistant.TurnKind != chat.TurnKindHecateTask {
		t.Fatalf("second tools assistant turn_kind = %q, want %q", secondToolsAssistant.TurnKind, chat.TurnKindHecateTask)
	}

	changedModelTools := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		fmt.Sprintf(`{"execution_mode":"hecate_task","provider":"openai","model":"gpt-4o-mini-2024-07-18","workspace":%q,"content":"use a different model with tools"}`, workspace))
	if changedModelTools.Data.TaskID == "" || changedModelTools.Data.TaskID == secondTools.Data.TaskID {
		t.Fatalf("model-change task_id = %q, want new task distinct from %q", changedModelTools.Data.TaskID, secondTools.Data.TaskID)
	}
	changedModelAssistant := changedModelTools.Data.Messages[len(changedModelTools.Data.Messages)-1]
	if changedModelAssistant.ExecutionMode != chat.ExecutionModeHecateTask || changedModelAssistant.TaskID != changedModelTools.Data.TaskID || changedModelAssistant.Model != "gpt-4o-mini-2024-07-18" {
		t.Fatalf("model-change assistant snapshot = execution_mode %q task %q model %q", changedModelAssistant.ExecutionMode, changedModelAssistant.TaskID, changedModelAssistant.Model)
	}
	if changedModelAssistant.TurnKind != chat.TurnKindHecateTask {
		t.Fatalf("model-change assistant turn_kind = %q, want %q", changedModelAssistant.TurnKind, chat.TurnKindHecateTask)
	}
	changedTask := mustRequestJSON[TaskResponse](client, http.MethodGet, "/hecate/v1/tasks/"+changedModelTools.Data.TaskID, "")
	if changedTask.Data.RequestedModel != "gpt-4o-mini-2024-07-18" {
		t.Fatalf("model-change task requested_model = %q, want gpt-4o-mini-2024-07-18", changedTask.Data.RequestedModel)
	}

	segments := changedModelTools.Data.Segments
	if len(segments) != 5 {
		t.Fatalf("segments = %d, want 5: %+v", len(segments), segments)
	}
	// All segments persist as `hecate_task` now. The segment shape
	// (model vs tools turn) is recoverable from segment.ID prefix
	// (`segment:` vs `task:`) and the per-segment task_id population.
	for i, segment := range segments {
		if segment.ExecutionMode != chat.ExecutionModeHecateTask {
			t.Fatalf("segment %d execution_mode = %q, want hecate_task: %+v", i, segment.ExecutionMode, segments)
		}
		wantKind := []string{chat.TurnKindDirectModel, chat.TurnKindHecateTask, chat.TurnKindDirectModel, chat.TurnKindHecateTask, chat.TurnKindHecateTask}[i]
		if segment.TurnKind != wantKind {
			t.Fatalf("segment %d turn_kind = %q, want %q: %+v", i, segment.TurnKind, wantKind, segments)
		}
		if segment.MessageCount != 2 {
			t.Fatalf("segment %d message_count = %d, want 2: %+v", i, segment.MessageCount, segments)
		}
	}
	if segments[0].ID != modelAssistant.SegmentID || segments[0].TaskID != "" || segments[0].Model != "gpt-4o-mini" {
		t.Fatalf("first model segment = %+v, want segment %q with gpt-4o-mini and no task", segments[0], modelAssistant.SegmentID)
	}
	if segments[1].ID != "task:"+firstTaskID || segments[1].TaskID != firstTaskID || segments[1].LatestRunID == "" {
		t.Fatalf("first tools segment = %+v, want task %q with latest run", segments[1], firstTaskID)
	}
	if segments[4].ID != "task:"+changedModelTools.Data.TaskID || segments[4].TaskID != changedModelTools.Data.TaskID || segments[4].Model != "gpt-4o-mini-2024-07-18" {
		t.Fatalf("model-change tools segment = %+v, want task %q and changed model", segments[4], changedModelTools.Data.TaskID)
	}
}

func TestHecateAgentNewSegmentLivePlaceholderDoesNotBorrowPreviousTask(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-hecate-chat-live-segment",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Now().UTC(),
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "Segment answer."},
				FinishReason: "stop",
			}},
			Usage: types.Usage{PromptTokens: 8, CompletionTokens: 3, TotalTokens: 11},
		},
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	handler := NewServer(logger, apiHandler)
	client := newTaskTestClient(t, handler)
	workspace := t.TempDir()

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","title":"Mixed chat","provider":"openai","model":"gpt-4o-mini"}`)
	firstTools := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		fmt.Sprintf(`{"execution_mode":"hecate_task","provider":"openai","model":"gpt-4o-mini","workspace":%q,"content":"use tools"}`, workspace))
	firstTaskID := firstTools.Data.TaskID
	if firstTaskID == "" {
		t.Fatalf("first tools turn task_id is empty: %+v", firstTools.Data)
	}
	secondModel := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"openai","model":"gpt-4o-mini","content":"back to direct chat"}`)
	if secondModel.Data.TaskID != firstTaskID {
		t.Fatalf("direct model segment should preserve latest task pointer on the session, got %q want %q", secondModel.Data.TaskID, firstTaskID)
	}

	updates, unsubscribe := apiHandler.agentChatLive.subscribe(session.Data.ID)
	defer unsubscribe()
	type requestResult struct {
		status   int
		body     string
		response ChatSessionResponse
	}
	done := make(chan requestResult, 1)
	go func() {
		recorder := performRequest(t, handler, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
			fmt.Sprintf(`{"execution_mode":"hecate_task","provider":"openai","model":"gpt-4o-mini","workspace":%q,"content":"tools again"}`, workspace))
		payload, _ := tryDecodeRecorder[ChatSessionResponse](recorder)
		done <- requestResult{status: recorder.Code, body: recorder.Body.String(), response: payload}
	}()

	var result requestResult
	deadline := time.NewTimer(asyncWaitTimeout)
	defer deadline.Stop()
	for result.status == 0 {
		select {
		case event := <-updates:
			assertNoLiveMessageBorrowedTask(t, event, "tools again", firstTaskID)
		case result = <-done:
		case <-deadline.C:
			t.Fatal("timed out waiting for tools re-entry request")
		}
	}
	for {
		select {
		case event := <-updates:
			assertNoLiveMessageBorrowedTask(t, event, "tools again", firstTaskID)
		default:
			if result.status != http.StatusOK {
				t.Fatalf("tools re-entry status = %d, want 200, body=%s", result.status, result.body)
			}
			if result.response.Data.TaskID == "" || result.response.Data.TaskID == firstTaskID {
				t.Fatalf("tools re-entry task_id = %q, want new task distinct from %q", result.response.Data.TaskID, firstTaskID)
			}
			for _, message := range result.response.Data.Messages {
				if strings.Contains(message.Content, "tools again") && message.TaskID == firstTaskID {
					t.Fatalf("final response message borrowed previous task_id: %+v", message)
				}
			}
			return
		}
	}
}

func assertNoLiveMessageBorrowedTask(t *testing.T, event AgentChatLiveEvent, content, previousTaskID string) {
	t.Helper()
	if event.Type != AgentChatLiveEventSessionUpdate || event.SessionUpdate == nil {
		return
	}
	for _, message := range event.SessionUpdate.Data.Messages {
		if strings.Contains(message.Content, content) && message.TaskID == previousTaskID {
			t.Fatalf("live message %q borrowed previous task_id %q: %+v", content, previousTaskID, message)
		}
	}
}

func agentChatMessageHasActivity(message ChatMessageItem, activityType string) bool {
	for _, activity := range message.Activities {
		if activity.Type == activityType {
			return true
		}
	}
	return false
}

func TestChatActivityFromTaskActivityCarriesApprovalMetadata(t *testing.T) {
	item := TaskActivityItem{
		ID:          "approval:appr_123",
		Type:        "approval",
		Status:      "pending",
		Title:       "agent_loop_tool_call",
		ApprovalID:  "appr_123",
		Kind:        "agent_loop_tool_call",
		NeedsAction: true,
		OccurredAt:  "2026-05-03T10:00:00Z",
	}

	activity := agentChatActivityFromTaskActivity(item)
	rendered := renderAgentChatActivities([]chat.Activity{activity})
	if len(rendered) != 1 {
		t.Fatalf("rendered activities = %d, want 1", len(rendered))
	}
	if rendered[0].ApprovalID != "appr_123" || !rendered[0].NeedsAction {
		t.Fatalf("approval metadata = id %q needs_action %v, want appr_123/true", rendered[0].ApprovalID, rendered[0].NeedsAction)
	}
}

func TestChatActivityFromTaskActivityCarriesArtifactMetadata(t *testing.T) {
	item := TaskActivityItem{
		ID:         "artifact:art_stderr",
		Type:       "artifact",
		Status:     "ready",
		Title:      "git-stderr.txt",
		ArtifactID: "art_stderr",
		Kind:       "stderr",
		Summary: map[string]any{
			"size_bytes":      float64(42),
			"content_preview": "  fatal: not a git repository\n",
		},
		OccurredAt: "2026-05-03T10:00:00Z",
	}

	activity := agentChatActivityFromTaskActivity(item)
	rendered := renderAgentChatActivities([]chat.Activity{activity})
	if len(rendered) != 1 {
		t.Fatalf("rendered activities = %d, want 1", len(rendered))
	}
	if rendered[0].ArtifactID != "art_stderr" || rendered[0].ArtifactSizeBytes != 42 {
		t.Fatalf("artifact metadata = id %q size %d, want art_stderr/42", rendered[0].ArtifactID, rendered[0].ArtifactSizeBytes)
	}
	if rendered[0].ArtifactPreview != "  fatal: not a git repository" {
		t.Fatalf("artifact preview = %q", rendered[0].ArtifactPreview)
	}
}

func TestMergeChatActivityClearsApprovalNeedsAction(t *testing.T) {
	items := []chat.Activity{{
		ID:          "task:approval:appr_123",
		Type:        "approval",
		Status:      "pending",
		Title:       "agent_loop_tool_call",
		Detail:      "pending",
		ApprovalID:  "appr_123",
		NeedsAction: true,
	}}

	items = mergeChatActivity(items, chat.Activity{
		ID:          "task:approval:appr_123",
		Type:        "approval",
		Status:      "approved",
		Title:       "agent_loop_tool_call",
		Detail:      "approved",
		ApprovalID:  "appr_123",
		NeedsAction: false,
	})
	if len(items) != 1 {
		t.Fatalf("items = %d, want merged single item", len(items))
	}
	if items[0].Status != "approved" || items[0].NeedsAction {
		t.Fatalf("merged approval = status %q needs_action %v, want approved/false", items[0].Status, items[0].NeedsAction)
	}
}

func TestTaskActivityItemsCarryStepApprovalMetadata(t *testing.T) {
	items := buildTaskActivityItems([]TaskStepItem{{
		ID:         "step_1",
		Kind:       "approval",
		Status:     "awaiting_approval",
		Title:      "Awaiting approval - turn 1",
		ApprovalID: "appr_123",
		StartedAt:  "2026-05-03T10:00:00Z",
	}}, nil, []TaskApprovalItem{{
		ID:     "appr_123",
		Status: "pending",
	}}, types.TaskRun{Status: "awaiting_approval"})
	item := taskActivityByID(items, "step:step_1")
	if item.Type != "approval" || item.ApprovalID != "appr_123" || !item.NeedsAction {
		t.Fatalf("approval activity = type %q id %q needs_action %v, want approval/appr_123/true", item.Type, item.ApprovalID, item.NeedsAction)
	}
}

func TestTaskActivityItemsUseResolvedApprovalStatusForStep(t *testing.T) {
	items := buildTaskActivityItems([]TaskStepItem{{
		ID:         "step_1",
		Kind:       "approval",
		Status:     "awaiting_approval",
		Title:      "Awaiting approval - turn 1",
		ApprovalID: "appr_123",
		StartedAt:  "2026-05-03T10:00:00Z",
	}}, nil, []TaskApprovalItem{{
		ID:     "appr_123",
		Status: "approved",
	}}, types.TaskRun{Status: "running"})
	item := taskActivityByID(items, "step:step_1")
	if item.Status != "approved" || item.NeedsAction {
		t.Fatalf("approval activity = status %q needs_action %v, want approved/false", item.Status, item.NeedsAction)
	}
}

func TestTaskActivityItemsExposeRTKDebugSummary(t *testing.T) {
	items := buildTaskActivityItems([]TaskStepItem{{
		ID:        "step_shell",
		Kind:      "shell",
		Status:    "completed",
		Title:     "shell_exec",
		StartedAt: "2026-05-03T10:00:00Z",
		Input: map[string]any{
			telemetry.AttrHecateSandboxRTKEnabled: true,
			"argv":                                []any{"rtk", "sh", "-lc", "go test ./..."},
		},
	}}, nil, nil, types.TaskRun{Status: "running"})

	item := taskActivityByID(items, "step:step_shell")
	if item.Summary[telemetry.AttrHecateSandboxRTKEnabled] != true {
		t.Fatalf("rtk summary = %#v, want true", item.Summary[telemetry.AttrHecateSandboxRTKEnabled])
	}
	activity := agentChatActivityFromTaskActivity(item)
	if !strings.Contains(activity.Detail, "via RTK") || !strings.Contains(activity.Detail, "rtk sh -lc go test ./...") {
		t.Fatalf("activity detail = %q, want RTK argv", activity.Detail)
	}
}

func TestTaskActivityItemsIncludeOutputArtifactPreview(t *testing.T) {
	items := buildTaskActivityItems(nil, []TaskArtifactItem{{
		ID:          "art_stdout",
		Kind:        "stdout",
		Name:        "git-stdout.txt",
		ContentText: "diff --git a/README.md b/README.md\n+hello\n",
		SizeBytes:   42,
		Status:      "ready",
		CreatedAt:   "2026-05-03T10:00:00Z",
	}}, nil, types.TaskRun{Status: "failed"})

	item := taskActivityByID(items, "artifact:art_stdout")
	preview, _ := item.Summary["content_preview"].(string)
	if !strings.Contains(preview, "+hello") {
		t.Fatalf("content_preview = %q, want stdout preview", preview)
	}
}

func TestTaskActivityArtifactPreviewPreservesLeadingWhitespaceAndCapsBytes(t *testing.T) {
	content := "  indented output\n" + strings.Repeat("λ", taskActivityArtifactPreviewMaxBytes)
	preview := taskActivityArtifactContentPreview(TaskArtifactItem{
		Kind:        "stderr",
		ContentText: content,
	})

	if !strings.HasPrefix(preview, "  indented output") {
		t.Fatalf("preview = %q, want leading whitespace preserved", preview[:min(len(preview), 40)])
	}
	if !strings.HasSuffix(preview, taskActivityArtifactPreviewTruncatedSuffix) {
		t.Fatalf("preview missing truncation suffix")
	}
	if len(preview) > taskActivityArtifactPreviewMaxBytes {
		t.Fatalf("preview length = %d, want <= %d", len(preview), taskActivityArtifactPreviewMaxBytes)
	}
	if !utf8.ValidString(preview) {
		t.Fatalf("preview is not valid UTF-8")
	}
}

func taskActivityByID(items []TaskActivityItem, id string) TaskActivityItem {
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	return TaskActivityItem{}
}

func TestHecateAgentCommandOutputPromotesGitStdout(t *testing.T) {
	store := taskstate.NewMemoryStore()
	handler := &Handler{taskStore: store}
	now := time.Now().UTC()
	_, err := store.CreateArtifact(context.Background(), types.TaskArtifact{
		ID:          "art_stdout",
		TaskID:      "task_1",
		RunID:       "run_1",
		Kind:        "stdout",
		Name:        "git-stdout.txt",
		ContentText: "diff --git a/README.md b/README.md\n+hello\n",
		Status:      "ready",
		CreatedAt:   now,
	})
	if err != nil {
		t.Fatalf("CreateArtifact(stdout): %v", err)
	}
	_, err = store.CreateArtifact(context.Background(), types.TaskArtifact{
		ID:          "art_stderr",
		TaskID:      "task_1",
		RunID:       "run_1",
		Kind:        "stderr",
		Name:        "git-stderr.txt",
		ContentText: "",
		Status:      "ready",
		CreatedAt:   now,
	})
	if err != nil {
		t.Fatalf("CreateArtifact(stderr): %v", err)
	}

	output := handler.finalHecateAgentCommandOutput(context.Background(), "task_1", "run_1")
	if !strings.Contains(output, "Command output") || !strings.Contains(output, "```diff") || !strings.Contains(output, "+hello") {
		t.Fatalf("command output not promoted as diff block:\n%s", output)
	}
}

func TestHecateAgentFinalAnswerFallsBackToSummaryArtifact(t *testing.T) {
	store := taskstate.NewMemoryStore()
	handler := &Handler{taskStore: store}
	now := time.Now().UTC()
	_, err := store.CreateArtifact(context.Background(), types.TaskArtifact{
		ID:          "art_final",
		TaskID:      "task_1",
		RunID:       "run_1",
		Kind:        "summary",
		Name:        "agent-final-answer.txt",
		ContentText: "The current diff updates the chat UI.",
		Status:      "ready",
		CreatedAt:   now,
	})
	if err != nil {
		t.Fatalf("CreateArtifact(summary): %v", err)
	}

	output := handler.finalHecateAgentAnswer(context.Background(), "task_1", "run_1")
	if output != "The current diff updates the chat UI." {
		t.Fatalf("final answer = %q", output)
	}
}

func TestMergeHecateAgentAnswerReplacesCommandIntro(t *testing.T) {
	merged := mergeHecateAgentAnswerWithCommandOutput(
		"Since you want to see the diff, I'll run `git diff` for you:",
		"Command output:\n\n```diff\n+hello\n```",
	)
	if strings.Contains(merged, "I'll run") {
		t.Fatalf("command intro was not replaced:\n%s", merged)
	}
	if !strings.Contains(merged, "+hello") {
		t.Fatalf("command output missing:\n%s", merged)
	}
}

func TestHecateAgentChatFallsBackToDirectModelWhenToolsUnavailable(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cpStore := controlplane.NewMemoryStore()
	provider := &fakeProvider{
		name: "ollama",
		capabilities: providers.Capabilities{
			Name:         "ollama",
			Kind:         providers.KindLocal,
			DefaultModel: "llama3.1:8b",
			Models:       []string{"llama3.1:8b"},
			ModelCapabilities: map[string]types.ModelCapabilities{
				"llama3.1:8b": {
					ToolCalling: modelcaps.ToolCallingNone,
					Streaming:   true,
					Source:      modelcaps.SourceProvider,
				},
			},
		},
		response: &types.ChatResponse{
			ID:        "chatcmpl-direct",
			Model:     "llama3.1:8b",
			CreatedAt: time.Now().UTC(),
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "plain answer"},
				FinishReason: "stop",
			}},
			Usage: types.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
		},
	}
	handler := newTestHTTPHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, cpStore)
	client := newTaskTestClient(t, handler)
	workspace := t.TempDir()

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		fmt.Sprintf(`{"agent_id":"hecate","workspace":%q,"provider":"ollama","model":"llama3.1:8b"}`, workspace))
	if session.Data.Capabilities.ToolCalling != modelcaps.ToolCallingNone {
		t.Fatalf("session capabilities = %+v, want none", session.Data.Capabilities)
	}

	updated := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","content":"use tools"}`)
	if len(updated.Data.Messages) < 2 {
		t.Fatalf("messages = %d, want at least 2", len(updated.Data.Messages))
	}
	assistant := updated.Data.Messages[len(updated.Data.Messages)-1]
	// Post-unification: every Hecate-side turn persists as
	// `hecate_task` regardless of tools state. The capability
	// downgrade flips `ToolsEnabled` to false but the
	// execution_mode stays consistent across the chat session.
	if assistant.ExecutionMode != chat.ExecutionModeHecateTask {
		t.Fatalf("assistant execution mode = %q, want hecate_task", assistant.ExecutionMode)
	}
	if assistant.ToolsEnabled {
		t.Fatalf("assistant tools_enabled = true, want false (capability downgrade)")
	}
	if assistant.TaskID != "" {
		t.Fatalf("assistant task id = %q, want empty", assistant.TaskID)
	}
	if assistant.Content != "plain answer" {
		t.Fatalf("assistant content = %q, want plain answer", assistant.Content)
	}
	if req := provider.LastRequest(); len(req.Tools) != 0 {
		t.Fatalf("provider request tools = %d, want 0", len(req.Tools))
	}
}

func TestHecateChatRejectsUnknownExecutionMode(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		capabilities: providers.Capabilities{
			Name:         "openai",
			Kind:         providers.KindCloud,
			DefaultModel: "gpt-4o-mini",
			Models:       []string{"gpt-4o-mini"},
		},
	}
	handler := newTestHTTPHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"openai","model":"gpt-4o-mini"}`)
	recorder := client.mustRequestStatus(http.StatusBadRequest, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"legacy_kind","content":"hello"}`)
	payload := decodeRecorder[struct {
		Error struct {
			Type           string `json:"type"`
			UserMessage    string `json:"user_message"`
			OperatorAction string `json:"operator_action"`
		} `json:"error"`
	}](t, recorder)
	if payload.Error.Type != errCodeExecutionModeInvalid {
		t.Fatalf("error type = %q, want %s", payload.Error.Type, errCodeExecutionModeInvalid)
	}
	if payload.Error.UserMessage == "" || payload.Error.OperatorAction == "" {
		t.Fatalf("error missing operator metadata: %+v", payload.Error)
	}
}

func TestExternalAgentChatRejectsDirectModelExecutionMode(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-external-direct-rejected",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Now().UTC(),
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "should not be appended"},
				FinishReason: "stop",
			}},
		},
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	apiHandler.SetAgentChatRunner(&fakeAgentChatRunner{nativeSessionID: "native_codex_direct_rejected"})
	handler := NewServer(logger, apiHandler)
	client := newTaskTestClient(t, handler)
	workspace := t.TempDir()

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		fmt.Sprintf(`{"agent_id":"codex","workspace":%q}`, workspace))
	recorder := client.mustRequestStatus(http.StatusBadRequest, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"provider":"openai","model":"gpt-4o-mini","content":"answer directly"}`)
	payload := decodeRecorder[struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}](t, recorder)
	if payload.Error.Type != errCodeRuntimeMismatch {
		t.Fatalf("error type = %q, want %s", payload.Error.Type, errCodeRuntimeMismatch)
	}
	if !strings.Contains(payload.Error.Message, "external agent sessions cannot run Hecate Chat turns") {
		t.Fatalf("error message = %q", payload.Error.Message)
	}
}

func TestHecateAgentChatRejectsBusyBackingRun(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, controlplane.NewMemoryStore(), nil, nil)
	server := NewServer(logger, apiHandler)
	client := newTaskTestClient(t, server)
	ctx := context.Background()
	now := time.Now().UTC()

	task, err := apiHandler.taskStore.CreateTask(ctx, types.Task{
		ID:            "task_busy",
		Title:         "Busy chat",
		ExecutionKind: "agent_loop",
		Status:        "running",
		CreatedAt:     now,
		UpdatedAt:     now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run, err := apiHandler.taskStore.CreateRun(ctx, types.TaskRun{
		ID:        "run_busy",
		TaskID:    task.ID,
		Status:    "running",
		StartedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	session, err := apiHandler.agentChat.Create(ctx, chat.Session{
		ID:              "chat_busy",
		Title:           "Busy",
		AgentID:         chat.DefaultAgentID,
		Workspace:       t.TempDir(),
		TaskID:          task.ID,
		LatestRunID:     run.ID,
		Provider:        "openai",
		Model:           "gpt-4o-mini",
		Capabilities:    types.ModelCapabilities{ToolCalling: modelcaps.ToolCallingParallel, Streaming: true, Source: modelcaps.SourceCatalog},
		WorkspaceBranch: "",
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	recorder := client.mustRequestStatus(http.StatusConflict, http.MethodPost, "/hecate/v1/chat/sessions/"+session.ID+"/messages",
		`{"content":"new turn"}`)
	var payload struct {
		Error struct {
			Type           string `json:"type"`
			Message        string `json:"message"`
			UserMessage    string `json:"user_message"`
			OperatorAction string `json:"operator_action"`
			TaskID         string `json:"task_id"`
			LatestRunID    string `json:"latest_run_id"`
			RunStatus      string `json:"run_status"`
		} `json:"error"`
	}
	payload = decodeRecorder[struct {
		Error struct {
			Type           string `json:"type"`
			Message        string `json:"message"`
			UserMessage    string `json:"user_message"`
			OperatorAction string `json:"operator_action"`
			TaskID         string `json:"task_id"`
			LatestRunID    string `json:"latest_run_id"`
			RunStatus      string `json:"run_status"`
		} `json:"error"`
	}](t, recorder)
	if payload.Error.Type != errCodeAgentSessionBusy {
		t.Fatalf("error type = %q, want %s", payload.Error.Type, errCodeAgentSessionBusy)
	}
	if !strings.Contains(payload.Error.Message, "still working on the current task") {
		t.Fatalf("message = %q", payload.Error.Message)
	}
	if payload.Error.UserMessage == "" || payload.Error.OperatorAction == "" {
		t.Fatalf("operator metadata missing from busy payload: %+v", payload.Error)
	}
	if payload.Error.TaskID != task.ID || payload.Error.LatestRunID != run.ID || payload.Error.RunStatus != "running" {
		t.Fatalf("busy payload = %+v", payload.Error)
	}
}

func TestHecateChatRejectsDirectModelTurnWhileBackingRunBusy(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, controlplane.NewMemoryStore(), nil, nil)
	server := NewServer(logger, apiHandler)
	client := newTaskTestClient(t, server)
	ctx := context.Background()
	now := time.Now().UTC()

	task, err := apiHandler.taskStore.CreateTask(ctx, types.Task{
		ID:            "task_busy_model_turn",
		Title:         "Busy chat",
		ExecutionKind: "agent_loop",
		Status:        "running",
		CreatedAt:     now,
		UpdatedAt:     now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run, err := apiHandler.taskStore.CreateRun(ctx, types.TaskRun{
		ID:        "run_busy_model_turn",
		TaskID:    task.ID,
		Status:    "awaiting_approval",
		StartedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	session, err := apiHandler.agentChat.Create(ctx, chat.Session{
		ID:           "chat_busy_model_turn",
		Title:        "Mixed busy",
		AgentID:      chat.DefaultAgentID,
		Workspace:    t.TempDir(),
		TaskID:       task.ID,
		LatestRunID:  run.ID,
		Provider:     "openai",
		Model:        "gpt-4o-mini",
		Capabilities: types.ModelCapabilities{ToolCalling: modelcaps.ToolCallingParallel, Streaming: true, Source: modelcaps.SourceCatalog},
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	recorder := client.mustRequestStatus(http.StatusConflict, http.MethodPost, "/hecate/v1/chat/sessions/"+session.ID+"/messages",
		`{"execution_mode":"hecate_task","tools_enabled":false,"content":"answer directly","model":"gpt-4o-mini"}`)
	payload := decodeRecorder[struct {
		Error struct {
			Type        string `json:"type"`
			Message     string `json:"message"`
			TaskID      string `json:"task_id"`
			LatestRunID string `json:"latest_run_id"`
			RunStatus   string `json:"run_status"`
		} `json:"error"`
	}](t, recorder)
	if payload.Error.Type != errCodeAgentSessionBusy {
		t.Fatalf("error type = %q, want %s", payload.Error.Type, errCodeAgentSessionBusy)
	}
	if payload.Error.TaskID != task.ID || payload.Error.LatestRunID != run.ID || payload.Error.RunStatus != "awaiting_approval" {
		t.Fatalf("busy payload = %+v", payload.Error)
	}
	if !strings.Contains(payload.Error.Message, "still working on the current task") {
		t.Fatalf("message = %q", payload.Error.Message)
	}
}
