package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestAgentLoopToolDispatcher_DispatchShellExecRoutesToShellExecutor(t *testing.T) {
	shell := &stubExecutor{
		result: &ExecutionResult{
			Status: "completed",
			Steps: []types.TaskStep{
				{ID: "sub-step-1", Status: "completed"},
			},
			Artifacts: []types.TaskArtifact{
				{ID: "artifact-1", StepID: "sub-step-1", Kind: "stdout", Name: "stdout.txt", ContentText: "agent-loop-dispatch\n", Status: "ready"},
			},
		},
	}
	dispatcher := &agentLoopToolDispatcher{shell: shell}
	spec := newAgentLoopSpec(t)

	result, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"call-shell",
		"shell_exec",
		`{"command":"printf agent-loop-dispatch","working_directory":"subdir"}`,
	), 5, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if len(shell.calls) != 1 {
		t.Fatalf("shell executor calls = %d, want 1", len(shell.calls))
	}
	gotTask := shell.calls[0]
	if gotTask.ExecutionKind != "shell" || gotTask.ShellCommand != "printf agent-loop-dispatch" || gotTask.WorkingDirectory != "subdir" {
		t.Fatalf("shell task = %+v, want shell command and working directory from tool args", gotTask)
	}

	if result.Step == nil {
		t.Fatalf("Dispatch() Step = nil, want tool step")
	}
	step := *result.Step
	if step.Kind != "tool" || step.Status != "completed" || step.ToolName != "shell_exec" || step.Index != 5 {
		t.Fatalf("tool step = %+v", step)
	}
	if got := step.Input["command"]; got != "printf agent-loop-dispatch" {
		t.Fatalf("step input command = %v, want printf agent-loop-dispatch", got)
	}
	if got := step.Input["working_directory"]; got != "subdir" {
		t.Fatalf("step input working_directory = %v, want subdir", got)
	}
	if len(result.Artifacts) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(result.Artifacts))
	}
	if result.Artifacts[0].StepID != step.ID {
		t.Fatalf("artifact StepID = %q, want dispatcher step ID %q", result.Artifacts[0].StepID, step.ID)
	}
	if !strings.Contains(result.Text, "status=completed") || !strings.Contains(result.Text, "agent-loop-dispatch") {
		t.Fatalf("tool result text = %q, want status and stdout digest", result.Text)
	}
}

func TestAgentLoopToolDispatcher_ShellExecInheritsWorkingDirectoryWhenArgOmitted(t *testing.T) {
	shell := &stubExecutor{result: &ExecutionResult{Status: "completed"}}
	dispatcher := &agentLoopToolDispatcher{shell: shell}
	spec := newAgentLoopSpec(t)
	spec.Task.WorkingDirectory = "/workspace/run"

	_, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"call-shell",
		"shell_exec",
		`{"command":"pwd"}`,
	), 1, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if len(shell.calls) != 1 {
		t.Fatalf("shell executor calls = %d, want 1", len(shell.calls))
	}
	if got := shell.calls[0].WorkingDirectory; got != "/workspace/run" {
		t.Fatalf("shell task WorkingDirectory = %q, want inherited workspace", got)
	}
}

func TestAgentLoopToolDispatcher_InvalidShellArgsReturnsToolErrorText(t *testing.T) {
	shell := &stubExecutor{}
	dispatcher := &agentLoopToolDispatcher{shell: shell}

	result, err := dispatcher.Dispatch(context.Background(), newAgentLoopSpec(t), agentLoopToolCall(
		"call-shell",
		"shell_exec",
		`not-json`,
	), 1, nil, nil)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if len(shell.calls) != 0 {
		t.Fatalf("shell executor calls = %d, want 0 for invalid args", len(shell.calls))
	}
	if result.Step != nil || len(result.Artifacts) != 0 {
		t.Fatalf("invalid args result should not create step/artifacts: %+v", result)
	}
	if !strings.Contains(result.Text, "invalid arguments for shell_exec") {
		t.Fatalf("result text = %q, want invalid arguments", result.Text)
	}
}
