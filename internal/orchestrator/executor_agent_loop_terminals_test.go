package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/internal/workspace"
	"github.com/hecatehq/hecate/internal/workspacecoord"
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
		done:       make(chan struct{}),
		running:    true,
	}
	terminals := newAgentLoopTerminals()
	terminals.sessions[session.id] = session
	go session.consumeOutput()
	go session.watch()

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

func TestAgentLoopTerminals_WorkspaceLeaseCoversExitAndOutputDrain(t *testing.T) {
	t.Parallel()

	spec := newAgentLoopTerminalSpec(t)
	registry := workspacecoord.NewRegistry()
	term := newControlledAgentLoopTerminal("term-lease")
	terminals := newAgentLoopTerminalsWithCoordinator(registry)
	terminals.openTerminal = func(context.Context, workspace.TerminalOptions) (workspace.Terminal, error) {
		return term, nil
	}
	t.Cleanup(func() { terminals.CloseAll(context.Background()) })

	snap, err := terminals.Open(context.Background(), spec, terminalOpenArgs{Command: "sh"})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	session, ok := terminals.lookup(snap.ID)
	if !ok {
		t.Fatalf("terminal %q was not registered", snap.ID)
	}
	assertWorkspaceBusy(t, registry, spec.Task.SandboxAllowedRoot)

	term.finishProcess(0)
	assertWorkspaceBusy(t, registry, spec.Task.SandboxAllowedRoot)
	term.finishOutput()
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("terminal watcher did not finish after output drained")
	}

	closure, err := registry.TryClose(context.Background(), spec.Task.SandboxAllowedRoot)
	if err != nil {
		t.Fatalf("TryClose() after terminal drain error = %v", err)
	}
	closure.Release()
}

func TestAgentLoopTerminals_RequiresWorkspaceCoordinatorBeforeSpawn(t *testing.T) {
	t.Parallel()

	terminals := newAgentLoopTerminalsWithCoordinator(nil)
	terminals.openTerminal = func(context.Context, workspace.TerminalOptions) (workspace.Terminal, error) {
		t.Fatal("OpenTerminal was called without workspace coordination")
		return nil, nil
	}
	_, err := terminals.Open(context.Background(), newAgentLoopTerminalSpec(t), terminalOpenArgs{Command: "sh"})
	if err == nil || !strings.Contains(err.Error(), "workspace coordination is required") {
		t.Fatalf("Open() error = %v, want required workspace coordinator", err)
	}
}

func TestAgentLoopTerminals_OpenRacingCloseRollsBackTerminalAndLease(t *testing.T) {
	t.Parallel()

	spec := newAgentLoopTerminalSpec(t)
	registry := workspacecoord.NewRegistry()
	term := newControlledAgentLoopTerminal("term-raced-open")
	terminals := newAgentLoopTerminalsWithCoordinator(registry)
	openEntered := make(chan struct{})
	releaseOpen := make(chan struct{})
	terminals.openTerminal = func(context.Context, workspace.TerminalOptions) (workspace.Terminal, error) {
		close(openEntered)
		<-releaseOpen
		return term, nil
	}

	openResult := make(chan error, 1)
	go func() {
		_, err := terminals.Open(context.Background(), spec, terminalOpenArgs{Command: "sh"})
		openResult <- err
	}()
	select {
	case <-openEntered:
	case <-time.After(time.Second):
		t.Fatal("Open() did not reach the spawn seam")
	}
	assertWorkspaceBusy(t, registry, spec.Task.SandboxAllowedRoot)

	closeResult := make(chan struct{})
	go func() {
		terminals.CloseAll(context.Background())
		close(closeResult)
	}()
	waitForAgentTerminalManagerClosed(t, terminals)
	close(releaseOpen)

	select {
	case err := <-openResult:
		if err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("Open() error = %v, want closed-manager error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Open() did not roll back after CloseAll()")
	}
	select {
	case <-term.closeCalled:
	case <-time.After(time.Second):
		t.Fatal("raced terminal was not closed during rollback")
	}
	select {
	case <-closeResult:
	case <-time.After(time.Second):
		t.Fatal("CloseAll() did not wait for the in-flight start to roll back")
	}

	terminals.mu.Lock()
	remaining := len(terminals.sessions)
	terminals.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("registered sessions after raced close = %d, want 0", remaining)
	}
	closure, err := registry.TryClose(context.Background(), spec.Task.SandboxAllowedRoot)
	if err != nil {
		t.Fatalf("TryClose() after raced start rollback error = %v", err)
	}
	closure.Release()
}

func TestAgentLoopExecutor_CloseAllTerminalsFencesRetainedRuns(t *testing.T) {
	t.Parallel()

	spec := newAgentLoopTerminalSpec(t)
	executor := &AgentLoopExecutor{
		terminalRuns:   make(map[string]*agentLoopTerminals),
		workspaceCoord: workspacecoord.NewRegistry(),
	}
	term := newControlledAgentLoopTerminal("term-awaiting")
	terminals := executor.terminalSessionsForRun("run-awaiting-approval")
	terminals.openTerminal = func(context.Context, workspace.TerminalOptions) (workspace.Terminal, error) {
		return term, nil
	}
	if _, err := terminals.Open(context.Background(), spec, terminalOpenArgs{Command: "sh"}); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	executor.CloseAllTerminals(context.Background())
	select {
	case <-term.closeCalled:
	case <-time.After(time.Second):
		t.Fatal("retained run terminal was not closed")
	}
	executor.terminalMu.Lock()
	remaining := len(executor.terminalRuns)
	closed := executor.terminalClosed
	executor.terminalMu.Unlock()
	if !closed || remaining != 0 {
		t.Fatalf("terminal executor state = closed %v, runs %d; want closed with no retained runs", closed, remaining)
	}

	fenced := executor.terminalSessionsForRun("run-after-shutdown")
	if _, err := fenced.Open(context.Background(), spec, terminalOpenArgs{Command: "sh"}); err == nil {
		t.Fatal("Open() after executor shutdown succeeded, want fenced manager")
	}
}

