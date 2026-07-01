package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func TestProjectSetupReadiness_ReadOnlyPristineProject(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_setup",
		Name: "Setup",
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}

	beforeWork, err := handler.projectWork.ListWorkItems(t.Context(), "proj_setup")
	if err != nil {
		t.Fatalf("ListWorkItems before: %v", err)
	}
	beforeRoles, err := handler.projectWork.ListRoles(t.Context(), "proj_setup")
	if err != nil {
		t.Fatalf("ListRoles before: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_setup/setup-readiness", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("setup readiness status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectSetupReadinessEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode setup readiness: %v", err)
	}
	if response.Object != "project_setup_readiness" || response.Data.ProjectID != "proj_setup" {
		t.Fatalf("setup readiness envelope = %+v, want project_setup_readiness for project", response)
	}
	if response.Data.ReadBackend != "hecate" {
		t.Fatalf("setup readiness read_backend = %q, want hecate", response.Data.ReadBackend)
	}
	if !response.Data.ShowOnboarding || response.Data.SetupStarted || response.Data.FirstWorkReady {
		t.Fatalf("setup readiness flags = %+v, want onboarding for pristine project", response.Data)
	}
	if response.Data.Summary.WorkItemCount != 0 || response.Data.Summary.RoleCount != 0 || !response.Data.Summary.MissingDefaults {
		t.Fatalf("setup readiness summary = %+v, want empty project with missing defaults", response.Data.Summary)
	}
	if response.Data.PrimaryAction.Type != projectSetupReadinessActionBootstrap || response.Data.PrimaryAction.Label != "Set up project" {
		t.Fatalf("primary action = %+v, want setup action", response.Data.PrimaryAction)
	}
	purpose := findProjectSetupReadinessCheckForTest(t, response.Data.Checks, "purpose")
	if purpose.Status != projectSetupReadinessStatusTodo || purpose.Action == nil || purpose.Action.Type != projectSetupReadinessActionProjectSettings {
		t.Fatalf("purpose check = %+v, want settings todo", purpose)
	}
	workspace := findProjectSetupReadinessCheckForTest(t, response.Data.Checks, "workspace_source")
	if workspace.Status != projectSetupReadinessStatusOptional || !workspace.Optional || workspace.Action != nil {
		t.Fatalf("workspace check = %+v, want optional without action", workspace)
	}
	sources := findProjectSetupReadinessCheckForTest(t, response.Data.Checks, "sources_memory")
	if sources.Status != projectSetupReadinessStatusTodo || sources.Action == nil || sources.Action.Type != projectSetupReadinessActionBootstrap {
		t.Fatalf("sources check = %+v, want bootstrap todo", sources)
	}
	firstWork := findProjectSetupReadinessCheckForTest(t, response.Data.Checks, "first_work_item")
	if firstWork.Status != projectSetupReadinessStatusTodo || firstWork.Action == nil || firstWork.Action.Type != projectSetupReadinessActionCreateWorkItem {
		t.Fatalf("first work check = %+v, want create-work todo", firstWork)
	}

	afterWork, err := handler.projectWork.ListWorkItems(t.Context(), "proj_setup")
	if err != nil {
		t.Fatalf("ListWorkItems after: %v", err)
	}
	afterRoles, err := handler.projectWork.ListRoles(t.Context(), "proj_setup")
	if err != nil {
		t.Fatalf("ListRoles after: %v", err)
	}
	if len(beforeWork) != len(afterWork) || len(beforeRoles) != len(afterRoles) {
		t.Fatalf("setup readiness mutated project state: work %d->%d roles %d->%d", len(beforeWork), len(afterWork), len(beforeRoles), len(afterRoles))
	}
}

