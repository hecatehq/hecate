package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projectassistantapp"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

type projectAssistantProposalResponse struct {
	Object string                    `json:"object"`
	Data   projectassistant.Proposal `json:"data"`
}

type projectAssistantProposalRecordResponse struct {
	Object string                          `json:"object"`
	Data   projectassistant.ProposalRecord `json:"data"`
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
		Type                 string                       `json:"type"`
		Message              string                       `json:"message"`
		ApplyStatus          string                       `json:"apply_status"`
		FailedActionIndex    int                          `json:"failed_action_index"`
		TotalActionCount     int                          `json:"total_action_count"`
		CommittedActionCount int                          `json:"committed_action_count"`
		ResumeActionIndex    int                          `json:"resume_action_index"`
		PartialResult        projectassistant.ApplyResult `json:"partial_result"`
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

func newProjectAssistantCairnlineReadTestHandler() (*Handler, http.Handler) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetAgentChatStore(chat.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetProjectSkillStore(projectskills.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	handler.SetProjectAssistantProposalStore(projectassistant.NewMemoryProposalStore())
	return handler, NewServer(quietLogger(), handler)
}

func newProjectAssistantCairnlineMirrorTestHandler(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	handler := NewHandler(config.Config{
		Server:   config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetAgentChatStore(chat.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetProjectSkillStore(projectskills.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	handler.SetProjectAssistantProposalStore(projectassistant.NewMemoryProposalStore())
	return handler, NewServer(quietLogger(), handler)
}

func poisonNativeProjectAssistantCache(handler *Handler) {
	handler.projectAssistantMu.Lock()
	defer handler.projectAssistantMu.Unlock()
	handler.projectAssistant = projectassistantapp.New(projectassistantapp.Options{})
}

type failingCreateAssignmentProjectWorkStore struct {
	projectwork.Store
	err error
}

func (s failingCreateAssignmentProjectWorkStore) CreateAssignment(context.Context, projectwork.Assignment) (projectwork.Assignment, error) {
	return projectwork.Assignment{}, s.err
}

type failingCreateRoleProjectWorkStore struct {
	projectwork.Store
	err error
}

func (s failingCreateRoleProjectWorkStore) CreateRole(context.Context, projectwork.AgentRoleProfile) (projectwork.AgentRoleProfile, error) {
	return projectwork.AgentRoleProfile{}, s.err
}

type failingUpdateProjectStore struct {
	projects.Store
	err error
}

func (s failingUpdateProjectStore) Update(context.Context, string, func(*projects.Project)) (projects.Project, error) {
	return projects.Project{}, s.err
}

type failingCreateMemoryCandidateStore struct {
	*memory.MemoryStore
	err error
}

func (s failingCreateMemoryCandidateStore) CreateCandidate(context.Context, memory.Candidate) (memory.Candidate, error) {
	return memory.Candidate{}, s.err
}

type failingProposalUpsertStore struct {
	projectassistant.ProposalStore
	err error
}

func (s failingProposalUpsertStore) Backend() string { return "failing" }

func (s failingProposalUpsertStore) UpsertProposal(context.Context, projectassistant.ProposalRecord) (projectassistant.ProposalRecord, error) {
	return projectassistant.ProposalRecord{}, s.err
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
	if response.Data.ReadBackend != "hecate" {
		t.Fatalf("read_backend = %q, want hecate", response.Data.ReadBackend)
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

func TestProjectAssistantAPI_ContextUsesCairnlineReadModelWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantCairnlineReadTestHandler()

	project, err := handler.projects.Create(t.Context(), projects.Project{
		ID:              "proj_context_cairnline",
		Name:            "Cairnline Context",
		Description:     "Read model context fixture",
		DefaultProvider: "openai",
		DefaultModel:    "gpt-5",
		DefaultRootID:   "root_cairnline",
		Roots: []projects.Root{{
			ID:     "root_cairnline",
			Path:   t.TempDir(),
			Kind:   "git",
			Active: true,
		}},
		ContextSources: []projects.ContextSource{{
			ID:         "ctx_cairnline_agents",
			Kind:       "workspace_instruction",
			Title:      "AGENTS.md",
			Path:       "AGENTS.md",
			Enabled:    true,
			Format:     "agents_md",
			TrustLabel: "workspace_guidance",
		}},
	})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	role, err := handler.projectWork.CreateRole(t.Context(), projectwork.AgentRoleProfile{
		ID:                "planner_cairnline",
		ProjectID:         project.ID,
		Name:              "Planner",
		DefaultDriverKind: projectwork.AssignmentDriverExternalAgent,
		SkillIDs:          []string{"planning"},
	})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	workItem, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:          "work_context_cairnline",
		ProjectID:   project.ID,
		Title:       "Plan through Cairnline",
		Status:      projectwork.WorkItemStatusReady,
		OwnerRoleID: role.ID,
		RootID:      "root_cairnline",
	})
	if err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(t.Context(), projectwork.Assignment{
		ID:         "asgn_context_cairnline",
		ProjectID:  project.ID,
		WorkItemID: workItem.ID,
		RoleID:     role.ID,
		RootID:     workItem.RootID,
		DriverKind: projectwork.AssignmentDriverExternalAgent,
		Status:     projectwork.AssignmentStatusQueued,
	}); err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}
	if _, err := handler.projectSkills.UpsertDiscovered(t.Context(), project.ID, []projectskills.Skill{{
		ID:                     "planning",
		ProjectID:              project.ID,
		Title:                  "Planning skill",
		Description:            "Shape reviewable project work.",
		Path:                   ".agents/skills/planning/SKILL.md",
		Format:                 projectskills.FormatSkillMD,
		Enabled:                true,
		Status:                 projectskills.StatusAvailable,
		TrustLabel:             projectskills.TrustWorkspaceSkill,
		SourceContextSourceIDs: []string{"ctx_cairnline_agents"},
	}}); err != nil {
		t.Fatalf("UpsertDiscovered: %v", err)
	}
	if _, err := handler.memory.Create(t.Context(), memory.Entry{
		ID:         "mem_context_cairnline",
		ProjectID:  project.ID,
		Title:      "Cairnline memory",
		Body:       "Assistant context should read memory through the Cairnline projection.",
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Create memory: %v", err)
	}
	if _, err := handler.memoryCandidates.CreateCandidate(t.Context(), memory.Candidate{
		ID:                  "cand_context_cairnline",
		ProjectID:           project.ID,
		Title:               "Pending Cairnline candidate",
		Body:                "Pending candidates should remain visible.",
		SuggestedTrustLabel: memory.TrustLabelGenerated,
		SuggestedSourceKind: memory.SourceKindGenerated,
		Status:              memory.CandidateStatusPending,
	}); err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/context", bytes.NewReader([]byte(`{
		"project_id":"proj_context_cairnline",
		"work_item_id":"work_context_cairnline",
		"request":"Queue planning"
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("context status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response projectAssistantContextResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode context response: %v", err)
	}
	if response.Data.ReadBackend != "cairnline" {
		t.Fatalf("read_backend = %q, want cairnline", response.Data.ReadBackend)
	}
	if response.Data.Project.ID != project.ID || response.Data.Project.DefaultModel != "gpt-5" {
		t.Fatalf("context project = %+v, want Cairnline-projected project defaults", response.Data.Project)
	}
	if response.Data.SelectedWork == nil || response.Data.SelectedWork.ID != workItem.ID || response.Data.Selection.RoleID != role.ID || response.Data.Selection.DriverKind != projectwork.AssignmentDriverExternalAgent {
		t.Fatalf("selected work/selection = %+v/%+v, want Cairnline-projected work owner and driver", response.Data.SelectedWork, response.Data.Selection)
	}
	if len(response.Data.Skills) != 1 || response.Data.Skills[0].ID != "planning" || len(response.Data.Skills[0].SourceContextSourceIDs) != 1 {
		t.Fatalf("skills = %+v, want Cairnline-projected skill metadata and provenance", response.Data.Skills)
	}
	if len(response.Data.Assignments) != 1 || response.Data.Assignments[0].ID != "asgn_context_cairnline" {
		t.Fatalf("assignments = %+v, want Cairnline-projected assignment", response.Data.Assignments)
	}
	if len(response.Data.Memory) != 1 || response.Data.Memory[0].ID != "mem_context_cairnline" || len(response.Data.MemoryCandidates) != 1 || response.Data.MemoryCandidates[0].ID != "cand_context_cairnline" {
		t.Fatalf("memory/candidates = %+v/%+v, want Cairnline-projected memory context", response.Data.Memory, response.Data.MemoryCandidates)
	}
}

func TestProjectAssistantAPI_ContextUsesCairnlineSidecarWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineSidecarReadTestServer(t, "memory-fixture")
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar assistant context enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("sidecar read-route predicate = false, want true")
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/context", bytes.NewReader([]byte(`{
		"project_id":"proj_fixture",
		"work_item_id":"work_fixture",
		"request":"Queue fixture work",
		"role_id":"role_fixture"
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("context status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response projectAssistantContextResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode context response: %v", err)
	}
	if response.Data.ReadBackend != "cairnline" || response.Data.Project.ID != "proj_fixture" {
		t.Fatalf("context backend/project = %q/%+v, want Cairnline sidecar fixture project", response.Data.ReadBackend, response.Data.Project)
	}
	if response.Data.SelectedWork == nil || response.Data.SelectedWork.ID != "work_fixture" || response.Data.Selection.RoleID != "role_fixture" || response.Data.Selection.DriverKind != projectwork.AssignmentDriverHecateTask {
		t.Fatalf("selected work/selection = %+v/%+v, want sidecar-selected fixture work and role", response.Data.SelectedWork, response.Data.Selection)
	}
	if !projectAssistantContextHasRole(response.Data.Roles, "role_fixture") {
		t.Fatalf("roles = %+v, want sidecar fixture role", response.Data.Roles)
	}
	if len(response.Data.Skills) != 1 || response.Data.Skills[0].ID != "skill_fixture" {
		t.Fatalf("skills = %+v, want sidecar fixture skill", response.Data.Skills)
	}
	if len(response.Data.Assignments) != 1 || response.Data.Assignments[0].ID != "asg_fixture" {
		t.Fatalf("assignments = %+v, want sidecar fixture assignment", response.Data.Assignments)
	}
	if len(response.Data.Memory) != 1 || response.Data.Memory[0].ID != "mem_fixture" {
		t.Fatalf("memory = %+v, want enabled sidecar fixture memory", response.Data.Memory)
	}
	if len(response.Data.MemoryCandidates) != 1 || response.Data.MemoryCandidates[0].ID != "memcand_fixture" {
		t.Fatalf("memory candidates = %+v, want pending sidecar fixture candidate", response.Data.MemoryCandidates)
	}
}

func projectAssistantContextHasRole(roles []projectassistant.RoleContext, id string) bool {
	for _, role := range roles {
		if role.ID == id {
			return true
		}
	}
	return false
}

func TestProjectAssistantAPI_ContextCairnlineMatchesHecate(t *testing.T) {
	t.Parallel()
	hecateHandler, hecateServer := newProjectAssistantTestHandler()
	cairnlineHandler, cairnlineServer := newProjectAssistantCairnlineReadTestHandler()
	rootPath := filepath.Join(t.TempDir(), "workspace")
	projectID, workItemID := seedProjectAssistantContextParityFixture(t, hecateHandler, rootPath)
	seedProjectAssistantContextParityFixture(t, cairnlineHandler, rootPath)
	poisonNativeProjectAssistantCache(cairnlineHandler)

	body := []byte(`{
		"project_id":"` + projectID + `",
		"work_item_id":"` + workItemID + `",
		"request":"Queue architecture follow-up"
	}`)
	hecateRec := httptest.NewRecorder()
	hecateServer.ServeHTTP(hecateRec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/context", bytes.NewReader(body)))
	if hecateRec.Code != http.StatusOK {
		t.Fatalf("hecate context status = %d body=%s, want 200", hecateRec.Code, hecateRec.Body.String())
	}
	cairnlineRec := httptest.NewRecorder()
	cairnlineServer.ServeHTTP(cairnlineRec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/context", bytes.NewReader(body)))
	if cairnlineRec.Code != http.StatusOK {
		t.Fatalf("cairnline context status = %d body=%s, want 200", cairnlineRec.Code, cairnlineRec.Body.String())
	}
	var hecateResponse, cairnlineResponse projectAssistantContextResponse
	if err := json.Unmarshal(hecateRec.Body.Bytes(), &hecateResponse); err != nil {
		t.Fatalf("decode hecate context response: %v", err)
	}
	if err := json.Unmarshal(cairnlineRec.Body.Bytes(), &cairnlineResponse); err != nil {
		t.Fatalf("decode cairnline context response: %v", err)
	}
	if hecateResponse.Data.ReadBackend != "hecate" {
		t.Fatalf("hecate read_backend = %q, want hecate", hecateResponse.Data.ReadBackend)
	}
	if cairnlineResponse.Data.ReadBackend != "cairnline" {
		t.Fatalf("cairnline read_backend = %q, want cairnline", cairnlineResponse.Data.ReadBackend)
	}

	hecateContext := normalizeProjectAssistantContextForParity(hecateResponse.Data)
	cairnlineContext := normalizeProjectAssistantContextForParity(cairnlineResponse.Data)
	if !reflect.DeepEqual(hecateContext, cairnlineContext) {
		t.Fatalf("assistant context mismatch after normalization:\n%s", projectAssistantContextParityMismatch(hecateContext, cairnlineContext))
	}
}

func TestProjectAssistantAPI_DraftCairnlineMatchesHecate(t *testing.T) {
	t.Parallel()
	hecateHandler, hecateServer := newProjectAssistantTestHandler()
	cairnlineHandler, cairnlineServer := newProjectAssistantCairnlineReadTestHandler()
	rootPath := filepath.Join(t.TempDir(), "workspace")
	projectID, workItemID := seedProjectAssistantContextParityFixture(t, hecateHandler, rootPath)
	seedProjectAssistantContextParityFixture(t, cairnlineHandler, rootPath)
	poisonNativeProjectAssistantCache(cairnlineHandler)

	body := []byte(`{
		"project_id":"` + projectID + `",
		"work_item_id":"` + workItemID + `",
		"request":"Queue architecture follow-up\nValidate deterministic draft parity.",
		"draft_mode":"deterministic"
	}`)
	hecateRec := httptest.NewRecorder()
	hecateServer.ServeHTTP(hecateRec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/draft", bytes.NewReader(body)))
	if hecateRec.Code != http.StatusOK {
		t.Fatalf("hecate draft status = %d body=%s, want 200", hecateRec.Code, hecateRec.Body.String())
	}
	cairnlineRec := httptest.NewRecorder()
	cairnlineServer.ServeHTTP(cairnlineRec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/draft", bytes.NewReader(body)))
	if cairnlineRec.Code != http.StatusOK {
		t.Fatalf("cairnline draft status = %d body=%s, want 200", cairnlineRec.Code, cairnlineRec.Body.String())
	}
	var hecateResponse, cairnlineResponse projectAssistantProposalResponse
	if err := json.Unmarshal(hecateRec.Body.Bytes(), &hecateResponse); err != nil {
		t.Fatalf("decode hecate draft response: %v", err)
	}
	if err := json.Unmarshal(cairnlineRec.Body.Bytes(), &cairnlineResponse); err != nil {
		t.Fatalf("decode cairnline draft response: %v", err)
	}

	hecateProposal := normalizeProjectAssistantProposalForParity(t, hecateResponse.Data)
	cairnlineProposal := normalizeProjectAssistantProposalForParity(t, cairnlineResponse.Data)
	if !reflect.DeepEqual(hecateProposal, cairnlineProposal) {
		t.Fatalf("assistant draft mismatch after normalization:\n%s", projectAssistantProposalParityMismatch(hecateProposal, cairnlineProposal))
	}
}

func seedProjectAssistantContextParityFixture(t *testing.T, handler *Handler, rootPath string) (string, string) {
	t.Helper()
	ctx := t.Context()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	projectID := "proj_assistant_context_parity"
	rootID := "root_assistant_context_parity"
	workItemID := "work_assistant_context_parity"
	roleID := "role_context_architect"
	compactToolOutput := true
	if _, err := handler.agentProfiles.Create(ctx, agentprofiles.Profile{
		ID:                  "profile_context_architect",
		Name:                "Context Architect",
		Description:         "Plans durable project coordination work.",
		Instructions:        "Prefer small, reviewable work with explicit evidence.",
		Surface:             agentprofiles.SurfaceHecateTask,
		ProviderHint:        "openai",
		ModelHint:           "gpt-5-mini",
		ExecutionProfile:    "exec_context_architect",
		ToolsEnabled:        true,
		WritesAllowed:       true,
		NetworkAllowed:      false,
		ApprovalPolicy:      agentprofiles.ApprovalRequire,
		ProjectMemoryPolicy: agentprofiles.MemoryInclude,
		ContextSourcePolicy: agentprofiles.ContextIncludeEnabled,
		SkillIDs:            []string{"skill_context_architecture"},
		CreatedAt:           now,
		UpdatedAt:           now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("Create profile: %v", err)
	}
	if _, err := handler.projects.Create(ctx, projects.Project{
		ID:                       projectID,
		Name:                     "Assistant Context Parity",
		Description:              "Ensures Cairnline assistant context matches Hecate context.",
		DefaultRootID:            rootID,
		DefaultProvider:          "openai",
		DefaultModel:             "gpt-5-mini",
		DefaultAgentProfile:      "profile_context_architect",
		DefaultWorkspaceMode:     "worktree",
		DefaultSystemPrompt:      "Use project context only after operator approval.",
		DefaultCompactToolOutput: &compactToolOutput,
		Roots: []projects.Root{{
			ID:        rootID,
			Path:      rootPath,
			Kind:      "git",
			GitRemote: "https://github.com/hecatehq/hecate.git",
			GitBranch: "main",
			Active:    true,
			CreatedAt: now,
			UpdatedAt: now.Add(time.Minute),
		}},
		ContextSources: []projects.ContextSource{{
			ID:             "ctx_assistant_agents",
			Kind:           "workspace_instruction",
			Title:          "AGENTS.md",
			Path:           "AGENTS.md",
			Enabled:        true,
			Format:         "agents_md",
			Scope:          "workspace",
			TrustLabel:     "workspace_guidance",
			SourceCategory: "instructions",
			Metadata:       map[string]string{"root_id": rootID},
			CreatedAt:      now.Add(2 * time.Minute),
			UpdatedAt:      now.Add(3 * time.Minute),
		}},
		CreatedAt: now,
		UpdatedAt: now.Add(4 * time.Minute),
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateRole(ctx, projectwork.AgentRoleProfile{
		ID:                  roleID,
		ProjectID:           projectID,
		Name:                "Context Architect",
		Description:         "Turns project context into reviewable work.",
		Instructions:        "Preserve provenance and ask for review before closeout.",
		DefaultDriverKind:   projectwork.AssignmentDriverExternalAgent,
		DefaultProvider:     "openai",
		DefaultModel:        "gpt-5-mini",
		DefaultAgentProfile: "profile_context_architect",
		SkillIDs:            []string{"skill_context_architecture"},
		CreatedAt:           now.Add(5 * time.Minute),
		UpdatedAt:           now.Add(6 * time.Minute),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if _, err := handler.projectWork.CreateWorkItem(ctx, projectwork.WorkItem{
		ID:              workItemID,
		ProjectID:       projectID,
		Title:           "Plan assistant parity work",
		Brief:           "Create a reviewable slice that proves Cairnline context parity.",
		Status:          projectwork.WorkItemStatusReady,
		Priority:        "normal",
		OwnerRoleID:     roleID,
		RootID:          rootID,
		ReviewerRoleIDs: []string{"reviewer_qa"},
		CreatedAt:       now.Add(7 * time.Minute),
		UpdatedAt:       now.Add(8 * time.Minute),
	}); err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(ctx, projectwork.Assignment{
		ID:         "asgn_assistant_context_parity",
		ProjectID:  projectID,
		WorkItemID: workItemID,
		RoleID:     roleID,
		RootID:     rootID,
		DriverKind: projectwork.AssignmentDriverExternalAgent,
		Status:     projectwork.AssignmentStatusQueued,
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:              projectwork.AssignmentExecutionKindContextSnapshot,
			ContextSnapshotID: "ctxsnap_assistant_context_parity",
			Status:            "queued",
		},
		CreatedAt: now.Add(9 * time.Minute),
		UpdatedAt: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}
	toolsAllowed := true
	writesAllowed := true
	networkAllowed := false
	if _, err := handler.projectSkills.UpsertDiscovered(ctx, projectID, []projectskills.Skill{{
		ID:                     "skill_context_architecture",
		ProjectID:              projectID,
		Title:                  "Context architecture",
		Description:            "Plans project work with explicit context provenance.",
		Path:                   ".agents/skills/context-architecture/SKILL.md",
		RootID:                 rootID,
		Format:                 projectskills.FormatSkillMD,
		SuggestedTools:         []string{"read", "write"},
		RequiredPermissions:    projectskills.RequiredPermissions{Tools: &toolsAllowed, Writes: &writesAllowed, Network: &networkAllowed},
		Enabled:                true,
		Status:                 projectskills.StatusAvailable,
		TrustLabel:             projectskills.TrustWorkspaceSkill,
		SourceContextSourceIDs: []string{"ctx_assistant_agents"},
		Warnings:               []string{"metadata-only skill body not injected"},
		DiscoveredAt:           now.Add(11 * time.Minute),
		CreatedAt:              now.Add(11 * time.Minute),
		UpdatedAt:              now.Add(12 * time.Minute),
	}}); err != nil {
		t.Fatalf("UpsertDiscovered: %v", err)
	}
	if _, err := handler.memory.Create(ctx, memory.Entry{
		ID:         "mem_assistant_context_parity",
		Scope:      memory.ScopeProject,
		ProjectID:  projectID,
		Title:      "Assistant context parity decision",
		Body:       "Cairnline must preserve the Project Assistant context packet before replacing Hecate Projects reads.",
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
		SourceID:   "operator",
		Enabled:    true,
		CreatedAt:  now.Add(13 * time.Minute),
		UpdatedAt:  now.Add(14 * time.Minute),
	}); err != nil {
		t.Fatalf("Create memory: %v", err)
	}
	if _, err := handler.memoryCandidates.CreateCandidate(ctx, memory.Candidate{
		ID:                  "cand_assistant_context_parity",
		ProjectID:           projectID,
		Title:               "Candidate from workspace guidance",
		Body:                "Workspace guidance may become memory only after operator review.",
		SuggestedKind:       "workspace_guidance",
		SuggestedTrustLabel: memory.TrustLabelGenerated,
		SuggestedSourceKind: memory.SourceKindGenerated,
		SuggestedSourceID:   "ctx_assistant_agents",
		SourceRefs: []memory.CandidateSourceRef{{
			Kind:  "context_source",
			ID:    "ctx_assistant_agents",
			Title: "AGENTS.md",
		}},
		Status:    memory.CandidateStatusPending,
		CreatedAt: now.Add(15 * time.Minute),
		UpdatedAt: now.Add(16 * time.Minute),
	}); err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}
	return projectID, workItemID
}

func normalizeProjectAssistantContextForParity(item projectassistant.DraftContext) projectassistant.DraftContext {
	item.ReadBackend = ""
	item.Project.CreatedAt = time.Time{}
	item.Project.UpdatedAt = time.Time{}
	for idx := range item.Project.ContextSources {
		item.Project.ContextSources[idx].CreatedAt = time.Time{}
		item.Project.ContextSources[idx].UpdatedAt = time.Time{}
	}
	if item.SelectedWork != nil {
		item.SelectedWork.CreatedAt = time.Time{}
		item.SelectedWork.UpdatedAt = time.Time{}
	}
	for idx := range item.Roles {
		item.Roles[idx].CreatedAt = time.Time{}
		item.Roles[idx].UpdatedAt = time.Time{}
	}
	for idx := range item.Skills {
		item.Skills[idx].DiscoveredAt = time.Time{}
		item.Skills[idx].CreatedAt = time.Time{}
		item.Skills[idx].UpdatedAt = time.Time{}
	}
	for idx := range item.Assignments {
		item.Assignments[idx].CreatedAt = time.Time{}
		item.Assignments[idx].UpdatedAt = time.Time{}
		item.Assignments[idx].StartedAt = nil
		item.Assignments[idx].CompletedAt = nil
	}
	for idx := range item.Memory {
		item.Memory[idx].CreatedAt = time.Time{}
		item.Memory[idx].UpdatedAt = time.Time{}
	}
	for idx := range item.MemoryCandidates {
		item.MemoryCandidates[idx].CreatedAt = time.Time{}
		item.MemoryCandidates[idx].UpdatedAt = time.Time{}
	}
	for idx := range item.RecentActivity {
		item.RecentActivity[idx].UpdatedAt = time.Time{}
	}
	return item
}

func projectAssistantContextParityMismatch(hecate, cairnline projectassistant.DraftContext) string {
	hecateJSON, hecateErr := json.MarshalIndent(hecate, "", "  ")
	cairnlineJSON, cairnlineErr := json.MarshalIndent(cairnline, "", "  ")
	if hecateErr != nil || cairnlineErr != nil {
		return "hecate and cairnline context differ"
	}
	return "hecate:\n" + string(hecateJSON) + "\n\ncairnline:\n" + string(cairnlineJSON)
}

func normalizeProjectAssistantProposalForParity(t *testing.T, item projectassistant.Proposal) projectassistant.Proposal {
	t.Helper()
	item.ID = ""
	item.TraceID = ""
	for idx := range item.Actions {
		item.Actions[idx].Patch = canonicalProjectAssistantPatchForParity(t, item.Actions[idx].Patch)
	}
	return item
}

func canonicalProjectAssistantPatchForParity(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode proposal patch: %v", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode canonical proposal patch: %v", err)
	}
	return canonical
}

func projectAssistantProposalParityMismatch(hecate, cairnline projectassistant.Proposal) string {
	hecateJSON, hecateErr := json.MarshalIndent(hecate, "", "  ")
	cairnlineJSON, cairnlineErr := json.MarshalIndent(cairnline, "", "  ")
	if hecateErr != nil || cairnlineErr != nil {
		return "hecate and cairnline proposals differ"
	}
	return "hecate:\n" + string(hecateJSON) + "\n\ncairnline:\n" + string(cairnlineJSON)
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
		RootID:      "root_api",
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
	if patch["project_id"] != project.ID || patch["work_item_id"] != workItem.ID || patch["role_id"] != "product_manager" || patch["root_id"] != "root_api" || patch["driver_kind"] != projectwork.AssignmentDriverExternalAgent {
		t.Fatalf("patch = %+v, want selected project/work/owner role/root/external driver", patch)
	}
}

func TestProjectAssistantAPI_DraftUsesCairnlineReadModelWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantCairnlineReadTestHandler()
	project, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_cairnline_draft",
		Name: "Cairnline Draft",
		Roots: []projects.Root{{
			ID:     "root_cairnline_draft",
			Path:   t.TempDir(),
			Active: true,
		}},
	})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	role, err := handler.projectWork.CreateRole(t.Context(), projectwork.AgentRoleProfile{
		ID:                "architect_cairnline_draft",
		ProjectID:         project.ID,
		Name:              "Architect",
		DefaultDriverKind: projectwork.AssignmentDriverExternalAgent,
	})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	workItem, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:          "work_cairnline_draft",
		ProjectID:   project.ID,
		Title:       "Plan next Cairnline-backed task",
		Brief:       "Draft an assignment from the portable read model.",
		Status:      projectwork.WorkItemStatusReady,
		OwnerRoleID: role.ID,
		RootID:      "root_cairnline_draft",
	})
	if err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	poisonNativeProjectAssistantCache(handler)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/draft", bytes.NewReader([]byte(`{
		"project_id":"proj_cairnline_draft",
		"work_item_id":"work_cairnline_draft",
		"request":"Queue Architect\nPrepare the next project slice."
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("draft status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var proposed projectAssistantProposalResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &proposed); err != nil {
		t.Fatalf("decode draft response: %v", err)
	}
	if proposed.Object != "project_assistant.proposal" || len(proposed.Data.Actions) != 1 {
		t.Fatalf("draft response = %+v, want one proposal action", proposed)
	}
	var patch map[string]string
	if err := json.Unmarshal(proposed.Data.Actions[0].Patch, &patch); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patch["project_id"] != project.ID || patch["work_item_id"] != workItem.ID || patch["role_id"] != role.ID || patch["root_id"] != workItem.RootID || patch["driver_kind"] != projectwork.AssignmentDriverExternalAgent {
		t.Fatalf("patch = %+v, want Cairnline-projected owner/root/default driver", patch)
	}
	if _, ok, err := handler.projectAssistantProposals.GetProposal(t.Context(), proposed.Data.ID); err != nil || !ok {
		t.Fatalf("GetProposal(%q) = ok %v err %v, want Cairnline draft stored in Hecate proposal ledger", proposed.Data.ID, ok, err)
	}
}

func TestProjectAssistantAPI_DraftUsesCairnlineSidecarWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineSidecarReadTestServer(t, "memory-fixture")
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar assistant draft enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("sidecar read-route predicate = false, want true")
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/draft", bytes.NewReader([]byte(`{
		"project_id":"proj_fixture",
		"work_item_id":"work_fixture",
		"request":"Queue Fixture Reviewer\nRead from the Cairnline sidecar context.",
		"role_id":"role_fixture"
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("draft status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var proposed projectAssistantProposalResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &proposed); err != nil {
		t.Fatalf("decode draft response: %v", err)
	}
	if proposed.Object != "project_assistant.proposal" || len(proposed.Data.Actions) != 1 || proposed.Data.Actions[0].Kind != projectassistant.ActionCreateAssignment {
		t.Fatalf("draft response = %+v, want one sidecar-backed assignment proposal", proposed)
	}
	var patch map[string]string
	if err := json.Unmarshal(proposed.Data.Actions[0].Patch, &patch); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patch["project_id"] != "proj_fixture" || patch["work_item_id"] != "work_fixture" || patch["role_id"] != "role_fixture" || patch["driver_kind"] != projectwork.AssignmentDriverHecateTask {
		t.Fatalf("patch = %+v, want sidecar project/work/role with normalized Hecate driver", patch)
	}
	if _, ok, err := handler.projectAssistantProposals.GetProposal(t.Context(), proposed.Data.ID); err != nil || !ok {
		t.Fatalf("GetProposal(%q) = ok %v err %v, want sidecar-context draft stored in Hecate proposal ledger", proposed.Data.ID, ok, err)
	}
}

func TestProjectAssistantAPI_MirrorsProposalLedgerToCairnlineWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantCairnlineMirrorTestHandler(t)
	client := newAPITestClient(t, server)
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_pa_mirror",
		Name: "Assistant Mirror",
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}

	drafted := mustRequestJSON[projectAssistantProposalResponse](client, http.MethodPost, "/hecate/v1/project-assistant/draft", `{
		"project_id":"proj_pa_mirror",
		"request":"Capture the first reviewable work item."
	}`)
	if drafted.Object != "project_assistant.proposal" || drafted.Data.ID == "" || len(drafted.Data.Actions) != 1 {
		t.Fatalf("draft response = %+v, want one mirrored proposal action", drafted)
	}
	mirroredDraft := getMirroredCairnlineAssistantProposalForTest(t, handler, drafted.Data.ID)
	if mirroredDraft.ProjectID != "proj_pa_mirror" || mirroredDraft.Status != cairnline.AssistantProposalStatusProposed || mirroredDraft.Source != cairnline.AssistantProposalSourceAssistant || len(mirroredDraft.Proposal.Actions) != 1 {
		t.Fatalf("mirrored draft = %+v, want proposed assistant-sourced ledger record", mirroredDraft)
	}

	proposed := mustRequestJSON[projectAssistantProposalResponse](client, http.MethodPost, "/hecate/v1/project-assistant/propose", `{
		"id":"pa_mirror_apply",
		"title":"Create mirrored work",
		"summary":"Create a work item and mirror the apply ledger.",
		"actions":[{
			"kind":"create_work_item",
			"reason":"The project needs one reviewable task.",
			"patch":{
				"id":"work_pa_mirror",
				"project_id":"proj_pa_mirror",
				"title":"Mirror assistant ledger",
				"brief":"Verify the Project Assistant proposal ledger mirrors to Cairnline.",
				"status":"ready"
			}
		}]
	}`)
	mirroredProposal := getMirroredCairnlineAssistantProposalForTest(t, handler, proposed.Data.ID)
	if mirroredProposal.Status != cairnline.AssistantProposalStatusProposed || mirroredProposal.Source != cairnline.AssistantProposalSourceAPI || len(mirroredProposal.ApplyAttempts) != 0 {
		t.Fatalf("mirrored proposal = %+v, want proposed API ledger without attempts", mirroredProposal)
	}

	applied := mustRequestJSON[projectAssistantApplyResponse](client, http.MethodPost, "/hecate/v1/project-assistant/apply", projectJourneyJSON(t, map[string]any{
		"proposal": proposed.Data,
		"confirm":  true,
	}))
	if applied.Data.Status != projectassistant.ApplyStatusApplied || !applied.Data.Applied || applied.Data.ProposalID != proposed.Data.ID {
		t.Fatalf("apply response = %+v, want applied proposal", applied)
	}
	mirroredApplied := getMirroredCairnlineAssistantProposalForTest(t, handler, proposed.Data.ID)
	if mirroredApplied.Status != cairnline.AssistantProposalStatusApplied || mirroredApplied.LatestResult == nil || !mirroredApplied.LatestResult.Applied || len(mirroredApplied.ApplyAttempts) != 1 || mirroredApplied.AppliedAt == nil {
		t.Fatalf("mirrored applied proposal = %+v, want applied ledger result and one attempt", mirroredApplied)
	}
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	workItem, err := service.GetWorkItem(t.Context(), "proj_pa_mirror", "work_pa_mirror")
	if err != nil {
		t.Fatalf("GetWorkItem(work_pa_mirror): %v", err)
	}
	if workItem.Title != "Mirror assistant ledger" || workItem.Status != cairnline.WorkStatusReady {
		t.Fatalf("mirrored work item = %+v, want assistant-created work item", workItem)
	}
}

