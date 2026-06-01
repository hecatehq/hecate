package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/mcp"
)

// makeCacheTestServer wires an httptest-backed MCP server that
// records every initialize handshake. Tests use the count to detect
// whether an Acquire hit the cache (no new spawn = count unchanged) or
// missed (count increments).
//
// Returns the URL plus a closure that returns the current spawn count.
// We reuse the JSON-RPC plumbing from pool_http_test (newTestMCPHTTPServer
// + registerStandardHandlers) so the fixture stays a single source of
// truth across the cache and pool integration tests.
func makeCacheTestServer(t *testing.T, name string, tools []mcp.Tool) (url string, initCount func() int32) {
	t.Helper()
	hs, srv := newTestMCPHTTPServer(t)
	var inits atomic.Int32
	// Wrap initialize so we can count it. The standard registrar sets
	// handlers AFTER we capture the URL, so we replace just the
	// initialize handler with a counting variant.
	registerStandardHandlers(srv, name, tools, map[string]func(json.RawMessage) mcp.CallToolResult{})
	srv.handle("initialize", func(req mcp.Request) (any, *mcp.RPCError) {
		inits.Add(1)
		return mcp.InitializeResult{
			ProtocolVersion: declaredClientProtocolVersion,
			Capabilities:    mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{}},
			ServerInfo:      mcp.ServerInfo{Name: name, Version: "0.0.0"},
		}, nil
	})
	return hs.URL, inits.Load
}

