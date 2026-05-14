package llamacpp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
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
}

// ModelLookup is the slim controlplane.Store surface the Runtime
// needs. Defined locally for test-stubbing parity with InstallerStore.
type ModelLookup interface {
	// LookupInstalled returns the model row for ID or an error. The
	// real store derives this from State.InstalledModels.
	LookupInstalled(ctx context.Context, id string) (InstalledModel, error)
}

// Runtime supervises a single llama-server child process. v1 holds at
// most one child at a time; switching models stops the active child
// before starting the new one. Concurrent EnsureLoaded calls
// serialize through the runtime's mutex.
type Runtime struct {
	opts resolvedRuntimeOptions

	mu     sync.Mutex
	state  RuntimeState
	active *runtimeSession
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
}

// runtimeSession is the per-running-child state. Lives in
// Runtime.active while the child is starting / running / stopping;
// nil-ed out on transition to idle/failed.
type runtimeSession struct {
	modelID   string
	handle    ProcessHandle
	startedAt time.Time

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
	return &Runtime{
		opts:  resolved,
		state: RuntimeIdle,
	}, nil
}

// Available reports whether the runtime has a binary configured. The
// API handlers use this to short-circuit /runtime/start with a 503
// when the feature is dormant.
func (r *Runtime) Available() bool {
	return strings.TrimSpace(r.opts.binaryPath) != ""
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
// for the currently-running model. Returns ErrRuntimeNotRunning if no
// child is running, ErrRuntimeWrongModel if the running model doesn't
// match modelID. Both map to local_model_runtime_unavailable.
func (r *Runtime) ActiveBaseURL(modelID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != RuntimeRunning || r.active == nil || r.active.handle == nil {
		return "", ErrRuntimeNotRunning
	}
	if modelID != "" && r.active.modelID != modelID {
		return "", fmt.Errorf("%w: running %q, requested %q", ErrRuntimeWrongModel, r.active.modelID, modelID)
	}
	return fmt.Sprintf("http://%s:%d", r.active.handle.Host(), r.active.handle.Port()), nil
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

	if r.state == RuntimeRunning && r.active != nil && r.active.modelID == modelID {
		return fmt.Sprintf("http://%s:%d", r.active.handle.Host(), r.active.handle.Port()), nil
	}

	// Anything else means we need to (maybe) stop and (definitely)
	// start. The transition can take 30s for the cold-load — we
	// release the mutex while waiting for health so Status reads
	// don't block, then re-acquire to commit.
	if r.active != nil {
		if err := r.stopActiveLocked(ctx); err != nil {
			// Best-effort: log the stop error in lastError but
			// proceed to attempt the start anyway. A dead child is
			// indistinguishable from a successful stop for our
			// purposes; the new start will pick a fresh port.
			r.lastError = fmt.Sprintf("stop previous: %v", err)
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
	r.active = &runtimeSession{
		modelID:     modelID,
		watcherDone: make(chan struct{}),
	}
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
		r.transitionFailedLocked(fmt.Sprintf("start child: %v", startErr))
		// Mutex must be locked when we return from the deferred
		// Unlock above; the outer defer takes care of the final
		// release.
		return "", startErr
	}
	r.active.handle = handle

	// Spawn crash listener while we still hold the mutex so the
	// observation can never race a stopActiveLocked called from
	// EnsureLoaded itself. The listener takes the mutex inside its
	// body — see watchChild.
	go r.watchChild(r.active)

	// Release for the health wait.
	r.mu.Unlock()

	healthCtx, cancel := context.WithTimeout(ctx, r.opts.healthTimeout)
	healthErr := handle.WaitForHealth(healthCtx)
	cancel()

	r.mu.Lock()
	if healthErr != nil {
		// Tear the child down so a half-loaded model isn't left
		// running.
		_ = handle.Stop(context.Background(), r.opts.stopTimeout)
		// Drain the exited channel via the watcher; transitionFailed
		// captures the error.
		r.transitionFailedLocked(fmt.Sprintf("wait for health: %v", healthErr))
		return "", healthErr
	}

	r.state = RuntimeRunning
	r.active.startedAt = r.opts.clock()
	r.lastError = ""
	r.lastErrorAt = time.Time{}

	return fmt.Sprintf("http://%s:%d", handle.Host(), handle.Port()), nil
}

// Stop kills the active child if any. Idempotent — returns nil when
// already idle. Synchronous: blocks until the child has exited or
// the stop timeout elapses.
func (r *Runtime) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		// Already idle. Clear any failed marker so the next status
		// read shows clean idle.
		r.state = RuntimeIdle
		return nil
	}
	return r.stopActiveLocked(ctx)
}

// stopActiveLocked must be called with r.mu held. Transitions
// stopping → idle. Releases the mutex while waiting for child exit
// so Status calls don't stall.
func (r *Runtime) stopActiveLocked(ctx context.Context) error {
	if r.active == nil {
		return nil
	}
	handle := r.active.handle
	watcher := r.active.watcherDone
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
	r.state = RuntimeIdle
	r.active = nil
	return stopErr
}

// watchChild listens for the child's exit. If the child exits while
// state is RuntimeRunning, the runtime classifies the exit as a crash
// and transitions to RuntimeFailed. Exits during RuntimeStopping are
// expected — they're the result of Stop being called.
func (r *Runtime) watchChild(session *runtimeSession) {
	defer close(session.watcherDone)

	if session.handle == nil {
		return
	}
	info := <-session.handle.Exited()

	r.mu.Lock()
	defer r.mu.Unlock()
	// If r.active has been replaced (e.g. by a fast restart), don't
	// touch the new state.
	if r.active != session {
		return
	}
	switch r.state {
	case RuntimeStopping:
		// Expected — Stop will transition us to idle once it sees
		// the watcher channel close.
	default:
		// Unexpected exit. Classify as crash.
		msg := fmt.Sprintf("child exited with code %d", info.ExitCode)
		if info.Signal != "" {
			msg = fmt.Sprintf("child terminated by %s", info.Signal)
		}
		r.lastError = msg
		r.lastErrorAt = r.opts.clock()
		r.state = RuntimeFailed
		r.active = nil
	}
}

// transitionFailedLocked must be called with r.mu held. Captures a
// failure and clears active state. Used by EnsureLoaded for failures
// it observes before installing the crash watcher.
func (r *Runtime) transitionFailedLocked(msg string) {
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
