package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/codeintel"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/taskworkflow"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestAgentLoopQAWorkflowUsesInspectionCatalogAndProducesStructuredReport(t *testing.T) {
	t.Parallel()
	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("", types.ToolCall{
			ID: "write", Type: "function",
			Function: types.ToolCallFunction{Name: "file_edit", Arguments: `{"path":"main.go","old_text":"a","new_text":"b","propose":true}`},
		}, types.ToolCall{
			ID: "network", Type: "function",
			Function: types.ToolCallFunction{Name: AgentToolHTTPRequest, Arguments: `{"url":"https://example.test"}`},
		}, types.ToolCall{
			ID: "mcp", Type: "function",
			Function: types.ToolCallFunction{Name: "mcp__docs__lookup", Arguments: `{}`},
		})),
		makeChatResp(makeAssistantMsg("## Findings\nNo workspace changes were made.")),
	}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkflowMode = types.WorkflowModeQA
	spec.Task.WorkflowVersion = taskworkflow.QAVersion
	spec.Run.WorkflowMode = types.WorkflowModeQA
	spec.Run.WorkflowVersion = taskworkflow.QAVersion

	result, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "completed" || len(result.PendingApprovals) != 0 {
		t.Fatalf("result = %+v, want completed without approval", result)
	}
	for _, blocked := range []string{"shell_exec", "git_exec", "file_write", "file_edit", "apply_patch", AgentToolCodeIntelligence, AgentToolHTTPRequest, AgentToolWebSearch, AgentToolDraftProjectProposal} {
		if hasToolDefinition(llm.lastReqs[0].Tools, blocked) {
			t.Errorf("QA tool catalog contains %q", blocked)
		}
	}
	for _, allowed := range []string{"read_file", "grep", "glob", "artifact_read", "list_dir", "git_status", "git_diff"} {
		if !hasToolDefinition(llm.lastReqs[0].Tools, allowed) {
			t.Errorf("QA tool catalog omits %q", allowed)
		}
	}
	blockedSteps := 0
	for _, step := range result.Steps {
		if step.Phase == "policy" && step.OutputSummary["policy"] == "workflow_report_only" {
			blockedSteps++
		}
	}
	if blockedSteps != 3 {
		t.Fatalf("blocked QA steps = %d, want 3; steps=%+v", blockedSteps, result.Steps)
	}
	manifest := findArtifactByKind(result.Artifacts, "workflow_manifest")
	report := findArtifactByKind(result.Artifacts, "workflow_report")
	if manifest == nil || report == nil {
		t.Fatalf("workflow artifacts = %+v, want manifest and report", result.Artifacts)
	}
	if findArtifactByKind(result.Artifacts, "agent_conversation") == nil {
		t.Fatalf("workflow artifacts = %+v, want retained agent conversation alongside QA manifest", result.Artifacts)
	}
	if findArtifactByKind(result.Artifacts, "summary") != nil {
		t.Fatalf("QA artifacts = %+v, want workflow_report instead of generic summary", result.Artifacts)
	}
	var payload struct {
		Workflow struct {
			Mode       string `json:"mode"`
			ReportOnly bool   `json:"report_only"`
		} `json:"workflow"`
		AgentReported struct {
			Outcome string `json:"outcome"`
			Summary string `json:"summary_markdown"`
		} `json:"agent_reported"`
	}
	if err := json.Unmarshal([]byte(report.ContentText), &payload); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if payload.Workflow.Mode != "qa" || !payload.Workflow.ReportOnly || payload.AgentReported.Outcome != "reported" || !strings.Contains(payload.AgentReported.Summary, "No workspace changes") {
		t.Fatalf("QA report payload = %+v, want report-only agent result", payload)
	}
}