func TestProjectAssistantAPI_CairnlineProposalAuthorityCommitsLedgerFirst(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantCairnlineMirrorTestHandler(t)
	handler.config.Projects.CairnlineWriteAuthority = projectCairnlineWriteAuthorityProjectAssistantProposals
	client := newAPITestClient(t, server)
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_pa_authority",
		Name: "Assistant Authority",
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}

	proposed := mustRequestJSON[projectAssistantProposalResponse](client, http.MethodPost, "/hecate/v1/project-assistant/propose", `{
		"id":"pa_authority_apply",
		"title":"Create authoritative work",
		"summary":"Create a work item through the Project Assistant ledger authority path.",
		"actions":[{
			"kind":"create_work_item",
			"reason":"The project needs one reviewable task.",
			"patch":{
				"id":"work_pa_authority",
				"project_id":"proj_pa_authority",
				"title":"Authoritative assistant ledger",
				"brief":"Verify Project Assistant proposal records can commit to Cairnline first.",
				"status":"ready"
			}
		}]
	}`)
	written := getMirroredCairnlineAssistantProposalForTest(t, handler, proposed.Data.ID)
	if written.Status != cairnline.AssistantProposalStatusProposed || written.Source != cairnline.AssistantProposalSourceAPI || len(written.Proposal.Actions) != 1 {
		t.Fatalf("Cairnline proposal = %+v, want proposed API ledger record", written)
	}
	shadow, ok, err := handler.projectAssistantProposals.GetProposal(t.Context(), proposed.Data.ID)
	if err != nil || !ok {
		t.Fatalf("GetProposal shadow = ok %v err %v, want Hecate compatibility shadow", ok, err)
	}
	if shadow.Fingerprint == "" {
		t.Fatalf("shadow proposal = %+v, want Hecate fingerprint preserved for apply safety", shadow)
	}

	applied := mustRequestJSON[projectAssistantApplyResponse](client, http.MethodPost, "/hecate/v1/project-assistant/apply", projectJourneyJSON(t, map[string]any{
		"proposal": proposed.Data,
		"confirm":  true,
	}))
	if applied.Data.Status != projectassistant.ApplyStatusApplied || !applied.Data.Applied || applied.Data.CommittedActionCount != 1 {
		t.Fatalf("apply response = %+v, want applied proposal ledger result", applied.Data)
	}
	appliedLedger := getMirroredCairnlineAssistantProposalForTest(t, handler, proposed.Data.ID)
	if appliedLedger.Status != cairnline.AssistantProposalStatusApplied || appliedLedger.LatestResult == nil || !appliedLedger.LatestResult.Applied || len(appliedLedger.ApplyAttempts) != 1 {
		t.Fatalf("Cairnline applied proposal = %+v, want applied ledger result and attempt", appliedLedger)
	}
	shadow, ok, err = handler.projectAssistantProposals.GetProposal(t.Context(), proposed.Data.ID)
	if err != nil || !ok || shadow.LatestResult == nil || !shadow.LatestResult.Applied || len(shadow.ApplyAttempts) != 1 {
		t.Fatalf("shadow proposal after apply = %+v ok %v err %v, want compatibility shadow with apply attempt", shadow, ok, err)
	}
}

