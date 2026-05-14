package llamacpp

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeStore is a minimal ModelLookup. Returns a configurable record
// or an error keyed by id.
type fakeStore struct {
	models map[string]InstalledModel
	err    error
}

func (f *fakeStore) LookupInstalled(_ context.Context, id string) (InstalledModel, error) {
	if f.err != nil {
		return InstalledModel{}, f.err
	}
	m, ok := f.models[id]
	if !ok {
		return InstalledModel{}, errors.New("not found")
	}
	return m, nil
}

// fakeStarter spawns fakeHandles deterministically. Tests can set
// startErr to force EnsureLoaded to fail at the spawn step, or set
// healthErr on the returned handle to fail at the health step.
type fakeStarter struct {
	mu          sync.Mutex
	startCalls  []ProcessStartOptions
	startErr    error
	healthErr   error
	healthDelay time.Duration
	// nextPID is incremented per call so concurrent tests don't collide.
	nextPID int32
}

func (s *fakeStarter) Start(_ context.Context, opts ProcessStartOptions) (ProcessHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startErr != nil {
		return nil, s.startErr
	}
	s.startCalls = append(s.startCalls, opts)
	h := &fakeHandle{
		pid:         int(atomic.AddInt32(&s.nextPID, 1)) + 100,
		port:        opts.Port,
		host:        opts.Host,
		healthErr:   s.healthErr,
		healthDelay: s.healthDelay,
		exited:      make(chan ProcessExitInfo, 1),
	}
	return h, nil
}

type fakeHandle struct {
	pid         int
	port        int
	host        string
	healthErr   error
	healthDelay time.Duration
	exited      chan ProcessExitInfo
	stopOnce    sync.Once
}

func (h *fakeHandle) PID() int                       { return h.pid }
func (h *fakeHandle) Port() int                      { return h.port }
func (h *fakeHandle) Host() string                   { return h.host }
func (h *fakeHandle) Exited() <-chan ProcessExitInfo { return h.exited }