func TestProjectSetupReadiness_SetupStartedFirstWorkReady(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	root := t.TempDir()
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:              "proj_ready",
		Name:            "Ready",
		Description:     "Coordinate release work.",
		DefaultProvider: "openai",
		DefaultModel:    "gpt-5",
		Roots:           []projects.Root{{ID: "root_ready", Path: root, Kind: "git", Active: true}},
		ContextSources: []projects.ContextSource{{
			ID:      "ctx_ready",
			Kind:    "workspace_instruction",
			Title:   "AGENTS.md",
			Path:    "AGENTS.md",
			Enabled: true,
		}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateRole(t.Context(), projectwork.AgentRoleProfile{
		ID:        "role_ready",
		ProjectID: "proj_ready",
		Name:      "Owner",
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if _, err := handler.projectSkills.UpsertDiscovered(t.Context(), "proj_ready", []projectskills.Skill{{
		ID:        "skill_ready",
		ProjectID: "proj_ready",
		Path:      "skills/ready/SKILL.md",
		Enabled:   true,
		Status:    projectskills.StatusAvailable,
	}}); err != nil {
		t.Fatalf("UpsertDiscovered: %v", err)
	}
	if _, err := handler.memory.Create(t.Context(), memory.Entry{
		ID:        "mem_ready",
		ProjectID: "proj_ready",
		Title:     "Release posture",
		Body:      "Keep setup explicit.",
		Enabled:   true,
	}); err != nil {
		t.Fatalf("Create memory: %v", err)
	}
	if _, err := handler.memoryCandidates.CreateCandidate(t.Context(), memory.Candidate{
		ID:        "memcand_ready",
		ProjectID: "proj_ready",
		Title:     "Candidate",
		Body:      "Review me.",
		Status:    memory.CandidateStatusPending,
	}); err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_ready/setup-readiness", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("setup readiness status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectSetupReadinessEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode setup readiness: %v", err)
	}
	if response.Data.ReadBackend != "hecate" {
		t.Fatalf("setup readiness read_backend = %q, want hecate", response.Data.ReadBackend)
	}
	if response.Data.ShowOnboarding || !response.Data.SetupStarted || !response.Data.FirstWorkReady {
		t.Fatalf("setup readiness flags = %+v, want setup started and first work ready", response.Data)
	}
	if response.Data.Summary.EnabledContextSourceCount != 1 || response.Data.Summary.RoleCount != 1 || response.Data.Summary.SkillCount != 1 || response.Data.Summary.SavedMemoryCount != 1 || response.Data.Summary.PendingMemoryCandidateCount != 1 {
		t.Fatalf("setup readiness summary = %+v, want setup counts", response.Data.Summary)
	}
	if response.Data.Summary.MissingDefaults || !response.Data.Summary.HasPurpose || !response.Data.Summary.HasActiveRoot {
		t.Fatalf("setup readiness summary = %+v, want ready project defaults/purpose/root", response.Data.Summary)
	}
	for _, id := range []string{"purpose", "workspace_source", "launch_defaults", "sources_memory", "roles"} {
		check := findProjectSetupReadinessCheckForTest(t, response.Data.Checks, id)
		if check.Status != projectSetupReadinessStatusReady || check.Action != nil {
			t.Fatalf("check %s = %+v, want ready without action", id, check)
		}
	}
	firstWork := findProjectSetupReadinessCheckForTest(t, response.Data.Checks, "first_work_item")
	if firstWork.Status != projectSetupReadinessStatusTodo || firstWork.Action == nil || firstWork.Action.Type != projectSetupReadinessActionCreateWorkItem {
		t.Fatalf("first work check = %+v, want create-work todo", firstWork)
	}
}

func TestProjectSetupReadiness_CairnlineConfiguredUsesReadModel(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)
	root := t.TempDir()
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:              "proj_cairnline_setup",
		Name:            "Cairnline setup",
		Description:     "Coordinate setup through Cairnline reads.",
		DefaultProvider: "openai",
		DefaultModel:    "gpt-5",
		Roots:           []projects.Root{{ID: "root_cairnline", Path: root, Kind: "git", Active: true}},
		ContextSources: []projects.ContextSource{{
			ID:      "ctx_cairnline",
			Kind:    "workspace_instruction",
			Title:   "AGENTS.md",
			Path:    "AGENTS.md",
			Enabled: true,
		}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateRole(t.Context(), projectwork.AgentRoleProfile{
		ID:        "role_cairnline",
		ProjectID: "proj_cairnline_setup",
		Name:      "Coordinator",
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if _, err := handler.projectSkills.UpsertDiscovered(t.Context(), "proj_cairnline_setup", []projectskills.Skill{{
		ID:        "skill_cairnline",
		ProjectID: "proj_cairnline_setup",
		Path:      "skills/cairnline/SKILL.md",
		Enabled:   true,
		Status:    projectskills.StatusAvailable,
	}}); err != nil {
		t.Fatalf("UpsertDiscovered: %v", err)
	}
	if _, err := handler.memory.Create(t.Context(), memory.Entry{
		ID:        "mem_cairnline",
		ProjectID: "proj_cairnline_setup",
		Title:     "Setup posture",
		Body:      "Use Cairnline for portable setup reads.",
		Enabled:   true,
	}); err != nil {
		t.Fatalf("Create memory: %v", err)
	}
	if _, err := handler.memoryCandidates.CreateCandidate(t.Context(), memory.Candidate{
		ID:        "memcand_cairnline",
		ProjectID: "proj_cairnline_setup",
		Title:     "Candidate",
		Body:      "Review me.",
		Status:    memory.CandidateStatusPending,
	}); err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_cairnline_setup/setup-readiness", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("setup readiness status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectSetupReadinessEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode setup readiness: %v", err)
	}
	if response.Data.ReadBackend != "cairnline" {
		t.Fatalf("setup readiness read_backend = %q, want cairnline", response.Data.ReadBackend)
	}
	if response.Data.ShowOnboarding || !response.Data.SetupStarted || !response.Data.FirstWorkReady {
		t.Fatalf("setup readiness flags = %+v, want Cairnline setup started and first work ready", response.Data)
	}
	if response.Data.Summary.EnabledContextSourceCount != 1 || response.Data.Summary.RoleCount != 1 || response.Data.Summary.SkillCount != 1 || response.Data.Summary.SavedMemoryCount != 1 || response.Data.Summary.PendingMemoryCandidateCount != 1 {
		t.Fatalf("setup readiness summary = %+v, want Cairnline-backed setup counts", response.Data.Summary)
	}
	if response.Data.Summary.MissingDefaults || !response.Data.Summary.HasPurpose || !response.Data.Summary.HasActiveRoot {
		t.Fatalf("setup readiness summary = %+v, want Hecate defaults/purpose/root over Cairnline setup counts", response.Data.Summary)
	}
}

func TestProjectSetupReadiness_StrictEmbeddedReadModelReadsWithoutHecateProject(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)
	const projectID = "proj_embedded_setup"

	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		if _, err := service.CreateExecutionProfile(t.Context(), cairnline.ExecutionProfile{
			ID:           "exec_embedded_setup",
			Name:         "Embedded setup runtime",
			ProviderHint: "openai",
			ModelHint:    "gpt-5",
		}); err != nil {
			return err
		}
		if _, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:                        projectID,
			Name:                      "Embedded Setup",
			Description:               "Coordinate setup from embedded Cairnline.",
			DefaultRootID:             "root_embedded_setup",
			DefaultExecutionProfileID: "exec_embedded_setup",
			Roots: []cairnline.Root{{
				ID:     "root_embedded_setup",
				Path:   "/workspace/embedded-setup",
				Kind:   "git",
				Active: true,
			}},
			ContextSources: []cairnline.Source{{
				ID:         "ctx_embedded_setup",
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
			ID:        "role_embedded_setup",
			ProjectID: projectID,
			Name:      "Coordinator",
		}); err != nil {
			return err
		}
		if _, err := service.CreateProjectSkill(t.Context(), cairnline.ProjectSkill{
			ID:          "setup",
			ProjectID:   projectID,
			Title:       "Setup",
			Description: "Coordinate project setup.",
			Path:        ".agents/skills/setup/SKILL.md",
			RootID:      "root_embedded_setup",
			Format:      cairnline.SkillFormatMarkdown,
			Enabled:     true,
			Status:      cairnline.SkillStatusAvailable,
			TrustLabel:  cairnline.SkillTrustWorkspace,
			SourceRefs:  []string{"ctx_embedded_setup"},
		}); err != nil {
			return err
		}
		if _, err := service.CreateMemoryEntry(t.Context(), cairnline.MemoryEntry{
			ID:         "mem_embedded_setup",
			ProjectID:  projectID,
			Title:      "Setup note",
			Body:       "Use explicit setup guidance.",
			Enabled:    true,
			TrustLabel: memory.TrustLabelOperatorMemory,
			SourceKind: memory.SourceKindOperator,
		}); err != nil {
			return err
		}
		_, err := service.CreateMemoryCandidate(t.Context(), cairnline.MemoryCandidate{
			ID:                  "memcand_embedded_setup",
			ProjectID:           projectID,
			Title:               "Setup candidate",
			Body:                "Review this setup candidate.",
			Status:              cairnline.MemoryCandidatePending,
			SuggestedKind:       "note",
			SuggestedTrustLabel: memory.TrustLabelGenerated,
			SuggestedSourceKind: memory.SourceKindGenerated,
		})
		return err
	}); err != nil {
		t.Fatalf("seed embedded Cairnline setup readiness: %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store seeded ok=%v err=%v, want no project row", ok, err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+projectID+"/setup-readiness", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("setup readiness status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectSetupReadinessEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode setup readiness: %v", err)
	}
	if response.Object != "project_setup_readiness" || response.Data.ProjectID != projectID || response.Data.ReadBackend != "cairnline" {
		t.Fatalf("setup readiness envelope = %+v, want embedded Cairnline setup readiness", response)
	}
	if response.Data.ShowOnboarding || !response.Data.SetupStarted || !response.Data.FirstWorkReady {
		t.Fatalf("setup readiness flags = %+v, want setup started and first work ready", response.Data)
	}
	summary := response.Data.Summary
	if summary.WorkItemCount != 0 || summary.RoleCount != 1 || summary.SkillCount != 1 || summary.EnabledContextSourceCount != 1 || summary.SavedMemoryCount != 1 || summary.PendingMemoryCandidateCount != 1 {
		t.Fatalf("setup readiness summary = %+v, want embedded setup counts", summary)
	}
	if summary.MissingDefaults || !summary.HasPurpose || !summary.HasActiveRoot {
		t.Fatalf("setup readiness summary = %+v, want embedded purpose/root/defaults", summary)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_missing/setup-readiness", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing project setup readiness status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectSetupReadiness_ReadsUseCairnlineSidecarWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineSidecarReadTestServer(t, "full")
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar setup readiness enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("sidecar read-route predicate = false, want true")
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/setup-readiness", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("setup readiness status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectSetupReadinessEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode setup readiness: %v", err)
	}
	if response.Data.ReadBackend != "cairnline" || response.Data.ProjectID != "proj_fixture" {
		t.Fatalf("setup readiness = %+v, want Cairnline fixture project", response.Data)
	}
	if response.Data.ShowOnboarding || !response.Data.SetupStarted || response.Data.FirstWorkReady {
		t.Fatalf("setup readiness flags = %+v, want sidecar setup started with existing work", response.Data)
	}
	summary := response.Data.Summary
	if summary.WorkItemCount != 1 || summary.RoleCount != 1 || summary.SkillCount != 1 || summary.EnabledContextSourceCount != 1 {
		t.Fatalf("setup readiness summary = %+v, want sidecar work/role/skill/source counts", summary)
	}
	if summary.SavedMemoryCount != 0 || summary.PendingMemoryCandidateCount != 0 {
		t.Fatalf("setup readiness summary = %+v, want no fixture memory", summary)
	}
	if !summary.HasPurpose || !summary.HasActiveRoot || !summary.MissingDefaults {
		t.Fatalf("setup readiness summary = %+v, want portable project with Hecate execution defaults missing", summary)
	}
	for _, id := range []string{"purpose", "workspace_source", "sources_memory", "roles", "first_work_item"} {
		check := findProjectSetupReadinessCheckForTest(t, response.Data.Checks, id)
		if check.Status != projectSetupReadinessStatusReady || check.Action != nil {
			t.Fatalf("check %s = %+v, want ready without action", id, check)
		}
	}
	defaults := findProjectSetupReadinessCheckForTest(t, response.Data.Checks, "launch_defaults")
	if defaults.Status != projectSetupReadinessStatusTodo || defaults.Action == nil || defaults.Action.Type != projectSetupReadinessActionProjectSettings {
		t.Fatalf("launch defaults check = %+v, want settings todo", defaults)
	}
}

func TestProjectSetupReadiness_CairnlineSidecarReadRequiresStructuredContent(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "text-only")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/setup-readiness", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("setup readiness status = %d body=%s, want 502", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "structuredContent") {
		t.Fatalf("error body = %s, want structuredContent diagnostic", rec.Body.String())
	}
}