func TestAgentLoopQAWorkflowRunSnapshotRejectsCodeIntelligenceBeforeApprovalOrDispatch(t *testing.T) {
	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("", agentLoopToolCall("codeintel-qa", AgentToolCodeIntelligence, `{"operation":"capabilities"}`))),
		makeChatResp(makeAssistantMsg("Code intelligence was unavailable, so I used only the supplied evidence.")),
	}}
	fake := &fakeCodeIntelligenceService{result: codeintel.Result{Text: "must not run"}}
	loop := NewAgentLoopExecutor(
		llm,
		&stubExecutor{},
		&stubExecutor{},
		&stubExecutor{},
		4,
		[]string{AgentToolCodeIntelligence},
		HTTPRequestPolicy{},
		WithCodeIntelligenceService(fake),
	)
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = t.TempDir()
	// Exercise the immutable execution snapshot rather than the mutable Task.
	spec.Run.WorkflowMode = types.WorkflowModeQA
	spec.Run.WorkflowVersion = taskworkflow.QAVersion

	result, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "completed" || len(result.PendingApprovals) != 0 {
		t.Fatalf("result = %+v, want completed QA report without approval", result)
	}
	if len(llm.lastReqs) == 0 || hasToolDefinition(llm.lastReqs[0].Tools, AgentToolCodeIntelligence) {
		t.Fatalf("QA catalog exposed %s: %+v", AgentToolCodeIntelligence, llm.lastReqs)
	}
	if fake.request.Operation != "" {
		t.Fatalf("code-intelligence provider ran for QA with request %+v", fake.request)
	}
	var blocked *types.TaskStep
	for index := range result.Steps {
		if result.Steps[index].ToolName == AgentToolCodeIntelligence {
			blocked = &result.Steps[index]
			break
		}
	}
	if blocked == nil || blocked.Phase != "policy" || blocked.Result != "denied" || blocked.OutputSummary["policy"] != "workflow_report_only" {
		t.Fatalf("QA code-intelligence step = %+v, want report-only policy denial", blocked)
	}
	if len(llm.lastReqs) < 2 {
		t.Fatalf("LLM requests = %d, want denied tool result followed by final report", len(llm.lastReqs))
	}
	deniedToolResult := false
	for _, message := range llm.lastReqs[1].Messages {
		if message.Role == "tool" && message.ToolError && strings.Contains(message.Content, "report-only QA workflow") {
			deniedToolResult = true
			break
		}
	}
	if !deniedToolResult {
		t.Fatalf("follow-up messages = %+v, want code-intelligence policy-denied tool result", llm.lastReqs[1].Messages)
	}
}

func TestAgentLoopQAWorkflowGitToolsExplainUnavailableEvidence(t *testing.T) {
	t.Parallel()

	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("", agentLoopToolCall("qa-status", "git_status", `{}`), agentLoopToolCall("qa-diff", "git_diff", `{"staged":true}`))),
		makeChatResp(makeAssistantMsg("Git metadata was unavailable, so I inspected only copied files.")),
	}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, []string{"git_status", "git_diff"}, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkflowMode = types.WorkflowModeQA
	spec.Task.WorkflowVersion = taskworkflow.QAVersion
	spec.Run.WorkflowMode = types.WorkflowModeQA
	spec.Run.WorkflowVersion = taskworkflow.QAVersion

	result, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "completed" || len(result.PendingApprovals) != 0 {
		t.Fatalf("result = %+v, want completed QA report without Git approval", result)
	}
	for _, name := range []string{"git_status", "git_diff"} {
		var description string
		for _, tool := range llm.lastReqs[0].Tools {
			if tool.Function.Name == name {
				description = tool.Function.Description
				break
			}
		}
		if description == "" {
			t.Errorf("QA tool catalog omits %q; it should explain the Git-evidence limitation", name)
		}
		if !strings.Contains(description, "Unavailable in Hecate's report-only QA v0 workflow") {
			t.Errorf("QA %s description = %q, want unavailable-evidence guidance", name, description)
		}
		var unavailable *types.TaskStep
		for i := range result.Steps {
			step := &result.Steps[i]
			if step.ToolName == name {
				unavailable = step
				break
			}
		}
		if unavailable == nil || unavailable.Result != "denied" || unavailable.ErrorKind != "workflow_evidence_unavailable" || unavailable.OutputSummary["policy"] != "workflow_git_metadata" {
			t.Fatalf("%s QA tool step = %+v, want explicit unavailable evidence record", name, unavailable)
		}
	}
	if len(llm.lastReqs) < 2 {
		t.Fatalf("LLM requests = %d, want QA follow-up with unavailable Git results", len(llm.lastReqs))
	}
	toolErrors := 0
	for _, message := range llm.lastReqs[1].Messages {
		if message.Role == "tool" && message.ToolError && strings.Contains(message.Content, taskworkflow.QAGitEvidenceUnavailableReason) {
			toolErrors++
		}
	}
	if toolErrors != 2 {
		t.Errorf("QA unavailable Git results with ToolError=true = %d, want 2; messages=%+v", toolErrors, llm.lastReqs[1].Messages)
	}
}

