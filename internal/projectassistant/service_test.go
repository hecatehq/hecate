package projectassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/pkg/types"
)

type assistantFixture struct {
	service          *Service
	projects         projects.Store
	chats            chat.Store
	work             projectwork.Store
	memoryEntries    memory.Store
	memoryCandidates memory.CandidateStore
}

type assistantFixtureBuilder struct {
	name  string
	build func(t *testing.T) assistantFixture
}

type fakeAssistantLLM struct {
	response string
	err      error
	requests []types.ChatRequest
}

func (f *fakeAssistantLLM) Chat(_ context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	return &types.ChatResponse{
		Choices: []types.ChatChoice{{
			Message: types.Message{Role: "assistant", Content: f.response},
		}},
	}, nil
}

func TestService_ProposeRejectsUnknownActionKind(t *testing.T) {
	t.Parallel()
	fixture := newMemoryAssistantFixture(t)

	_, err := fixture.service.Propose(context.Background(), ProposalInput{
		Actions: []Action{{Kind: "rewrite_the_world", Patch: rawPatch(t, map[string]string{"name": "bad"})}},
	})
	if !errors.Is(err, ErrUnknownActionKind) {
		t.Fatalf("Propose err = %v, want ErrUnknownActionKind", err)
	}
}

func TestService_DraftCreatesAssignmentProposalAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)
			workItem, err := fixture.work.CreateWorkItem(ctx, projectwork.WorkItem{
				ID:          "work_plan",
				ProjectID:   project.ID,
				Title:       "Plan next work",
				Brief:       "Pick the next reviewable task.",
				Status:      projectwork.WorkItemStatusReady,
				OwnerRoleID: "product_manager",
			})
			if err != nil {
				t.Fatalf("CreateWorkItem: %v", err)
			}

			proposal, err := fixture.service.Draft(ctx, DraftInput{
				ProjectID:  project.ID,
				WorkItemID: workItem.ID,
				Request:    "Queue product planning\nPrefer a reviewable handoff.",
				DriverKind: projectwork.AssignmentDriverExternalAgent,
				TraceID:    "trace_draft",
			})
			if err != nil {
				t.Fatalf("Draft assignment: %v", err)
			}
			if proposal.Title != "Queue product planning" || proposal.TraceID != "trace_draft" {
				t.Fatalf("proposal title/trace = %q/%q, want request title and trace", proposal.Title, proposal.TraceID)
			}
			if len(proposal.Actions) != 1 || proposal.Actions[0].Kind != ActionCreateAssignment {
				t.Fatalf("actions = %+v, want one create_assignment", proposal.Actions)
			}
			patch := rawPatchMap(t, proposal.Actions[0].Patch)
			if patch["project_id"] != project.ID || patch["work_item_id"] != workItem.ID || patch["role_id"] != "product_manager" || patch["driver_kind"] != projectwork.AssignmentDriverExternalAgent || patch["status"] != projectwork.AssignmentStatusQueued {
				t.Fatalf("assignment patch = %+v, want project/work/owner-role/external queued", patch)
			}
		})
	}
}

func TestService_DraftCreatesWorkItemProposalAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)

			proposal, err := fixture.service.Draft(ctx, DraftInput{
				ProjectID: project.ID,
				Request:   "Write rollout notes\nCapture release caveats.",
				RoleID:    "architect",
			})
			if err != nil {
				t.Fatalf("Draft work item: %v", err)
			}
			if len(proposal.Actions) != 1 || proposal.Actions[0].Kind != ActionCreateWorkItem {
				t.Fatalf("actions = %+v, want one create_work_item", proposal.Actions)
			}
			patch := rawPatchMap(t, proposal.Actions[0].Patch)
			if patch["project_id"] != project.ID || patch["title"] != "Write rollout notes" || patch["brief"] != "Capture release caveats." || patch["owner_role_id"] != "architect" {
				t.Fatalf("work item patch = %+v, want project/title/brief/owner role", patch)
			}
		})
	}
}

