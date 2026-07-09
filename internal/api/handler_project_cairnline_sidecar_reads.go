package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/cairnline"
	mcpclient "github.com/hecatehq/hecate/internal/mcp/client"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

var errProjectCairnlineSidecarReadFailed = errors.New("cairnline sidecar project read failed")

type ProjectCairnlineSidecarSkillItem struct {
	ID                     string                                       `json:"id"`
	ProjectID              string                                       `json:"project_id,omitempty"`
	Title                  string                                       `json:"title,omitempty"`
	Description            string                                       `json:"description,omitempty"`
	Path                   string                                       `json:"path,omitempty"`
	RootID                 string                                       `json:"root_id,omitempty"`
	Format                 string                                       `json:"format,omitempty"`
	SuggestedTools         []string                                     `json:"suggested_tools,omitempty"`
	RequiredPermissions    *ProjectSkillRequiredPermissionsResponseItem `json:"required_permissions,omitempty"`
	Enabled                bool                                         `json:"enabled"`
	Status                 string                                       `json:"status,omitempty"`
	TrustLabel             string                                       `json:"trust_label,omitempty"`
	SourceContextSourceIDs []string                                     `json:"source_context_source_ids,omitempty"`
	SourceRefs             []string                                     `json:"source_refs,omitempty"`
	Warnings               []string                                     `json:"warnings,omitempty"`
	DiscoveredAt           string                                       `json:"discovered_at,omitempty"`
	CreatedAt              string                                       `json:"created_at,omitempty"`
	UpdatedAt              string                                       `json:"updated_at,omitempty"`
}

func (h *Handler) projectCairnlineSidecarReadRoutesEnabled() bool {
	return h != nil &&
		h.config.ProjectsCoordinationBackend() == "cairnline" &&
		h.projectCairnlineConnectorMode() == "sidecar" &&
		h.config.ProjectsCairnlineReadSource() == "sidecar"
}

func (h *Handler) renderCairnlineSidecarProjects(ctx context.Context) ([]ProjectResponseItem, error) {
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "projects.list", map[string]string{})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, projectCairnlineSidecarReadFailure("projects.list returned a tool-level error: " + strings.TrimSpace(result.Text))
	}
	projects, structuredReady, structuredErr := projectCairnlineSidecarStructuredProjects(result.Result.StructuredContent)
	if structuredErr != nil {
		return nil, projectCairnlineSidecarReadFailure("projects.list structuredContent parse failed: " + structuredErr.Error())
	}
	if !structuredReady {
		return nil, projectCairnlineSidecarReadFailure("projects.list did not return typed structuredContent")
	}
	data := make([]ProjectResponseItem, 0, len(projects))
	for _, project := range projects {
		converted := projectFromCairnlineSidecar(project)
		converted, err = h.projectWithHecateRuntimeOverlay(ctx, converted)
		if err != nil {
			return nil, err
		}
		data = append(data, renderProjectWithBackend(converted, "cairnline"))
	}
	return data, nil
}

func (h *Handler) renderCairnlineSidecarProject(ctx context.Context, projectID string) (*ProjectResponseItem, error) {
	project, ok, err := h.cairnlineSidecarProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	converted := projectFromCairnlineSidecar(project)
	converted, err = h.projectWithHecateRuntimeOverlay(ctx, converted)
	if err != nil {
		return nil, err
	}
	rendered := renderProjectWithBackend(converted, "cairnline")
	return &rendered, nil
}

