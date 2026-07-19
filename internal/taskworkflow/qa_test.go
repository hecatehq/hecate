package taskworkflow

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestQAWorkflowArtifactsDescribeBoundedReportOnlyContract(t *testing.T) {
	t.Parallel()
	task := types.Task{
		ID:              "task-qa",
		WorkflowMode:    types.WorkflowModeQA,
		WorkflowVersion: QAVersion,
	}
	run := types.TaskRun{
		ID:              "run-qa",
		TaskID:          task.ID,
		WorkflowMode:    types.WorkflowModeQA,
		WorkflowVersion: QAVersion,
		RequestID:       "req-qa",
		TraceID:         "trace-qa",
	}
	createdAt := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	manifest, err := ManifestArtifact(task, run, createdAt)
	if err != nil {
		t.Fatalf("ManifestArtifact: %v", err)
	}
	if manifest.ID != ManifestArtifactID(run.ID) || manifest.Kind != "workflow_manifest" || manifest.MimeType != "application/json" {
		t.Fatalf("manifest = %+v, want stable JSON workflow manifest", manifest)
	}
	var manifestPayload map[string]any
	if err := json.Unmarshal([]byte(manifest.ContentText), &manifestPayload); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifestPayload["report_only"] != true || manifestPayload["workflow_mode"] != "qa" || manifestPayload["runbook_id"] != QARunbookID {
		t.Fatalf("manifest payload = %#v, want QA report-only contract", manifestPayload)
	}
	if _, ok := manifestPayload["success_artifact_kinds"]; !ok {
		t.Fatalf("manifest payload = %#v, want success-only report artifact declaration", manifestPayload)
	}
	allowed, ok := manifestPayload["allowed_evidence_tools"].([]any)
	if !ok || !containsString(allowed, "read_file") || containsString(allowed, "git_status") {
		t.Fatalf("manifest payload = %#v, want file evidence tools without Git inspection", manifestPayload)
	}
	unavailable, ok := manifestPayload["unavailable_evidence_tools"].([]any)
	if !ok || !containsString(unavailable, "git_status") || !containsString(unavailable, "git_diff") {
		t.Fatalf("manifest payload = %#v, want explicit unavailable Git evidence tools", manifestPayload)
	}
	blocked, ok := manifestPayload["blocked_capabilities"].([]any)
	if !ok || !containsString(blocked, "browser inspection and automation") || !containsString(blocked, "Git repository metadata and structured Git inspection") || !containsString(blocked, "semantic and structural code intelligence") {
		t.Fatalf("manifest payload = %#v, want browser, Git, and code-intelligence inspection blocked in QA v0", manifestPayload)
	}

	report, err := ReportArtifact(task, run, "step-final", createdAt, "## Findings\nNo changes made.")
	if err != nil {
		t.Fatalf("ReportArtifact: %v", err)
	}
	if report.ID != ReportArtifactID(run.ID) || report.Kind != "workflow_report" || report.StepID != "step-final" {
		t.Fatalf("report = %+v, want stable QA report artifact", report)
	}
	var reportPayload struct {
		SchemaVersion string `json:"schema_version"`
		Workflow      struct {
			Mode       string `json:"mode"`
			ReportOnly bool   `json:"report_only"`
		} `json:"workflow"`
		AgentReported struct {
			Outcome string `json:"outcome"`
			Summary string `json:"summary_markdown"`
		} `json:"agent_reported"`
		HecateObserved struct {
			NativeNetworkPosture   string `json:"native_network_posture"`
			GitEvidencePosture     string `json:"git_evidence_posture"`
			BrowserEvidencePosture string `json:"browser_evidence_posture"`
		} `json:"hecate_observed"`
	}
	if err := json.Unmarshal([]byte(report.ContentText), &reportPayload); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if reportPayload.SchemaVersion != qaReportSchemaVersion || reportPayload.Workflow.Mode != "qa" || !reportPayload.Workflow.ReportOnly || reportPayload.AgentReported.Outcome != "reported" || reportPayload.AgentReported.Summary != "## Findings\nNo changes made." {
		t.Fatalf("report payload = %+v, want versioned agent-reported QA output", reportPayload)
	}
	if reportPayload.HecateObserved.NativeNetworkPosture != "blocked" || reportPayload.HecateObserved.GitEvidencePosture != "unavailable_in_v0" || reportPayload.HecateObserved.BrowserEvidencePosture != "unavailable_in_v0" {
		t.Fatalf("observed posture = %+v, want native-network blocked and Git/browser evidence unavailable", reportPayload.HecateObserved)
	}
}