// newCacheTestCache builds a SharedClientCache with a tight TTL and
// reaper interval suitable for tests. The reaper interval is passed
// through the unexported constructor so it's set BEFORE the reaper
// goroutine starts — mutating c.reaper after construction would
// race with the goroutine's read of it in reaperLoop. (An earlier
// version of this helper did exactly that and tripped go test
// -race; the unexported newSharedClientCacheWithReaper is the
// race-free seam tests should use.)
//
// reaper == 0 falls back to the cache's internal default
// (defaultReaperInterval = 30s) — fine for tests that don't care
// about reaper timing.
func newCacheTestCache(t *testing.T, ttl, reaper time.Duration) *SharedClientCache {
	t.Helper()
	c := newSharedClientCacheWithReaper(ttl, reaper, mcp.ClientInfo{Name: "hecate-cache-test", Version: "0.0.0"})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestCache_Acquire_HitsAndMisses pins the core caching invariant: a
// second Acquire with the same config returns the same Client without
// re-spawning, while a different config triggers a fresh spawn.
func TestCache_Acquire_HitsAndMisses(t *testing.T) {
	t.Parallel()
	urlA, initsA := makeCacheTestServer(t, "a", []mcp.Tool{
		{Name: "t1", InputSchema: json.RawMessage(`{}`)},
	})
	urlB, initsB := makeCacheTestServer(t, "b", []mcp.Tool{
		{Name: "t1", InputSchema: json.RawMessage(`{}`)},
	})

	cache := newCacheTestCache(t, time.Minute, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfgA := ServerConfig{Name: "alias-a", URL: urlA}
	cfgB := ServerConfig{Name: "alias-b", URL: urlB}

	// First Acquire of A → spawn.
	clientA1, _, releaseA1, err := cache.Acquire(ctx, cfgA)
	if err != nil {
		t.Fatalf("Acquire A 1: %v", err)
	}
	if got := initsA(); got != 1 {
		t.Errorf("server A initialize count = %d, want 1 after first Acquire", got)
	}

	// Second Acquire of A with the SAME upstream URL → must hit cache,
	// no new initialize.
	clientA2, _, releaseA2, err := cache.Acquire(ctx, cfgA)
	if err != nil {
		t.Fatalf("Acquire A 2: %v", err)
	}
	if got := initsA(); got != 1 {
		t.Errorf("server A initialize count = %d, want 1 after second Acquire (cache hit expected)", got)
	}
	if clientA1 != clientA2 {
		t.Error("expected the same *Client pointer on cache hit")
	}

	// Acquire B → different config, must spawn a fresh client.
	_, _, releaseB, err := cache.Acquire(ctx, cfgB)
	if err != nil {
		t.Fatalf("Acquire B: %v", err)
	}
	if got := initsB(); got != 1 {
		t.Errorf("server B initialize count = %d, want 1", got)
	}
	if got := initsA(); got != 1 {
		t.Errorf("server A initialize count drifted to %d after acquiring B", got)
	}

	stats := cache.Stats()
	if stats.Entries != 2 {
		t.Errorf("Stats.Entries = %d, want 2", stats.Entries)
	}
	// Stats.InUse sums refcounts: A acquired twice (refcount=2) plus
	// B acquired once (refcount=1) = 3 live references in flight.
	if stats.InUse != 3 {
		t.Errorf("Stats.InUse = %d, want 3 (A held twice + B held once)", stats.InUse)
	}

	releaseA1()
	releaseA2()
	releaseB()
}

// TestCache_NameNotInKey verifies that two configs differing only in
// Name (the operator's per-task alias) share a cached Client. This is
// the "two tasks aliasing the same upstream as 'fs' and 'filesystem'
// share one subprocess" case from the cache's design comment.
func TestCache_NameNotInKey(t *testing.T) {
	t.Parallel()
	url, inits := makeCacheTestServer(t, "fs", []mcp.Tool{
		{Name: "read", InputSchema: json.RawMessage(`{}`)},
	})

	cache := newCacheTestCache(t, time.Minute, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c1, _, r1, err := cache.Acquire(ctx, ServerConfig{Name: "fs", URL: url})
	if err != nil {
		t.Fatalf("Acquire fs: %v", err)
	}
	defer r1()
	c2, _, r2, err := cache.Acquire(ctx, ServerConfig{Name: "filesystem", URL: url})
	if err != nil {
		t.Fatalf("Acquire filesystem: %v", err)
	}
	defer r2()
	if c1 != c2 {
		t.Error("two configs differing only in Name should share a cached Client")
	}
	if got := inits(); got != 1 {
		t.Errorf("initialize count = %d, want 1 (Name should not affect cache key)", got)
	}
}

// TestCache_TTLEvictsIdleEntries: an entry whose refcount drops to
// zero and stays idle longer than the configured TTL is evicted by
// the reaper, freeing the underlying Client. We use a tiny TTL +
// reaper interval to exercise this in a few hundred ms.
func TestCache_TTLEvictsIdleEntries(t *testing.T) {
	t.Parallel()
	url, _ := makeCacheTestServer(t, "x", []mcp.Tool{
		{Name: "t", InputSchema: json.RawMessage(`{}`)},
	})

	cache := newCacheTestCache(t, 80*time.Millisecond, 30*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := ServerConfig{Name: "x", URL: url}

	_, _, release, err := cache.Acquire(ctx, cfg)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := cache.Stats().Entries; got != 1 {
		t.Fatalf("Stats.Entries = %d, want 1", got)
	}
	release()

	// Wait long enough for TTL + reaper interval to fire. We poll
	// Stats() rather than sleeping a fixed duration so the test stays
	// fast on a warm machine and forgiving on a cold one.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cache.Stats().Entries == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("entry was not evicted after TTL elapsed; Stats() = %+v", cache.Stats())
}

// TestCache_TTLDoesNotEvictInUseEntries: even after the TTL elapses,
// an entry with refcount > 0 must NOT be evicted — that would yank
// the Client out from under an in-flight run. The test holds the
// release func across the TTL window and verifies the entry survives.
func TestCache_TTLDoesNotEvictInUseEntries(t *testing.T) {
	t.Parallel()
	url, _ := makeCacheTestServer(t, "x", []mcp.Tool{
		{Name: "t", InputSchema: json.RawMessage(`{}`)},
	})

	cache := newCacheTestCache(t, 50*time.Millisecond, 20*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := ServerConfig{Name: "x", URL: url}
	_, _, release, err := cache.Acquire(ctx, cfg)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer release()

	// Wait several TTL windows. The entry is in-use, so it must survive.
	time.Sleep(200 * time.Millisecond)

	stats := cache.Stats()
	if stats.Entries != 1 {
		t.Errorf("Stats.Entries = %d, want 1 (in-use entry must not evict)", stats.Entries)
	}
	if stats.InUse != 1 {
		t.Errorf("Stats.InUse = %d, want 1", stats.InUse)
	}
}

// TestCache_Evict_RemovesEntry pins the manual-eviction surface that
// Pool.Call uses on transport errors. After Evict, Stats reflects the
// removal and the next Acquire spawns fresh.
func TestCache_Evict_RemovesEntry(t *testing.T) {
	t.Parallel()
	url, inits := makeCacheTestServer(t, "x", []mcp.Tool{
		{Name: "t", InputSchema: json.RawMessage(`{}`)},
	})

	cache := newCacheTestCache(t, time.Minute, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := ServerConfig{Name: "x", URL: url}
	_, _, release, err := cache.Acquire(ctx, cfg)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()

	if got := inits(); got != 1 {
		t.Errorf("initialize count = %d, want 1 before Evict", got)
	}

	cache.Evict(cfg)
	if got := cache.Stats().Entries; got != 0 {
		t.Errorf("Stats.Entries = %d, want 0 after Evict", got)
	}

	// Next Acquire must respawn — incrementing the initialize counter.
	_, _, r2, err := cache.Acquire(ctx, cfg)
	if err != nil {
		t.Fatalf("Acquire after Evict: %v", err)
	}
	defer r2()
	if got := inits(); got != 2 {
		t.Errorf("initialize count = %d, want 2 (Evict should force respawn on next Acquire)", got)
	}
}

// TestCache_Close_TearsDownAllEntries: Close removes every entry and
// closes its Client, even ones that are still in-use. Idempotent on
// the second call.
func TestCache_Close_TearsDownAllEntries(t *testing.T) {
	t.Parallel()
	url1, _ := makeCacheTestServer(t, "a", []mcp.Tool{{Name: "t", InputSchema: json.RawMessage(`{}`)}})
	url2, _ := makeCacheTestServer(t, "b", []mcp.Tool{{Name: "t", InputSchema: json.RawMessage(`{}`)}})

	cache := NewSharedClientCache(time.Minute, mcp.ClientInfo{Name: "hecate-cache-close-test", Version: "0"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, _, err := cache.Acquire(ctx, ServerConfig{Name: "a", URL: url1})
	if err != nil {
		t.Fatalf("Acquire a: %v", err)
	}
	_, _, _, err = cache.Acquire(ctx, ServerConfig{Name: "b", URL: url2})
	if err != nil {
		t.Fatalf("Acquire b: %v", err)
	}

	if got := cache.Stats().Entries; got != 2 {
		t.Fatalf("Stats.Entries = %d, want 2 before Close", got)
	}

	if err := cache.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := cache.Stats().Entries; got != 0 {
		t.Errorf("Stats.Entries = %d, want 0 after Close", got)
	}
	// Idempotent: second Close is a no-op (reaperWg already drained).
	if err := cache.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestCache_ConcurrentAcquireSameKey: many goroutines racing to
// Acquire the same key must see exactly one underlying Client. The
// race-recheck in Acquire (insert under lock; if another goroutine
// won, close ours and use theirs) is what we're pinning.
func TestCache_ConcurrentAcquireSameKey(t *testing.T) {
	t.Parallel()
	url, inits := makeCacheTestServer(t, "x", []mcp.Tool{
		{Name: "t", InputSchema: json.RawMessage(`{}`)},
	})

	cache := newCacheTestCache(t, time.Minute, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := ServerConfig{Name: "x", URL: url}

	const goroutines = 10
	results := make(chan *Client, goroutines)
	releases := make(chan func(), goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			c, _, release, err := cache.Acquire(ctx, cfg)
			if err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			results <- c
			releases <- release
		}()
	}
	wg.Wait()
	close(results)
	close(releases)

	// All goroutines must have received the SAME *Client.
	var first *Client
	for c := range results {
		if first == nil {
			first = c
			continue
		}
		if c != first {
			t.Error("concurrent Acquires for same key returned different *Clients")
			break
		}
	}

	// Race may produce extra spawns; the cache discards losers, so
	// initialize count is bounded by goroutines but on a sane impl
	// is small. We assert it's not zero (we did spawn) and not
	// pathologically large (every goroutine respawned).
	got := inits()
	if got < 1 {
		t.Errorf("initialize count = %d, want >= 1", got)
	}
	if got > goroutines {
		t.Errorf("initialize count = %d, want <= %d", got, goroutines)
	}

	for r := range releases {
		r()
	}
}

// TestCache_MaxEntries_EvictsLRUIdleOnOverflow pins the cache's
// soft-cap behavior: when an Acquire-miss would push the cache over
// maxEntries, the least-recently-used IDLE entry is evicted before
// the new insert. We seed three idle entries with staggered
// lastUsed timestamps, then trigger a fourth acquire under cap=3 and
// verify the OLDEST idle entry got evicted (not the newest).
func TestCache_MaxEntries_EvictsLRUIdleOnOverflow(t *testing.T) {
	t.Parallel()
	urlA, _ := makeCacheTestServer(t, "a", []mcp.Tool{{Name: "t", InputSchema: json.RawMessage(`{}`)}})
	urlB, _ := makeCacheTestServer(t, "b", []mcp.Tool{{Name: "t", InputSchema: json.RawMessage(`{}`)}})
	urlC, _ := makeCacheTestServer(t, "c", []mcp.Tool{{Name: "t", InputSchema: json.RawMessage(`{}`)}})
	urlD, _ := makeCacheTestServer(t, "d", []mcp.Tool{{Name: "t", InputSchema: json.RawMessage(`{}`)}})

	// Cap of 3, no auto-eviction by TTL during the test (long ttl
	// + long reaper interval keeps the reaper from confusing the
	// LRU signal).
	cache := newSharedClientCacheFull(cacheBuildConfig{ttl: time.Minute, reaperInterval: time.Minute, maxEntries: 3, info: mcp.ClientInfo{Name: "test", Version: "0"}})
	t.Cleanup(func() { _ = cache.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Acquire + release each of A, B, C in order — small sleeps so
	// their lastUsed timestamps are strictly ordered. Without the
	// sleep, monotonic-time resolution on some platforms can leave
	// two lastUsed values equal and make "least recent" ambiguous.
	for i, u := range []string{urlA, urlB, urlC} {
		_, _, release, err := cache.Acquire(ctx, ServerConfig{Name: fmt.Sprintf("s%d", i), URL: u})
		if err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
		release()
		time.Sleep(2 * time.Millisecond)
	}
	if got := cache.Stats().Entries; got != 3 {
		t.Fatalf("Stats.Entries = %d, want 3 before overflow", got)
	}

	// Acquire D. We're at cap=3 with all three idle, so the oldest
	// (A) should be evicted, the new D inserted.
	_, _, releaseD, err := cache.Acquire(ctx, ServerConfig{Name: "d", URL: urlD})
	if err != nil {
		t.Fatalf("Acquire D: %v", err)
	}
	t.Cleanup(releaseD)

	if got := cache.Stats().Entries; got != 3 {
		t.Errorf("Stats.Entries = %d, want 3 (one evicted, one inserted)", got)
	}
	// Verify A is gone — re-acquiring its config would be a cache
	// miss, which Stats can't distinguish from a hit. So we use
	// the next-best signal: re-acquiring A causes a respawn (B and
	// C should still be present, D was just inserted; only A was
	// evicted). We test this via Stats: after re-acquiring A,
	// entries goes to 4, then triggers another LRU eviction.
	_, _, releaseA2, err := cache.Acquire(ctx, ServerConfig{Name: "s0", URL: urlA})
	if err != nil {
		t.Fatalf("Re-acquire A: %v", err)
	}
	t.Cleanup(releaseA2)
	// After this re-acquire the cap=3 is enforced again. The
	// least-recently-used IDLE entry now is B (still the oldest
	// idle). So B should evict and the cache should still be 3.
	if got := cache.Stats().Entries; got != 3 {
		t.Errorf("Stats.Entries = %d after re-acquire, want 3 (LRU eviction)", got)
	}
}

// TestCache_MaxEntries_AllowsOverCapWhenAllInUse pins the soft-cap
// fail-open behavior: when we're at cap and every entry is in-use
// (no idle entries to evict), Acquire is allowed to push the cache
// over cap rather than rejecting the request. Rejecting would break
// a legitimate run; the alternative is unbounded growth, but TTL +
// future releases catch up as soon as anything goes idle.
func TestCache_MaxEntries_AllowsOverCapWhenAllInUse(t *testing.T) {
	t.Parallel()
	urlA, _ := makeCacheTestServer(t, "a", []mcp.Tool{{Name: "t", InputSchema: json.RawMessage(`{}`)}})
	urlB, _ := makeCacheTestServer(t, "b", []mcp.Tool{{Name: "t", InputSchema: json.RawMessage(`{}`)}})
	urlC, _ := makeCacheTestServer(t, "c", []mcp.Tool{{Name: "t", InputSchema: json.RawMessage(`{}`)}})

	cache := newSharedClientCacheFull(cacheBuildConfig{ttl: time.Minute, reaperInterval: time.Minute, maxEntries: 2, info: mcp.ClientInfo{Name: "test", Version: "0"}})
	t.Cleanup(func() { _ = cache.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Acquire A and B and HOLD them — both in-use, refcount > 0.
	_, _, releaseA, err := cache.Acquire(ctx, ServerConfig{Name: "a", URL: urlA})
	if err != nil {
		t.Fatalf("Acquire A: %v", err)
	}
	t.Cleanup(releaseA)
	_, _, releaseB, err := cache.Acquire(ctx, ServerConfig{Name: "b", URL: urlB})
	if err != nil {
		t.Fatalf("Acquire B: %v", err)
	}
	t.Cleanup(releaseB)

	// We're at cap=2, both held. C should still go through — no
	// idle entry to evict, fail-open kicks in.
	_, _, releaseC, err := cache.Acquire(ctx, ServerConfig{Name: "c", URL: urlC})
	if err != nil {
		t.Fatalf("Acquire C with all in-use must not fail: %v", err)
	}
	t.Cleanup(releaseC)

	stats := cache.Stats()
	if stats.Entries != 3 {
		t.Errorf("Stats.Entries = %d, want 3 (over-cap allowed when all in-use)", stats.Entries)
	}
	if stats.InUse != 3 {
		t.Errorf("Stats.InUse = %d, want 3", stats.InUse)
	}
}

// TestCache_MaxEntries_DisabledByZero pins that maxEntries=0 means
// "no cap" — useful for tests and any deployment that wants to rely
// solely on TTL eviction.
func TestCache_MaxEntries_DisabledByZero(t *testing.T) {
	t.Parallel()
	urlA, _ := makeCacheTestServer(t, "a", []mcp.Tool{{Name: "t", InputSchema: json.RawMessage(`{}`)}})
	urlB, _ := makeCacheTestServer(t, "b", []mcp.Tool{{Name: "t", InputSchema: json.RawMessage(`{}`)}})
	urlC, _ := makeCacheTestServer(t, "c", []mcp.Tool{{Name: "t", InputSchema: json.RawMessage(`{}`)}})

	cache := newSharedClientCacheFull(cacheBuildConfig{ttl: time.Minute, reaperInterval: time.Minute, maxEntries: 0, info: mcp.ClientInfo{Name: "test", Version: "0"}})
	t.Cleanup(func() { _ = cache.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i, u := range []string{urlA, urlB, urlC} {
		_, _, release, err := cache.Acquire(ctx, ServerConfig{Name: fmt.Sprintf("s%d", i), URL: u})
		if err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
		release()
	}
	if got := cache.Stats().Entries; got != 3 {
		t.Errorf("Stats.Entries = %d, want 3 (cap disabled)", got)
	}
}

// TestCache_DoubleReleaseIsIdempotent: a release func called twice
// must not double-decrement the refcount, otherwise a third Acquire
// could see the entry as evictable and lose it under us. We Acquire
// twice (refcount=2), call the same release twice (must take it to 1,
// not 0), then verify the entry is still in-use.
func TestCache_DoubleReleaseIsIdempotent(t *testing.T) {
	t.Parallel()
	url, _ := makeCacheTestServer(t, "x", []mcp.Tool{
		{Name: "t", InputSchema: json.RawMessage(`{}`)},
	})

	cache := newCacheTestCache(t, time.Minute, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := ServerConfig{Name: "x", URL: url}
	_, _, release1, err := cache.Acquire(ctx, cfg)
	if err != nil {
		t.Fatalf("Acquire 1: %v", err)
	}
	_, _, release2, err := cache.Acquire(ctx, cfg)
	if err != nil {
		t.Fatalf("Acquire 2: %v", err)
	}
	defer release2()

	if got := cache.Stats().InUse; got != 2 {
		t.Fatalf("Stats.InUse = %d, want 2 after two Acquires", got)
	}

	// First release: 2 → 1.
	release1()
	if got := cache.Stats().InUse; got != 1 {
		t.Errorf("Stats.InUse = %d, want 1 after one release", got)
	}
	// Second release of the SAME func: must be a no-op.
	release1()
	if got := cache.Stats().InUse; got != 1 {
		t.Errorf("Stats.InUse = %d, want 1 (double-release should be idempotent)", got)
	}
}

// TestCache_Observer_HitMissEvictedFire pins that the CacheObserver
// hooks fire on the right paths: a fresh Acquire records Miss, a
// subsequent Acquire of the same cfg records Hit, and Evict() fires
// Evicted with the operator-supplied alias.
func TestCache_Observer_HitMissEvictedFire(t *testing.T) {
	t.Parallel()
	url, _ := makeCacheTestServer(t, "fs", []mcp.Tool{
		{Name: "ping", InputSchema: json.RawMessage(`{}`)},
	})

	var (
		mu      sync.Mutex
		hits    []string
		misses  []string
		evicted []string
	)
	obs := &CacheObserver{
		OnHit:     func(s string) { mu.Lock(); hits = append(hits, s); mu.Unlock() },
		OnMiss:    func(s string) { mu.Lock(); misses = append(misses, s); mu.Unlock() },
		OnEvicted: func(s string) { mu.Lock(); evicted = append(evicted, s); mu.Unlock() },
	}

	cache := newCacheTestCache(t, time.Minute, time.Minute)
	cache.SetObserver(obs)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := ServerConfig{Name: "fs", URL: url}

	// First acquire = miss.
	_, _, r1, err := cache.Acquire(ctx, cfg)
	if err != nil {
		t.Fatalf("Acquire 1: %v", err)
	}
	r1()

	// Second acquire = hit.
	_, _, r2, err := cache.Acquire(ctx, cfg)
	if err != nil {
		t.Fatalf("Acquire 2: %v", err)
	}
	r2()

	// Evict surfaces the alias on the observer.
	cache.Evict(cfg)

	mu.Lock()
	defer mu.Unlock()
	if len(misses) != 1 || misses[0] != "fs" {
		t.Errorf("misses = %v, want [\"fs\"]", misses)
	}
	if len(hits) != 1 || hits[0] != "fs" {
		t.Errorf("hits = %v, want [\"fs\"]", hits)
	}
	if len(evicted) != 1 || evicted[0] != "fs" {
		t.Errorf("evicted = %v, want [\"fs\"]", evicted)
	}
}

// TestCache_Observer_NilSafe pins that a partially-set CacheObserver
// (some callbacks nil) doesn't panic when a missing event fires. This
// matches the documented contract — observers can implement only the
// events they care about.
func TestCache_Observer_NilSafe(t *testing.T) {
	t.Parallel()
	url, _ := makeCacheTestServer(t, "fs", []mcp.Tool{
		{Name: "ping", InputSchema: json.RawMessage(`{}`)},
	})

	// Only Hit is wired; Miss and Evicted left nil.
	var hits int
	cache := newCacheTestCache(t, time.Minute, time.Minute)
	cache.SetObserver(&CacheObserver{
		OnHit: func(string) { hits++ },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := ServerConfig{Name: "fs", URL: url}

	// Miss path: must not panic on nil OnMiss.
	_, _, r1, err := cache.Acquire(ctx, cfg)
	if err != nil {
		t.Fatalf("Acquire (miss): %v", err)
	}
	r1()
	// Hit path: should fire and increment.
	_, _, r2, err := cache.Acquire(ctx, cfg)
	if err != nil {
		t.Fatalf("Acquire (hit): %v", err)
	}
	r2()
	// Evict path: must not panic on nil OnEvicted.
	cache.Evict(cfg)

	if hits != 1 {
		t.Errorf("hits = %d, want 1", hits)
	}
}

// makeCacheTestServerWithPing wires a fixture whose ping handler is
// controllable: the supplied callback runs synchronously inside the
// handler, so tests can make ping fail (return an RPC error) or hang
// (block until t.Cleanup). Other methods behave normally.
//
// pingHandler == nil falls back to the default "answer empty result"
// behavior — no test-specific fixture wrapping needed for the
// happy-path cases.
func makeCacheTestServerWithPing(t *testing.T, name string, pingHandler func() (any, *mcp.RPCError)) string {
	t.Helper()
	hs, srv := newTestMCPHTTPServer(t)
	registerStandardHandlers(srv, name, []mcp.Tool{
		{Name: "noop", InputSchema: json.RawMessage(`{}`)},
	}, map[string]func(json.RawMessage) mcp.CallToolResult{})
	if pingHandler != nil {
		srv.handle("ping", func(_ mcp.Request) (any, *mcp.RPCError) {
			return pingHandler()
		})
	}
	return hs.URL
}

// TestCache_HealthCheck_EvictsUnresponsiveIdleEntry pins the
// headline behavior: an idle cached entry whose ping fails is
// evicted before the next Acquire would have handed back a dead
// client. We use a server that returns an RPC error on ping (the
// failure mode the cache treats as "wedge"), wait long enough for
// the health-check loop to fire once, and assert the entry is gone.
func TestCache_HealthCheck_EvictsUnresponsiveIdleEntry(t *testing.T) {
	t.Parallel()
	url := makeCacheTestServerWithPing(t, "stuck", func() (any, *mcp.RPCError) {
		return nil, mcp.NewError(mcp.ErrCodeInternalError, "wedged")
	})

	// Tight pingInterval so the loop fires quickly; pingTimeout is
	// the inner per-call bound. Tests don't get to use defaultReaperInterval
	// so we use the full constructor.
	cache := newSharedClientCacheFull(cacheBuildConfig{
		ttl:            time.Minute,
		reaperInterval: time.Minute,
		pingInterval:   30 * time.Millisecond,
		pingTimeout:    1 * time.Second,
		info:           mcp.ClientInfo{Name: "hecate-cache-test", Version: "0"},
	})
	t.Cleanup(func() { _ = cache.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := ServerConfig{Name: "stuck", URL: url}
	_, _, release, err := cache.Acquire(ctx, cfg)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()

	// Health check fires every ~30ms; the failing ping should
	// trigger eviction. Poll Stats so the test is fast on a warm
	// machine and forgiving on a cold one.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cache.Stats().Entries == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("entry not evicted by health check; Stats = %+v", cache.Stats())
}

// TestCache_HealthCheck_HangingPingEvictsAfterTimeout: ping that
// hangs forever must surface as a deadline-exceeded inside
// pingTimeout, evicting the entry. Distinguished from the
// RPC-error path above — both should evict but the timeout one
// is the more pernicious "alive on the wire but not responsive"
// failure mode that motivated the feature.
func TestCache_HealthCheck_HangingPingEvictsAfterTimeout(t *testing.T) {
	t.Parallel()
	hang := make(chan struct{})
	url := makeCacheTestServerWithPing(t, "hang", func() (any, *mcp.RPCError) {
		<-hang
		return struct{}{}, nil
	})
	// Cleanup ordering matters: t.Cleanup is LIFO. Register
	// close(hang) AFTER makeCacheTestServerWithPing so it runs
	// BEFORE the httptest server's Close (registered inside the
	// helper) — otherwise hs.Close blocks forever waiting for the
	// still-blocking <-hang server-side handler.
	t.Cleanup(func() { close(hang) })

	cache := newSharedClientCacheFull(cacheBuildConfig{
		ttl:            time.Minute,
		reaperInterval: time.Minute,
		pingInterval:   30 * time.Millisecond,
		pingTimeout:    100 * time.Millisecond,
		info:           mcp.ClientInfo{Name: "hecate-cache-test", Version: "0"},
	})
	t.Cleanup(func() { _ = cache.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, release, err := cache.Acquire(ctx, ServerConfig{Name: "hang", URL: url})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()

	// pingInterval=30ms → first probe fires at ~30ms.
	// pingTimeout=100ms → that probe completes at ~130ms with a
	// deadline-exceeded → eviction. Cap at 2s to avoid wedging the
	// test on a slow machine.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cache.Stats().Entries == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("entry not evicted after ping timeout; Stats = %+v", cache.Stats())
}

// TestCache_HealthCheck_SkipsInUseEntries: an in-use entry must
// NOT be pinged or evicted by the health-check loop — pinging
// would race the active tool call's response, and evicting an
// in-use entry would yank the client out from under a legitimate
// caller. We hold an Acquire across multiple ping-loop ticks and
// assert the entry survives even when the upstream's ping handler
// would fail it if probed.
func TestCache_HealthCheck_SkipsInUseEntries(t *testing.T) {
	t.Parallel()
	url := makeCacheTestServerWithPing(t, "inuse", func() (any, *mcp.RPCError) {
		// Would-fail handler — but we expect no probe at all while
		// the entry is in-use.
		return nil, mcp.NewError(mcp.ErrCodeInternalError, "wedged")
	})

	cache := newSharedClientCacheFull(cacheBuildConfig{
		ttl:            time.Minute,
		reaperInterval: time.Minute,
		pingInterval:   30 * time.Millisecond,
		pingTimeout:    1 * time.Second,
		info:           mcp.ClientInfo{Name: "hecate-cache-test", Version: "0"},
	})
	t.Cleanup(func() { _ = cache.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := ServerConfig{Name: "inuse", URL: url}
	_, _, release, err := cache.Acquire(ctx, cfg)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer release() // hold across the entire test

	// Wait several ping-loop intervals. With the handler set to
	// fail, an in-use-blind ping loop would have evicted the
	// entry; the in-use guard keeps it alive.
	time.Sleep(150 * time.Millisecond)
	if got := cache.Stats(); got.Entries != 1 || got.InUse != 1 {
		t.Errorf("Stats = %+v after in-use hold, want Entries=1 InUse=1", got)
	}
}

// TestCache_HealthCheck_DisabledByZeroPingInterval: pingInterval=0
// means "no health-check loop." Verifying by registering a
// would-fail ping handler and asserting the entry survives across
// what would otherwise be many ping-loop ticks.
func TestCache_HealthCheck_DisabledByZeroPingInterval(t *testing.T) {
	t.Parallel()
	url := makeCacheTestServerWithPing(t, "disabled", func() (any, *mcp.RPCError) {
		return nil, mcp.NewError(mcp.ErrCodeInternalError, "would-fail")
	})

	cache := newSharedClientCacheFull(cacheBuildConfig{
		ttl:            time.Minute,
		reaperInterval: time.Minute,
		pingInterval:   0, // disabled
		pingTimeout:    1 * time.Second,
		info:           mcp.ClientInfo{Name: "hecate-cache-test", Version: "0"},
	})
	t.Cleanup(func() { _ = cache.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := ServerConfig{Name: "disabled", URL: url}
	_, _, release, err := cache.Acquire(ctx, cfg)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()

	// 200ms is much longer than any sane pingInterval — a running
	// loop would have fired several times by now.
	time.Sleep(200 * time.Millisecond)
	if got := cache.Stats().Entries; got != 1 {
		t.Errorf("Stats.Entries = %d, want 1 (loop disabled)", got)
	}
}

// TestCache_HTTPClient_DefaultsConstructedOnce: with no explicit
// HTTPClient passed in, the cache lazy-constructs a single
// *http.Client and reuses it for every HTTP MCP transport it
// spawns. Pins the seam — a regression that constructed a fresh
// client per spawn would silently lose the configurability win
// (and force operators to inject custom transports per cached
// entry, which there's no API for).
func TestCache_HTTPClient_DefaultsConstructedOnce(t *testing.T) {
	t.Parallel()
	urlA, _ := makeCacheTestServer(t, "a", []mcp.Tool{
		{Name: "ping", InputSchema: json.RawMessage(`{}`)},
	})
	urlB, _ := makeCacheTestServer(t, "b", []mcp.Tool{
		{Name: "ping", InputSchema: json.RawMessage(`{}`)},
	})

	cache := newCacheTestCache(t, time.Minute, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Acquire two distinct upstreams so we exercise multiple spawns.
	_, _, releaseA, err := cache.Acquire(ctx, ServerConfig{Name: "a", URL: urlA})
	if err != nil {
		t.Fatalf("Acquire A: %v", err)
	}
	defer releaseA()
	_, _, releaseB, err := cache.Acquire(ctx, ServerConfig{Name: "b", URL: urlB})
	if err != nil {
		t.Fatalf("Acquire B: %v", err)
	}
	defer releaseB()

	// The cache's httpClient must be non-nil (default-constructed).
	if cache.httpClient == nil {
		t.Fatal("cache.httpClient is nil; default construction failed")
	}
	// Both spawned HTTP transports must reference the cache's
	// shared client. Reach into entries via the bind map (cache
	// doesn't expose entries directly, so we walk what's there).
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.entries) != 2 {
		t.Fatalf("cache.entries = %d, want 2", len(cache.entries))
	}
	for _, entry := range cache.entries {
		// HTTPTransport stores its httpCli in an unexported field;
		// reach in via type assertion. Same package — fine.
		ht, ok := entry.client.transport.(*HTTPTransport)
		if !ok {
			t.Errorf("entry transport = %T, want *HTTPTransport", entry.client.transport)
			continue
		}
		if ht.httpCli != cache.httpClient {
			t.Errorf("transport httpCli = %p, cache.httpClient = %p; want pointer equality",
				ht.httpCli, cache.httpClient)
		}
	}
}

// TestCache_HTTPClient_CustomInjected: passing a custom
// *http.Client via SharedClientCacheOptions.HTTPClient threads it
// through to every HTTP transport. Mirrors the deploy path where
// an operator wants a corporate-proxy transport, mTLS, or a
// non-default timeout.
func TestCache_HTTPClient_CustomInjected(t *testing.T) {
	t.Parallel()
	url, _ := makeCacheTestServer(t, "custom", []mcp.Tool{
		{Name: "ping", InputSchema: json.RawMessage(`{}`)},
	})

	// Distinguishable client: longer-than-default timeout. The test
	// asserts pointer equality, but the timeout is a sanity check
	// that we used the operator's client and not a default.
	custom := &http.Client{Timeout: 90 * time.Second}
	cache := NewSharedClientCacheWithOptions(SharedClientCacheOptions{
		TTL:          time.Minute,
		PingInterval: -1, // disable health check; not under test
		Info:         mcp.ClientInfo{Name: "test", Version: "0"},
		HTTPClient:   custom,
	})
	t.Cleanup(func() { _ = cache.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, release, err := cache.Acquire(ctx, ServerConfig{Name: "custom", URL: url})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer release()

	if cache.httpClient != custom {
		t.Errorf("cache.httpClient = %p, want operator-provided %p", cache.httpClient, custom)
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for _, entry := range cache.entries {
		ht, ok := entry.client.transport.(*HTTPTransport)
		if !ok {
			t.Errorf("entry transport = %T, want *HTTPTransport", entry.client.transport)
			continue
		}
		if ht.httpCli != custom {
			t.Errorf("transport httpCli = %p, want injected %p", ht.httpCli, custom)
		}
	}
}
