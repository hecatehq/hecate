package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/hecatehq/hecate/internal/memory"
)

type createProjectMemoryRequest struct {
	Title      string `json:"title"`
	Body       string `json:"body"`
	TrustLabel string `json:"trust_label,omitempty"`
	SourceKind string `json:"source_kind,omitempty"`
	SourceID   string `json:"source_id,omitempty"`
	Enabled    *bool  `json:"enabled,omitempty"`
}

type candidateSourceRefRequest struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	URL   string `json:"url,omitempty"`
}

type createProjectMemoryCandidateRequest struct {
	Title               string                      `json:"title"`
	Body                string                      `json:"body"`
	SuggestedKind       string                      `json:"suggested_kind,omitempty"`
	SuggestedTrustLabel string                      `json:"suggested_trust_label,omitempty"`
	SuggestedSourceKind string                      `json:"suggested_source_kind,omitempty"`
	SuggestedSourceID   string                      `json:"suggested_source_id,omitempty"`
	SourceRefs          []candidateSourceRefRequest `json:"source_refs,omitempty"`
}

type promoteProjectMemoryCandidateRequest struct {
	Title      *string `json:"title,omitempty"`
	Body       *string `json:"body,omitempty"`
	TrustLabel *string `json:"trust_label,omitempty"`
	SourceKind *string `json:"source_kind,omitempty"`
	SourceID   *string `json:"source_id,omitempty"`
	Enabled    *bool   `json:"enabled,omitempty"`
}

type rejectProjectMemoryCandidateRequest struct {
	Reason string `json:"reason,omitempty"`
}

type updateProjectMemoryRequest struct {
	Title      *string `json:"title,omitempty"`
	Body       *string `json:"body,omitempty"`
	TrustLabel *string `json:"trust_label,omitempty"`
	SourceKind *string `json:"source_kind,omitempty"`
	SourceID   *string `json:"source_id,omitempty"`
	Enabled    *bool   `json:"enabled,omitempty"`
}

