package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
)

func TestProjectSkillsAPI_DiscoverListAndPatch(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	server := NewServer(quietLogger(), handler)
	root := t.TempDir()
	writeProjectSkillAPITestFile(t, root, ".hecate/skills/backend/SKILL.md", `---
name: Backend
description: Build backend changes.
hecate:
  suggested_tools:
    - git.diff
  required_permissions:
    tools: true
    writes: true
    network: false
---
`)
	writeProjectSkillAPITestFile(t, root, ".agents/skills/qa/SKILL.md", "# QA\n")
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_skills_api",
		Name: "Skills API",
		Roots: []projects.Root{{
			ID:     "root_skills_api",
			Path:   root,
			Active: true,
		}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}

	discoverRec := httptest.NewRecorder()
	server.ServeHTTP(discoverRec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_skills_api/skills/discover", nil))
	if discoverRec.Code != http.StatusOK {
		t.Fatalf("discover status = %d body=%s, want 200", discoverRec.Code, discoverRec.Body.String())
	}
	var discovered ProjectSkillsResponse
	if err := json.Unmarshal(discoverRec.Body.Bytes(), &discovered); err != nil {
		t.Fatalf("decode discover response: %v", err)
	}
	if discovered.Object != "project_skills" || len(discovered.Data) != 2 {
		t.Fatalf("discover response = %+v, want two project skills", discovered)
	}
	backend := projectSkillResponseFor(discovered.Data, "backend")
	if backend == nil || len(backend.SuggestedTools) != 1 || backend.SuggestedTools[0] != "git.diff" {
		t.Fatalf("backend suggested tools = %+v, want git.diff", backend)
	}
	if backend.RequiredPermissions == nil || backend.RequiredPermissions.Tools == nil || !*backend.RequiredPermissions.Tools || backend.RequiredPermissions.Writes == nil || !*backend.RequiredPermissions.Writes || backend.RequiredPermissions.Network == nil || *backend.RequiredPermissions.Network {
		t.Fatalf("backend required permissions = %+v, want tools/writes on and network off", backend.RequiredPermissions)
	}

	patchRec := httptest.NewRecorder()
	server.ServeHTTP(patchRec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/proj_skills_api/skills/backend", projectSkillAPITestJSONBody(t, map[string]any{
		"enabled":     false,
		"title":       "Backend Lead",
		"description": "Operator curated backend skill.",
		"trust_label": "operator_curated_skill",
	})))
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s, want 200", patchRec.Code, patchRec.Body.String())
	}
	var patched ProjectSkillResponse
	if err := json.Unmarshal(patchRec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if patched.Data.Enabled || patched.Data.Title != "Backend Lead" || patched.Data.TrustLabel != "operator_curated_skill" {
		t.Fatalf("patched skill = %+v, want operator metadata", patched.Data)
	}

	listRec := httptest.NewRecorder()
	server.ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_skills_api/skills", nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", listRec.Code, listRec.Body.String())
	}
	var listed ProjectSkillsResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if !projectSkillResponseHas(listed.Data, "backend", false, "operator_curated_skill") {
		t.Fatalf("listed skills = %+v, want patched backend", listed.Data)
	}

	missingRec := httptest.NewRecorder()
	server.ServeHTTP(missingRec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/proj_skills_api/skills/missing", projectSkillAPITestJSONBody(t, map[string]any{"enabled": true})))
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("patch missing status = %d body=%s, want 404", missingRec.Code, missingRec.Body.String())
	}
}

func TestProjectSkillsAPI_DiscoverReportsConflicts(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	server := NewServer(quietLogger(), handler)
	root := t.TempDir()
	writeProjectSkillAPITestFile(t, root, ".hecate/skills/review/SKILL.md", "# Review\n")
	writeProjectSkillAPITestFile(t, root, ".agents/skills/review/SKILL.md", "# Review Again\n")
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_skill_conflict_api",
		Name: "Skill Conflict API",
		Roots: []projects.Root{{
			ID:     "root_conflict",
			Path:   root,
			Active: true,
		}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_skill_conflict_api/skills/discover", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("discover status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectSkillsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Data) != 1 || response.Data[0].ID != "review" || response.Data[0].Status != "conflict" || len(response.Data[0].Warnings) == 0 {
		t.Fatalf("skills = %+v, want conflict record", response.Data)
	}
}

