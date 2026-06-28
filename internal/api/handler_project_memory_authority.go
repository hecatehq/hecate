package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/memory"
)

func applyProjectMemoryUpdate(item *memory.Entry, req updateProjectMemoryRequest) {
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
}

func (h *Handler) projectMemoryWritesUseCairnlineAuthority() bool {
	return h != nil &&
		h.config.ProjectsCoordinationBackend() == "cairnline" &&
		h.config.ProjectsCairnlineWriteAuthorityEnabled("project-memory")
}

func (h *Handler) createProjectMemoryEntryWithCairnlineAuthority(ctx context.Context, projectID string, entry memory.Entry) (memory.Entry, error) {
	var created cairnline.MemoryEntry
	err := h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		if err := h.seedProjectMetadataForCairnlineMemoryAuthority(ctx, service, projectID); err != nil {
			return err
		}
		item, err := service.CreateMemoryEntry(ctx, cairnlinebridge.MemoryEntry(entry))
		if err != nil {
			return err
		}
		if item.Enabled != entry.Enabled {
			item.Enabled = entry.Enabled
			item, err = service.UpdateMemoryEntry(ctx, item)
			if err != nil {
				return err
			}
		}
		created = item
		return nil
	})
	if err != nil {
		return memory.Entry{}, err
	}
	return projectMemoryFromCairnline(created), nil
}

func (h *Handler) updateProjectMemoryEntryWithCairnlineAuthority(ctx context.Context, projectID, memoryID string, update func(*memory.Entry)) (memory.Entry, error) {
	var updated cairnline.MemoryEntry
	err := h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		existing, err := service.GetMemoryEntry(ctx, projectID, memoryID)
		if err != nil {
			return err
		}
		entry := projectMemoryFromCairnline(existing)
		if update != nil {
			update(&entry)
		}
		entry = normalizeProjectMemoryEntryForCairnlineAuthority(entry)
		entry.UpdatedAt = time.Time{}
		item, err := service.UpdateMemoryEntry(ctx, cairnlinebridge.MemoryEntry(entry))
		if err != nil {
			return err
		}
		updated = item
		return nil
	})
	if err != nil {
		return memory.Entry{}, err
	}
	return projectMemoryFromCairnline(updated), nil
}

func (h *Handler) deleteProjectMemoryEntryWithCairnlineAuthority(ctx context.Context, projectID, memoryID string) error {
	return h.withCairnlineEmbeddedMirrorService(ctx, func(service *cairnline.Service) error {
		return service.DeleteMemoryEntry(ctx, projectID, memoryID)
	})
}

func (h *Handler) seedProjectMetadataForCairnlineMemoryAuthority(ctx context.Context, service *cairnline.Service, projectID string) error {
	project, ok := h.projectForCairnlineMirror(ctx, "project_memory_authority", projectID)
	if !ok {
		return errors.Join(cairnline.ErrNotFound, errors.New("project not found for Cairnline memory authority"))
	}
	_, err := cairnlinebridge.UpsertProjectMetadata(ctx, service, project)
	return err
}

func normalizeProjectMemoryEntryForCairnlineAuthority(entry memory.Entry) memory.Entry {
	entry.ID = strings.TrimSpace(entry.ID)
	entry.Scope = strings.TrimSpace(entry.Scope)
	if entry.Scope == "" {
		entry.Scope = memory.ScopeProject
	}
	entry.ProjectID = strings.TrimSpace(entry.ProjectID)
	entry.Title = strings.TrimSpace(entry.Title)
	entry.Body = strings.TrimSpace(entry.Body)
	entry.TrustLabel = strings.TrimSpace(entry.TrustLabel)
	if entry.TrustLabel == "" {
		entry.TrustLabel = memory.TrustLabelOperatorMemory
	}
	entry.SourceKind = strings.TrimSpace(entry.SourceKind)
	if entry.SourceKind == "" {
		entry.SourceKind = memory.SourceKindOperator
	}
	entry.SourceID = strings.TrimSpace(entry.SourceID)
	return entry
}

func (h *Handler) shadowProjectMemoryEntryToHecate(ctx context.Context, operation string, entry memory.Entry) {
	if h == nil || h.memory == nil {
		return
	}
	if err := h.memory.Delete(ctx, entry.ProjectID, entry.ID); err != nil && !errors.Is(err, memory.ErrNotFound) {
		h.logProjectMemoryShadowError(ctx, operation, entry.ProjectID, entry.ID, err)
		return
	}
	if _, err := h.memory.Create(ctx, entry); err != nil {
		h.logProjectMemoryShadowError(ctx, operation, entry.ProjectID, entry.ID, err)
	}
}

func (h *Handler) shadowProjectMemoryEntryDeleteToHecate(ctx context.Context, operation, projectID, memoryID string) {
	if h == nil || h.memory == nil {
		return
	}
	if err := h.memory.Delete(ctx, projectID, memoryID); err != nil && !errors.Is(err, memory.ErrNotFound) {
		h.logProjectMemoryShadowError(ctx, operation, projectID, memoryID, err)
	}
}

func (h *Handler) logProjectMemoryShadowError(ctx context.Context, operation, projectID, memoryID string, err error) {
	if err == nil {
		return
	}
	if h == nil {
		return
	}
	logger := h.logger
	if logger == nil {
		return
	}
	logger.WarnContext(ctx, "project memory Hecate shadow failed", "operation", operation, "project_id", projectID, "memory_id", memoryID, "error", err)
}

func writeProjectMemoryMutationError(w http.ResponseWriter, err error, notFoundMessage string) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, memory.ErrInvalid) || errors.Is(err, cairnline.ErrInvalid) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return true
	}
	if errors.Is(err, memory.ErrNotFound) || errors.Is(err, cairnline.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, notFoundMessage)
		return true
	}
	if errors.Is(err, memory.ErrAlreadyExists) || errors.Is(err, memory.ErrConflict) || errors.Is(err, cairnline.ErrConflict) {
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
		return true
	}
	WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
	return true
}
