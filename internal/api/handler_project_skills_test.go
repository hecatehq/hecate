package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/projects"
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

func projectSkillResponseHas(items []ProjectSkillResponseItem, id string, enabled bool, trustLabel string) bool {
	for _, item := range items {
		if item.ID == id && item.Enabled == enabled && item.TrustLabel == trustLabel {
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

func projectSkillAPITestJSONBody(t *testing.T, payload any) *bytes.Reader {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return bytes.NewReader(raw)
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
