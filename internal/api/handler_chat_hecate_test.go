package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
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

func TestHecateAgentChatProjectDraftToolCreatesProposalArtifact(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		responses: []*types.ChatResponse{
			{
				ID:        "chatcmpl-proposal-tool",
				Model:     "gpt-4o-mini",
				CreatedAt: time.Now().UTC(),
				Choices: []types.ChatChoice{{
					Index: 0,
					Message: types.Message{
						Role: "assistant",
						ToolCalls: []types.ToolCall{{
							ID:   "call_project_proposal",
							Type: "function",
							Function: types.ToolCallFunction{
								Name:      orchestrator.AgentToolDraftProjectProposal,
								Arguments: `{"request":"Plan next work for hecate\nCapture the next reviewable project task."}`,
							},
						}},
					},
					FinishReason: "tool_calls",
				}},
				Usage: types.Usage{PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28},
			},
			{
				ID:        "chatcmpl-proposal-final",
				Model:     "gpt-4o-mini",
				CreatedAt: time.Now().UTC(),
				Choices: []types.ChatChoice{{
					Index:        0,
					Message:      types.Message{Role: "assistant", Content: "I drafted a proposal for review in Projects."},
					FinishReason: "stop",
				}},
				Usage: types.Usage{PromptTokens: 30, CompletionTokens: 9, TotalTokens: 39},
			},
		},
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	handler := NewServer(logger, apiHandler)
	client := newTaskTestClient(t, handler)
	workspace := t.TempDir()

	project := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects",
		`{"name":"Hecate"}`)
	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		fmt.Sprintf(`{"agent_id":"hecate","title":"Project planning","workspace":%q,"project_id":%q,"provider":"openai","model":"gpt-4o-mini"}`, workspace, project.Data.ID))
	response := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","content":"Plan next work for hecate"}`)
	if response.Data.Status != "completed" || response.Data.TaskID == "" || response.Data.LatestRunID == "" {
		t.Fatalf("chat response status/task/run = %q/%q/%q, want completed with task run", response.Data.Status, response.Data.TaskID, response.Data.LatestRunID)
	}
	assistant := response.Data.Messages[len(response.Data.Messages)-1]
	proposalActivity := findChatActivityByType(assistant, orchestrator.ProjectAssistantProposalArtifactKind)
	if proposalActivity.ArtifactID == "" {
		t.Fatalf("assistant activities missing proposal artifact activity: %+v", assistant.Activities)
	}
	artifacts := mustRequestJSON[TaskArtifactsResponse](client, http.MethodGet, "/hecate/v1/tasks/"+response.Data.TaskID+"/runs/"+response.Data.LatestRunID+"/artifacts", "")
	var proposalArtifact TaskArtifactItem
	for _, artifact := range artifacts.Data {
		if artifact.Kind == orchestrator.ProjectAssistantProposalArtifactKind {
			proposalArtifact = artifact
			break
		}
	}
	if proposalArtifact.ID == "" || proposalArtifact.ID != proposalActivity.ArtifactID {
		t.Fatalf("proposal artifact = %+v, activity = %+v", proposalArtifact, proposalActivity)
	}
	var payload orchestrator.ProjectAssistantDraftResult
	if err := json.Unmarshal([]byte(proposalArtifact.ContentText), &payload); err != nil {
		t.Fatalf("proposal artifact JSON error = %v\n%s", err, proposalArtifact.ContentText)
	}
	if payload.ProjectID != project.Data.ID || payload.SourceChatSessionID != session.Data.ID || payload.ActionCount != 1 || !json.Valid(payload.Proposal) {
		t.Fatalf("proposal payload = %+v, want linked project/session and embedded proposal", payload)
	}
	if items, err := apiHandler.projectWork.ListWorkItems(context.Background(), project.Data.ID); err != nil || len(items) != 0 {
		t.Fatalf("work items after draft = %d, err=%v; want no direct project mutations", len(items), err)
	}
}

