package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/internal/workspace"
	"github.com/hecatehq/hecate/pkg/types"
)

const (
	agentLoopTerminalDefaultReadBytes = 8 * 1024
	agentLoopTerminalMaxReadBytes     = 64 * 1024
	agentLoopTerminalOutputLimitBytes = 64 * 1024
	agentLoopTerminalDefaultWait      = 10 * time.Second
	agentLoopTerminalMaxWait          = 60 * time.Second
)

type agentLoopTerminals struct {
	mu       sync.Mutex
	sessions map[string]*agentLoopTerminalSession
}

type agentLoopTerminalSession struct {
	id         string
	nativeID   string
	command    string
	args       []string
	workingDir string
	terminal   workspace.Terminal
	outputDone chan struct{}

	mu        sync.Mutex
	output    []byte
	truncated bool
	running   bool
	exitCode  *int
	errText   string
	closed    bool
}

type agentLoopTerminalSnapshot struct {
	ID               string
	NativeID         string
	Command          string
	Args             []string
	WorkingDirectory string
	Output           string
	OutputBytes      int
	Truncated        bool
	Running          bool
	ExitCode         *int
	Error            string
}

func newAgentLoopTerminals() *agentLoopTerminals {
	return &agentLoopTerminals{sessions: make(map[string]*agentLoopTerminalSession)}
}

func (e *AgentLoopExecutor) terminalSessionsForRun(runID string) *agentLoopTerminals {
	key := strings.TrimSpace(runID)
	if key == "" {
		key = "run"
	}
	e.terminalMu.Lock()
	defer e.terminalMu.Unlock()
	if e.terminalRuns == nil {
		e.terminalRuns = make(map[string]*agentLoopTerminals)
	}
	sessions := e.terminalRuns[key]
	if sessions == nil {
		sessions = newAgentLoopTerminals()
		e.terminalRuns[key] = sessions
	}
	return sessions
}

func (e *AgentLoopExecutor) closeTerminalSessionsForRun(ctx context.Context, runID string) {
	key := strings.TrimSpace(runID)
	if key == "" {
		key = "run"
	}
	e.terminalMu.Lock()
	sessions := e.terminalRuns[key]
	delete(e.terminalRuns, key)
	e.terminalMu.Unlock()
	if sessions != nil {
		sessions.CloseAll(ctx)
	}
}

func (e *AgentLoopExecutor) CloseTerminalsForRun(ctx context.Context, runID string) {
	e.closeTerminalSessionsForRun(ctx, runID)
}

func dispatchTerminalOpenTool(ctx context.Context, spec ExecutionSpec, args terminalOpenArgs, stepIndex int, startedAt time.Time, toolCallID, toolName string, terminals *agentLoopTerminals) (string, *types.TaskStep, []types.TaskArtifact, error) {
	if terminals == nil {
		return terminalToolFailure(spec, stepIndex, startedAt, toolName, map[string]any{"command": args.Command}, "terminal manager is not configured")
	}
	snap, err := terminals.Open(ctx, spec, args)
	if err != nil {
		return terminalToolFailure(spec, stepIndex, startedAt, toolName, terminalOpenInput(args), fmt.Sprintf("terminal_open: %v", err))
	}
	return terminalToolSuccess(spec, stepIndex, startedAt, toolCallID, toolName, terminalOpenInput(args), snap, false)
}

func dispatchTerminalWriteTool(ctx context.Context, spec ExecutionSpec, args terminalWriteArgs, stepIndex int, startedAt time.Time, toolName string, terminals *agentLoopTerminals) (string, *types.TaskStep, []types.TaskArtifact, error) {
	if strings.TrimSpace(args.TerminalID) == "" {
		return terminalToolFailure(spec, stepIndex, startedAt, toolName, map[string]any{"input_chars": len(args.Input)}, "terminal_write: terminal_id is required")
	}
	if terminals == nil {
		return terminalToolFailure(spec, stepIndex, startedAt, toolName, terminalIDInput(args.TerminalID), "terminal manager is not configured")
	}
	snap, err := terminals.Write(ctx, args.TerminalID, args.Input)
	if err != nil {
		return terminalToolFailure(spec, stepIndex, startedAt, toolName, terminalIDInput(args.TerminalID), fmt.Sprintf("terminal_write: %v", err))
	}
	input := terminalIDInput(args.TerminalID)
	input["input_chars"] = len(args.Input)
	return terminalToolSuccess(spec, stepIndex, startedAt, "", toolName, input, snap, false)
}