func TestAgentLoopQAWorkflowNeverStartsConfiguredMCPHost(t *testing.T) {
	t.Parallel()
	loop := NewAgentLoopExecutor(&scriptedLLM{}, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	var factoryCalls atomic.Int32
	loop.SetMCPHostFactory(func(context.Context, []types.MCPServerConfig) (AgentMCPHost, error) {
		factoryCalls.Add(1)
		return nil, errors.New("MCP host must not start for QA")
	})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkflowMode = types.WorkflowModeQA
	spec.Task.WorkflowVersion = taskworkflow.QAVersion
	spec.Run.WorkflowMode = types.WorkflowModeQA
	spec.Run.WorkflowVersion = taskworkflow.QAVersion
	spec.Task.MCPServers = []types.MCPServerConfig{{Name: "docs", Command: "fake"}}

	result, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "failed" || !strings.Contains(result.LastError, "cannot start external MCP") {
		t.Fatalf("result = %+v, want rejected QA MCP configuration", result)
	}
	if factoryCalls.Load() != 0 {
		t.Fatalf("MCP factory calls = %d, want 0", factoryCalls.Load())
	}
	if findArtifactByKind(result.Artifacts, "workflow_manifest") == nil {
		t.Fatalf("artifacts = %+v, want workflow manifest on early failure", result.Artifacts)
	}
}

func TestAgentLoopQAWorkflowBlocksBrowserInspectionInV0(t *testing.T) {
	t.Parallel()

	allowed := true
	inspector := &fakeBrowserInspector{}
	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("", agentLoopToolCall("browser-qa", AgentToolBrowserInspect, `{"url":"https://app.example.test/reports"}`))),
		makeChatResp(makeAssistantMsg("Browser inspection is unavailable in this QA workflow.")),
	}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{}, WithBrowserInspector(inspector))
	spec := newAgentLoopSpec(t)
	spec.Task.WorkflowMode = types.WorkflowModeQA
	spec.Task.WorkflowVersion = taskworkflow.QAVersion
	spec.Run.WorkflowMode = types.WorkflowModeQA
	spec.Run.WorkflowVersion = taskworkflow.QAVersion
	spec.Task.OriginKind = "project_work_item"
	spec.Task.AgentPresetID = "preset-browser-evidence"
	spec.Task.AgentPresetBrowserAllowed = &allowed
	spec.Task.AgentPresetBrowserAllowedOrigins = []string{"https://app.example.test"}

	result, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if hasToolDefinition(llm.lastReqs[0].Tools, AgentToolBrowserInspect) {
		t.Fatalf("QA catalog exposed browser evidence in v0: %+v", llm.lastReqs[0].Tools)
	}
	if result.Status != "completed" || len(result.PendingApprovals) != 0 {
		t.Fatalf("result = %+v, want browser inspection denied without approval", result)
	}
	if len(inspector.requests) != 0 {
		t.Fatalf("browser inspector ran for QA v0: %+v", inspector.requests)
	}
}