func TestHecateAgentChatProjectDraftToolRejectsProjectMismatch(t *testing.T) {
	handler := newTestAPIHandlerWithSettings(slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, config.Config{}, controlplane.NewMemoryStore())
	if _, err := handler.agentChat.Create(context.Background(), chat.Session{
		ID:        "chat_mismatch",
		AgentID:   chat.DefaultAgentID,
		ProjectID: "proj_source",
		Status:    "idle",
	}); err != nil {
		t.Fatalf("Create(chat) error = %v", err)
	}

	_, err := handler.DraftProjectProposal(context.Background(), orchestrator.ProjectAssistantDraftInput{
		ProjectID:           "proj_other",
		SourceChatSessionID: "chat_mismatch",
		Request:             "Plan next work",
	})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("DraftProjectProposal err = %v, want project mismatch", err)
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
		ID:            "proj_hecate",
		Name:          "Hecate",
		Description:   "Local AI operations console.",
		DefaultRootID: "root_main",
		Roots: []projects.Root{
			{
				ID:        "root_main",
				Path:      workspace,
				Kind:      "git",
				GitRemote: "git@github.com:hecatehq/hecate.git",
				GitBranch: "main",
				Active:    true,
			},
			{
				ID:     "root_archive",
				Path:   filepath.Join(workspace, "archive"),
				Kind:   "local",
				Active: false,
			},
		},
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
	if _, err := apiHandler.projectSkills.UpsertDiscovered(context.Background(), project.ID, []projectskills.Skill{
		{
			ID:          "backend",
			ProjectID:   project.ID,
			Title:       "Backend Skill",
			Description: "Backend changes.",
			Path:        ".hecate/skills/backend/SKILL.md",
			Enabled:     true,
		},
	}); err != nil {
		t.Fatalf("UpsertDiscovered skills: %v", err)
	}
	if _, err := apiHandler.projectWork.CreateWorkItem(context.Background(), projectwork.WorkItem{
		ID:          "work_chat_context",
		ProjectID:   project.ID,
		Title:       "Implement chat context",
		Brief:       "Teach linked chat about project skills and work state.",
		Status:      projectwork.WorkItemStatusReady,
		Priority:    "high",
		OwnerRoleID: "architect",
	}); err != nil {
		t.Fatalf("Create work item: %v", err)
	}
	if _, err := apiHandler.projectWork.CreateWorkItem(context.Background(), projectwork.WorkItem{
		ID:        "work_done",
		ProjectID: project.ID,
		Title:     "Already done",
		Brief:     "Completed work should not enter the active chat hint.",
		Status:    projectwork.WorkItemStatusDone,
	}); err != nil {
		t.Fatalf("Create done work item: %v", err)
	}
	if _, err := apiHandler.projectWork.CreateAssignment(context.Background(), projectwork.Assignment{
		ID:         "asgn_plan",
		ProjectID:  project.ID,
		WorkItemID: "work_chat_context",
		RoleID:     "architect",
		DriverKind: projectwork.AssignmentDriverHecateTask,
		Status:     projectwork.AssignmentStatusQueued,
	}); err != nil {
		t.Fatalf("Create assignment: %v", err)
	}
	if _, err := apiHandler.projectWork.CreateAssignment(context.Background(), projectwork.Assignment{
		ID:         "asgn_done",
		ProjectID:  project.ID,
		WorkItemID: "work_chat_context",
		RoleID:     "reviewer_qa",
		DriverKind: projectwork.AssignmentDriverHecateTask,
		Status:     projectwork.AssignmentStatusCompleted,
	}); err != nil {
		t.Fatalf("Create completed assignment: %v", err)
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
		"Project roots (metadata only; files are not read):",
		"- Root " + workspace + " (root_main): active=true, default=true, kind=git, branch=main, remote=git@github.com:hecatehq/hecate.git",
		"- Root " + filepath.Join(workspace, "archive") + " (root_archive): active=false, kind=local",
		"Role hints:",
		"A Project Planner (role_planner): Shapes reviewable project work.",
		"Project skills (metadata only; skill bodies are not loaded):",
		"Backend Skill (backend): Backend changes. Path: .hecate/skills/backend/SKILL.md",
		"Use skills as procedures/guidance, not as role assignments.",
		"Active project work snapshot:",
		"Shown active work item status counts: ready=1",
		"- Work item Implement chat context (work_chat_context): status=ready, priority=high, owner_role=architect",
		"Brief: Teach linked chat about project skills and work state.",
		"Active assignments:",
		"- Assignment asgn_plan: work_item=work_chat_context, role=architect, status=queued, driver=hecate_task",
		"Accepted project memory:",
		"Project memory: Project Assistant boundary\nID: mem_boundary\nTrust: operator_memory",
		"Project changes should be drafted as typed proposals and applied only after operator review.",
		"Operator system prompt:\nPrefer concise answers.",
	} {
		if !strings.Contains(backingTask.SystemPrompt, want) {
			t.Fatalf("task system_prompt missing %q:\n%s", want, backingTask.SystemPrompt)
		}
	}
	for _, excluded := range []string{"work_done", "asgn_done"} {
		if strings.Contains(backingTask.SystemPrompt, excluded) {
			t.Fatalf("task system_prompt included inactive work %q:\n%s", excluded, backingTask.SystemPrompt)
		}
	}
}

