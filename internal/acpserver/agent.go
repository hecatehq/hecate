package acpserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	kitacp "github.com/hecatehq/acp-adapter-kit/acp"
	"github.com/hecatehq/acp-adapter-kit/runtimeacp"
	"github.com/hecatehq/hecate/internal/workspacefs"
)

const (
	defaultPollInterval   = 250 * time.Millisecond
	defaultRequestTimeout = 30 * time.Second
	defaultCancelTimeout  = 10 * time.Second
	// JSON escaping can expand control-heavy text by up to roughly sixfold.
	// Keep chunks comfortably below the kit's 1 MiB protocol envelope cap.
	maxUpdateTextBytes = 128 * 1024
	// Tool identifiers and titles originate in model output. Keep each field
	// well below the transport cap even after JSON escaping; oversized ids are
	// replaced by a stable, opaque surrogate so later status updates still
	// refer to the same ACP tool call.
	maxToolFieldBytes = 64 * 1024
	// JSON error data is written back to the ACP peer. Bound it independently
	// of the inbound message cap because quoted control characters can expand
	// severalfold during encoding.
	maxRPCErrorDataBytes = 8 * 1024
	// Resource links become durable task-prompt text. Bound even valid paths
	// independently of the transport envelope.
	maxWorkspaceResourcePathBytes = 4 * 1024
	maxResourceLinksPerPrompt     = 64
	maxTaskPromptBytes            = 1024 * 1024
	approvalWaitMessage           = "Hecate is awaiting operator approval. Resolve it in the Hecate console to continue."
)

var (
	errTurnCancelled                = errors.New("ACP turn cancelled")
	errInvalidWorkspaceResourceLink = errors.New("resource link must reference an existing regular file inside the session workspace")
)

// Config controls the ACP bridge's bounded local behavior. It intentionally
// has no provider or workspace knobs: those belong to the Hecate runtime and
// the ACP client's session/new request respectively.
type Config struct {
	Version        string
	PollInterval   time.Duration
	RequestTimeout time.Duration
	CancelTimeout  time.Duration
}

// Agent binds ACP session methods to the durable Hecate task runtime. It has
// no provider-specific behavior and does not share implementation with the
// outbound External Agent adapter boundary.
type Agent struct {
	runtime Runtime
	config  Config

	mu          sync.RWMutex
	initialized bool
	sessions    map[string]*session
	cancelWG    sync.WaitGroup
}

type session struct {
	id  string
	cwd string

	turnMu sync.Mutex
	mu     sync.Mutex

	taskID    string
	lastRunID string
	// pendingPrompt is retained only after a task was created but its first
	// start failed. Retrying a different prompt would otherwise silently run
	// the old task prompt, so it is deliberately rejected.
	pendingPrompt string
	active        *activeTurn
	closed        bool
}

type activeTurn struct {
	mu     sync.Mutex
	taskID string
	runID  string
	reason string
	// completed fences late protocol-context cancellation after Hecate has
	// already reported a terminal event for this turn.
	completed bool
	// cancellationStarted is protected by mu. Cancellation may happen
	// before StartTask returns, so its controller is deferred until the durable
	// run identifier is available. The controller retries a transient runtime
	// failure until it observes a terminal event or reaches CancelTimeout.
	cancellationStarted bool

	// outputMu serializes outbound ACP updates. Cancellation deliberately does
	// not acquire it: a blocked stdout writer must never delay native-run
	// cancellation. An update already inside method.Notify may complete after
	// cancellation, but every later chunk/update re-checks the cancelled state.
	outputMu sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc

	cancelOnce sync.Once
}

// NewAgent creates a task-backed ACP agent. Runtime is required; it is kept
// as an interface so protocol behavior can be exercised without an HTTP
// server in focused tests.
func NewAgent(runtime Runtime, config Config) (*Agent, error) {
	if runtime == nil {
		return nil, errors.New("Hecate runtime is required")
	}
	if config.PollInterval <= 0 {
		config.PollInterval = defaultPollInterval
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = defaultRequestTimeout
	}
	if config.CancelTimeout <= 0 {
		config.CancelTimeout = defaultCancelTimeout
	}
	return &Agent{
		runtime:  runtime,
		config:   config,
		sessions: make(map[string]*session),
	}, nil
}

