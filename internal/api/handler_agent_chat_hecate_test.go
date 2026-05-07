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

	"github.com/hecate/agent-runtime/internal/agentchat"
	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/controlplane"
	"github.com/hecate/agent-runtime/internal/modelcaps"
	"github.com/hecate/agent-runtime/internal/providers"
	"github.com/hecate/agent-runtime/internal/taskstate"
	"github.com/hecate/agent-runtime/pkg/types"
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
				Message:      types.Message{Role: "assistant", Content: "Hecate Agent final answer."},
				FinishReason: "stop",
			}},
			Usage: types.Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14},
		},
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	handler := NewServer(logger, apiHandler)
	client := newTaskTestClient(t, handler)
	workspace := t.TempDir()

	session := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/hecate/v1/agent-chat/sessions",
		fmt.Sprintf(`{"runtime_kind":"agent","title":"Refactor chat","workspace":%q,"provider":"openai","model":"gpt-4o-mini"}`, workspace))
	if session.Data.RuntimeKind != "agent" {
		t.Fatalf("runtime_kind = %q, want agent", session.Data.RuntimeKind)
	}
	if session.Data.Capabilities.ToolCalling != modelcaps.ToolCallingParallel {
		t.Fatalf("capabilities = %+v, want parallel catalog capabilities", session.Data.Capabilities)
	}

	first := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/hecate/v1/agent-chat/sessions/"+session.Data.ID+"/messages",
		`{"content":"inspect the repo"}`)
	if first.Data.TaskID == "" || first.Data.LatestRunID == "" {
		t.Fatalf("first response missing task/run linkage: %+v", first.Data)
	}
	if first.Data.Status != "completed" {
		t.Fatalf("first status = %q, want completed", first.Data.Status)
	}
	if len(first.Data.Messages) < 2 || !strings.Contains(first.Data.Messages[len(first.Data.Messages)-1].Content, "Hecate Agent final answer") {
		t.Fatalf("first transcript = %+v", first.Data.Messages)
	}
	assistant := first.Data.Messages[len(first.Data.Messages)-1]
	if assistant.RuntimeKind != "agent" || assistant.TaskID != first.Data.TaskID || assistant.SegmentID != "task:"+first.Data.TaskID {
		t.Fatalf("assistant message runtime snapshot = runtime %q segment %q task %q", assistant.RuntimeKind, assistant.SegmentID, assistant.TaskID)
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
		t.Fatalf("assistant timing = %+v, want persisted Hecate Agent run timing", assistant.Timing)
	}

	task := mustRequestJSON[TaskResponse](client, http.MethodGet, "/hecate/v1/tasks/"+first.Data.TaskID, "")
	if task.Data.ExecutionKind != "agent_loop" || task.Data.ExecutionProfile != "chat_agent" {
		t.Fatalf("task execution fields = kind %q profile %q", task.Data.ExecutionKind, task.Data.ExecutionProfile)
	}
	if task.Data.OriginKind != "agent_chat" || task.Data.OriginID != session.Data.ID {
		t.Fatalf("task origin = %q/%q, want agent_chat/%s", task.Data.OriginKind, task.Data.OriginID, session.Data.ID)
	}

	second := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/hecate/v1/agent-chat/sessions/"+session.Data.ID+"/messages",
		`{"content":"continue from there"}`)
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
	chatStore := agentchat.NewMemoryStore()
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
	session, err := chatStore.Create(ctx, agentchat.Session{
		ID:          "chat_live",
		Title:       "Live chat",
		RuntimeKind: "agent",
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
	if _, err := chatStore.AppendMessage(ctx, session.ID, agentchat.Message{
		ID:          "msg_assistant",
		RuntimeKind: "agent",
		SegmentID:   "task:" + task.ID,
		TaskID:      task.ID,
		RunID:       run.ID,
		Role:        "assistant",
		Status:      "running",
		Content:     "",
		CreatedAt:   now,
		StartedAt:   now,
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

	snapshot := awaitAgentChatLiveSession(t, updates, 2*time.Second, func(item AgentChatSessionItem) bool {
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

func awaitAgentChatLiveSession(t *testing.T, updates <-chan AgentChatLiveEvent, timeout time.Duration, matches func(AgentChatSessionItem) bool) AgentChatSessionItem {
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

	session := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/hecate/v1/agent-chat/sessions",
		`{"runtime_kind":"model","title":"Mixed chat","provider":"openai","model":"gpt-4o-mini"}`)
	if session.Data.RuntimeKind != "model" || session.Data.TaskID != "" {
		t.Fatalf("created session = runtime %q task %q, want model/no task", session.Data.RuntimeKind, session.Data.TaskID)
	}

	modelTurn := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/hecate/v1/agent-chat/sessions/"+session.Data.ID+"/messages",
		`{"runtime_kind":"model","provider":"openai","model":"gpt-4o-mini","content":"answer directly"}`)
	if len(modelTurn.Data.Messages) != 2 {
		t.Fatalf("model messages = %d, want 2", len(modelTurn.Data.Messages))
	}
	modelAssistant := modelTurn.Data.Messages[1]
	if modelAssistant.RuntimeKind != "model" || modelAssistant.TaskID != "" || modelAssistant.Model != "gpt-4o-mini" {
		t.Fatalf("model assistant snapshot = runtime %q task %q model %q", modelAssistant.RuntimeKind, modelAssistant.TaskID, modelAssistant.Model)
	}
	if !strings.Contains(modelAssistant.Content, "Segment answer") {
		t.Fatalf("model assistant content = %q", modelAssistant.Content)
	}

	toolsTurn := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/hecate/v1/agent-chat/sessions/"+session.Data.ID+"/messages",
		fmt.Sprintf(`{"runtime_kind":"agent","provider":"openai","model":"gpt-4o-mini","workspace":%q,"content":"use tools"}`, workspace))
	if toolsTurn.Data.TaskID == "" || toolsTurn.Data.LatestRunID == "" {
		t.Fatalf("tools turn missing task/run: %+v", toolsTurn.Data)
	}
	firstTaskID := toolsTurn.Data.TaskID
	toolsAssistant := toolsTurn.Data.Messages[len(toolsTurn.Data.Messages)-1]
	if toolsAssistant.RuntimeKind != "agent" || toolsAssistant.TaskID != firstTaskID || toolsAssistant.SegmentID != "task:"+firstTaskID {
		t.Fatalf("tools assistant snapshot = runtime %q task %q segment %q", toolsAssistant.RuntimeKind, toolsAssistant.TaskID, toolsAssistant.SegmentID)
	}

	secondModel := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/hecate/v1/agent-chat/sessions/"+session.Data.ID+"/messages",
		`{"runtime_kind":"model","provider":"openai","model":"gpt-4o-mini","content":"back to direct chat"}`)
	if secondModel.Data.TaskID != firstTaskID {
		t.Fatalf("model segment should preserve latest task pointer, got %q want %q", secondModel.Data.TaskID, firstTaskID)
	}
	secondModelAssistant := secondModel.Data.Messages[len(secondModel.Data.Messages)-1]
	if secondModelAssistant.RuntimeKind != "model" || secondModelAssistant.TaskID != "" {
		t.Fatalf("second model assistant snapshot = runtime %q task %q", secondModelAssistant.RuntimeKind, secondModelAssistant.TaskID)
	}

	secondTools := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/hecate/v1/agent-chat/sessions/"+session.Data.ID+"/messages",
		fmt.Sprintf(`{"runtime_kind":"agent","provider":"openai","model":"gpt-4o-mini","workspace":%q,"content":"tools again"}`, workspace))
	if secondTools.Data.TaskID == "" || secondTools.Data.TaskID == firstTaskID {
		t.Fatalf("tools re-entry task_id = %q, want new task distinct from %q", secondTools.Data.TaskID, firstTaskID)
	}
	secondToolsAssistant := secondTools.Data.Messages[len(secondTools.Data.Messages)-1]
	if secondToolsAssistant.RuntimeKind != "agent" || secondToolsAssistant.TaskID != secondTools.Data.TaskID || secondToolsAssistant.SegmentID != "task:"+secondTools.Data.TaskID {
		t.Fatalf("second tools assistant snapshot = runtime %q task %q segment %q", secondToolsAssistant.RuntimeKind, secondToolsAssistant.TaskID, secondToolsAssistant.SegmentID)
	}

	changedModelTools := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/hecate/v1/agent-chat/sessions/"+session.Data.ID+"/messages",
		fmt.Sprintf(`{"runtime_kind":"agent","provider":"openai","model":"gpt-4o-mini-2024-07-18","workspace":%q,"content":"use a different model with tools"}`, workspace))
	if changedModelTools.Data.TaskID == "" || changedModelTools.Data.TaskID == secondTools.Data.TaskID {
		t.Fatalf("model-change task_id = %q, want new task distinct from %q", changedModelTools.Data.TaskID, secondTools.Data.TaskID)
	}
	changedModelAssistant := changedModelTools.Data.Messages[len(changedModelTools.Data.Messages)-1]
	if changedModelAssistant.RuntimeKind != "agent" || changedModelAssistant.TaskID != changedModelTools.Data.TaskID || changedModelAssistant.Model != "gpt-4o-mini-2024-07-18" {
		t.Fatalf("model-change assistant snapshot = runtime %q task %q model %q", changedModelAssistant.RuntimeKind, changedModelAssistant.TaskID, changedModelAssistant.Model)
	}
	changedTask := mustRequestJSON[TaskResponse](client, http.MethodGet, "/hecate/v1/tasks/"+changedModelTools.Data.TaskID, "")
	if changedTask.Data.RequestedModel != "gpt-4o-mini-2024-07-18" {
		t.Fatalf("model-change task requested_model = %q, want gpt-4o-mini-2024-07-18", changedTask.Data.RequestedModel)
	}

	segments := changedModelTools.Data.Segments
	if len(segments) != 5 {
		t.Fatalf("segments = %d, want 5: %+v", len(segments), segments)
	}
	wantKinds := []string{"model", "agent", "model", "agent", "agent"}
	for i, want := range wantKinds {
		if segments[i].RuntimeKind != want {
			t.Fatalf("segment %d runtime_kind = %q, want %q: %+v", i, segments[i].RuntimeKind, want, segments)
		}
		if segments[i].MessageCount != 2 {
			t.Fatalf("segment %d message_count = %d, want 2: %+v", i, segments[i].MessageCount, segments)
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

	session := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/hecate/v1/agent-chat/sessions",
		`{"runtime_kind":"model","title":"Mixed chat","provider":"openai","model":"gpt-4o-mini"}`)
	firstTools := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/hecate/v1/agent-chat/sessions/"+session.Data.ID+"/messages",
		fmt.Sprintf(`{"runtime_kind":"agent","provider":"openai","model":"gpt-4o-mini","workspace":%q,"content":"use tools"}`, workspace))
	firstTaskID := firstTools.Data.TaskID
	if firstTaskID == "" {
		t.Fatalf("first tools turn task_id is empty: %+v", firstTools.Data)
	}
	secondModel := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/hecate/v1/agent-chat/sessions/"+session.Data.ID+"/messages",
		`{"runtime_kind":"model","provider":"openai","model":"gpt-4o-mini","content":"back to direct chat"}`)
	if secondModel.Data.TaskID != firstTaskID {
		t.Fatalf("direct model segment should preserve latest task pointer on the session, got %q want %q", secondModel.Data.TaskID, firstTaskID)
	}

	updates, unsubscribe := apiHandler.agentChatLive.subscribe(session.Data.ID)
	defer unsubscribe()
	type requestResult struct {
		status   int
		body     string
		response AgentChatSessionResponse
	}
	done := make(chan requestResult, 1)
	go func() {
		recorder := performRequest(t, handler, http.MethodPost, "/hecate/v1/agent-chat/sessions/"+session.Data.ID+"/messages",
			fmt.Sprintf(`{"runtime_kind":"agent","provider":"openai","model":"gpt-4o-mini","workspace":%q,"content":"tools again"}`, workspace))
		payload, _ := tryDecodeRecorder[AgentChatSessionResponse](recorder)
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

func agentChatMessageHasActivity(message AgentChatMessageItem, activityType string) bool {
	for _, activity := range message.Activities {
		if activity.Type == activityType {
			return true
		}
	}
	return false
}

func TestAgentChatActivityFromTaskActivityCarriesApprovalMetadata(t *testing.T) {
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
	rendered := renderAgentChatActivities([]agentchat.Activity{activity})
	if len(rendered) != 1 {
		t.Fatalf("rendered activities = %d, want 1", len(rendered))
	}
	if rendered[0].ApprovalID != "appr_123" || !rendered[0].NeedsAction {
		t.Fatalf("approval metadata = id %q needs_action %v, want appr_123/true", rendered[0].ApprovalID, rendered[0].NeedsAction)
	}
}

func TestAgentChatActivityFromTaskActivityCarriesArtifactMetadata(t *testing.T) {
	item := TaskActivityItem{
		ID:         "artifact:art_stderr",
		Type:       "artifact",
		Status:     "ready",
		Title:      "git-stderr.txt",
		ArtifactID: "art_stderr",
		Kind:       "stderr",
		Summary: map[string]any{
			"size_bytes": float64(42),
		},
		OccurredAt: "2026-05-03T10:00:00Z",
	}

	activity := agentChatActivityFromTaskActivity(item)
	rendered := renderAgentChatActivities([]agentchat.Activity{activity})
	if len(rendered) != 1 {
		t.Fatalf("rendered activities = %d, want 1", len(rendered))
	}
	if rendered[0].ArtifactID != "art_stderr" || rendered[0].ArtifactSizeBytes != 42 {
		t.Fatalf("artifact metadata = id %q size %d, want art_stderr/42", rendered[0].ArtifactID, rendered[0].ArtifactSizeBytes)
	}
}

func TestMergeAgentChatActivityClearsApprovalNeedsAction(t *testing.T) {
	items := []agentchat.Activity{{
		ID:          "task:approval:appr_123",
		Type:        "approval",
		Status:      "pending",
		Title:       "agent_loop_tool_call",
		Detail:      "pending",
		ApprovalID:  "appr_123",
		NeedsAction: true,
	}}

	items = mergeAgentChatActivity(items, agentchat.Activity{
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

func TestHecateAgentChatRejectsDisabledToolCapability(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cpStore := controlplane.NewMemoryStore()
	if _, err := cpStore.UpsertModelCapabilityOverride(context.Background(), controlplane.ModelCapabilityRecord{
		Provider:    "ollama",
		Model:       "llama3.1:8b",
		ToolCalling: modelcaps.ToolCallingNone,
	}); err != nil {
		t.Fatalf("UpsertModelCapabilityOverride: %v", err)
	}
	provider := &fakeProvider{
		name: "ollama",
		capabilities: providers.Capabilities{
			Name:         "ollama",
			Kind:         providers.KindLocal,
			DefaultModel: "llama3.1:8b",
			Models:       []string{"llama3.1:8b"},
		},
	}
	handler := newTestHTTPHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, cpStore)
	client := newTaskTestClient(t, handler)
	workspace := t.TempDir()

	session := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/hecate/v1/agent-chat/sessions",
		fmt.Sprintf(`{"runtime_kind":"agent","workspace":%q,"provider":"ollama","model":"llama3.1:8b"}`, workspace))
	if session.Data.Capabilities.ToolCalling != modelcaps.ToolCallingNone {
		t.Fatalf("session capabilities = %+v, want none", session.Data.Capabilities)
	}

	recorder := client.mustRequestStatus(http.StatusUnprocessableEntity, http.MethodPost, "/hecate/v1/agent-chat/sessions/"+session.Data.ID+"/messages",
		`{"content":"use tools"}`)
	var payload struct {
		Error struct {
			Type         string                  `json:"type"`
			Message      string                  `json:"message"`
			Provider     string                  `json:"provider"`
			Model        string                  `json:"model"`
			Capabilities types.ModelCapabilities `json:"capabilities"`
		} `json:"error"`
	}
	payload = decodeRecorder[struct {
		Error struct {
			Type         string                  `json:"type"`
			Message      string                  `json:"message"`
			Provider     string                  `json:"provider"`
			Model        string                  `json:"model"`
			Capabilities types.ModelCapabilities `json:"capabilities"`
		} `json:"error"`
	}](t, recorder)
	if payload.Error.Type != errCodeModelCapability {
		t.Fatalf("error type = %q, want %s", payload.Error.Type, errCodeModelCapability)
	}
	if !strings.Contains(payload.Error.Message, "Tools are disabled for this model") {
		t.Fatalf("message = %q", payload.Error.Message)
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
	session, err := apiHandler.agentChat.Create(ctx, agentchat.Session{
		ID:              "agent_chat_busy",
		Title:           "Busy",
		RuntimeKind:     "agent",
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

	recorder := client.mustRequestStatus(http.StatusConflict, http.MethodPost, "/hecate/v1/agent-chat/sessions/"+session.ID+"/messages",
		`{"content":"new turn"}`)
	var payload struct {
		Error struct {
			Type        string `json:"type"`
			Message     string `json:"message"`
			TaskID      string `json:"task_id"`
			LatestRunID string `json:"latest_run_id"`
			RunStatus   string `json:"run_status"`
		} `json:"error"`
	}
	payload = decodeRecorder[struct {
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
	if !strings.Contains(payload.Error.Message, "still working on the current task") {
		t.Fatalf("message = %q", payload.Error.Message)
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
	session, err := apiHandler.agentChat.Create(ctx, agentchat.Session{
		ID:           "agent_chat_busy_model_turn",
		Title:        "Mixed busy",
		RuntimeKind:  "model",
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

	recorder := client.mustRequestStatus(http.StatusConflict, http.MethodPost, "/hecate/v1/agent-chat/sessions/"+session.ID+"/messages",
		`{"runtime_kind":"model","content":"answer directly","model":"gpt-4o-mini"}`)
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