func TestProjectAssistantAPI_CairnlineProposalAuthorityDoesNotBlockOnShadowUpsertFailure(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantCairnlineMirrorTestHandler(t)
	handler.config.Projects.CairnlineWriteAuthority = projectCairnlineWriteAuthorityProjectAssistantProposals
	handler.SetProjectAssistantProposalStore(failingProposalUpsertStore{
		ProposalStore: projectassistant.NewMemoryProposalStore(),
		err:           errors.New("shadow proposal store unavailable"),
	})
	client := newAPITestClient(t, server)
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_pa_shadow_failure",
		Name: "Assistant Shadow Failure",
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}

	proposed := mustRequestJSON[projectAssistantProposalResponse](client, http.MethodPost, "/hecate/v1/project-assistant/propose", `{
		"id":"pa_shadow_failure",
		"title":"Create ledger despite shadow failure",
		"summary":"The authoritative Cairnline ledger should commit before the Hecate compatibility shadow.",
		"actions":[{
			"kind":"create_work_item",
			"reason":"The proposal should survive a Hecate shadow write failure.",
			"patch":{
				"id":"work_pa_shadow_failure",
				"project_id":"proj_pa_shadow_failure",
				"title":"Shadow failure proof",
				"brief":"Verify Project Assistant proposal authority is not just a post-commit mirror.",
				"status":"ready"
			}
		}]
	}`)
	if proposed.Data.ID != "pa_shadow_failure" || len(proposed.Data.Actions) != 1 {
		t.Fatalf("proposal response = %+v, want successful proposal despite Hecate shadow failure", proposed.Data)
	}
	written := getMirroredCairnlineAssistantProposalForTest(t, handler, proposed.Data.ID)
	if written.ProjectID != "proj_pa_shadow_failure" || written.Status != cairnline.AssistantProposalStatusProposed {
		t.Fatalf("Cairnline proposal = %+v, want authoritative proposal committed despite shadow failure", written)
	}
}