func TestProjectSkillsAPI_ListUsesCairnlineReadModelWhenConfigured(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	server := NewServer(quietLogger(), handler)
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_skills_cairnline",
		Name: "Skills Cairnline",
		Roots: []projects.Root{{
			ID:     "root_skills_cairnline",
			Path:   t.TempDir(),
			Active: true,
		}},
		ContextSources: []projects.ContextSource{{
			ID:      "ctx_agents",
			Kind:    "workspace_instruction",
			Title:   "AGENTS.md",
			Path:    "AGENTS.md",
			Enabled: true,
		}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	tools := true
	writes := false
	if _, err := handler.projectSkills.UpsertDiscovered(t.Context(), "proj_skills_cairnline", []projectskills.Skill{{
		ID:          "backend",
		ProjectID:   "proj_skills_cairnline",
		Title:       "Backend",
		Description: "Build backend changes.",
		Path:        ".agents/skills/backend/SKILL.md",
		RootID:      "root_skills_cairnline",
		Format:      projectskills.FormatSkillMD,
		Enabled:     true,
		Status:      projectskills.StatusAvailable,
		TrustLabel:  projectskills.TrustWorkspaceSkill,
		SuggestedTools: []string{
			"git.diff",
		},
		RequiredPermissions: projectskills.RequiredPermissions{
			Tools:  &tools,
			Writes: &writes,
		},
		SourceContextSourceIDs: []string{"ctx_agents"},
		Warnings:               []string{"metadata-only skill"},
	}}); err != nil {
		t.Fatalf("Upsert skills: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_skills_cairnline/skills", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var listed ProjectSkillsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	backend := projectSkillResponseFor(listed.Data, "backend")
	if backend == nil {
		t.Fatalf("listed skills = %+v, want backend skill", listed.Data)
	}
	if backend.ReadBackend != "cairnline" || backend.Path != ".agents/skills/backend/SKILL.md" || backend.RootID != "root_skills_cairnline" || backend.TrustLabel != projectskills.TrustWorkspaceSkill {
		t.Fatalf("backend skill = %+v, want Cairnline-projected portable metadata", *backend)
	}
	if len(backend.SourceContextSourceIDs) != 1 || backend.SourceContextSourceIDs[0] != "ctx_agents" || len(backend.Warnings) != 1 || backend.Warnings[0] != "metadata-only skill" {
		t.Fatalf("backend provenance = sources %+v warnings %+v, want Cairnline-projected source refs and warnings", backend.SourceContextSourceIDs, backend.Warnings)
	}
	if len(backend.SuggestedTools) != 1 || backend.SuggestedTools[0] != "git.diff" {
		t.Fatalf("backend suggested tools = %+v, want Hecate snapshot enrichment", backend.SuggestedTools)
	}
	if backend.RequiredPermissions == nil || backend.RequiredPermissions.Tools == nil || !*backend.RequiredPermissions.Tools || backend.RequiredPermissions.Writes == nil || *backend.RequiredPermissions.Writes {
		t.Fatalf("backend required permissions = %+v, want Hecate snapshot enrichment", backend.RequiredPermissions)
	}

	nativeSkills, err := handler.projectSkills.List(t.Context(), "proj_skills_cairnline")
	if err != nil {
		t.Fatalf("List native skills: %v", err)
	}
	assertProjectSkillsProjectionParity(t, renderProjectSkills(nativeSkills, "hecate"), listed.Data)
}

func TestProjectSkillsAPI_StrictEmbeddedReadModelReadsSkillsWithoutHecateStores(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)
	const projectID = "proj_embedded_skills"
	tools := true
	network := false

	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		if _, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:   projectID,
			Name: "Embedded Skills",
			Roots: []cairnline.Root{{
				ID:     "root_embedded_skills",
				Path:   "/workspace/embedded-skills",
				Kind:   "git",
				Active: true,
			}},
			ContextSources: []cairnline.Source{{
				ID:         "ctx_agents",
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
		_, err := service.CreateProjectSkill(t.Context(), cairnline.ProjectSkill{
			ID:          "backend",
			ProjectID:   projectID,
			Title:       "Backend",
			Description: "Build backend changes.",
			Path:        ".agents/skills/backend/SKILL.md",
			RootID:      "root_embedded_skills",
			Format:      cairnline.SkillFormatMarkdown,
			SuggestedTools: []string{
				"git.diff",
			},
			RequiredPermissions: cairnline.RequiredPermissions{
				Tools:   &tools,
				Network: &network,
			},
			Enabled:    true,
			Status:     cairnline.SkillStatusAvailable,
			TrustLabel: cairnline.SkillTrustWorkspace,
			SourceRefs: []string{"ctx_agents"},
			Warnings:   []string{"metadata-only skill"},
		})
		return err
	}); err != nil {
		t.Fatalf("seed embedded Cairnline skills: %v", err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store seeded ok=%v err=%v, want no project row", ok, err)
	}
	if nativeSkills, err := handler.projectSkills.List(t.Context(), projectID); err != nil || len(nativeSkills) != 0 {
		t.Fatalf("Hecate skill store rows = %+v err=%v, want none", nativeSkills, err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+projectID+"/skills", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var listed ProjectSkillsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	backend := projectSkillResponseFor(listed.Data, "backend")
	if backend == nil {
		t.Fatalf("listed skills = %+v, want backend skill", listed.Data)
	}
	if backend.ReadBackend != "cairnline" || backend.ProjectID != projectID || backend.Path != ".agents/skills/backend/SKILL.md" || backend.RootID != "root_embedded_skills" {
		t.Fatalf("backend skill = %+v, want embedded Cairnline skill projection", *backend)
	}
	if len(backend.SourceContextSourceIDs) != 1 || backend.SourceContextSourceIDs[0] != "ctx_agents" || len(backend.Warnings) != 1 || backend.Warnings[0] != "metadata-only skill" {
		t.Fatalf("backend provenance = sources %+v warnings %+v, want embedded Cairnline provenance", backend.SourceContextSourceIDs, backend.Warnings)
	}
	if len(backend.SuggestedTools) != 1 || backend.SuggestedTools[0] != "git.diff" {
		t.Fatalf("backend suggested tools = %+v, want embedded Cairnline suggested tools", backend.SuggestedTools)
	}
	if backend.RequiredPermissions == nil || backend.RequiredPermissions.Tools == nil || !*backend.RequiredPermissions.Tools || backend.RequiredPermissions.Network == nil || *backend.RequiredPermissions.Network {
		t.Fatalf("backend required permissions = %+v, want embedded Cairnline permissions", backend.RequiredPermissions)
	}

	missingRec := httptest.NewRecorder()
	server.ServeHTTP(missingRec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_missing/skills", nil))
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d body=%s, want 404", missingRec.Code, missingRec.Body.String())
	}
}

func TestProjectSkillsAPI_ListUsesCairnlineSidecarWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineSidecarReadTestServer(t, "full")
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar skills list enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("sidecar read-route predicate = false, want true")
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/skills", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("skills status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectSkillsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode skills response: %v", err)
	}
	if response.Object != "project_skills" {
		t.Fatalf("skills object = %q, want project_skills", response.Object)
	}
	skill := projectSkillResponseFor(response.Data, "skill_fixture")
	if skill == nil {
		t.Fatalf("skills = %+v, want fixture skill", response.Data)
	}
	if skill.ReadBackend != "cairnline" || skill.ProjectID != "proj_fixture" || skill.Title != "Fixture Skill" {
		t.Fatalf("skill = %+v, want sidecar Cairnline fixture skill", *skill)
	}
	if skill.Path != ".agents/skills/fixture/SKILL.md" || !skill.Enabled || skill.Status != projectskills.StatusAvailable || skill.TrustLabel != projectskills.TrustWorkspaceSkill {
		t.Fatalf("skill metadata = %+v, want portable sidecar skill metadata", *skill)
	}
}

func TestProjectSkillsAPI_CairnlineSidecarReadRequiresStructuredContent(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "text-only")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/skills", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("skills status = %d body=%s, want 502", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "structuredContent") {
		t.Fatalf("error body = %s, want structuredContent diagnostic", rec.Body.String())
	}
}

func TestProjectSkillsAPI_MirrorsMutationsToCairnlineWhenConfigured(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server:   config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	server := NewServer(quietLogger(), handler)
	root := t.TempDir()
	writeProjectSkillAPITestFile(t, root, ".agents/skills/backend/SKILL.md", `---
name: Backend
description: Build backend changes.
---
`)
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_skills_mirror",
		Name: "Skills Mirror",
		Roots: []projects.Root{{
			ID:     "root_skills_mirror",
			Path:   root,
			Active: true,
		}},
		ContextSources: []projects.ContextSource{{
			ID:      "ctx_agents",
			Kind:    "workspace_instruction",
			Title:   "AGENTS.md",
			Path:    "AGENTS.md",
			Enabled: true,
		}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}

	discoverRec := httptest.NewRecorder()
	server.ServeHTTP(discoverRec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_skills_mirror/skills/discover", nil))
	if discoverRec.Code != http.StatusOK {
		t.Fatalf("discover status = %d body=%s, want 200", discoverRec.Code, discoverRec.Body.String())
	}
	mirrored := getMirroredCairnlineProjectSkillForTest(t, handler, "proj_skills_mirror", "backend")
	if mirrored.Title != "Backend" || mirrored.Path != ".agents/skills/backend/SKILL.md" || !mirrored.Enabled || mirrored.Status != projectskills.StatusAvailable {
		t.Fatalf("mirrored discovered skill = %+v, want available Backend skill", mirrored)
	}
	if mirrored.TrustLabel != projectskills.TrustWorkspaceSkill {
		t.Fatalf("mirrored trust label = %q, want workspace skill", mirrored.TrustLabel)
	}

	patchRec := httptest.NewRecorder()
	server.ServeHTTP(patchRec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/proj_skills_mirror/skills/backend", projectSkillAPITestJSONBody(t, map[string]any{
		"enabled":     false,
		"title":       "Backend Owner",
		"description": "Operator-reviewed backend guidance.",
		"trust_label": "operator_curated_skill",
	})))
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s, want 200", patchRec.Code, patchRec.Body.String())
	}
	mirrored = getMirroredCairnlineProjectSkillForTest(t, handler, "proj_skills_mirror", "backend")
	if mirrored.Enabled || mirrored.Title != "Backend Owner" || mirrored.Description != "Operator-reviewed backend guidance." || mirrored.TrustLabel != "operator_curated_skill" {
		t.Fatalf("mirrored patched skill = %+v, want operator-curated disabled skill", mirrored)
	}
}