func (h *Handler) HandleProjectMemoryEntries(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.PathValue("id"))
	if !h.requireProjectExists(w, r, projectID) {
		return
	}
	if !h.requireProjectMemoryStore(w) {
		return
	}
	includeDisabled := requestBool(r, "include_disabled")
	items, err := h.memory.List(r.Context(), memory.Filter{
		ProjectID:       projectID,
		IncludeDisabled: includeDisabled,
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	data := make([]ProjectMemoryResponseItem, 0, len(items))
	for _, item := range items {
		data = append(data, renderProjectMemory(item))
	}
	WriteJSON(w, http.StatusOK, ProjectMemoryListResponse{Object: "project_memory", Data: data})
}

func (h *Handler) HandleCreateProjectMemoryEntry(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.PathValue("id"))
	if !h.requireProjectExists(w, r, projectID) {
		return
	}
	if !h.requireProjectMemoryStore(w) {
		return
	}
	var req createProjectMemoryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	entry := memory.Entry{
		ID:         newOpaqueTaskResourceID("mem"),
		Scope:      memory.ScopeProject,
		ProjectID:  projectID,
		Title:      req.Title,
		Body:       req.Body,
		TrustLabel: req.TrustLabel,
		SourceKind: req.SourceKind,
		SourceID:   req.SourceID,
		Enabled:    enabled,
	}
	created, err := h.memory.Create(r.Context(), entry)
	if errors.Is(err, memory.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if errors.Is(err, memory.ErrAlreadyExists) {
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, ProjectMemoryResponse{Object: "project_memory_entry", Data: renderProjectMemory(created)})
}

func (h *Handler) HandleProjectMemoryCandidates(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.PathValue("id"))
	if !h.requireProjectExists(w, r, projectID) {
		return
	}
	candidateStore, ok := h.requireProjectMemoryCandidateStore(w)
	if !ok {
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" && !requestBool(r, "include_resolved") {
		status = memory.CandidateStatusPending
	}
	items, err := candidateStore.ListCandidates(r.Context(), memory.CandidateFilter{
		ProjectID: projectID,
		Status:    status,
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	data := make([]ProjectMemoryCandidateResponseItem, 0, len(items))
	for _, item := range items {
		data = append(data, renderProjectMemoryCandidate(item))
	}
	WriteJSON(w, http.StatusOK, ProjectMemoryCandidateListResponse{Object: "project_memory_candidates", Data: data})
}

func (h *Handler) HandleCreateProjectMemoryCandidate(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.PathValue("id"))
	if !h.requireProjectExists(w, r, projectID) {
		return
	}
	candidateStore, ok := h.requireProjectMemoryCandidateStore(w)
	if !ok {
		return
	}
	var req createProjectMemoryCandidateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	candidate := memory.Candidate{
		ID:                  newOpaqueTaskResourceID("memcand"),
		ProjectID:           projectID,
		Title:               req.Title,
		Body:                req.Body,
		SuggestedKind:       req.SuggestedKind,
		SuggestedTrustLabel: req.SuggestedTrustLabel,
		SuggestedSourceKind: req.SuggestedSourceKind,
		SuggestedSourceID:   req.SuggestedSourceID,
		SourceRefs:          candidateSourceRefsFromRequest(req.SourceRefs),
		Status:              memory.CandidateStatusPending,
	}
	created, err := candidateStore.CreateCandidate(r.Context(), candidate)
	if errors.Is(err, memory.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if errors.Is(err, memory.ErrAlreadyExists) {
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, ProjectMemoryCandidateResponse{Object: "project_memory_candidate", Data: renderProjectMemoryCandidate(created)})
}

func (h *Handler) HandlePromoteProjectMemoryCandidate(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.PathValue("id"))
	if !h.requireProjectExists(w, r, projectID) {
		return
	}
	candidateStore, ok := h.requireProjectMemoryCandidateStore(w)
	if !ok {
		return
	}
	var req promoteProjectMemoryCandidateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	candidateID := r.PathValue("candidate_id")
	candidate, ok, err := candidateStore.GetCandidate(r.Context(), projectID, candidateID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project memory candidate not found")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	entry := memory.Entry{
		ID:         newOpaqueTaskResourceID("mem"),
		Scope:      memory.ScopeProject,
		ProjectID:  projectID,
		Title:      candidate.Title,
		Body:       candidate.Body,
		TrustLabel: candidate.SuggestedTrustLabel,
		SourceKind: candidate.SuggestedSourceKind,
		SourceID:   candidate.SuggestedSourceID,
		Enabled:    enabled,
	}
	if req.Title != nil {
		entry.Title = *req.Title
	}
	if req.Body != nil {
		entry.Body = *req.Body
	}
	if req.TrustLabel != nil {
		entry.TrustLabel = *req.TrustLabel
	}
	if req.SourceKind != nil {
		entry.SourceKind = *req.SourceKind
	}
	if req.SourceID != nil {
		entry.SourceID = *req.SourceID
	}
	updated, _, err := candidateStore.PromoteCandidate(r.Context(), projectID, candidateID, entry)
	if errors.Is(err, memory.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if errors.Is(err, memory.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project memory candidate not found")
		return
	}
	if errors.Is(err, memory.ErrConflict) {
		WriteError(w, http.StatusConflict, errCodeConflict, "project memory candidate is already resolved")
		return
	}
	if errors.Is(err, memory.ErrAlreadyExists) {
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectMemoryCandidateResponse{Object: "project_memory_candidate", Data: renderProjectMemoryCandidate(updated)})
}

func (h *Handler) HandleRejectProjectMemoryCandidate(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.PathValue("id"))
	if !h.requireProjectExists(w, r, projectID) {
		return
	}
	candidateStore, ok := h.requireProjectMemoryCandidateStore(w)
	if !ok {
		return
	}
	var req rejectProjectMemoryCandidateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	candidateID := r.PathValue("candidate_id")
	candidate, ok, err := candidateStore.GetCandidate(r.Context(), projectID, candidateID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project memory candidate not found")
		return
	}
	if candidate.Status != memory.CandidateStatusPending {
		WriteError(w, http.StatusConflict, errCodeConflict, "project memory candidate is already resolved")
		return
	}
	updated, err := candidateStore.UpdateCandidate(r.Context(), projectID, candidateID, func(item *memory.Candidate) {
		item.Status = memory.CandidateStatusRejected
		item.StatusReason = req.Reason
		item.PromotedMemoryID = ""
	})
	if errors.Is(err, memory.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectMemoryCandidateResponse{Object: "project_memory_candidate", Data: renderProjectMemoryCandidate(updated)})
}

func (h *Handler) HandleUpdateProjectMemoryEntry(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.PathValue("id"))
	if !h.requireProjectExists(w, r, projectID) {
		return
	}
	if !h.requireProjectMemoryStore(w) {
		return
	}
	var req updateProjectMemoryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	entry, err := h.memory.Update(r.Context(), projectID, r.PathValue("memory_id"), func(item *memory.Entry) {
		if req.Title != nil {
			item.Title = *req.Title
		}
		if req.Body != nil {
			item.Body = *req.Body
		}
		if req.TrustLabel != nil {
			item.TrustLabel = *req.TrustLabel
		}
		if req.SourceKind != nil {
			item.SourceKind = *req.SourceKind
		}
		if req.SourceID != nil {
			item.SourceID = *req.SourceID
		}
		if req.Enabled != nil {
			item.Enabled = *req.Enabled
		}
	})
	if errors.Is(err, memory.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project memory entry not found")
		return
	}
	if errors.Is(err, memory.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectMemoryResponse{Object: "project_memory_entry", Data: renderProjectMemory(entry)})
}