func TestProjectAssistantAPI_CairnlineApplyWorkAuthorityDoesNotBlockOnRoleShadowFailure(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantCairnlineMirrorTestHandler(t)
	handler.config.Projects.CairnlineWriteAuthority = strings.Join([]string{
		projectCairnlineWriteAuthorityProjectAssistantProposals,
		projectCairnlineWriteAuthorityProjectRoles,
	}, ",")
	handler.SetProjectWorkStore(failingCreateRoleProjectWorkStore{
		Store: projectwork.NewMemoryStore(),
		err:   errors.New("shadow role store unavailable"),
	})
	client := newAPITestClient(t, server)
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_pa_apply_role_authority",
		Name: "Assistant Apply Role Authority",
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}

	proposed := mustRequestJSON[projectAssistantProposalResponse](client, http.MethodPost, "/hecate/v1/project-assistant/propose", `{
		"id":"pa_apply_role_authority",
		"title":"Create role through authority",
		"summary":"Project Assistant apply should use the Cairnline-first work authority seam.",
		"actions":[{
			"kind":"create_role",
			"reason":"The project needs an implementation owner.",
			"patch":{
				"id":"role_apply_authority",
				"project_id":"proj_pa_apply_role_authority",
				"name":"Implementation Owner",
				"description":"Owns scoped implementation work.",
				"instructions":"Implement only the requested slice and leave evidence.",
				"default_driver_kind":"external_agent",
				"default_provider":"openai",
				"default_model":"gpt-5",
				"default_agent_profile":"implementation",
				"skill_ids":["backend"]
			}
		}]
	}`)
	applied := mustRequestJSON[projectAssistantApplyResponse](client, http.MethodPost, "/hecate/v1/project-assistant/apply", projectJourneyJSON(t, map[string]any{
		"proposal": proposed.Data,
		"confirm":  true,
	}))
	if applied.Data.Status != projectassistant.ApplyStatusApplied || !applied.Data.Applied || applied.Data.CommittedActionCount != 1 {
		t.Fatalf("apply response = %+v, want applied role action despite Hecate shadow failure", applied.Data)
	}

	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	role, err := getCairnlineProjectRoleForAuthority(t.Context(), service, "proj_pa_apply_role_authority", "role_apply_authority")
	if err != nil {
		t.Fatalf("Get role_apply_authority from Cairnline: %v", err)
	}
	if role.Name != "Implementation Owner" || role.DefaultExecutionMode != cairnline.ExecutionExternalAdapter || !reflect.DeepEqual(role.DefaultSkillIDs, []string{"backend"}) {
		t.Fatalf("Cairnline role = %+v, want authoritative external-agent role with backend skill", role)
	}
	roles, err := handler.projectWork.ListRoles(t.Context(), "proj_pa_apply_role_authority")
	if err != nil {
		t.Fatalf("ListRoles shadow: %v", err)
	}
	for _, role := range roles {
		if role.ID == "role_apply_authority" {
			t.Fatalf("shadow role store unexpectedly contains %+v, want Cairnline authority to survive failed Hecate shadow", role)
		}
	}
}

func TestProjectAssistantAPI_CairnlineApplyMemoryCandidateAuthorityDoesNotBlockOnShadowFailure(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantCairnlineMirrorTestHandler(t)
	handler.config.Projects.CairnlineWriteAuthority = strings.Join([]string{
		"project-memory",
		"memory-candidates",
	}, ",")
	handler.SetMemoryStore(failingCreateMemoryCandidateStore{
		MemoryStore: memory.NewMemoryStore(),
		err:         errors.New("shadow memory candidate store unavailable"),
	})
	client := newAPITestClient(t, server)
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_pa_apply_memory_authority",
		Name: "Assistant Apply Memory Authority",
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}

	proposed := mustRequestJSON[projectAssistantProposalResponse](client, http.MethodPost, "/hecate/v1/project-assistant/propose", `{
		"id":"pa_apply_memory_authority",
		"title":"Create memory candidate through authority",
		"summary":"Project Assistant apply should use the Cairnline-first memory candidate authority seam.",
		"actions":[{
			"kind":"create_memory_candidate",
			"reason":"Capture a reviewable project lesson.",
			"patch":{
				"id":"memcand_apply_authority",
				"project_id":"proj_pa_apply_memory_authority",
				"title":"Authority-backed memory candidate",
				"body":"Assistant memory-candidate apply should commit through Cairnline when candidate authority is enabled.",
				"suggested_kind":"process",
				"suggested_trust_label":"operator_reviewed",
				"suggested_source_kind":"project_assistant",
				"suggested_source_id":"pa_apply_memory_authority",
				"source_refs":[{"kind":"proposal","id":"pa_apply_memory_authority","title":"Create memory candidate through authority"}]
			}
		}]
	}`)
	applied := mustRequestJSON[projectAssistantApplyResponse](client, http.MethodPost, "/hecate/v1/project-assistant/apply", projectJourneyJSON(t, map[string]any{
		"proposal": proposed.Data,
		"confirm":  true,
	}))
	if applied.Data.Status != projectassistant.ApplyStatusApplied || !applied.Data.Applied || applied.Data.CommittedActionCount != 1 {
		t.Fatalf("apply response = %+v, want applied memory-candidate action despite Hecate shadow failure", applied.Data)
	}

	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	candidate, err := service.GetMemoryCandidate(t.Context(), "proj_pa_apply_memory_authority", "memcand_apply_authority")
	if err != nil {
		t.Fatalf("GetMemoryCandidate from Cairnline: %v", err)
	}
	if candidate.Status != cairnline.MemoryCandidatePending || candidate.SuggestedSourceKind != "project_assistant" || candidate.SuggestedSourceID != "pa_apply_memory_authority" || len(candidate.SourceRefs) != 1 || candidate.SourceRefs[0].ID != "pa_apply_memory_authority" {
		t.Fatalf("Cairnline memory candidate = %+v, want authoritative assistant-sourced pending candidate", candidate)
	}
	if _, ok, err := handler.memoryCandidates.GetCandidate(t.Context(), "proj_pa_apply_memory_authority", "memcand_apply_authority"); err != nil || ok {
		t.Fatalf("shadow memory candidate ok=%v err=%v, want missing after injected shadow failure", ok, err)
	}
}

func TestProjectAssistantAPI_CairnlineApplyProjectMetadataAuthorityDoesNotBlockOnShadowFailure(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantCairnlineMirrorTestHandler(t)
	handler.config.Projects.CairnlineWriteAuthority = projectCairnlineWriteAuthorityProjectMetadataDefaults
	baseProjects := projects.NewMemoryStore()
	project, err := baseProjects.Create(t.Context(), projects.Project{
		ID:            "proj_pa_apply_project_authority",
		Name:          "Assistant Project Authority",
		DefaultRootID: "root_main",
		Roots: []projects.Root{{
			ID:     "root_main",
			Path:   "/workspace/main",
			Kind:   "git",
			Active: true,
		}},
	})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	handler.SetProjectStore(failingUpdateProjectStore{
		Store: baseProjects,
		err:   errors.New("shadow project store unavailable"),
	})
	if !handler.projectMetadataDefaultsWritesUseCairnlineAuthority() {
		t.Fatal("metadata/default write authority is disabled, want enabled for test")
	}
	proposal := projectassistant.Proposal{
		ID:                   "pa_apply_project_authority",
		Title:                "Update project through authority",
		Summary:              "Project Assistant apply should use the Cairnline-first metadata/default authority seam.",
		RequiresConfirmation: true,
		Actions: []projectassistant.Action{
			{
				Kind:   projectassistant.ActionUpdateProject,
				Reason: "Rename the project through the authority seam.",
				Target: map[string]string{"project_id": "proj_pa_apply_project_authority"},
				Patch:  json.RawMessage(`{"name":"Assistant Project Authority Updated","description":"Cairnline owns this metadata update."}`),
			},
			{
				Kind:   projectassistant.ActionSetProjectDefaults,
				Reason: "Set launch defaults through the authority seam.",
				Target: map[string]string{"project_id": "proj_pa_apply_project_authority"},
				Patch:  json.RawMessage(`{"default_root_id":"root_main","default_provider":"anthropic","default_model":"claude-sonnet-4-5"}`),
			},
		},
	}
	applyBody, err := json.Marshal(map[string]any{"proposal": proposal, "confirm": true})
	if err != nil {
		t.Fatalf("marshal apply body: %v", err)
	}
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/apply", bytes.NewReader(applyBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var applied projectAssistantApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &applied); err != nil {
		t.Fatalf("decode apply response: %v", err)
	}
	if applied.Data.Status != projectassistant.ApplyStatusApplied || !applied.Data.Applied || applied.Data.CommittedActionCount != 2 {
		t.Fatalf("apply response = %+v, want applied project metadata/default actions despite Hecate shadow failure", applied.Data)
	}

	mirrored := getMirroredCairnlineProjectForTest(t, handler, project.ID)
	if mirrored.Name != "Assistant Project Authority Updated" || mirrored.Description != "Cairnline owns this metadata update." || mirrored.DefaultRootID != "root_main" {
		t.Fatalf("Cairnline project = %+v, want authoritative metadata/default update", mirrored)
	}
	assertMirroredExecutionProfileForTest(t, handler, mirrored.DefaultExecutionProfileID, "anthropic", "claude-sonnet-4-5")
	native, ok, err := baseProjects.Get(t.Context(), project.ID)
	if err != nil || !ok {
		t.Fatalf("Get native project ok=%v err=%v", ok, err)
	}
	if native.Name != "Assistant Project Authority" || native.DefaultModel != "" {
		t.Fatalf("native shadow project = %+v, want unchanged after injected shadow failure", native)
	}
}