func TestProjectSkillsAPI_CairnlineWriteAuthorityCommitsSkillsFirst(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:     "cairnline",
			CairnlineWriteAuthority: projectCairnlineWriteAuthorityProjectSkills,
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	server := NewServer(quietLogger(), handler)
	root := t.TempDir()
	writeProjectSkillAPITestFile(t, root, "docs-ai/skills/backend/SKILL.md", `---
name: Backend
description: Build backend changes.
hecate:
  suggested_tools:
    - git.diff
  required_permissions:
    tools: true
    writes: true
    network: false
---
`)
	writeProjectSkillAPITestFile(t, root, "AGENTS.md", "Use docs-ai/skills/backend/SKILL.md.\n")
	project, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_skills_authority",
		Name: "Skills Authority",
		Roots: []projects.Root{{
			ID:     "root_skills_authority",
			Path:   root,
			Active: true,
		}},
		ContextSources: []projects.ContextSource{{
			ID:      "ctx_agents",
			Kind:    "workspace_instruction",
			Title:   "AGENTS.md",
			Path:    "AGENTS.md",
			Enabled: true,
			Format:  "agents_md",
			Metadata: map[string]string{
				"root_id": "root_skills_authority",
			},
		}},
	})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}

	discoverRec := httptest.NewRecorder()
	server.ServeHTTP(discoverRec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_skills_authority/skills/discover", nil))
	if discoverRec.Code != http.StatusOK {
		t.Fatalf("discover status = %d body=%s, want 200", discoverRec.Code, discoverRec.Body.String())
	}
	var discovered ProjectSkillsResponse
	if err := json.Unmarshal(discoverRec.Body.Bytes(), &discovered); err != nil {
		t.Fatalf("decode discover response: %v", err)
	}
	backend := projectSkillResponseFor(discovered.Data, "backend")
	if backend == nil {
		t.Fatalf("discovered skills = %+v, want backend", discovered.Data)
	}
	if backend.ReadBackend != "cairnline" || backend.Path != "docs-ai/skills/backend/SKILL.md" || backend.RootID != "root_skills_authority" {
		t.Fatalf("backend response = %+v, want Cairnline-authoritative projection", *backend)
	}
	if len(backend.SuggestedTools) != 1 || backend.SuggestedTools[0] != "git.diff" {
		t.Fatalf("backend suggested tools = %+v, want git.diff", backend.SuggestedTools)
	}
	if backend.RequiredPermissions == nil || backend.RequiredPermissions.Writes == nil || !*backend.RequiredPermissions.Writes || backend.RequiredPermissions.Network == nil || *backend.RequiredPermissions.Network {
		t.Fatalf("backend required permissions = %+v, want writes true and network false", backend.RequiredPermissions)
	}
	if len(backend.SourceContextSourceIDs) != 1 || backend.SourceContextSourceIDs[0] != "ctx_agents" {
		t.Fatalf("backend source refs = %+v, want ctx_agents", backend.SourceContextSourceIDs)
	}
	mirrored := getMirroredCairnlineProjectSkillForTest(t, handler, project.ID, "backend")
	if mirrored.Title != "Backend" || len(mirrored.SuggestedTools) != 1 || mirrored.SuggestedTools[0] != "git.diff" || mirrored.RequiredPermissions.Network == nil || *mirrored.RequiredPermissions.Network {
		t.Fatalf("Cairnline skill = %+v, want discovered metadata committed first", mirrored)
	}
	native, err := handler.projectSkills.List(t.Context(), project.ID)
	if err != nil {
		t.Fatalf("List native skills: %v", err)
	}
	if shadow := projectSkillResponseFor(renderProjectSkills(native, "hecate"), "backend"); shadow == nil || shadow.Title != "Backend" {
		t.Fatalf("native shadow skills = %+v, want backend compatibility shadow", native)
	}

	patchRec := httptest.NewRecorder()
	server.ServeHTTP(patchRec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/proj_skills_authority/skills/backend", projectSkillAPITestJSONBody(t, map[string]any{
		"enabled":     false,
		"title":       "Backend Owner",
		"description": "Operator-reviewed backend guidance.",
		"trust_label": "operator_curated_skill",
	})))
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s, want 200", patchRec.Code, patchRec.Body.String())
	}
	var patched ProjectSkillResponse
	if err := json.Unmarshal(patchRec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if patched.Data.ReadBackend != "cairnline" || patched.Data.Enabled || patched.Data.Title != "Backend Owner" || patched.Data.TrustLabel != "operator_curated_skill" {
		t.Fatalf("patched response = %+v, want disabled Cairnline-authoritative skill", patched.Data)
	}
	mirrored = getMirroredCairnlineProjectSkillForTest(t, handler, project.ID, "backend")
	if mirrored.Enabled || mirrored.Title != "Backend Owner" || mirrored.Description != "Operator-reviewed backend guidance." || mirrored.TrustLabel != "operator_curated_skill" {
		t.Fatalf("patched Cairnline skill = %+v, want operator override", mirrored)
	}
	native, err = handler.projectSkills.List(t.Context(), project.ID)
	if err != nil {
		t.Fatalf("List native patched skills: %v", err)
	}
	if shadow := projectSkillResponseFor(renderProjectSkills(native, "hecate"), "backend"); shadow == nil || shadow.Enabled || shadow.Title != "Backend Owner" || shadow.TrustLabel != "operator_curated_skill" {
		t.Fatalf("native shadow skills = %+v, want patched compatibility shadow", native)
	}

	rediscoverRec := httptest.NewRecorder()
	server.ServeHTTP(rediscoverRec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_skills_authority/skills/discover", nil))
	if rediscoverRec.Code != http.StatusOK {
		t.Fatalf("rediscover status = %d body=%s, want 200", rediscoverRec.Code, rediscoverRec.Body.String())
	}
	mirrored = getMirroredCairnlineProjectSkillForTest(t, handler, project.ID, "backend")
	if mirrored.Enabled || mirrored.Title != "Backend Owner" || mirrored.Description != "Operator-reviewed backend guidance." || mirrored.TrustLabel != "operator_curated_skill" {
		t.Fatalf("rediscovered Cairnline skill = %+v, want operator overrides preserved", mirrored)
	}
}

func TestProjectSkillsAPI_CairnlineWriteAuthorityAcceptsCairnlineOnlyProject(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:     "cairnline",
			CairnlineWriteAuthority: projectCairnlineWriteAuthorityProjectSkills,
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectSkillStore(projectskills.NewMemoryStore())
	server := NewServer(quietLogger(), handler)
	root := t.TempDir()
	writeProjectSkillAPITestFile(t, root, ".agents/skills/backend/SKILL.md", `---
name: Backend
description: Build backend changes.
---
`)
	const projectID = "proj_skills_cairnline_only"
	seedCairnlineOnlyProjectWorkGraphForTest(t, handler, cairnline.Project{
		ID:   projectID,
		Name: "Cairnline-only skills",
		Roots: []cairnline.Root{{
			ID:     "root_skills_cairnline_only",
			Path:   root,
			Active: true,
		}},
		ContextSources: []cairnline.Source{{
			ID:      "ctx_agents",
			Kind:    "workspace_instruction",
			Title:   "AGENTS.md",
			Locator: "AGENTS.md",
			Enabled: true,
			Format:  "agents_md",
			Metadata: map[string]string{
				"root_id": "root_skills_cairnline_only",
			},
		}},
	}, nil, nil, nil)

	discoverRec := httptest.NewRecorder()
	server.ServeHTTP(discoverRec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+projectID+"/skills/discover", nil))
	if discoverRec.Code != http.StatusOK {
		t.Fatalf("discover status = %d body=%s, want 200", discoverRec.Code, discoverRec.Body.String())
	}
	var discovered ProjectSkillsResponse
	if err := json.Unmarshal(discoverRec.Body.Bytes(), &discovered); err != nil {
		t.Fatalf("decode discover response: %v", err)
	}
	backend := projectSkillResponseFor(discovered.Data, "backend")
	if backend == nil || backend.ReadBackend != "cairnline" || backend.ProjectID != projectID || backend.RootID != "root_skills_cairnline_only" || backend.Path != ".agents/skills/backend/SKILL.md" {
		t.Fatalf("discovered backend = %+v, want Cairnline-only project skill", backend)
	}
	mirrored := getMirroredCairnlineProjectSkillForTest(t, handler, projectID, "backend")
	if mirrored.Title != "Backend" || mirrored.ProjectID != projectID || mirrored.RootID != "root_skills_cairnline_only" {
		t.Fatalf("Cairnline skill = %+v, want authoritative skill on Cairnline-only project", mirrored)
	}
	native, err := handler.projectSkills.List(t.Context(), projectID)
	if err != nil {
		t.Fatalf("List native skills: %v", err)
	}
	if shadow := projectSkillResponseFor(renderProjectSkills(native, "hecate"), "backend"); shadow == nil || shadow.ProjectID != projectID {
		t.Fatalf("native shadow skills = %+v, want backend compatibility shadow", native)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("native project exists = %t err=%v, want missing after skill discovery", ok, err)
	}

	patchRec := httptest.NewRecorder()
	server.ServeHTTP(patchRec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+projectID+"/skills/backend", projectSkillAPITestJSONBody(t, map[string]any{
		"enabled":     false,
		"title":       "Backend Owner",
		"description": "Operator-reviewed backend guidance.",
		"trust_label": "operator_curated_skill",
	})))
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s, want 200", patchRec.Code, patchRec.Body.String())
	}
	var patched ProjectSkillResponse
	if err := json.Unmarshal(patchRec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if patched.Data.ReadBackend != "cairnline" || patched.Data.Enabled || patched.Data.Title != "Backend Owner" || patched.Data.TrustLabel != "operator_curated_skill" {
		t.Fatalf("patched skill = %+v, want patched Cairnline-only skill", patched.Data)
	}
	mirrored = getMirroredCairnlineProjectSkillForTest(t, handler, projectID, "backend")
	if mirrored.Enabled || mirrored.Title != "Backend Owner" || mirrored.TrustLabel != "operator_curated_skill" {
		t.Fatalf("patched Cairnline skill = %+v, want operator override", mirrored)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("native project exists = %t err=%v, want still missing after skill update", ok, err)
	}
}