func TestService_ContextBuildsProjectAssistantPacketAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)
			role, err := fixture.work.CreateRole(ctx, projectwork.AgentRoleProfile{
				ID:                "planner",
				ProjectID:         project.ID,
				Name:              "Planning Lead",
				DefaultDriverKind: projectwork.AssignmentDriverExternalAgent,
			})
			if err != nil {
				t.Fatalf("CreateRole: %v", err)
			}
			workItem, err := fixture.work.CreateWorkItem(ctx, projectwork.WorkItem{
				ID:          "work_context",
				ProjectID:   project.ID,
				Title:       "Shape assistant context",
				Brief:       "Define what the assistant sees.",
				Status:      projectwork.WorkItemStatusReady,
				OwnerRoleID: role.ID,
			})
			if err != nil {
				t.Fatalf("CreateWorkItem: %v", err)
			}
			if _, err := fixture.work.CreateAssignment(ctx, projectwork.Assignment{
				ID:         "asgn_context",
				ProjectID:  project.ID,
				WorkItemID: workItem.ID,
				RoleID:     role.ID,
				DriverKind: projectwork.AssignmentDriverExternalAgent,
				Status:     projectwork.AssignmentStatusRunning,
			}); err != nil {
				t.Fatalf("CreateAssignment: %v", err)
			}
			if _, err := fixture.memoryEntries.Create(ctx, memory.Entry{
				ID:         "mem_context",
				ProjectID:  project.ID,
				Title:      "Assistant context decision",
				Body:       "Drafts inspect context before proposing durable changes.",
				TrustLabel: memory.TrustLabelOperatorMemory,
				SourceKind: memory.SourceKindOperator,
				Enabled:    true,
			}); err != nil {
				t.Fatalf("Create memory: %v", err)
			}
			if _, err := fixture.memoryCandidates.CreateCandidate(ctx, memory.Candidate{
				ID:                  "cand_context",
				ProjectID:           project.ID,
				Title:               "Candidate context note",
				Body:                "Candidate memory is visible but still reviewable.",
				SuggestedTrustLabel: memory.TrustLabelGenerated,
				SuggestedSourceKind: memory.SourceKindGenerated,
				Status:              memory.CandidateStatusPending,
			}); err != nil {
				t.Fatalf("CreateCandidate: %v", err)
			}
			if _, err := fixture.memoryCandidates.CreateCandidate(ctx, memory.Candidate{
				ID:                  "cand_rejected",
				ProjectID:           project.ID,
				Title:               "Rejected context note",
				Body:                "Rejected suggestions stay out of assistant context.",
				SuggestedTrustLabel: memory.TrustLabelGenerated,
				SuggestedSourceKind: memory.SourceKindGenerated,
				Status:              memory.CandidateStatusRejected,
			}); err != nil {
				t.Fatalf("Create rejected candidate: %v", err)
			}

			packet, err := fixture.service.Context(ctx, ContextInput{
				ProjectID:  project.ID,
				WorkItemID: workItem.ID,
				Request:    "Queue planning",
			})
			if err != nil {
				t.Fatalf("Context: %v", err)
			}
			if packet.Project.ID != project.ID || packet.SelectedWork == nil || packet.SelectedWork.ID != workItem.ID {
				t.Fatalf("context project/work = %+v/%+v, want selected project/work", packet.Project, packet.SelectedWork)
			}
			if packet.Selection.RoleID != role.ID || packet.Selection.RoleSource != "selected_work_owner" || packet.Selection.DriverKind != projectwork.AssignmentDriverExternalAgent || packet.Selection.DriverSource != "role_default" {
				t.Fatalf("selection = %+v, want owner role and role default driver", packet.Selection)
			}
			if !strings.Contains(packet.Selection.Reason, "Selected work item is owned by Planning Lead") || !strings.Contains(packet.Selection.Reason, "Using external_agent") {
				t.Fatalf("selection reason = %q, want owner/default explanation", packet.Selection.Reason)
			}
			if len(packet.Roles) == 0 || !contextRoleExists(packet.Roles, role.ID) {
				t.Fatalf("roles = %+v, want custom role included", packet.Roles)
			}
			if len(packet.Assignments) != 1 || packet.Assignments[0].ID != "asgn_context" {
				t.Fatalf("assignments = %+v, want recent assignment", packet.Assignments)
			}
			if len(packet.Memory) != 1 || packet.Memory[0].ID != "mem_context" {
				t.Fatalf("memory = %+v, want enabled project memory", packet.Memory)
			}
			if len(packet.MemoryCandidates) != 1 || packet.MemoryCandidates[0].ID != "cand_context" {
				t.Fatalf("memory candidates = %+v, want candidate", packet.MemoryCandidates)
			}
			if len(packet.RecentActivity) == 0 {
				t.Fatalf("recent activity is empty, want context timeline entries")
			}
		})
	}
}

func TestService_ContextRejectsMissingExplicitRole(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newMemoryAssistantFixture(t)
	project := createTestProject(t, ctx, fixture.projects)

	_, err := fixture.service.Context(ctx, ContextInput{
		ProjectID: project.ID,
		Request:   "Draft with missing role",
		RoleID:    "missing_role",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Context err = %v, want ErrNotFound", err)
	}
}

func TestService_ContextBudgetsMemoryBodiesAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)
			memoryBody := strings.Repeat("é", contextMemoryBodyMaxBytes)
			candidateBody := strings.Repeat("é", contextCandidateBodyMaxBytes)
			if _, err := fixture.memoryEntries.Create(ctx, memory.Entry{
				ID:         "mem_budget",
				ProjectID:  project.ID,
				Title:      "Large project memory",
				Body:       memoryBody,
				TrustLabel: memory.TrustLabelOperatorMemory,
				SourceKind: memory.SourceKindOperator,
				Enabled:    true,
			}); err != nil {
				t.Fatalf("Create memory: %v", err)
			}
			if _, err := fixture.memoryCandidates.CreateCandidate(ctx, memory.Candidate{
				ID:                  "cand_budget",
				ProjectID:           project.ID,
				Title:               "Large candidate memory",
				Body:                candidateBody,
				SuggestedTrustLabel: memory.TrustLabelGenerated,
				SuggestedSourceKind: memory.SourceKindGenerated,
				Status:              memory.CandidateStatusPending,
			}); err != nil {
				t.Fatalf("CreateCandidate: %v", err)
			}

			packet, err := fixture.service.Context(ctx, ContextInput{
				ProjectID: project.ID,
				Request:   "Inspect budgeted context",
			})
			if err != nil {
				t.Fatalf("Context: %v", err)
			}
			if len(packet.Memory) != 1 || len(packet.MemoryCandidates) != 1 {
				t.Fatalf("memory/candidates = %+v/%+v, want one of each", packet.Memory, packet.MemoryCandidates)
			}
			gotMemory := packet.Memory[0]
			if !gotMemory.BodyTruncated || len(gotMemory.Body) > contextMemoryBodyMaxBytes || !strings.HasSuffix(gotMemory.Body, contextTruncatedSuffix) || !utf8.ValidString(gotMemory.Body) {
				t.Fatalf("memory body budget = len:%d truncated:%v suffix:%v valid:%v", len(gotMemory.Body), gotMemory.BodyTruncated, strings.HasSuffix(gotMemory.Body, contextTruncatedSuffix), utf8.ValidString(gotMemory.Body))
			}
			if gotMemory.BodyOriginalBytes != len(memoryBody) || gotMemory.BodyReturnedBytes != len(gotMemory.Body) || gotMemory.BodyTokensEstimate != estimateTokensFromBytes(len(gotMemory.Body)) {
				t.Fatalf("memory budget metadata = %+v, want original/returned bytes and token estimate", gotMemory)
			}
			gotCandidate := packet.MemoryCandidates[0]
			if !gotCandidate.BodyTruncated || len(gotCandidate.Body) > contextCandidateBodyMaxBytes || !strings.HasSuffix(gotCandidate.Body, contextTruncatedSuffix) || !utf8.ValidString(gotCandidate.Body) {
				t.Fatalf("candidate body budget = len:%d truncated:%v suffix:%v valid:%v", len(gotCandidate.Body), gotCandidate.BodyTruncated, strings.HasSuffix(gotCandidate.Body, contextTruncatedSuffix), utf8.ValidString(gotCandidate.Body))
			}
			if gotCandidate.BodyOriginalBytes != len(candidateBody) || gotCandidate.BodyReturnedBytes != len(gotCandidate.Body) || gotCandidate.BodyTokensEstimate != estimateTokensFromBytes(len(gotCandidate.Body)) {
				t.Fatalf("candidate budget metadata = %+v, want original/returned bytes and token estimate", gotCandidate)
			}
			if packet.Budget.MemoryBodyMaxBytes != contextMemoryBodyMaxBytes || packet.Budget.MemoryCandidateBodyMaxBytes != contextCandidateBodyMaxBytes || packet.Budget.BodyTruncatedCount != 2 {
				t.Fatalf("context budget = %+v, want byte limits and two truncated bodies", packet.Budget)
			}
			if packet.Budget.BodyOriginalBytes != gotMemory.BodyOriginalBytes+gotCandidate.BodyOriginalBytes || packet.Budget.BodyReturnedBytes != gotMemory.BodyReturnedBytes+gotCandidate.BodyReturnedBytes || packet.Budget.BodyTokensEstimate != gotMemory.BodyTokensEstimate+gotCandidate.BodyTokensEstimate {
				t.Fatalf("context budget totals = %+v, want memory + candidate totals", packet.Budget)
			}
		})
	}
}