func (h *Handler) HandleDeleteProjectMemoryEntry(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.PathValue("id"))
	if !h.requireProjectExists(w, r, projectID) {
		return
	}
	if !h.requireProjectMemoryStore(w) {
		return
	}
	err := h.memory.Delete(r.Context(), projectID, r.PathValue("memory_id"))
	if errors.Is(err, memory.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project memory entry not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) requireProjectExists(w http.ResponseWriter, r *http.Request, projectID string) bool {
	if h.projects == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "project store is not configured")
		return false
	}
	if projectID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "project id is required")
		return false
	}
	_, ok, err := h.projects.Get(r.Context(), projectID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return false
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return false
	}
	return true
}

func (h *Handler) requireProjectMemoryStore(w http.ResponseWriter) bool {
	if h.memory == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "project memory store is not configured")
		return false
	}
	return true
}

func (h *Handler) requireProjectMemoryCandidateStore(w http.ResponseWriter) (memory.CandidateStore, bool) {
	if h.memoryCandidates == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "project memory candidate store is not configured")
		return nil, false
	}
	return h.memoryCandidates, true
}

func renderProjectMemory(entry memory.Entry) ProjectMemoryResponseItem {
	return ProjectMemoryResponseItem{
		ID:         entry.ID,
		Scope:      entry.Scope,
		ProjectID:  entry.ProjectID,
		Title:      entry.Title,
		Body:       entry.Body,
		TrustLabel: entry.TrustLabel,
		SourceKind: entry.SourceKind,
		SourceID:   entry.SourceID,
		Enabled:    entry.Enabled,
		CreatedAt:  formatOptionalTime(entry.CreatedAt),
		UpdatedAt:  formatOptionalTime(entry.UpdatedAt),
	}
}

func renderProjectMemoryCandidate(candidate memory.Candidate) ProjectMemoryCandidateResponseItem {
	return ProjectMemoryCandidateResponseItem{
		ID:                  candidate.ID,
		ProjectID:           candidate.ProjectID,
		Title:               candidate.Title,
		Body:                candidate.Body,
		SuggestedKind:       candidate.SuggestedKind,
		SuggestedTrustLabel: candidate.SuggestedTrustLabel,
		SuggestedSourceKind: candidate.SuggestedSourceKind,
		SuggestedSourceID:   candidate.SuggestedSourceID,
		SourceRefs:          renderProjectMemoryCandidateSourceRefs(candidate.SourceRefs),
		Status:              candidate.Status,
		StatusReason:        candidate.StatusReason,
		PromotedMemoryID:    candidate.PromotedMemoryID,
		CreatedAt:           formatOptionalTime(candidate.CreatedAt),
		UpdatedAt:           formatOptionalTime(candidate.UpdatedAt),
	}
}

func candidateSourceRefsFromRequest(refs []candidateSourceRefRequest) []memory.CandidateSourceRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]memory.CandidateSourceRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, memory.CandidateSourceRef{
			Kind:  ref.Kind,
			ID:    ref.ID,
			Title: ref.Title,
			URL:   ref.URL,
		})
	}
	return out
}

func renderProjectMemoryCandidateSourceRefs(refs []memory.CandidateSourceRef) []ProjectMemoryCandidateSourceRefResponseItem {
	if len(refs) == 0 {
		return nil
	}
	out := make([]ProjectMemoryCandidateSourceRefResponseItem, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ProjectMemoryCandidateSourceRefResponseItem{
			Kind:  ref.Kind,
			ID:    ref.ID,
			Title: ref.Title,
			URL:   ref.URL,
		})
	}
	return out
}

func requestBool(r *http.Request, key string) bool {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	return raw == "1" || strings.EqualFold(raw, "true")
}