func TestAgentLoopQAWorkflowWithoutLLMRecordsManifestButDoesNotInventReport(t *testing.T) {
	t.Parallel()
	loop := NewAgentLoopExecutor(nil, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkflowMode = types.WorkflowModeQA
	spec.Task.WorkflowVersion = taskworkflow.QAVersion
	spec.Run.WorkflowMode = types.WorkflowModeQA
	spec.Run.WorkflowVersion = taskworkflow.QAVersion

	result, err := loop.Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "failed" || findArtifactByKind(result.Artifacts, "workflow_manifest") == nil || findArtifactByKind(result.Artifacts, "workflow_report") != nil {
		t.Fatalf("result = %+v, want failed manifest-only QA execution", result)
	}
}

func TestAgentLoopQAWorkflowManifestStaysStableAcrossSameRunResume(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := taskstate.NewMemoryStore()
	runStartedAt := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	spec := newAgentLoopSpec(t)
	spec.Task.WorkflowMode = types.WorkflowModeQA
	spec.Task.WorkflowVersion = taskworkflow.QAVersion
	spec.Run.WorkflowMode = types.WorkflowModeQA
	spec.Run.WorkflowVersion = taskworkflow.QAVersion
	spec.Run.StartedAt = runStartedAt
	spec.StartedAt = runStartedAt.Add(10 * time.Second)
	spec.UpsertArtifact = func(artifact types.TaskArtifact) error {
		_, err := store.CreateArtifact(ctx, artifact)
		return err
	}
	loop := NewAgentLoopExecutor(nil, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})

	first, err := loop.Execute(ctx, spec)
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	firstManifest := findArtifactByKind(first.Artifacts, "workflow_manifest")
	if firstManifest == nil {
		t.Fatalf("first artifacts = %+v, want workflow manifest", first.Artifacts)
	}

	// Re-running the durable run models recovery after an approval pause. The
	// executor-attempt timestamp changes, while the recorded run start does not.
	spec.StartedAt = runStartedAt.Add(5 * time.Minute)
	spec.ResumeCheckpoint = &ResumeCheckpoint{SameRun: true}
	resumed, err := loop.Execute(ctx, spec)
	if err != nil {
		t.Fatalf("resumed Execute: %v", err)
	}
	resumedManifest := findArtifactByKind(resumed.Artifacts, "workflow_manifest")
	if resumedManifest == nil {
		t.Fatalf("resumed artifacts = %+v, want workflow manifest", resumed.Artifacts)
	}
	stored, found, err := store.GetArtifact(ctx, spec.Task.ID, firstManifest.ID)
	if err != nil || !found {
		t.Fatalf("GetArtifact(%q): found=%t err=%v", firstManifest.ID, found, err)
	}
	if !firstManifest.CreatedAt.Equal(runStartedAt) || !resumedManifest.CreatedAt.Equal(runStartedAt) || !stored.CreatedAt.Equal(runStartedAt) {
		t.Fatalf("manifest timestamps = first %s resumed %s stored %s, want durable run start %s", firstManifest.CreatedAt, resumedManifest.CreatedAt, stored.CreatedAt, runStartedAt)
	}
	if resumedManifest.ContentText != firstManifest.ContentText || stored.ContentText != firstManifest.ContentText {
		t.Fatalf("manifest content changed across same-run resume: first=%q resumed=%q stored=%q", firstManifest.ContentText, resumedManifest.ContentText, stored.ContentText)
	}
}