func (h *Handler) cairnlineSidecarProject(ctx context.Context, projectID string) (ProjectCairnlineSidecarProjectItem, bool, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return ProjectCairnlineSidecarProjectItem{}, false, nil
	}
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "projects.get", map[string]string{"id": projectID})
	if err != nil {
		return ProjectCairnlineSidecarProjectItem{}, false, err
	}
	if result.IsError {
		if projectCairnlineSidecarToolErrorIsNotFound(result) {
			return ProjectCairnlineSidecarProjectItem{}, false, nil
		}
		return ProjectCairnlineSidecarProjectItem{}, false, projectCairnlineSidecarReadFailure("projects.get returned a tool-level error: " + strings.TrimSpace(result.Text))
	}
	project, structuredReady, structuredErr := projectCairnlineSidecarStructuredProject(result.Result.StructuredContent)
	if structuredErr != nil {
		return ProjectCairnlineSidecarProjectItem{}, false, projectCairnlineSidecarReadFailure("projects.get structuredContent parse failed: " + structuredErr.Error())
	}
	if !structuredReady {
		return ProjectCairnlineSidecarProjectItem{}, false, projectCairnlineSidecarReadFailure("projects.get did not return typed structuredContent")
	}
	return project, true, nil
}

func (h *Handler) callProjectCairnlineSidecarProjectReadTool(ctx context.Context, toolName string, args any) (*orchestrator.CachedMCPToolCallResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if h == nil {
		return nil, projectCairnlineSidecarReadFailure("handler is not configured")
	}
	cfg, _, timeout := h.projectCairnlineSidecarMCPConfig()
	rawArgs, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	cache := h.projectCairnlineSidecarMCPClientCache()
	readCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := orchestrator.CallCachedMCPServerTool(readCtx, cfg, h.secretCipher, cache, toolName, rawArgs)
	if err != nil {
		if projectCairnlineSidecarReadErrShouldEvict(err) {
			cache.Evict(mcpclient.ServerConfig{
				Name:    cfg.Name,
				Command: cfg.Command,
				Args:    cfg.Args,
				Env:     cfg.Env,
				URL:     cfg.URL,
				Headers: cfg.Headers,
			})
			result, err = orchestrator.CallCachedMCPServerTool(readCtx, cfg, h.secretCipher, cache, toolName, rawArgs)
		}
	}
	if err != nil {
		return nil, projectCairnlineSidecarReadFailure(err.Error())
	}
	return result, nil
}

func (h *Handler) cairnlineSidecarProjectActivity(ctx context.Context, projectID string) (cairnline.ProjectActivity, error) {
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "projects.activity", map[string]string{"project_id": strings.TrimSpace(projectID)})
	if err != nil {
		return cairnline.ProjectActivity{}, err
	}
	if result.IsError {
		return cairnline.ProjectActivity{}, projectCairnlineSidecarReadFailure("projects.activity returned a tool-level error: " + strings.TrimSpace(result.Text))
	}
	activity, structuredReady, structuredErr := projectCairnlineSidecarStructuredProjectActivity(result.Result.StructuredContent)
	if structuredErr != nil {
		return cairnline.ProjectActivity{}, projectCairnlineSidecarReadFailure("projects.activity structuredContent parse failed: " + structuredErr.Error())
	}
	if !structuredReady {
		return cairnline.ProjectActivity{}, projectCairnlineSidecarReadFailure("projects.activity did not return typed structuredContent")
	}
	return activity, nil
}

func (h *Handler) cairnlineSidecarProjectOperationsBrief(ctx context.Context, projectID string) (cairnline.ProjectOperationsBrief, error) {
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "projects.operations_brief", map[string]string{"project_id": strings.TrimSpace(projectID)})
	if err != nil {
		return cairnline.ProjectOperationsBrief{}, err
	}
	if result.IsError {
		return cairnline.ProjectOperationsBrief{}, projectCairnlineSidecarReadFailure("projects.operations_brief returned a tool-level error: " + strings.TrimSpace(result.Text))
	}
	brief, structuredReady, structuredErr := projectCairnlineSidecarStructuredProjectOperationsBrief(result.Result.StructuredContent)
	if structuredErr != nil {
		return cairnline.ProjectOperationsBrief{}, projectCairnlineSidecarReadFailure("projects.operations_brief structuredContent parse failed: " + structuredErr.Error())
	}
	if !structuredReady {
		return cairnline.ProjectOperationsBrief{}, projectCairnlineSidecarReadFailure("projects.operations_brief did not return typed structuredContent")
	}
	return brief, nil
}

