package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/internal/workspace"
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

func TestAgentLoopTerminalTools_ReadOnlyPolicyBlocksInteractiveShellAndStdin(t *testing.T) {
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
		`{}`,
	), 1, nil, terminals)
	if err != nil {
		t.Fatalf("terminal_open Dispatch() error = %v", err)
	}
	if result.Step == nil || result.Step.Status != "completed" || result.Step.Result != telemetry.ResultDenied || result.Step.ToolName != AgentToolTerminalOpen || !result.ToolError {
		t.Fatalf("terminal_open denied step = %+v tool_error=%v, want completed policy decision with denied result", result.Step, result.ToolError)
	}
	if !strings.Contains(result.Text, "workspace writes are disabled") {
		t.Fatalf("terminal_open denied text = %q, want sandbox policy denial", result.Text)
	}

	written, err := dispatcher.Dispatch(context.Background(), spec, agentLoopToolCall(
		"call-terminal-write",
		AgentToolTerminalWrite,
		`{"terminal_id":"forged","input":"echo bypass > blocked.txt\n"}`,
	), 2, nil, terminals)
	if err != nil {
		t.Fatalf("terminal_write Dispatch() error = %v", err)
	}
	if written.Step == nil || written.Step.Result != telemetry.ResultDenied || !written.ToolError {
		t.Fatalf("terminal_write denied result = %+v tool_error=%v, want policy denial", written.Step, written.ToolError)
	}
	terminals.mu.Lock()
	openSessions := len(terminals.sessions)
	terminals.mu.Unlock()
	if openSessions != 0 {
		t.Fatalf("read-only terminal sessions = %d, want none", openSessions)
	}
	if _, err := os.Stat(filepath.Join(spec.Task.WorkingDirectory, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("blocked terminal sequence created file: %v", err)
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

func TestAgentLoopTerminalTools_WaitDrainsOutputBeforeSnapshot(t *testing.T) {
	term := &waitDrainTerminal{
		output: make(chan workspace.OutputChunk),
		waited: make(chan struct{}),
	}
	session := &agentLoopTerminalSession{
		id:         "terminal-1",
		nativeID:   "term_1",
		command:    "sh",
		terminal:   term,
		outputDone: make(chan struct{}),
		running:    true,
	}
	terminals := newAgentLoopTerminals()
	terminals.sessions[session.id] = session
	go session.consumeOutput()

	type waitResult struct {
		snap     agentLoopTerminalSnapshot
		timedOut bool
		err      error
	}
	done := make(chan waitResult, 1)
	go func() {
		snap, timedOut, err := terminals.Wait(context.Background(), session.id, 2000)
		done <- waitResult{snap: snap, timedOut: timedOut, err: err}
	}()

	<-term.waited
	select {
	case result := <-done:
		t.Fatalf("Wait returned before output drained: snap=%+v timedOut=%v err=%v", result.snap, result.timedOut, result.err)
	default:
	}

	term.output <- workspace.OutputChunk{Stream: "stdout", Text: "drained-output\n"}
	close(term.output)

	select {
	case result := <-done:
		if result.err != nil || result.timedOut {
			t.Fatalf("Wait result err=%v timedOut=%v, want clean exit", result.err, result.timedOut)
		}
		if result.snap.Running || result.snap.ExitCode == nil || *result.snap.ExitCode != 0 {
			t.Fatalf("Wait snapshot = %+v, want exited terminal", result.snap)
		}
		if !strings.Contains(result.snap.Output, "drained-output") {
			t.Fatalf("Wait snapshot output = %q, want drained output", result.snap.Output)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after output channel drained")
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

type waitDrainTerminal struct {
	output chan workspace.OutputChunk
	waited chan struct{}
}

func (t *waitDrainTerminal) ID() string { return "term_1" }

func (t *waitDrainTerminal) Output() <-chan workspace.OutputChunk { return t.output }

func (t *waitDrainTerminal) Write(context.Context, string) error { return nil }

func (t *waitDrainTerminal) WaitForExit(context.Context) (workspace.Result, error) {
	close(t.waited)
	return workspace.Result{ExitCode: 0}, nil
}

func (t *waitDrainTerminal) Kill(context.Context) error { return nil }

func (t *waitDrainTerminal) Close(context.Context) error { return nil }