func TestProjectChatWorkflowSystemPromptSkipsExternalAgentSessions(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{name: "openai"}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())

	project, err := apiHandler.projects.Create(context.Background(), projects.Project{
		ID:   "proj_external",
		Name: "External Project",
	})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}

	prompt := apiHandler.projectChatWorkflowSystemPrompt(context.Background(), chat.Session{
		AgentID:   "codex",
		ProjectID: project.ID,
	})
	if prompt != "" {
		t.Fatalf("external-agent project prompt = %q, want empty", prompt)
	}
}

func TestProjectChatWorkflowSystemPromptUsesStrictEmbeddedCairnlineProject(t *testing.T) {
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	const projectID = "proj_chat_embedded"
	rootPath := t.TempDir()

	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		if _, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:            projectID,
			Name:          "Embedded Chat",
			Description:   "Project prompt comes from embedded Cairnline.",
			DefaultRootID: "root_chat_embedded",
			Roots: []cairnline.Root{{
				ID:        "root_chat_embedded",
				Path:      rootPath,
				Kind:      "git",
				GitBranch: "main",
				Active:    true,
			}},
			ContextSources: []cairnline.Source{{
				ID:         "ctx_chat_embedded",
				Kind:       "workspace_instruction",
				Title:      "AGENTS.md",
				Locator:    "AGENTS.md",
				Enabled:    true,
				Format:     "agents_md",
				TrustLabel: "workspace_guidance",
			}},
		}); err != nil {
			return err
		}
		if _, err := service.CreateRole(t.Context(), cairnline.Role{
			ID:              "role_chat_planner",
			ProjectID:       projectID,
			Name:            "Chat Planner",
			Description:     "Shapes chat-discovered work.",
			DefaultSkillIDs: []string{"planning"},
		}); err != nil {
			return err
		}
		if _, err := service.CreateProjectSkill(t.Context(), cairnline.ProjectSkill{
			ID:          "planning",
			ProjectID:   projectID,
			Title:       "Planning",
			Description: "Plan reviewable chat work.",
			Path:        ".agents/skills/planning/SKILL.md",
			RootID:      "root_chat_embedded",
			Format:      cairnline.SkillFormatMarkdown,
			Enabled:     true,
			Status:      cairnline.SkillStatusAvailable,
			TrustLabel:  cairnline.SkillTrustWorkspace,
			SourceRefs:  []string{"ctx_chat_embedded"},
		}); err != nil {
			return err
		}
		if _, err := service.CreateWorkItem(t.Context(), cairnline.WorkItem{
			ID:          "work_chat_embedded",
			ProjectID:   projectID,
			Title:       "Plan embedded chat context",
			Brief:       "Keep chat project prelude backed by Cairnline.",
			Status:      cairnline.WorkStatusReady,
			Priority:    "high",
			OwnerRoleID: "role_chat_planner",
			RootID:      "root_chat_embedded",
		}); err != nil {
			return err
		}
		if _, err := service.CreateAssignment(t.Context(), cairnline.Assignment{
			ID:            "asgn_chat_embedded",
			ProjectID:     projectID,
			WorkItemID:    "work_chat_embedded",
			RoleID:        "role_chat_planner",
			RootID:        "root_chat_embedded",
			ExecutionMode: cairnline.ExecutionMCPPull,
			Status:        cairnline.AssignmentQueued,
		}); err != nil {
			return err
		}
		_, err := service.CreateMemoryEntry(t.Context(), cairnline.MemoryEntry{
			ID:         "mem_chat_embedded",
			ProjectID:  projectID,
			Title:      "Embedded chat memory",
			Body:       "Hecate Chat should use embedded Cairnline project context.",
			Enabled:    true,
			TrustLabel: memory.TrustLabelOperatorMemory,
			SourceKind: memory.SourceKindOperator,
		})
		return err
	}); err != nil {
		t.Fatalf("seed embedded Cairnline chat project: %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store seeded ok=%v err=%v, want no native project row", ok, err)
	}

	prompt := handler.projectChatWorkflowSystemPrompt(t.Context(), chat.Session{ProjectID: projectID})
	for _, want := range []string{
		"Project: Embedded Chat (proj_chat_embedded)",
		"Project roots (metadata only; files are not read):",
		"- Root " + rootPath + " (root_chat_embedded): active=true, default=true, kind=git, branch=main",
		"Role hints:",
		"Chat Planner (role_chat_planner): Shapes chat-discovered work.",
		"Project skills (metadata only; skill bodies are not loaded):",
		"Planning (planning): Plan reviewable chat work. Path: .agents/skills/planning/SKILL.md",
		"Active project work snapshot:",
		"- Work item Plan embedded chat context (work_chat_embedded): status=ready, priority=high, owner_role=role_chat_planner",
		"- Assignment asgn_chat_embedded: work_item=work_chat_embedded, role=role_chat_planner, status=queued, driver=hecate_task",
		"Project memory: Embedded chat memory\nID: mem_chat_embedded\nTrust: operator_memory",
		"Hecate Chat should use embedded Cairnline project context.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("strict embedded project prompt missing %q:\n%s", want, prompt)
		}
	}

	packet := renderChatContextPacket(handler.directModelContextPacket(t.Context(), chat.Session{ProjectID: projectID}, "openai", "gpt-4o-mini", ""))
	if packet == nil ||
		!chatContextPacketHasKind(*packet, "project") ||
		!chatContextPacketHasKind(*packet, "project_skills") ||
		!chatContextPacketHasKind(*packet, "project_work") {
		t.Fatalf("strict embedded context packet missing project metadata: %+v", packet)
	}
}

