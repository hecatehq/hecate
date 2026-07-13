package orchestrator

import (
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestAgentLoopApprovalGate_EvaluateConfiguredToolsDedupesAndBuildsPause(t *testing.T) {
	spec := newAgentLoopSpec(t)
	spec.RequestID = "req-gate"
	spec.TraceID = "trace-gate"
	when := time.Date(2026, 5, 29, 10, 30, 0, 0, time.UTC)

	gate := newAgentLoopApprovalGate([]string{" shell_exec ", "", "shell_exec"})
	pause, ok := gate.Evaluate(spec, 2, 7, when, []types.ToolCall{
		agentLoopToolCall("call-shell-1", "shell_exec", `{"command":"ls"}`),
		agentLoopToolCall("call-file-1", "file_write", `{"path":"out.txt","content":"hi"}`),
		agentLoopToolCall("call-shell-2", "shell_exec", `{"command":"pwd"}`),
	})
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

	step := pause.Step
	if step.Kind != "approval" || step.Status != "awaiting_approval" || step.Phase != "approval" {
		t.Fatalf("approval step shape = %+v", step)
	}
	if step.Index != 7 || step.ApprovalID != approval.ID || step.ToolName != "builtin.agent_loop_approval" {
		t.Fatalf("approval step linkage = %+v, approval id %q", step, approval.ID)
	}
	if got := step.Input["turn"]; got != 2 {
		t.Fatalf("approval step turn input = %v, want 2", got)
	}
	if got := step.Input["reason"]; got != approval.Reason {
		t.Fatalf("approval step reason input = %v, want %q", got, approval.Reason)
	}
	if !step.StartedAt.Equal(when) || !step.FinishedAt.Equal(when) {
		t.Fatalf("approval step timestamps = %s/%s, want %s", step.StartedAt, step.FinishedAt, when)
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