func TestService_DraftUsesContextSelection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newMemoryAssistantFixture(t)
	project := createTestProject(t, ctx, fixture.projects)
	role, err := fixture.work.CreateRole(ctx, projectwork.AgentRoleProfile{
		ID:                "planner",
		ProjectID:         project.ID,
		Name:              "Planning Lead",
		DefaultDriverKind: projectwork.AssignmentDriverExternalAgent,
	})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	workItem, err := fixture.work.CreateWorkItem(ctx, projectwork.WorkItem{
		ID:          "work_context_draft",
		ProjectID:   project.ID,
		Title:       "Draft from context",
		Status:      projectwork.WorkItemStatusReady,
		OwnerRoleID: role.ID,
	})
	if err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}

	proposal, err := fixture.service.Draft(ctx, DraftInput{
		ProjectID:  project.ID,
		WorkItemID: workItem.ID,
		Request:    "Queue selected owner",
	})
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	patch := rawPatchMap(t, proposal.Actions[0].Patch)
	if patch["role_id"] != role.ID || patch["driver_kind"] != projectwork.AssignmentDriverExternalAgent {
		t.Fatalf("patch = %+v, want context-selected owner role and role default driver", patch)
	}
}

func TestService_DraftModelUsesLLMProposal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newMemoryAssistantFixture(t)
	project := createTestProject(t, ctx, fixture.projects)
	if _, err := fixture.projects.Update(ctx, project.ID, func(project *projects.Project) {
		project.DefaultProvider = "ollama"
		project.DefaultModel = "llama3.1:8b"
	}); err != nil {
		t.Fatalf("set project defaults: %v", err)
	}
	role, err := fixture.work.CreateRole(ctx, projectwork.AgentRoleProfile{
		ID:                "planner",
		ProjectID:         project.ID,
		Name:              "Planning Lead",
		DefaultDriverKind: projectwork.AssignmentDriverExternalAgent,
	})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	workItem, err := fixture.work.CreateWorkItem(ctx, projectwork.WorkItem{
		ID:          "work_model",
		ProjectID:   project.ID,
		Title:       "Model-backed draft",
		Status:      projectwork.WorkItemStatusReady,
		OwnerRoleID: role.ID,
	})
	if err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	llm := &fakeAssistantLLM{
		response: `{
			"title": "Queue planning review",
			"summary": "Queue the selected role for the selected work item.",
			"actions": [{
				"kind": "create_assignment",
				"target": {"project_id": "proj_alpha"},
				"patch": {
					"project_id": "proj_alpha",
					"work_item_id": "work_model",
					"role_id": "planner",
					"driver_kind": "external_agent",
					"status": "queued"
				},
				"reason": "Queue reviewable work without starting execution."
			}]
		}`,
	}
	fixture.service.llm = llm

	proposal, err := fixture.service.Draft(ctx, DraftInput{
		ProjectID:  project.ID,
		WorkItemID: workItem.ID,
		Request:    "Queue planning review",
		DraftMode:  DraftModeModel,
		Provider:   "openai",
		Model:      "gpt-test",
		RequestID:  "req_model_draft",
		TraceID:    "trace_model_draft",
	})
	if err != nil {
		t.Fatalf("Draft model: %v", err)
	}
	if proposal.Title != "Queue planning review" || proposal.TraceID != "trace_model_draft" {
		t.Fatalf("proposal title/trace = %q/%q, want model title and trace", proposal.Title, proposal.TraceID)
	}
	if len(proposal.Actions) != 1 || proposal.Actions[0].Kind != ActionCreateAssignment {
		t.Fatalf("actions = %+v, want model create_assignment", proposal.Actions)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("llm requests = %d, want one", len(llm.requests))
	}
	req := llm.requests[0]
	if req.RequestID != "req_model_draft" || req.Model != "gpt-test" || req.Scope.ProviderHint != "openai" {
		t.Fatalf("llm request = %+v, want explicit request/model/provider", req)
	}
	if !strings.Contains(string(req.ResponseFormat), "json_object") || len(req.Messages) != 2 {
		t.Fatalf("llm response format/messages = %s/%d, want json object and system+user", string(req.ResponseFormat), len(req.Messages))
	}
	if !strings.Contains(req.Messages[1].Content, `"selected_work"`) || !strings.Contains(req.Messages[1].Content, `"selection"`) {
		t.Fatalf("llm prompt = %q, want selected work and selection context", req.Messages[1].Content)
	}
}