func TestProjectSetupReadiness_CairnlineMatchesHecate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	hecateHandler, hecateServer := newProjectWorkTestServer()
	seedProjectSetupReadinessReadyTest(t, hecateHandler, root)
	cairnlineHandler, cairnlineServer := newProjectWorkCairnlineReadTestServer()
	seedProjectSetupReadinessReadyTest(t, cairnlineHandler, root)

	hecate := mustRequestJSON[ProjectSetupReadinessEnvelope](newAPITestClient(t, hecateServer), http.MethodGet, "/hecate/v1/projects/proj_setup_parity/setup-readiness", "")
	cairnline := mustRequestJSON[ProjectSetupReadinessEnvelope](newAPITestClient(t, cairnlineServer), http.MethodGet, "/hecate/v1/projects/proj_setup_parity/setup-readiness", "")
	if hecate.Data.ReadBackend != "hecate" {
		t.Fatalf("Hecate setup readiness read_backend = %q, want hecate", hecate.Data.ReadBackend)
	}
	if cairnline.Data.ReadBackend != "cairnline" {
		t.Fatalf("Cairnline setup readiness read_backend = %q, want cairnline", cairnline.Data.ReadBackend)
	}

	hecateData := normalizeProjectSetupReadinessForParity(hecate.Data)
	cairnlineData := normalizeProjectSetupReadinessForParity(cairnline.Data)
	if !reflect.DeepEqual(hecateData, cairnlineData) {
		t.Fatalf("setup readiness mismatch\nHecate:   %+v\nCairnline: %+v", hecateData, cairnlineData)
	}
}