func TestProjectSkillsAPI_CairnlineReplacementModeSkipsNativeSkillShadows(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineReplacementIdentityAuthorityTestServer(t)
	client := newAPITestClient(t, server)
	root := t.TempDir()
	writeProjectSkillAPITestFile(t, root, ".agents/skills/backend/SKILL.md", `---
name: Backend
description: Build backend changes.
---
`)
	created := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name": "Replacement skills",
		"roots": []map[string]any{{
			"id":     "root_replacement_skills",
			"path":   root,
			"kind":   "git",
			"active": true,
		}},
	}))
	projectID := created.Data.ID
	if projectID == "" || created.Data.ReadBackend != "cairnline" {
		t.Fatalf("created project = %+v, want Cairnline replacement identity", created.Data)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("native project exists = %t err=%v, want missing after replacement create", ok, err)
	}

	discovered := mustRequestJSONStatus[ProjectSkillsResponse](client, http.StatusOK, http.MethodPost, "/hecate/v1/projects/"+projectID+"/skills/discover", "")
	backend := projectSkillResponseFor(discovered.Data, "backend")
	if backend == nil || backend.ReadBackend != "cairnline" || backend.ProjectID != projectID || backend.RootID != "root_replacement_skills" {
		t.Fatalf("discovered backend = %+v, want Cairnline-backed replacement skill", backend)
	}
	mirrored := getMirroredCairnlineProjectSkillForTest(t, handler, projectID, "backend")
	if mirrored.Title != "Backend" || mirrored.ProjectID != projectID {
		t.Fatalf("Cairnline skill = %+v, want authoritative replacement skill", mirrored)
	}
	assertNoNativeProjectSkillForTest(t, handler, projectID, "backend")

	patched := mustRequestJSONStatus[ProjectSkillResponse](client, http.StatusOK, http.MethodPatch, "/hecate/v1/projects/"+projectID+"/skills/backend", projectJourneyJSON(t, map[string]any{
		"enabled":     false,
		"title":       "Backend Owner",
		"trust_label": "operator_curated_skill",
	}))
	if patched.Data.ReadBackend != "cairnline" || patched.Data.Enabled || patched.Data.Title != "Backend Owner" || patched.Data.TrustLabel != "operator_curated_skill" {
		t.Fatalf("patched skill = %+v, want patched Cairnline replacement skill", patched.Data)
	}
	mirrored = getMirroredCairnlineProjectSkillForTest(t, handler, projectID, "backend")
	if mirrored.Enabled || mirrored.Title != "Backend Owner" || mirrored.TrustLabel != "operator_curated_skill" {
		t.Fatalf("patched Cairnline skill = %+v, want operator override", mirrored)
	}
	assertNoNativeProjectSkillForTest(t, handler, projectID, "backend")

	listed := mustRequestJSONStatus[ProjectSkillsResponse](client, http.StatusOK, http.MethodGet, "/hecate/v1/projects/"+projectID+"/skills", "")
	listedBackend := projectSkillResponseFor(listed.Data, "backend")
	if listedBackend == nil || listedBackend.ReadBackend != "cairnline" || listedBackend.Enabled || listedBackend.Title != "Backend Owner" {
		t.Fatalf("listed skills = %+v, want Cairnline read model with patched backend skill", listed.Data)
	}
}