// Server exposes the provider-neutral ACP kit transport with Hecate-native
// handlers. The kit owns JSON-RPC framing, ordered request dispatch,
// concurrent cancellation, request IDs, errors, and the inbound 1 MiB cap.
func (a *Agent) Server() *kitacp.Server {
	return kitacp.NewServer(
		kitacp.AdapterInfo{
			Name:    "hecate",
			Title:   "Hecate",
			Version: a.config.Version,
			Capabilities: kitacp.Capabilities{
				SessionClose: true,
			},
		},
		kitacp.WithInitializeHandler(a.initialize),
		kitacp.WithMethod("session/new", a.newSession),
		kitacp.WithConcurrentMethod("session/prompt", a.prompt),
		// ACP clients should send session/cancel as a notification. Keep the
		// request form too for older clients; both paths are idempotent.
		kitacp.WithNotification("session/cancel", a.cancelNotification),
		kitacp.WithConcurrentMethod("session/cancel", a.cancelMethod),
		kitacp.WithConcurrentMethod("session/close", a.closeSession),
	)
}

func (a *Agent) initialize(raw json.RawMessage) (any, *kitacp.RPCError) {
	var request struct {
		ProtocolVersion int `json:"protocolVersion"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, invalidParams(err)
	}
	if request.ProtocolVersion != runtimeacp.ProtocolVersion {
		return nil, &kitacp.RPCError{
			Code:    -32602,
			Message: "unsupported ACP protocol version",
			Data: map[string]any{
				"requested": request.ProtocolVersion,
				"supported": runtimeacp.ProtocolVersion,
			},
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), a.config.RequestTimeout)
	defer cancel()
	if err := a.runtime.EnsureReady(ctx); err != nil {
		return nil, runtimeError("Hecate is not ready for ACP", err)
	}

	a.mu.Lock()
	a.initialized = true
	a.mu.Unlock()

	return map[string]any{
		"protocolVersion": runtimeacp.ProtocolVersion,
		"agentCapabilities": map[string]any{
			"loadSession": false,
			"promptCapabilities": map[string]any{
				"image":           false,
				"audio":           false,
				"embeddedContext": false,
			},
			"mcpCapabilities": map[string]any{
				"acp":  false,
				"http": false,
				"sse":  false,
			},
			"sessionCapabilities": map[string]any{
				"close": map[string]any{},
			},
		},
		"agentInfo": map[string]any{
			"name":    "hecate",
			"title":   "Hecate",
			"version": a.config.Version,
		},
		"authMethods": []any{},
	}, nil
}

func (a *Agent) newSession(_ *kitacp.MethodContext, raw json.RawMessage) (any, *kitacp.RPCError) {
	var request newSessionRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, invalidParams(err)
	}
	cwd := strings.TrimSpace(request.CWD)
	if cwd == "" || !filepath.IsAbs(cwd) {
		return nil, invalidParams(errors.New("cwd must be an absolute path"))
	}
	if len(request.MCPServers) > 0 {
		return nil, unsupportedParams("MCP servers are not supported by Hecate ACP yet")
	}
	if len(request.AdditionalDirectories) > 0 {
		return nil, unsupportedParams("additionalDirectories are not supported by Hecate ACP yet")
	}

	a.mu.RLock()
	initialized := a.initialized
	a.mu.RUnlock()
	if !initialized {
		return nil, runtimeError("initialize Hecate ACP before creating a session", nil)
	}

	id, err := newSessionID()
	if err != nil {
		return nil, runtimeError("create ACP session", err)
	}
	created := &session{
		id:  id,
		cwd: filepath.Clean(cwd),
	}
	a.mu.Lock()
	a.sessions[id] = created
	a.mu.Unlock()

	return map[string]any{"sessionId": id}, nil
}

func (a *Agent) prompt(method *kitacp.MethodContext, raw json.RawMessage) (any, *kitacp.RPCError) {
	var request promptRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, invalidParams(err)
	}
	if strings.TrimSpace(request.SessionID) == "" {
		return nil, invalidParams(errors.New("sessionId is required"))
	}
	session := a.lookupSession(request.SessionID)
	if session == nil {
		return nil, unknownSession(request.SessionID)
	}
	prompt, err := promptText(session.cwd, request.Prompt)
	if err != nil {
		return nil, invalidParams(err)
	}

	session.turnMu.Lock()
	defer session.turnMu.Unlock()
	if method.Context().Err() != nil {
		return promptResult(runtimeacp.StopReasonCancelled), nil
	}

	active, err := a.startTurn(session, prompt)
	if err != nil {
		if errors.Is(err, errTurnCancelled) {
			return promptResult(runtimeacp.StopReasonCancelled), nil
		}
		if method.Context().Err() != nil {
			return promptResult(runtimeacp.StopReasonCancelled), nil
		}
		return nil, runtimeError("start Hecate task run", err)
	}
	if method.Context().Err() != nil {
		a.cancelTurn(session, active, "ACP client cancelled the prompt")
		return promptResult(runtimeacp.StopReasonCancelled), nil
	}
	return a.streamTurn(method, session, active)
}

func (a *Agent) cancelNotification(raw json.RawMessage) error {
	_, rpcErr := a.cancel(raw)
	if rpcErr != nil {
		// Notifications cannot return an RPC error. Treat malformed or stale
		// cancellation as a no-op; it must never tear down the stdio server.
		return nil
	}
	return nil
}

func (a *Agent) cancelMethod(_ *kitacp.MethodContext, raw json.RawMessage) (any, *kitacp.RPCError) {
	return a.cancel(raw)
}

func (a *Agent) cancel(raw json.RawMessage) (any, *kitacp.RPCError) {
	var request sessionReference
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, invalidParams(err)
	}
	if session := a.lookupSession(request.SessionID); session != nil {
		a.cancelSession(session, "ACP client cancelled the session")
	}
	return map[string]any{}, nil
}

func (a *Agent) closeSession(_ *kitacp.MethodContext, raw json.RawMessage) (any, *kitacp.RPCError) {
	var request sessionReference
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, invalidParams(err)
	}
	if strings.TrimSpace(request.SessionID) == "" {
		return nil, invalidParams(errors.New("sessionId is required"))
	}

	a.mu.Lock()
	session := a.sessions[request.SessionID]
	delete(a.sessions, request.SessionID)
	a.mu.Unlock()
	if session != nil {
		session.mu.Lock()
		session.closed = true
		active := session.active
		session.mu.Unlock()
		if active != nil {
			a.cancelTurn(session, active, "ACP session closed")
		}
	}
	return map[string]any{}, nil
}

// CloseAll releases every in-memory ACP session after stdio disconnect. The
// Hecate tasks stay durable for operator inspection, but active owned runs are
// explicitly asked to stop rather than being left unsupervised.
func (a *Agent) CloseAll() {
	a.mu.Lock()
	sessions := make([]*session, 0, len(a.sessions))
	for id, session := range a.sessions {
		delete(a.sessions, id)
		sessions = append(sessions, session)
	}
	a.mu.Unlock()
	for _, session := range sessions {
		session.mu.Lock()
		session.closed = true
		active := session.active
		session.mu.Unlock()
		if active != nil {
			a.cancelTurn(session, active, "ACP stdio connection closed")
		}
	}
}

func (a *Agent) startTurn(session *session, prompt string) (*activeTurn, error) {
	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		return nil, errors.New("ACP session is closed")
	}
	if session.active != nil {
		session.mu.Unlock()
		return nil, errors.New("the previous Hecate turn is still shutting down")
	}
	taskID := session.taskID
	lastRunID := session.lastRunID
	cwd := session.cwd
	if taskID != "" && lastRunID == "" && session.pendingPrompt != prompt {
		session.mu.Unlock()
		return nil, errors.New("the previous Hecate task did not start; retry its original prompt or close the ACP session before sending a different prompt")
	}
	turnContext, turnCancel := context.WithCancel(context.Background())
	active := &activeTurn{ctx: turnContext, cancel: turnCancel}
	// Install the cancellation generation before any runtime request. A
	// session/cancel or session/close received while CreateTask/StartTask is
	// in flight now fences the later result instead of being silently lost.
	session.active = active
	session.mu.Unlock()

	requestContext, cancel := context.WithTimeout(context.Background(), a.config.RequestTimeout)
	defer cancel()

	var run Run
	if taskID == "" {
		task, err := a.runtime.CreateTask(requestContext, CreateTaskRequest{
			Title:            "ACP session",
			Prompt:           prompt,
			WorkingDirectory: cwd,
		})
		if err != nil {
			a.finishTurn(session, active)
			if active.cancelled() {
				return active, errTurnCancelled
			}
			return nil, err
		}
		taskID = task.ID
		if strings.TrimSpace(taskID) == "" {
			a.finishTurn(session, active)
			return nil, errors.New("Hecate returned an empty task id")
		}
		active.setTaskID(taskID)
		// Record a successfully-created task before starting it. If the
		// runtime is briefly unavailable at start time, retrying the same prompt
		// can start the same durable task. A different prompt is refused rather
		// than silently running this original prompt.
		session.mu.Lock()
		if !session.closed && session.active == active {
			session.taskID = taskID
			session.pendingPrompt = prompt
			lastRunID = session.lastRunID
		}
		closed := session.closed || session.active != active
		session.mu.Unlock()
		if closed || active.cancelled() {
			a.clearUnstartedTask(session, active, taskID)
			a.finishTurn(session, active)
			return active, errTurnCancelled
		}
	} else {
		active.setTaskID(taskID)
		if active.cancelled() {
			if lastRunID == "" {
				a.clearUnstartedTask(session, active, taskID)
			}
			a.finishTurn(session, active)
			return active, errTurnCancelled
		}
	}
	if lastRunID == "" {
		var err error
		run, err = a.runtime.StartTask(requestContext, taskID)
		if err != nil {
			if active.cancelled() {
				a.clearUnstartedTask(session, active, taskID)
				a.finishTurn(session, active)
				return active, errTurnCancelled
			}
			a.finishTurn(session, active)
			return nil, err
		}
	} else {
		var err error
		run, err = a.runtime.ContinueTask(requestContext, taskID, lastRunID, prompt)
		if err != nil {
			a.finishTurn(session, active)
			if active.cancelled() {
				return active, errTurnCancelled
			}
			return nil, err
		}
	}
	if strings.TrimSpace(run.ID) == "" {
		a.finishTurn(session, active)
		return nil, errors.New("Hecate returned an empty run id")
	}

	active.setRunID(run.ID)
	session.mu.Lock()
	closed := session.closed || session.active != active
	if !closed {
		session.taskID = taskID
		session.lastRunID = run.ID
		session.pendingPrompt = ""
	}
	session.mu.Unlock()
	if closed {
		a.cancelTurn(session, active, "ACP session closed while the task started")
		return active, nil
	}
	if active.cancelled() {
		a.dispatchCancellation(session, active)
	}
	return active, nil
}

func (a *Agent) clearUnstartedTask(session *session, active *activeTurn, taskID string) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.active == active && session.taskID == taskID && session.lastRunID == "" {
		session.taskID = ""
		session.pendingPrompt = ""
	}
}

func (a *Agent) streamTurn(method *kitacp.MethodContext, session *session, active *activeTurn) (any, *kitacp.RPCError) {
	// The kit cancels the inbound JSON-RPC request context for both protocol
	// cancellation and connection teardown. Link that to the session turn so a
	// currently-blocked event poll is interrupted promptly, while CancelRun
	// itself still receives a fresh detached context below.
	streamDone := make(chan struct{})
	watcherDone := make(chan struct{})
	defer func() {
		// A request-context cancellation can race a terminal Hecate event. Join
		// the watcher before this handler returns so it cannot start a detached
		// cancellation controller after transport shutdown has started.
		close(streamDone)
		<-watcherDone
	}()
	go func() {
		defer close(watcherDone)
		select {
		case <-method.Context().Done():
			a.cancelTurn(session, active, "ACP client cancelled the prompt")
		case <-active.ctx.Done():
		case <-streamDone:
		}
	}()

	var afterSequence int64
	emittedAssistantText := false
	emittedApprovalWait := false
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-method.Context().Done():
			a.cancelTurn(session, active, "ACP client cancelled the prompt")
			return promptResult(runtimeacp.StopReasonCancelled), nil
		case <-active.ctx.Done():
			return promptResult(runtimeacp.StopReasonCancelled), nil
		case <-timer.C:
		}

		taskID, runID := active.identifiers()
		if taskID == "" || runID == "" {
			return promptResult(runtimeacp.StopReasonCancelled), nil
		}
		requestContext, cancel := context.WithTimeout(active.ctx, a.config.RequestTimeout)
		events, err := a.runtime.ListRunEvents(requestContext, taskID, runID, afterSequence)
		cancel()
		if err != nil {
			if active.ctx.Err() != nil || method.Context().Err() != nil {
				return promptResult(runtimeacp.StopReasonCancelled), nil
			}
			a.cancelTurn(session, active, "Hecate event stream became unavailable")
			return nil, runtimeError("read Hecate task events", err)
		}

		for _, event := range events {
			if event.Sequence > afterSequence {
				afterSequence = event.Sequence
			}
			if !a.isActive(session, active) {
				return promptResult(runtimeacp.StopReasonCancelled), nil
			}

			terminal, result, rpcErr := a.forwardEvent(method, session, active, event, &emittedAssistantText, &emittedApprovalWait)
			if rpcErr != nil {
				// A failed ACP output write means the editor can no longer
				// supervise this native run. Do not merely drop the session
				// pointer: ask Hecate to stop the run before returning the
				// protocol error. A run.failed event is already terminal, so it
				// needs no additional cancellation request.
				if !isTerminalEvent(event.Type) {
					a.cancelTurn(session, active, "ACP client output failed")
					return nil, rpcErr
				}
				a.finishTurn(session, active)
				return nil, rpcErr
			}
			if terminal {
				// sendTextUpdate/sendToolUpdate can return errTurnCancelled
				// because the JSON-RPC request itself was cancelled before the
				// stream watcher runs. Ensure that race still reaches the native
				// Hecate run; cancelTurn is idempotent for an already-cancelled
				// session or a terminal native run.
				if active.cancelled() || method.Context().Err() != nil {
					a.cancelTurn(session, active, "ACP client cancelled the prompt")
					if !isTerminalEvent(event.Type) {
						return result, nil
					}
				}
				a.finishTurn(session, active)
				return result, nil
			}
		}

		timer.Reset(a.config.PollInterval)
	}
}

func (a *Agent) forwardEvent(method *kitacp.MethodContext, session *session, active *activeTurn, event RunEvent, emittedAssistantText, emittedApprovalWait *bool) (bool, any, *kitacp.RPCError) {
	switch event.Type {
	case "assistant.text_complete":
		text := stringValue(event.Data, "text")
		if text == "" {
			return false, nil, nil
		}
		if err := a.sendTextUpdate(method, session, active, text); err != nil {
			if errors.Is(err, errTurnCancelled) {
				return true, promptResult(runtimeacp.StopReasonCancelled), nil
			}
			return false, nil, runtimeError("send ACP assistant update", err)
		}
		*emittedAssistantText = true
	case "assistant.final_answer":
		// Agent-loop events already emit every completed assistant message.
		// The final summary is only a fallback for runtimes that produced no
		// text event, preventing duplicate final responses in ACP clients.
		if !*emittedAssistantText {
			if summary := stringValue(event.Data, "summary"); summary != "" {
				if err := a.sendTextUpdate(method, session, active, summary); err != nil {
					if errors.Is(err, errTurnCancelled) {
						return true, promptResult(runtimeacp.StopReasonCancelled), nil
					}
					return false, nil, runtimeError("send ACP assistant update", err)
				}
				*emittedAssistantText = true
			}
		}
	case "assistant.tool_call_proposed":
		if err := a.sendToolUpdate(method, session, active, event, "tool_call", "pending"); err != nil {
			if errors.Is(err, errTurnCancelled) {
				return true, promptResult(runtimeacp.StopReasonCancelled), nil
			}
			return false, nil, runtimeError("send ACP tool update", err)
		}
	case "tool.started":
		if err := a.sendToolUpdate(method, session, active, event, "tool_call_update", "in_progress"); err != nil {
			if errors.Is(err, errTurnCancelled) {
				return true, promptResult(runtimeacp.StopReasonCancelled), nil
			}
			return false, nil, runtimeError("send ACP tool update", err)
		}
	case "tool.completed", "tool.file.patch":
		if err := a.sendToolUpdate(method, session, active, event, "tool_call_update", "completed"); err != nil {
			if errors.Is(err, errTurnCancelled) {
				return true, promptResult(runtimeacp.StopReasonCancelled), nil
			}
			return false, nil, runtimeError("send ACP tool update", err)
		}
	case "tool.failed", "tool.timed_out", "tool.cancelled", "policy.tool_blocked":
		if err := a.sendToolUpdate(method, session, active, event, "tool_call_update", "failed"); err != nil {
			if errors.Is(err, errTurnCancelled) {
				return true, promptResult(runtimeacp.StopReasonCancelled), nil
			}
			return false, nil, runtimeError("send ACP tool update", err)
		}
	case "run.awaiting_approval", "approval.requested":
		if emittedApprovalWait != nil && !*emittedApprovalWait {
			if err := a.sendTextUpdate(method, session, active, approvalWaitMessage); err != nil {
				if errors.Is(err, errTurnCancelled) {
					return true, promptResult(runtimeacp.StopReasonCancelled), nil
				}
				return false, nil, runtimeError("send ACP approval update", err)
			}
			*emittedApprovalWait = true
		}
	case "run.finished":
		return true, promptResult(runtimeacp.StopReasonEndTurn), nil
	case "run.cancelled":
		return true, promptResult(runtimeacp.StopReasonCancelled), nil
	case "run.failed":
		message := stringValue(event.Data, "message")
		if message == "" {
			message = stringValue(event.Data, "error")
		}
		if message != "" {
			if err := a.sendTextUpdate(method, session, active, "Hecate run failed: "+truncateText(message, maxUpdateTextBytes)); errors.Is(err, errTurnCancelled) {
				return true, promptResult(runtimeacp.StopReasonCancelled), nil
			}
		}
		return false, nil, runtimeError("Hecate task run failed", errors.New(nonEmpty(message, "run failed")))
	}
	return false, nil, nil
}

func (a *Agent) cancelSession(session *session, reason string) {
	session.mu.Lock()
	active := session.active
	session.mu.Unlock()
	if active != nil {
		a.cancelTurn(session, active, reason)
	}
}

func (a *Agent) cancelTurn(session *session, active *activeTurn, reason string) {
	if active == nil {
		return
	}
	// Do not wait for outputMu here. method.Notify ultimately writes to the
	// ACP client's stdout pipe and may block indefinitely when that client has
	// stopped reading. Mark the turn first so no later update starts, then send
	// the detached native cancellation regardless of output backpressure.
	active.requestCancellation(reason)
	a.dispatchCancellation(session, active)
}

func (a *Agent) dispatchCancellation(session *session, active *activeTurn) {
	taskID, runID, reason, ready := active.cancellationTarget()
	if !ready {
		return
	}
	a.cancelWG.Add(1)
	go func() {
		defer a.cancelWG.Done()
		a.cancelAndAwait(session, active, taskID, runID, reason)
	}()
}

// waitForCancellations keeps the ACP process alive long enough for active
// detached cancellation controllers to reach Hecate. It is called only after
// the ACP transport has stopped accepting work, and the caller supplies a
// bounded context so a misbehaving Runtime cannot make shutdown unbounded.
func (a *Agent) waitForCancellations(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan struct{})
	go func() {
		a.cancelWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// cancelAndAwait owns cancellation after an ACP turn is fenced locally. The
// runtime call is intentionally detached from the cancelled protocol request,
// but errors cannot be ignored: a temporary local-runtime outage must not
// strand the session forever or allow a second prompt to overlap the first.
// On deadline the session is retired, forcing a fresh ACP session rather than
// risking continued work under a turn the editor can no longer supervise.
func (a *Agent) cancelAndAwait(session *session, active *activeTurn, taskID, runID, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), a.config.CancelTimeout)
	defer cancel()
	var afterSequence int64
	cancelAcknowledged := false
	for {
		if ctx.Err() != nil {
			a.retireCancelledSession(session, active)
			return
		}

		if !cancelAcknowledged {
			cancelContext, cancelRequest := context.WithTimeout(ctx, cancellationAttemptTimeout(ctx))
			cancelAcknowledged = a.runtime.CancelRun(cancelContext, taskID, runID, reason) == nil
			cancelRequest()
		}

		requestContext, requestCancel := context.WithTimeout(ctx, cancellationAttemptTimeout(ctx))
		events, err := a.runtime.ListRunEvents(requestContext, taskID, runID, afterSequence)
		requestCancel()
		if err == nil {
			for _, event := range events {
				if event.Sequence > afterSequence {
					afterSequence = event.Sequence
				}
				if isTerminalEvent(event.Type) {
					a.finishTurn(session, active)
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			a.retireCancelledSession(session, active)
			return
		case <-time.After(cancellationRetryInterval(a.config.PollInterval)):
		}
	}
}

func cancellationAttemptTimeout(parent context.Context) time.Duration {
	const maximum = time.Second
	if deadline, ok := parent.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < maximum {
			return max(remaining, time.Millisecond)
		}
	}
	return maximum
}

func cancellationRetryInterval(pollInterval time.Duration) time.Duration {
	const maximum = 250 * time.Millisecond
	if pollInterval <= 0 {
		return maximum
	}
	return min(pollInterval, maximum)
}

func (a *Agent) retireCancelledSession(session *session, active *activeTurn) {
	// No terminal native event arrived before the bounded retry window. Keep
	// the durable task for Hecate operators, but remove this ACP session so it
	// cannot issue an overlapping continuation against an unknown run state.
	session.mu.Lock()
	if session.active != active {
		session.mu.Unlock()
		return
	}
	session.active = nil
	session.closed = true
	session.mu.Unlock()

	a.mu.Lock()
	if a.sessions[session.id] == session {
		delete(a.sessions, session.id)
	}
	a.mu.Unlock()
}

func (a *Agent) finishTurn(session *session, active *activeTurn) {
	active.markCompleted()
	session.mu.Lock()
	if session.active == active {
		session.active = nil
	}
	session.mu.Unlock()
}

func (a *Agent) isActive(session *session, active *activeTurn) bool {
	a.mu.RLock()
	current := a.sessions[session.id] == session
	a.mu.RUnlock()
	if !current {
		return false
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return !session.closed && session.active == active
}

func (active *activeTurn) setTaskID(taskID string) {
	active.mu.Lock()
	active.taskID = taskID
	active.mu.Unlock()
}

func (active *activeTurn) setRunID(runID string) {
	active.mu.Lock()
	active.runID = runID
	active.mu.Unlock()
}

func (active *activeTurn) identifiers() (string, string) {
	active.mu.Lock()
	defer active.mu.Unlock()
	return active.taskID, active.runID
}

func (active *activeTurn) cancelled() bool {
	return active.ctx.Err() != nil
}

func (active *activeTurn) requestCancellation(reason string) {
	active.cancelOnce.Do(func() {
		active.mu.Lock()
		if active.completed {
			active.mu.Unlock()
			return
		}
		active.reason = nonEmpty(strings.TrimSpace(reason), "ACP client cancelled the prompt")
		active.cancel()
		active.mu.Unlock()
	})
}

func (active *activeTurn) markCompleted() {
	active.mu.Lock()
	active.completed = true
	active.mu.Unlock()
}

func (active *activeTurn) cancellationTarget() (taskID, runID, reason string, ready bool) {
	active.mu.Lock()
	defer active.mu.Unlock()
	if active.ctx.Err() == nil || active.cancellationStarted || active.taskID == "" || active.runID == "" {
		return "", "", "", false
	}
	active.cancellationStarted = true
	return active.taskID, active.runID, nonEmpty(active.reason, "ACP client cancelled the prompt"), true
}

func (a *Agent) lookupSession(id string) *session {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sessions[id]
}

func (a *Agent) sendTextUpdate(method *kitacp.MethodContext, session *session, active *activeTurn, text string) error {
	active.outputMu.Lock()
	defer active.outputMu.Unlock()
	for _, chunk := range splitText(text, maxUpdateTextBytes) {
		if !a.canSendUpdate(method, session, active) {
			return errTurnCancelled
		}
		if err := method.Notify("session/update", map[string]any{
			"sessionId": session.id,
			"update": map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"content": map[string]any{
					"type": "text",
					"text": chunk,
				},
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (a *Agent) sendToolUpdate(method *kitacp.MethodContext, session *session, active *activeTurn, event RunEvent, updateType, status string) error {
	active.outputMu.Lock()
	defer active.outputMu.Unlock()
	if !a.canSendUpdate(method, session, active) {
		return errTurnCancelled
	}
	return notifyTool(method, session.id, event, updateType, status)
}

func (a *Agent) canSendUpdate(method *kitacp.MethodContext, session *session, active *activeTurn) bool {
	return method.Context().Err() == nil && !active.cancelled() && a.isActive(session, active)
}

func notifyTool(method *kitacp.MethodContext, sessionID string, event RunEvent, updateType, status string) error {
	id := stringValue(event.Data, "tool_call_id")
	if id == "" {
		return nil
	}
	id = boundedToolCallID(id)
	update := map[string]any{
		"sessionUpdate": updateType,
		"toolCallId":    id,
		"status":        status,
	}
	if updateType == "tool_call" {
		update["title"] = truncateText(nonEmpty(stringValue(event.Data, "tool_name"), "Hecate tool"), maxToolFieldBytes)
	}
	return method.Notify("session/update", map[string]any{
		"sessionId": sessionID,
		"update":    update,
	})
}

func boundedToolCallID(value string) string {
	if len(value) <= maxToolFieldBytes {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return "hecate_tool_" + hex.EncodeToString(sum[:])
}

func promptText(workspaceRoot string, blocks []runtimeacp.ContentBlock) (string, error) {
	if blocks == nil {
		return "", errors.New("prompt is required")
	}
	parts := make([]string, 0, len(blocks))
	promptBytes := 0
	resourceLinks := 0
	appendPart := func(part string) error {
		addedBytes := len(part)
		if len(parts) > 0 {
			addedBytes += len("\n\n")
		}
		if addedBytes > maxTaskPromptBytes-promptBytes {
			return errors.New("ACP prompt exceeds the 1 MiB task-prompt limit")
		}
		parts = append(parts, part)
		promptBytes += addedBytes
		return nil
	}
	for _, block := range blocks {
		contentType := strings.TrimSpace(block.Type)
		switch contentType {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				if err := appendPart(block.Text); err != nil {
					return "", err
				}
			}
		case "resource_link":
			resourceLinks++
			if resourceLinks > maxResourceLinksPerPrompt {
				return "", errors.New("ACP prompt exceeds the 64 resource-link limit")
			}
			relativePath, err := workspaceResourceRelativePath(workspaceRoot, block.URI)
			if err != nil {
				return "", err
			}
			reference := fmt.Sprintf(
				"[Workspace file reference: %q. Read it through Hecate's workspace tools if needed.]",
				relativePath,
			)
			if err := appendPart(reference); err != nil {
				return "", err
			}
		case "image", "audio", "resource":
			return "", errors.New("image, audio, and embedded-resource prompt content is not supported by Hecate ACP yet")
		default:
			// Content type is client-controlled. Keep the protocol error stable
			// and bounded rather than reflecting an arbitrarily large or
			// control-heavy value into JSON-RPC error data.
			return "", errors.New("unsupported ACP prompt content type")
		}
	}
	prompt := strings.TrimSpace(strings.Join(parts, "\n\n"))
	if prompt == "" {
		return "", errors.New("prompt must include text or a workspace file link")
	}
	return prompt, nil
}

func workspaceResourceRelativePath(workspaceRoot, rawURI string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURI))
	if err != nil ||
		!strings.EqualFold(parsed.Scheme, "file") ||
		parsed.Opaque != "" ||
		parsed.User != nil ||
		(parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost")) ||
		parsed.RawQuery != "" ||
		parsed.ForceQuery ||
		parsed.Fragment != "" {
		return "", errInvalidWorkspaceResourceLink
	}

	path := filepath.FromSlash(parsed.Path)
	if goruntime.GOOS == "windows" &&
		len(path) >= 3 &&
		(path[0] == '\\' || path[0] == '/') &&
		path[2] == ':' {
		path = path[1:]
	}
	if path == "" ||
		len(path) > maxWorkspaceResourcePathBytes ||
		strings.ContainsRune(path, '\x00') ||
		!filepath.IsAbs(path) {
		return "", errInvalidWorkspaceResourceLink
	}

	relativePath, err := filepath.Rel(filepath.Clean(workspaceRoot), filepath.Clean(path))
	if err != nil ||
		relativePath == "." ||
		len(relativePath) > maxWorkspaceResourcePathBytes ||
		!filepath.IsLocal(relativePath) {
		return "", errInvalidWorkspaceResourceLink
	}
	fsys, err := workspacefs.New(workspaceRoot)
	if err != nil {
		return "", errInvalidWorkspaceResourceLink
	}
	info, _, err := fsys.Stat(relativePath)
	if err != nil || !info.Mode().IsRegular() {
		return "", errInvalidWorkspaceResourceLink
	}
	return filepath.ToSlash(relativePath), nil
}

func promptResult(reason runtimeacp.StopReason) map[string]any {
	return map[string]any{"stopReason": string(reason)}
}

func invalidParams(err error) *kitacp.RPCError {
	return &kitacp.RPCError{Code: -32602, Message: "invalid ACP parameters", Data: boundedRPCErrorData(err)}
}

func unsupportedParams(message string) *kitacp.RPCError {
	return &kitacp.RPCError{Code: -32602, Message: "unsupported ACP parameters", Data: message}
}

func unknownSession(_ string) *kitacp.RPCError {
	// The session identifier comes from the client and is not needed to repair
	// this error. Do not reflect it into the protocol response.
	return &kitacp.RPCError{Code: -32602, Message: "unknown ACP session"}
}

func runtimeError(message string, err error) *kitacp.RPCError {
	data := any(nil)
	if err != nil {
		data = boundedRPCErrorData(err)
	}
	return &kitacp.RPCError{Code: -32000, Message: message, Data: data}
}

func boundedRPCErrorData(err error) string {
	if err == nil {
		return ""
	}
	return truncateText(err.Error(), maxRPCErrorDataBytes)
}

func stringValue(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, _ := data[key].(string)
	return strings.TrimSpace(value)
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func isTerminalEvent(eventType string) bool {
	switch eventType {
	case "run.finished", "run.failed", "run.cancelled":
		return true
	default:
		return false
	}
}

func splitText(text string, maxBytes int) []string {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return []string{text}
	}
	chunks := make([]string, 0, len(text)/maxBytes+1)
	for len(text) > maxBytes {
		cut := maxBytes
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		if cut == 0 {
			cut = maxBytes
		}
		chunks = append(chunks, text[:cut])
		text = text[cut:]
	}
	chunks = append(chunks, text)
	return chunks
}

func truncateText(text string, maxBytes int) string {
	chunks := splitText(text, maxBytes)
	if len(chunks) == 0 {
		return ""
	}
	return chunks[0]
}

func newSessionID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate random session id: %w", err)
	}
	return "acp_" + hex.EncodeToString(raw[:]), nil
}

type newSessionRequest struct {
	CWD                   string            `json:"cwd"`
	MCPServers            []json.RawMessage `json:"mcpServers"`
	AdditionalDirectories []string          `json:"additionalDirectories"`
}

type promptRequest struct {
	SessionID string                    `json:"sessionId"`
	Prompt    []runtimeacp.ContentBlock `json:"prompt"`
}

type sessionReference struct {
	SessionID string `json:"sessionId"`
}
