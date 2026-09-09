package orchestrator

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/taskworkflow"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestAgentLoopApprovalGate_EvaluateConfiguredToolsDedupesAndBuildsPause(t *testing.T) {
	spec := newAgentLoopSpec(t)
	spec.RequestID = "req-gate"
	spec.TraceID = "trace-gate"
	when := time.Date(2026, 5, 29, 10, 30, 0, 0, time.UTC)

	gate := newAgentLoopApprovalGate([]string{" shell_exec ", "", "shell_exec"})
	calls := []types.ToolCall{
		agentLoopToolCall("call-shell-1", "shell_exec", `{"command":"ls"}`),
		agentLoopToolCall("call-file-1", "file_write", `{"path":"out.txt","content":"hi"}`),
		agentLoopToolCall("call-shell-2", "shell_exec", `{"command":"pwd"}`),
	}
	pause, ok := gate.Evaluate(spec, 2, 7, when, calls)
	if !ok {
		t.Fatalf("Evaluate() ok = false, want true")
	}

	approval := pause.Approval
	if approval.Kind != "agent_loop_tool_call" || approval.Status != "pending" {
		t.Fatalf("approval shape = %+v, want pending agent_loop_tool_call", approval)
	}
	if approval.TaskID != spec.Task.ID || approval.RunID != spec.Run.ID {
		t.Fatalf("approval task/run = %s/%s, want %s/%s", approval.TaskID, approval.RunID, spec.Task.ID, spec.Run.ID)
	}
	if approval.RequestID != "req-gate" || approval.TraceID != "trace-gate" {
		t.Fatalf("approval request/trace = %s/%s", approval.RequestID, approval.TraceID)
	}
	if !approval.CreatedAt.Equal(when) {
		t.Fatalf("approval CreatedAt = %s, want %s", approval.CreatedAt, when)
	}
	if strings.Count(approval.Reason, "shell_exec") != 1 {
		t.Fatalf("approval reason should mention shell_exec once, got %q", approval.Reason)
	}
	if strings.Contains(approval.Reason, "file_write") {
		t.Fatalf("approval reason should not mention non-gated file_write: %q", approval.Reason)
	}
	if len(approval.ActionSummary) != 3 {
		t.Fatalf("approval action summary = %#v, want all three pending calls", approval.ActionSummary)
	}
	if !strings.Contains(approval.ActionSummary[1], "file_write") || !strings.Contains(approval.ActionSummary[1], "content_bytes=2") {
		t.Fatalf("approval action summary omitted non-gated file write shape: %#v", approval.ActionSummary)
	}
	if strings.Contains(strings.Join(approval.ActionSummary, "\n"), "hi") {
		t.Fatalf("approval action summary exposed file content: %#v", approval.ActionSummary)
	}
	if !approval.ActionSummaryIncomplete {
		t.Fatal("approval action summary incomplete = false, want withheld command/content marker")
	}

	step := pause.Step
	if step.Kind != "approval" || step.Status != "awaiting_approval" || step.Phase != "approval" {
		t.Fatalf("approval step shape = %+v", step)
	}
	if step.Index != 7 || step.ApprovalID != approval.ID || step.ToolName != "builtin.agent_loop_approval" {
		t.Fatalf("approval step linkage = %+v, approval id %q", step, approval.ID)
	}
	if approval.StepID != step.ID {
		t.Fatalf("approval StepID = %q, want linked Step %q", approval.StepID, step.ID)
	}
	if got := step.Input["model_call_index"]; got != 2 {
		t.Fatalf("approval step model-call input = %v, want 2", got)
	}
	if _, ok := step.Input["model_call"]; ok {
		t.Fatalf("legacy model_call unexpectedly present: %#v", step.Input)
	}
	if got := step.Input["reason"]; got != approval.Reason {
		t.Fatalf("approval step reason input = %v, want %q", got, approval.Reason)
	}
	if got := step.Input[toolCallBundleDigestKey]; got != agentToolCallBundleDigest(calls) {
		t.Fatalf("approval step bundle digest = %v, want ordered call digest", got)
	}
	if encoded, err := json.Marshal(step.Input); err != nil {
		t.Fatalf("marshal approval Step input: %v", err)
	} else if strings.Contains(string(encoded), `"content":"hi"`) || strings.Contains(string(encoded), `"command":"ls"`) {
		t.Fatalf("approval Step input exposed raw tool arguments: %s", encoded)
	}
	if !step.StartedAt.Equal(when) || !step.FinishedAt.Equal(when) {
		t.Fatalf("approval step timestamps = %s/%s, want %s", step.StartedAt, step.FinishedAt, when)
	}
}