func TestService_DraftModelRejectsMissingModel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newMemoryAssistantFixture(t)
	project := createTestProject(t, ctx, fixture.projects)
	llm := &fakeAssistantLLM{response: `{}`}
	fixture.service.llm = llm

	_, err := fixture.service.Draft(ctx, DraftInput{
		ProjectID: project.ID,
		Request:   "Draft with no configured model",
		DraftMode: DraftModeModel,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Draft err = %v, want ErrInvalid", err)
	}
	if len(llm.requests) != 0 {
		t.Fatalf("llm requests = %d, want none when model is missing", len(llm.requests))
	}
}

func TestService_DraftModelRejectsOutOfScopeActions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name     string
		response string
	}{
		{
			name: "project action",
			response: `{
				"title": "Create project",
				"actions": [{
					"kind": "create_project",
					"target": {"project_id": "proj_alpha"},
					"patch": {"name": "Other project"}
				}]
			}`,
		},
		{
			name: "wrong project",
			response: `{
				"title": "Create work elsewhere",
				"actions": [{
					"kind": "create_work_item",
					"target": {"project_id": "proj_other"},
					"patch": {"project_id": "proj_other", "title": "Elsewhere"}
				}]
			}`,
		},
		{
			name: "assignment binds run",
			response: `{
				"title": "Bind existing run",
				"actions": [{
					"kind": "create_assignment",
					"target": {"project_id": "proj_alpha"},
					"patch": {
						"project_id": "proj_alpha",
						"work_item_id": "work_model_guard",
						"role_id": "planner",
						"driver_kind": "hecate_task",
						"status": "queued",
						"run_id": "run_existing"
					}
				}]
			}`,
		},
		{
			name: "memory candidate claims operator provenance",
			response: `{
				"title": "Remember generated note",
				"actions": [{
					"kind": "create_memory_candidate",
					"target": {"project_id": "proj_alpha"},
					"patch": {
						"project_id": "proj_alpha",
						"title": "Generated note",
						"body": "This was generated by a model draft.",
						"suggested_trust_label": "operator_memory",
						"suggested_source_kind": "operator"
					}
				}]
			}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := newMemoryAssistantFixture(t)
			project := createTestProject(t, ctx, fixture.projects)
			if _, err := fixture.projects.Update(ctx, project.ID, func(project *projects.Project) {
				project.DefaultModel = "llama3.1:8b"
			}); err != nil {
				t.Fatalf("set project defaults: %v", err)
			}
			role, err := fixture.work.CreateRole(ctx, projectwork.AgentRoleProfile{
				ID:                "planner",
				ProjectID:         project.ID,
				Name:              "Planning Lead",
				DefaultDriverKind: projectwork.AssignmentDriverHecateTask,
			})
			if err != nil {
				t.Fatalf("CreateRole: %v", err)
			}
			workItem, err := fixture.work.CreateWorkItem(ctx, projectwork.WorkItem{
				ID:          "work_model_guard",
				ProjectID:   project.ID,
				Title:       "Guard model draft",
				Status:      projectwork.WorkItemStatusReady,
				OwnerRoleID: role.ID,
			})
			if err != nil {
				t.Fatalf("CreateWorkItem: %v", err)
			}
			llm := &fakeAssistantLLM{response: tc.response}
			fixture.service.llm = llm

			_, err = fixture.service.Draft(ctx, DraftInput{
				ProjectID:  project.ID,
				WorkItemID: workItem.ID,
				Request:    "Draft guarded action",
				DraftMode:  DraftModeModel,
			})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Draft err = %v, want ErrInvalid", err)
			}
			if len(llm.requests) != 1 {
				t.Fatalf("llm requests = %d, want one", len(llm.requests))
			}
		})
	}
}

func TestDecodeModelDraftResponseExtractsWrappedJSON(t *testing.T) {
	t.Parallel()
	draft, err := decodeModelDraftResponse(&types.ChatResponse{
		Choices: []types.ChatChoice{{
			Message: types.Message{Role: "assistant", Content: "```json\n{\"proposal\":{\"title\":\"Wrapped\",\"actions\":[{\"kind\":\"create_work_item\",\"target\":{\"project_id\":\"proj_alpha\"},\"patch\":{\"project_id\":\"proj_alpha\",\"title\":\"Wrapped work\"}}]}}\n```\nTrailing prose with {another brace}."},
		}},
	})
	if err != nil {
		t.Fatalf("decodeModelDraftResponse: %v", err)
	}
	if draft.Title != "Wrapped" || len(draft.Actions) != 1 || draft.Actions[0].Kind != ActionCreateWorkItem {
		t.Fatalf("draft = %+v, want wrapped create_work_item", draft)
	}
}

func TestService_DraftRejectsUnsupportedDriverKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newMemoryAssistantFixture(t)
	project := createTestProject(t, ctx, fixture.projects)

	_, err := fixture.service.Draft(ctx, DraftInput{
		ProjectID:  project.ID,
		Request:    "Queue speculative work",
		DriverKind: "spreadsheet_macro",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Draft err = %v, want ErrInvalid", err)
	}
}

func TestService_ApplyRequiresConfirmation(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			proposal := Proposal{
				ID:                   "pa_confirm",
				Title:                "Create project",
				RequiresConfirmation: true,
				Actions: []Action{{
					Kind:  ActionCreateProject,
					Patch: rawPatch(t, map[string]string{"id": "proj_confirm", "name": "Confirm me"}),
				}},
			}

			_, err := fixture.service.Apply(ctx, proposal, false)
			if !errors.Is(err, ErrConfirmationRequired) {
				t.Fatalf("Apply err = %v, want ErrConfirmationRequired", err)
			}
			items, err := fixture.projects.List(ctx)
			if err != nil {
				t.Fatalf("list projects: %v", err)
			}
			if len(items) != 0 {
				t.Fatalf("projects = %+v, want none after unconfirmed apply", items)
			}
		})
	}
}