func TestProjectChatWorkflowSystemPromptStrictEmbeddedSkipsNativeFallback(t *testing.T) {
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	const projectID = "proj_chat_native_only"

	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:          projectID,
		Name:        "Native Only Chat Project",
		Description: "This native row should not feed strict embedded chat.",
	}); err != nil {
		t.Fatalf("seed native project: %v", err)
	}
	if _, err := handler.memory.Create(t.Context(), memory.Entry{
		ID:         "mem_native_only",
		Scope:      memory.ScopeProject,
		ProjectID:  projectID,
		Title:      "Native-only memory",
		Body:       "This native memory should not feed strict embedded chat.",
		Enabled:    true,
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
	}); err != nil {
		t.Fatalf("seed native memory: %v", err)
	}

	prompt := handler.projectChatWorkflowSystemPrompt(t.Context(), chat.Session{ProjectID: projectID})
	for _, notWant := range []string{
		"Native Only Chat Project",
		"This native row should not feed strict embedded chat.",
		"Native-only memory",
		"This native memory should not feed strict embedded chat.",
	} {
		if strings.Contains(prompt, notWant) {
			t.Fatalf("strict embedded project prompt fell back to native content %q:\n%s", notWant, prompt)
		}
	}

	packet := renderChatContextPacket(handler.directModelContextPacket(t.Context(), chat.Session{ProjectID: projectID}, "openai", "gpt-4o-mini", ""))
	if packet != nil && (chatContextPacketHasKind(*packet, "project") || chatContextPacketHasKind(*packet, "memory")) {
		t.Fatalf("strict embedded context packet fell back to native project or memory metadata: %+v", packet)
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

func TestDirectHecateChatAutoCompactsLongTranscript(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-hecate-direct-compact",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Now().UTC(),
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "ack"},
				FinishReason: "stop",
			}},
		},
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	handler := NewServer(logger, apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"openai","model":"gpt-4o-mini"}`)
	var updated ChatSessionResponse
	for i := 1; i <= 10; i++ {
		updated = mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
			fmt.Sprintf(`{"execution_mode":"hecate_task","tools_enabled":false,"content":"question %02d"}`, i))
	}

	if updated.Data.ContextSummary == nil {
		t.Fatal("context_summary is nil, want automatic compaction metadata")
	}
	if updated.Data.ContextSummary.MessageCount != 10 {
		t.Fatalf("context_summary message_count = %d, want 10", updated.Data.ContextSummary.MessageCount)
	}
	if updated.Data.ContextSummary.Strategy != chat.ContextSummaryStrategySemantic {
		t.Fatalf("context_summary strategy = %q, want semantic", updated.Data.ContextSummary.Strategy)
	}
	request := provider.LastRequest()
	if len(request.Messages) != 10 {
		t.Fatalf("provider message count = %d, want compact summary + 8 retained + current", len(request.Messages))
	}
	if request.Messages[0].Role != "system" || !strings.Contains(request.Messages[0].Content, "Earlier chat transcript compacted by Hecate") {
		t.Fatalf("first provider message = %+v, want compacted transcript system summary", request.Messages[0])
	}
	if request.Messages[1].Role != "user" || request.Messages[1].Content != "question 06" {
		t.Fatalf("first retained provider message = %+v, want question 06", request.Messages[1])
	}
	if request.Messages[len(request.Messages)-1].Content != "question 10" {
		t.Fatalf("last provider message = %+v, want current prompt", request.Messages[len(request.Messages)-1])
	}
	lastAssistant := updated.Data.Messages[len(updated.Data.Messages)-1]
	if lastAssistant.ContextPacket == nil || !chatContextPacketHasKind(*lastAssistant.ContextPacket, "transcript_summary") {
		t.Fatalf("assistant context packet missing transcript_summary: %+v", lastAssistant.ContextPacket)
	}
}

func TestHecateChatCompactEndpointCompactsTranscript(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-hecate-manual-compact",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Now().UTC(),
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "ack"},
				FinishReason: "stop",
			}},
		},
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	handler := NewServer(logger, apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"openai","model":"gpt-4o-mini"}`)
	for i := 1; i <= 5; i++ {
		_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
			fmt.Sprintf(`{"execution_mode":"hecate_task","tools_enabled":false,"content":"manual %02d"}`, i))
	}

	compacted := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/compact", `{}`)
	if compacted.Data.ContextSummary == nil {
		t.Fatal("context_summary is nil, want manual compaction metadata")
	}
	if compacted.Data.ContextSummary.MessageCount != 2 {
		t.Fatalf("context_summary message_count = %d, want 2", compacted.Data.ContextSummary.MessageCount)
	}
	if compacted.Data.ContextSummary.Strategy != chat.ContextSummaryStrategySemantic {
		t.Fatalf("context_summary strategy = %q, want semantic", compacted.Data.ContextSummary.Strategy)
	}
	if compacted.Data.ContextSummary.Content != "ack" {
		t.Fatalf("context_summary content = %q, want provider semantic summary", compacted.Data.ContextSummary.Content)
	}
	request := provider.LastRequest()
	if len(request.Messages) != 2 || !strings.Contains(request.Messages[1].Content, "manual 01") {
		t.Fatalf("semantic compaction request = %+v, want transcript in compaction prompt", request.Messages)
	}
}

