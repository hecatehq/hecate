package llamacpp

import (
	"context"
	"sync"
	"testing"
	"time"
)

// LRU keep-warm tests. v1 behavior (MaxResident=1) is covered by
// runtime_test.go; these target MaxResident > 1.

// makeLRURuntime mirrors makeRuntime but exposes MaxResident so
// tests can pick a cap > 1.
func makeLRURuntime(t *testing.T, store ModelLookup, starter ProcessStarter, maxResident int) *Runtime {
	t.Helper()
	rt, err := NewRuntime(RuntimeOptions{
		BinaryPath:    "/fake/llama-server",
		DataDir:       t.TempDir(),
		ModelStore:    store,
		Starter:       starter,
		Clock:         fixedClock(time.Unix(1700000000, 0).UTC()),
		HealthTimeout: 500 * time.Millisecond,
		StopTimeout:   200 * time.Millisecond,
		MaxResident:   maxResident,
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return rt
}

func TestRuntime_KeepsMultipleResident(t *testing.T) {
	t.Parallel()
	store := &fakeStore{models: map[string]InstalledModel{
		"a": {ID: "a", FilePath: "models/a.gguf"},
		"b": {ID: "b", FilePath: "models/b.gguf"},
	}}
	starter := &fakeStarter{}
	rt := makeLRURuntime(t, store, starter, 2)

	if _, err := rt.EnsureLoaded(context.Background(), "a"); err != nil {
		t.Fatalf("EnsureLoaded a: %v", err)
	}
	if _, err := rt.EnsureLoaded(context.Background(), "b"); err != nil {
		t.Fatalf("EnsureLoaded b: %v", err)
	}
	// Both children must remain resident — capacity isn't exceeded.
	sessions := rt.SessionsSnapshot()
	if len(sessions) != 2 {
		t.Fatalf("resident sessions = %d; want 2", len(sessions))
	}
	if len(starter.startCalls) != 2 {
		t.Fatalf("starter calls = %d; want 2 (one per model)", len(starter.startCalls))
	}
}

func TestRuntime_HotPathReusesResident(t *testing.T) {
	t.Parallel()
	store := &fakeStore{models: map[string]InstalledModel{
		"a": {ID: "a", FilePath: "models/a.gguf"},
		"b": {ID: "b", FilePath: "models/b.gguf"},
	}}
	starter := &fakeStarter{}
	rt := makeLRURuntime(t, store, starter, 2)

	urlA1, err := rt.EnsureLoaded(context.Background(), "a")
	if err != nil {
		t.Fatalf("first EnsureLoaded a: %v", err)
	}
	if _, err := rt.EnsureLoaded(context.Background(), "b"); err != nil {
		t.Fatalf("EnsureLoaded b: %v", err)
	}
	// Reloading a previously resident model must return the same
	// base URL without spawning a new child.
	urlA2, err := rt.EnsureLoaded(context.Background(), "a")
	if err != nil {
		t.Fatalf("second EnsureLoaded a: %v", err)
	}
	if urlA1 != urlA2 {
		t.Fatalf("URL for a changed across hot-path: %q → %q", urlA1, urlA2)
	}
	if len(starter.startCalls) != 2 {
		t.Fatalf("starter calls = %d; want 2 (a + b only)", len(starter.startCalls))
	}
}

func TestRuntime_EvictsLRUWhenCapacityExceeded(t *testing.T) {
	t.Parallel()
	store := &fakeStore{models: map[string]InstalledModel{
		"a": {ID: "a", FilePath: "models/a.gguf"},
		"b": {ID: "b", FilePath: "models/b.gguf"},
		"c": {ID: "c", FilePath: "models/c.gguf"},
	}}
	starter := &fakeStarter{}
	rt := makeLRURuntime(t, store, starter, 2)

	// Load a, then b. a is LRU.
	if _, err := rt.EnsureLoaded(context.Background(), "a"); err != nil {
		t.Fatalf("EnsureLoaded a: %v", err)
	}
	if _, err := rt.EnsureLoaded(context.Background(), "b"); err != nil {
		t.Fatalf("EnsureLoaded b: %v", err)
	}
	// Bump a to fresh — b is now LRU.
	if _, err := rt.EnsureLoaded(context.Background(), "a"); err != nil {
		t.Fatalf("bump a: %v", err)
	}
	// Load c → capacity exceeded → b should be evicted.
	if _, err := rt.EnsureLoaded(context.Background(), "c"); err != nil {
		t.Fatalf("EnsureLoaded c: %v", err)
	}
	sessions := rt.SessionsSnapshot()
	if len(sessions) != 2 {
		t.Fatalf("resident sessions = %d; want 2", len(sessions))
	}
	residentIDs := map[string]bool{}
	for _, s := range sessions {
		residentIDs[s.ModelID] = true
	}
	if !residentIDs["a"] || !residentIDs["c"] {
		t.Fatalf("expected {a, c} resident, got %+v", residentIDs)
	}
	if residentIDs["b"] {
		t.Fatal("b should have been evicted as the LRU")
	}
}

func TestRuntime_ActiveBaseURLLooksUpByModel(t *testing.T) {
	t.Parallel()
	store := &fakeStore{models: map[string]InstalledModel{
		"a": {ID: "a", FilePath: "models/a.gguf"},
		"b": {ID: "b", FilePath: "models/b.gguf"},
	}}
	starter := &fakeStarter{}
	rt := makeLRURuntime(t, store, starter, 2)

	if _, err := rt.EnsureLoaded(context.Background(), "a"); err != nil {
		t.Fatalf("EnsureLoaded a: %v", err)
	}
	if _, err := rt.EnsureLoaded(context.Background(), "b"); err != nil {
		t.Fatalf("EnsureLoaded b: %v", err)
	}

	// Both models resolve via ActiveBaseURL — the proxy can route
	// to either without re-spawning.
	if _, err := rt.ActiveBaseURL("a"); err != nil {
		t.Fatalf("ActiveBaseURL(a): %v", err)
	}
	if _, err := rt.ActiveBaseURL("b"); err != nil {
		t.Fatalf("ActiveBaseURL(b): %v", err)
	}
}

func TestRuntime_PrimaryFollowsMostRecentlyTouched(t *testing.T) {
	t.Parallel()
	store := &fakeStore{models: map[string]InstalledModel{
		"a": {ID: "a", FilePath: "models/a.gguf"},
		"b": {ID: "b", FilePath: "models/b.gguf"},
	}}
	rt := makeLRURuntime(t, store, &fakeStarter{}, 2)

	if _, err := rt.EnsureLoaded(context.Background(), "a"); err != nil {
		t.Fatalf("EnsureLoaded a: %v", err)
	}
	if _, err := rt.EnsureLoaded(context.Background(), "b"); err != nil {
		t.Fatalf("EnsureLoaded b: %v", err)
	}
	if rt.Status().ActiveModelID != "b" {
		t.Fatalf("primary = %q; want b (most recent load)", rt.Status().ActiveModelID)
	}
	// ActiveBaseURL("a") promotes a back to primary.
	if _, err := rt.ActiveBaseURL("a"); err != nil {
		t.Fatalf("ActiveBaseURL(a): %v", err)
	}
	if rt.Status().ActiveModelID != "a" {
		t.Fatalf("primary after ActiveBaseURL = %q; want a", rt.Status().ActiveModelID)
	}
}

func TestRuntime_StopShutsDownAllSessions(t *testing.T) {
	t.Parallel()
	store := &fakeStore{models: map[string]InstalledModel{
		"a": {ID: "a", FilePath: "models/a.gguf"},
		"b": {ID: "b", FilePath: "models/b.gguf"},
	}}
	rt := makeLRURuntime(t, store, &fakeStarter{}, 3)

	if _, err := rt.EnsureLoaded(context.Background(), "a"); err != nil {
		t.Fatalf("EnsureLoaded a: %v", err)
	}
	if _, err := rt.EnsureLoaded(context.Background(), "b"); err != nil {
		t.Fatalf("EnsureLoaded b: %v", err)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(rt.SessionsSnapshot()) != 0 {
		t.Fatal("expected zero resident sessions after Stop")
	}
	if rt.Status().State != RuntimeIdle {
		t.Fatalf("state after Stop = %q; want idle", rt.Status().State)
	}
}

func TestRuntime_CrashRemovesSessionFromPool(t *testing.T) {
	t.Parallel()
	store := &fakeStore{models: map[string]InstalledModel{
		"a": {ID: "a", FilePath: "models/a.gguf"},
		"b": {ID: "b", FilePath: "models/b.gguf"},
	}}
	rt := makeLRURuntime(t, store, &fakeStarter{}, 2)

	if _, err := rt.EnsureLoaded(context.Background(), "a"); err != nil {
		t.Fatalf("EnsureLoaded a: %v", err)
	}
	if _, err := rt.EnsureLoaded(context.Background(), "b"); err != nil {
		t.Fatalf("EnsureLoaded b: %v", err)
	}

	// Reach in and crash session a. The watcher should observe
	// the unexpected exit, remove a from the pool, and leave b
	// running (state stays "running" because another session
	// remains).
	rt.mu.Lock()
	handleA := rt.sessions["a"].handle.(*fakeHandle)
	rt.mu.Unlock()
	handleA.simulateCrash(134, "")

	deadline := time.After(time.Second)
	for {
		sessions := rt.SessionsSnapshot()
		if len(sessions) == 1 && sessions[0].ModelID == "b" {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("never observed pool cleanup; sessions = %+v", rt.SessionsSnapshot())
		case <-time.After(5 * time.Millisecond):
		}
	}
	// LastError must reflect the crash so dashboards / the UI can
	// surface it even though the runtime overall is still running.
	if rt.Status().LastError == "" {
		t.Fatal("LastError should be set after crash")
	}
}

func TestRuntime_ConcurrentEnsureLoaded(t *testing.T) {
	t.Parallel()
	// Stress-check that N goroutines hitting EnsureLoaded
	// concurrently produce N children at most equal to MaxResident.
	// The mutex must serialize transitions correctly.
	store := &fakeStore{models: map[string]InstalledModel{
		"a": {ID: "a", FilePath: "models/a.gguf"},
		"b": {ID: "b", FilePath: "models/b.gguf"},
		"c": {ID: "c", FilePath: "models/c.gguf"},
	}}
	starter := &fakeStarter{}
	rt := makeLRURuntime(t, store, starter, 2)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		modelID := []string{"a", "b", "c"}[i%3]
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, _ = rt.EnsureLoaded(context.Background(), id)
		}(modelID)
	}
	wg.Wait()
	sessions := rt.SessionsSnapshot()
	if len(sessions) > 2 {
		t.Fatalf("resident sessions = %d; capacity is 2", len(sessions))
	}
}

func TestRuntime_MaxResidentDefaultsToOne(t *testing.T) {
	t.Parallel()
	store := &fakeStore{models: map[string]InstalledModel{
		"a": {ID: "a", FilePath: "models/a.gguf"},
		"b": {ID: "b", FilePath: "models/b.gguf"},
	}}
	starter := &fakeStarter{}
	// MaxResident not set → falls through to default 1.
	rt := makeLRURuntime(t, store, starter, 0)
	if rt.MaxResident() != 1 {
		t.Fatalf("MaxResident default = %d; want 1", rt.MaxResident())
	}
	if _, err := rt.EnsureLoaded(context.Background(), "a"); err != nil {
		t.Fatalf("EnsureLoaded a: %v", err)
	}
	if _, err := rt.EnsureLoaded(context.Background(), "b"); err != nil {
		t.Fatalf("EnsureLoaded b: %v", err)
	}
	if len(rt.SessionsSnapshot()) != 1 {
		t.Fatalf("resident sessions = %d; default cap is 1", len(rt.SessionsSnapshot()))
	}
}
