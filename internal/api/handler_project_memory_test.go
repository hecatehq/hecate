package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/storage"
)

func newProjectMemoryTestServer() http.Handler {
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	return NewServer(quietLogger(), handler)
}

func TestProjectMemoryAPI_CRUD(t *testing.T) {
	t.Parallel()
	server := newProjectMemoryTestServer()
	project := createMemoryTestProject(t, server, "Memory")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/memory", bytes.NewReader([]byte(`{
		"title":"Commit style",
		"body":"Use conventional commits.",
		"source_kind":"operator"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create memory status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectMemoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Object != "project_memory_entry" || created.Data.ID == "" {
		t.Fatalf("created response = %+v, want memory entry envelope with id", created)
	}
	if created.Data.ProjectID != project.Data.ID || created.Data.Scope != "project" {
		t.Fatalf("project/scope = %q/%q, want project-scoped entry", created.Data.ProjectID, created.Data.Scope)
	}
	if created.Data.TrustLabel != "operator_memory" || !created.Data.Enabled {
		t.Fatalf("trust/enabled = %q/%v, want operator_memory enabled", created.Data.TrustLabel, created.Data.Enabled)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/memory", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list memory status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var listed ProjectMemoryListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if listed.Object != "project_memory" || len(listed.Data) != 1 || listed.Data[0].ID != created.Data.ID {
		t.Fatalf("listed = %+v, want created entry", listed)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+project.Data.ID+"/memory/"+created.Data.ID, bytes.NewReader([]byte(`{
		"body":"Prefer small, reviewable patches.",
		"trust_label":"generated_summary",
		"source_kind":"handoff",
		"source_id":"art_1",
		"enabled":false
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch memory status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var updated ProjectMemoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if updated.Data.Body != "Prefer small, reviewable patches." || updated.Data.Enabled {
		t.Fatalf("updated memory = %+v, want disabled patched body", updated.Data)
	}
	if updated.Data.TrustLabel != "generated_summary" || updated.Data.SourceKind != "handoff" || updated.Data.SourceID != "art_1" {
		t.Fatalf("updated provenance = %+v, want generated handoff provenance", updated.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/memory", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list active status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode active list response: %v", err)
	}
	if len(listed.Data) != 0 {
		t.Fatalf("active list = %+v, want disabled entry filtered", listed.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/memory?include_disabled=true", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list all status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode all list response: %v", err)
	}
	if len(listed.Data) != 1 || listed.Data[0].ID != created.Data.ID {
		t.Fatalf("all list = %+v, want disabled entry", listed.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+project.Data.ID+"/memory/"+created.Data.ID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete memory status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
}

func TestProjectMemoryAPI_CandidatePromotionFlow(t *testing.T) {
	t.Parallel()
	server := newProjectMemoryTestServer()
	project := createMemoryTestProject(t, server, "Candidates")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/memory/candidates", bytes.NewReader([]byte(`{
		"title":"Generated review note",
		"body":"Keep model-created summaries labelled until reviewed.",
		"suggested_kind":"note",
		"suggested_trust_label":"generated_summary",
		"suggested_source_kind":"task_output",
		"suggested_source_id":"run_1",
		"source_refs":[{"kind":"task_run","id":"run_1","title":"Task run"}]
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create candidate status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectMemoryCandidateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode candidate response: %v", err)
	}
	if created.Object != "project_memory_candidate" || created.Data.ID == "" {
		t.Fatalf("created candidate = %+v, want candidate envelope", created)
	}
	if created.Data.Status != "pending" || created.Data.SuggestedTrustLabel != "generated_summary" || len(created.Data.SourceRefs) != 1 {
		t.Fatalf("created candidate data = %+v, want pending generated candidate with source ref", created.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/memory", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list memory before promotion status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var listedMemory ProjectMemoryListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listedMemory); err != nil {
		t.Fatalf("decode memory list response: %v", err)
	}
	if len(listedMemory.Data) != 0 {
		t.Fatalf("memory before promotion = %+v, want none", listedMemory.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/memory/candidates/"+created.Data.ID+"/promote", bytes.NewReader([]byte(`{
		"title":"Reviewed memory",
		"body":"Keep generated summaries labelled unless the operator upgrades trust.",
		"trust_label":"operator_memory",
		"source_kind":"operator",
		"source_id":"run_1",
		"enabled":true
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("promote candidate status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var promoted ProjectMemoryCandidateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &promoted); err != nil {
		t.Fatalf("decode promoted candidate response: %v", err)
	}
	if promoted.Data.Status != "promoted" || promoted.Data.PromotedMemoryID == "" {
		t.Fatalf("promoted candidate = %+v, want promoted with memory id", promoted.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/memory/candidates/"+created.Data.ID+"/promote", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("repeat promote status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var retried ProjectMemoryCandidateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &retried); err != nil {
		t.Fatalf("decode repeat promoted candidate response: %v", err)
	}
	if retried.Data.PromotedMemoryID != promoted.Data.PromotedMemoryID {
		t.Fatalf("repeat promoted memory id = %q, want %q", retried.Data.PromotedMemoryID, promoted.Data.PromotedMemoryID)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/memory?include_disabled=true", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list memory after promotion status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listedMemory); err != nil {
		t.Fatalf("decode promoted memory list response: %v", err)
	}
	if len(listedMemory.Data) != 1 || listedMemory.Data[0].ID != promoted.Data.PromotedMemoryID {
		t.Fatalf("memory after promotion = %+v, want promoted memory", listedMemory.Data)
	}
	if listedMemory.Data[0].Title != "Reviewed memory" || listedMemory.Data[0].TrustLabel != "operator_memory" {
		t.Fatalf("promoted memory = %+v, want operator edits applied", listedMemory.Data[0])
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/memory/candidates", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list pending candidates status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var listedCandidates ProjectMemoryCandidateListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listedCandidates); err != nil {
		t.Fatalf("decode candidate list response: %v", err)
	}
	if len(listedCandidates.Data) != 0 {
		t.Fatalf("pending candidates = %+v, want none", listedCandidates.Data)
	}
}

func TestProjectMemoryAPI_CandidateRejectFlow(t *testing.T) {
	t.Parallel()
	server := newProjectMemoryTestServer()
	project := createMemoryTestProject(t, server, "Reject")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/memory/candidates", bytes.NewReader([]byte(`{
		"title":"Speculative note",
		"body":"Maybe always skip tests."
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create candidate status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectMemoryCandidateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode candidate response: %v", err)
	}
	if created.Data.SuggestedTrustLabel != "generated_summary" || created.Data.SuggestedSourceKind != "generated" {
		t.Fatalf("candidate defaults = %+v, want generated lower-trust defaults", created.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/memory/candidates/"+created.Data.ID+"/reject", bytes.NewReader([]byte(`{"reason":"Not a durable project fact"}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("reject candidate status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var rejected ProjectMemoryCandidateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &rejected); err != nil {
		t.Fatalf("decode reject response: %v", err)
	}
	if rejected.Data.Status != "rejected" || rejected.Data.StatusReason != "Not a durable project fact" {
		t.Fatalf("rejected candidate = %+v, want rejected reason", rejected.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/memory", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list memory status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var listedMemory ProjectMemoryListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listedMemory); err != nil {
		t.Fatalf("decode memory list response: %v", err)
	}
	if len(listedMemory.Data) != 0 {
		t.Fatalf("memory after rejection = %+v, want none", listedMemory.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/memory/candidates?include_resolved=true", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list resolved candidates status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var listedCandidates ProjectMemoryCandidateListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listedCandidates); err != nil {
		t.Fatalf("decode resolved candidates response: %v", err)
	}
	if len(listedCandidates.Data) != 1 || listedCandidates.Data[0].Status != "rejected" {
		t.Fatalf("resolved candidates = %+v, want rejected candidate", listedCandidates.Data)
	}
}

func TestProjectMemoryAPI_ValidationAndScoping(t *testing.T) {
	t.Parallel()
	server := newProjectMemoryTestServer()
	project := createMemoryTestProject(t, server, "Scoped")
	other := createMemoryTestProject(t, server, "Other")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/memory", bytes.NewReader([]byte(`{"title":" ","body":"Body"}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blank title status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_missing/memory", bytes.NewReader([]byte(`{"title":"T","body":"B"}`))))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing project status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/memory", bytes.NewReader([]byte(`{"title":"Project A","body":"Body"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectMemoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+other.Data.ID+"/memory/"+created.Data.ID, bytes.NewReader([]byte(`{"body":"cross project"}`))))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-project patch status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestProjectMemoryAPI_SQLiteBackendParity(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "hecate.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	projectStore, err := projects.NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore(projects): %v", err)
	}
	memoryStore, err := memory.NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore(memory): %v", err)
	}
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projectStore)
	handler.SetMemoryStore(memoryStore)
	server := NewServer(quietLogger(), handler)
	project := createMemoryTestProject(t, server, "SQLite memory")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/memory", bytes.NewReader([]byte(`{
		"title":"SQLite note",
		"body":"Persist this project-scoped note.",
		"trust_label":"generated_summary",
		"source_kind":"handoff",
		"source_id":"artifact_1"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("sqlite create memory status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectMemoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode sqlite create response: %v", err)
	}
	if created.Data.ProjectID != project.Data.ID || created.Data.TrustLabel != "generated_summary" || created.Data.SourceID != "artifact_1" {
		t.Fatalf("sqlite created memory = %+v, want scoped generated handoff", created.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/projects/"+project.Data.ID+"/memory/"+created.Data.ID, bytes.NewReader([]byte(`{"enabled":false}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("sqlite patch memory status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/memory", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("sqlite active list status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var listed ProjectMemoryListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode sqlite active list response: %v", err)
	}
	if len(listed.Data) != 0 {
		t.Fatalf("sqlite active list = %+v, want disabled entry filtered", listed.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/memory?include_disabled=true", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("sqlite all list status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode sqlite all list response: %v", err)
	}
	if len(listed.Data) != 1 || listed.Data[0].ID != created.Data.ID || listed.Data[0].Enabled {
		t.Fatalf("sqlite all list = %+v, want disabled created entry", listed.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+project.Data.ID+"/memory/"+created.Data.ID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("sqlite delete memory status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/memory/candidates", bytes.NewReader([]byte(`{
		"title":"SQLite candidate",
		"body":"Review before persisting.",
		"suggested_source_kind":"chat_message",
		"suggested_source_id":"msg_1",
		"source_refs":[{"kind":"chat_message","id":"msg_1"}]
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("sqlite create candidate status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var candidate ProjectMemoryCandidateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &candidate); err != nil {
		t.Fatalf("decode sqlite candidate response: %v", err)
	}
	if candidate.Data.SuggestedSourceKind != "chat_message" || len(candidate.Data.SourceRefs) != 1 {
		t.Fatalf("sqlite candidate = %+v, want chat source ref", candidate.Data)
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/memory/candidates/"+candidate.Data.ID+"/reject", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("sqlite reject candidate status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
}

func TestProjectMemoryAPI_ProjectDeleteRemovesMemory(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	projectStore := projects.NewMemoryStore()
	memoryStore := memory.NewMemoryStore()
	handler.SetProjectStore(projectStore)
	handler.SetMemoryStore(memoryStore)
	server := NewServer(quietLogger(), handler)
	project := createMemoryTestProject(t, server, "Delete")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/memory", bytes.NewReader([]byte(`{"title":"T","body":"B"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create memory status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/projects/"+project.Data.ID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete project status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
	items, err := memoryStore.List(t.Context(), memory.Filter{ProjectID: project.Data.ID, IncludeDisabled: true})
	if err != nil {
		t.Fatalf("List memory after project delete: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("memory after project delete = %+v, want none", items)
	}
}

func TestProjectMemoryAPI_CreateDuplicateMapsToConflict(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	projectStore := projects.NewMemoryStore()
	if _, err := projectStore.Create(t.Context(), projects.Project{ID: "proj_conflict", Name: "Conflict"}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	handler.SetProjectStore(projectStore)
	handler.SetMemoryStore(conflictMemoryStore{})
	server := NewServer(quietLogger(), handler)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/proj_conflict/memory", bytes.NewReader([]byte(`{"title":"T","body":"B"}`))))

	if rec.Code != http.StatusConflict {
		t.Fatalf("create duplicate status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	var payload struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload.Error.Type != errCodeConflict {
		t.Fatalf("error type = %q, want %q", payload.Error.Type, errCodeConflict)
	}
}

func TestProjectMemoryAPI_CandidateEndpointsRequireCandidateStore(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	projectStore := projects.NewMemoryStore()
	if _, err := projectStore.Create(t.Context(), projects.Project{ID: "proj_plain", Name: "Plain"}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	handler.SetProjectStore(projectStore)
	handler.SetMemoryStore(conflictMemoryStore{})
	server := NewServer(quietLogger(), handler)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_plain/memory/candidates", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("candidate list status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	var payload struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload.Error.Type != errCodeInvalidRequest || payload.Error.Message != "project memory candidate store is not configured" {
		t.Fatalf("error = %+v, want candidate store configuration error", payload.Error)
	}
}

func createMemoryTestProject(t *testing.T, server http.Handler, name string) ProjectResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects", bytes.NewReader([]byte(`{"name":"`+name+`"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created ProjectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode project response: %v", err)
	}
	return created
}

type conflictMemoryStore struct{}

func (conflictMemoryStore) Backend() string {
	return "memory"
}

func (conflictMemoryStore) Create(context.Context, memory.Entry) (memory.Entry, error) {
	return memory.Entry{}, memory.ErrAlreadyExists
}

func (conflictMemoryStore) Get(context.Context, string, string) (memory.Entry, bool, error) {
	return memory.Entry{}, false, nil
}

func (conflictMemoryStore) List(context.Context, memory.Filter) ([]memory.Entry, error) {
	return nil, nil
}

func (conflictMemoryStore) Update(context.Context, string, string, func(*memory.Entry)) (memory.Entry, error) {
	return memory.Entry{}, memory.ErrNotFound
}

func (conflictMemoryStore) Delete(context.Context, string, string) error {
	return memory.ErrNotFound
}

func (conflictMemoryStore) DeleteByProjectID(context.Context, string) (int, error) {
	return 0, nil
}