func (h *Handler) cairnlineSidecarAssignmentLaunchPacket(ctx context.Context, projectID, assignmentID string) (cairnline.AssignmentLaunchPacket, bool, error) {
	projectID = strings.TrimSpace(projectID)
	assignmentID = strings.TrimSpace(assignmentID)
	if projectID == "" || assignmentID == "" {
		return cairnline.AssignmentLaunchPacket{}, false, nil
	}
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "assignments.launch_packet", map[string]string{
		"project_id":    projectID,
		"assignment_id": assignmentID,
	})
	if err != nil {
		return cairnline.AssignmentLaunchPacket{}, false, err
	}
	if result.IsError {
		if projectCairnlineSidecarToolErrorIsNotFound(result) {
			return cairnline.AssignmentLaunchPacket{}, false, nil
		}
		return cairnline.AssignmentLaunchPacket{}, false, projectCairnlineSidecarReadFailure("assignments.launch_packet returned a tool-level error: " + strings.TrimSpace(result.Text))
	}
	packet, structuredReady, structuredErr := projectCairnlineSidecarStructuredAssignmentLaunchPacket(result.Result.StructuredContent)
	if structuredErr != nil {
		return cairnline.AssignmentLaunchPacket{}, false, projectCairnlineSidecarReadFailure("assignments.launch_packet structuredContent parse failed: " + structuredErr.Error())
	}
	if !structuredReady {
		return cairnline.AssignmentLaunchPacket{}, false, projectCairnlineSidecarReadFailure("assignments.launch_packet did not return typed structuredContent")
	}
	return packet, true, nil
}

func (h *Handler) cairnlineSidecarAssignmentContext(ctx context.Context, projectID, assignmentID string) (cairnline.AssignmentContext, bool, error) {
	projectID = strings.TrimSpace(projectID)
	assignmentID = strings.TrimSpace(assignmentID)
	if projectID == "" || assignmentID == "" {
		return cairnline.AssignmentContext{}, false, nil
	}
	result, err := h.callProjectCairnlineSidecarProjectReadTool(ctx, "assignments.context", map[string]string{
		"project_id":    projectID,
		"assignment_id": assignmentID,
	})
	if err != nil {
		return cairnline.AssignmentContext{}, false, err
	}
	if result.IsError {
		if projectCairnlineSidecarToolErrorIsNotFound(result) {
			return cairnline.AssignmentContext{}, false, nil
		}
		return cairnline.AssignmentContext{}, false, projectCairnlineSidecarReadFailure("assignments.context returned a tool-level error: " + strings.TrimSpace(result.Text))
	}
	packet, structuredReady, structuredErr := projectCairnlineSidecarStructuredAssignmentContext(result.Result.StructuredContent)
	if structuredErr != nil {
		return cairnline.AssignmentContext{}, false, projectCairnlineSidecarReadFailure("assignments.context structuredContent parse failed: " + structuredErr.Error())
	}
	if !structuredReady {
		return cairnline.AssignmentContext{}, false, projectCairnlineSidecarReadFailure("assignments.context did not return typed structuredContent")
	}
	return packet, true, nil
}

func projectFromCairnlineSidecar(project ProjectCairnlineSidecarProjectItem) projects.Project {
	return projects.Project{
		ID:             project.ID,
		Name:           project.Name,
		Description:    project.Description,
		Roots:          projectRootsFromCairnlineSidecar(project.Roots),
		ContextSources: projectContextSourcesFromCairnlineSidecar(project.ContextSources),
		DefaultRootID:  project.DefaultRootID,
		CreatedAt:      projectCairnlineSidecarTime(project.CreatedAt),
		UpdatedAt:      projectCairnlineSidecarTime(project.UpdatedAt),
	}
}