func dispatchTerminalReadTool(spec ExecutionSpec, args terminalReadArgs, stepIndex int, startedAt time.Time, toolName string, terminals *agentLoopTerminals) (string, *types.TaskStep, []types.TaskArtifact, error) {
	if strings.TrimSpace(args.TerminalID) == "" {
		return terminalToolFailure(spec, stepIndex, startedAt, toolName, nil, "terminal_read: terminal_id is required")
	}
	if terminals == nil {
		return terminalToolFailure(spec, stepIndex, startedAt, toolName, terminalIDInput(args.TerminalID), "terminal manager is not configured")
	}
	snap, err := terminals.Read(args.TerminalID, args.MaxBytes)
	if err != nil {
		return terminalToolFailure(spec, stepIndex, startedAt, toolName, terminalIDInput(args.TerminalID), fmt.Sprintf("terminal_read: %v", err))
	}
	return terminalToolSuccess(spec, stepIndex, startedAt, "", toolName, terminalIDInput(args.TerminalID), snap, false)
}

func dispatchTerminalWaitTool(ctx context.Context, spec ExecutionSpec, args terminalWaitArgs, stepIndex int, startedAt time.Time, toolName string, terminals *agentLoopTerminals) (string, *types.TaskStep, []types.TaskArtifact, error) {
	if strings.TrimSpace(args.TerminalID) == "" {
		return terminalToolFailure(spec, stepIndex, startedAt, toolName, nil, "terminal_wait: terminal_id is required")
	}
	if terminals == nil {
		return terminalToolFailure(spec, stepIndex, startedAt, toolName, terminalIDInput(args.TerminalID), "terminal manager is not configured")
	}
	snap, timedOut, err := terminals.Wait(ctx, args.TerminalID, args.TimeoutMS)
	if err != nil {
		return terminalToolFailure(spec, stepIndex, startedAt, toolName, terminalIDInput(args.TerminalID), fmt.Sprintf("terminal_wait: %v", err))
	}
	input := terminalIDInput(args.TerminalID)
	input["timeout_ms"] = normalizeTerminalWait(args.TimeoutMS).Milliseconds()
	return terminalToolSuccess(spec, stepIndex, startedAt, "", toolName, input, snap, timedOut)
}

func dispatchTerminalKillTool(ctx context.Context, spec ExecutionSpec, args terminalKillArgs, stepIndex int, startedAt time.Time, toolName string, terminals *agentLoopTerminals) (string, *types.TaskStep, []types.TaskArtifact, error) {
	if strings.TrimSpace(args.TerminalID) == "" {
		return terminalToolFailure(spec, stepIndex, startedAt, toolName, nil, "terminal_kill: terminal_id is required")
	}
	if terminals == nil {
		return terminalToolFailure(spec, stepIndex, startedAt, toolName, terminalIDInput(args.TerminalID), "terminal manager is not configured")
	}
	snap, err := terminals.Kill(ctx, args.TerminalID)
	if err != nil {
		return terminalToolFailure(spec, stepIndex, startedAt, toolName, terminalIDInput(args.TerminalID), fmt.Sprintf("terminal_kill: %v", err))
	}
	return terminalToolSuccess(spec, stepIndex, startedAt, "", toolName, terminalIDInput(args.TerminalID), snap, false)
}