func TestAgentLoopQAWorkflowRecoveryPreservesExistingReportTimestamp(t *testing.T) {
	t.Parallel()

	llm := &scriptedLLM{responses: []*types.ChatResponse{
		makeChatResp(makeAssistantMsg("Saved QA finding.")),
	}}
	loop := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	spec := newAgentLoopSpec(t)
	spec.Task.WorkflowMode = types.WorkflowModeQA
	spec.Task.WorkflowVersion = taskworkflow.QAVersion
	spec.Run.WorkflowMode = types.WorkflowModeQA
	spec.Run.WorkflowVersion = taskworkflow.QAVersion
	artifacts := make(map[string]types.TaskArtifact)
	spec.UpsertArtifact = func(artifact types.TaskArtifact) error {
		artifacts[artifact.ID] = artifact
		return nil
	}
	spec.GetArtifact = func(_ string, artifactID string) (types.TaskArtifact, bool, error) {
		artifact, found := artifacts[artifactID]
		return artifact, found, nil
	}

	first, err := loop.Execute(t.Context(), spec)
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	firstReport := findArtifactByKind(first.Artifacts, "workflow_report")
	conversation := findArtifactByKind(first.Artifacts, "agent_conversation")
	if firstReport == nil || conversation == nil {
		t.Fatalf("first artifacts = %+v, want workflow report and conversation", first.Artifacts)
	}

	// Simulate a crash after the report upsert but before the terminal run
	// transition. Recovery should finish from the saved assistant response
	// without treating the already-recorded report as newly created evidence.
	spec.ResumeCheckpoint = &ResumeCheckpoint{
		SourceRunID:           spec.Run.ID,
		SameRun:               true,
		LastCompletedStepID:   first.Steps[0].ID,
		LastStepIndex:         first.Steps[0].Index,
		ThisRunModelCallCount: 1,
		AgentConversation:     []byte(conversation.ContentText),
	}
	recovered, err := loop.Execute(t.Context(), spec)
	if err != nil {
		t.Fatalf("recovery Execute: %v", err)
	}
	recoveredReport := findArtifactByKind(recovered.Artifacts, "workflow_report")
	if recoveredReport == nil {
		t.Fatalf("recovery artifacts = %+v, want workflow report", recovered.Artifacts)
	}
	stored := artifacts[firstReport.ID]
	if !recoveredReport.CreatedAt.Equal(firstReport.CreatedAt) || !stored.CreatedAt.Equal(firstReport.CreatedAt) {
		t.Fatalf("report timestamps = first %s recovered %s stored %s, want preserved report time", firstReport.CreatedAt, recoveredReport.CreatedAt, stored.CreatedAt)
	}
	if recoveredReport.ContentText != firstReport.ContentText || stored.ContentText != firstReport.ContentText {
		t.Fatalf("report content changed during same-run recovery: first=%q recovered=%q stored=%q", firstReport.ContentText, recoveredReport.ContentText, stored.ContentText)
	}
	if got := llm.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want only the pre-crash call", got)
	}
}

func TestAgentLoopQAWorkflowReportDeclaresBrowserEvidenceUnavailable(t *testing.T) {
	t.Parallel()

	spec := newAgentLoopSpec(t)
	spec.Task.WorkflowMode = types.WorkflowModeQA
	spec.Task.WorkflowVersion = taskworkflow.QAVersion
	spec.Run.WorkflowMode = types.WorkflowModeQA
	spec.Run.WorkflowVersion = taskworkflow.QAVersion
	report, err := buildTerminalArtifact(spec, "step-final", time.Now().UTC(), "Recovered QA report")
	if err != nil {
		t.Fatalf("buildTerminalArtifact: %v", err)
	}
	var payload struct {
		Observed struct {
			BrowserEvidencePosture string `json:"browser_evidence_posture"`
		} `json:"hecate_observed"`
	}
	if err := json.Unmarshal([]byte(report.ContentText), &payload); err != nil {
		t.Fatalf("decode QA report: %v", err)
	}
	if payload.Observed.BrowserEvidencePosture != "unavailable_in_v0" {
		t.Fatalf("browser evidence posture = %q, want unavailable_in_v0", payload.Observed.BrowserEvidencePosture)
	}
}
