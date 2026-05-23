package client

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/mcp"
)

// TestCache_RealSubprocess_ReusedAcrossPools is the e2e counterpart
// to the cache_test unit suite: it proves the cache reuses an actual
// subprocess (not just an httptest fixture) across two NewPoolWithCache
// invocations. Where the unit tests verify the cache's internal
// bookkeeping and the http_test integration tests prove pool-cache
// interaction over real HTTP, this test closes the loop on the path
// production runs through — exec.Cmd + StdioTransport + the cache's
// release-don't-close semantics under a real OS process lifecycle.
//
// Skipped when testing.Short() is set since spawning a child of the
// test binary takes a few hundred milliseconds.
//
// What it pins:
//   - Two pools built sequentially against the same fixture config
//     share one *Client (pointer equality).
//   - cache.Stats() shows exactly one entry after both pools release.
//   - tool calls keep working through the second pool — the first
//     pool's Close did not tear down the underlying subprocess.
//   - Cache.Close cleanly stops the subprocess.
func TestCache_RealSubprocess_ReusedAcrossPools(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test")
	}

	cfg := ServerConfig{
		Name:    "fixture",
		Command: os.Args[0],
		Args:    []string{"-test.run=^$"},
		Env:     map[string]string{fixtureEnvKey: "1"},
	}

	cache := NewSharedClientCache(time.Minute, mcp.ClientInfo{
		Name:    "hecate-cache-subprocess-test",
		Version: "0.0.0",
	})
	t.Cleanup(func() {
		// Close inside Cleanup so a t.Fatal mid-test still tears down
		// the subprocess. Subsequent Close calls are no-ops.
		closeDone := make(chan error, 1)
		go func() { closeDone <- cache.Close() }()
		select {
		case <-closeDone:
		case <-time.After(5 * time.Second):
			t.Error("cache.Close blocked > 5s — subprocess didn't exit on stdin close")
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Pool 1 — first acquire spawns the subprocess.
	pool1, err := NewPoolWithCache(ctx, []ServerConfig{cfg}, cache)
	if err != nil {
		t.Fatalf("NewPoolWithCache 1: %v", err)
	}

	// Capture the underlying Client pointer through the pool's
	// internal map. We're in the same package so reaching into
	// p.clients is fine — this is exactly how pool_test reaches in
	// to set up its harness.
	pool1.mu.Lock()
	client1 := pool1.clients["fixture"].client
	pool1.mu.Unlock()
	if client1 == nil {
		t.Fatal("pool1 has no client for fixture")
	}

	// Exercise the subprocess so we know it's alive before closing
	// pool1 — otherwise a "still works" assertion against pool2 is
	// less informative.
	text, isErr, err := pool1.Call(ctx, "mcp__fixture__echo", json.RawMessage(`{"msg":"first"}`))
	if err != nil || isErr {
		t.Fatalf("pool1 Call: err=%v isErr=%v text=%q", err, isErr, text)
	}
	if !strings.Contains(text, "first") {
		t.Errorf("pool1 echo text = %q, want it to contain 'first'", text)
	}

	if err := pool1.Close(); err != nil {
		t.Fatalf("pool1 Close: %v", err)
	}

	// After pool1 closes, the cache should still hold the entry with
	// refcount=0 (idle, but not yet evicted because TTL hasn't fired).
	if got := cache.Stats(); got.Entries != 1 || got.InUse != 0 {
		t.Fatalf("after pool1 Close: Stats = %+v, want Entries=1 InUse=0", got)
	}

	// Pool 2 — must reuse the same Client without respawning the
	// subprocess. Pointer equality is the simplest proof.
	pool2, err := NewPoolWithCache(ctx, []ServerConfig{cfg}, cache)
	if err != nil {
		t.Fatalf("NewPoolWithCache 2: %v", err)
	}
	t.Cleanup(func() { _ = pool2.Close() })

	pool2.mu.Lock()
	client2 := pool2.clients["fixture"].client
	pool2.mu.Unlock()
	if client2 != client1 {
		t.Errorf("pool2 client = %p, pool1 client = %p; want pointer equality (cache should reuse)", client2, client1)
	}

	if got := cache.Stats(); got.Entries != 1 {
		t.Errorf("after pool2 Acquire: Stats.Entries = %d, want 1 (no new entry)", got.Entries)
	}

	// Pool 2 must be able to use the cached subprocess. If pool1.Close
	// had wrongly torn the subprocess down, this call would fail with
	// a transport-closed error.
	text, isErr, err = pool2.Call(ctx, "mcp__fixture__echo", json.RawMessage(`{"msg":"second"}`))
	if err != nil || isErr {
		t.Fatalf("pool2 Call after pool1 Close: err=%v isErr=%v text=%q", err, isErr, text)
	}
	if !strings.Contains(text, "second") {
		t.Errorf("pool2 echo text = %q, want it to contain 'second'", text)
	}
}