func TestService_ApplyCreateAndUpdateProjectAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			toolsEnabled := true
			compactToolOutput := true

			proposal := Proposal{
				ID:                   "pa_project",
				Title:                "Project setup",
				RequiresConfirmation: true,
				Actions: []Action{
					{
						Kind: ActionCreateProject,
						Patch: rawPatch(t, map[string]any{
							"id":          "proj_alpha",
							"name":        "Alpha",
							"description": "main",
							"roots": []map[string]any{{
								"id":   "root_a",
								"path": filepath.Join(t.TempDir(), "alpha"),
								"kind": "local",
							}},
						}),
					},
					{
						Kind:   ActionAttachProjectRoot,
						Target: map[string]string{"project_id": "proj_alpha"},
						Patch: rawPatch(t, map[string]any{
							"id":   "root_b",
							"path": filepath.Join(t.TempDir(), "beta"),
							"kind": "local",
						}),
					},
					{
						Kind:   ActionSetProjectDefaults,
						Target: map[string]string{"project_id": "proj_alpha"},
						Patch: rawPatch(t, map[string]any{
							"default_root_id":             "root_b",
							"default_provider":            "ollama",
							"default_model":               "llama3.1:8b",
							"default_agent_profile":       "reviewer",
							"default_tools_enabled":       toolsEnabled,
							"default_workspace_mode":      "worktree",
							"default_system_prompt":       "Stay crisp.",
							"default_compact_tool_output": compactToolOutput,
						}),
					},
					{
						Kind:   ActionRemoveProjectRoot,
						Target: map[string]string{"project_id": "proj_alpha", "root_id": "root_a"},
					},
				},
			}

			result, err := fixture.service.Apply(ctx, proposal, true)
			if err != nil {
				t.Fatalf("Apply setup: %v", err)
			}
			if !result.Applied || len(result.Actions) != len(proposal.Actions) {
				t.Fatalf("result = %+v, want all actions applied", result)
			}
			project, ok, err := fixture.projects.Get(ctx, "proj_alpha")
			if err != nil {
				t.Fatalf("get project: %v", err)
			}
			if !ok {
				t.Fatal("project not found after apply")
			}
			if project.Name != "Alpha" || project.Description != "main" {
				t.Fatalf("project metadata = %+v, want created metadata", project)
			}
			if len(project.Roots) != 1 || project.Roots[0].ID != "root_b" {
				t.Fatalf("project roots = %+v, want only root_b", project.Roots)
			}
			if project.DefaultRootID != "root_b" ||
				project.DefaultProvider != "ollama" ||
				project.DefaultModel != "llama3.1:8b" ||
				project.DefaultAgentProfile != "reviewer" ||
				project.DefaultWorkspaceMode != "worktree" ||
				project.DefaultSystemPrompt != "Stay crisp." {
				t.Fatalf("project defaults = %+v, want applied defaults", project)
			}
			if project.DefaultToolsEnabled == nil || !*project.DefaultToolsEnabled {
				t.Fatalf("default tools enabled = %v, want true", project.DefaultToolsEnabled)
			}
			if project.DefaultCompactToolOutput == nil || !*project.DefaultCompactToolOutput {
				t.Fatalf("default compact output = %v, want true", project.DefaultCompactToolOutput)
			}

			name := "Renamed"
			description := "Updated"
			_, err = fixture.service.Apply(ctx, Proposal{
				ID:                   "pa_project_update",
				RequiresConfirmation: true,
				Actions: []Action{{
					Kind:   ActionUpdateProject,
					Target: map[string]string{"project_id": "proj_alpha"},
					Patch:  rawPatch(t, map[string]any{"name": name, "description": description}),
				}},
			}, true)
			if err != nil {
				t.Fatalf("Apply update: %v", err)
			}
			project, ok, err = fixture.projects.Get(ctx, "proj_alpha")
			if err != nil || !ok {
				t.Fatalf("get updated project ok=%v err=%v", ok, err)
			}
			if project.Name != name || project.Description != description {
				t.Fatalf("updated project = %+v, want renamed/updated", project)
			}
		})
	}
}

func TestService_ApplyCreateProjectWorkspacePathAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			workspacePath := filepath.Join(t.TempDir(), "workspace")

			_, err := fixture.service.Apply(ctx, Proposal{
				ID:                   "pa_workspace_project",
				RequiresConfirmation: true,
				Actions: []Action{{
					Kind: ActionCreateProject,
					Patch: rawPatch(t, map[string]any{
						"id":             "proj_workspace",
						"name":           "Workspace",
						"workspace_path": workspacePath,
						"workspace_kind": "git",
					}),
				}},
			}, true)
			if err != nil {
				t.Fatalf("Apply create workspace project: %v", err)
			}

			project, ok, err := fixture.projects.Get(ctx, "proj_workspace")
			if err != nil || !ok {
				t.Fatalf("get project ok=%v err=%v", ok, err)
			}
			if len(project.Roots) != 1 {
				t.Fatalf("roots = %+v, want one generated workspace root", project.Roots)
			}
			root := project.Roots[0]
			if root.ID == "" || root.Path != workspacePath || root.Kind != "git" || !root.Active {
				t.Fatalf("root = %+v, want active git workspace root at %q", root, workspacePath)
			}
			if project.DefaultRootID != root.ID {
				t.Fatalf("default_root_id = %q, want generated root id %q", project.DefaultRootID, root.ID)
			}
		})
	}
}

func TestService_ApplyCreateProjectWithoutWorkspaceAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)

			_, err := fixture.service.Apply(ctx, Proposal{
				ID:                   "pa_no_workspace_project",
				RequiresConfirmation: true,
				Actions: []Action{{
					Kind: ActionCreateProject,
					Patch: rawPatch(t, map[string]string{
						"id":   "proj_no_workspace",
						"name": "No workspace",
					}),
				}},
			}, true)
			if err != nil {
				t.Fatalf("Apply create workspace-less project: %v", err)
			}

			project, ok, err := fixture.projects.Get(ctx, "proj_no_workspace")
			if err != nil || !ok {
				t.Fatalf("get project ok=%v err=%v", ok, err)
			}
			if len(project.Roots) != 0 || project.DefaultRootID != "" {
				t.Fatalf("project = %+v, want workspace-less project", project)
			}
		})
	}
}

