package orchestrator

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAgentLoopTerminalTools_OpenWaitCapturesOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}

	spec := newAgentLoopTerminalSpec(t)
	terminals := newAgentLoopTerminals()
	t.Cleanup(func() { terminals.CloseAll(context.Background()) })

	dispatcher := &agentLoopToolDispatcher{}
	open, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"call-terminal-open",
		AgentToolTerminalOpen,
		`{"command":"sh","args":["-c","printf native-terminal"]}`,
	), 1, nil, terminals)
	if err != nil {
		t.Fatalf("terminal_open Dispatch() error = %v", err)
	}
	if open.Step == nil || open.Step.Status != "completed" || open.Step.ToolName != AgentToolTerminalOpen {
		t.Fatalf("terminal_open step = %+v, want completed", open.Step)
	}
	terminalID, _ := open.Step.OutputSummary["terminal_id"].(string)
	if terminalID == "" {
		t.Fatalf("terminal_open output summary = %+v, want terminal_id", open.Step.OutputSummary)
	}
	if !strings.Contains(open.Text, "terminal_id="+terminalID) {
		t.Fatalf("terminal_open result text = %q, want terminal id", open.Text)
	}

	waitArgs := `{"terminal_id":"` + terminalID + `","timeout_ms":2000}`
	waited, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"call-terminal-wait",
		AgentToolTerminalWait,
		waitArgs,
	), 2, nil, terminals)
	if err != nil {
		t.Fatalf("terminal_wait Dispatch() error = %v", err)
	}
	if waited.Step == nil || waited.Step.Status != "completed" || waited.Step.ToolName != AgentToolTerminalWait {
		t.Fatalf("terminal_wait step = %+v, want completed", waited.Step)
	}
	if !strings.Contains(waited.Text, "running=false") || !strings.Contains(waited.Text, "exit_code=0") || !strings.Contains(waited.Text, "native-terminal") {
		t.Fatalf("terminal_wait result text = %q, want exited output", waited.Text)
	}
}

func TestAgentLoopTerminalTools_WriteReadKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}

	spec := newAgentLoopTerminalSpec(t)
	terminals := newAgentLoopTerminals()
	t.Cleanup(func() { terminals.CloseAll(context.Background()) })

	dispatcher := &agentLoopToolDispatcher{}
	open, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"call-terminal-open",
		AgentToolTerminalOpen,
		`{"command":"cat"}`,
	), 1, nil, terminals)
	if err != nil {
		t.Fatalf("terminal_open Dispatch() error = %v", err)
	}
	terminalID, _ := open.Step.OutputSummary["terminal_id"].(string)
	if terminalID == "" {
		t.Fatalf("terminal_open output summary = %+v, want terminal_id", open.Step.OutputSummary)
	}

	writeArgs := `{"terminal_id":"` + terminalID + `","input":"ping-from-agent\n"}`
	written, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"call-terminal-write",
		AgentToolTerminalWrite,
		writeArgs,
	), 2, nil, terminals)
	if err != nil {
		t.Fatalf("terminal_write Dispatch() error = %v", err)
	}
	if written.Step == nil || written.Step.Status != "completed" {
		t.Fatalf("terminal_write step = %+v, want completed", written.Step)
	}

	var read agentLoopToolDispatchResult
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		readArgs := `{"terminal_id":"` + terminalID + `","max_bytes":4096}`
		read, err = dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
			"call-terminal-read",
			AgentToolTerminalRead,
			readArgs,
		), 3, nil, terminals)
		if err != nil {
			t.Fatalf("terminal_read Dispatch() error = %v", err)
		}
		if strings.Contains(read.Text, "ping-from-agent") {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !strings.Contains(read.Text, "ping-from-agent") {
		t.Fatalf("terminal_read result text = %q, want echoed stdin", read.Text)
	}

	killArgs := `{"terminal_id":"` + terminalID + `"}`
	killed, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"call-terminal-kill",
		AgentToolTerminalKill,
		killArgs,
	), 4, nil, terminals)
	if err != nil {
		t.Fatalf("terminal_kill Dispatch() error = %v", err)
	}
	if killed.Step == nil || killed.Step.Status != "completed" || killed.Step.ToolName != AgentToolTerminalKill {
		t.Fatalf("terminal_kill step = %+v, want completed", killed.Step)
	}
}

func TestAgentLoopTerminalTools_PolicyDeniedReturnsFailedStep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}

	spec := newAgentLoopTerminalSpec(t)
	spec.Task.SandboxReadOnly = true
	terminals := newAgentLoopTerminals()
	t.Cleanup(func() { terminals.CloseAll(context.Background()) })

	dispatcher := &agentLoopToolDispatcher{}
	result, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"call-terminal-open",
		AgentToolTerminalOpen,
		`{"command":"touch","args":["blocked.txt"]}`,
	), 1, nil, terminals)
	if err != nil {
		t.Fatalf("terminal_open Dispatch() error = %v", err)
	}
	if result.Step == nil || result.Step.Status != "failed" || result.Step.ToolName != AgentToolTerminalOpen {
		t.Fatalf("terminal_open denied step = %+v, want failed", result.Step)
	}
	if !strings.Contains(result.Text, "write access is disabled") {
		t.Fatalf("terminal_open denied text = %q, want sandbox policy denial", result.Text)
	}
}

func TestAgentLoopTerminalTools_WaitTimeoutKeepsTerminalRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell semantics")
	}

	spec := newAgentLoopTerminalSpec(t)
	terminals := newAgentLoopTerminals()
	t.Cleanup(func() { terminals.CloseAll(context.Background()) })

	dispatcher := &agentLoopToolDispatcher{}
	open, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"call-terminal-open",
		AgentToolTerminalOpen,
		`{"command":"sleep","args":["2"]}`,
	), 1, nil, terminals)
	if err != nil {
		t.Fatalf("terminal_open Dispatch() error = %v", err)
	}
	terminalID, _ := open.Step.OutputSummary["terminal_id"].(string)
	if terminalID == "" {
		t.Fatalf("terminal_open output summary = %+v, want terminal_id", open.Step.OutputSummary)
	}

	waitArgs := `{"terminal_id":"` + terminalID + `","timeout_ms":50}`
	waited, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"call-terminal-wait",
		AgentToolTerminalWait,
		waitArgs,
	), 2, nil, terminals)
	if err != nil {
		t.Fatalf("terminal_wait Dispatch() error = %v", err)
	}
	if waited.Step == nil || waited.Step.Status != "completed" {
		t.Fatalf("terminal_wait step = %+v, want completed timeout result", waited.Step)
	}
	if waited.Step.OutputSummary["timeout"] != true || !strings.Contains(waited.Text, "running=true") {
		t.Fatalf("terminal_wait timeout summary=%+v text=%q, want running timeout", waited.Step.OutputSummary, waited.Text)
	}
}

func newAgentLoopTerminalSpec(t *testing.T) ExecutionSpec {
	t.Helper()
	spec := newAgentLoopSpec(t)
	dir := t.TempDir()
	spec.Task.WorkingDirectory = dir
	spec.Task.SandboxAllowedRoot = dir
	return spec
}