func projectRootsFromCairnlineSidecar(items []ProjectCairnlineSidecarRootItem) []projects.Root {
	out := make([]projects.Root, 0, len(items))
	for _, root := range items {
		out = append(out, projects.Root{
			ID:        root.ID,
			Path:      root.Path,
			Kind:      root.Kind,
			GitRemote: root.GitRemote,
			GitBranch: root.GitBranch,
			Active:    root.Active,
		})
	}
	return out
}

func projectContextSourcesFromCairnlineSidecar(items []ProjectCairnlineSidecarSourceItem) []projects.ContextSource {
	out := make([]projects.ContextSource, 0, len(items))
	for _, source := range items {
		out = append(out, projects.ContextSource{
			ID:             source.ID,
			Kind:           source.Kind,
			Title:          source.Title,
			Path:           source.Locator,
			Enabled:        source.Enabled,
			Format:         source.Format,
			Scope:          source.Scope,
			TrustLabel:     source.TrustLabel,
			SourceCategory: source.SourceCategory,
			Metadata:       cloneProjectContextMetadata(source.Metadata),
		})
	}
	return out
}

func projectRolesFromCairnlineSidecar(items []ProjectCairnlineSidecarRoleItem) []projectwork.AgentRoleProfile {
	out := make([]projectwork.AgentRoleProfile, 0, len(items))
	for _, item := range items {
		out = append(out, projectwork.AgentRoleProfile{
			ID:                item.ID,
			ProjectID:         item.ProjectID,
			Name:              item.Name,
			Description:       item.Description,
			Instructions:      item.Instructions,
			DefaultDriverKind: item.DefaultExecutionMode,
			SkillIDs:          append([]string(nil), item.DefaultSkillIDs...),
		})
	}
	return out
}

func projectWorkItemsFromCairnlineSidecar(items []ProjectCairnlineSidecarWorkItem) []projectwork.WorkItem {
	out := make([]projectwork.WorkItem, 0, len(items))
	for _, item := range items {
		out = append(out, projectwork.WorkItem{
			ID:              item.ID,
			ProjectID:       item.ProjectID,
			Title:           item.Title,
			Brief:           item.Brief,
			Status:          item.Status,
			Priority:        item.Priority,
			OwnerRoleID:     item.OwnerRoleID,
			RootID:          item.RootID,
			ReviewerRoleIDs: append([]string(nil), item.ReviewerRoleIDs...),
		})
	}
	return out
}

func projectAssignmentsFromCairnlineSidecar(items []ProjectCairnlineSidecarAssignmentItem) []projectwork.Assignment {
	out := make([]projectwork.Assignment, 0, len(items))
	for _, item := range items {
		out = append(out, projectwork.Assignment{
			ID:         item.ID,
			ProjectID:  item.ProjectID,
			WorkItemID: item.WorkItemID,
			RoleID:     item.RoleID,
			RootID:     item.RootID,
			DriverKind: projectWorkAssignmentDriverFromCairnline(item.ExecutionMode),
			Status:     projectWorkAssignmentStatusFromCairnline(item.Status),
			ExecutionRef: projectwork.NormalizeAssignmentExecutionRef(projectwork.AssignmentExecutionRef{
				ContextSnapshotID: item.ContextSnapshotID,
			}),
			CreatedAt:   projectCairnlineSidecarTime(item.CreatedAt),
			UpdatedAt:   projectCairnlineSidecarTime(item.UpdatedAt),
			StartedAt:   projectCairnlineSidecarTime(item.StartedAt),
			CompletedAt: projectCairnlineSidecarTime(item.CompletedAt),
		})
	}
	return out
}

func projectArtifactsFromCairnlineSidecar(artifacts []ProjectCairnlineSidecarArtifactItem, evidence []ProjectCairnlineSidecarEvidenceItem, reviews []ProjectCairnlineSidecarReviewItem) []projectwork.CollaborationArtifact {
	out := make([]projectwork.CollaborationArtifact, 0, len(artifacts))
	for _, item := range artifacts {
		out = append(out, projectGenericArtifactFromCairnlineSidecar(item))
	}
	for _, item := range evidence {
		out = append(out, projectEvidenceFromCairnlineSidecar(item))
	}
	for _, item := range reviews {
		out = append(out, projectReviewFromCairnlineSidecar(item))
	}
	return out
}

