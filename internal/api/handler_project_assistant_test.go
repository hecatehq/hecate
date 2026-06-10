package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
)

type projectAssistantProposalResponse struct {
	Object string                    `json:"object"`
	Data   projectassistant.Proposal `json:"data"`
}

type projectAssistantApplyResponse struct {
	Object string                       `json:"object"`
	Data   projectassistant.ApplyResult `json:"data"`
}

type projectAssistantContextResponse struct {
	Object string                        `json:"object"`
	Data   projectassistant.DraftContext `json:"data"`
}

type projectAssistantErrorResponse struct {
	Error struct {
		Type              string                       `json:"type"`
		Message           string                       `json:"message"`
		FailedActionIndex int                          `json:"failed_action_index"`
		PartialResult     projectassistant.ApplyResult `json:"partial_result"`
	} `json:"error"`
}

func newProjectAssistantTestServer() http.Handler {
	_, server := newProjectAssistantTestHandler()
	return server
}

func newProjectAssistantTestHandler() (*Handler, http.Handler) {
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetAgentChatStore(chat.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	return handler, NewServer(quietLogger(), handler)
}

func TestProjectAssistantAPI_ContextBuildsSelectionPacket(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantTestHandler()
	project, err := handler.projects.Create(t.Context(), projects.Project{ID: "proj_context", Name: "Context Project"})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	role, err := handler.projectWork.CreateRole(t.Context(), projectwork.AgentRoleProfile{
		ID:                "planner",
		ProjectID:         project.ID,
		Name:              "Planning Lead",
		DefaultDriverKind: projectwork.AssignmentDriverExternalAgent,
	})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	workItem, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:          "work_context",
		ProjectID:   project.ID,
		Title:       "Plan context",
		Status:      projectwork.WorkItemStatusReady,
		OwnerRoleID: role.ID,
	})
	if err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if _, err := handler.memory.Create(t.Context(), memory.Entry{
		ID:         "mem_context",
		ProjectID:  project.ID,
		Title:      "Context decision",
		Body:       "Expose assistant context before model drafting.",
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Create memory: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/context", bytes.NewReader([]byte(`{
		"project_id":"proj_context",
		"work_item_id":"work_context",
		"request":"Queue planning"
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("context status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response projectAssistantContextResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode context response: %v", err)
	}
	if response.Object != "project_assistant.context" || response.Data.Project.ID != project.ID || response.Data.SelectedWork == nil || response.Data.SelectedWork.ID != workItem.ID {
		t.Fatalf("context response = %+v, want project assistant context with selected work", response)
	}
	if response.Data.Selection.RoleID != role.ID || response.Data.Selection.DriverKind != projectwork.AssignmentDriverExternalAgent || response.Data.Selection.RoleSource != "selected_work_owner" {
		t.Fatalf("selection = %+v, want owner role and external driver", response.Data.Selection)
	}
	if len(response.Data.Memory) != 1 || response.Data.Memory[0].ID != "mem_context" {
		t.Fatalf("memory = %+v, want memory entry included", response.Data.Memory)
	}
}