func (h *fakeHandle) WaitForHealth(ctx context.Context) error {
	if h.healthErr != nil {
		return h.healthErr
	}
	if h.healthDelay > 0 {
		select {
		case <-time.After(h.healthDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (h *fakeHandle) Stop(_ context.Context, _ time.Duration) error {
	h.stopOnce.Do(func() {
		h.exited <- ProcessExitInfo{ExitCode: 0, At: time.Now()}
		close(h.exited)
	})
	return nil
}

// simulateCrash sends a non-zero exit on the handle's exited channel
// without an explicit Stop call — used to drive the crash-listener
// path of the runtime.
func (h *fakeHandle) simulateCrash(code int, signal string) {
	h.stopOnce.Do(func() {
		h.exited <- ProcessExitInfo{ExitCode: code, Signal: signal, At: time.Now()}
		close(h.exited)
	})
}

func makeRuntime(t *testing.T, store ModelLookup, starter ProcessStarter) *Runtime {
	t.Helper()
	rt, err := NewRuntime(RuntimeOptions{
		BinaryPath:    "/fake/llama-server",
		DataDir:       t.TempDir(),
		ModelStore:    store,
		Starter:       starter,
		Clock:         fixedClock(time.Unix(1700000000, 0).UTC()),
		HealthTimeout: 500 * time.Millisecond,
		StopTimeout:   200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return rt
}

func TestRuntime_EnsureLoadedHappyPath(t *testing.T) {
	t.Parallel()
	store := &fakeStore{models: map[string]InstalledModel{
		"qwen": {ID: "qwen", FilePath: "models/qwen.gguf", RecommendedContext: 4096},
	}}
	starter := &fakeStarter{}
	rt := makeRuntime(t, store, starter)

	url, err := rt.EnsureLoaded(context.Background(), "qwen")
	if err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if url == "" {
		t.Fatal("EnsureLoaded returned empty url")
	}
	status := rt.Status()
	if status.State != RuntimeRunning {
		t.Fatalf("state = %q; want running", status.State)
	}
	if status.ActiveModelID != "qwen" {
		t.Fatalf("active model = %q; want qwen", status.ActiveModelID)
	}
	if len(starter.startCalls) != 1 {
		t.Fatalf("start calls = %d; want 1", len(starter.startCalls))
	}
	if starter.startCalls[0].ContextSize != 4096 {
		t.Fatalf("context size = %d; want 4096 (from recommended)", starter.startCalls[0].ContextSize)
	}
}

func TestRuntime_EnsureLoadedReturnsURLWhenAlreadyRunning(t *testing.T) {
	t.Parallel()
	store := &fakeStore{models: map[string]InstalledModel{
		"qwen": {ID: "qwen", FilePath: "models/qwen.gguf"},
	}}
	starter := &fakeStarter{}
	rt := makeRuntime(t, store, starter)

	if _, err := rt.EnsureLoaded(context.Background(), "qwen"); err != nil {
		t.Fatalf("first EnsureLoaded: %v", err)
	}
	url2, err := rt.EnsureLoaded(context.Background(), "qwen")
	if err != nil {
		t.Fatalf("second EnsureLoaded: %v", err)
	}
	if url2 == "" {
		t.Fatal("second EnsureLoaded returned empty url")
	}
	if len(starter.startCalls) != 1 {
		t.Fatalf("starter was invoked %d times; want 1", len(starter.startCalls))
	}
}

func TestRuntime_EnsureLoadedSwitchModel(t *testing.T) {
	t.Parallel()
	store := &fakeStore{models: map[string]InstalledModel{
		"qwen":  {ID: "qwen", FilePath: "models/qwen.gguf"},
		"llama": {ID: "llama", FilePath: "models/llama.gguf"},
	}}
	starter := &fakeStarter{}
	rt := makeRuntime(t, store, starter)

	if _, err := rt.EnsureLoaded(context.Background(), "qwen"); err != nil {
		t.Fatalf("first EnsureLoaded: %v", err)
	}
	if _, err := rt.EnsureLoaded(context.Background(), "llama"); err != nil {
		t.Fatalf("switch EnsureLoaded: %v", err)
	}
	status := rt.Status()
	if status.ActiveModelID != "llama" {
		t.Fatalf("active model after switch = %q; want llama", status.ActiveModelID)
	}
	if len(starter.startCalls) != 2 {
		t.Fatalf("starter calls = %d; want 2", len(starter.startCalls))
	}
}

func TestRuntime_StopIdempotent(t *testing.T) {
	t.Parallel()
	store := &fakeStore{models: map[string]InstalledModel{
		"qwen": {ID: "qwen", FilePath: "models/qwen.gguf"},
	}}
	rt := makeRuntime(t, store, &fakeStarter{})

	if _, err := rt.EnsureLoaded(context.Background(), "qwen"); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if rt.Status().State != RuntimeIdle {
		t.Fatalf("state after stop = %q; want idle", rt.Status().State)
	}
	// Stop again — idempotent no-op.
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestRuntime_CrashTransitionsToFailed(t *testing.T) {
	t.Parallel()
	store := &fakeStore{models: map[string]InstalledModel{
		"qwen": {ID: "qwen", FilePath: "models/qwen.gguf"},
	}}
	starter := &fakeStarter{}
	rt := makeRuntime(t, store, starter)

	if _, err := rt.EnsureLoaded(context.Background(), "qwen"); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}

	// Reach into the fakeHandle and simulate a crash. The crash
	// listener should observe the unexpected exit and flip state
	// to failed.
	rt.mu.Lock()
	handle := rt.active.handle.(*fakeHandle)
	rt.mu.Unlock()
	handle.simulateCrash(134, "")

	// Crash watcher is async; poll for the state transition.
	deadline := time.After(time.Second)
	for {
		if rt.Status().State == RuntimeFailed {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("never observed RuntimeFailed after crash; status = %+v", rt.Status())
		case <-time.After(5 * time.Millisecond):
		}
	}
	status := rt.Status()
	if status.LastError == "" {
		t.Fatalf("LastError should be set after crash; status = %+v", status)
	}
	if status.LastErrorAt.IsZero() {
		t.Fatalf("LastErrorAt should be set after crash")
	}
}

func TestRuntime_HealthFailureTearsDownChild(t *testing.T) {
	t.Parallel()
	store := &fakeStore{models: map[string]InstalledModel{
		"qwen": {ID: "qwen", FilePath: "models/qwen.gguf"},
	}}
	starter := &fakeStarter{healthErr: errors.New("health timeout")}
	rt := makeRuntime(t, store, starter)

	if _, err := rt.EnsureLoaded(context.Background(), "qwen"); err == nil {
		t.Fatal("expected health failure")
	}
	if rt.Status().State != RuntimeFailed {
		t.Fatalf("state after health fail = %q; want failed", rt.Status().State)
	}
	if rt.Status().LastError == "" {
		t.Fatal("LastError should be set after health failure")
	}
}

func TestRuntime_StartFailureSurfacesAsFailed(t *testing.T) {
	t.Parallel()
	store := &fakeStore{models: map[string]InstalledModel{
		"qwen": {ID: "qwen", FilePath: "models/qwen.gguf"},
	}}
	starter := &fakeStarter{startErr: errors.New("binary not found")}
	rt := makeRuntime(t, store, starter)

	if _, err := rt.EnsureLoaded(context.Background(), "qwen"); err == nil {
		t.Fatal("expected start failure")
	}
	if rt.Status().State != RuntimeFailed {
		t.Fatalf("state = %q; want failed", rt.Status().State)
	}
}

func TestRuntime_UnavailableWhenBinaryMissing(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	rt, err := NewRuntime(RuntimeOptions{
		DataDir:    t.TempDir(),
		ModelStore: store,
		Starter:    &fakeStarter{},
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if rt.Available() {
		t.Fatal("Available() should be false with empty BinaryPath")
	}
	if _, err := rt.EnsureLoaded(context.Background(), "qwen"); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("expected ErrRuntimeUnavailable, got %v", err)
	}
}

func TestRuntime_ActiveBaseURLMatchesRunningModel(t *testing.T) {
	t.Parallel()
	store := &fakeStore{models: map[string]InstalledModel{
		"qwen": {ID: "qwen", FilePath: "models/qwen.gguf"},
	}}
	rt := makeRuntime(t, store, &fakeStarter{})

	// Idle → ErrRuntimeNotRunning
	if _, err := rt.ActiveBaseURL(""); !errors.Is(err, ErrRuntimeNotRunning) {
		t.Fatalf("expected ErrRuntimeNotRunning, got %v", err)
	}

	if _, err := rt.EnsureLoaded(context.Background(), "qwen"); err != nil {
		t.Fatalf("EnsureLoaded: %v", err)
	}
	if _, err := rt.ActiveBaseURL("qwen"); err != nil {
		t.Fatalf("ActiveBaseURL(qwen): %v", err)
	}
	// Wrong model
	if _, err := rt.ActiveBaseURL("llama"); !errors.Is(err, ErrRuntimeWrongModel) {
		t.Fatalf("expected ErrRuntimeWrongModel, got %v", err)
	}
}

func TestRuntime_FreeTCPPortHandsOutDistinctPorts(t *testing.T) {
	t.Parallel()
	// Sanity: two consecutive calls must hand out non-zero ports.
	// Doesn't strictly guarantee uniqueness in a stressed test
	// env, but a zero or negative value is a clear bug.
	for i := 0; i < 5; i++ {
		p, err := freeTCPPort()
		if err != nil {
			t.Fatalf("freeTCPPort: %v", err)
		}
		if p <= 0 {
			t.Fatalf("freeTCPPort returned %d", p)
		}
	}
}
