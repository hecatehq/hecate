package llamacpp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// ProcessStarter is the dependency the Runtime injects to spawn a
// llama-server child. Production wires this to runtime_process.go's
// ExecProcessStarter (real exec.Cmd + HTTP health polling). Tests
// inject a fake to drive the state machine deterministically.
type ProcessStarter interface {
	Start(ctx context.Context, opts ProcessStartOptions) (ProcessHandle, error)
}

// ProcessStartOptions captures everything the child needs at spawn
// time. The Runtime resolves these from RuntimeStartRequest plus the
// InstalledModel record before handing them off to the starter.
type ProcessStartOptions struct {
	// BinaryPath is the absolute path to llama-server. Resolved from
	// HECATE_LLAMA_SERVER_BIN at gateway boot; the runtime does not
	// re-resolve per start.
	BinaryPath string
	// ModelPath is the absolute path to the .gguf file the child
	// should load.
	ModelPath string
	// Host the child should bind on. Defaults to 127.0.0.1 — the
	// runtime always binds loopback to avoid exposing the model
	// inference port to the network.
	Host string
	// Port is the loopback port the child should listen on. The
	// runtime picks a free port before each start (port pool reset
	// on every transition; v1 does not reuse ports).
	Port int
	// ContextSize is the n_ctx llama-server should be started with.
	// Pulled from InstalledModel.RecommendedContext or overridden
	// per start request.
	ContextSize int
}

// ProcessHandle is the surface the Runtime holds on a running child.
// It is intentionally minimal — the Runtime only needs to wait for
// health, listen for exit, and stop. Anything richer (HTTP proxy
// targets, etc.) lives off the handle's Port + Host.
type ProcessHandle interface {
	// PID returns the OS process id. Diagnostic only.
	PID() int
	// Port returns the loopback port the child is bound to.
	Port() int
	// Host returns the bind host (127.0.0.1 in v1).
	Host() string
	// WaitForHealth blocks until the child's /health endpoint
	// returns OK or ctx is cancelled. Returns nil on health,
	// non-nil on cancellation or hard timeout.
	WaitForHealth(ctx context.Context) error
	// Stop sends a graceful kill, then waits up to timeout for the
	// child to exit. After timeout it issues SIGKILL. Always
	// returns; never blocks past timeout + grace period.
	Stop(ctx context.Context, timeout time.Duration) error
	// Exited returns a channel that closes when the child exits
	// for any reason (operator stop, crash, panic). The
	// ProcessExitInfo carries the exit code + signal so the
	// runtime can classify operator-requested vs unexpected.
	Exited() <-chan ProcessExitInfo
}

// ProcessExitInfo is the message sent on ProcessHandle.Exited when
// the child terminates.
type ProcessExitInfo struct {
	// ExitCode is the OS exit code, or -1 if the process was killed
	// by signal.
	ExitCode int
	// Signal is non-empty when the child was killed by signal —
	// e.g. "killed", "terminated". Empty for normal exits.
	Signal string
	// At is the wall-clock moment the runtime observed the exit.
	At time.Time
}

// RuntimeOptions configures a Runtime. All fields are optional except
// BinaryPath, ModelStore, and Starter.
type RuntimeOptions struct {
	// BinaryPath points at llama-server. The Runtime treats this as
	// opaque — the Starter is responsible for invoking it. An empty
	// BinaryPath leaves the Runtime in "unavailable" mode and every
	// EnsureLoaded returns ErrRuntimeUnavailable.
	BinaryPath string
	// DataDir is the prefix InstalledModel.FilePath is resolved
	// against when computing the absolute model path. Must match
	// the dataDir the installer was wired with.
	DataDir string
	// ModelStore reads InstalledModel rows by ID. The Runtime never
	// writes through this interface — only the installer does.
	ModelStore ModelLookup
	// Starter spawns child processes. Production uses ExecProcessStarter.
	Starter ProcessStarter
	// Clock backs StartedAt / LastErrorAt. Defaults to time.Now.
	Clock Clock
	// HealthTimeout caps how long EnsureLoaded waits for a fresh
	// child to report healthy. Defaults to 30s.
	HealthTimeout time.Duration
	// StopTimeout is the grace period given to a child after the
	// graceful kill before SIGKILL escalation. Defaults to 5s.
	StopTimeout time.Duration
	// MaxResident is the LRU keep-warm cap — the maximum number of
	// llama-server children the Runtime keeps loaded at once.
	// Defaults to 1 (preserves v1 single-child restart-on-switch
	// behavior). Bump for hosts with enough RAM to keep multiple
	// models warm; EnsureLoaded picks the right child or evicts
	// the LRU when capacity is reached. The operator's gates on
	// memory pressure — Hecate does not auto-tune this.
	MaxResident int
}