func TestProjectSkillsAPI_MirrorRefreshesSkillSourceRefsOnRediscovery(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server:   config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	server := NewServer(quietLogger(), handler)
	root := t.TempDir()
	writeProjectSkillAPITestFile(t, root, "docs-ai/skills/backend/SKILL.md", "# Backend\n")
	writeProjectSkillAPITestFile(t, root, "AGENTS.md", "Use docs-ai/skills/backend/SKILL.md.\n")
	writeProjectSkillAPITestFile(t, root, "CLAUDE.md", "Use docs-ai/skills/backend/SKILL.md.\n")
	project, err := handler.projects.Create(t.Context(), projects.Project{
		ID:   "proj_skills_refs",
		Name: "Skill Source Refs",
		Roots: []projects.Root{{
			ID:     "root_skills_refs",
			Path:   root,
			Active: true,
		}},
		ContextSources: []projects.ContextSource{{
			ID:      "ctx_agents",
			Kind:    "workspace_instruction",
			Title:   "AGENTS.md",
			Path:    "AGENTS.md",
			Enabled: true,
			Format:  "agents_md",
			Metadata: map[string]string{
				"root_id": "root_skills_refs",
			},
		}},
	})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}

	discoverRec := httptest.NewRecorder()
	server.ServeHTTP(discoverRec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_skills_refs/skills/discover", nil))
	if discoverRec.Code != http.StatusOK {
		t.Fatalf("discover status = %d body=%s, want 200", discoverRec.Code, discoverRec.Body.String())
	}
	mirrored := getMirroredCairnlineProjectSkillForTest(t, handler, project.ID, "backend")
	if !stringSliceContains(mirrored.SourceRefs, "ctx_agents") {
		t.Fatalf("initial mirrored source refs = %+v, want ctx_agents", mirrored.SourceRefs)
	}

	patchRec := httptest.NewRecorder()
	server.ServeHTTP(patchRec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/proj_skills_refs/skills/backend", projectSkillAPITestJSONBody(t, map[string]any{
		"enabled":     false,
		"title":       "Backend Owner",
		"description": "Operator-reviewed backend guidance.",
		"trust_label": "operator_curated_skill",
	})))
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s, want 200", patchRec.Code, patchRec.Body.String())
	}
	if _, err := handler.projects.Update(t.Context(), project.ID, func(project *projects.Project) {
		project.ContextSources = []projects.ContextSource{{
			ID:      "ctx_claude",
			Kind:    "workspace_instruction",
			Title:   "CLAUDE.md",
			Path:    "CLAUDE.md",
			Enabled: true,
			Format:  "claude_md",
			Metadata: map[string]string{
				"root_id": "root_skills_refs",
			},
		}}
	}); err != nil {
		t.Fatalf("Update project context sources: %v", err)
	}

	rediscoverRec := httptest.NewRecorder()
	server.ServeHTTP(rediscoverRec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_skills_refs/skills/discover", nil))
	if rediscoverRec.Code != http.StatusOK {
		t.Fatalf("rediscover status = %d body=%s, want 200", rediscoverRec.Code, rediscoverRec.Body.String())
	}
	mirrored = getMirroredCairnlineProjectSkillForTest(t, handler, project.ID, "backend")
	if mirrored.Enabled || mirrored.Title != "Backend Owner" || mirrored.Description != "Operator-reviewed backend guidance." || mirrored.TrustLabel != "operator_curated_skill" {
		t.Fatalf("rediscovered mirrored skill = %+v, want operator overrides preserved", mirrored)
	}
	if !stringSliceContains(mirrored.SourceRefs, "ctx_claude") || stringSliceContains(mirrored.SourceRefs, "ctx_agents") {
		t.Fatalf("rediscovered mirrored source refs = %+v, want ctx_claude only", mirrored.SourceRefs)
	}
}

