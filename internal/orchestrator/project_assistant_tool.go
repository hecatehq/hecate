package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

const (
	AgentToolDraftProjectProposal          = "draft_project_proposal"
	ProjectAssistantProposalArtifactKind   = "project_assistant_proposal"
	projectAssistantProposalArtifactObject = "project_assistant.chat_proposal"
)

// ProjectAssistantDraftTool is the agent-loop seam for project-linked
// Hecate Chat. Implementations draft a reviewable Project Assistant
// proposal only; applying the proposal stays behind the Project
// Assistant confirmation API.
type ProjectAssistantDraftTool interface {
	DraftProjectProposal(context.Context, ProjectAssistantDraftInput) (ProjectAssistantDraftResult, error)
}

type ProjectAssistantDraftInput struct {
	ProjectID           string
	SourceChatSessionID string
	Request             string
	WorkItemID          string
	RoleID              string
	DriverKind          string
	RequestID           string
	TraceID             string
}

type ProjectAssistantDraftResult struct {
	Object              string          `json:"object"`
	ProjectID           string          `json:"project_id"`
	SourceChatSessionID string          `json:"source_chat_session_id,omitempty"`
	Request             string          `json:"request,omitempty"`
	ProposalID          string          `json:"proposal_id"`
	Title               string          `json:"title,omitempty"`
	Summary             string          `json:"summary,omitempty"`
	ActionCount         int             `json:"action_count"`
	Proposal            json.RawMessage `json:"proposal"`
}

type projectAssistantDraftArgs struct {
	Request    string `json:"request"`
	WorkItemID string `json:"work_item_id,omitempty"`
	RoleID     string `json:"role_id,omitempty"`
	DriverKind string `json:"driver_kind,omitempty"`
}

func (e *AgentLoopExecutor) SetProjectAssistantDraftTool(tool ProjectAssistantDraftTool) {
	if e == nil {
		return
	}
	if e.toolDispatcher != nil {
		e.toolDispatcher.projectAssistantDraftTool = tool
	}
}

func projectAssistantDraftToolAvailable(task types.Task, tool ProjectAssistantDraftTool) bool {
	return tool != nil &&
		strings.TrimSpace(task.ProjectID) != "" &&
		strings.TrimSpace(task.OriginKind) == "chat" &&
		strings.TrimSpace(task.OriginID) != "" &&
		strings.TrimSpace(task.ExecutionProfile) == "chat_agent"
}

func (d *agentLoopToolDispatcher) projectAssistantDraftProposalTool(ctx context.Context, spec ExecutionSpec, args projectAssistantDraftArgs, stepIndex int, startedAt time.Time, toolName string) agentLoopToolDispatchResult {
	request := strings.TrimSpace(args.Request)
	if request == "" {
		return agentLoopToolDispatchResult{Text: "draft_project_proposal: request is required"}
	}
	finishedAt := time.Now().UTC()
	step := types.TaskStep{
		ID:       spec.NewID("step"),
		TaskID:   spec.Task.ID,
		RunID:    spec.Run.ID,
		Index:    stepIndex,
		Kind:     "tool",
		Title:    "Draft Project Assistant proposal",
		Status:   "completed",
		Phase:    "execution",
		Result:   resultFromStatus("completed"),
		ToolName: toolName,
		Input: map[string]any{
			"project_id":             strings.TrimSpace(spec.Task.ProjectID),
			"source_chat_session_id": strings.TrimSpace(spec.Task.OriginID),
			"request":                request,
			"work_item_id":           strings.TrimSpace(args.WorkItemID),
			"role_id":                strings.TrimSpace(args.RoleID),
			"driver_kind":            strings.TrimSpace(args.DriverKind),
		},
		StartedAt: startedAt,
		RequestID: spec.RequestID,
		TraceID:   spec.TraceID,
	}
	if !projectAssistantDraftToolAvailable(spec.Task, d.projectAssistantDraftTool) {
		step.Status = "failed"
		step.Result = resultFromStatus("failed")
		step.Error = "draft_project_proposal is only available to project-linked, task-backed Hecate Chat Turns"
		step.FinishedAt = finishedAt
		step.OutputSummary = map[string]any{"is_error": true}
		return agentLoopToolDispatchResult{Text: step.Error, Step: &step}
	}

	result, err := d.projectAssistantDraftTool.DraftProjectProposal(ctx, ProjectAssistantDraftInput{
		ProjectID:           strings.TrimSpace(spec.Task.ProjectID),
		SourceChatSessionID: strings.TrimSpace(spec.Task.OriginID),
		Request:             request,
		WorkItemID:          strings.TrimSpace(args.WorkItemID),
		RoleID:              strings.TrimSpace(args.RoleID),
		DriverKind:          strings.TrimSpace(args.DriverKind),
		RequestID:           spec.RequestID,
		TraceID:             spec.TraceID,
	})
	finishedAt = time.Now().UTC()
	step.FinishedAt = finishedAt
	if err != nil {
		step.Status = "failed"
		step.Result = resultFromStatus("failed")
		step.Error = err.Error()
		step.OutputSummary = map[string]any{"is_error": true}
		return agentLoopToolDispatchResult{
			Text: fmt.Sprintf("draft_project_proposal failed: %v", err),
			Step: &step,
		}
	}
	if result.Object == "" {
		result.Object = projectAssistantProposalArtifactObject
	}
	result.ProjectID = strings.TrimSpace(result.ProjectID)
	if result.ProjectID == "" {
		result.ProjectID = strings.TrimSpace(spec.Task.ProjectID)
	}
	if result.SourceChatSessionID == "" {
		result.SourceChatSessionID = strings.TrimSpace(spec.Task.OriginID)
	}
	if result.Request == "" {
		result.Request = request
	}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		step.Status = "failed"
		step.Result = resultFromStatus("failed")
		step.Error = fmt.Sprintf("encode proposal artifact: %v", err)
		step.OutputSummary = map[string]any{"is_error": true}
		return agentLoopToolDispatchResult{Text: step.Error, Step: &step}
	}
	artifact := types.TaskArtifact{
		ID:          spec.NewID("artifact"),
		TaskID:      spec.Task.ID,
		RunID:       spec.Run.ID,
		StepID:      step.ID,
		Kind:        ProjectAssistantProposalArtifactKind,
		Name:        "Project Assistant proposal",
		Description: "Reviewable Project Assistant proposal drafted from Hecate Chat.",
		MimeType:    "application/json",
		StorageKind: "inline",
		Path:        "project-assistant-proposal.json",
		ContentText: string(raw),
		SizeBytes:   int64(len(raw)),
		Status:      "ready",
		CreatedAt:   finishedAt,
		RequestID:   spec.RequestID,
		TraceID:     spec.TraceID,
	}
	step.OutputSummary = map[string]any{
		"is_error":     false,
		"artifact_id":  artifact.ID,
		"project_id":   result.ProjectID,
		"proposal_id":  result.ProposalID,
		"action_count": result.ActionCount,
	}
	text := fmt.Sprintf("status=ready\nproposal_id=%s\nproposal_artifact_id=%s\nactions=%d\nReview this Project Assistant proposal in Projects before applying it.", result.ProposalID, artifact.ID, result.ActionCount)
	return agentLoopToolDispatchResult{Text: text, Step: &step, Artifacts: []types.TaskArtifact{artifact}}
}