func TestAgentLoopApprovalGate_ActionSummaryShowsAllowlistedReadOnlyGitArgv(t *testing.T) {
	spec := newAgentLoopSpec(t)
	gate := newAgentLoopApprovalGate([]string{"git_exec"})

	pause, ok := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{
		agentLoopToolCall("call-git", "git_exec", `{"command":"branch -vv"}`),
		agentLoopToolCall("call-read", "read_file", `{"path":"README.md"}`),
	})
	if !ok {
		t.Fatal("Evaluate() ok = false, want approval")
	}
	if got, want := pause.Approval.ActionSummary, []string{"git branch -vv", "read_file path=README.md max_bytes=0 lines=0-0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("action summary = %#v, want %#v", got, want)
	}
	if pause.Approval.ActionSummaryIncomplete {
		t.Fatal("allowlisted Git/read summary unexpectedly incomplete")
	}
}

func TestAgentLoopApprovalGate_EvaluateMCPRequireApprovalOnly(t *testing.T) {
	spec := newAgentLoopSpec(t)
	spec.Task.MCPServers = []types.MCPServerConfig{
		{Name: "github", Command: "fake", ApprovalPolicy: types.MCPApprovalRequireApproval},
		{Name: "filesystem", Command: "fake", ApprovalPolicy: types.MCPApprovalAuto},
		{Name: "danger", Command: "fake", ApprovalPolicy: types.MCPApprovalBlock},
	}
	gate := newAgentLoopApprovalGate(nil)

	pause, ok := gate.Evaluate(spec, 1, 4, time.Now().UTC(), []types.ToolCall{
		agentLoopToolCall("call-github", "mcp__github__create_issue", `{}`),
		agentLoopToolCall("call-filesystem", "mcp__filesystem__read_file", `{}`),
		agentLoopToolCall("call-danger", "mcp__danger__delete_repo", `{}`),
		agentLoopToolCall("call-missing", "mcp__missing__do", `{}`),
	})
	if !ok {
		t.Fatalf("Evaluate() ok = false, want true")
	}
	reason := pause.Approval.Reason
	if !strings.Contains(reason, "mcp__github__create_issue") {
		t.Fatalf("approval reason missing require_approval MCP tool: %q", reason)
	}
	for _, notWanted := range []string{"mcp__filesystem__read_file", "mcp__danger__delete_repo", "mcp__missing__do"} {
		if strings.Contains(reason, notWanted) {
			t.Fatalf("approval reason included non-gated MCP tool %q: %q", notWanted, reason)
		}
	}
}

func TestAgentLoopApprovalGate_NoGatedToolsDoesNotPause(t *testing.T) {
	spec := newAgentLoopSpec(t)
	spec.Task.MCPServers = []types.MCPServerConfig{
		{Name: "github", Command: "fake", ApprovalPolicy: types.MCPApprovalBlock},
	}
	gate := newAgentLoopApprovalGate([]string{"shell_exec"})

	if _, ok := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{
		agentLoopToolCall("call-file", "file_write", `{}`),
		agentLoopToolCall("call-blocked-mcp", "mcp__github__create_issue", `{}`),
	}); ok {
		t.Fatalf("Evaluate() ok = true, want false for non-gated and blocked tools")
	}
}