// ModelLookup is the slim controlplane.Store surface the Runtime
// needs. Defined locally for test-stubbing parity with InstallerStore.
type ModelLookup interface {
	// LookupInstalled returns the model row for ID or an error. The
	// real store derives this from State.InstalledModels.
	LookupInstalled(ctx context.Context, id string) (InstalledModel, error)
}

// Runtime supervises llama-server child processes. v1 capped at one
// child; v2 supports an LRU keep-warm pool of N children
// (MaxResident). With MaxResident=1 the behavior is identical to v1
// — switching models stops the active child before starting the new
// one. With MaxResident > 1, EnsureLoaded picks the right child or
// evicts the LRU.
//
// Concurrent EnsureLoaded calls serialize through the runtime's
// mutex. The mutex is released during slow operations (spawn +
// health-poll) so Status reads don't block.
type Runtime struct {
	opts resolvedRuntimeOptions

	mu sync.Mutex
	// state mirrors the "primary" session — the most recently
	// loaded / interacted-with model. UI surfaces this single
	// state for backwards compatibility; SessionsSnapshot returns
	// the per-session detail when callers want the full picture.
	state RuntimeState
	// active is the primary session — i.e. sessions[primaryID].
	// Held as a pointer alongside the map so existing v1 code
	// paths (Status, Stop, watchChild) continue to reach for
	// r.active. nil when no session is primary (idle).
	active    *runtimeSession
	sessions  map[string]*runtimeSession
	lruOrder  []string
	primaryID string
	// lastError / lastErrorAt persist across the failed state so the
	// UI can render them until the next start attempt clears them.
	lastError   string
	lastErrorAt time.Time
}

type resolvedRuntimeOptions struct {
	binaryPath    string
	dataDir       string
	store         ModelLookup
	starter       ProcessStarter
	clock         Clock
	healthTimeout time.Duration
	stopTimeout   time.Duration
	maxResident   int
}

// runtimeSession is the per-running-child state. Lives in
// Runtime.active while the child is starting / running / stopping;
// nil-ed out on transition to idle/failed.
type runtimeSession struct {
	modelID   string
	handle    ProcessHandle
	spawnedAt time.Time // when EnsureLoaded began spawning the child
	startedAt time.Time // when /health first returned OK

	// span is the runtime-lifecycle OTel span. Receives
	// runtime.starting / .started / .stopped / .crashed events as
	// the state machine transitions; ends on transition to idle or
	// failed. nil under the noop tracer.
	span trace.Span

	// watcherDone closes when the crash-listener goroutine returns —
	// used by Stop / EnsureLoaded to deterministically wait for
	// teardown before transitioning out of stopping.
	watcherDone chan struct{}
}

// NewRuntime wires a runtime. Returns an error if any of the required
// dependencies are missing; an empty BinaryPath is allowed (signals
// "feature dormant" — EnsureLoaded returns ErrRuntimeUnavailable).
func NewRuntime(opts RuntimeOptions) (*Runtime, error) {
	if opts.DataDir == "" {
		return nil, errors.New("runtime: DataDir is required")
	}
	if opts.ModelStore == nil {
		return nil, errors.New("runtime: ModelStore is required")
	}
	if opts.Starter == nil {
		return nil, errors.New("runtime: Starter is required")
	}
	resolved := resolvedRuntimeOptions{
		binaryPath:    opts.BinaryPath,
		dataDir:       opts.DataDir,
		store:         opts.ModelStore,
		starter:       opts.Starter,
		clock:         opts.Clock,
		healthTimeout: opts.HealthTimeout,
		stopTimeout:   opts.StopTimeout,
		maxResident:   opts.MaxResident,
	}
	if resolved.clock == nil {
		resolved.clock = time.Now
	}
	if resolved.healthTimeout <= 0 {
		resolved.healthTimeout = 30 * time.Second
	}
	if resolved.stopTimeout <= 0 {
		resolved.stopTimeout = 5 * time.Second
	}
	if resolved.maxResident <= 0 {
		resolved.maxResident = 1
	}
	return &Runtime{
		opts:     resolved,
		state:    RuntimeIdle,
		sessions: make(map[string]*runtimeSession),
	}, nil
}

