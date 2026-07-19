package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
	for _, blocked := range []string{"shell_exec", "git_exec", "file_write", "file_edit", "apply_patch", AgentToolHTTPRequest, AgentToolWebSearch, AgentToolDraftProjectProposal} {
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