func TestProjectAssistantAPI_CairnlineApplyProjectRootAuthorityDoesNotBlockOnShadowFailure(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantCairnlineMirrorTestHandler(t)
	handler.config.Projects.CairnlineWriteAuthority = projectCairnlineWriteAuthorityProjectRoots
	baseProjects := projects.NewMemoryStore()
	project, err := baseProjects.Create(t.Context(), projects.Project{
		ID:            "proj_pa_apply_root_authority",
		Name:          "Assistant Root Authority",
		DefaultRootID: "root_main",
		Roots: []projects.Root{{
			ID:     "root_main",
			Path:   "/workspace/main",
			Kind:   "git",
			Active: true,
		}},
	})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if err := handler.writeProjectIdentityToCairnline(t.Context(), project); err != nil {
		t.Fatalf("write initial Cairnline project: %v", err)
	}
	seedCairnlineOnlyProjectGraphForTest(t, handler, project.ID)
	handler.SetProjectStore(failingUpdateProjectStore{
		Store: baseProjects,
		err:   errors.New("shadow project store unavailable"),
	})
	if !handler.projectRootWritesUseCairnlineAuthority() {
		t.Fatal("root write authority is disabled, want enabled for test")
	}
	proposal := projectassistant.Proposal{
		ID:                   "pa_apply_root_authority",
		Title:                "Update roots through authority",
		Summary:              "Project Assistant apply should use the Cairnline-first root authority seam.",
		RequiresConfirmation: true,
		Actions: []projectassistant.Action{
			{
				Kind:   projectassistant.ActionAttachProjectRoot,
				Reason: "Attach a root through the authority seam.",
				Target: map[string]string{"project_id": project.ID},
				Patch:  json.RawMessage(`{"id":"root_attached","path":"/workspace/attached","kind":"git_worktree","active":true}`),
			},
			{
				Kind:   projectassistant.ActionRemoveProjectRoot,
				Reason: "Remove the original root through the authority seam.",
				Target: map[string]string{"project_id": project.ID, "root_id": "root_main"},
			},
		},
	}
	applyBody, err := json.Marshal(map[string]any{"proposal": proposal, "confirm": true})
	if err != nil {
		t.Fatalf("marshal apply body: %v", err)
	}
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/apply", bytes.NewReader(applyBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var applied projectAssistantApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &applied); err != nil {
		t.Fatalf("decode apply response: %v", err)
	}
	if applied.Data.Status != projectassistant.ApplyStatusApplied || !applied.Data.Applied || applied.Data.CommittedActionCount != 2 {
		t.Fatalf("apply response = %+v, want applied root actions despite Hecate shadow failure", applied.Data)
	}

	mirrored := getMirroredCairnlineProjectForTest(t, handler, project.ID)
	if findMirroredCairnlineRootForTest(mirrored.Roots, "root_attached") == nil {
		t.Fatalf("Cairnline roots = %+v, want attached root", mirrored.Roots)
	}
	if findMirroredCairnlineRootForTest(mirrored.Roots, "root_main") != nil {
		t.Fatalf("Cairnline roots = %+v, want removed root_main absent", mirrored.Roots)
	}
	if findMirroredCairnlineRootForTest(mirrored.Roots, "root_cairnline_only") == nil {
		t.Fatalf("Cairnline roots = %+v, want Cairnline-only root preserved", mirrored.Roots)
	}
	if findMirroredCairnlineSourceForTest(mirrored.ContextSources, "ctx_cairnline_only") == nil {
		t.Fatalf("Cairnline sources = %+v, want Cairnline-only source preserved", mirrored.ContextSources)
	}
	native, ok, err := baseProjects.Get(t.Context(), project.ID)
	if err != nil || !ok {
		t.Fatalf("Get native project ok=%v err=%v", ok, err)
	}
	if len(native.Roots) != 1 || native.Roots[0].ID != "root_main" || native.DefaultRootID != "root_main" {
		t.Fatalf("native shadow project = %+v, want unchanged after injected shadow failure", native)
	}
}