func TestService_ApplyCreateProjectRejectsWorkspacePathWithRoots(t *testing.T) {
	t.Parallel()
	fixture := newMemoryAssistantFixture(t)

	_, err := fixture.service.Apply(context.Background(), Proposal{
		ID:                   "pa_workspace_conflict",
		RequiresConfirmation: true,
		Actions: []Action{{
			Kind: ActionCreateProject,
			Patch: rawPatch(t, map[string]any{
				"id":             "proj_workspace_conflict",
				"name":           "Broken",
				"workspace_path": filepath.Join(t.TempDir(), "workspace"),
				"roots": []map[string]string{{
					"path": filepath.Join(t.TempDir(), "other"),
				}},
			}),
		}},
	}, true)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Apply err = %v, want ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "workspace_path cannot be combined with roots") {
		t.Fatalf("Apply err = %v, want workspace_path conflict", err)
	}
}

func TestService_ApplyRevalidatesStaleTargets(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)

			cases := []struct {
				name   string
				action Action
			}{
				{
					name: "missing project",
					action: Action{
						Kind:   ActionSetProjectDefaults,
						Target: map[string]string{"project_id": "proj_missing"},
						Patch:  rawPatch(t, map[string]string{"default_model": "llama3.1:8b"}),
					},
				},
				{
					name: "missing root",
					action: Action{
						Kind:   ActionRemoveProjectRoot,
						Target: map[string]string{"project_id": project.ID, "root_id": "root_missing"},
					},
				},
				{
					name: "missing default root",
					action: Action{
						Kind:   ActionSetProjectDefaults,
						Target: map[string]string{"project_id": project.ID},
						Patch:  rawPatch(t, map[string]string{"default_root_id": "root_missing"}),
					},
				},
				{
					name: "missing chat",
					action: Action{
						Kind:   ActionMoveChatSession,
						Target: map[string]string{"chat_session_id": "chat_missing"},
						Patch:  rawPatch(t, map[string]string{"project_id": project.ID}),
					},
				},
			}

			for idx, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					_, err := fixture.service.Apply(ctx, Proposal{
						ID:                   fmt.Sprintf("pa_stale_%d", idx),
						RequiresConfirmation: true,
						Actions:              []Action{tc.action},
					}, true)
					if !errors.Is(err, ErrNotFound) {
						t.Fatalf("Apply err = %v, want ErrNotFound", err)
					}
				})
			}
		})
	}
}

func TestService_ApplyMoveChatSessionUpdatesOnlyTarget(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)
			createTestChatSession(t, ctx, fixture.chats, "chat_a")
			createTestChatSession(t, ctx, fixture.chats, "chat_b")

			_, err := fixture.service.Apply(ctx, Proposal{
				ID:                   "pa_move_chat",
				RequiresConfirmation: true,
				Actions: []Action{{
					Kind:   ActionMoveChatSession,
					Target: map[string]string{"chat_session_id": "chat_a"},
					Patch:  rawPatch(t, map[string]string{"project_id": project.ID}),
				}},
			}, true)
			if err != nil {
				t.Fatalf("Apply move chat: %v", err)
			}

			chatA, ok, err := fixture.chats.Get(ctx, "chat_a")
			if err != nil || !ok {
				t.Fatalf("get chat_a ok=%v err=%v", ok, err)
			}
			chatB, ok, err := fixture.chats.Get(ctx, "chat_b")
			if err != nil || !ok {
				t.Fatalf("get chat_b ok=%v err=%v", ok, err)
			}
			if chatA.ProjectID != project.ID {
				t.Fatalf("chat_a project = %q, want %q", chatA.ProjectID, project.ID)
			}
			if chatB.ProjectID != "" {
				t.Fatalf("chat_b project = %q, want unchanged empty project", chatB.ProjectID)
			}
		})
	}
}

func TestService_ApplyCreatesProjectWorkRecords(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)

			_, err := fixture.service.Apply(ctx, Proposal{
				ID:                   "pa_project_work",
				RequiresConfirmation: true,
				Actions: []Action{
					{
						Kind: ActionCreateWorkItem,
						Patch: rawPatch(t, map[string]any{
							"id":         "work_a",
							"project_id": project.ID,
							"title":      "Build assistant",
							"brief":      "Create typed project assistant actions.",
						}),
					},
					{
						Kind: ActionCreateAssignment,
						Patch: rawPatch(t, map[string]any{
							"id":           "asgn_a",
							"project_id":   project.ID,
							"work_item_id": "work_a",
							"role_id":      "software_developer",
							"driver_kind":  projectwork.AssignmentDriverHecateTask,
						}),
					},
					{
						Kind: ActionCreateHandoff,
						Patch: rawPatch(t, map[string]any{
							"id":                      "handoff_a",
							"project_id":              project.ID,
							"work_item_id":            "work_a",
							"title":                   "Continue implementation",
							"summary":                 "Project assistant core is ready for UI.",
							"recommended_next_action": "Add project cockpit cards.",
						}),
					},
				},
			}, true)
			if err != nil {
				t.Fatalf("Apply project work: %v", err)
			}

			item, ok, err := fixture.work.GetWorkItem(ctx, project.ID, "work_a")
			if err != nil || !ok {
				t.Fatalf("get work item ok=%v err=%v", ok, err)
			}
			if item.Title != "Build assistant" || item.Status != projectwork.WorkItemStatusBacklog {
				t.Fatalf("work item = %+v, want backlog item", item)
			}
			assignments, err := fixture.work.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: project.ID, WorkItemID: "work_a"})
			if err != nil {
				t.Fatalf("list assignments: %v", err)
			}
			if len(assignments) != 1 || assignments[0].ID != "asgn_a" || assignments[0].Status != projectwork.AssignmentStatusQueued {
				t.Fatalf("assignments = %+v, want queued assignment", assignments)
			}
			handoffs, err := fixture.work.ListHandoffs(ctx, projectwork.HandoffFilter{ProjectID: project.ID, WorkItemID: "work_a"})
			if err != nil {
				t.Fatalf("list handoffs: %v", err)
			}
			if len(handoffs) != 1 || handoffs[0].ID != "handoff_a" || handoffs[0].Status != projectwork.HandoffStatusPending {
				t.Fatalf("handoffs = %+v, want pending handoff", handoffs)
			}
		})
	}
}

