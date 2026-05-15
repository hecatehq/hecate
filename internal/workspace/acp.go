package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Caller is the narrow surface of acp.Dispatcher that ACPWorkspace
// consumes. Defined here so the workspace package depends on the
// behavior (send a JSON-RPC call, await a response) rather than the
// full Dispatcher type — and so tests can mock outbound RPC without
// spinning up an entire bridge.
//
// In production, ACPWorkspace is constructed with *acp.Dispatcher as
// the Caller. The dispatcher's Call method blocks until the editor
// answers or ctx is cancelled.
type Caller interface {
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
}

// ACPWorkspace is the editor-owned implementation. Every file or
// process operation dispatches to the connected ACP editor as a
// reverse-RPC call — fs/write_text_file, terminal/create, and so on
// — so the editor's native filesystem and terminal subsystems own
// the actual side effects. The Hecate sandbox is bypassed in this
// mode by design: the editor enforces its own permission model
// (Zed's project-trust gates, JetBrains' AI assistant prompts, …).
//
// One ACPWorkspace per session — the session id rides in every
// outbound params payload because ACP's reverse-RPC surface is
// session-scoped. Constructing one without a sessionID produces a
// workspace that returns errors on every method; this is intentional
// so the wiring step (HECATE_WORKSPACE_MODE=editor-owned) fails
// loudly rather than silently corrupting state.
type ACPWorkspace struct {
	caller    Caller
	sessionID string
}

// NewACPWorkspace constructs the editor-owned workspace. sessionID is
// the ACP session id carried in every outbound params payload —
// editors route the call to the matching session on their side.
func NewACPWorkspace(caller Caller, sessionID string) *ACPWorkspace {
	return &ACPWorkspace{caller: caller, sessionID: sessionID}
}

// Compile-time interface conformance — same guard LocalWorkspace
// carries. If a future change drifts the method shapes, the build
// catches it instead of the runtime.
var (
	_ Workspace = (*ACPWorkspace)(nil)
	_ Permitter = (*ACPWorkspace)(nil)
)

// ---------------------------------------------------------------------------
// File operations
// ---------------------------------------------------------------------------

type acpFSWriteParams struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Content   string `json:"content"`
}

type acpFSReadParams struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
}

type acpFSReadResult struct {
	Content string `json:"content"`
}

// WriteFile dispatches an fs/write_text_file reverse-RPC. The editor
// is responsible for resolving the path against its project root,
// running its own permission prompts if necessary, and writing the
// file through its native APIs (which integrates with editor undo,
// buffer reload, and file watchers).
func (w *ACPWorkspace) WriteFile(ctx context.Context, req FileRequest) (FileResult, error) {
	if err := w.ensureWired(); err != nil {
		return FileResult{}, err
	}
	resolvedPath := joinWorkspacePath(req.WorkingDirectory, req.Path)
	if _, err := w.caller.Call(ctx, "fs/write_text_file", acpFSWriteParams{
		SessionID: w.sessionID,
		Path:      resolvedPath,
		Content:   req.Content,
	}); err != nil {
		return FileResult{}, err
	}
	return FileResult{
		Path:         resolvedPath,
		BytesWritten: len(req.Content),
	}, nil
}

// AppendFile is read + concatenate + write. ACP doesn't define an
// append primitive (fs/append_text_file isn't part of the spec), so
// we round-trip through fs/read_text_file. Slower than the local
// path, but operators rarely append in the editor-owned scenario.
func (w *ACPWorkspace) AppendFile(ctx context.Context, req FileRequest) (FileResult, error) {
	if err := w.ensureWired(); err != nil {
		return FileResult{}, err
	}
	resolvedPath := joinWorkspacePath(req.WorkingDirectory, req.Path)
	existing, err := w.readFile(ctx, resolvedPath)
	// fs/read_text_file returns a not-found error for missing files;
	// treat as empty so callers don't have to special-case append-on-new.
	if err != nil && !isACPNotFound(err) {
		return FileResult{}, err
	}
	combined := existing + req.Content
	if _, err := w.caller.Call(ctx, "fs/write_text_file", acpFSWriteParams{
		SessionID: w.sessionID,
		Path:      resolvedPath,
		Content:   combined,
	}); err != nil {
		return FileResult{}, err
	}
	return FileResult{
		Path:         resolvedPath,
		BytesWritten: len(req.Content),
	}, nil
}

