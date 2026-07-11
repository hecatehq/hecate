package api

import "context"

const (
	cairnlineMirrorFamilyProjects           = "projects"
	cairnlineMirrorFamilyRoots              = "roots"
	cairnlineMirrorFamilyContextSources     = "context-sources"
	cairnlineMirrorFamilySkills             = "skills"
	cairnlineMirrorFamilyRoles              = "roles"
	cairnlineMirrorFamilyWorkItems          = "work-items"
	cairnlineMirrorFamilyAssignments        = "assignments"
	cairnlineMirrorFamilyArtifacts          = "artifacts"
	cairnlineMirrorFamilyHandoffs           = "handoffs"
	cairnlineMirrorFamilyMemory             = "memory"
	cairnlineMirrorFamilyMemoryCandidates   = "memory-candidates"
	cairnlineMirrorFamilyAssistantProposals = "project-assistant-proposals"
)

func (h *Handler) recordCairnlineMirrorFailure(ctx context.Context, _ string, operation, projectID string, err error) {
	h.logCairnlineMirrorError(ctx, operation, projectID, err)
}

func (h *Handler) recordCairnlineMirrorResult(ctx context.Context, family, operation, projectID string, err error) {
	if err != nil {
		h.recordCairnlineMirrorFailure(ctx, family, operation, projectID, err)
	}
}