func TestAgentLoopApprovalGate_HardNativePolicyBlocksDoNotPauseForApproval(t *testing.T) {
	spec := newAgentLoopSpec(t)
	toolsEnabled := false
	spec.Task.AgentPresetID = "review_qa"
	spec.Task.AgentPresetToolsEnabled = &toolsEnabled
	spec.Task.SandboxReadOnly = true
	gate := newAgentLoopApprovalGate([]string{
		AgentToolHTTPRequest,
		"shell_exec",
		"git_exec",
		"file_write",
		"file_edit",
		"apply_patch",
		AgentToolTerminalOpen,
	})

	if _, ok := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{
		agentLoopToolCall("call-http", AgentToolHTTPRequest, `{}`),
		agentLoopToolCall("call-shell", "shell_exec", `{}`),
		agentLoopToolCall("call-git", "git_exec", `{}`),
		agentLoopToolCall("call-file", "file_write", `{}`),
		agentLoopToolCall("call-edit", "file_edit", `{"propose":false}`),
		agentLoopToolCall("call-patch", "apply_patch", `{"propose":false}`),
		agentLoopToolCall("call-terminal", AgentToolTerminalOpen, `{}`),
		agentLoopToolCall("call-mcp", "mcp__github__create_issue", `{}`),
	}); ok {
		t.Fatal("Evaluate() ok = true, want hard policy blocks dispatched as denied without approval pause")
	}
}

func TestAgentLoopApprovalGate_QAUnavailableGitEvidenceDoesNotPauseForGitPolicies(t *testing.T) {
	t.Parallel()
	spec := newAgentLoopSpec(t)
	// A retained Run snapshot is authoritative after the mutable Task has been
	// edited. Approval admission must use that same snapshot as dispatch.
	spec.Run.WorkflowMode = types.WorkflowModeQA
	spec.Run.WorkflowVersion = taskworkflow.QAVersion

	for _, policy := range []string{"git_exec", "all_tools"} {
		t.Run(policy, func(t *testing.T) {
			gate := newAgentLoopApprovalGate(agentLoopGatedTools(map[string]struct{}{policy: {}}))
			if _, ok := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{
				agentLoopToolCall("call-status", "git_status", `{}`),
				agentLoopToolCall("call-diff", "git_diff", `{}`),
			}); ok {
				t.Fatalf("%s policy paused QA unavailable Git evidence, want deterministic diagnostic without approval", policy)
			}
		})
	}
}

func TestAgentLoopApprovalGate_ReadOnlyProposalCallsStillHonorGlobalApproval(t *testing.T) {
	spec := newAgentLoopSpec(t)
	spec.Task.SandboxReadOnly = true
	gate := newAgentLoopApprovalGate([]string{"file_edit", "apply_patch"})

	pause, ok := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{
		agentLoopToolCall("call-edit", "file_edit", `{"propose":true}`),
		agentLoopToolCall("call-patch", "apply_patch", `{"propose":true}`),
	})
	if !ok || !strings.Contains(pause.Approval.Reason, "file_edit") || !strings.Contains(pause.Approval.Reason, "apply_patch") {
		t.Fatalf("proposal approval = %+v ok=%v, want both proposal-capable tools gated", pause.Approval, ok)
	}
}