func (t *agentLoopTerminals) Open(ctx context.Context, spec ExecutionSpec, args terminalOpenArgs) (agentLoopTerminalSnapshot, error) {
	root, err := agentLoopTerminalWorkspaceRoot(spec)
	if err != nil {
		return agentLoopTerminalSnapshot{}, err
	}
	policy := taskPolicy(spec)
	if strings.TrimSpace(policy.AllowedRoot) == "" {
		policy.AllowedRoot = root
	}
	term, err := workspace.NewLocalWorkspace().OpenTerminal(ctx, workspace.TerminalOptions{
		Command:          strings.TrimSpace(args.Command),
		Args:             args.Args,
		WorkingDirectory: args.WorkingDirectory,
		Policy:           policy,
	})
	if err != nil {
		return agentLoopTerminalSnapshot{}, err
	}
	id := spec.NewID("terminal")
	session := &agentLoopTerminalSession{
		id:         id,
		nativeID:   term.ID(),
		command:    strings.TrimSpace(args.Command),
		args:       append([]string(nil), args.Args...),
		workingDir: firstNonEmpty(args.WorkingDirectory, "."),
		terminal:   term,
		outputDone: make(chan struct{}),
		running:    true,
	}
	t.mu.Lock()
	t.sessions[id] = session
	t.mu.Unlock()
	go session.consumeOutput()
	return session.Snapshot(agentLoopTerminalDefaultReadBytes), nil
}

func (t *agentLoopTerminals) Write(ctx context.Context, id, input string) (agentLoopTerminalSnapshot, error) {
	session, ok := t.lookup(id)
	if !ok {
		return agentLoopTerminalSnapshot{}, fmt.Errorf("terminal %q not found", strings.TrimSpace(id))
	}
	if err := session.terminal.Write(ctx, input); err != nil {
		return session.Snapshot(agentLoopTerminalDefaultReadBytes), err
	}
	return session.Snapshot(agentLoopTerminalDefaultReadBytes), nil
}

func (t *agentLoopTerminals) Read(id string, maxBytes int) (agentLoopTerminalSnapshot, error) {
	session, ok := t.lookup(id)
	if !ok {
		return agentLoopTerminalSnapshot{}, fmt.Errorf("terminal %q not found", strings.TrimSpace(id))
	}
	return session.Snapshot(normalizeTerminalReadBytes(maxBytes)), nil
}

func (t *agentLoopTerminals) Wait(ctx context.Context, id string, timeoutMS int) (agentLoopTerminalSnapshot, bool, error) {
	session, ok := t.lookup(id)
	if !ok {
		return agentLoopTerminalSnapshot{}, false, fmt.Errorf("terminal %q not found", strings.TrimSpace(id))
	}
	timeout := normalizeTerminalWait(timeoutMS)
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := session.terminal.WaitForExit(waitCtx)
	if err != nil {
		if waitCtx.Err() != nil {
			return session.Snapshot(agentLoopTerminalDefaultReadBytes), true, nil
		}
		session.markDone(result.ExitCode, err)
		session.waitOutputDrained(waitCtx)
		return session.Snapshot(agentLoopTerminalDefaultReadBytes), false, err
	}
	session.markDone(result.ExitCode, nil)
	session.waitOutputDrained(waitCtx)
	return session.Snapshot(agentLoopTerminalDefaultReadBytes), false, nil
}

func (t *agentLoopTerminals) Kill(ctx context.Context, id string) (agentLoopTerminalSnapshot, error) {
	session, ok := t.lookup(id)
	if !ok {
		return agentLoopTerminalSnapshot{}, fmt.Errorf("terminal %q not found", strings.TrimSpace(id))
	}
	if err := session.terminal.Kill(ctx); err != nil {
		return session.Snapshot(agentLoopTerminalDefaultReadBytes), err
	}
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := session.terminal.WaitForExit(waitCtx)
	if err == nil {
		session.markDone(result.ExitCode, nil)
		session.waitOutputDrained(waitCtx)
	} else if waitCtx.Err() == nil {
		session.markDone(result.ExitCode, err)
		session.waitOutputDrained(waitCtx)
	}
	return session.Snapshot(agentLoopTerminalDefaultReadBytes), nil
}

