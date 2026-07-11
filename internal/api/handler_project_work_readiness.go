package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
)

type ProjectWorkItemReadinessEnvelope struct {
	Object string                           `json:"object"`
	Data   ProjectWorkItemReadinessResponse `json:"data"`
}

type ProjectWorkItemReadinessResponse struct {
	ProjectID                    string                                  `json:"project_id"`
	WorkItemID                   string                                  `json:"work_item_id"`
	ReadBackend                  string                                  `json:"read_backend,omitempty"`
	Ready                        bool                                    `json:"ready"`
	Status                       string                                  `json:"status"`
	Title                        string                                  `json:"title"`
	Detail                       string                                  `json:"detail"`
	Blockers                     []string                                `json:"blockers"`
	Warnings                     []string                                `json:"warnings"`
	AssignmentCount              int                                     `json:"assignment_count"`
	CompletedAssignments         int                                     `json:"completed_assignments"`
	ReviewFollowUpCount          int                                     `json:"review_follow_up_count"`
	ReviewFollowUpArtifactIDs    []string                                `json:"review_follow_up_artifact_ids,omitempty"`
	ReviewFollowUps              []ProjectWorkItemReviewFollowUpResponse `json:"review_follow_ups,omitempty"`
	MissingEvidenceAssignmentIDs []string                                `json:"missing_evidence_assignment_ids,omitempty"`
}

type ProjectWorkItemReviewFollowUpResponse struct {
	ArtifactID           string `json:"artifact_id"`
	Title                string `json:"title"`
	Status               string `json:"status"`
	Blocker              string `json:"blocker,omitempty"`
	ReviewedAssignmentID string `json:"reviewed_assignment_id,omitempty"`
	ReviewVerdict        string `json:"review_verdict,omitempty"`
	ReviewRisk           string `json:"review_risk,omitempty"`
}

func (h *Handler) HandleProjectWorkItemReadiness(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	strictEmbeddedRead := h.projectReadRoutesUseCairnlineReadModel() && h.requiresEmbeddedCairnlineProjectReads()
	if !strictEmbeddedRead && !h.requireProject(w, r, projectID) {
		return
	}
	readiness, err := h.renderProjectWorkItemReadiness(r.Context(), projectID, r.PathValue("work_item_id"))
	if err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
			return
		}
		if errors.Is(err, projectwork.ErrNotFound) {
			WriteError(w, http.StatusNotFound, errCodeNotFound, "work item not found")
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectWorkItemReadinessEnvelope{Object: "project_work_item_readiness", Data: readiness})
}

func (h *Handler) renderProjectWorkItemReadiness(ctx context.Context, projectID, workItemID string) (ProjectWorkItemReadinessResponse, error) {
	if h.projectReadRoutesUseCairnlineReadModel() {
		return h.renderCairnlineProjectWorkItemReadiness(ctx, projectID, workItemID)
	}
	readiness, err := h.projectWorkApplication().WorkItemReadiness(ctx, projectID, workItemID)
	if err != nil {
		return ProjectWorkItemReadinessResponse{}, err
	}
	return renderProjectWorkItemReadiness(readiness), nil
}

func renderProjectWorkItemReadiness(readiness projectworkapp.WorkItemReadiness) ProjectWorkItemReadinessResponse {
	return ProjectWorkItemReadinessResponse{
		ProjectID:                    readiness.ProjectID,
		WorkItemID:                   readiness.WorkItemID,
		ReadBackend:                  "hecate",
		Ready:                        readiness.Ready,
		Status:                       readiness.Status,
		Title:                        readiness.Title,
		Detail:                       readiness.Detail,
		Blockers:                     append([]string(nil), readiness.Blockers...),
		Warnings:                     append([]string(nil), readiness.Warnings...),
		AssignmentCount:              readiness.AssignmentCount,
		CompletedAssignments:         readiness.CompletedAssignments,
		ReviewFollowUpCount:          readiness.ReviewFollowUpCount,
		ReviewFollowUpArtifactIDs:    append([]string(nil), readiness.ReviewFollowUpArtifactIDs...),
		ReviewFollowUps:              renderProjectWorkItemReviewFollowUps(readiness.ReviewFollowUps),
		MissingEvidenceAssignmentIDs: append([]string(nil), readiness.MissingEvidenceAssignmentIDs...),
	}
}

func renderProjectWorkItemReviewFollowUps(items []projectworkapp.ReviewFollowUpReadiness) []ProjectWorkItemReviewFollowUpResponse {
	if len(items) == 0 {
		return nil
	}
	out := make([]ProjectWorkItemReviewFollowUpResponse, 0, len(items))
	for _, item := range items {
		out = append(out, ProjectWorkItemReviewFollowUpResponse{
			ArtifactID:           item.ArtifactID,
			Title:                item.Title,
			Status:               item.Status,
			Blocker:              item.Blocker,
			ReviewedAssignmentID: item.ReviewedAssignmentID,
			ReviewVerdict:        item.ReviewVerdict,
			ReviewRisk:           item.ReviewRisk,
		})
	}
	return out
}
