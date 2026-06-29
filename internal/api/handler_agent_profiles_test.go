package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/config"
)

func newAgentProfilesTestServer() http.Handler {
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	return NewServer(quietLogger(), handler)
}

func newAgentProfilesCairnlineMirrorTestServer(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	handler := NewHandler(config.Config{
		Server:   config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	return handler, NewServer(quietLogger(), handler)
}

func newAgentProfilesCairnlineAuthorityTestServer(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:     "cairnline",
			CairnlineWriteAuthority: projectCairnlineWriteAuthorityAgentProfiles,
		},
	}, quietLogger(), nil, nil, nil, nil)
	return handler, NewServer(quietLogger(), handler)
}

func TestAgentProfilesAPI_CRUD(t *testing.T) {
	t.Parallel()
	server := newAgentProfilesTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/agent-profiles", bytes.NewReader([]byte(`{
		"id":"prof_backend",
		"name":"Backend implementer",
		"description":"Go runtime work",
		"instructions":"Prefer narrow tests.",
		"surface":"hecate_task",
		"provider_hint":"anthropic",
		"model_hint":"claude-sonnet-4",
		"execution_profile":"implementation",
		"tools_enabled":true,
		"writes_allowed":true,
		"network_allowed":false,
		"approval_policy":"require",
		"project_memory_policy":"visible_only",
		"context_source_policy":"include_enabled",
		"skill_ids":["backend","providers"],
		"external_agent_kind":"codex",
		"external_agent_options":{"effort":"high"}
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created AgentProfileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Object != "agent_profile" || created.Data.ID != "prof_backend" || created.Data.ExecutionProfile != "implementation" {
		t.Fatalf("created = %+v, want profile envelope", created)
	}
	if created.Data.BuiltIn {
		t.Fatalf("created profile is marked built-in")
	}
	if !created.Data.ToolsEnabled || !created.Data.WritesAllowed || created.Data.NetworkAllowed {
		t.Fatalf("posture = tools=%v writes=%v network=%v, want true/true/false", created.Data.ToolsEnabled, created.Data.WritesAllowed, created.Data.NetworkAllowed)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/agent-profiles/prof_backend", bytes.NewReader([]byte(`{
		"name":"Backend reviewer",
		"writes_allowed":false,
		"approval_policy":"block"
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var updated AgentProfileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if updated.Data.Name != "Backend reviewer" || updated.Data.WritesAllowed || updated.Data.ApprovalPolicy != "block" {
		t.Fatalf("updated = %+v, want patched profile", updated.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/agent-profiles", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var listed AgentProfilesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if listed.Object != "agent_profiles" || !agentProfileResponseIDExists(listed.Data, "implementation") || !agentProfileResponseIDExists(listed.Data, "prof_backend") {
		t.Fatalf("listed = %+v, want built-ins plus created profile", listed)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/agent-profiles/prof_backend", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
}

func TestAgentProfilesAPI_MirrorsMutationsToCairnlineWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newAgentProfilesCairnlineMirrorTestServer(t)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/agent-profiles", bytes.NewReader([]byte(`{
		"id":"prof_cairnline",
		"name":"Cairnline profile",
		"description":"Mirrored posture",
		"instructions":"Use project context deliberately.",
		"project_memory_policy":"include",
		"context_source_policy":"include_enabled",
		"skill_ids":["implementation"],
		"execution_profile":"profile_execution",
		"provider_hint":"openai",
		"model_hint":"gpt-5",
		"tools_enabled":true,
		"writes_allowed":true,
		"network_allowed":false,
		"approval_policy":"require",
		"external_agent_kind":"hecate"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	profile := getMirroredCairnlineAgentProfileForTest(t, handler, "prof_cairnline")
	if profile.Name != "Cairnline profile" || profile.MemoryPolicy != "include" || profile.SourcePolicy != "include_enabled" || !stringSliceContains(profile.SkillIDs, "implementation") {
		t.Fatalf("mirrored profile after create = %+v, want profile metadata", profile)
	}
	if !cairnlineExecutionProfileIDExistsForTest(t, handler, "profile_execution") {
		t.Fatalf("mirrored execution profiles missing profile_execution")
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/agent-profiles/prof_cairnline", bytes.NewReader([]byte(`{
		"name":"Updated Cairnline profile",
		"project_memory_policy":"visible_only",
		"skill_ids":["review"]
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	profile = getMirroredCairnlineAgentProfileForTest(t, handler, "prof_cairnline")
	if profile.Name != "Updated Cairnline profile" || profile.MemoryPolicy != "visible_only" || !stringSliceContains(profile.SkillIDs, "review") || stringSliceContains(profile.SkillIDs, "implementation") {
		t.Fatalf("mirrored profile after patch = %+v, want patched profile metadata", profile)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/agent-profiles/prof_cairnline", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
	assertCairnlineAgentProfileMissingForTest(t, handler, "prof_cairnline")
	if !cairnlineExecutionProfileIDExistsForTest(t, handler, "profile_execution") {
		t.Fatalf("delete should leave shared execution profile posture in place")
	}
}

func TestAgentProfilesAPI_CairnlineWriteAuthorityCommitsProfilesFirst(t *testing.T) {
	t.Parallel()
	handler, server := newAgentProfilesCairnlineAuthorityTestServer(t)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/agent-profiles", bytes.NewReader([]byte(`{
		"id":"prof_bad",
		"name":"Bad",
		"surface":"terminal"
	}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create bad enum status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(handler.cairnlineEmbeddedDatabasePath()); !os.IsNotExist(err) {
		t.Fatalf("Cairnline database stat after invalid create error = %v, want database not created", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/agent-profiles", bytes.NewReader([]byte(`{
		"id":"prof_authority",
		"name":"Authority profile",
		"description":"Cairnline owns this profile.",
		"instructions":"Keep profile metadata portable.",
		"surface":"external_agent",
		"provider_hint":"anthropic",
		"model_hint":"claude-sonnet-4",
		"execution_profile":"prof_authority_exec",
		"tools_enabled":true,
		"writes_allowed":true,
		"network_allowed":false,
		"approval_policy":"require",
		"project_memory_policy":"include",
		"context_source_policy":"include_enabled",
		"skill_ids":["review","review","release"],
		"external_agent_kind":"codex",
		"external_agent_options":{"effort":"high"}
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created AgentProfileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Data.ID != "prof_authority" || created.Data.Surface != agentprofiles.SurfaceExternalAgent || !created.Data.ToolsEnabled || !created.Data.WritesAllowed || len(created.Data.SkillIDs) != 2 {
		t.Fatalf("created profile = %+v, want normalized Hecate-compatible response", created.Data)
	}
	profile := getMirroredCairnlineAgentProfileForTest(t, handler, "prof_authority")
	if profile.Name != "Authority profile" || profile.MemoryPolicy != "include" || profile.SourcePolicy != "include_enabled" || !stringSliceContains(profile.SkillIDs, "release") {
		t.Fatalf("Cairnline profile = %+v, want authority metadata", profile)
	}
	execution := getMirroredCairnlineExecutionProfileForTest(t, handler, "prof_authority_exec")
	if execution.AgentKind != "codex" || execution.ProviderHint != "anthropic" || execution.ModelHint != "claude-sonnet-4" || execution.ToolsPolicy != "allow" || execution.WritesPolicy != "allow" || execution.NetworkPolicy != "block" || execution.AdapterOptions["effort"] != "high" {
		t.Fatalf("Cairnline execution profile = %+v, want authority execution posture", execution)
	}
	if shadow, ok, err := handler.agentProfiles.Get(t.Context(), "prof_authority"); err != nil || !ok || shadow.ExternalAgentKind != "codex" || shadow.ExecutionProfile != "prof_authority_exec" {
		t.Fatalf("Hecate shadow profile = %+v ok=%v err=%v, want compatibility row", shadow, ok, err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/agent-profiles/prof_authority", bytes.NewReader([]byte(`{
		"name":"Updated authority profile",
		"model_hint":"claude-opus-4",
		"writes_allowed":false,
		"skill_ids":["review"]
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var updated AgentProfileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if updated.Data.Name != "Updated authority profile" || updated.Data.ModelHint != "claude-opus-4" || updated.Data.WritesAllowed || len(updated.Data.SkillIDs) != 1 || updated.Data.SkillIDs[0] != "review" {
		t.Fatalf("updated profile = %+v, want patched response", updated.Data)
	}
	profile = getMirroredCairnlineAgentProfileForTest(t, handler, "prof_authority")
	if profile.Name != "Updated authority profile" || !stringSliceContains(profile.SkillIDs, "review") || stringSliceContains(profile.SkillIDs, "release") {
		t.Fatalf("updated Cairnline profile = %+v, want patched metadata", profile)
	}
	execution = getMirroredCairnlineExecutionProfileForTest(t, handler, "prof_authority_exec")
	if execution.ModelHint != "claude-opus-4" || execution.WritesPolicy != "block" {
		t.Fatalf("updated Cairnline execution profile = %+v, want patched execution posture", execution)
	}
	if shadow, ok, err := handler.agentProfiles.Get(t.Context(), "prof_authority"); err != nil || !ok || shadow.Name != "Updated authority profile" || shadow.WritesAllowed {
		t.Fatalf("updated Hecate shadow profile = %+v ok=%v err=%v, want patched compatibility row", shadow, ok, err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/agent-profiles/prof_authority", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
	assertCairnlineAgentProfileMissingForTest(t, handler, "prof_authority")
	if _, ok, err := handler.agentProfiles.Get(t.Context(), "prof_authority"); err != nil || ok {
		t.Fatalf("Hecate shadow after delete ok=%v err=%v, want missing", ok, err)
	}
	if !cairnlineExecutionProfileIDExistsForTest(t, handler, "prof_authority_exec") {
		t.Fatalf("delete should leave portable execution profile posture in place")
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/agent-profiles/prof_authority", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestAgentProfilesAPI_BuiltInProfilesAreReadOnly(t *testing.T) {
	t.Parallel()
	server := newAgentProfilesTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/agent-profiles/implementation", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get built-in status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var got AgentProfileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if !got.Data.BuiltIn || got.Data.ExecutionProfile != "coding_agent" || !got.Data.WritesAllowed {
		t.Fatalf("built-in profile = %+v, want implementation posture", got.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/agent-profiles", bytes.NewReader([]byte(`{"id":"implementation","name":"Override"}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("create built-in status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/agent-profiles/implementation", bytes.NewReader([]byte(`{"name":"Override"}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("patch built-in status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/agent-profiles/implementation", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete built-in status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
}

func TestAgentProfilesAPI_RejectsInvalidEnums(t *testing.T) {
	t.Parallel()
	server := newAgentProfilesTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/agent-profiles", bytes.NewReader([]byte(`{
		"id":"prof_bad",
		"name":"Bad",
		"surface":"terminal"
	}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create bad enum status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func agentProfileResponseIDExists(items []AgentProfileResponseItem, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func TestAgentProfilesAPI_GeneratesIDsAndReturnsNotFound(t *testing.T) {
	t.Parallel()
	server := newAgentProfilesTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/agent-profiles", bytes.NewReader([]byte(`{"name":"Generated"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created AgentProfileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if !strings.HasPrefix(created.Data.ID, "prof_") {
		t.Fatalf("generated id = %q, want prof_ prefix", created.Data.ID)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/agent-profiles/prof_missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get missing status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/agent-profiles/prof_missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func getMirroredCairnlineAgentProfileForTest(t *testing.T, handler *Handler, id string) cairnline.AgentProfile {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	profiles, err := service.ListAgentProfiles(t.Context())
	if err != nil {
		t.Fatalf("ListAgentProfiles: %v", err)
	}
	for _, profile := range profiles {
		if profile.ID == id {
			return profile
		}
	}
	t.Fatalf("mirrored Cairnline agent profile %q not found in %+v", id, profiles)
	return cairnline.AgentProfile{}
}

func assertCairnlineAgentProfileMissingForTest(t *testing.T, handler *Handler, id string) {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	profiles, err := service.ListAgentProfiles(t.Context())
	if err != nil {
		t.Fatalf("ListAgentProfiles: %v", err)
	}
	for _, profile := range profiles {
		if profile.ID == id {
			t.Fatalf("mirrored Cairnline agent profile %q still exists: %+v", id, profile)
		}
	}
}

func cairnlineExecutionProfileIDExistsForTest(t *testing.T, handler *Handler, id string) bool {
	t.Helper()
	return getMirroredCairnlineExecutionProfileForTestOrNil(t, handler, id) != nil
}

func getMirroredCairnlineExecutionProfileForTest(t *testing.T, handler *Handler, id string) cairnline.ExecutionProfile {
	t.Helper()
	item := getMirroredCairnlineExecutionProfileForTestOrNil(t, handler, id)
	if item == nil {
		t.Fatalf("mirrored Cairnline execution profile %q not found", id)
	}
	return *item
}

func getMirroredCairnlineExecutionProfileForTestOrNil(t *testing.T, handler *Handler, id string) *cairnline.ExecutionProfile {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	items, err := service.ListExecutionProfiles(t.Context())
	if err != nil {
		t.Fatalf("ListExecutionProfiles: %v", err)
	}
	for _, item := range items {
		if item.ID == id {
			found := item
			return &found
		}
	}
	return nil
}

func stringSliceContains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