func TestAgentLoopApprovalGate_AgentPresetPolicyComposesWithExistingGates(t *testing.T) {
	t.Parallel()

	assignmentSpec := func(policy string) ExecutionSpec {
		spec := newAgentLoopSpec(t)
		spec.Task.OriginKind = "project_work_item"
		spec.Task.AgentPresetID = "implementation"
		spec.Task.AgentPresetApprovalPolicy = policy
		return spec
	}

	t.Run("require adds a gate to an otherwise ungated call", func(t *testing.T) {
		spec := assignmentSpec(types.AgentPresetApprovalRequire)
		gate := newAgentLoopApprovalGate(nil)
		pause, ok := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{
			agentLoopToolCall("call-read", "read_file", `{"path":"README.md"}`),
		})
		if !ok || !strings.Contains(pause.Approval.Reason, "frozen Agent Preset") {
			t.Fatalf("approval = %+v ok=%v, want preset-required pause", pause.Approval, ok)
		}
	})

	t.Run("allow does not weaken a global gate", func(t *testing.T) {
		spec := assignmentSpec(types.AgentPresetApprovalAllow)
		gate := newAgentLoopApprovalGate([]string{"shell_exec"})
		if _, ok := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{
			agentLoopToolCall("call-shell", "shell_exec", `{"command":"pwd"}`),
		}); !ok {
			t.Fatal("allow policy bypassed the runtime shell approval gate")
		}
	})

	t.Run("block converts a global gate into a hard refusal", func(t *testing.T) {
		spec := assignmentSpec(types.AgentPresetApprovalBlock)
		gate := newAgentLoopApprovalGate([]string{"shell_exec"})
		call := agentLoopToolCall("call-shell", "shell_exec", `{"command":"pwd"}`)
		if _, ok := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{call}); ok {
			t.Fatal("block policy requested approval, want dispatcher refusal")
		}
		if !gate.isBlockedByAgentPreset(call, spec) {
			t.Fatal("block policy did not refuse globally gated call")
		}
	})

	t.Run("block converts an MCP server gate into a hard refusal", func(t *testing.T) {
		spec := assignmentSpec(types.AgentPresetApprovalBlock)
		spec.Task.MCPServers = []types.MCPServerConfig{{
			Name: "github", Command: "fake", ApprovalPolicy: types.MCPApprovalRequireApproval,
		}}
		gate := newAgentLoopApprovalGate(nil)
		call := agentLoopToolCall("call-mcp", "mcp__github__create_issue", `{}`)
		if !gate.isBlockedByAgentPreset(call, spec) {
			t.Fatal("block policy did not refuse MCP require_approval call")
		}
	})

	t.Run("block leaves otherwise permitted calls available", func(t *testing.T) {
		spec := assignmentSpec(types.AgentPresetApprovalBlock)
		spec.Task.MCPServers = []types.MCPServerConfig{{
			Name: "docs", Command: "fake", ApprovalPolicy: types.MCPApprovalAuto,
		}}
		gate := newAgentLoopApprovalGate(nil)
		for _, call := range []types.ToolCall{
			agentLoopToolCall("call-read", "read_file", `{"path":"README.md"}`),
			agentLoopToolCall("call-mcp", "mcp__docs__lookup", `{}`),
		} {
			if gate.isBlockedByAgentPreset(call, spec) || gate.isGated(call, spec) {
				t.Fatalf("otherwise permitted call %q was gated or blocked", call.Function.Name)
			}
		}
	})

	t.Run("legacy non-assignment and QA tasks do not activate a stored value", func(t *testing.T) {
		gate := newAgentLoopApprovalGate(nil)
		call := agentLoopToolCall("call-read", "read_file", `{"path":"README.md"}`)
		legacy := assignmentSpec("")
		manual := assignmentSpec(types.AgentPresetApprovalRequire)
		manual.Task.OriginKind = "manual"
		qa := assignmentSpec(types.AgentPresetApprovalRequire)
		qa.Run.WorkflowMode = types.WorkflowModeQA
		qa.Run.WorkflowVersion = "v0"
		for name, spec := range map[string]ExecutionSpec{"legacy": legacy, "manual": manual, "qa": qa} {
			if gate.isGated(call, spec) || gate.isBlockedByAgentPreset(call, spec) {
				t.Fatalf("%s task unexpectedly activated preset approval policy", name)
			}
		}
	})

	t.Run("invalid persisted value fails safely to approval", func(t *testing.T) {
		spec := assignmentSpec("unexpected")
		gate := newAgentLoopApprovalGate(nil)
		if _, ok := gate.Evaluate(spec, 1, 2, time.Now().UTC(), []types.ToolCall{
			agentLoopToolCall("call-read", "read_file", `{"path":"README.md"}`),
		}); !ok {
			t.Fatal("invalid persisted approval snapshot did not fail safely")
		}
	})

	t.Run("require does not gate a stale unadvertised call", func(t *testing.T) {
		spec := assignmentSpec(types.AgentPresetApprovalRequire)
		gate := newAgentLoopApprovalGate(nil)
		if _, ok := gate.EvaluateAdvertised(spec, 1, 2, time.Now().UTC(), []types.ToolCall{
			agentLoopToolCall("call-stale", "removed_tool", `{}`),
		}, []types.Tool{{Function: types.ToolFunction{Name: "read_file"}}}); ok {
			t.Fatal("preset require paused for a call absent from the run's advertised catalog")
		}
	})
}

func agentLoopToolCall(id, name, args string) types.ToolCall {
	return types.ToolCall{
		ID:   id,
		Type: "function",
		Function: types.ToolCallFunction{
			Name:      name,
			Arguments: args,
		},
	}
}