func TestService_ApplyRejectsNonQueuedAssignmentProposal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name        string
		patch       map[string]any
		errContains string
	}{
		{
			name: "execution link",
			patch: map[string]any{
				"status":  projectwork.AssignmentStatusQueued,
				"task_id": "task_existing",
			},
			errContains: "cannot bind chats, tasks, runs",
		},
		{
			name: "running status",
			patch: map[string]any{
				"status": projectwork.AssignmentStatusRunning,
			},
			errContains: "must create queued assignments",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := newMemoryAssistantFixture(t)
			project := createTestProject(t, ctx, fixture.projects)
			if _, err := fixture.work.CreateWorkItem(ctx, projectwork.WorkItem{
				ID:        "work_execution_link",
				ProjectID: project.ID,
				Title:     "Reject assignment links",
				Status:    projectwork.WorkItemStatusReady,
			}); err != nil {
				t.Fatalf("CreateWorkItem: %v", err)
			}
			patch := map[string]any{
				"project_id":   project.ID,
				"work_item_id": "work_execution_link",
				"role_id":      "software_developer",
				"driver_kind":  projectwork.AssignmentDriverHecateTask,
			}
			for key, value := range tc.patch {
				patch[key] = value
			}

			_, err := fixture.service.Apply(ctx, Proposal{
				ID:                   "pa_assignment_links_" + tc.name,
				RequiresConfirmation: true,
				Actions: []Action{{
					Kind:  ActionCreateAssignment,
					Patch: rawPatch(t, patch),
				}},
			}, true)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Apply err = %v, want ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Fatalf("Apply err = %v, want %q", err, tc.errContains)
			}
			assignments, err := fixture.work.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: project.ID})
			if err != nil {
				t.Fatalf("ListAssignments: %v", err)
			}
			if len(assignments) != 0 {
				t.Fatalf("assignments = %+v, want none after rejected proposal", assignments)
			}
		})
	}
}

func TestService_ApplyCreatesMemoryCandidateOnly(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)

			_, err := fixture.service.Apply(ctx, Proposal{
				ID:                   "pa_memory_candidate",
				RequiresConfirmation: true,
				Actions: []Action{{
					Kind: ActionCreateMemoryCandidate,
					Patch: rawPatch(t, map[string]any{
						"id":             "memcand_a",
						"project_id":     project.ID,
						"title":          "Project assistant decision",
						"body":           "Project Assistant creates candidates, not durable entries.",
						"suggested_kind": "decision",
					}),
				}},
			}, true)
			if err != nil {
				t.Fatalf("Apply memory candidate: %v", err)
			}

			candidates, err := fixture.memoryCandidates.ListCandidates(ctx, memory.CandidateFilter{
				ProjectID: project.ID,
				Status:    memory.CandidateStatusPending,
			})
			if err != nil {
				t.Fatalf("list candidates: %v", err)
			}
			if len(candidates) != 1 || candidates[0].ID != "memcand_a" {
				t.Fatalf("candidates = %+v, want one pending candidate", candidates)
			}
			entries, err := fixture.memoryEntries.List(ctx, memory.Filter{ProjectID: project.ID, IncludeDisabled: true})
			if err != nil {
				t.Fatalf("list memory entries: %v", err)
			}
			if len(entries) != 0 {
				t.Fatalf("memory entries = %+v, want no durable entries", entries)
			}
		})
	}
}

func TestService_ApplyRepeatedProposalConflicts(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			proposal := Proposal{
				ID:                   "pa_repeat",
				RequiresConfirmation: true,
				Actions: []Action{{
					Kind:  ActionCreateProject,
					Patch: rawPatch(t, map[string]string{"id": "proj_repeat", "name": "Repeat"}),
				}},
			}

			if _, err := fixture.service.Apply(ctx, proposal, true); err != nil {
				t.Fatalf("first Apply: %v", err)
			}
			_, err := fixture.service.Apply(ctx, proposal, true)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("second Apply err = %v, want ErrConflict", err)
			}
		})
	}
}

func TestService_ApplyPartialFailureReturnsProgressAndResumesAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			proposal := Proposal{
				ID:                   "pa_partial_resume",
				RequiresConfirmation: true,
				Actions: []Action{
					{
						Kind:  ActionCreateProject,
						Patch: rawPatch(t, map[string]string{"id": "proj_partial", "name": "Partial"}),
					},
					{
						Kind: ActionCreateWorkItem,
						Patch: rawPatch(t, map[string]string{
							"id":         "work_after_retry",
							"project_id": "proj_after_retry",
							"title":      "Resume after missing project",
						}),
					},
				},
			}

			result, err := fixture.service.Apply(ctx, proposal, true)
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("Apply err = %v, want ErrNotFound", err)
			}
			var applyErr *ApplyError
			if !errors.As(err, &applyErr) {
				t.Fatalf("Apply err = %T %v, want ApplyError", err, err)
			}
			if applyErr.FailedActionIndex != 1 {
				t.Fatalf("failed_action_index = %d, want 1", applyErr.FailedActionIndex)
			}
			if result.Applied || len(result.Actions) != 1 || result.Actions[0].ID != "proj_partial" {
				t.Fatalf("partial result = %+v, want first action only", result)
			}
			if applyErr.Result.ProposalID != result.ProposalID || len(applyErr.Result.Actions) != len(result.Actions) {
				t.Fatalf("apply error result = %+v, want returned partial result %+v", applyErr.Result, result)
			}
			if _, ok, err := fixture.projects.Get(ctx, "proj_partial"); err != nil || !ok {
				t.Fatalf("get partially-created project ok=%v err=%v", ok, err)
			}

			if _, err := fixture.projects.Create(ctx, projects.Project{ID: "proj_after_retry", Name: "After retry"}); err != nil {
				t.Fatalf("create missing project before retry: %v", err)
			}
			result, err = fixture.service.Apply(ctx, proposal, true)
			if err != nil {
				t.Fatalf("retry Apply: %v", err)
			}
			if !result.Applied || len(result.Actions) != 2 {
				t.Fatalf("retry result = %+v, want both actions applied", result)
			}
			item, ok, err := fixture.work.GetWorkItem(ctx, "proj_after_retry", "work_after_retry")
			if err != nil || !ok {
				t.Fatalf("get work item ok=%v err=%v", ok, err)
			}
			if item.Title != "Resume after missing project" {
				t.Fatalf("work item title = %q, want resumed action title", item.Title)
			}

			projectsAfterRetry, err := fixture.projects.List(ctx)
			if err != nil {
				t.Fatalf("list projects: %v", err)
			}
			var partialProjectCount int
			for _, project := range projectsAfterRetry {
				if project.ID == "proj_partial" {
					partialProjectCount++
				}
			}
			if partialProjectCount != 1 {
				t.Fatalf("proj_partial count = %d, want one non-duplicated project", partialProjectCount)
			}
		})
	}
}

