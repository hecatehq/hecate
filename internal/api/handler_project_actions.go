package api

import "strings"

const (
	projectActionDraftProjectProposal    = "draft_project_proposal"
	projectActionOpenActivityBucket      = "open_activity_bucket"
	projectActionOpenAssignmentPreflight = "open_assignment_preflight"
	projectActionOpenMemoryReview        = "open_memory_review"
	projectActionOpenAgentPresets        = "open_agent_presets"
	projectActionOpenProjectSettings     = "open_project_settings"
	projectActionOpenRoles               = "open_roles"
	projectActionOpenSkills              = "open_skills"
	projectActionOpenTask                = "open_task"
	projectActionOpenWorkItem            = "open_work_item"
	projectActionReviewMemoryCandidate   = "review_memory_candidate"
)

type ProjectActionResponse struct {
	Type           string `json:"type"`
	ProjectID      string `json:"project_id"`
	WorkItemID     string `json:"work_item_id,omitempty"`
	AssignmentID   string `json:"assignment_id,omitempty"`
	ArtifactID     string `json:"artifact_id,omitempty"`
	HandoffID      string `json:"handoff_id,omitempty"`
	ActivityBucket string `json:"activity_bucket,omitempty"`
	TaskID         string `json:"task_id,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	ChatID         string `json:"chat_id,omitempty"`
	CandidateID    string `json:"candidate_id,omitempty"`
	Request        string `json:"request,omitempty"`
}

type ProjectOperationsBriefActionResponse = ProjectActionResponse

func newProjectActionDraftProposal(projectID, workItemID, request string) ProjectActionResponse {
	return ProjectActionResponse{
		Type:       projectActionDraftProjectProposal,
		ProjectID:  strings.TrimSpace(projectID),
		WorkItemID: strings.TrimSpace(workItemID),
		Request:    strings.TrimSpace(request),
	}
}

func newProjectActionOpenAssignmentPreflight(projectID, workItemID, assignmentID, bucket string) ProjectActionResponse {
	return ProjectActionResponse{
		Type:           projectActionOpenAssignmentPreflight,
		ProjectID:      strings.TrimSpace(projectID),
		WorkItemID:     strings.TrimSpace(workItemID),
		AssignmentID:   strings.TrimSpace(assignmentID),
		ActivityBucket: strings.TrimSpace(bucket),
	}
}

func newProjectActionOpenMemoryReview(projectID string) ProjectActionResponse {
	return ProjectActionResponse{Type: projectActionOpenMemoryReview, ProjectID: strings.TrimSpace(projectID)}
}

func newProjectActionOpenAgentPresets(projectID string) ProjectActionResponse {
	return ProjectActionResponse{Type: projectActionOpenAgentPresets, ProjectID: strings.TrimSpace(projectID)}
}

func newProjectActionOpenProjectSettings(projectID string) ProjectActionResponse {
	return ProjectActionResponse{Type: projectActionOpenProjectSettings, ProjectID: strings.TrimSpace(projectID)}
}

func newProjectActionOpenRoles(projectID string) ProjectActionResponse {
	return ProjectActionResponse{Type: projectActionOpenRoles, ProjectID: strings.TrimSpace(projectID)}
}

func newProjectActionOpenSkills(projectID string) ProjectActionResponse {
	return ProjectActionResponse{Type: projectActionOpenSkills, ProjectID: strings.TrimSpace(projectID)}
}

func newProjectActionOpenTask(projectID, taskID, runID string) ProjectActionResponse {
	return ProjectActionResponse{
		Type:      projectActionOpenTask,
		ProjectID: strings.TrimSpace(projectID),
		TaskID:    strings.TrimSpace(taskID),
		RunID:     strings.TrimSpace(runID),
	}
}

func newProjectActionOpenWorkItem(projectID, workItemID, assignmentID, handoffID, bucket string) ProjectActionResponse {
	return ProjectActionResponse{
		Type:           projectActionOpenWorkItem,
		ProjectID:      strings.TrimSpace(projectID),
		WorkItemID:     strings.TrimSpace(workItemID),
		AssignmentID:   strings.TrimSpace(assignmentID),
		HandoffID:      strings.TrimSpace(handoffID),
		ActivityBucket: strings.TrimSpace(bucket),
	}
}

func newProjectActionOpenActivityBucket(projectID, bucket string) ProjectActionResponse {
	return ProjectActionResponse{
		Type:           projectActionOpenActivityBucket,
		ProjectID:      strings.TrimSpace(projectID),
		ActivityBucket: strings.TrimSpace(bucket),
	}
}

func newProjectActionReviewMemoryCandidate(projectID, candidateID string) ProjectActionResponse {
	return ProjectActionResponse{
		Type:        projectActionReviewMemoryCandidate,
		ProjectID:   strings.TrimSpace(projectID),
		CandidateID: strings.TrimSpace(candidateID),
	}
}