func TestProjectAssistantAPI_ContextBudgetsMemoryBodies(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantTestHandler()
	project, err := handler.projects.Create(t.Context(), projects.Project{ID: "proj_budget", Name: "Budget Project"})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	memoryBody := strings.Repeat("é", 6000)
	candidateBody := strings.Repeat("é", 3000)
	if _, err := handler.memory.Create(t.Context(), memory.Entry{
		ID:         "mem_budget",
		ProjectID:  project.ID,
		Title:      "Large context memory",
		Body:       memoryBody,
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Create memory: %v", err)
	}
	if _, err := handler.memoryCandidates.CreateCandidate(t.Context(), memory.Candidate{
		ID:                  "cand_budget",
		ProjectID:           project.ID,
		Title:               "Large context candidate",
		Body:                candidateBody,
		SuggestedTrustLabel: memory.TrustLabelGenerated,
		SuggestedSourceKind: memory.SourceKindGenerated,
		Status:              memory.CandidateStatusPending,
	}); err != nil {
		t.Fatalf("Create candidate: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/context", bytes.NewReader([]byte(`{
		"project_id":"proj_budget",
		"request":"Inspect context budget"
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("context status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response projectAssistantContextResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode context response: %v", err)
	}
	if len(response.Data.Memory) != 1 || len(response.Data.MemoryCandidates) != 1 {
		t.Fatalf("memory/candidates = %+v/%+v, want one of each", response.Data.Memory, response.Data.MemoryCandidates)
	}
	gotMemory := response.Data.Memory[0]
	if !gotMemory.BodyTruncated || gotMemory.BodyOriginalBytes != len(memoryBody) || gotMemory.BodyReturnedBytes != len(gotMemory.Body) {
		t.Fatalf("memory budget = %+v, want truncated body with original and returned byte counts", gotMemory)
	}
	if !strings.HasSuffix(gotMemory.Body, "\n...[truncated]") || !utf8.ValidString(gotMemory.Body) {
		t.Fatalf("memory body suffix/utf8 = %v/%v, want truncated suffix and valid utf8", strings.HasSuffix(gotMemory.Body, "\n...[truncated]"), utf8.ValidString(gotMemory.Body))
	}
	gotCandidate := response.Data.MemoryCandidates[0]
	if !gotCandidate.BodyTruncated || gotCandidate.BodyOriginalBytes != len(candidateBody) || gotCandidate.BodyReturnedBytes != len(gotCandidate.Body) {
		t.Fatalf("candidate budget = %+v, want truncated body with original and returned byte counts", gotCandidate)
	}
	if !strings.HasSuffix(gotCandidate.Body, "\n...[truncated]") || !utf8.ValidString(gotCandidate.Body) {
		t.Fatalf("candidate body suffix/utf8 = %v/%v, want truncated suffix and valid utf8", strings.HasSuffix(gotCandidate.Body, "\n...[truncated]"), utf8.ValidString(gotCandidate.Body))
	}
	if response.Data.Budget.BodyTruncatedCount != 2 || response.Data.Budget.BodyOriginalBytes != gotMemory.BodyOriginalBytes+gotCandidate.BodyOriginalBytes || response.Data.Budget.BodyReturnedBytes != gotMemory.BodyReturnedBytes+gotCandidate.BodyReturnedBytes {
		t.Fatalf("context budget = %+v, want aggregate body metadata", response.Data.Budget)
	}
}

func TestProjectAssistantAPI_DraftCreatesAssignmentProposal(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantTestHandler()
	project, err := handler.projects.Create(t.Context(), projects.Project{ID: "proj_api", Name: "API Project"})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	workItem, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:          "work_api",
		ProjectID:   project.ID,
		Title:       "Plan next work",
		Brief:       "Pick the next reviewable task.",
		Status:      projectwork.WorkItemStatusReady,
		OwnerRoleID: "product_manager",
	})
	if err != nil {
		t.Fatalf("Create work item: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/draft", bytes.NewReader([]byte(`{
		"project_id":"proj_api",
		"work_item_id":"work_api",
		"request":"Queue Product Manager\nPrepare acceptance criteria.",
		"driver_kind":"external_agent"
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("draft status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var proposed projectAssistantProposalResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &proposed); err != nil {
		t.Fatalf("decode draft response: %v", err)
	}
	if proposed.Object != "project_assistant.proposal" || proposed.Data.ID == "" {
		t.Fatalf("draft response = %+v, want proposal envelope with id", proposed)
	}
	if proposed.Data.Title != "Queue Product Manager" || len(proposed.Data.Actions) != 1 || proposed.Data.Actions[0].Kind != projectassistant.ActionCreateAssignment {
		t.Fatalf("proposal = %+v, want one assignment proposal with request title", proposed.Data)
	}
	var patch map[string]string
	if err := json.Unmarshal(proposed.Data.Actions[0].Patch, &patch); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patch["project_id"] != project.ID || patch["work_item_id"] != workItem.ID || patch["role_id"] != "product_manager" || patch["driver_kind"] != projectwork.AssignmentDriverExternalAgent {
		t.Fatalf("patch = %+v, want selected project/work/owner role/external driver", patch)
	}
}

func TestProjectAssistantAPI_DraftBootstrapProposal(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantTestHandler()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".hecate", "skills", "research"), 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hecate", "skills", "research", "SKILL.md"), []byte("# Research\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	_, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_bootstrap_api",
		Name: "Bootstrap API Project",
		Roots: []projects.Root{{
			ID:     "root_bootstrap_api",
			Path:   root,
			Kind:   "git",
			Active: true,
		}},
		ContextSources: []projects.ContextSource{{
			ID:             "ctx_agents_api",
			Kind:           "workspace_instruction",
			Title:          "AGENTS.md",
			Path:           "AGENTS.md",
			Enabled:        true,
			Format:         "agents_md",
			Scope:          "workspace",
			TrustLabel:     "workspace_guidance",
			SourceCategory: "workspace_guidance",
			Metadata:       map[string]string{"root_id": "root_bootstrap_api"},
		}},
	})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}

	discoverRec := httptest.NewRecorder()
	server.ServeHTTP(discoverRec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_bootstrap_api/skills/discover", nil))
	if discoverRec.Code != http.StatusOK {
		t.Fatalf("discover skills status = %d body=%s, want 200", discoverRec.Code, discoverRec.Body.String())
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/draft", bytes.NewReader([]byte(`{
		"project_id":"proj_bootstrap_api",
		"request":"Bootstrap project guidance",
		"draft_mode":"bootstrap"
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("draft bootstrap status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var proposed projectAssistantProposalResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &proposed); err != nil {
		t.Fatalf("decode draft response: %v", err)
	}
	if proposed.Object != "project_assistant.proposal" || proposed.Data.ID == "" {
		t.Fatalf("draft response = %+v, want proposal envelope with id", proposed)
	}
	if len(proposed.Data.Actions) != 2 {
		t.Fatalf("actions = %+v, want guidance candidate and skill role", proposed.Data.Actions)
	}
	if proposed.Data.Actions[0].Kind != projectassistant.ActionCreateMemoryCandidate || proposed.Data.Actions[1].Kind != projectassistant.ActionCreateRole {
		t.Fatalf("action kinds = %s/%s, want memory candidate then role", proposed.Data.Actions[0].Kind, proposed.Data.Actions[1].Kind)
	}
	var memoryPatch map[string]any
	if err := json.Unmarshal(proposed.Data.Actions[0].Patch, &memoryPatch); err != nil {
		t.Fatalf("decode memory patch: %v", err)
	}
	if memoryPatch["suggested_source_kind"] != "context_source" || memoryPatch["suggested_source_id"] != "ctx_agents_api" {
		t.Fatalf("memory patch = %+v, want context-source provenance", memoryPatch)
	}
	var rolePatch map[string]any
	if err := json.Unmarshal(proposed.Data.Actions[1].Patch, &rolePatch); err != nil {
		t.Fatalf("decode role patch: %v", err)
	}
	if rolePatch["id"] != "skill_research" || rolePatch["name"] != "Research" {
		t.Fatalf("role patch = %+v, want skill-derived role", rolePatch)
	}
	skillIDs, _ := rolePatch["skill_ids"].([]any)
	if len(skillIDs) != 1 || skillIDs[0] != "research" {
		t.Fatalf("role skill ids = %+v, want research", rolePatch["skill_ids"])
	}
}

func TestProjectAssistantAPI_ProposeRejectsUnknownActionKind(t *testing.T) {
	t.Parallel()
	server := newProjectAssistantTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/propose", strings.NewReader(`{
		"actions":[{"kind":"rewrite_the_world","patch":{"name":"bad"}}]
	}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("propose status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unknown project assistant action kind") {
		t.Fatalf("propose body = %s, want unknown action error", rec.Body.String())
	}
}

func TestProjectAssistantAPI_ProposeRejectsAssignmentBoundaryViolations(t *testing.T) {
	t.Parallel()
	server := newProjectAssistantTestServer()

	cases := []struct {
		name     string
		patch    string
		contains string
	}{
		{
			name:     "execution link",
			patch:    `"project_id":"proj_api","work_item_id":"work_api","role_id":"developer","driver_kind":"hecate_task","status":"queued","task_id":"task_existing"`,
			contains: "cannot bind chats, tasks, runs",
		},
		{
			name:     "non queued status",
			patch:    `"project_id":"proj_api","work_item_id":"work_api","role_id":"developer","driver_kind":"hecate_task","status":"running"`,
			contains: "must create queued assignments",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := `{"actions":[{"kind":"create_assignment","patch":{` + tc.patch + `}}]}`
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/propose", strings.NewReader(body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("propose status = %d body=%s, want 400", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.contains) {
				t.Fatalf("propose body = %s, want %q", rec.Body.String(), tc.contains)
			}
		})
	}
}

func TestProjectAssistantAPI_ProposeAndApplyCreateProject(t *testing.T) {
	t.Parallel()
	server := newProjectAssistantTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/propose", bytes.NewReader([]byte(`{
		"id":"pa_api",
		"title":"Create API project",
		"summary":"Create a project from a typed assistant action.",
		"actions":[{
			"kind":"create_project",
			"reason":"Operator asked for a new project.",
			"patch":{
				"id":"proj_api",
				"name":"API Project",
				"description":"Created through Project Assistant",
				"workspace_path":"/tmp/hecate-api-project",
				"workspace_kind":"git"
			}
		}]
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("propose status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var proposed projectAssistantProposalResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &proposed); err != nil {
		t.Fatalf("decode propose response: %v", err)
	}
	if proposed.Object != "project_assistant.proposal" || proposed.Data.ID != "pa_api" {
		t.Fatalf("propose response = %+v, want project assistant proposal envelope", proposed)
	}
	if !proposed.Data.RequiresConfirmation {
		t.Fatalf("requires_confirmation = false, want true")
	}
	if len(proposed.Data.Actions) != 1 || proposed.Data.Actions[0].Kind != projectassistant.ActionCreateProject {
		t.Fatalf("proposal actions = %+v, want create_project action", proposed.Data.Actions)
	}

	applyBody, err := json.Marshal(map[string]any{"proposal": proposed.Data})
	if err != nil {
		t.Fatalf("marshal apply body: %v", err)
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/apply", bytes.NewReader(applyBody)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("unconfirmed apply status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}

	applyBody, err = json.Marshal(map[string]any{"proposal": proposed.Data, "confirm": true})
	if err != nil {
		t.Fatalf("marshal confirmed apply body: %v", err)
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/apply", bytes.NewReader(applyBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("confirmed apply status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var applied projectAssistantApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &applied); err != nil {
		t.Fatalf("decode apply response: %v", err)
	}
	if applied.Object != "project_assistant.apply_result" || !applied.Data.Applied || applied.Data.ProposalID != "pa_api" {
		t.Fatalf("apply response = %+v, want applied project assistant result", applied)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_api", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get project status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var project ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &project); err != nil {
		t.Fatalf("decode project response: %v", err)
	}
	if project.Data.Name != "API Project" || project.Data.Description != "Created through Project Assistant" {
		t.Fatalf("project = %+v, want assistant-created metadata", project.Data)
	}
	if len(project.Data.Roots) != 1 {
		t.Fatalf("roots = %+v, want generated workspace root", project.Data.Roots)
	}
	root := project.Data.Roots[0]
	if root.Path != "/tmp/hecate-api-project" || root.Kind != "git" || !root.Active || project.Data.DefaultRootID != root.ID {
		t.Fatalf("root = %+v default_root_id=%q, want generated default workspace root", root, project.Data.DefaultRootID)
	}
}

func TestProjectAssistantAPI_ApplyPartialFailureIncludesProgress(t *testing.T) {
	t.Parallel()
	server := newProjectAssistantTestServer()
	proposal := projectassistant.Proposal{
		ID:                   "pa_partial_api",
		Title:                "Partial apply",
		RequiresConfirmation: true,
		Actions: []projectassistant.Action{
			{
				Kind:  projectassistant.ActionCreateProject,
				Patch: json.RawMessage(`{"id":"proj_partial_api","name":"Partial API"}`),
			},
			{
				Kind: projectassistant.ActionCreateWorkItem,
				Patch: json.RawMessage(`{
					"id":"work_missing_project",
					"project_id":"proj_missing_api",
					"title":"Cannot create yet"
				}`),
			},
		},
	}
	applyBody, err := json.Marshal(map[string]any{"proposal": proposal, "confirm": true})
	if err != nil {
		t.Fatalf("marshal apply body: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/apply", bytes.NewReader(applyBody)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("partial apply status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
	var payload projectAssistantErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode partial apply error: %v", err)
	}
	if payload.Error.Type != errCodeNotFound || payload.Error.FailedActionIndex != 1 {
		t.Fatalf("error = %+v, want not_found at action index 1", payload.Error)
	}
	partial := payload.Error.PartialResult
	if partial.ProposalID != "pa_partial_api" || partial.Applied || len(partial.Actions) != 1 {
		t.Fatalf("partial_result = %+v, want one unapplied partial result", partial)
	}
	if partial.Actions[0].Kind != projectassistant.ActionCreateProject || partial.Actions[0].ID != "proj_partial_api" {
		t.Fatalf("partial action = %+v, want created project action", partial.Actions[0])
	}
}

func TestProjectAssistantAPI_RepeatedApplyConflicts(t *testing.T) {
	t.Parallel()
	server := newProjectAssistantTestServer()
	proposal := projectassistant.Proposal{
		ID:                   "pa_repeat_api",
		Title:                "Create once",
		RequiresConfirmation: true,
		Actions: []projectassistant.Action{{
			Kind:  projectassistant.ActionCreateProject,
			Patch: json.RawMessage(`{"id":"proj_repeat_api","name":"Repeat"}`),
		}},
	}
	applyBody, err := json.Marshal(map[string]any{"proposal": proposal, "confirm": true})
	if err != nil {
		t.Fatalf("marshal apply body: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/apply", bytes.NewReader(applyBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("first apply status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/apply", bytes.NewReader(applyBody)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("second apply status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
}