func (w *ACPWorkspace) readFile(ctx context.Context, path string) (string, error) {
	raw, err := w.caller.Call(ctx, "fs/read_text_file", acpFSReadParams{
		SessionID: w.sessionID,
		Path:      path,
	})
	if err != nil {
		return "", err
	}
	var out acpFSReadResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("acp: decode fs/read_text_file response: %w", err)
	}
	return out.Content, nil
}

// ---------------------------------------------------------------------------
// Commands (one-shot)
// ---------------------------------------------------------------------------

type acpTerminalCreateParams struct {
	SessionID string            `json:"sessionId"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Cwd       string            `json:"cwd,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

type acpTerminalCreateResult struct {
	TerminalID string `json:"terminalId"`
}

type acpTerminalIDParams struct {
	SessionID  string `json:"sessionId"`
	TerminalID string `json:"terminalId"`
}

type acpTerminalOutputResult struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated,omitempty"`
}

type acpTerminalWaitResult struct {
	ExitCode int    `json:"exitCode"`
	Signal   string `json:"signal,omitempty"`
}

// Run executes a command synchronously by creating a terminal,
// waiting for exit, draining final output, and releasing the
// terminal. Result.Stdout / Stderr carry the captured output.
func (w *ACPWorkspace) Run(ctx context.Context, command Command) (Result, error) {
	return w.RunStreaming(ctx, command, nil)
}

// RunStreaming creates an editor-side terminal, polls terminal/output
// at a small cadence while the process runs, calls onChunk for each
// new slice of output, then drains the final output after exit. The
// editor is responsible for the actual process spawn — Hecate is just
// the orchestration layer.
//
// onChunk may be nil; the captured output still lands in the returned
// Result for the orchestrator's artifact pipeline.
func (w *ACPWorkspace) RunStreaming(ctx context.Context, command Command, onChunk func(OutputChunk)) (Result, error) {
	if err := w.ensureWired(); err != nil {
		return Result{}, err
	}
	terminalID, err := w.createTerminal(ctx, acpTerminalCreateParams{
		SessionID: w.sessionID,
		Command:   "sh",
		Args:      []string{"-lc", command.Command},
		Cwd:       command.WorkingDirectory,
	})
	if err != nil {
		return Result{}, err
	}
	defer w.releaseTerminal(context.Background(), terminalID) // best-effort release on every exit path

	var stdoutBuf, stderrBuf strings.Builder
	waitDone := make(chan acpTerminalWaitResult, 1)
	waitErr := make(chan error, 1)
	go func() {
		raw, err := w.caller.Call(ctx, "terminal/wait_for_exit", acpTerminalIDParams{
			SessionID:  w.sessionID,
			TerminalID: terminalID,
		})
		if err != nil {
			waitErr <- err
			return
		}
		var wr acpTerminalWaitResult
		if jerr := json.Unmarshal(raw, &wr); jerr != nil {
			waitErr <- jerr
			return
		}
		waitDone <- wr
	}()

	// Polling loop: drain terminal/output until either the wait
	// goroutine reports exit or ctx is cancelled. 100ms cadence is
	// a compromise between responsiveness and round-trip overhead;
	// the editor side typically responds in <10ms so polling cost
	// is the dominant factor.
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case wr := <-waitDone:
			// One last drain to capture any trailing output the
			// editor buffered between the previous poll and exit.
			w.drainOnce(ctx, terminalID, &stdoutBuf, &stderrBuf, onChunk)
			return Result{
				Stdout:   stdoutBuf.String(),
				Stderr:   stderrBuf.String(),
				ExitCode: wr.ExitCode,
			}, nil
		case err := <-waitErr:
			return Result{Stdout: stdoutBuf.String(), Stderr: stderrBuf.String()}, err
		case <-ctx.Done():
			// Attempt a kill so the editor doesn't leak a child
			// process; ignore the kill error — the caller's
			// real error is ctx.Err().
			_, _ = w.caller.Call(context.Background(), "terminal/kill", acpTerminalIDParams{
				SessionID:  w.sessionID,
				TerminalID: terminalID,
			})
			return Result{Stdout: stdoutBuf.String(), Stderr: stderrBuf.String()}, ctx.Err()
		case <-ticker.C:
			w.drainOnce(ctx, terminalID, &stdoutBuf, &stderrBuf, onChunk)
		}
	}
}

