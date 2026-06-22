package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/pkg/types"
)

type fakeProjectAssistantDraftTool struct {
	input ProjectAssistantDraftInput
	err   error
}

func (f *fakeProjectAssistantDraftTool) DraftProjectProposal(_ context.Context, input ProjectAssistantDraftInput) (ProjectAssistantDraftResult, error) {
	f.input = input
	if f.err != nil {
		return ProjectAssistantDraftResult{}, f.err
	}
	return ProjectAssistantDraftResult{
		Object:      "project_assistant.chat_proposal",
		ProjectID:   input.ProjectID,
		Request:     input.Request,
		ProposalID:  "proposal_1",
		Title:       "Plan next work",
		Summary:     "Capture the next reviewable project task.",
		ActionCount: 1,
		Proposal: json.RawMessage(`{
			"id":"proposal_1",
			"title":"Plan next work",
			"summary":"Capture the next reviewable project task.",
			"actions":[{"kind":"create_work_item","target":{"project_id":"proj_1"},"patch":{"title":"Plan next work"}}],
			"requires_confirmation":true
		}`),
	}, nil
}

func TestAgentToolDefinitions_ProjectAssistantDraftOnlyForProjectChat(t *testing.T) {
	base := agentToolDefinitions(false)
	if hasTool(base, AgentToolDraftProjectProposal) {
		t.Fatalf("%s present without project-chat availability", AgentToolDraftProjectProposal)
	}
	withProposal := agentToolDefinitions(true)
	if !hasTool(withProposal, AgentToolDraftProjectProposal) {
		t.Fatalf("%s missing when project-chat availability is true", AgentToolDraftProjectProposal)
	}
}

func TestAgentLoop_ProjectAssistantDraftToolCreatesProposalArtifact(t *testing.T) {
	tool := &fakeProjectAssistantDraftTool{}
	dispatcher := &agentLoopToolDispatcher{projectAssistantDraftTool: tool}
	spec := newAgentLoopSpec(t)
	spec.Task.ProjectID = "proj_1"
	spec.Task.OriginKind = "chat"
	spec.Task.OriginID = "chat_1"
	spec.Task.ExecutionProfile = "chat_agent"
	spec.RequestID = "req_1"
	spec.TraceID = "trace_1"

	result, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"call-proposal",
		AgentToolDraftProjectProposal,
		`{"request":"Plan next work","role_id":"role_pm","driver_kind":"hecate_task"}`,
	), 3, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if tool.input.ProjectID != "proj_1" || tool.input.SourceChatSessionID != "chat_1" {
		t.Fatalf("draft input project/session = %q/%q, want proj_1/chat_1", tool.input.ProjectID, tool.input.SourceChatSessionID)
	}
	if tool.input.Request != "Plan next work" || tool.input.RoleID != "role_pm" || tool.input.DriverKind != "hecate_task" {
		t.Fatalf("draft input request/role/driver = %+v", tool.input)
	}
	if result.Step == nil || result.Step.ToolName != AgentToolDraftProjectProposal || result.Step.Status != "completed" {
		t.Fatalf("step = %+v, want completed %s tool step", result.Step, AgentToolDraftProjectProposal)
	}
	if len(result.Artifacts) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(result.Artifacts))
	}
	artifact := result.Artifacts[0]
	if artifact.Kind != ProjectAssistantProposalArtifactKind || artifact.MimeType != "application/json" || artifact.Status != "ready" {
		t.Fatalf("artifact shape = %+v, want ready json proposal artifact", artifact)
	}
	if !strings.Contains(result.Text, "Review this Project Assistant proposal in Projects") {
		t.Fatalf("result text = %q, want operator-review guidance", result.Text)
	}
	var payload ProjectAssistantDraftResult
	if err := json.Unmarshal([]byte(artifact.ContentText), &payload); err != nil {
		t.Fatalf("artifact ContentText JSON error = %v\n%s", err, artifact.ContentText)
	}
	if payload.ProposalID != "proposal_1" || payload.ActionCount != 1 || !json.Valid(payload.Proposal) {
		t.Fatalf("artifact payload = %+v, want embedded proposal metadata", payload)
	}
}

func TestAgentLoop_ProjectAssistantDraftToolRejectsNonProjectChatTask(t *testing.T) {
	dispatcher := &agentLoopToolDispatcher{projectAssistantDraftTool: &fakeProjectAssistantDraftTool{}}
	spec := newAgentLoopSpec(t)
	spec.Task.ProjectID = "proj_1"
	spec.Task.OriginKind = "project_work_item"
	spec.Task.OriginID = "work_1"
	spec.Task.ExecutionProfile = "implementation"

	result, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"call-proposal",
		AgentToolDraftProjectProposal,
		`{"request":"Plan next work"}`,
	), 2, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if result.Step == nil || result.Step.Status != "failed" {
		t.Fatalf("step = %+v, want failed availability step", result.Step)
	}
	if len(result.Artifacts) != 0 {
		t.Fatalf("artifacts = %d, want none", len(result.Artifacts))
	}
}

func hasTool(tools []types.Tool, name string) bool {
	for _, tool := range tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}