// Available reports whether the runtime has a binary configured. The
// API handlers use this to short-circuit /runtime/start with a 503
// when the feature is dormant.
func (r *Runtime) Available() bool {
	return strings.TrimSpace(r.opts.binaryPath) != ""
}

// ResidentSessionSummary describes one resident session. The order in
// SessionsSnapshot's return matches LRU order, oldest first.
type ResidentSessionSummary struct {
	ModelID   string    `json:"model_id"`
	State     string    `json:"state"`
	Port      int       `json:"port,omitempty"`
	PID       int       `json:"pid,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	Primary   bool      `json:"primary,omitempty"`
}

// SessionsSnapshot returns a snapshot of every resident session,
// LRU-ordered (oldest first). UI callers render this for the
// multi-resident keep-warm view (MaxResident > 1). Single-resident
// callers may keep using Status() — they'll see one entry here too.
func (r *Runtime) SessionsSnapshot() []ResidentSessionSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ResidentSessionSummary, 0, len(r.sessions))
	for _, id := range r.lruOrder {
		session, ok := r.sessions[id]
		if !ok || session == nil {
			continue
		}
		summary := ResidentSessionSummary{
			ModelID:   id,
			State:     residentSessionState(session),
			StartedAt: session.startedAt,
			Primary:   id == r.primaryID,
		}
		if session.handle != nil {
			summary.Port = session.handle.Port()
			summary.PID = session.handle.PID()
		}
		out = append(out, summary)
	}
	return out
}

// MaxResident returns the configured cap. Diagnostic surface for the
// UI ("3 / 5 resident").
func (r *Runtime) MaxResident() int {
	return r.opts.maxResident
}

func residentSessionState(s *runtimeSession) string {
	if s == nil {
		return "idle"
	}
	if !s.startedAt.IsZero() {
		return "running"
	}
	return "starting"
}

// Status snapshots the current state. Safe to call concurrently with
// EnsureLoaded / Stop — the read takes the same mutex.
func (r *Runtime) Status() RuntimeStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := RuntimeStatus{
		State:       r.state,
		LastError:   r.lastError,
		LastErrorAt: r.lastErrorAt,
	}
	if r.active != nil {
		status.ActiveModelID = r.active.modelID
		if r.active.handle != nil {
			status.Port = r.active.handle.Port()
			status.PID = r.active.handle.PID()
		}
		status.StartedAt = r.active.startedAt
	}
	return status
}

// ActiveBaseURL returns the URL the proxy should forward requests to
// for the requested model. Looks up the model in the resident-set
// map; ErrRuntimeNotRunning when no session is resident,
// ErrRuntimeWrongModel when the requested model isn't loaded (an
// empty modelID matches the primary session for v1-compat). Both
// map to local_model_runtime_unavailable.
func (r *Runtime) ActiveBaseURL(modelID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Empty modelID: legacy v1 callers that just want "the running
	// model". Falls back to the primary session.
	if modelID == "" {
		if r.active == nil || r.active.handle == nil || r.state != RuntimeRunning {
			return "", ErrRuntimeNotRunning
		}
		return fmt.Sprintf("http://%s:%d", r.active.handle.Host(), r.active.handle.Port()), nil
	}
	session, ok := r.sessions[modelID]
	if !ok || session.handle == nil {
		// Nothing loaded → not-running; loaded but with a different
		// id → wrong-model. The runtime is one or the other from the
		// caller's perspective.
		if r.active != nil {
			return "", fmt.Errorf("%w: resident %q, requested %q",
				ErrRuntimeWrongModel, r.activeModelIDs(), modelID)
		}
		return "", ErrRuntimeNotRunning
	}
	// Mark this model as primary so subsequent Status reads reflect
	// the most recently used. The LRU order is also bumped so the
	// next eviction targets a colder model.
	r.touchLRULocked(modelID)
	r.primaryID = modelID
	r.active = session
	return fmt.Sprintf("http://%s:%d", session.handle.Host(), session.handle.Port()), nil
}

// activeModelIDs renders a short diagnostic for error messages.
// Must be called with r.mu held.
func (r *Runtime) activeModelIDs() string {
	if len(r.lruOrder) == 0 {
		return ""
	}
	out := r.lruOrder[0]
	for _, id := range r.lruOrder[1:] {
		out += "," + id
	}
	return out
}

// touchLRULocked moves modelID to the tail of the LRU order. Must
// be called with r.mu held.
func (r *Runtime) touchLRULocked(modelID string) {
	for i, id := range r.lruOrder {
		if id == modelID {
			r.lruOrder = append(r.lruOrder[:i], r.lruOrder[i+1:]...)
			break
		}
	}
	r.lruOrder = append(r.lruOrder, modelID)
}

// EnsureLoaded transitions the runtime to "running with modelID".
// If the runtime is already running this model, returns the live
// base URL immediately. If it's running a different model, stops
// it first, then starts the new one. Returns ErrRuntimeUnavailable
// when the runtime has no binary configured.
//
// Serialized through the runtime mutex — concurrent callers queue.
// The first caller drives the transition; subsequent callers see
// the already-running state and return immediately.
func (r *Runtime) EnsureLoaded(ctx context.Context, modelID string) (string, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return "", errors.New("ensure loaded: model id is required")
	}
	if !r.Available() {
		return "", ErrRuntimeUnavailable
	}
	model, err := r.opts.store.LookupInstalled(ctx, modelID)
	if err != nil {
		return "", err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Hot path: model already resident. Bump LRU + promote to
	// primary; return its base URL without touching the child.
	if existing, ok := r.sessions[modelID]; ok && existing.handle != nil {
		r.touchLRULocked(modelID)
		r.primaryID = modelID
		r.active = existing
		r.state = RuntimeRunning
		return fmt.Sprintf("http://%s:%d", existing.handle.Host(), existing.handle.Port()), nil
	}

	// Capacity check. Evict the LRU child(ren) until there's room
	// for one more. When MaxResident=1 this evicts the previous
	// (and only) session — same shape as v1's stop-then-start.
	for len(r.sessions) >= r.opts.maxResident {
		victimID := r.lruOrder[0]
		victim := r.sessions[victimID]
		if victim == nil {
			// Defensive: stale entry in lruOrder. Drop and continue.
			r.lruOrder = r.lruOrder[1:]
			continue
		}
		if err := r.stopSessionLocked(ctx, victimID, victim); err != nil {
			// Best-effort: log in lastError but continue with the
			// new start. A dead child is indistinguishable from a
			// successful stop for our purposes.
			r.lastError = fmt.Sprintf("evict %s: %v", victimID, err)
			r.lastErrorAt = r.opts.clock()
		}
	}

	// Resolve absolute model path. InstalledModel.FilePath is stored
	// relative to dataDir for portability.
	modelPath := model.FilePath
	if !filepath.IsAbs(modelPath) {
		modelPath = filepath.Join(r.opts.dataDir, modelPath)
	}
	port, err := freeTCPPort()
	if err != nil {
		r.transitionFailedLocked(fmt.Sprintf("pick port: %v", err))
		return "", err
	}
	contextSize := model.RecommendedContext
	if contextSize <= 0 {
		contextSize = 4096
	}

	r.state = RuntimeStarting
	// startRuntimeSpan attaches the canonical attributes (engine,
	// model id, port, context_size). Span lives as long as the
	// session — closed by stopSessionLocked / watchChild on
	// transition out.
	_, span := startRuntimeSpan(ctx, modelID, port, contextSize)
	session := &runtimeSession{
		modelID:   modelID,
		spawnedAt: r.opts.clock(),
		span:      span,
		// watcherDone stays nil until the crash listener is
		// actually spawned. A concurrent Stop seen between this
		// point and `go r.watchChild(session)` below would
		// otherwise block forever on a channel no goroutine will
		// close — see stopSessionLocked's `if watcher != nil`
		// guard.
	}
	r.sessions[modelID] = session
	r.touchLRULocked(modelID)
	r.primaryID = modelID
	r.active = session
	// Release while starting + health-polling so Status / Stop can be
	// observed mid-transition.
	r.mu.Unlock()

	handle, startErr := r.opts.starter.Start(ctx, ProcessStartOptions{
		BinaryPath:  r.opts.binaryPath,
		ModelPath:   modelPath,
		Host:        "127.0.0.1",
		Port:        port,
		ContextSize: contextSize,
	})

	r.mu.Lock()
	if startErr != nil {
		// Remove the orphaned session from the pool, mark the
		// failure, and surface it. The session never reached
		// "running" so there's no watcher to coordinate with.
		delete(r.sessions, modelID)
		r.dropLRUEntryLocked(modelID)
		r.transitionFailedLocked(fmt.Sprintf("start child: %v", startErr))
		// Mutex must be locked when we return from the deferred
		// Unlock above; the outer defer takes care of the final
		// release.
		return "", startErr
	}
	session.handle = handle
	// watcherDone must be created *before* the goroutine is
	// spawned so Stop's `<-watcher` receive can never observe
	// "nil channel after spawn". The goroutine guarantees the
	// close in its defer.
	session.watcherDone = make(chan struct{})

	// Spawn crash listener while we still hold the mutex so the
	// observation can never race a stop called from EnsureLoaded
	// itself. The listener takes the mutex inside its body — see
	// watchChild.
	go r.watchChild(session)

	// Release for the health wait.
	r.mu.Unlock()

	healthCtx, cancel := context.WithTimeout(ctx, r.opts.healthTimeout)
	healthErr := handle.WaitForHealth(healthCtx)
	cancel()

	r.mu.Lock()
	if healthErr != nil {
		// Tear the child down so a half-loaded model isn't left
		// running. The pool entry is the failing session — drop it
		// from sessions/lru so capacity opens up for the next
		// EnsureLoaded.
		_ = handle.Stop(context.Background(), r.opts.stopTimeout)
		delete(r.sessions, modelID)
		r.dropLRUEntryLocked(modelID)
		r.transitionFailedLocked(fmt.Sprintf("wait for health: %v", healthErr))
		return "", healthErr
	}

	r.state = RuntimeRunning
	session.startedAt = r.opts.clock()
	r.active = session
	r.primaryID = modelID
	r.lastError = ""
	r.lastErrorAt = time.Time{}

	// Time-to-first-healthy = (running transition - spawn start).
	// Surfaces cold-load regressions across model sizes in traces
	// without an extra metric instrument.
	if session.span != nil {
		ttfh := session.startedAt.Sub(session.spawnedAt)
		if ttfh < 0 {
			ttfh = 0
		}
		recordRuntimeStarted(session.span, modelID, handle.PID(), ttfh.Milliseconds())
	}

	return fmt.Sprintf("http://%s:%d", handle.Host(), handle.Port()), nil
}

// dropLRUEntryLocked removes modelID from the LRU order. Must be
// called with r.mu held.
func (r *Runtime) dropLRUEntryLocked(modelID string) {
	for i, id := range r.lruOrder {
		if id == modelID {
			r.lruOrder = append(r.lruOrder[:i], r.lruOrder[i+1:]...)
			return
		}
	}
}

// Stop kills every resident child. Idempotent — returns nil when
// already idle. Synchronous: blocks until each child has exited or
// the stop timeout elapses. The first error encountered is
// returned; subsequent stops still run so a partial-failure host
// doesn't leak children.
func (r *Runtime) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sessions) == 0 {
		// Already idle. Clear any failed marker so the next status
		// read shows clean idle.
		r.state = RuntimeIdle
		r.active = nil
		r.primaryID = ""
		return nil
	}
	var firstErr error
	// Snapshot the ids so the iteration order is deterministic and
	// the map mutation inside stopSessionLocked doesn't trip us.
	ids := make([]string, 0, len(r.sessions))
	for id := range r.sessions {
		ids = append(ids, id)
	}
	for _, id := range ids {
		session := r.sessions[id]
		if session == nil {
			continue
		}
		if err := r.stopSessionLocked(ctx, id, session); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	r.state = RuntimeIdle
	r.active = nil
	r.primaryID = ""
	return firstErr
}

// stopSessionLocked stops a single named session. Must be called
// with r.mu held; releases the mutex while waiting for child exit
// so Status reads from other goroutines don't stall. Removes the
// session from the pool and the LRU order on return.
func (r *Runtime) stopSessionLocked(ctx context.Context, modelID string, session *runtimeSession) error {
	if session == nil {
		delete(r.sessions, modelID)
		r.dropLRUEntryLocked(modelID)
		return nil
	}
	handle := session.handle
	watcher := session.watcherDone
	span := session.span
	startedAt := session.startedAt
	r.state = RuntimeStopping
	r.mu.Unlock()

	var stopErr error
	if handle != nil {
		stopErr = handle.Stop(ctx, r.opts.stopTimeout)
	}
	if watcher != nil {
		<-watcher
	}

	r.mu.Lock()
	// Emit the operator-stop event on the span before ending it.
	// The watcher may have recorded a crashed event if the child
	// died during stop — recordRuntimeStopped layers on top of
	// that, and the runtime.reason attribute tells dashboards
	// which path fired.
	if span != nil {
		uptime := time.Duration(0)
		if !startedAt.IsZero() {
			uptime = r.opts.clock().Sub(startedAt)
		}
		recordRuntimeStopped(span, modelID, "operator", uptime.Milliseconds())
		span.End()
	}
	delete(r.sessions, modelID)
	r.dropLRUEntryLocked(modelID)
	if r.active == session {
		r.active = nil
		r.primaryID = ""
	}
	return stopErr
}

// stopActiveLocked is the legacy v1 entry point that stops the
// primary session. Retained for backwards-compatible call sites
// (none today, but tests may reach in via the unexported method
// name). Delegates to stopSessionLocked.
func (r *Runtime) stopActiveLocked(ctx context.Context) error {
	if r.active == nil {
		return nil
	}
	return r.stopSessionLocked(ctx, r.active.modelID, r.active)
}

// watchChild listens for the child's exit. If the child exits while
// state is RuntimeRunning, the runtime classifies the exit as a crash
// and transitions to RuntimeFailed. Exits during RuntimeStopping are
// expected — they're the result of Stop being called.
func (r *Runtime) watchChild(session *runtimeSession) {
	defer func() {
		// Channel may be nil if EnsureLoaded failed between
		// session creation and watcher spawn — defensive only,
		// the spawn happens with watcherDone already set.
		if session.watcherDone != nil {
			close(session.watcherDone)
		}
	}()

	if session.handle == nil {
		return
	}
	info := <-session.handle.Exited()

	r.mu.Lock()
	defer r.mu.Unlock()
	// If this session has already been removed from the pool
	// (operator stop / eviction), the exit is expected and we
	// don't touch state.
	current := r.sessions[session.modelID]
	if current != session {
		return
	}
	switch r.state {
	case RuntimeStopping:
		// Expected — stopSessionLocked is in progress; it'll
		// transition state to idle once the watcher closes.
	default:
		// Unexpected exit. Classify as crash.
		msg := fmt.Sprintf("child exited with code %d", info.ExitCode)
		if info.Signal != "" {
			msg = fmt.Sprintf("child terminated by %s", info.Signal)
		}
		// Span-record the crash before clearing the pool entry so
		// the timeline shows the failure with its exit code. Set
		// the span status to Error since the operator didn't drive
		// this transition.
		if session.span != nil {
			recordRuntimeCrashed(session.span, session.modelID, info.ExitCode, info.Signal)
			session.span.SetStatus(codes.Error, msg)
			session.span.End()
		}
		delete(r.sessions, session.modelID)
		r.dropLRUEntryLocked(session.modelID)
		r.lastError = msg
		r.lastErrorAt = r.opts.clock()
		if r.active == session {
			r.active = nil
			r.primaryID = ""
		}
		// If other sessions are still resident, drop back to
		// running; otherwise mark failed.
		if len(r.sessions) > 0 {
			r.state = RuntimeRunning
			// Promote the LRU tail as the new primary.
			if len(r.lruOrder) > 0 {
				r.primaryID = r.lruOrder[len(r.lruOrder)-1]
				r.active = r.sessions[r.primaryID]
			}
		} else {
			r.state = RuntimeFailed
		}
	}
}

// transitionFailedLocked must be called with r.mu held. Captures a
// failure and clears active state. Used by EnsureLoaded for failures
// it observes before installing the crash watcher.
func (r *Runtime) transitionFailedLocked(msg string) {
	// Close out the span on a failed transition so traces show the
	// abort with its message. Health-failure and spawn-failure paths
	// land here.
	if r.active != nil && r.active.span != nil {
		r.active.span.SetStatus(codes.Error, msg)
		r.active.span.End()
	}
	r.state = RuntimeFailed
	r.active = nil
	r.lastError = msg
	r.lastErrorAt = r.opts.clock()
}

var (
	// ErrRuntimeUnavailable signals that the feature is dormant —
	// no llama-server binary is configured. Handlers map this to a
	// 503 with code local_models_unavailable.
	ErrRuntimeUnavailable = errors.New("local model runtime is not available in this build")

	// ErrRuntimeNotRunning is returned by ActiveBaseURL when no
	// child is running. Maps to local_model_runtime_unavailable.
	ErrRuntimeNotRunning = errors.New("no local model runtime is running")

	// ErrRuntimeWrongModel is returned by ActiveBaseURL when the
	// running model doesn't match the requested model id. Maps to
	// local_model_runtime_unavailable; the proxy may auto-EnsureLoaded
	// to recover before failing the request.
	ErrRuntimeWrongModel = errors.New("runtime is running a different model")
)