func containsString(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestQAExecutionValidationFailsClosedForPersistedRows(t *testing.T) {
	t.Parallel()

	validTask := types.Task{
		ExecutionKind:               "agent_loop",
		WorkflowMode:                types.WorkflowModeQA,
		WorkflowVersion:             QAVersion,
		WorkspaceMode:               "ephemeral",
		SandboxReadOnly:             true,
		WorkspaceSystemPromptPolicy: types.WorkspaceSystemPromptExclude,
	}
	validRun := types.TaskRun{WorkflowMode: types.WorkflowModeQA, WorkflowVersion: QAVersion}
	if err := ValidateExecution(validTask, validRun); err != nil {
		t.Fatalf("ValidateExecution(valid QA) = %v", err)
	}

	cases := []struct {
		name string
		task types.Task
		run  types.TaskRun
		want error
	}{
		{
			name: "unknown_task_mode",
			task: func() types.Task { task := validTask; task.WorkflowMode = "review"; return task }(),
			run:  types.TaskRun{},
			want: ErrUnsupportedWorkflowMode,
		},
		{
			name: "noncanonical_task_mode",
			task: func() types.Task { task := validTask; task.WorkflowMode = " QA "; return task }(),
			run:  types.TaskRun{},
			want: ErrUnsupportedWorkflowMode,
		},
		{
			name: "unknown_run_mode",
			task: validTask,
			run:  types.TaskRun{WorkflowMode: "review"},
			want: ErrUnsupportedWorkflowMode,
		},
		{
			name: "future_version",
			task: validTask,
			run:  types.TaskRun{WorkflowMode: types.WorkflowModeQA, WorkflowVersion: "v1"},
			want: ErrQAWorkflowVersion,
		},
		{
			name: "noncanonical_run_version",
			task: validTask,
			run:  types.TaskRun{WorkflowMode: types.WorkflowModeQA, WorkflowVersion: " v0 "},
			want: ErrQAWorkflowVersion,
		},
		{
			name: "noncanonical_task_version",
			task: func() types.Task { task := validTask; task.WorkflowVersion = " v0 "; return task }(),
			run:  types.TaskRun{},
			want: ErrQAWorkflowVersion,
		},
		{
			name: "missing_run_snapshot_version",
			task: validTask,
			run:  types.TaskRun{WorkflowMode: types.WorkflowModeQA},
			want: ErrQAWorkflowVersion,
		},
		{
			name: "orphan_run_version",
			task: validTask,
			run:  types.TaskRun{WorkflowVersion: QAVersion},
			want: ErrInvalidWorkflowSnapshot,
		},
		{
			name: "orphan_task_version",
			task: func() types.Task {
				task := validTask
				task.WorkflowMode = ""
				return task
			}(),
			run:  types.TaskRun{},
			want: ErrInvalidWorkflowSnapshot,
		},
		{
			name: "missing_version",
			task: func() types.Task { task := validTask; task.WorkflowVersion = ""; return task }(),
			run:  types.TaskRun{WorkflowMode: types.WorkflowModeQA},
			want: ErrQAWorkflowVersion,
		},
		{
			name: "shell_executor",
			task: func() types.Task { task := validTask; task.ExecutionKind = "shell"; return task }(),
			run:  validRun,
			want: ErrQARequiresAgentLoop,
		},
		{
			name: "mcp_server",
			task: func() types.Task {
				task := validTask
				task.MCPServers = []types.MCPServerConfig{{Name: "docs"}}
				return task
			}(),
			run:  validRun,
			want: ErrQAMCPServers,
		},
		{
			name: "writable_sandbox",
			task: func() types.Task { task := validTask; task.SandboxReadOnly = false; return task }(),
			run:  validRun,
			want: ErrQAReadOnly,
		},
		{
			name: "native_network",
			task: func() types.Task { task := validTask; task.SandboxNetwork = true; return task }(),
			run:  validRun,
			want: ErrQANetwork,
		},
		{
			name: "in_place_workspace",
			task: func() types.Task { task := validTask; task.WorkspaceMode = "in_place"; return task }(),
			run:  validRun,
			want: ErrQAWorkspaceMode,
		},
		{
			name: "workspace_reuse",
			task: func() types.Task { task := validTask; task.WorkspaceReuse = true; return task }(),
			run:  validRun,
			want: ErrQAWorkspaceReuse,
		},
		{
			name: "workspace_system_prompt_inherit",
			task: func() types.Task { task := validTask; task.WorkspaceSystemPromptPolicy = ""; return task }(),
			run:  validRun,
			want: ErrQAWorkspaceSystemPrompt,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateExecution(tc.task, tc.run); !errors.Is(err, tc.want) {
				t.Fatalf("ValidateExecution() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestQAToolPolicyFailsClosedOutsideStructuredInspection(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"read_file", "grep", "glob", "artifact_read", "list_dir", "git_status", "git_diff"} {
		if BlocksTool(types.WorkflowModeQA, name) {
			t.Errorf("BlocksTool(qa, %q) = true, want allowed evidence tool", name)
		}
	}
	for _, name := range []string{"shell_exec", "git_exec", "file_write", "file_edit", "apply_patch", "code_intelligence", "http_request", "web_search", "browser_inspect", "mcp__docs__lookup", "draft_project_proposal"} {
		if !BlocksTool(types.WorkflowModeQA, name) {
			t.Errorf("BlocksTool(qa, %q) = false, want blocked", name)
		}
	}
	if BlocksTool("", "shell_exec") {
		t.Fatal("BlocksTool(normal, shell_exec) = true, want normal task policy unchanged")
	}
	if !BlocksTool("future_mode", "read_file") {
		t.Fatal("BlocksTool(future_mode, read_file) = false, want unknown persisted mode fail-closed")
	}
	for _, name := range []string{"git_status", "git_diff"} {
		if !IsUnavailableEvidenceTool(types.WorkflowModeQA, name) {
			t.Errorf("IsUnavailableEvidenceTool(qa, %q) = false, want QA metadata limitation", name)
		}
	}
	if IsUnavailableEvidenceTool("", "git_status") {
		t.Fatal("IsUnavailableEvidenceTool(normal, git_status) = true, want normal structured Git inspection")
	}
}