func TestProjectAssistantAPI_ProposalRouteUsesStrictEmbeddedReadSource(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantCairnlineMirrorTestHandler(t)
	client := newAPITestClient(t, server)
	project := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", `{
		"name":"Strict Embedded Assistant"
	}`)

	drafted := mustRequestJSON[projectAssistantProposalResponse](client, http.MethodPost, "/hecate/v1/project-assistant/draft", projectJourneyJSON(t, map[string]any{
		"project_id": project.Data.ID,
		"request":    "Capture the first reviewable work item.",
	}))
	if drafted.Data.ID == "" || len(drafted.Data.Actions) == 0 {
		t.Fatalf("drafted proposal = %+v, want mirrored proposal with at least one action", drafted.Data)
	}
	mirroredDraft := getMirroredCairnlineAssistantProposalForTest(t, handler, drafted.Data.ID)
	if mirroredDraft.ProjectID != project.Data.ID || mirroredDraft.Status != cairnline.AssistantProposalStatusProposed {
		t.Fatalf("mirrored draft = %+v, want proposed embedded proposal for project %s", mirroredDraft, project.Data.ID)
	}

	handler.config.Projects.CairnlineReadSource = "embedded"
	handler.SetProjectAssistantProposalStore(projectassistant.NewMemoryProposalStore())
	if _, ok, err := handler.projectAssistantProposals.GetProposal(t.Context(), drafted.Data.ID); err != nil || ok {
		t.Fatalf("native proposal store = ok %v err %v, want cleared store before strict embedded read", ok, err)
	}

	record := mustRequestJSON[projectAssistantProposalRecordResponse](client, http.MethodGet, "/hecate/v1/project-assistant/proposals/"+drafted.Data.ID, "")
	if record.Data.ID != drafted.Data.ID || record.Data.ProjectID != project.Data.ID || record.Data.Status != projectassistant.ProposalStatusProposed || len(record.Data.Proposal.Actions) != len(drafted.Data.Actions) {
		t.Fatalf("proposal record = %+v, want strict embedded Cairnline-projected proposal", record.Data)
	}
}

func TestProjectAssistantAPI_ApplyMirrorsCoordinationGraphToCairnlineWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantCairnlineMirrorTestHandler(t)
	client := newAPITestClient(t, server)
	project, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_pa_apply_graph",
		Name: "Assistant Apply Graph",
	})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}

	proposed := mustRequestJSON[projectAssistantProposalResponse](client, http.MethodPost, "/hecate/v1/project-assistant/propose", `{
		"id":"pa_apply_graph",
		"title":"Create coordination graph",
		"summary":"Create role, work, assignment, handoff, and memory candidate records.",
		"actions":[{
			"kind":"create_role",
			"reason":"The work needs an implementation owner.",
			"patch":{
				"id":"role_apply_graph",
				"project_id":"proj_pa_apply_graph",
				"name":"Implementer",
				"description":"Owns implementation tasks.",
				"instructions":"Make scoped changes and leave evidence.",
				"default_driver_kind":"external_agent",
				"default_provider":"openai",
				"default_model":"gpt-5",
				"skill_ids":["backend"]
			}
		},{
			"kind":"create_work_item",
			"reason":"The project needs a reviewable task.",
			"patch":{
				"id":"work_apply_graph",
				"project_id":"proj_pa_apply_graph",
				"title":"Mirror assistant side effects",
				"brief":"Verify Project Assistant apply side effects mirror to Cairnline.",
				"status":"ready",
				"priority":"normal",
				"owner_role_id":"role_apply_graph",
				"reviewer_role_ids":["role_apply_graph"]
			}
		},{
			"kind":"create_assignment",
			"reason":"Queue the implementation assignment.",
			"patch":{
				"id":"asgn_apply_graph",
				"project_id":"proj_pa_apply_graph",
				"work_item_id":"work_apply_graph",
				"role_id":"role_apply_graph",
				"driver_kind":"external_agent",
				"status":"queued"
			}
		},{
			"kind":"create_handoff",
			"reason":"Record the follow-up context.",
			"patch":{
				"id":"handoff_apply_graph",
				"project_id":"proj_pa_apply_graph",
				"work_item_id":"work_apply_graph",
				"source_assignment_id":"asgn_apply_graph",
				"target_role_id":"role_apply_graph",
				"title":"Implementation handoff",
				"summary":"Use the mirrored assignment context.",
				"recommended_next_action":"Start the queued assignment.",
				"context_refs":["ctx:assistant-apply"],
				"status":"pending",
				"provenance_kind":"assistant",
				"trust_label":"operator_reviewed",
				"created_by_role_id":"role_apply_graph"
			}
		},{
			"kind":"create_memory_candidate",
			"reason":"Capture a reviewable lesson for the project.",
			"patch":{
				"id":"memcand_apply_graph",
				"project_id":"proj_pa_apply_graph",
				"title":"Assistant apply mirror coverage",
				"body":"Project Assistant apply should mirror durable coordination records into Cairnline.",
				"suggested_kind":"process",
				"suggested_trust_label":"operator_reviewed",
				"suggested_source_kind":"project_assistant",
				"suggested_source_id":"pa_apply_graph",
				"source_refs":[{"kind":"proposal","id":"pa_apply_graph","title":"Create coordination graph"}]
			}
		}]
	}`)

	applied := mustRequestJSON[projectAssistantApplyResponse](client, http.MethodPost, "/hecate/v1/project-assistant/apply", projectJourneyJSON(t, map[string]any{
		"proposal": proposed.Data,
		"confirm":  true,
	}))
	if applied.Data.Status != projectassistant.ApplyStatusApplied || applied.Data.CommittedActionCount != 5 || applied.Data.ResumeActionIndex != 5 {
		t.Fatalf("apply response = %+v, want all coordination actions applied", applied.Data)
	}

	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()

	roles, err := service.ListRoles(t.Context(), project.ID)
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	var role cairnline.Role
	for _, item := range roles {
		if item.ID == "role_apply_graph" {
			role = item
			break
		}
	}
	if role.ID == "" || role.Name != "Implementer" || role.DefaultExecutionMode != cairnline.ExecutionExternalAdapter || !reflect.DeepEqual(role.DefaultSkillIDs, []string{"backend"}) {
		t.Fatalf("mirrored role = %+v, want external-agent implementer role with backend skill", role)
	}

	workItem, err := service.GetWorkItem(t.Context(), project.ID, "work_apply_graph")
	if err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	if workItem.Title != "Mirror assistant side effects" || workItem.OwnerRoleID != role.ID || !reflect.DeepEqual(workItem.ReviewerRoleIDs, []string{role.ID}) {
		t.Fatalf("mirrored work item = %+v, want owner/reviewer role links", workItem)
	}

	assignment, err := service.GetAssignment(t.Context(), project.ID, "asgn_apply_graph")
	if err != nil {
		t.Fatalf("GetAssignment: %v", err)
	}
	if assignment.WorkItemID != workItem.ID || assignment.RoleID != role.ID || assignment.ExecutionMode != cairnline.ExecutionExternalAdapter || assignment.DesiredAgent.Kind != cairnline.DesiredAgentAny || !reflect.DeepEqual(assignment.DesiredAgent.SkillIDs, []string{"backend"}) {
		t.Fatalf("mirrored assignment = %+v, want portable external-agent assignment with role skills", assignment)
	}

	handoff, err := service.GetHandoff(t.Context(), project.ID, workItem.ID, "handoff_apply_graph")
	if err != nil {
		t.Fatalf("GetHandoff: %v", err)
	}
	if handoff.SourceAssignmentID != assignment.ID || handoff.ToRoleID != role.ID || handoff.FromRoleID != role.ID || handoff.Status != cairnline.HandoffStatusOpen || handoff.Body != "Use the mirrored assignment context." || handoff.RecommendedNextAction != "Start the queued assignment." || !reflect.DeepEqual(handoff.ContextRefs, []string{"ctx:assistant-apply"}) {
		t.Fatalf("mirrored handoff = %+v, want routed open handoff with context refs", handoff)
	}

	candidate, err := service.GetMemoryCandidate(t.Context(), project.ID, "memcand_apply_graph")
	if err != nil {
		t.Fatalf("GetMemoryCandidate: %v", err)
	}
	if candidate.Status != cairnline.MemoryCandidatePending || candidate.SuggestedSourceKind != "project_assistant" || candidate.SuggestedSourceID != "pa_apply_graph" || len(candidate.SourceRefs) != 1 || candidate.SourceRefs[0].ID != "pa_apply_graph" {
		t.Fatalf("mirrored memory candidate = %+v, want pending assistant-sourced memory candidate", candidate)
	}
}

func TestProjectAssistantAPI_PartialApplyMirrorsCommittedActionsToCairnlineWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantCairnlineMirrorTestHandler(t)
	baseWork := projectwork.NewMemoryStore()
	handler.SetProjectWorkStore(failingCreateAssignmentProjectWorkStore{
		Store: baseWork,
		err:   projectwork.ErrDuplicate,
	})
	client := newAPITestClient(t, server)
	project, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_pa_partial_graph",
		Name: "Assistant Partial Graph",
	})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateRole(t.Context(), projectwork.AgentRoleProfile{
		ID:        "role_pa_partial",
		ProjectID: project.ID,
		Name:      "Implementer",
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	proposed := mustRequestJSON[projectAssistantProposalResponse](client, http.MethodPost, "/hecate/v1/project-assistant/propose", `{
		"id":"pa_partial_graph",
		"title":"Partially create coordination graph",
		"summary":"Create work, then fail assignment creation.",
		"actions":[{
			"kind":"create_work_item",
			"reason":"The first action should commit.",
			"patch":{
				"id":"work_pa_partial",
				"project_id":"proj_pa_partial_graph",
				"title":"Committed partial work",
				"brief":"This work item should mirror even when the next action fails.",
				"status":"ready",
				"priority":"normal",
				"owner_role_id":"role_pa_partial"
			}
		},{
			"kind":"create_assignment",
			"reason":"The injected store failure should stop here.",
			"patch":{
				"id":"asgn_pa_partial",
				"project_id":"proj_pa_partial_graph",
				"work_item_id":"work_pa_partial",
				"role_id":"role_pa_partial",
				"driver_kind":"hecate_task",
				"status":"queued"
			}
		}]
	}`)

	payload := mustRequestJSONStatus[projectAssistantErrorResponse](client, http.StatusConflict, http.MethodPost, "/hecate/v1/project-assistant/apply", projectJourneyJSON(t, map[string]any{
		"proposal": proposed.Data,
		"confirm":  true,
	}))
	if payload.Error.ApplyStatus != projectassistant.ApplyStatusPartialDueToRuntimeFailure || payload.Error.CommittedActionCount != 1 || payload.Error.ResumeActionIndex != 1 || payload.Error.FailedActionIndex != 1 {
		t.Fatalf("partial apply error = %+v, want one committed action and failed assignment action", payload.Error)
	}
	if len(payload.Error.PartialResult.Actions) != 1 || payload.Error.PartialResult.Actions[0].ID != "work_pa_partial" {
		t.Fatalf("partial result actions = %+v, want committed work item only", payload.Error.PartialResult.Actions)
	}

	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	workItem, err := service.GetWorkItem(t.Context(), project.ID, "work_pa_partial")
	if err != nil {
		t.Fatalf("GetWorkItem(work_pa_partial): %v", err)
	}
	if workItem.Title != "Committed partial work" || workItem.OwnerRoleID != "role_pa_partial" {
		t.Fatalf("mirrored partial work item = %+v, want committed work item", workItem)
	}
	if _, err := service.GetAssignment(t.Context(), project.ID, "asgn_pa_partial"); err == nil {
		t.Fatalf("GetAssignment(asgn_pa_partial) succeeded, want no mirrored failed assignment")
	} else if !errors.Is(err, cairnline.ErrNotFound) {
		t.Fatalf("GetAssignment(asgn_pa_partial) err = %v, want ErrNotFound", err)
	}
	record := getMirroredCairnlineAssistantProposalForTest(t, handler, proposed.Data.ID)
	if record.Status != cairnline.AssistantProposalStatusPartial || record.LatestResult == nil || record.LatestResult.AppliedActionCount != 1 || len(record.ApplyAttempts) != 1 {
		t.Fatalf("mirrored partial proposal = %+v, want partial ledger with one attempt", record)
	}
}

func TestProjectAssistantAPI_ProjectSideEffectsMirrorThroughNarrowCairnlineSeams(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantCairnlineMirrorTestHandler(t)
	project, err := handler.projects.Create(t.Context(), projects.Project{
		ID:            "proj_assistant_project_mirror",
		Name:          "Assistant Project Mirror",
		DefaultRootID: "root_main",
		Roots: []projects.Root{{
			ID:     "root_main",
			Path:   "/workspace/main",
			Kind:   "git",
			Active: true,
		}},
	})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if err := handler.writeProjectIdentityToCairnline(t.Context(), project); err != nil {
		t.Fatalf("write initial Cairnline project: %v", err)
	}
	seedCairnlineOnlyProjectGraphForTest(t, handler, project.ID)

	proposal := projectassistant.Proposal{
		ID:                   "pa_assistant_project_mirror",
		Title:                "Update project state",
		RequiresConfirmation: true,
		Actions: []projectassistant.Action{
			{
				Kind:   projectassistant.ActionUpdateProject,
				Target: map[string]string{"project_id": project.ID},
				Patch:  json.RawMessage(`{"name":"Assistant Project Mirror Updated","description":"Mirrored through metadata seam"}`),
			},
			{
				Kind:   projectassistant.ActionAttachProjectRoot,
				Target: map[string]string{"project_id": project.ID},
				Patch:  json.RawMessage(`{"id":"root_attached","path":"/workspace/attached","kind":"git_worktree","active":true}`),
			},
			{
				Kind:   projectassistant.ActionSetProjectDefaults,
				Target: map[string]string{"project_id": project.ID},
				Patch:  json.RawMessage(`{"default_root_id":"root_attached","default_provider":"anthropic","default_model":"claude-sonnet-4-5","default_agent_profile":"architecture"}`),
			},
			{
				Kind:   projectassistant.ActionRemoveProjectRoot,
				Target: map[string]string{"project_id": project.ID, "root_id": "root_main"},
			},
		},
	}
	applyBody, err := json.Marshal(map[string]any{"proposal": proposal, "confirm": true})
	if err != nil {
		t.Fatalf("marshal apply body: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/apply", bytes.NewReader(applyBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var applied projectAssistantApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &applied); err != nil {
		t.Fatalf("decode apply response: %v", err)
	}
	if applied.Data.CommittedActionCount != 4 || len(applied.Data.Actions) != 4 {
		t.Fatalf("apply result = %+v, want four committed actions", applied.Data)
	}

	mirrored := getMirroredCairnlineProjectForTest(t, handler, project.ID)
	if mirrored.Name != "Assistant Project Mirror Updated" || mirrored.Description != "Mirrored through metadata seam" {
		t.Fatalf("mirrored project metadata = %+v, want assistant-updated metadata", mirrored)
	}
	if findMirroredCairnlineRootForTest(mirrored.Roots, "root_attached") == nil {
		t.Fatalf("mirrored roots = %+v, want attached root", mirrored.Roots)
	}
	if findMirroredCairnlineRootForTest(mirrored.Roots, "root_main") != nil {
		t.Fatalf("mirrored roots = %+v, want removed root_main absent", mirrored.Roots)
	}
	if findMirroredCairnlineRootForTest(mirrored.Roots, "root_cairnline_only") == nil {
		t.Fatalf("mirrored roots = %+v, want Cairnline-only root preserved", mirrored.Roots)
	}
	if findMirroredCairnlineSourceForTest(mirrored.ContextSources, "ctx_cairnline_only") == nil {
		t.Fatalf("mirrored sources = %+v, want Cairnline-only source preserved", mirrored.ContextSources)
	}
	if mirrored.DefaultRootID != "root_attached" || mirrored.DefaultProfileID != "architecture" {
		t.Fatalf("mirrored defaults = %+v, want attached root and architecture profile", mirrored)
	}
	assertMirroredExecutionProfileForTest(t, handler, mirrored.DefaultExecutionProfileID, "anthropic", "claude-sonnet-4-5")
}

func TestProjectAssistantAPI_DraftReviewFollowUpProposal(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantTestHandler()
	project, err := handler.projects.Create(t.Context(), projects.Project{ID: "proj_review_api", Name: "Review API"})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	workItem, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:          "work_review_api",
		ProjectID:   project.ID,
		Title:       "Review requested changes",
		Status:      projectwork.WorkItemStatusReview,
		OwnerRoleID: "product_manager",
	})
	if err != nil {
		t.Fatalf("Create work item: %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(t.Context(), projectwork.Assignment{
		ID:         "asgn_impl",
		ProjectID:  project.ID,
		WorkItemID: workItem.ID,
		RoleID:     "software_developer",
		DriverKind: projectwork.AssignmentDriverHecateTask,
		Status:     projectwork.AssignmentStatusCompleted,
	}); err != nil {
		t.Fatalf("CreateAssignment(impl): %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(t.Context(), projectwork.Assignment{
		ID:         "asgn_review",
		ProjectID:  project.ID,
		WorkItemID: workItem.ID,
		RoleID:     "reviewer_qa",
		DriverKind: projectwork.AssignmentDriverHecateTask,
		Status:     projectwork.AssignmentStatusCompleted,
	}); err != nil {
		t.Fatalf("CreateAssignment(review): %v", err)
	}
	if _, err := handler.projectWork.CreateArtifact(t.Context(), projectwork.CollaborationArtifact{
		ID:                     "artifact_review",
		ProjectID:              project.ID,
		WorkItemID:             workItem.ID,
		AssignmentID:           "asgn_review",
		Kind:                   projectwork.ArtifactKindReview,
		Title:                  "Architecture review",
		Body:                   "Verdict: Changes requested.",
		ReviewedAssignmentID:   "asgn_impl",
		ReviewVerdict:          projectwork.ReviewVerdictChangesRequested,
		ReviewFollowUpRequired: true,
	}); err != nil {
		t.Fatalf("CreateArtifact(review): %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/draft", bytes.NewReader([]byte(`{
		"project_id":"proj_review_api",
		"work_item_id":"work_review_api",
		"draft_mode":"review_follow_up",
		"review_artifact_id":"artifact_review"
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("draft review follow-up status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var proposed projectAssistantProposalResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &proposed); err != nil {
		t.Fatalf("decode draft response: %v", err)
	}
	if proposed.Object != "project_assistant.proposal" || len(proposed.Data.Actions) != 3 {
		t.Fatalf("draft response = %+v, want three-action review follow-up proposal", proposed)
	}
	if proposed.Data.Actions[0].Kind != projectassistant.ActionCreateHandoff || proposed.Data.Actions[1].Kind != projectassistant.ActionCreateAssignment || proposed.Data.Actions[2].Kind != projectassistant.ActionUpdateHandoff {
		t.Fatalf("actions = %+v, want create_handoff/create_assignment/update_handoff", proposed.Data.Actions)
	}
	var assignmentPatch map[string]string
	if err := json.Unmarshal(proposed.Data.Actions[1].Patch, &assignmentPatch); err != nil {
		t.Fatalf("decode assignment patch: %v", err)
	}
	if assignmentPatch["work_item_id"] != workItem.ID || assignmentPatch["role_id"] != "software_developer" || assignmentPatch["status"] != projectwork.AssignmentStatusQueued {
		t.Fatalf("assignment patch = %+v, want queued reviewed-role assignment", assignmentPatch)
	}
	assignments, err := handler.projectWork.ListAssignments(t.Context(), projectwork.AssignmentFilter{ProjectID: project.ID, WorkItemID: workItem.ID})
	if err != nil {
		t.Fatalf("ListAssignments: %v", err)
	}
	if len(assignments) != 2 {
		t.Fatalf("assignments = %+v, want draft route not to mutate project work", assignments)
	}
}