func projectGenericArtifactFromCairnlineSidecar(item ProjectCairnlineSidecarArtifactItem) projectwork.CollaborationArtifact {
	return projectwork.CollaborationArtifact{
		ID:           item.ID,
		ProjectID:    item.ProjectID,
		WorkItemID:   item.WorkItemID,
		AssignmentID: item.AssignmentID,
		Kind:         item.Kind,
		Title:        item.Title,
		Body:         item.Body,
		AuthorRoleID: item.AuthorRoleID,
		CreatedAt:    projectCairnlineSidecarTime(item.CreatedAt),
		UpdatedAt:    projectCairnlineSidecarTime(item.UpdatedAt),
	}
}

func projectEvidenceFromCairnlineSidecar(item ProjectCairnlineSidecarEvidenceItem) projectwork.CollaborationArtifact {
	return projectwork.CollaborationArtifact{
		ID:                 item.ID,
		ProjectID:          item.ProjectID,
		WorkItemID:         item.WorkItemID,
		AssignmentID:       item.AssignmentID,
		Kind:               projectwork.ArtifactKindEvidenceLink,
		Title:              item.Title,
		Body:               item.Body,
		EvidenceSourceKind: firstNonEmptyString(strings.TrimSpace(item.SourceKind), projectwork.EvidenceSourceExternal),
		EvidenceURL:        item.Locator,
		EvidenceExternalID: item.ExternalID,
		EvidenceProvider:   item.Provider,
		EvidenceTrustLabel: item.TrustLabel,
		CreatedAt:          projectCairnlineSidecarTime(item.CreatedAt),
		UpdatedAt:          projectCairnlineSidecarTime(item.UpdatedAt),
	}
}

func projectReviewFromCairnlineSidecar(item ProjectCairnlineSidecarReviewItem) projectwork.CollaborationArtifact {
	return projectwork.CollaborationArtifact{
		ID:                     item.ID,
		ProjectID:              item.ProjectID,
		WorkItemID:             item.WorkItemID,
		AssignmentID:           item.AssignmentID,
		Kind:                   projectwork.ArtifactKindReview,
		Title:                  item.Title,
		Body:                   item.Body,
		AuthorRoleID:           item.ReviewerRoleID,
		ReviewedAssignmentID:   item.AssignmentID,
		ReviewVerdict:          projectHealthReviewVerdictFromCairnline(item.Verdict),
		ReviewRisk:             projectHealthReviewRiskFromCairnline(item.Risk),
		ReviewFollowUpRequired: projectHealthReviewRequiresFollowUpFromCairnline(item.Verdict),
		CreatedAt:              projectCairnlineSidecarTime(item.CreatedAt),
		UpdatedAt:              projectCairnlineSidecarTime(item.UpdatedAt),
	}
}

func projectHandoffsFromCairnlineSidecar(items []ProjectCairnlineSidecarHandoffItem) []projectwork.Handoff {
	out := make([]projectwork.Handoff, 0, len(items))
	for _, item := range items {
		out = append(out, projectwork.Handoff{
			ID:                    item.ID,
			ProjectID:             item.ProjectID,
			WorkItemID:            item.WorkItemID,
			SourceAssignmentID:    item.SourceAssignmentID,
			SourceRunID:           item.SourceRunID,
			SourceChatSessionID:   item.SourceChatSessionID,
			SourceMessageID:       item.SourceMessageID,
			TargetRoleID:          item.ToRoleID,
			TargetAssignmentID:    item.TargetAssignmentID,
			TargetWorkItemID:      item.TargetWorkItemID,
			Title:                 item.Title,
			Summary:               item.Body,
			RecommendedNextAction: item.RecommendedNextAction,
			LinkedArtifactIDs:     append([]string(nil), item.LinkedArtifactIDs...),
			LinkedMemoryIDs:       append([]string(nil), item.LinkedMemoryIDs...),
			ContextRefs:           append([]string(nil), item.ContextRefs...),
			Status:                projectHealthHandoffStatusFromCairnline(item.Status),
			ProvenanceKind:        item.ProvenanceKind,
			TrustLabel:            item.TrustLabel,
			CreatedByRoleID:       item.FromRoleID,
			CreatedAt:             projectCairnlineSidecarTime(item.CreatedAt),
			UpdatedAt:             projectCairnlineSidecarTime(item.UpdatedAt),
			StatusChangedAt:       projectworkTime(projectCairnlineSidecarTime(item.StatusChangedAt), projectCairnlineSidecarTime(item.UpdatedAt)),
		})
	}
	return out
}