// drainOnce fetches the editor-side terminal's accumulated output
// since the last drain and appends it to the local buffers. ACP's
// terminal/output returns the FULL buffer each time today; we track
// what we've already seen by length so onChunk only sees the delta.
//
// (When the spec stabilizes a delta-style stream method we'll switch
// to that; the current polling model is forward-compat — the buffers
// stay correct either way.)
func (w *ACPWorkspace) drainOnce(ctx context.Context, terminalID string, stdoutBuf, stderrBuf *strings.Builder, onChunk func(OutputChunk)) {
	raw, err := w.caller.Call(ctx, "terminal/output", acpTerminalIDParams{
		SessionID:  w.sessionID,
		TerminalID: terminalID,
	})
	if err != nil {
		return // best-effort; the next tick retries
	}
	var out acpTerminalOutputResult
	if jerr := json.Unmarshal(raw, &out); jerr != nil {
		return
	}
	if delta := stringSuffixAfter(stdoutBuf, out.Stdout); delta != "" {
		stdoutBuf.WriteString(delta)
		if onChunk != nil {
			onChunk(OutputChunk{Stream: "stdout", Text: delta})
		}
	}
	if delta := stringSuffixAfter(stderrBuf, out.Stderr); delta != "" {
		stderrBuf.WriteString(delta)
		if onChunk != nil {
			onChunk(OutputChunk{Stream: "stderr", Text: delta})
		}
	}
}

// createTerminal sends terminal/create and returns the editor-assigned
// terminal id. Pulled out of Run/RunStreaming so OpenTerminal can
// share it.
func (w *ACPWorkspace) createTerminal(ctx context.Context, params acpTerminalCreateParams) (string, error) {
	raw, err := w.caller.Call(ctx, "terminal/create", params)
	if err != nil {
		return "", err
	}
	var out acpTerminalCreateResult
	if jerr := json.Unmarshal(raw, &out); jerr != nil {
		return "", fmt.Errorf("acp: decode terminal/create response: %w", jerr)
	}
	if strings.TrimSpace(out.TerminalID) == "" {
		return "", errors.New("acp: terminal/create returned empty terminalId")
	}
	return out.TerminalID, nil
}

func (w *ACPWorkspace) releaseTerminal(ctx context.Context, terminalID string) {
	_, _ = w.caller.Call(ctx, "terminal/release", acpTerminalIDParams{
		SessionID:  w.sessionID,
		TerminalID: terminalID,
	})
}

// ---------------------------------------------------------------------------
// Wiring helpers
// ---------------------------------------------------------------------------

func (w *ACPWorkspace) ensureWired() error {
	if w == nil || w.caller == nil {
		return errors.New("acp workspace: dispatcher is not wired")
	}
	if w.sessionID == "" {
		return errors.New("acp workspace: session id is empty")
	}
	return nil
}

// joinWorkspacePath normalizes the (workingDir, path) pair into a
// single path string the editor can resolve. Conservative: when
// workingDir is empty we return path as-is so absolute paths flow
// through unchanged.
func joinWorkspacePath(workingDir, path string) string {
	if workingDir == "" {
		return path
	}
	if strings.HasPrefix(path, "/") {
		return path
	}
	return strings.TrimRight(workingDir, "/") + "/" + strings.TrimLeft(path, "/")
}

// stringSuffixAfter returns the portion of next that comes after the
// already-seen prefix in seen. Used by drainOnce to compute deltas
// from the editor's full-buffer responses. When the buffers
// unexpectedly diverge (editor truncated, restart, etc.) we treat
// the entire next as the delta — over-reports rather than drops.
func stringSuffixAfter(seen *strings.Builder, next string) string {
	prefix := seen.String()
	if strings.HasPrefix(next, prefix) {
		return next[len(prefix):]
	}
	return next
}

// once captures one-time release wiring used by OpenTerminal's
// returned handle. Exposed via the local type to avoid pulling in
// sync.Once at the public method signatures.
type once struct {
	mu   sync.Mutex
	done bool
}

func (o *once) doOnce(fn func()) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.done {
		return
	}
	o.done = true
	fn()
}

// isACPNotFound reports whether err is the editor's "no such file"
// response. ACP doesn't fully nail this down; we recognize the
// JSON-RPC method-not-found code (-32601) by exclusion and look for
// the well-known not-found sentinel in the message. Adjusted as the
// editor side stabilizes.
func isACPNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "no such file")
}