func TestHecateChatCompactEndpointFallsBackToDeterministicSummary(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-hecate-compact-fallback",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Now().UTC(),
			Choices: []types.ChatChoice{{
				Index:        0,
				Message:      types.Message{Role: "assistant", Content: "ack"},
				FinishReason: "stop",
			}},
		},
		errSequence: []error{nil, nil, nil, nil, nil, fmt.Errorf("semantic compaction unavailable")},
	}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	handler := NewServer(logger, apiHandler)
	client := newTaskTestClient(t, handler)

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		`{"agent_id":"hecate","provider":"openai","model":"gpt-4o-mini"}`)
	for i := 1; i <= 5; i++ {
		_ = mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
			fmt.Sprintf(`{"execution_mode":"hecate_task","tools_enabled":false,"content":"fallback %02d"}`, i))
	}

	compacted := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/compact", `{}`)
	if compacted.Data.ContextSummary == nil {
		t.Fatal("context_summary is nil, want fallback compaction metadata")
	}
	if compacted.Data.ContextSummary.Strategy != chat.ContextSummaryStrategyDeterministic {
		t.Fatalf("context_summary strategy = %q, want deterministic fallback", compacted.Data.ContextSummary.Strategy)
	}
	if !strings.Contains(compacted.Data.ContextSummary.Content, "fallback 01") {
		t.Fatalf("context_summary content = %q, want deterministic transcript line", compacted.Data.ContextSummary.Content)
	}
}