func TestProjectAssistantAPI_DraftReviewFollowUpUsesCairnlineReadModelWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantCairnlineReadTestHandler()
	project, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_cairnline_review_draft",
		Name: "Cairnline Review Draft",
		Roots: []projects.Root{{
			ID:     "root_cairnline_review",
			Path:   t.TempDir(),
			Active: true,
		}},
	})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	for _, role := range []projectwork.AgentRoleProfile{
		{ID: "operator", ProjectID: project.ID, Name: "Operator"},
		{ID: "implementation", ProjectID: project.ID, Name: "Implementation"},
		{ID: "reviewer", ProjectID: project.ID, Name: "Reviewer"},
	} {
		if _, err := handler.projectWork.CreateRole(t.Context(), role); err != nil {
			t.Fatalf("CreateRole(%s): %v", role.ID, err)
		}
	}
	workItem, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:          "work_cairnline_review_draft",
		ProjectID:   project.ID,
		Title:       "Review follow-up through Cairnline",
		Status:      projectwork.WorkItemStatusReview,
		OwnerRoleID: "operator",
		RootID:      "root_cairnline_review",
	})
	if err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(t.Context(), projectwork.Assignment{
		ID:         "asgn_cairnline_reviewed",
		ProjectID:  project.ID,
		WorkItemID: workItem.ID,
		RoleID:     "implementation",
		DriverKind: projectwork.AssignmentDriverHecateTask,
		Status:     projectwork.AssignmentStatusCompleted,
	}); err != nil {
		t.Fatalf("CreateAssignment(reviewed): %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(t.Context(), projectwork.Assignment{
		ID:         "asgn_cairnline_reviewer",
		ProjectID:  project.ID,
		WorkItemID: workItem.ID,
		RoleID:     "reviewer",
		DriverKind: projectwork.AssignmentDriverExternalAgent,
		Status:     projectwork.AssignmentStatusCompleted,
	}); err != nil {
		t.Fatalf("CreateAssignment(reviewer): %v", err)
	}
	if _, err := handler.projectWork.CreateArtifact(t.Context(), projectwork.CollaborationArtifact{
		ID:                     "artifact_cairnline_review",
		ProjectID:              project.ID,
		WorkItemID:             workItem.ID,
		AssignmentID:           "asgn_cairnline_reviewer",
		Kind:                   projectwork.ArtifactKindReview,
		Title:                  "Implementation review",
		Body:                   "Verdict: Changes requested.",
		ReviewedAssignmentID:   "asgn_cairnline_reviewed",
		ReviewVerdict:          projectwork.ReviewVerdictChangesRequested,
		ReviewRisk:             projectwork.ReviewRiskMedium,
		ReviewFollowUpRequired: true,
	}); err != nil {
		t.Fatalf("CreateArtifact(review): %v", err)
	}
	poisonNativeProjectAssistantCache(handler)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/draft", bytes.NewReader([]byte(`{
		"project_id":"proj_cairnline_review_draft",
		"work_item_id":"work_cairnline_review_draft",
		"draft_mode":"review_follow_up",
		"review_artifact_id":"artifact_cairnline_review"
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("draft review follow-up status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var proposed projectAssistantProposalResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &proposed); err != nil {
		t.Fatalf("decode draft response: %v", err)
	}
	if len(proposed.Data.Actions) != 3 || proposed.Data.Actions[0].Kind != projectassistant.ActionCreateHandoff || proposed.Data.Actions[1].Kind != projectassistant.ActionCreateAssignment || proposed.Data.Actions[2].Kind != projectassistant.ActionUpdateHandoff {
		t.Fatalf("actions = %+v, want Cairnline-backed review follow-up handoff/assignment/link", proposed.Data.Actions)
	}
	var assignmentPatch map[string]string
	if err := json.Unmarshal(proposed.Data.Actions[1].Patch, &assignmentPatch); err != nil {
		t.Fatalf("decode assignment patch: %v", err)
	}
	if assignmentPatch["role_id"] != "implementation" || assignmentPatch["driver_kind"] != projectwork.AssignmentDriverHecateTask || assignmentPatch["root_id"] != workItem.RootID {
		t.Fatalf("assignment patch = %+v, want reviewed assignment role/driver and work item root", assignmentPatch)
	}
}