func projectSkillsFromCairnlineSidecar(items []ProjectCairnlineSidecarSkillItem) []projectskills.Skill {
	out := make([]projectskills.Skill, 0, len(items))
	for _, item := range items {
		sourceIDs := append([]string(nil), item.SourceContextSourceIDs...)
		if len(sourceIDs) == 0 {
			sourceIDs = append(sourceIDs, item.SourceRefs...)
		}
		out = append(out, projectskills.Skill{
			ID:                     item.ID,
			ProjectID:              item.ProjectID,
			Title:                  item.Title,
			Description:            item.Description,
			Path:                   item.Path,
			RootID:                 item.RootID,
			Format:                 item.Format,
			SuggestedTools:         append([]string(nil), item.SuggestedTools...),
			RequiredPermissions:    projectSkillRequiredPermissionsFromResponse(item.RequiredPermissions),
			Enabled:                item.Enabled,
			Status:                 item.Status,
			TrustLabel:             item.TrustLabel,
			SourceContextSourceIDs: sourceIDs,
			Warnings:               append([]string(nil), item.Warnings...),
			DiscoveredAt:           projectCairnlineSidecarTime(item.DiscoveredAt),
			CreatedAt:              projectCairnlineSidecarTime(item.CreatedAt),
			UpdatedAt:              projectCairnlineSidecarTime(item.UpdatedAt),
		})
	}
	return out
}

func projectMemoryEntriesFromCairnlineSidecar(items []ProjectCairnlineSidecarMemoryEntryItem) []memory.Entry {
	out := make([]memory.Entry, 0, len(items))
	for _, item := range items {
		out = append(out, memory.Entry{
			ID:         item.ID,
			Scope:      memory.ScopeProject,
			ProjectID:  item.ProjectID,
			Title:      item.Title,
			Body:       item.Body,
			TrustLabel: item.TrustLabel,
			SourceKind: item.SourceKind,
			SourceID:   item.SourceID,
			Enabled:    item.Enabled,
			CreatedAt:  projectCairnlineSidecarTime(item.CreatedAt),
			UpdatedAt:  projectCairnlineSidecarTime(item.UpdatedAt),
		})
	}
	return out
}

func projectMemoryCandidatesFromCairnlineSidecar(items []ProjectCairnlineSidecarMemoryCandidateItem) []memory.Candidate {
	out := make([]memory.Candidate, 0, len(items))
	for _, item := range items {
		out = append(out, memory.Candidate{
			ID:                  item.ID,
			ProjectID:           item.ProjectID,
			Title:               item.Title,
			Body:                item.Body,
			SuggestedKind:       item.SuggestedKind,
			SuggestedTrustLabel: item.SuggestedTrustLabel,
			SuggestedSourceKind: item.SuggestedSourceKind,
			SuggestedSourceID:   item.SuggestedSourceID,
			SourceRefs:          projectMemoryCandidateSourceRefsFromCairnlineSidecar(item.SourceRefs),
			Status:              item.Status,
			StatusReason:        item.StatusReason,
			PromotedMemoryID:    item.PromotedMemoryID,
			CreatedAt:           projectCairnlineSidecarTime(item.CreatedAt),
			UpdatedAt:           projectCairnlineSidecarTime(item.UpdatedAt),
		})
	}
	return out
}

