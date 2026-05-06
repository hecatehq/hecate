package api

import (
	"context"
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
	handler := newTestHTTPHandlerWithControlPlane(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	client := newTaskTestClient(t, handler)
	workspace := t.TempDir()

	session := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions",
		fmt.Sprintf(`{"runtime_kind":"hecate_agent","title":"Refactor chat","workspace":%q,"provider":"openai","model":"gpt-4o-mini"}`, workspace))
	if session.Data.RuntimeKind != "hecate_agent" {
		t.Fatalf("runtime_kind = %q, want hecate_agent", session.Data.RuntimeKind)
	}
	if session.Data.Capabilities.ToolCalling != modelcaps.ToolCallingParallel {
		t.Fatalf("capabilities = %+v, want parallel catalog capabilities", session.Data.Capabilities)
	}

	first := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions/"+session.Data.ID+"/messages",
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
	if assistant.RuntimeKind != "hecate_agent" || assistant.TaskID != first.Data.TaskID || assistant.SegmentID != "task:"+first.Data.TaskID {
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

	task := mustRequestJSON[TaskResponse](client, http.MethodGet, "/v1/tasks/"+first.Data.TaskID, "")
	if task.Data.ExecutionKind != "agent_loop" || task.Data.ExecutionProfile != "chat_hecate_agent" {
		t.Fatalf("task execution fields = kind %q profile %q", task.Data.ExecutionKind, task.Data.ExecutionProfile)
	}
	if task.Data.OriginKind != "agent_chat" || task.Data.OriginID != session.Data.ID {
		t.Fatalf("task origin = %q/%q, want agent_chat/%s", task.Data.OriginKind, task.Data.OriginID, session.Data.ID)
	}

	second := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions/"+session.Data.ID+"/messages",
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
	runs := mustRequestJSON[TaskRunsResponse](client, http.MethodGet, "/v1/tasks/"+first.Data.TaskID+"/runs", "")
	if len(runs.Data) != 2 {
		t.Fatalf("runs = %d, want 2 continued runs: %+v", len(runs.Data), runs.Data)
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
	handler := newTestHTTPHandlerWithControlPlane(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	client := newTaskTestClient(t, handler)
	workspace := t.TempDir()

	session := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions",
		`{"runtime_kind":"model","title":"Mixed chat","provider":"openai","model":"gpt-4o-mini"}`)
	if session.Data.RuntimeKind != "model" || session.Data.TaskID != "" {
		t.Fatalf("created session = runtime %q task %q, want model/no task", session.Data.RuntimeKind, session.Data.TaskID)
	}

	modelTurn := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions/"+session.Data.ID+"/messages",
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

	toolsTurn := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions/"+session.Data.ID+"/messages",
		fmt.Sprintf(`{"runtime_kind":"hecate_agent","provider":"openai","model":"gpt-4o-mini","workspace":%q,"content":"use tools"}`, workspace))
	if toolsTurn.Data.TaskID == "" || toolsTurn.Data.LatestRunID == "" {
		t.Fatalf("tools turn missing task/run: %+v", toolsTurn.Data)
	}
	firstTaskID := toolsTurn.Data.TaskID
	toolsAssistant := toolsTurn.Data.Messages[len(toolsTurn.Data.Messages)-1]
	if toolsAssistant.RuntimeKind != "hecate_agent" || toolsAssistant.TaskID != firstTaskID || toolsAssistant.SegmentID != "task:"+firstTaskID {
		t.Fatalf("tools assistant snapshot = runtime %q task %q segment %q", toolsAssistant.RuntimeKind, toolsAssistant.TaskID, toolsAssistant.SegmentID)
	}

	secondModel := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions/"+session.Data.ID+"/messages",
		`{"runtime_kind":"model","provider":"openai","model":"gpt-4o-mini","content":"back to direct chat"}`)
	if secondModel.Data.TaskID != firstTaskID {
		t.Fatalf("model segment should preserve latest task pointer, got %q want %q", secondModel.Data.TaskID, firstTaskID)
	}
	secondModelAssistant := secondModel.Data.Messages[len(secondModel.Data.Messages)-1]
	if secondModelAssistant.RuntimeKind != "model" || secondModelAssistant.TaskID != "" {
		t.Fatalf("second model assistant snapshot = runtime %q task %q", secondModelAssistant.RuntimeKind, secondModelAssistant.TaskID)
	}

	secondTools := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions/"+session.Data.ID+"/messages",
		fmt.Sprintf(`{"runtime_kind":"hecate_agent","provider":"openai","model":"gpt-4o-mini","workspace":%q,"content":"tools again"}`, workspace))
	if secondTools.Data.TaskID == "" || secondTools.Data.TaskID == firstTaskID {
		t.Fatalf("tools re-entry task_id = %q, want new task distinct from %q", secondTools.Data.TaskID, firstTaskID)
	}
	secondToolsAssistant := secondTools.Data.Messages[len(secondTools.Data.Messages)-1]
	if secondToolsAssistant.RuntimeKind != "hecate_agent" || secondToolsAssistant.TaskID != secondTools.Data.TaskID || secondToolsAssistant.SegmentID != "task:"+secondTools.Data.TaskID {
		t.Fatalf("second tools assistant snapshot = runtime %q task %q segment %q", secondToolsAssistant.RuntimeKind, secondToolsAssistant.TaskID, secondToolsAssistant.SegmentID)
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

func TestHecateAgentChatRejectsUnknownToolCapability(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "ollama",
		capabilities: providers.Capabilities{
			Name:         "ollama",
			Kind:         providers.KindLocal,
			DefaultModel: "llama3.1:8b",
			Models:       []string{"llama3.1:8b"},
		},
	}
	handler := newTestHTTPHandlerWithControlPlane(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	client := newTaskTestClient(t, handler)
	workspace := t.TempDir()

	session := mustRequestJSON[AgentChatSessionResponse](client, http.MethodPost, "/v1/agent-chat/sessions",
		fmt.Sprintf(`{"runtime_kind":"hecate_agent","workspace":%q,"provider":"ollama","model":"llama3.1:8b"}`, workspace))
	if session.Data.Capabilities.ToolCalling != modelcaps.ToolCallingUnknown {
		t.Fatalf("session capabilities = %+v, want unknown", session.Data.Capabilities)
	}

	recorder := client.mustRequestStatus(http.StatusUnprocessableEntity, http.MethodPost, "/v1/agent-chat/sessions/"+session.Data.ID+"/messages",
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
	if !strings.Contains(payload.Error.Message, "unknown or no tool-calling support") {
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
		RuntimeKind:     "hecate_agent",
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

	recorder := client.mustRequestStatus(http.StatusConflict, http.MethodPost, "/v1/agent-chat/sessions/"+session.ID+"/messages",
		`{"content":"new turn"}`)
	var payload struct {
		Error struct {
			Type        string `json:"type"`
			TaskID      string `json:"task_id"`
			LatestRunID string `json:"latest_run_id"`
			RunStatus   string `json:"run_status"`
		} `json:"error"`
	}
	payload = decodeRecorder[struct {
		Error struct {
			Type        string `json:"type"`
			TaskID      string `json:"task_id"`
			LatestRunID string `json:"latest_run_id"`
			RunStatus   string `json:"run_status"`
		} `json:"error"`
	}](t, recorder)
	if payload.Error.Type != errCodeAgentSessionBusy {
		t.Fatalf("error type = %q, want %s", payload.Error.Type, errCodeAgentSessionBusy)
	}
	if payload.Error.TaskID != task.ID || payload.Error.LatestRunID != run.ID || payload.Error.RunStatus != "running" {
		t.Fatalf("busy payload = %+v", payload.Error)
	}
}