func seedProjectSetupReadinessReadyTest(t *testing.T, handler *Handler, root string) {
	t.Helper()
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:              "proj_setup_parity",
		Name:            "Setup parity",
		Description:     "Coordinate setup parity.",
		DefaultProvider: "openai",
		DefaultModel:    "gpt-5",
		Roots:           []projects.Root{{ID: "root_setup_parity", Path: root, Kind: "git", Active: true}},
		ContextSources: []projects.ContextSource{{
			ID:      "ctx_setup_parity",
			Kind:    "workspace_instruction",
			Title:   "AGENTS.md",
			Path:    "AGENTS.md",
			Enabled: true,
		}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateRole(t.Context(), projectwork.AgentRoleProfile{
		ID:        "role_setup_parity",
		ProjectID: "proj_setup_parity",
		Name:      "Coordinator",
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if _, err := handler.projectSkills.UpsertDiscovered(t.Context(), "proj_setup_parity", []projectskills.Skill{{
		ID:        "skill_setup_parity",
		ProjectID: "proj_setup_parity",
		Path:      "skills/setup/SKILL.md",
		Enabled:   true,
		Status:    projectskills.StatusAvailable,
	}}); err != nil {
		t.Fatalf("UpsertDiscovered: %v", err)
	}
	if _, err := handler.memory.Create(t.Context(), memory.Entry{
		ID:        "mem_setup_parity",
		ProjectID: "proj_setup_parity",
		Title:     "Setup posture",
		Body:      "Keep setup explicit.",
		Enabled:   true,
	}); err != nil {
		t.Fatalf("Create memory: %v", err)
	}
	if _, err := handler.memoryCandidates.CreateCandidate(t.Context(), memory.Candidate{
		ID:        "memcand_setup_parity",
		ProjectID: "proj_setup_parity",
		Title:     "Candidate",
		Body:      "Review me.",
		Status:    memory.CandidateStatusPending,
	}); err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}
}

func normalizeProjectSetupReadinessForParity(item ProjectSetupReadinessResponse) ProjectSetupReadinessResponse {
	item.GeneratedAt = ""
	item.ReadBackend = ""
	return item
}

func findProjectSetupReadinessCheckForTest(t *testing.T, checks []ProjectSetupReadinessCheckResponse, id string) ProjectSetupReadinessCheckResponse {
	t.Helper()
	for _, check := range checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("missing setup readiness check %q in %+v", id, checks)
	return ProjectSetupReadinessCheckResponse{}
}