func projectMemoryCandidateSourceRefsFromCairnlineSidecar(items []ProjectCairnlineSidecarMemoryCandidateSourceRef) []memory.CandidateSourceRef {
	out := make([]memory.CandidateSourceRef, 0, len(items))
	for _, item := range items {
		out = append(out, memory.CandidateSourceRef{
			Kind:  item.Kind,
			ID:    item.ID,
			Title: item.Title,
			URL:   item.URL,
		})
	}
	return out
}

func projectSkillRequiredPermissionsFromResponse(item *ProjectSkillRequiredPermissionsResponseItem) projectskills.RequiredPermissions {
	if item == nil {
		return projectskills.RequiredPermissions{}
	}
	return projectskills.RequiredPermissions{
		Tools:   item.Tools,
		Writes:  item.Writes,
		Network: item.Network,
	}
}

func projectCairnlineSidecarTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func projectCairnlineSidecarStructuredSkills(raw json.RawMessage) ([]ProjectCairnlineSidecarSkillItem, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return []ProjectCairnlineSidecarSkillItem{}, true, nil
	}
	var skills []ProjectCairnlineSidecarSkillItem
	if err := json.Unmarshal(trimmed, &skills); err != nil {
		return nil, false, err
	}
	if skills == nil {
		skills = []ProjectCairnlineSidecarSkillItem{}
	}
	return skills, true, nil
}

// cairnlineToolErrorCodeNotFound mirrors the machine-readable error code
// Cairnline emits in structuredContent on a failed tool call. Kept as a local
// wire constant so this classification change stays decoupled from the cairnline
// module pin; once the error-code contract is tagged and the ExecutionRef bump
// lands, this can be swapped for the exported cairnline.ErrorCodeNotFound.
const cairnlineToolErrorCodeNotFound = "not_found"

// projectCairnlineSidecarToolErrorCode returns the machine-readable error code
// Cairnline emits in structuredContent on a failed tool call, or "" when the
// sidecar did not provide one (pre-contract builds). The code is one of the
// Cairnline wire error-code values (not_found, invalid, already_exists,
// conflict, internal).
func projectCairnlineSidecarToolErrorCode(result *orchestrator.CachedMCPToolCallResult) string {
	if result == nil {
		return ""
	}
	raw := bytes.TrimSpace(result.Result.StructuredContent)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Error.Code)
}

// projectCairnlineSidecarToolErrorIsNotFound reports whether a failed sidecar
// tool call represents a not-found condition. It prefers the machine-readable
// structured error code Cairnline now emits; when the sidecar predates that
// contract and returns no code, it falls back to the legacy prose match so a
// not-found still maps to 404 during rollout.
func projectCairnlineSidecarToolErrorIsNotFound(result *orchestrator.CachedMCPToolCallResult) bool {
	if code := projectCairnlineSidecarToolErrorCode(result); code != "" {
		return code == cairnlineToolErrorCodeNotFound
	}
	if result == nil {
		return false
	}
	return projectCairnlineSidecarToolErrorTextIsNotFound(result.Text)
}

// projectCairnlineSidecarToolErrorTextIsNotFound is the transitional prose
// fallback: it matches Cairnline's not-found message by substring. It is used
// only when the sidecar returned no structured error code, and should be
// removed once every sidecar build emits structuredContent error codes.
func projectCairnlineSidecarToolErrorTextIsNotFound(text string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(text)), "not found")
}

func projectCairnlineSidecarReadErrShouldEvict(err error) bool {
	return errors.Is(err, mcpclient.ErrClientClosed) || mcpclient.IsTransportClosedErr(err)
}

func projectCairnlineSidecarReadFailure(message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "unknown failure"
	}
	return fmt.Errorf("%w: %s", errProjectCairnlineSidecarReadFailed, message)
}