func (t *agentLoopTerminals) CloseAll(ctx context.Context) {
	t.mu.Lock()
	sessions := make([]*agentLoopTerminalSession, 0, len(t.sessions))
	for id, session := range t.sessions {
		sessions = append(sessions, session)
		delete(t.sessions, id)
	}
	t.mu.Unlock()
	for _, session := range sessions {
		session.close(ctx)
	}
}

func (t *agentLoopTerminals) lookup(id string) (*agentLoopTerminalSession, bool) {
	id = strings.TrimSpace(id)
	t.mu.Lock()
	defer t.mu.Unlock()
	session, ok := t.sessions[id]
	return session, ok
}

func (s *agentLoopTerminalSession) consumeOutput() {
	if s.outputDone != nil {
		defer close(s.outputDone)
	}
	for chunk := range s.terminal.Output() {
		s.appendOutput(chunk)
	}
}

func (s *agentLoopTerminalSession) waitOutputDrained(ctx context.Context) {
	if s.outputDone == nil {
		return
	}
	select {
	case <-s.outputDone:
	case <-ctx.Done():
	}
}

func (s *agentLoopTerminalSession) appendOutput(chunk workspace.OutputChunk) {
	text := chunk.Text
	if chunk.Stream == "stderr" {
		text = prefixTerminalStderr(text)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.output = append(s.output, []byte(text)...)
	if len(s.output) > agentLoopTerminalOutputLimitBytes {
		s.output = append([]byte(nil), s.output[len(s.output)-agentLoopTerminalOutputLimitBytes:]...)
		for len(s.output) > 0 && !utf8.Valid(s.output) {
			s.output = s.output[1:]
		}
		s.truncated = true
	}
}

func (s *agentLoopTerminalSession) markDone(exitCode int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	s.exitCode = &exitCode
	if err != nil {
		s.errText = err.Error()
	}
}

func (s *agentLoopTerminalSession) close(ctx context.Context) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	_ = s.terminal.Close(ctx)
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
}

func (s *agentLoopTerminalSession) Snapshot(maxBytes int) agentLoopTerminalSnapshot {
	maxBytes = normalizeTerminalReadBytes(maxBytes)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.output
	truncated := s.truncated
	if len(out) > maxBytes {
		out = out[len(out)-maxBytes:]
		for len(out) > 0 && !utf8.Valid(out) {
			out = out[1:]
		}
		truncated = true
	}
	return agentLoopTerminalSnapshot{
		ID:               s.id,
		NativeID:         s.nativeID,
		Command:          s.command,
		Args:             append([]string(nil), s.args...),
		WorkingDirectory: s.workingDir,
		Output:           string(out),
		OutputBytes:      len(out),
		Truncated:        truncated,
		Running:          s.running,
		ExitCode:         copyIntPtr(s.exitCode),
		Error:            s.errText,
	}
}

func agentLoopTerminalWorkspaceRoot(spec ExecutionSpec) (string, error) {
	root := strings.TrimSpace(spec.Task.SandboxAllowedRoot)
	if root == "" {
		root = strings.TrimSpace(spec.Task.WorkingDirectory)
	}
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace %q is not a directory", abs)
	}
	return abs, nil
}

func terminalToolSuccess(spec ExecutionSpec, stepIndex int, startedAt time.Time, toolCallID, toolName string, input map[string]any, snap agentLoopTerminalSnapshot, timedOut bool) (string, *types.TaskStep, []types.TaskArtifact, error) {
	finishedAt := time.Now().UTC()
	step := types.TaskStep{
		ID:         spec.NewID("step"),
		TaskID:     spec.Task.ID,
		RunID:      spec.Run.ID,
		Index:      stepIndex,
		Kind:       "tool",
		Title:      fmt.Sprintf("%s (%s)", toolName, terminalStatus(snap, timedOut)),
		Status:     "completed",
		Phase:      "execution",
		Result:     telemetry.ResultSuccess,
		ToolName:   toolName,
		Input:      input,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
	}
	step.OutputSummary = terminalOutputSummary(snap, timedOut)
	_ = toolCallID
	return terminalToolText(snap, timedOut), &step, nil, nil
}