func TestService_ApplyChangedProposalAfterPartialFailureConflictsAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			proposal := Proposal{
				ID:                   "pa_partial_changed",
				RequiresConfirmation: true,
				Actions: []Action{
					{
						Kind:  ActionCreateProject,
						Patch: rawPatch(t, map[string]string{"id": "proj_partial_changed", "name": "Partial"}),
					},
					{
						Kind: ActionCreateWorkItem,
						Patch: rawPatch(t, map[string]string{
							"id":         "work_changed",
							"project_id": "proj_missing_changed",
							"title":      "Original title",
						}),
					},
				},
			}
			if _, err := fixture.service.Apply(ctx, proposal, true); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Apply err = %v, want ErrNotFound", err)
			}

			changed := proposal
			changed.Actions = cloneActions(proposal.Actions)
			changed.Actions[1].Patch = rawPatch(t, map[string]string{
				"id":         "work_changed",
				"project_id": "proj_missing_changed",
				"title":      "Changed title",
			})
			_, err := fixture.service.Apply(ctx, changed, true)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("changed Apply err = %v, want ErrConflict", err)
			}
		})
	}
}

func assistantFixtureBuilders() []assistantFixtureBuilder {
	return []assistantFixtureBuilder{
		{name: "memory", build: newMemoryAssistantFixture},
		{name: "sqlite", build: newSQLiteAssistantFixture},
	}
}

func newMemoryAssistantFixture(t *testing.T) assistantFixture {
	t.Helper()
	projectStore := projects.NewMemoryStore()
	chatStore := chat.NewMemoryStore()
	workStore := projectwork.NewMemoryStore()
	memoryStore := memory.NewMemoryStore()
	return assistantFixture{
		service:          projectassistantService(projectStore, chatStore, workStore, memoryStore),
		projects:         projectStore,
		chats:            chatStore,
		work:             workStore,
		memoryEntries:    memoryStore,
		memoryCandidates: memoryStore,
	}
}

func newSQLiteAssistantFixture(t *testing.T) assistantFixture {
	t.Helper()
	ctx := context.Background()
	client, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "hecate.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("new sqlite client: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Fatalf("close sqlite client: %v", err)
		}
	})
	projectStore, err := projects.NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("new project sqlite store: %v", err)
	}
	chatStore, err := chat.NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("new chat sqlite store: %v", err)
	}
	workStore, err := projectwork.NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("new project work sqlite store: %v", err)
	}
	memoryStore, err := memory.NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("new memory sqlite store: %v", err)
	}
	return assistantFixture{
		service:          projectassistantService(projectStore, chatStore, workStore, memoryStore),
		projects:         projectStore,
		chats:            chatStore,
		work:             workStore,
		memoryEntries:    memoryStore,
		memoryCandidates: memoryStore,
	}
}

func projectassistantService(projectStore projects.Store, chatStore chat.Store, workStore projectwork.Store, memoryStore memory.Store) *Service {
	var candidateStore memory.CandidateStore
	if candidates, ok := memoryStore.(memory.CandidateStore); ok {
		candidateStore = candidates
	}
	return NewService(Stores{
		Projects:         projectStore,
		Chats:            chatStore,
		Work:             workStore,
		Memory:           memoryStore,
		MemoryCandidates: candidateStore,
	}, sequenceIDGenerator())
}

func sequenceIDGenerator() IDGenerator {
	var seq int
	return func(prefix string) string {
		seq++
		return fmt.Sprintf("%s_%02d", prefix, seq)
	}
}

func rawPatch(t *testing.T, value any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal patch: %v", err)
	}
	return payload
}

func rawPatchMap(t *testing.T, payload json.RawMessage) map[string]string {
	t.Helper()
	var decoded map[string]string
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	return decoded
}

func contextRoleExists(roles []RoleContext, id string) bool {
	for _, role := range roles {
		if role.ID == id {
			return true
		}
	}
	return false
}

func createTestProject(t *testing.T, ctx context.Context, store projects.Store) projects.Project {
	t.Helper()
	project, err := store.Create(ctx, projects.Project{
		ID:   "proj_alpha",
		Name: "Alpha",
		Roots: []projects.Root{{
			ID:     "root_a",
			Path:   filepath.Join(t.TempDir(), "alpha"),
			Kind:   "local",
			Active: true,
		}},
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return project
}

func createTestChatSession(t *testing.T, ctx context.Context, store chat.Store, id string) chat.Session {
	t.Helper()
	session, err := store.Create(ctx, chat.Session{ID: id, Title: id})
	if err != nil {
		t.Fatalf("create chat session %q: %v", id, err)
	}
	return session
}