func projectSkillResponseHas(items []ProjectSkillResponseItem, id string, enabled bool, trustLabel string) bool {
	for _, item := range items {
		if item.ID == id && item.Enabled == enabled && item.TrustLabel == trustLabel {
			return true
		}
	}
	return false
}

func stringSliceContains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func projectSkillResponseFor(items []ProjectSkillResponseItem, id string) *ProjectSkillResponseItem {
	for idx := range items {
		if items[idx].ID == id {
			return &items[idx]
		}
	}
	return nil
}

func assertNoNativeProjectSkillForTest(t *testing.T, handler *Handler, projectID, skillID string) {
	t.Helper()
	items, err := handler.projectSkills.List(t.Context(), projectID)
	if err != nil {
		t.Fatalf("List native skills: %v", err)
	}
	if skill := projectSkillResponseFor(renderProjectSkills(items, "hecate"), skillID); skill != nil {
		t.Fatalf("native project skill %q exists in replacement mode: %+v", skillID, *skill)
	}
}

func assertProjectSkillsProjectionParity(t *testing.T, hecate, cairnline []ProjectSkillResponseItem) {
	t.Helper()
	if len(hecate) != len(cairnline) {
		t.Fatalf("skill projection count = hecate:%d cairnline:%d", len(hecate), len(cairnline))
	}
	normalizedHecate := append([]ProjectSkillResponseItem(nil), hecate...)
	normalizedCairnline := append([]ProjectSkillResponseItem(nil), cairnline...)
	for idx := range normalizedHecate {
		if normalizedHecate[idx].ReadBackend != "hecate" {
			t.Fatalf("hecate skill[%d] read_backend = %q, want hecate", idx, normalizedHecate[idx].ReadBackend)
		}
		if normalizedCairnline[idx].ReadBackend != "cairnline" {
			t.Fatalf("cairnline skill[%d] read_backend = %q, want cairnline", idx, normalizedCairnline[idx].ReadBackend)
		}
		normalizedHecate[idx].ReadBackend = ""
		normalizedCairnline[idx].ReadBackend = ""
	}
	if !reflect.DeepEqual(normalizedHecate, normalizedCairnline) {
		t.Fatalf("skill projection mismatch\nhecate:   %+v\ncairnline: %+v", normalizedHecate, normalizedCairnline)
	}
}

func projectSkillAPITestJSONBody(t *testing.T, payload any) *bytes.Reader {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return bytes.NewReader(raw)
}

func getMirroredCairnlineProjectSkillForTest(t *testing.T, handler *Handler, projectID, skillID string) cairnline.ProjectSkill {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	skill, err := service.GetProjectSkill(t.Context(), projectID, skillID)
	if err != nil {
		t.Fatalf("GetProjectSkill(%q, %q): %v", projectID, skillID, err)
	}
	return skill
}

func writeProjectSkillAPITestFile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