func terminalToolFailure(spec ExecutionSpec, stepIndex int, startedAt time.Time, toolName string, input map[string]any, message string) (string, *types.TaskStep, []types.TaskArtifact, error) {
	finishedAt := time.Now().UTC()
	step := types.TaskStep{
		ID:         spec.NewID("step"),
		TaskID:     spec.Task.ID,
		RunID:      spec.Run.ID,
		Index:      stepIndex,
		Kind:       "tool",
		Title:      fmt.Sprintf("%s (failed)", toolName),
		Status:     "failed",
		Phase:      "execution",
		Result:     telemetry.ResultError,
		ToolName:   toolName,
		Input:      input,
		Error:      message,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
	}
	step.OutputSummary = map[string]any{"error": message}
	return message, &step, nil, nil
}

func terminalOpenInput(args terminalOpenArgs) map[string]any {
	return map[string]any{
		"command":           strings.TrimSpace(args.Command),
		"args":              append([]string(nil), args.Args...),
		"working_directory": args.WorkingDirectory,
	}
}

func terminalIDInput(id string) map[string]any {
	return map[string]any{"terminal_id": strings.TrimSpace(id)}
}

func terminalOutputSummary(snap agentLoopTerminalSnapshot, timedOut bool) map[string]any {
	out := map[string]any{
		"terminal_id":  snap.ID,
		"running":      snap.Running,
		"output_bytes": snap.OutputBytes,
		"truncated":    snap.Truncated,
	}
	if snap.ExitCode != nil {
		out["exit_code"] = *snap.ExitCode
	}
	if snap.Error != "" {
		out["error"] = snap.Error
	}
	if timedOut {
		out["timeout"] = true
	}
	return out
}

func terminalToolText(snap agentLoopTerminalSnapshot, timedOut bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "terminal_id=%s running=%v", snap.ID, snap.Running)
	if snap.ExitCode != nil {
		fmt.Fprintf(&b, " exit_code=%d", *snap.ExitCode)
	}
	if timedOut {
		b.WriteString(" timeout=true")
	}
	if snap.Truncated {
		b.WriteString(" truncated=true")
	}
	if snap.Error != "" {
		fmt.Fprintf(&b, "\nerror=%s", snap.Error)
	}
	if snap.Output != "" {
		b.WriteString("\n--- output ---\n")
		b.WriteString(snap.Output)
	}
	return b.String()
}

func terminalStatus(snap agentLoopTerminalSnapshot, timedOut bool) string {
	if timedOut {
		return "running"
	}
	if snap.Running {
		return "started"
	}
	return "exited"
}

func normalizeTerminalReadBytes(n int) int {
	if n <= 0 {
		return agentLoopTerminalDefaultReadBytes
	}
	if n > agentLoopTerminalMaxReadBytes {
		return agentLoopTerminalMaxReadBytes
	}
	return n
}

func normalizeTerminalWait(timeoutMS int) time.Duration {
	if timeoutMS <= 0 {
		return agentLoopTerminalDefaultWait
	}
	timeout := time.Duration(timeoutMS) * time.Millisecond
	if timeout > agentLoopTerminalMaxWait {
		return agentLoopTerminalMaxWait
	}
	return timeout
}

func prefixTerminalStderr(text string) string {
	if text == "" {
		return text
	}
	lines := strings.SplitAfter(text, "\n")
	var b strings.Builder
	for _, line := range lines {
		if line == "" {
			continue
		}
		b.WriteString("[stderr] ")
		b.WriteString(line)
	}
	return b.String()
}

func copyIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}