func TestProjectAssistantAPI_ChatDraftDerivesProjectFromSession(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantTestHandler()
	project, err := handler.projects.Create(t.Context(), projects.Project{ID: "proj_chat_draft", Name: "Chat Draft Project"})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	session, err := handler.agentChat.Create(t.Context(), chat.Session{
		ID:        "chat_pa_draft",
		Title:     "Project chat",
		ProjectID: project.ID,
		AgentID:   chat.DefaultAgentID,
		Provider:  "ollama",
		Model:     "qwen2.5-coder",
		Status:    "idle",
	})
	if err != nil {
		t.Fatalf("Create chat: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/chat/sessions/"+session.ID+"/project-assistant/draft", bytes.NewReader([]byte(`{
		"request":"Plan next project work\nCapture a reviewable task.",
		"draft_mode":"model",
		"provider":"openai",
		"model":"gpt-test"
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("chat draft status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var proposed projectAssistantProposalResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &proposed); err != nil {
		t.Fatalf("decode chat draft response: %v", err)
	}
	if proposed.Object != "project_assistant.proposal" || len(proposed.Data.Actions) != 1 {
		t.Fatalf("chat draft response = %+v, want one proposal action", proposed)
	}
	if proposed.Data.Actions[0].Kind != projectassistant.ActionCreateWorkItem {
		t.Fatalf("action kind = %q, want create_work_item", proposed.Data.Actions[0].Kind)
	}
	var patch map[string]any
	if err := json.Unmarshal(proposed.Data.Actions[0].Patch, &patch); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patch["project_id"] != project.ID || patch["title"] != "Plan next project work" {
		t.Fatalf("patch = %+v, want linked project and request-derived title", patch)
	}
	updatedSession, ok, err := handler.agentChat.Get(t.Context(), session.ID)
	if err != nil || !ok {
		t.Fatalf("Get chat = ok %v err %v", ok, err)
	}
	if len(updatedSession.Messages) != 0 {
		t.Fatalf("chat messages = %+v, want draft route not to append messages", updatedSession.Messages)
	}
	workItems, err := handler.projectWork.ListWorkItems(t.Context(), project.ID)
	if err != nil {
		t.Fatalf("ListWorkItems: %v", err)
	}
	if len(workItems) != 0 {
		t.Fatalf("work items = %+v, want draft route not to mutate project work", workItems)
	}
}

func TestProjectAssistantAPI_ChatDraftUsesCairnlineReadModelWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantCairnlineReadTestHandler()
	project, err := handler.projects.Create(t.Context(), projects.Project{ID: "proj_chat_cairnline_draft", Name: "Chat Cairnline Draft"})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	session, err := handler.agentChat.Create(t.Context(), chat.Session{
		ID:        "chat_cairnline_pa_draft",
		Title:     "Project chat",
		ProjectID: project.ID,
		AgentID:   chat.DefaultAgentID,
		Status:    "idle",
	})
	if err != nil {
		t.Fatalf("Create chat: %v", err)
	}
	poisonNativeProjectAssistantCache(handler)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/chat/sessions/"+session.ID+"/project-assistant/draft", bytes.NewReader([]byte(`{
		"request":"Plan Cairnline chat work\nCapture a portable draft."
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("chat draft status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var proposed projectAssistantProposalResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &proposed); err != nil {
		t.Fatalf("decode chat draft response: %v", err)
	}
	if proposed.Object != "project_assistant.proposal" || len(proposed.Data.Actions) != 1 || proposed.Data.Actions[0].Kind != projectassistant.ActionCreateWorkItem {
		t.Fatalf("chat draft response = %+v, want Cairnline-backed create_work_item proposal", proposed)
	}
}

func TestDraftProjectProposalUsesCairnlineReadModelWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, _ := newProjectAssistantCairnlineReadTestHandler()
	project, err := handler.projects.Create(t.Context(), projects.Project{ID: "proj_tool_cairnline_draft", Name: "Tool Cairnline Draft"})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	poisonNativeProjectAssistantCache(handler)

	result, err := handler.DraftProjectProposal(t.Context(), orchestrator.ProjectAssistantDraftInput{
		ProjectID: project.ID,
		Request:   "Plan Cairnline tool work\nCapture a portable draft.",
		RequestID: "req_tool_cairnline",
		TraceID:   "trace_tool_cairnline",
	})
	if err != nil {
		t.Fatalf("DraftProjectProposal() error = %v", err)
	}
	if result.ProjectID != project.ID || result.ProposalID == "" || result.ActionCount != 1 {
		t.Fatalf("result = %+v, want Cairnline-backed one-action proposal", result)
	}
}

func TestProjectAssistantAPI_ChatDraftRequiresLinkedHecateSession(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantTestHandler()
	if _, err := handler.agentChat.Create(t.Context(), chat.Session{
		ID:      "chat_unlinked",
		Title:   "No project",
		AgentID: chat.DefaultAgentID,
		Status:  "idle",
	}); err != nil {
		t.Fatalf("Create unlinked chat: %v", err)
	}
	if _, err := handler.agentChat.Create(t.Context(), chat.Session{
		ID:        "chat_external",
		Title:     "External",
		ProjectID: "proj_unused",
		AgentID:   "claude_code",
		Status:    "idle",
	}); err != nil {
		t.Fatalf("Create external chat: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/chat/sessions/chat_unlinked/project-assistant/draft", strings.NewReader(`{"request":"Plan work"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unlinked status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	var unlinked projectAssistantErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &unlinked); err != nil {
		t.Fatalf("decode unlinked error: %v", err)
	}
	if unlinked.Error.Type != errCodeInvalidRequest {
		t.Fatalf("unlinked error type = %q, want %s", unlinked.Error.Type, errCodeInvalidRequest)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/chat/sessions/chat_external/project-assistant/draft", strings.NewReader(`{"request":"Plan work"}`)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("external status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	var external projectAssistantErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &external); err != nil {
		t.Fatalf("decode external error: %v", err)
	}
	if external.Error.Type != errCodeRuntimeMismatch {
		t.Fatalf("external error type = %q, want %s", external.Error.Type, errCodeRuntimeMismatch)
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

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/project-assistant/proposals/pa_api", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get proposal status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var proposalRecord projectAssistantProposalRecordResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &proposalRecord); err != nil {
		t.Fatalf("decode proposal record response: %v", err)
	}
	if proposalRecord.Object != "project_assistant.proposal_record" || proposalRecord.Data.ID != "pa_api" || proposalRecord.Data.Status != projectassistant.ProposalStatusProposed {
		t.Fatalf("proposal record = %+v, want proposed record", proposalRecord)
	}
	if proposalRecord.Data.ProjectID != "proj_api" || proposalRecord.Data.Source != projectassistant.ProposalSourceAPI {
		t.Fatalf("proposal record project/source = %q/%q, want proj_api/api", proposalRecord.Data.ProjectID, proposalRecord.Data.Source)
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
	if applied.Object != "project_assistant.apply_result" || applied.Data.Status != projectassistant.ApplyStatusApplied || !applied.Data.Applied || applied.Data.ProposalID != "pa_api" {
		t.Fatalf("apply response = %+v, want applied project assistant result", applied)
	}
	if applied.Data.TotalActionCount != 1 || applied.Data.CommittedActionCount != 1 || applied.Data.ResumeActionIndex != 1 || applied.Data.FailedActionIndex != nil {
		t.Fatalf("apply progress = %+v, want applied counts for one action", applied.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/project-assistant/proposals/pa_api", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get applied proposal status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &proposalRecord); err != nil {
		t.Fatalf("decode applied proposal record response: %v", err)
	}
	if proposalRecord.Data.Status != projectassistant.ApplyStatusApplied || proposalRecord.Data.LatestResult == nil || !proposalRecord.Data.LatestResult.Applied || len(proposalRecord.Data.ApplyAttempts) != 1 {
		t.Fatalf("applied proposal record = %+v, want applied latest result with one attempt", proposalRecord.Data)
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

func TestProjectAssistantAPI_ProposalReadUsesCairnlineReadModel(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetAgentChatStore(chat.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetProjectSkillStore(projectskills.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	server := NewServer(quietLogger(), handler)
	project, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_cairnline_proposal",
		Name: "Cairnline Proposal",
	})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	proposal := projectassistant.Proposal{
		ID:                   "pa_cairnline_get",
		Title:                "Create first task",
		Summary:              "Queue one work item.",
		RequiresConfirmation: true,
		Actions: []projectassistant.Action{{
			Kind:   projectassistant.ActionCreateWorkItem,
			Target: map[string]string{"project_id": project.ID},
			Patch: json.RawMessage(`{
				"id":"work_cairnline_from_proposal",
				"project_id":"proj_cairnline_proposal",
				"title":"Cairnline-projected work",
				"brief":"Read the proposal from the Cairnline read model.",
				"status":"ready",
				"priority":"normal"
			}`),
			Reason: "Capture the first reviewable work item.",
		}},
	}
	if _, err := handler.projectAssistantProposals.UpsertProposal(t.Context(), projectassistant.ProposalRecord{
		ID:        proposal.ID,
		ProjectID: project.ID,
		Source:    projectassistant.ProposalSourceDraft,
		Proposal:  proposal,
		Status:    projectassistant.ProposalStatusProposed,
	}); err != nil {
		t.Fatalf("Upsert proposal: %v", err)
	}
	result := projectassistant.ApplyResult{
		ProposalID:           proposal.ID,
		Status:               projectassistant.ApplyStatusApplied,
		Applied:              true,
		TotalActionCount:     1,
		CommittedActionCount: 1,
		ResumeActionIndex:    1,
		Actions: []projectassistant.ActionResult{{
			Kind: projectassistant.ActionCreateWorkItem,
			ID:   "work_cairnline_from_proposal",
			Data: map[string]string{
				"project_id":   project.ID,
				"work_item_id": "work_cairnline_from_proposal",
			},
		}},
	}
	if _, err := handler.projectAssistantProposals.RecordApplyAttempt(t.Context(), projectassistant.ApplyAttempt{
		ID:         "paatt_cairnline_get",
		ProposalID: proposal.ID,
		Status:     projectassistant.ApplyStatusApplied,
		Confirmed:  true,
		Result:     result,
	}); err != nil {
		t.Fatalf("Record apply attempt: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/project-assistant/proposals/pa_cairnline_get", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get proposal status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response projectAssistantProposalRecordResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode proposal record response: %v", err)
	}
	if response.Object != "project_assistant.proposal_record" || response.Data.ID != proposal.ID || response.Data.ProjectID != project.ID {
		t.Fatalf("proposal response = %+v, want Cairnline-projected proposal record", response)
	}
	if response.Data.Status != projectassistant.ApplyStatusApplied || response.Data.LatestResult == nil || response.Data.LatestResult.CommittedActionCount != 1 || len(response.Data.ApplyAttempts) != 1 {
		t.Fatalf("proposal ledger = %+v, want applied latest result with one attempt", response.Data)
	}
	if len(response.Data.Proposal.Actions) != 1 || response.Data.Proposal.Actions[0].Kind != projectassistant.ActionCreateWorkItem || response.Data.Proposal.Actions[0].Reason != "Capture the first reviewable work item." {
		t.Fatalf("proposal actions = %+v, want Hecate-shaped action projected from Cairnline", response.Data.Proposal.Actions)
	}
	var patch map[string]any
	if err := json.Unmarshal(response.Data.Proposal.Actions[0].Patch, &patch); err != nil {
		t.Fatalf("decode projected action patch: %v", err)
	}
	if patch["id"] != "work_cairnline_from_proposal" || patch["project_id"] != project.ID {
		t.Fatalf("projected patch = %+v, want work item patch reconstructed from Cairnline", patch)
	}
}

func TestProjectAssistantAPI_ProposalReadUsesCairnlineSidecarWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineSidecarReadTestServer(t, "full")
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar assistant proposal enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("sidecar read-route predicate = false, want true")
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/project-assistant/proposals/pa_fixture", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get proposal status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response projectAssistantProposalRecordResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode proposal record response: %v", err)
	}
	if response.Object != "project_assistant.proposal_record" || response.Data.ID != "pa_fixture" || response.Data.ProjectID != "proj_fixture" {
		t.Fatalf("proposal response = %+v, want sidecar fixture proposal record", response)
	}
	if response.Data.Status != projectassistant.ProposalStatusProposed || response.Data.Proposal.Title != "Queue fixture work" {
		t.Fatalf("proposal ledger = %+v, want proposed sidecar assistant proposal", response.Data)
	}
	if len(response.Data.Proposal.Actions) != 1 || response.Data.Proposal.Actions[0].Kind != projectassistant.ActionCreateWorkItem || response.Data.Proposal.Actions[0].Reason != "Capture the next sidecar-backed project task." {
		t.Fatalf("proposal actions = %+v, want converted create_work_item action", response.Data.Proposal.Actions)
	}
}

func TestProjectAssistantAPI_ProposalReadSidecarFallsBackToNativeLedgerWhenMissing(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "full")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/propose", bytes.NewReader([]byte(`{
		"id":"pa_native_sidecar",
		"title":"Create native proposal",
		"summary":"Create a Hecate-owned proposal while sidecar reads are enabled.",
		"actions":[{
			"kind":"create_project",
			"reason":"Exercise native proposal fallback.",
			"patch":{
				"id":"proj_native_sidecar",
				"name":"Native Sidecar Proposal"
			}
		}]
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("propose status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/project-assistant/proposals/pa_native_sidecar", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get proposal status = %d body=%s, want 200 native fallback after sidecar miss", rec.Code, rec.Body.String())
	}
	var response projectAssistantProposalRecordResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode proposal record response: %v", err)
	}
	if response.Data.ID != "pa_native_sidecar" || response.Data.ProjectID != "proj_native_sidecar" || response.Data.Source != projectassistant.ProposalSourceAPI {
		t.Fatalf("proposal record = %+v, want Hecate-owned API proposal after sidecar miss", response.Data)
	}
}

func TestProjectAssistantAPI_ProposalReadCairnlineSidecarRequiresStructuredContent(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "assistant.proposals.get-text-only")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/project-assistant/proposals/pa_fixture", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("get proposal status = %d body=%s, want 502", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "structuredContent") {
		t.Fatalf("error body = %s, want structuredContent diagnostic", rec.Body.String())
	}
}

func TestProjectAssistantAPI_ProposalReadCairnlineSidecarRequiresMatchingID(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "assistant.proposals.get-id-mismatch")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/project-assistant/proposals/pa_fixture", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("get proposal status = %d body=%s, want 502", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "returned proposal id pa_fixture_other for requested id pa_fixture") {
		t.Fatalf("error body = %s, want proposal id mismatch diagnostic", rec.Body.String())
	}
}

func TestProjectAssistantAPI_ProposalReadCairnlineSidecarMissingProposal(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "full")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/project-assistant/proposals/pa_missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get proposal status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectAssistantAPI_ApplyPreflightFailureIncludesEmptyProgress(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantTestHandler()
	proposal := projectassistant.Proposal{
		ID:                   "pa_preflight_api",
		Title:                "Preflight apply",
		RequiresConfirmation: true,
		Actions: []projectassistant.Action{
			{
				Kind:  projectassistant.ActionCreateProject,
				Patch: json.RawMessage(`{"id":"proj_preflight_api","name":"Preflight API"}`),
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
		t.Fatalf("preflight apply status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
	var payload projectAssistantErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode preflight apply error: %v", err)
	}
	if payload.Error.Type != errCodeNotFound || payload.Error.ApplyStatus != projectassistant.ApplyStatusBlockedBeforeApply || payload.Error.FailedActionIndex != 1 {
		t.Fatalf("error = %+v, want not_found at action index 1", payload.Error)
	}
	if payload.Error.PartialResult.Status != projectassistant.ApplyStatusBlockedBeforeApply {
		t.Fatalf("partial_result status = %q, want %q", payload.Error.PartialResult.Status, projectassistant.ApplyStatusBlockedBeforeApply)
	}
	if payload.Error.TotalActionCount != 2 || payload.Error.CommittedActionCount != 0 || payload.Error.ResumeActionIndex != 0 {
		t.Fatalf("error progress = %+v, want failed action 1 and resume action 0", payload.Error)
	}
	partial := payload.Error.PartialResult
	if partial.ProposalID != "pa_preflight_api" || partial.Applied || len(partial.Actions) != 0 {
		t.Fatalf("partial_result = %+v, want no action results before preflight failure", partial)
	}
	if partial.TotalActionCount != 2 || partial.CommittedActionCount != 0 || partial.ResumeActionIndex != 0 || partial.FailedActionIndex == nil || *partial.FailedActionIndex != 1 {
		t.Fatalf("partial_result progress = %+v, want failed action 1 and resume action 0", partial)
	}
	if _, ok, err := handler.projects.Get(t.Context(), "proj_preflight_api"); err != nil || ok {
		t.Fatalf("Get preflight-blocked project ok=%v err=%v, want no durable mutation", ok, err)
	}
}

func TestProjectAssistantAPI_ApplyDoneReturnsCloseoutReadiness(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantTestHandler()
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_assistant_closeout",
		Name: "Assistant Closeout",
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:        "work_assistant_closeout",
		ProjectID: "proj_assistant_closeout",
		Title:     "Guard assistant closeout",
		Status:    projectwork.WorkItemStatusReview,
	}); err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(t.Context(), projectwork.Assignment{
		ID:         "asgn_assistant_closeout",
		ProjectID:  "proj_assistant_closeout",
		WorkItemID: "work_assistant_closeout",
		RoleID:     "software_developer",
		Status:     projectwork.AssignmentStatusCompleted,
	}); err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}
	proposal := projectassistant.Proposal{
		ID:                   "pa_assistant_closeout",
		Title:                "Mark done",
		RequiresConfirmation: true,
		Actions: []projectassistant.Action{{
			Kind:   projectassistant.ActionUpdateWorkItem,
			Target: map[string]string{"project_id": "proj_assistant_closeout", "work_item_id": "work_assistant_closeout"},
			Patch:  json.RawMessage(`{"status":"done"}`),
		}},
	}
	applyBody, err := json.Marshal(map[string]any{"proposal": proposal, "confirm": true})
	if err != nil {
		t.Fatalf("marshal apply body: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/apply", bytes.NewReader(applyBody)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("assistant closeout apply status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	var payload struct {
		Error struct {
			Type                 string                           `json:"type"`
			Message              string                           `json:"message"`
			OperatorAction       string                           `json:"operator_action"`
			ApplyStatus          string                           `json:"apply_status"`
			FailedActionIndex    int                              `json:"failed_action_index"`
			TotalActionCount     int                              `json:"total_action_count"`
			CommittedActionCount int                              `json:"committed_action_count"`
			ResumeActionIndex    int                              `json:"resume_action_index"`
			PartialResult        projectassistant.ApplyResult     `json:"partial_result"`
			Readiness            ProjectWorkItemReadinessResponse `json:"readiness"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode assistant closeout error: %v", err)
	}
	if payload.Error.Type != errCodeConflict || payload.Error.ApplyStatus != projectassistant.ApplyStatusBlockedBeforeApply || payload.Error.FailedActionIndex != 0 || payload.Error.OperatorAction == "" {
		t.Fatalf("assistant closeout error = %+v, want conflict at action 0 with operator action", payload.Error)
	}
	if payload.Error.TotalActionCount != 1 || payload.Error.CommittedActionCount != 0 || payload.Error.ResumeActionIndex != 0 {
		t.Fatalf("assistant closeout progress = %+v, want failed action 0 and resume action 0", payload.Error)
	}
	if payload.Error.PartialResult.ProposalID != "pa_assistant_closeout" || payload.Error.PartialResult.Applied || len(payload.Error.PartialResult.Actions) != 0 {
		t.Fatalf("partial_result = %+v, want no committed assistant actions", payload.Error.PartialResult)
	}
	if payload.Error.PartialResult.TotalActionCount != 1 || payload.Error.PartialResult.CommittedActionCount != 0 || payload.Error.PartialResult.ResumeActionIndex != 0 || payload.Error.PartialResult.FailedActionIndex == nil || *payload.Error.PartialResult.FailedActionIndex != 0 {
		t.Fatalf("partial_result progress = %+v, want failed action 0 and resume action 0", payload.Error.PartialResult)
	}
	if payload.Error.Readiness.Ready || payload.Error.Readiness.Status != "blocked" ||
		len(payload.Error.Readiness.MissingEvidenceAssignmentIDs) != 1 ||
		payload.Error.Readiness.MissingEvidenceAssignmentIDs[0] != "asgn_assistant_closeout" {
		t.Fatalf("assistant closeout readiness = %+v, want missing evidence blocker", payload.Error.Readiness)
	}
	stored, ok, err := handler.projectWork.GetWorkItem(t.Context(), "proj_assistant_closeout", "work_assistant_closeout")
	if err != nil || !ok {
		t.Fatalf("GetWorkItem() ok=%v err=%v, want stored work item", ok, err)
	}
	if stored.Status == projectwork.WorkItemStatusDone {
		t.Fatalf("stored status = %q, want closeout guard to keep item open", stored.Status)
	}
}

func getMirroredCairnlineAssistantProposalForTest(t *testing.T, handler *Handler, proposalID string) cairnline.AssistantProposalRecord {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	record, err := service.GetAssistantProposal(t.Context(), proposalID)
	if err != nil {
		t.Fatalf("GetAssistantProposal(%q): %v", proposalID, err)
	}
	return record
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
