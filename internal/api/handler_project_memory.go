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
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, ProjectMemoryResponse{Object: "project_memory_entry", Data: renderProjectMemory(created)})
}

func (h *Handler) HandleUpdateProjectMemoryEntry(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.PathValue("id"))
	if !h.requireProjectExists(w, r, projectID) {
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
	if h.projects == nil || h.memory == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "project memory store is not configured")
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

func requestBool(r *http.Request, key string) bool {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	return raw == "1" || strings.EqualFold(raw, "true")
}