func chatContextPacketHasKind(packet ChatContextPacketItem, kind string) bool {
	for _, item := range packet.Items {
		if item.Kind == kind {
			return true
		}
	}
	return false
}

func TestProjectChatPromptHelpersBoundUTF8Text(t *testing.T) {
	remaining := 64
	section, truncated := boundedPromptContextSection("Header", strings.Repeat("é", 80), 48, &remaining)
	if !truncated {
		t.Fatalf("truncated = false, want true")
	}
	if section == "" || !strings.HasSuffix(section, "\n[truncated]") {
		t.Fatalf("section = %q, want truncated section", section)
	}
	if !utf8.ValidString(section) {
		t.Fatalf("section is not valid UTF-8: %q", section)
	}
	if len(section) > 48 {
		t.Fatalf("section length = %d, want <= 48", len(section))
	}
	if remaining != 64-len(section) {
		t.Fatalf("remaining = %d, want %d", remaining, 64-len(section))
	}
}

func TestProjectChatRootHintsBoundsAndOrdersMetadata(t *testing.T) {
	text := projectChatRootHints(projects.Project{
		DefaultRootID: "root_default",
		Roots: []projects.Root{
			{ID: "root_zeta", Path: "/tmp/zeta", Kind: "local", Active: true},
			{ID: "root_inactive", Path: "/tmp/inactive", Kind: "local"},
			{ID: "root_alpha", Path: "/tmp/alpha", Kind: "git", Active: true},
			{ID: "root_default", Path: "/tmp/default", Kind: "git_worktree", Active: false, GitBranch: "feature/chat"},
			{ID: "root_omitted", Path: "/tmp/omitted", Kind: "local"},
		},
	})

	for _, want := range []string{
		"Project roots (metadata only; files are not read):",
		"- Root /tmp/default (root_default): active=false, default=true, kind=git_worktree, branch=feature/chat",
		"- 1 additional roots omitted.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("root hints missing %q:\n%s", want, text)
		}
	}
	defaultIndex := strings.Index(text, "root_default")
	activeIndex := strings.Index(text, "root_zeta")
	if defaultIndex < 0 || activeIndex < 0 || defaultIndex > activeIndex {
		t.Fatalf("root hints order = %q, want default root before active roots", text)
	}
	if strings.Contains(text, "root_omitted") {
		t.Fatalf("root hints included omitted root:\n%s", text)
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

func TestChatSessionsProjectIDUsesStrictEmbeddedCairnlineProject(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{name: "openai"}
	apiHandler := newTestAPIHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, controlplane.NewMemoryStore())
	handler := NewServer(logger, apiHandler)
	client := newAPITestClient(t, handler)
	const projectID = "proj_chat_cairnline_only"

	if err := apiHandler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		_, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:          projectID,
			Name:        "Cairnline-only chat project",
			Description: "Chat sessions should validate against embedded Cairnline.",
		})
		return err
	}); err != nil {
		t.Fatalf("seed embedded Cairnline project: %v", err)
	}
	if _, ok, err := apiHandler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("native project exists = %t err=%v, want missing native project", ok, err)
	}

	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		fmt.Sprintf(`{"agent_id":"hecate","project_id":%q,"provider":"openai","model":"gpt-4o-mini"}`, projectID))
	if created.Data.ProjectID != projectID {
		t.Fatalf("created project_id = %q, want %q", created.Data.ProjectID, projectID)
	}
	if _, ok, err := apiHandler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("native project exists after chat create = %t err=%v, want missing native project", ok, err)
	}

	list := mustRequestJSON[ChatSessionsResponse](client, http.MethodGet, "/hecate/v1/chat/sessions", "")
	if len(list.Data) != 1 || list.Data[0].ProjectID != projectID {
		t.Fatalf("listed chat sessions = %+v, want one session for Cairnline-only project %q", list.Data, projectID)
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

func findChatActivityByType(message ChatMessageItem, activityType string) ChatActivityItem {
	for _, activity := range message.Activities {
		if activity.Type == activityType {
			return activity
		}
	}
	return ChatActivityItem{}
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

func TestChatActivityFromTaskActivityFormatsProjectAssistantProposalDetail(t *testing.T) {
	item := TaskActivityItem{
		ID:         "artifact:proposal_1",
		Type:       orchestrator.ProjectAssistantProposalArtifactKind,
		Status:     "ready",
		Title:      "Project Assistant proposal",
		ArtifactID: "artifact_project_proposal",
		Kind:       orchestrator.ProjectAssistantProposalArtifactKind,
		Summary: map[string]any{
			"proposal_title":        "Plan next project work",
			"proposal_action_count": float64(2),
		},
		OccurredAt: "2026-05-03T10:00:00Z",
	}

	activity := agentChatActivityFromTaskActivity(item)
	rendered := renderAgentChatActivities([]chat.Activity{activity})
	if len(rendered) != 1 {
		t.Fatalf("rendered activities = %d, want 1", len(rendered))
	}
	if rendered[0].Detail != "Plan next project work - 2 actions - ready for review" {
		t.Fatalf("proposal detail = %q", rendered[0].Detail)
	}
	if rendered[0].ArtifactID != "artifact_project_proposal" {
		t.Fatalf("artifact_id = %q", rendered[0].ArtifactID)
	}
}

func TestChatActivityFromTaskActivityCarriesMCPApp(t *testing.T) {
	item := TaskActivityItem{
		ID:       "step:step_weather",
		Type:     "tool_call",
		Status:   "completed",
		Title:    "mcp__weather__get_weather (completed)",
		ToolName: "mcp__weather__get_weather",
		Kind:     "tool",
		Summary: map[string]any{
			"mcp_app": map[string]any{
				"resource_uri":   "ui://weather/dashboard",
				"mime_type":      "text/html;profile=mcp-app",
				"html":           "<!doctype html><html><body>weather app</body></html>",
				"tool_name":      "mcp__weather__get_weather",
				"tool_input":     map[string]any{"city": "Lisbon"},
				"tool_result":    map[string]any{"content": []any{map[string]any{"type": "text", "text": "72F"}}},
				"resource_meta":  map[string]any{"ui": map[string]any{"prefersBorder": true}},
				"html_truncated": false,
			},
		},
		OccurredAt: "2026-05-03T10:00:00Z",
	}

	activity := agentChatActivityFromTaskActivity(item)
	rendered := renderAgentChatActivities([]chat.Activity{activity})
	if len(rendered) != 1 {
		t.Fatalf("rendered activities = %d, want 1", len(rendered))
	}
	app := rendered[0].MCPApp
	if app == nil {
		t.Fatal("MCPApp = nil")
	}
	if app.ResourceURI != "ui://weather/dashboard" || !strings.Contains(app.HTML, "weather app") {
		t.Fatalf("app = %+v, want weather dashboard HTML", app)
	}
	if !strings.Contains(string(app.ToolInput), `"city":"Lisbon"`) {
		t.Fatalf("tool_input = %s, want city", app.ToolInput)
	}
	if !strings.Contains(string(app.ToolResult), `"72F"`) {
		t.Fatalf("tool_result = %s, want result content", app.ToolResult)
	}
	if !strings.Contains(string(app.ResourceMeta), `"prefersBorder":true`) {
		t.Fatalf("resource_meta = %s, want prefersBorder", app.ResourceMeta)
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

func TestTaskActivityItemsIncludeProjectAssistantProposalMetadata(t *testing.T) {
	items := buildTaskActivityItems(nil, []TaskArtifactItem{{
		ID:          "artifact_project_proposal",
		TaskID:      "task_1",
		RunID:       "run_1",
		Kind:        orchestrator.ProjectAssistantProposalArtifactKind,
		Name:        "Project Assistant proposal",
		MimeType:    "application/json",
		StorageKind: "inline",
		Status:      "ready",
		ContentText: `{
			"object":"project_assistant.chat_proposal",
			"project_id":"proj_1",
			"proposal_id":"pa_1",
			"title":"Plan next project work",
			"action_count":2,
			"proposal":{"id":"pa_1","title":"Plan next project work","actions":[]}
		}`,
	}}, nil, types.TaskRun{})

	item := taskActivityByID(items, "artifact:artifact_project_proposal")
	if item.Type != orchestrator.ProjectAssistantProposalArtifactKind {
		t.Fatalf("activity type = %q, want proposal activity", item.Type)
	}
	if item.Summary["proposal_title"] != "Plan next project work" || item.Summary["proposal_action_count"] != 2 {
		t.Fatalf("proposal summary = %#v, want title/action count", item.Summary)
	}
	if item.Summary["proposal_id"] != "pa_1" {
		t.Fatalf("proposal_id = %#v", item.Summary["proposal_id"])
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

func TestHecateAgentChatRejectsInvalidMCPServers(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{name: "openai"}
	handler := newTestHTTPHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	client := newTaskTestClient(t, handler)
	workspace := t.TempDir()

	session := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions",
		fmt.Sprintf(`{"agent_id":"hecate","workspace":%q,"provider":"openai","model":"gpt-4o-mini"}`, workspace))
	recorder := client.mustRequestStatus(http.StatusBadRequest, http.MethodPost, "/hecate/v1/chat/sessions/"+session.Data.ID+"/messages",
		`{"execution_mode":"hecate_task","content":"show app","mcp_servers":[{"name":"weather","command":"node","url":"https://example.invalid/mcp"}]}`)
	payload := decodeRecorder[struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}](t, recorder)
	if payload.Error.Type != errCodeInvalidRequest {
		t.Fatalf("error type = %q, want %s", payload.Error.Type, errCodeInvalidRequest)
	}
	if !strings.Contains(payload.Error.Message, "command and url are mutually exclusive") {
		t.Fatalf("error message = %q, want MCP validation detail", payload.Error.Message)
	}
}

func TestHecateAgentChatRejectsSessionLevelMCPServers(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{name: "openai"}
	handler := newTestHTTPHandlerWithSettings(logger, []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	client := newTaskTestClient(t, handler)
	workspace := t.TempDir()

	recorder := client.mustRequestStatus(http.StatusBadRequest, http.MethodPost, "/hecate/v1/chat/sessions",
		fmt.Sprintf(`{"agent_id":"hecate","workspace":%q,"provider":"openai","model":"gpt-4o-mini","mcp_servers":[{"name":"weather","command":"node"}]}`, workspace))
	payload := decodeRecorder[struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}](t, recorder)
	if payload.Error.Type != errCodeInvalidRequest {
		t.Fatalf("error type = %q, want %s", payload.Error.Type, errCodeInvalidRequest)
	}
	if !strings.Contains(payload.Error.Message, "chat session mcp_servers are only supported for external agents") {
		t.Fatalf("error message = %q, want session-level MCP guidance", payload.Error.Message)
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
