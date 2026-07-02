package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hecatehq/cairnline"
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

func newProjectMemoryCairnlineReadTestServer() http.Handler {
	_, server := newProjectMemoryCairnlineReadTestHandler()
	return server
}

func newProjectMemoryCairnlineReadTestHandler() (*Handler, http.Handler) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	return handler, NewServer(quietLogger(), handler)
}

func newProjectMemoryCairnlineMirrorTestServer(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	handler := NewHandler(config.Config{
		Server:   config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	return handler, NewServer(quietLogger(), handler)
}

func newProjectMemoryCairnlineAuthorityTestServer(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:     "cairnline",
			CairnlineWriteAuthority: "project-memory",
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	return handler, NewServer(quietLogger(), handler)
}

func newProjectMemoryCairnlineCandidateAuthorityTestServer(t *testing.T) (*Handler, http.Handler) {
	t.Helper()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend:     "cairnline",
			CairnlineWriteAuthority: "project-memory,memory-candidates",
		},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	return handler, NewServer(quietLogger(), handler)
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

func TestProjectMemoryAPI_MirrorsMutationsToCairnlineWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectMemoryCairnlineMirrorTestServer(t)
	client := newAPITestClient(t, server)
	project := createMemoryTestProject(t, server, "Mirror Memory")
	projectID := project.Data.ID

	created := mustRequestJSONStatus[ProjectMemoryResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory", `{
		"title":"Release practice",
		"body":"Keep release notes reviewable.",
		"trust_label":"operator_memory",
		"source_kind":"operator",
		"enabled":false
	}`)
	mirroredMemory := getMirroredCairnlineMemoryEntryForTest(t, handler, projectID, created.Data.ID)
	if mirroredMemory.Title != "Release practice" || mirroredMemory.Body != "Keep release notes reviewable." || mirroredMemory.Enabled {
		t.Fatalf("mirrored memory = %+v, want created disabled memory", mirroredMemory)
	}

	updated := mustRequestJSON[ProjectMemoryResponse](client, http.MethodPatch, "/hecate/v1/projects/"+projectID+"/memory/"+created.Data.ID, `{
		"title":"Release practice updated",
		"enabled":true,
		"source_kind":"handoff",
		"source_id":"handoff_1"
	}`)
	mirroredMemory = getMirroredCairnlineMemoryEntryForTest(t, handler, projectID, updated.Data.ID)
	if mirroredMemory.Title != "Release practice updated" || !mirroredMemory.Enabled || mirroredMemory.SourceKind != "handoff" || mirroredMemory.SourceID != "handoff_1" {
		t.Fatalf("mirrored updated memory = %+v, want patched metadata", mirroredMemory)
	}

	client.mustRequestStatus(http.StatusNoContent, http.MethodDelete, "/hecate/v1/projects/"+projectID+"/memory/"+created.Data.ID, "")
	assertCairnlineMemoryEntryMissingForTest(t, handler, projectID, created.Data.ID)

	candidate := mustRequestJSONStatus[ProjectMemoryCandidateResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory/candidates", `{
		"title":"Generated lesson",
		"body":"Mention evidence in handoffs.",
		"suggested_kind":"lesson",
		"suggested_trust_label":"generated_summary",
		"suggested_source_kind":"artifact",
		"suggested_source_id":"art_1",
		"source_refs":[{"kind":"artifact","id":"art_1","title":"Review artifact","url":"https://example.test/review"}]
	}`)
	mirroredCandidate := getMirroredCairnlineMemoryCandidateForTest(t, handler, projectID, candidate.Data.ID)
	if mirroredCandidate.Status != cairnline.MemoryCandidatePending || len(mirroredCandidate.SourceRefs) != 1 || mirroredCandidate.SourceRefs[0].URL != "https://example.test/review" {
		t.Fatalf("mirrored candidate = %+v, want pending source-ref metadata", mirroredCandidate)
	}

	promoted := mustRequestJSON[ProjectMemoryCandidateResponse](client, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory/candidates/"+candidate.Data.ID+"/promote", `{
		"title":"Reviewed lesson",
		"body":"Mention evidence in handoffs after review.",
		"trust_label":"operator_memory",
		"source_kind":"operator",
		"source_id":"art_1",
		"enabled":true
	}`)
	if promoted.Data.PromotedMemoryID == "" {
		t.Fatalf("promoted candidate = %+v, want promoted memory id", promoted.Data)
	}
	mirroredPromotedMemory := getMirroredCairnlineMemoryEntryForTest(t, handler, projectID, promoted.Data.PromotedMemoryID)
	if mirroredPromotedMemory.Title != "Reviewed lesson" || mirroredPromotedMemory.Body != "Mention evidence in handoffs after review." || mirroredPromotedMemory.TrustLabel != "operator_memory" {
		t.Fatalf("mirrored promoted memory = %+v, want reviewed entry", mirroredPromotedMemory)
	}
	mirroredCandidate = getMirroredCairnlineMemoryCandidateForTest(t, handler, projectID, candidate.Data.ID)
	if mirroredCandidate.Status != cairnline.MemoryCandidatePromoted || mirroredCandidate.PromotedMemoryID != promoted.Data.PromotedMemoryID {
		t.Fatalf("mirrored promoted candidate = %+v, want promoted status and memory ref", mirroredCandidate)
	}

	rejectCandidate := mustRequestJSONStatus[ProjectMemoryCandidateResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory/candidates", `{
		"title":"Speculative lesson",
		"body":"Skip tests when tired."
	}`)
	rejected := mustRequestJSON[ProjectMemoryCandidateResponse](client, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory/candidates/"+rejectCandidate.Data.ID+"/reject", `{"reason":"Not durable project guidance"}`)
	mirroredRejected := getMirroredCairnlineMemoryCandidateForTest(t, handler, projectID, rejected.Data.ID)
	if mirroredRejected.Status != cairnline.MemoryCandidateRejected || mirroredRejected.StatusReason != "Not durable project guidance" || mirroredRejected.PromotedMemoryID != "" {
		t.Fatalf("mirrored rejected candidate = %+v, want rejected reason without promoted memory", mirroredRejected)
	}
}

func TestProjectMemoryAPI_CairnlineWriteAuthorityCommitsAcceptedMemoryFirst(t *testing.T) {
	t.Parallel()
	handler, server := newProjectMemoryCairnlineAuthorityTestServer(t)
	client := newAPITestClient(t, server)
	project := createMemoryTestProject(t, server, "Memory Authority")
	projectID := project.Data.ID

	created := mustRequestJSONStatus[ProjectMemoryResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory", `{
		"title":"Authority note",
		"body":"Accepted memory can be Cairnline-authoritative.",
		"enabled":false
	}`)
	if created.Data.ReadBackend != "cairnline" || created.Data.TrustLabel != memory.TrustLabelOperatorMemory || created.Data.SourceKind != memory.SourceKindOperator || created.Data.Enabled {
		t.Fatalf("created memory = %+v, want disabled Cairnline-backed operator memory with normalized source", created.Data)
	}
	mirroredMemory := getMirroredCairnlineMemoryEntryForTest(t, handler, projectID, created.Data.ID)
	if mirroredMemory.Title != "Authority note" || mirroredMemory.SourceKind != memory.SourceKindOperator || mirroredMemory.Enabled {
		t.Fatalf("Cairnline memory = %+v, want authoritative disabled normalized entry", mirroredMemory)
	}
	shadow, ok, err := handler.memory.Get(t.Context(), projectID, created.Data.ID)
	if err != nil || !ok {
		t.Fatalf("native shadow memory ok=%v error=%v, want present", ok, err)
	}
	if shadow.Title != created.Data.Title || shadow.SourceKind != memory.SourceKindOperator || shadow.Enabled {
		t.Fatalf("native shadow memory = %+v, want created Cairnline entry shadowed", shadow)
	}

	updated := mustRequestJSON[ProjectMemoryResponse](client, http.MethodPatch, "/hecate/v1/projects/"+projectID+"/memory/"+created.Data.ID, `{
		"title":"Authority note updated",
		"body":"Shadow Hecate after Cairnline commits.",
		"trust_label":"generated_summary",
		"source_kind":"handoff",
		"source_id":"handoff_1",
		"enabled":true
	}`)
	if updated.Data.ReadBackend != "cairnline" || updated.Data.Title != "Authority note updated" || updated.Data.TrustLabel != memory.TrustLabelGenerated || updated.Data.SourceKind != "handoff" || !updated.Data.Enabled {
		t.Fatalf("updated memory = %+v, want Cairnline-backed patched entry", updated.Data)
	}
	mirroredMemory = getMirroredCairnlineMemoryEntryForTest(t, handler, projectID, created.Data.ID)
	if mirroredMemory.Title != "Authority note updated" || mirroredMemory.Body != "Shadow Hecate after Cairnline commits." || mirroredMemory.SourceKind != "handoff" || mirroredMemory.SourceID != "handoff_1" || !mirroredMemory.Enabled {
		t.Fatalf("updated Cairnline memory = %+v, want patched authoritative entry", mirroredMemory)
	}
	shadow, ok, err = handler.memory.Get(t.Context(), projectID, created.Data.ID)
	if err != nil || !ok {
		t.Fatalf("updated native shadow memory ok=%v error=%v, want present", ok, err)
	}
	if shadow.Title != updated.Data.Title || shadow.Body != updated.Data.Body || shadow.TrustLabel != updated.Data.TrustLabel || shadow.SourceKind != updated.Data.SourceKind || shadow.SourceID != updated.Data.SourceID || shadow.Enabled != updated.Data.Enabled {
		t.Fatalf("updated native shadow memory = %+v, want updated Cairnline entry shadowed", shadow)
	}

	candidate := mustRequestJSONStatus[ProjectMemoryCandidateResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory/candidates", `{
		"title":"Candidate remains Hecate-owned",
		"body":"Candidates are a separate authority switchpoint."
	}`)
	if candidate.Data.ReadBackend != "hecate" {
		t.Fatalf("candidate read backend = %q, want hecate while only accepted memory is Cairnline-authoritative", candidate.Data.ReadBackend)
	}

	client.mustRequestStatus(http.StatusNoContent, http.MethodDelete, "/hecate/v1/projects/"+projectID+"/memory/"+created.Data.ID, "")
	assertCairnlineMemoryEntryMissingForTest(t, handler, projectID, created.Data.ID)
	if _, ok, err := handler.memory.Get(t.Context(), projectID, created.Data.ID); err != nil || ok {
		t.Fatalf("deleted native shadow ok=%v error=%v, want missing", ok, err)
	}
}

func TestProjectMemoryAPI_CairnlineWriteAuthorityAcceptsCairnlineOnlyProject(t *testing.T) {
	t.Parallel()
	handler, server := newProjectMemoryCairnlineAuthorityTestServer(t)
	client := newAPITestClient(t, server)
	const projectID = "proj_cairnline_memory_only"
	seedCairnlineOnlyProjectWorkGraphForTest(t, handler, cairnline.Project{
		ID:   projectID,
		Name: "Cairnline memory only",
	}, nil, nil, nil)

	created := mustRequestJSONStatus[ProjectMemoryResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory", `{
		"title":"Cairnline-only note",
		"body":"Accepted memory does not need a native Hecate project row.",
		"enabled":false
	}`)
	if created.Data.ReadBackend != "cairnline" || created.Data.ProjectID != projectID || created.Data.Enabled {
		t.Fatalf("created memory = %+v, want disabled Cairnline-backed entry for Cairnline-only project", created.Data)
	}
	mirroredMemory := getMirroredCairnlineMemoryEntryForTest(t, handler, projectID, created.Data.ID)
	if mirroredMemory.Title != "Cairnline-only note" || mirroredMemory.Body != "Accepted memory does not need a native Hecate project row." || mirroredMemory.Enabled {
		t.Fatalf("Cairnline memory = %+v, want authoritative entry on Cairnline-only project", mirroredMemory)
	}
	shadow, ok, err := handler.memory.Get(t.Context(), projectID, created.Data.ID)
	if err != nil || !ok {
		t.Fatalf("native shadow memory ok=%v error=%v, want present even without native project row", ok, err)
	}
	if shadow.ProjectID != projectID || shadow.Title != created.Data.Title {
		t.Fatalf("native shadow memory = %+v, want Cairnline-only project id and title", shadow)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("native project exists = %t err=%v, want missing after memory create", ok, err)
	}

	updated := mustRequestJSON[ProjectMemoryResponse](client, http.MethodPatch, "/hecate/v1/projects/"+projectID+"/memory/"+created.Data.ID, `{
		"title":"Cairnline-only note updated",
		"enabled":true,
		"source_kind":"operator",
		"source_id":"manual"
	}`)
	if updated.Data.ReadBackend != "cairnline" || updated.Data.Title != "Cairnline-only note updated" || !updated.Data.Enabled || updated.Data.SourceID != "manual" {
		t.Fatalf("updated memory = %+v, want patched Cairnline-only entry", updated.Data)
	}
	mirroredMemory = getMirroredCairnlineMemoryEntryForTest(t, handler, projectID, created.Data.ID)
	if mirroredMemory.Title != "Cairnline-only note updated" || !mirroredMemory.Enabled || mirroredMemory.SourceID != "manual" {
		t.Fatalf("updated Cairnline memory = %+v, want patched authoritative entry", mirroredMemory)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("native project exists = %t err=%v, want still missing after memory update", ok, err)
	}

	client.mustRequestStatus(http.StatusNoContent, http.MethodDelete, "/hecate/v1/projects/"+projectID+"/memory/"+created.Data.ID, "")
	assertCairnlineMemoryEntryMissingForTest(t, handler, projectID, created.Data.ID)
	if _, ok, err := handler.memory.Get(t.Context(), projectID, created.Data.ID); err != nil || ok {
		t.Fatalf("deleted native shadow ok=%v error=%v, want missing", ok, err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("native project exists = %t err=%v, want still missing after memory delete", ok, err)
	}
}

func TestShadowProjectMemoryEntryToHecateUpdatesExistingShadowInPlace(t *testing.T) {
	t.Parallel()
	store := &existingProjectMemoryShadowStore{
		entry: memory.Entry{
			ID:         "mem_existing",
			Scope:      memory.ScopeProject,
			ProjectID:  "proj_existing",
			Title:      "Old title",
			Body:       "Old body",
			TrustLabel: memory.TrustLabelOperatorMemory,
			SourceKind: memory.SourceKindOperator,
			SourceID:   "old_source",
			Enabled:    false,
		},
	}
	handler := &Handler{memory: store}

	handler.shadowProjectMemoryEntryToHecate(t.Context(), "test_shadow_update", memory.Entry{
		ID:         "mem_existing",
		Scope:      memory.ScopeProject,
		ProjectID:  "proj_existing",
		Title:      "New title",
		Body:       "New body",
		TrustLabel: memory.TrustLabelGenerated,
		SourceKind: "handoff",
		SourceID:   "handoff_1",
		Enabled:    true,
	})

	if store.deleted || store.created {
		t.Fatalf("shadow calls delete=%v create=%v, want update in place", store.deleted, store.created)
	}
	if !store.updated {
		t.Fatal("shadow did not update existing entry")
	}
	if got := store.entry; got.Title != "New title" || got.Body != "New body" || got.TrustLabel != memory.TrustLabelGenerated || got.SourceKind != "handoff" || got.SourceID != "handoff_1" || !got.Enabled {
		t.Fatalf("shadow entry = %+v, want updated fields", got)
	}
}

func TestProjectMemoryAPI_CairnlineWriteAuthorityCommitsMemoryCandidatesFirst(t *testing.T) {
	t.Parallel()
	handler, server := newProjectMemoryCairnlineCandidateAuthorityTestServer(t)
	client := newAPITestClient(t, server)
	project := createMemoryTestProject(t, server, "Memory Candidate Authority")
	projectID := project.Data.ID

	rejectCandidate := mustRequestJSONStatus[ProjectMemoryCandidateResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory/candidates", `{
		"title":"Generated maybe",
		"body":"Reviewable candidates can be Cairnline-authoritative.",
		"source_refs":[{"kind":"handoff","id":"hand_1","title":"Handoff"}]
	}`)
	if rejectCandidate.Data.ReadBackend != "cairnline" || rejectCandidate.Data.SuggestedTrustLabel != memory.TrustLabelGenerated || rejectCandidate.Data.SuggestedSourceKind != memory.SourceKindGenerated || len(rejectCandidate.Data.SourceRefs) != 1 {
		t.Fatalf("created candidate = %+v, want Cairnline-backed normalized candidate with source ref", rejectCandidate.Data)
	}
	mirroredCandidate := getMirroredCairnlineMemoryCandidateForTest(t, handler, projectID, rejectCandidate.Data.ID)
	if mirroredCandidate.Status != cairnline.MemoryCandidatePending || mirroredCandidate.Title != "Generated maybe" {
		t.Fatalf("Cairnline candidate = %+v, want pending authoritative candidate", mirroredCandidate)
	}
	shadowCandidate, ok, err := handler.memoryCandidates.GetCandidate(t.Context(), projectID, rejectCandidate.Data.ID)
	if err != nil || !ok {
		t.Fatalf("native candidate shadow ok=%v error=%v, want present", ok, err)
	}
	if shadowCandidate.ID != rejectCandidate.Data.ID || shadowCandidate.Title != rejectCandidate.Data.Title || shadowCandidate.Status != memory.CandidateStatusPending {
		t.Fatalf("native candidate shadow = %+v, want shadowed pending candidate", shadowCandidate)
	}

	rejected := mustRequestJSON[ProjectMemoryCandidateResponse](client, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory/candidates/"+rejectCandidate.Data.ID+"/reject", `{"reason":"Too speculative"}`)
	if rejected.Data.ReadBackend != "cairnline" || rejected.Data.Status != memory.CandidateStatusRejected || rejected.Data.StatusReason != "Too speculative" {
		t.Fatalf("rejected candidate = %+v, want Cairnline-backed rejected state", rejected.Data)
	}
	mirroredCandidate = getMirroredCairnlineMemoryCandidateForTest(t, handler, projectID, rejectCandidate.Data.ID)
	if mirroredCandidate.Status != cairnline.MemoryCandidateRejected || mirroredCandidate.StatusReason != "Too speculative" {
		t.Fatalf("Cairnline rejected candidate = %+v, want rejected reason", mirroredCandidate)
	}
	shadowCandidate, ok, err = handler.memoryCandidates.GetCandidate(t.Context(), projectID, rejectCandidate.Data.ID)
	if err != nil || !ok || shadowCandidate.Status != memory.CandidateStatusRejected || shadowCandidate.StatusReason != "Too speculative" {
		t.Fatalf("native rejected candidate shadow = %+v ok=%v error=%v, want rejected shadow", shadowCandidate, ok, err)
	}

	promoteCandidate := mustRequestJSONStatus[ProjectMemoryCandidateResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory/candidates", `{
		"title":"Generated lesson",
		"body":"Capture durable lessons after review.",
		"suggested_kind":"lesson",
		"suggested_trust_label":"generated_summary",
		"suggested_source_kind":"artifact",
		"suggested_source_id":"art_1"
	}`)
	promoted := mustRequestJSON[ProjectMemoryCandidateResponse](client, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory/candidates/"+promoteCandidate.Data.ID+"/promote", `{
		"title":"Reviewed lesson",
		"body":"Capture durable lessons after operator review.",
		"trust_label":"operator_memory",
		"source_kind":"operator",
		"source_id":"art_1",
		"enabled":true
	}`)
	if promoted.Data.ReadBackend != "cairnline" || promoted.Data.Status != memory.CandidateStatusPromoted || promoted.Data.PromotedMemoryID == "" {
		t.Fatalf("promoted candidate = %+v, want Cairnline-backed promoted state", promoted.Data)
	}
	mirroredMemory := getMirroredCairnlineMemoryEntryForTest(t, handler, projectID, promoted.Data.PromotedMemoryID)
	if mirroredMemory.Title != "Reviewed lesson" || mirroredMemory.Body != "Capture durable lessons after operator review." || mirroredMemory.TrustLabel != memory.TrustLabelOperatorMemory || mirroredMemory.SourceKind != memory.SourceKindOperator {
		t.Fatalf("Cairnline promoted memory = %+v, want reviewed operator memory", mirroredMemory)
	}
	shadowMemory, ok, err := handler.memory.Get(t.Context(), projectID, promoted.Data.PromotedMemoryID)
	if err != nil || !ok || shadowMemory.Title != "Reviewed lesson" || shadowMemory.TrustLabel != memory.TrustLabelOperatorMemory {
		t.Fatalf("native promoted memory shadow = %+v ok=%v error=%v, want promoted memory shadow", shadowMemory, ok, err)
	}
	shadowCandidate, ok, err = handler.memoryCandidates.GetCandidate(t.Context(), projectID, promoteCandidate.Data.ID)
	if err != nil || !ok || shadowCandidate.Status != memory.CandidateStatusPromoted || shadowCandidate.PromotedMemoryID != promoted.Data.PromotedMemoryID {
		t.Fatalf("native promoted candidate shadow = %+v ok=%v error=%v, want promoted candidate shadow", shadowCandidate, ok, err)
	}

	retried := mustRequestJSON[ProjectMemoryCandidateResponse](client, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory/candidates/"+promoteCandidate.Data.ID+"/promote", `{}`)
	if retried.Data.PromotedMemoryID != promoted.Data.PromotedMemoryID {
		t.Fatalf("repeat promote id = %q, want %q", retried.Data.PromotedMemoryID, promoted.Data.PromotedMemoryID)
	}
}

func TestProjectMemoryAPI_CairnlineCandidateAuthorityAcceptsCairnlineOnlyProject(t *testing.T) {
	t.Parallel()
	handler, server := newProjectMemoryCairnlineCandidateAuthorityTestServer(t)
	client := newAPITestClient(t, server)
	const projectID = "proj_cairnline_candidate_only"
	seedCairnlineOnlyProjectWorkGraphForTest(t, handler, cairnline.Project{
		ID:   projectID,
		Name: "Cairnline candidate only",
	}, nil, nil, nil)

	candidate := mustRequestJSONStatus[ProjectMemoryCandidateResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory/candidates", `{
		"title":"Cairnline-only candidate",
		"body":"Reviewable memory candidates can start from a Cairnline-only project.",
		"source_refs":[{"kind":"operator","id":"manual","title":"Operator note"}]
	}`)
	if candidate.Data.ReadBackend != "cairnline" || candidate.Data.ProjectID != projectID || candidate.Data.Status != memory.CandidateStatusPending || len(candidate.Data.SourceRefs) != 1 {
		t.Fatalf("created candidate = %+v, want pending Cairnline-backed candidate for Cairnline-only project", candidate.Data)
	}
	mirroredCandidate := getMirroredCairnlineMemoryCandidateForTest(t, handler, projectID, candidate.Data.ID)
	if mirroredCandidate.Title != "Cairnline-only candidate" || mirroredCandidate.Status != cairnline.MemoryCandidatePending {
		t.Fatalf("Cairnline candidate = %+v, want authoritative pending candidate", mirroredCandidate)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("native project exists = %t err=%v, want missing after candidate create", ok, err)
	}

	promoted := mustRequestJSON[ProjectMemoryCandidateResponse](client, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory/candidates/"+candidate.Data.ID+"/promote", `{
		"title":"Reviewed Cairnline-only lesson",
		"body":"Promoting a candidate writes memory through Cairnline authority.",
		"trust_label":"operator_memory",
		"source_kind":"operator",
		"source_id":"manual",
		"enabled":true
	}`)
	if promoted.Data.ReadBackend != "cairnline" || promoted.Data.Status != memory.CandidateStatusPromoted || promoted.Data.PromotedMemoryID == "" {
		t.Fatalf("promoted candidate = %+v, want Cairnline-backed promoted candidate", promoted.Data)
	}
	promotedMemory := getMirroredCairnlineMemoryEntryForTest(t, handler, projectID, promoted.Data.PromotedMemoryID)
	if promotedMemory.Title != "Reviewed Cairnline-only lesson" || promotedMemory.TrustLabel != memory.TrustLabelOperatorMemory || !promotedMemory.Enabled {
		t.Fatalf("promoted Cairnline memory = %+v, want reviewed enabled memory entry", promotedMemory)
	}
	shadowMemory, ok, err := handler.memory.Get(t.Context(), projectID, promoted.Data.PromotedMemoryID)
	if err != nil || !ok || shadowMemory.Title != "Reviewed Cairnline-only lesson" {
		t.Fatalf("native promoted memory shadow = %+v ok=%v error=%v, want present", shadowMemory, ok, err)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("native project exists = %t err=%v, want still missing after candidate promote", ok, err)
	}

	rejectCandidate := mustRequestJSONStatus[ProjectMemoryCandidateResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory/candidates", `{
		"title":"Reject me",
		"body":"This one should stay out of memory."
	}`)
	rejected := mustRequestJSON[ProjectMemoryCandidateResponse](client, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory/candidates/"+rejectCandidate.Data.ID+"/reject", `{"reason":"Not durable"}`)
	if rejected.Data.ReadBackend != "cairnline" || rejected.Data.Status != memory.CandidateStatusRejected || rejected.Data.StatusReason != "Not durable" {
		t.Fatalf("rejected candidate = %+v, want Cairnline-backed rejected candidate", rejected.Data)
	}
	mirroredRejected := getMirroredCairnlineMemoryCandidateForTest(t, handler, projectID, rejectCandidate.Data.ID)
	if mirroredRejected.Status != cairnline.MemoryCandidateRejected || mirroredRejected.StatusReason != "Not durable" {
		t.Fatalf("Cairnline rejected candidate = %+v, want rejected reason", mirroredRejected)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("native project exists = %t err=%v, want still missing after candidate reject", ok, err)
	}
}

func TestProjectMemoryAPI_ListUsesCairnlineReadModelWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectMemoryCairnlineReadTestHandler()
	project := createMemoryTestProject(t, server, "Cairnline Memory")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/memory", bytes.NewReader([]byte(`{
		"title":"Enabled note",
		"body":"Visible to project agents.",
		"trust_label":"operator_memory",
		"source_kind":"operator"
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create enabled memory status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var enabled ProjectMemoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &enabled); err != nil {
		t.Fatalf("decode enabled memory: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/memory", bytes.NewReader([]byte(`{
		"title":"Disabled note",
		"body":"Keep inspectable but do not include by default.",
		"enabled":false
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create disabled memory status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var disabled ProjectMemoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &disabled); err != nil {
		t.Fatalf("decode disabled memory: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/memory", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list enabled memory status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var listed ProjectMemoryListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode enabled memory list: %v", err)
	}
	if len(listed.Data) != 1 || listed.Data[0].ID != enabled.Data.ID || listed.Data[0].ReadBackend != "cairnline" {
		t.Fatalf("enabled memory list = %+v, want only enabled Cairnline-backed entry", listed.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/memory?include_disabled=true", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list all memory status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	listed = ProjectMemoryListResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode all memory list: %v", err)
	}
	if len(listed.Data) != 2 {
		t.Fatalf("all memory list = %+v, want enabled and disabled entries", listed.Data)
	}
	disabledEntry := projectMemoryResponseForTest(listed.Data, disabled.Data.ID)
	if disabledEntry == nil || disabledEntry.ReadBackend != "cairnline" || disabledEntry.Enabled {
		t.Fatalf("disabled memory entry = %+v, want disabled Cairnline-backed entry", disabledEntry)
	}

	nativeEntries, err := handler.memory.List(t.Context(), memory.Filter{
		ProjectID:       project.Data.ID,
		IncludeDisabled: true,
	})
	if err != nil {
		t.Fatalf("List native memory entries: %v", err)
	}
	assertProjectMemoryProjectionParity(t, renderProjectMemoryEntries(nativeEntries, "hecate"), listed.Data)
}

func TestProjectMemoryAPI_CandidatesUseCairnlineReadModelWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectMemoryCairnlineReadTestHandler()
	project := createMemoryTestProject(t, server, "Cairnline Candidates")

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/memory/candidates", bytes.NewReader([]byte(`{
		"title":"Pending note",
		"body":"Review before promotion.",
		"suggested_kind":"note",
		"suggested_trust_label":"generated_summary",
		"source_refs":[{"kind":"handoff","id":"hand_1","title":"Handoff"}]
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create pending candidate status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var pending ProjectMemoryCandidateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &pending); err != nil {
		t.Fatalf("decode pending candidate: %v", err)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/memory/candidates", bytes.NewReader([]byte(`{
		"title":"Rejected note",
		"body":"Not durable enough."
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create rejected candidate status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var rejected ProjectMemoryCandidateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &rejected); err != nil {
		t.Fatalf("decode rejected candidate: %v", err)
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/projects/"+project.Data.ID+"/memory/candidates/"+rejected.Data.ID+"/reject", bytes.NewReader([]byte(`{"reason":"Not durable"}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("reject candidate status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/memory/candidates", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list pending candidates status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var listed ProjectMemoryCandidateListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode pending candidate list: %v", err)
	}
	if len(listed.Data) != 1 || listed.Data[0].ID != pending.Data.ID || listed.Data[0].ReadBackend != "cairnline" || len(listed.Data[0].SourceRefs) != 1 {
		t.Fatalf("pending candidate list = %+v, want pending Cairnline-backed candidate with source refs", listed.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/memory/candidates?include_resolved=true", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list all candidates status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	listed = ProjectMemoryCandidateListResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode all candidate list: %v", err)
	}
	rejectedCandidate := projectMemoryCandidateResponseForTest(listed.Data, rejected.Data.ID)
	if len(listed.Data) != 2 || rejectedCandidate == nil || rejectedCandidate.ReadBackend != "cairnline" || rejectedCandidate.Status != memory.CandidateStatusRejected || rejectedCandidate.StatusReason != "Not durable" {
		t.Fatalf("all candidate list = %+v, want rejected Cairnline-backed candidate with reason", listed.Data)
	}

	nativeCandidates, err := handler.memoryCandidates.ListCandidates(t.Context(), memory.CandidateFilter{ProjectID: project.Data.ID})
	if err != nil {
		t.Fatalf("List native memory candidates: %v", err)
	}
	assertProjectMemoryCandidateProjectionParity(t, renderProjectMemoryCandidates(nativeCandidates, "hecate"), listed.Data)
}

func TestProjectMemoryAPI_StrictEmbeddedReadModelReadsMemoryWithoutHecateStores(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
		Projects: config.ProjectsConfig{
			CoordinationBackend: "cairnline",
			CairnlineReadSource: "embedded",
		},
	}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)
	client := newAPITestClient(t, server)
	const projectID = "proj_embedded_memory"

	if err := handler.withCairnlineEmbeddedMirrorService(t.Context(), func(service *cairnline.Service) error {
		if _, err := service.CreateProject(t.Context(), cairnline.Project{
			ID:   projectID,
			Name: "Embedded Memory",
		}); err != nil {
			return err
		}
		if _, err := service.CreateMemoryEntry(t.Context(), cairnline.MemoryEntry{
			ID:         "mem_embedded",
			ProjectID:  projectID,
			Title:      "Embedded note",
			Body:       "Read directly from Cairnline.",
			TrustLabel: memory.TrustLabelOperatorMemory,
			SourceKind: memory.SourceKindOperator,
		}); err != nil {
			return err
		}
		if _, err := service.CreateMemoryCandidate(t.Context(), cairnline.MemoryCandidate{
			ID:                  "memcand_embedded",
			ProjectID:           projectID,
			Title:               "Embedded candidate",
			Body:                "Review directly from Cairnline.",
			SuggestedKind:       "note",
			SuggestedTrustLabel: memory.TrustLabelGenerated,
			SuggestedSourceKind: memory.SourceKindGenerated,
			SourceRefs: []cairnline.MemoryCandidateSourceRef{{
				Kind:  "operator",
				ID:    "seed",
				Title: "Seed",
			}},
		}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seed embedded Cairnline memory: %v", err)
	}

	listedMemory := mustRequestJSON[ProjectMemoryListResponse](client, http.MethodGet, "/hecate/v1/projects/"+projectID+"/memory", "")
	if listedMemory.Object != "project_memory" || len(listedMemory.Data) != 1 {
		t.Fatalf("memory response = %+v, want one embedded Cairnline entry", listedMemory)
	}
	if got := listedMemory.Data[0]; got.ID != "mem_embedded" || got.ProjectID != projectID || got.ReadBackend != "cairnline" || !got.Enabled {
		t.Fatalf("memory entry = %+v, want embedded Cairnline projection", got)
	}

	listedCandidates := mustRequestJSON[ProjectMemoryCandidateListResponse](client, http.MethodGet, "/hecate/v1/projects/"+projectID+"/memory/candidates", "")
	if listedCandidates.Object != "project_memory_candidates" || len(listedCandidates.Data) != 1 {
		t.Fatalf("candidate response = %+v, want one embedded Cairnline candidate", listedCandidates)
	}
	if got := listedCandidates.Data[0]; got.ID != "memcand_embedded" || got.ProjectID != projectID || got.ReadBackend != "cairnline" || got.Status != memory.CandidateStatusPending || len(got.SourceRefs) != 1 {
		t.Fatalf("memory candidate = %+v, want embedded Cairnline projection with source ref", got)
	}

	client.mustRequestStatus(http.StatusNotFound, http.MethodGet, "/hecate/v1/projects/proj_missing/memory", "")
}

func TestProjectMemoryAPI_ListUsesCairnlineSidecarWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineSidecarReadTestServer(t, "memory-fixture")
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar memory list enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("sidecar read-route predicate = false, want true")
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/memory", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("memory status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectMemoryListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode memory response: %v", err)
	}
	if response.Object != "project_memory" || len(response.Data) != 1 {
		t.Fatalf("memory response = %+v, want one enabled fixture entry", response)
	}
	entry := projectMemoryResponseForTest(response.Data, "mem_fixture")
	if entry == nil || entry.ReadBackend != "cairnline" || entry.ProjectID != "proj_fixture" || !entry.Enabled || entry.Title != "Fixture memory" {
		t.Fatalf("memory entry = %+v, want sidecar Cairnline fixture memory", entry)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/memory?include_disabled=true", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("memory include-disabled status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	response = ProjectMemoryListResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode include-disabled memory response: %v", err)
	}
	disabled := projectMemoryResponseForTest(response.Data, "mem_disabled_fixture")
	if len(response.Data) != 2 || disabled == nil || disabled.ReadBackend != "cairnline" || disabled.Enabled {
		t.Fatalf("memory entries = %+v, want disabled sidecar fixture included", response.Data)
	}
}

func TestProjectMemoryAPI_CandidatesUseCairnlineSidecarWhenConfigured(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineSidecarReadTestServer(t, "memory-fixture")
	if handler.projectReadRoutesUseCairnlineReadModel() {
		t.Fatal("sidecar memory candidates enabled embedded Cairnline read-model routes")
	}
	if !handler.projectCairnlineSidecarReadRoutesEnabled() {
		t.Fatal("sidecar read-route predicate = false, want true")
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/memory/candidates", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("memory candidates status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectMemoryCandidateListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode memory candidates response: %v", err)
	}
	pending := projectMemoryCandidateResponseForTest(response.Data, "memcand_fixture")
	if len(response.Data) != 1 || pending == nil || pending.ReadBackend != "cairnline" || pending.ProjectID != "proj_fixture" || pending.Status != memory.CandidateStatusPending || len(pending.SourceRefs) != 1 {
		t.Fatalf("memory candidates = %+v, want one pending sidecar fixture candidate", response.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_fixture/memory/candidates?include_resolved=true", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("memory candidates include-resolved status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	response = ProjectMemoryCandidateListResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode include-resolved memory candidates response: %v", err)
	}
	rejected := projectMemoryCandidateResponseForTest(response.Data, "memcand_rejected_fixture")
	if len(response.Data) != 2 || rejected == nil || rejected.ReadBackend != "cairnline" || rejected.Status != memory.CandidateStatusRejected || rejected.StatusReason != "Not durable." {
		t.Fatalf("memory candidates = %+v, want rejected sidecar fixture candidate included", response.Data)
	}
}

func TestProjectMemoryAPI_CairnlineSidecarReadRequiresStructuredContent(t *testing.T) {
	t.Parallel()
	_, server := newProjectsCairnlineSidecarReadTestServer(t, "text-only")

	for _, endpoint := range []string{
		"/hecate/v1/projects/proj_fixture/memory",
		"/hecate/v1/projects/proj_fixture/memory/candidates",
	} {
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, endpoint, nil))
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("%s status = %d body=%s, want 502", endpoint, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "structuredContent") {
			t.Fatalf("%s error body = %s, want structuredContent diagnostic", endpoint, rec.Body.String())
		}
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

func projectMemoryResponseForTest(items []ProjectMemoryResponseItem, id string) *ProjectMemoryResponseItem {
	for index := range items {
		if items[index].ID == id {
			return &items[index]
		}
	}
	return nil
}

func projectMemoryCandidateResponseForTest(items []ProjectMemoryCandidateResponseItem, id string) *ProjectMemoryCandidateResponseItem {
	for index := range items {
		if items[index].ID == id {
			return &items[index]
		}
	}
	return nil
}

func assertProjectMemoryProjectionParity(t *testing.T, hecate, cairnline []ProjectMemoryResponseItem) {
	t.Helper()
	if len(hecate) != len(cairnline) {
		t.Fatalf("memory projection count = hecate:%d cairnline:%d", len(hecate), len(cairnline))
	}
	normalizedHecate := append([]ProjectMemoryResponseItem(nil), hecate...)
	normalizedCairnline := append([]ProjectMemoryResponseItem(nil), cairnline...)
	for idx := range normalizedHecate {
		if normalizedHecate[idx].ReadBackend != "hecate" {
			t.Fatalf("hecate memory[%d] read_backend = %q, want hecate", idx, normalizedHecate[idx].ReadBackend)
		}
		if normalizedCairnline[idx].ReadBackend != "cairnline" {
			t.Fatalf("cairnline memory[%d] read_backend = %q, want cairnline", idx, normalizedCairnline[idx].ReadBackend)
		}
		normalizedHecate[idx].ReadBackend = ""
		normalizedCairnline[idx].ReadBackend = ""
	}
	if !reflect.DeepEqual(normalizedHecate, normalizedCairnline) {
		t.Fatalf("memory projection mismatch\nhecate:   %+v\ncairnline: %+v", normalizedHecate, normalizedCairnline)
	}
}

func assertProjectMemoryCandidateProjectionParity(t *testing.T, hecate, cairnline []ProjectMemoryCandidateResponseItem) {
	t.Helper()
	if len(hecate) != len(cairnline) {
		t.Fatalf("memory candidate projection count = hecate:%d cairnline:%d", len(hecate), len(cairnline))
	}
	normalizedHecate := append([]ProjectMemoryCandidateResponseItem(nil), hecate...)
	normalizedCairnline := append([]ProjectMemoryCandidateResponseItem(nil), cairnline...)
	for idx := range normalizedHecate {
		if normalizedHecate[idx].ReadBackend != "hecate" {
			t.Fatalf("hecate memory candidate[%d] read_backend = %q, want hecate", idx, normalizedHecate[idx].ReadBackend)
		}
		if normalizedCairnline[idx].ReadBackend != "cairnline" {
			t.Fatalf("cairnline memory candidate[%d] read_backend = %q, want cairnline", idx, normalizedCairnline[idx].ReadBackend)
		}
		normalizedHecate[idx].ReadBackend = ""
		normalizedCairnline[idx].ReadBackend = ""
	}
	if !reflect.DeepEqual(normalizedHecate, normalizedCairnline) {
		t.Fatalf("memory candidate projection mismatch\nhecate:   %+v\ncairnline: %+v", normalizedHecate, normalizedCairnline)
	}
}

func getMirroredCairnlineMemoryEntryForTest(t *testing.T, handler *Handler, projectID, memoryID string) cairnline.MemoryEntry {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	entry, err := service.GetMemoryEntry(t.Context(), projectID, memoryID)
	if err != nil {
		t.Fatalf("GetMemoryEntry(%q, %q): %v", projectID, memoryID, err)
	}
	return entry
}

func assertCairnlineMemoryEntryMissingForTest(t *testing.T, handler *Handler, projectID, memoryID string) {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	if _, err := service.GetMemoryEntry(t.Context(), projectID, memoryID); !errors.Is(err, cairnline.ErrNotFound) {
		t.Fatalf("GetMemoryEntry(%q, %q) error = %v, want Cairnline ErrNotFound", projectID, memoryID, err)
	}
}

func getMirroredCairnlineMemoryCandidateForTest(t *testing.T, handler *Handler, projectID, candidateID string) cairnline.MemoryCandidate {
	t.Helper()
	service, store, err := cairnline.NewSQLiteService(t.Context(), handler.cairnlineEmbeddedDatabasePath())
	if err != nil {
		t.Fatalf("open Cairnline mirror: %v", err)
	}
	defer store.Close()
	candidate, err := service.GetMemoryCandidate(t.Context(), projectID, candidateID)
	if err != nil {
		t.Fatalf("GetMemoryCandidate(%q, %q): %v", projectID, candidateID, err)
	}
	return candidate
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
	if rec.Code != http.StatusOK {
		t.Fatalf("delete project status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var deleted ProjectDeleteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &deleted); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if deleted.Data.ProjectID != project.Data.ID || deleted.Data.MemoryEntriesDeleted != 1 {
		t.Fatalf("delete response = %+v, want project id and 1 memory entry", deleted)
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

type existingProjectMemoryShadowStore struct {
	entry   memory.Entry
	created bool
	updated bool
	deleted bool
}

func (s *existingProjectMemoryShadowStore) Backend() string {
	return "memory"
}

func (s *existingProjectMemoryShadowStore) Create(context.Context, memory.Entry) (memory.Entry, error) {
	s.created = true
	return memory.Entry{}, errors.New("create should not be called for existing shadows")
}

func (s *existingProjectMemoryShadowStore) Get(context.Context, string, string) (memory.Entry, bool, error) {
	return s.entry, true, nil
}

func (s *existingProjectMemoryShadowStore) List(context.Context, memory.Filter) ([]memory.Entry, error) {
	return nil, nil
}

func (s *existingProjectMemoryShadowStore) Update(_ context.Context, _ string, _ string, update func(*memory.Entry)) (memory.Entry, error) {
	s.updated = true
	if update != nil {
		update(&s.entry)
	}
	return s.entry, nil
}

func (s *existingProjectMemoryShadowStore) Delete(context.Context, string, string) error {
	s.deleted = true
	return nil
}

func (s *existingProjectMemoryShadowStore) DeleteByProjectID(context.Context, string) (int, error) {
	return 0, nil
}