func TestAgentLoopTerminals_ExpiredCloseRetainsBackgroundCleanup(t *testing.T) {
	t.Parallel()

	spec := newAgentLoopTerminalSpec(t)
	registry := workspacecoord.NewRegistry()
	term := newRetryCloseAgentLoopTerminal("term-expired-close")
	terminals := newAgentLoopTerminalsWithCoordinator(registry)
	terminals.openTerminal = func(context.Context, workspace.TerminalOptions) (workspace.Terminal, error) {
		return term, nil
	}
	if _, err := terminals.Open(context.Background(), spec, terminalOpenArgs{Command: "sh"}); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	terminals.CloseAll(ctx)
	select {
	case <-term.cancelledClose:
	case <-time.After(time.Second):
		t.Fatal("CloseAll() did not pass the exhausted shutdown context to the terminal")
	}
	select {
	case <-term.backgroundClose:
	case <-time.After(time.Second):
		t.Fatal("terminal cleanup was not retained after the shutdown context expired")
	}

	deadline := time.Now().Add(time.Second)
	for {
		closure, err := registry.TryClose(context.Background(), spec.Task.SandboxAllowedRoot)
		if err == nil {
			closure.Release()
			break
		}
		if !errors.Is(err, workspacecoord.ErrBusy) {
			t.Fatalf("TryClose() after background cleanup error = %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("workspace lease remained after retained terminal cleanup")
		}
		time.Sleep(10 * time.Millisecond)
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

type controlledAgentLoopTerminal struct {
	id          string
	output      chan workspace.OutputChunk
	processDone chan struct{}
	closeCalled chan struct{}

	processOnce sync.Once
	outputOnce  sync.Once
	closeOnce   sync.Once
	mu          sync.Mutex
	exitCode    int
}

func newControlledAgentLoopTerminal(id string) *controlledAgentLoopTerminal {
	return &controlledAgentLoopTerminal{
		id:          id,
		output:      make(chan workspace.OutputChunk),
		processDone: make(chan struct{}),
		closeCalled: make(chan struct{}),
	}
}

func (term *controlledAgentLoopTerminal) ID() string { return term.id }

func (term *controlledAgentLoopTerminal) Output() <-chan workspace.OutputChunk { return term.output }

func (*controlledAgentLoopTerminal) Write(context.Context, string) error { return nil }

func (term *controlledAgentLoopTerminal) WaitForExit(ctx context.Context) (workspace.Result, error) {
	select {
	case <-term.processDone:
		term.mu.Lock()
		exitCode := term.exitCode
		term.mu.Unlock()
		return workspace.Result{ExitCode: exitCode}, nil
	case <-ctx.Done():
		return workspace.Result{}, ctx.Err()
	}
}

func (term *controlledAgentLoopTerminal) Kill(context.Context) error {
	term.finishProcess(143)
	term.finishOutput()
	return nil
}

func (term *controlledAgentLoopTerminal) Close(context.Context) error {
	term.closeOnce.Do(func() {
		close(term.closeCalled)
		term.finishProcess(143)
		term.finishOutput()
	})
	return nil
}

func (term *controlledAgentLoopTerminal) finishProcess(exitCode int) {
	term.processOnce.Do(func() {
		term.mu.Lock()
		term.exitCode = exitCode
		term.mu.Unlock()
		close(term.processDone)
	})
}

func (term *controlledAgentLoopTerminal) finishOutput() {
	term.outputOnce.Do(func() { close(term.output) })
}

func assertWorkspaceBusy(t *testing.T, registry *workspacecoord.Registry, workspaceRoot string) {
	t.Helper()
	closure, err := registry.TryClose(context.Background(), workspaceRoot)
	if closure != nil {
		closure.Release()
	}
	if !errors.Is(err, workspacecoord.ErrBusy) {
		t.Fatalf("TryClose() error = %v, want workspacecoord.ErrBusy", err)
	}
}

func waitForAgentTerminalManagerClosed(t *testing.T, terminals *agentLoopTerminals) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		terminals.mu.Lock()
		closed := terminals.closed
		terminals.mu.Unlock()
		if closed {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("terminal manager was not fenced")
}

type retryCloseAgentLoopTerminal struct {
	*controlledAgentLoopTerminal
	cancelledClose  chan struct{}
	backgroundClose chan struct{}
	cancelOnce      sync.Once
	backgroundOnce  sync.Once
}

func newRetryCloseAgentLoopTerminal(id string) *retryCloseAgentLoopTerminal {
	return &retryCloseAgentLoopTerminal{
		controlledAgentLoopTerminal: newControlledAgentLoopTerminal(id),
		cancelledClose:              make(chan struct{}),
		backgroundClose:             make(chan struct{}),
	}
}

func (term *retryCloseAgentLoopTerminal) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		term.cancelOnce.Do(func() { close(term.cancelledClose) })
		return err
	}
	term.backgroundOnce.Do(func() { close(term.backgroundClose) })
	term.finishProcess(143)
	term.finishOutput()
	return nil
}
