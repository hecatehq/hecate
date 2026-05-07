package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/hecate/agent-runtime/internal/mcp"
)

// SharedClientCache amortizes MCP-client startup across runs. Today
// every agent-loop run spawns its MCP subprocesses fresh and tears
// them down at run end — paying ~hundreds of ms per stdio server on
// process exec + initialize handshake + tools/list. The cache holds
// one Client per upstream config and hands it back on subsequent
// runs, so an operator firing a batch of tasks against the same
// server pays the spawn cost once instead of N times.
//
// Lifecycle:
//
//   - Acquire(cfg) returns the cached Client (and its tools snapshot)
//     for cfg, spawning one on a miss. The caller gets back a release
//     func and must call it when done — the cache decrements the
//     refcount but does NOT immediately close the Client.
//   - Idle entries (refcount == 0, lastUsed older than ttl) are
//     evicted by a background reaper goroutine. This is the only
//     normal teardown path.
//   - Evict(cfg) removes a cached entry on demand. Used by callers
//     who detect a transport-level error (subprocess died) so the
//     next Acquire spawns fresh instead of returning the dead client.
//   - Close stops the reaper and tears down every cached Client.
//     Idempotent. Caller must ensure no in-flight runs are still
//     using cached clients before Close, since Close cuts those
//     connections.
//
// Concurrency: every public method is safe for concurrent use. The
// internal lock is held only for short bookkeeping; spawning happens
// outside the lock (with a race-recheck on insert) so a slow upstream
// init doesn't serialize unrelated Acquires.
//
// Cache key: a SHA-256 over the transport-identifying fields of
// ServerConfig — Command/Args/Env for stdio, URL/Headers for HTTP.
// The operator-chosen Name is intentionally excluded: it's the
// per-task alias used to namespace tools (mcp__<name>__<tool>), not
// part of upstream identity. Two tasks aliasing the same upstream as
// "fs" and "filesystem" share one subprocess.
// CacheObserver is the optional telemetry seam for SharedClientCache.
// All three callbacks are nil-safe (the cache wraps every invocation
// in a nil check), so observers can implement only the events they
// care about by leaving fields nil.
//
// Server is the operator-chosen alias from the per-task config (NOT
// part of the cache key — see configKey). It's blank on Evicted
// callbacks fired from paths where the alias isn't known
// (TTL/LRU eviction inside the reaper, where the cache only carries
// the upstream key, not the operator's alias).
//
// Implementations must be cheap and non-blocking — these fire from
// the cache's hot path under c.mu in some cases. A typical
// implementation just bumps an atomic counter or calls into an
// already-fast metrics SDK.
type CacheObserver struct {
	OnHit     func(server string)
	OnMiss    func(server string)
	OnEvicted func(server string)
}

type SharedClientCache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry

	ttl    time.Duration
	info   mcp.ClientInfo
	reaper time.Duration

	// maxEntries is the soft cap on cached upstream count. Acquire's
	// miss path evicts the least-recently-used IDLE entry before
	// inserting a new one when the cache is at-or-over this size. If
	// every entry is in-use (refcount > 0) the over-cap insert is
	// allowed — rejecting an Acquire would break a legitimate run,
	// and TTL eviction will catch up once anything goes idle. 0
	// disables the cap (used by tests that don't care).
	maxEntries int

	// observer carries optional callbacks for hit/miss/evict events.
	// nil = no observer wired (the orchestrator-level helper installs
	// one that records metrics; tests typically leave it nil).
	observer *CacheObserver

	// pingInterval / pingTimeout govern the proactive health-check
	// loop. The reactive eviction in Pool.Call only fires AFTER a
	// tool call has already failed; the ping loop catches subprocesses
	// that are alive but wedged (event-loop deadlock, tight CPU loop)
	// before the next real call hits the wall. pingInterval == 0
	// disables the loop entirely (useful for tests that don't want
	// a ticker at all). pingTimeout bounds each individual ping;
	// failure or deadline-exceeded evicts the entry.
	pingInterval time.Duration
	pingTimeout  time.Duration

	// httpClient is reused across every HTTP MCP transport this cache
	// spawns. Stdio servers ignore it. The seam exists for two
	// reasons: (1) deploys that need a custom transport (corporate
	// proxy, mTLS, alternate DialContext) can inject one via
	// SharedClientCacheOptions.HTTPClient, and (2) keeping a single
	// client object makes the timeout / connection-limit policy a
	// single point to configure rather than scattered per
	// transport. (Connection pooling itself is already shared via
	// http.DefaultTransport when each transport defaults its own
	// client, so this isn't about pool reuse — it's about
	// configurability.)
	httpClient *http.Client

	closeCh   chan struct{}
	closeOnce sync.Once
	reaperWg  sync.WaitGroup
}

type cacheEntry struct {
	client     *Client
	tools      []mcp.Tool
	inUse      int
	lastUsed   time.Time
	lastPinged time.Time
}

const (
	defaultCacheTTL       = 5 * time.Minute
	defaultReaperInterval = 30 * time.Second
	// defaultCacheMaxEntries is the SharedClientCache's default soft
	// cap. 256 is generous for any real deployment (most operators
	// use 1-3 distinct MCP servers across all their tasks) but tight
	// enough to bound a runaway tenant or a config-permutation churn
	// from accumulating an unbounded set of cached subprocesses.
	defaultCacheMaxEntries = 256
	// defaultPingInterval is how often the health-check loop pings
	// idle entries. 60s balances "fast enough to detect a wedge
	// before the operator's next task" against "not so much wire
	// chatter that it shows up in upstream logs as flooding."
	defaultPingInterval = 60 * time.Second
	// defaultPingTimeout bounds each individual ping. 5s is plenty
	// for a healthy MCP server (ping is a one-shot empty-result
	// round-trip) and tight enough that a wedged subprocess
	// surfaces quickly.
	defaultPingTimeout = 5 * time.Second
)

// NewSharedClientCache builds a cache with the given idle TTL and
// the cache's default knobs for everything else (max-entries cap of
// 256, reaper at 30s, health-check ping every 60s with a 5s timeout
// per ping). Every Client the cache spawns reports info as its MCP
// ClientInfo on the initialize handshake, so upstream server logs
// identify a single stable client identity (e.g. "hecate-agent-loop
// / <version>") regardless of which run triggered the spawn.
//
// ttl <= 0 falls back to defaultCacheTTL (5 minutes).
//
// For deployments that need to override the cap or health-check
// cadence, see NewSharedClientCacheWithLimits.
func NewSharedClientCache(ttl time.Duration, info mcp.ClientInfo) *SharedClientCache {
	return newSharedClientCacheFull(cacheBuildConfig{
		ttl:            ttl,
		reaperInterval: defaultReaperInterval,
		maxEntries:     defaultCacheMaxEntries,
		pingInterval:   defaultPingInterval,
		pingTimeout:    defaultPingTimeout,
		info:           info,
	})
}

// NewSharedClientCacheWithLimits is the explicit-cap counterpart for
// callers that want to override the max-entries cap (e.g. a deployment
// expecting many distinct MCP servers per tenant). maxEntries <= 0
// disables the cap entirely — only TTL eviction applies.
//
// All other knobs (ttl, reaper, ping) match NewSharedClientCache's
// defaults. For full control, see NewSharedClientCacheWithOptions.
func NewSharedClientCacheWithLimits(ttl time.Duration, maxEntries int, info mcp.ClientInfo) *SharedClientCache {
	return newSharedClientCacheFull(cacheBuildConfig{
		ttl:            ttl,
		reaperInterval: defaultReaperInterval,
		maxEntries:     maxEntries,
		pingInterval:   defaultPingInterval,
		pingTimeout:    defaultPingTimeout,
		info:           info,
	})
}

// SharedClientCacheOptions is the public knob bundle for callers that
// need to tune more than just ttl + maxEntries — e.g. configuring the
// proactive health-check loop. Zero values fall back to defaults
// (defaultCacheTTL, defaultCacheMaxEntries, defaultPingInterval,
// defaultPingTimeout). PingInterval == 0 explicitly disables the
// health-check loop while keeping reactive eviction in Pool.Call.
//
// reaperInterval is intentionally NOT exposed here — it's a tuning
// knob with no operator-visible signal beyond test harnesses, and
// the in-package newSharedClientCacheFull is sufficient for those.
type SharedClientCacheOptions struct {
	TTL          time.Duration
	MaxEntries   int
	PingInterval time.Duration
	PingTimeout  time.Duration
	Info         mcp.ClientInfo
	// HTTPClient is shared across every HTTP MCP transport the cache
	// spawns. nil falls back to a default `&http.Client{Timeout:
	// 5*time.Minute}`. Inject a custom client when the deploy needs
	// a corporate proxy, mTLS, an alternate DialContext, or a
	// different per-request timeout. Stdio MCP servers ignore this
	// field.
	HTTPClient *http.Client
}

// NewSharedClientCacheWithOptions builds a cache from the full
// option set. Use this when you need fine control over the
// health-check cadence (e.g. a deploy that wants pings every 30s
// instead of the default 60s, or wants to disable them entirely),
// or when you need to inject a custom *http.Client (proxy, mTLS,
// alternate timeouts).
func NewSharedClientCacheWithOptions(opts SharedClientCacheOptions) *SharedClientCache {
	maxEntries := opts.MaxEntries
	if maxEntries == 0 {
		maxEntries = defaultCacheMaxEntries
	}
	pingInterval := opts.PingInterval
	if pingInterval == 0 {
		// Use the default for the unset case. Callers that want the
		// loop disabled pass a negative value.
		pingInterval = defaultPingInterval
	}
	if pingInterval < 0 {
		pingInterval = 0 // disable
	}
	return newSharedClientCacheFull(cacheBuildConfig{
		ttl:            opts.TTL,
		reaperInterval: defaultReaperInterval,
		maxEntries:     maxEntries,
		pingInterval:   pingInterval,
		pingTimeout:    opts.PingTimeout,
		info:           opts.Info,
		httpClient:     opts.HTTPClient,
	})
}

// cacheBuildConfig captures every knob the internal constructor
// supports. Using a struct rather than positional params keeps the
// argument list manageable as the cache grows new options
// (max-entries, ping-interval, ping-timeout, ...).
type cacheBuildConfig struct {
	ttl            time.Duration
	reaperInterval time.Duration
	maxEntries     int
	pingInterval   time.Duration
	pingTimeout    time.Duration
	info           mcp.ClientInfo
	httpClient     *http.Client // nil → cache lazy-constructs the default
}

// newSharedClientCacheFull is the internal constructor that takes
// every knob. Tests use it to drive tight TTL / reaper / ping
// windows without racing the reaper goroutine — every field must be
// set BEFORE the goroutine starts (mutating after construction would
// race with the goroutine's own reads).
//
// Sentinel handling on each field: ttl <= 0 → defaultCacheTTL;
// reaperInterval <= 0 → defaultReaperInterval; maxEntries <= 0 → cap
// disabled; pingInterval <= 0 → health-check loop disabled (the
// reactive eviction in Pool.Call still applies); pingTimeout <= 0 →
// defaultPingTimeout (only meaningful when pingInterval > 0).
func newSharedClientCacheFull(b cacheBuildConfig) *SharedClientCache {
	if b.ttl <= 0 {
		b.ttl = defaultCacheTTL
	}
	if b.reaperInterval <= 0 {
		b.reaperInterval = defaultReaperInterval
	}
	if b.pingTimeout <= 0 {
		b.pingTimeout = defaultPingTimeout
	}
	if b.httpClient == nil {
		// Match NewHTTPTransport's prior default so behavior on the
		// "no overrides" path is unchanged. Operators that want a
		// different timeout / proxy / mTLS inject via
		// SharedClientCacheOptions.HTTPClient.
		b.httpClient = &http.Client{Timeout: 5 * time.Minute}
	}
	c := &SharedClientCache{
		entries:      make(map[string]*cacheEntry),
		ttl:          b.ttl,
		info:         b.info,
		reaper:       b.reaperInterval,
		maxEntries:   b.maxEntries,
		pingInterval: b.pingInterval, // 0 = disabled
		pingTimeout:  b.pingTimeout,
		httpClient:   b.httpClient,
		closeCh:      make(chan struct{}),
	}
	c.reaperWg.Add(1)
	go c.reaperLoop()
	if b.pingInterval > 0 {
		c.reaperWg.Add(1)
		go c.healthCheckLoop()
	}
	return c
}

// newSharedClientCacheWithReaper is the legacy four-arg constructor
// kept for tests written against the prior signature. Treats the cap
// as disabled (0) and disables the health-check loop — explicit cap /
// health-check tests use newSharedClientCacheFull directly.
func newSharedClientCacheWithReaper(ttl, reaperInterval time.Duration, info mcp.ClientInfo) *SharedClientCache {
	return newSharedClientCacheFull(cacheBuildConfig{
		ttl:            ttl,
		reaperInterval: reaperInterval,
		info:           info,
	})
}

// Acquire returns a Client + tools snapshot for cfg, spawning one on
// a miss. The caller MUST call the returned release func exactly once
// when finished — that decrements the refcount so the reaper can
// eventually evict the entry.
//
// Returns the same (client, tools) tuple for every Acquire of the
// same cfg until either Evict is called or Close runs. The tools
// slice is the upstream's catalog at first-init time; we don't
// re-list across runs because tools/list is rarely dynamic and
// re-running it on every Acquire would erase most of the cache's
// latency win.
//
// On error (init fails, network down) no entry is created and no
// release func is returned.
// SetObserver attaches the given CacheObserver. nil clears any
// previously-set observer. Safe to call before Acquire-type
// operations begin; not safe to call concurrently with them.
// Production code wires this once at construction (via
// NewSharedClientCacheWithLimits → orchestrator helper) and never
// touches it again.
func (c *SharedClientCache) SetObserver(o *CacheObserver) {
	c.observer = o
}

func (c *SharedClientCache) Acquire(ctx context.Context, cfg ServerConfig) (*Client, []mcp.Tool, func(), error) {
	key := configKey(cfg)
	server := cfg.Name

	// Fast path: cache hit.
	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		e.inUse++
		e.lastUsed = time.Now()
		client, tools := e.client, e.tools
		c.mu.Unlock()
		c.notifyHit(server)
		return client, tools, c.releaseFor(key), nil
	}
	c.mu.Unlock()

	// Miss: notify before spawning so observers see the miss even if
	// the spawn fails (an init-failure miss is still operationally
	// interesting — operators should see "we tried and couldn't").
	c.notifyMiss(server)

	// Spawn outside the lock so concurrent Acquires for OTHER keys
	// aren't blocked behind this one's process exec. Pass the
	// cache's shared *http.Client so every HTTP MCP transport this
	// cache spawns reuses the same client (single point to configure
	// timeout / proxy / mTLS via SharedClientCacheOptions.HTTPClient).
	client, tools, err := spawnClient(ctx, c.info, cfg, c.httpClient)
	if err != nil {
		return nil, nil, nil, err
	}

	// Race recheck: another goroutine may have spawned the same key
	// while we were spawning ours. Whoever inserts first wins; the
	// loser closes its Client and uses the winner's.
	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		c.mu.Unlock()
		_ = client.Close()
		c.mu.Lock()
		e.inUse++
		e.lastUsed = time.Now()
		client, tools = e.client, e.tools
		c.mu.Unlock()
		return client, tools, c.releaseFor(key), nil
	}
	// Cap enforcement: if we're at-or-over maxEntries before this
	// insert, try to evict the least-recently-used IDLE entry first
	// so the new insert doesn't grow the working set unbounded. If
	// every entry is in-use we allow the over-cap insert anyway —
	// blocking Acquire would break a legitimate run, and TTL eviction
	// or future releases will catch up. Eviction happens INSIDE the
	// lock so a concurrent Acquire can't race into the slot we're
	// freeing; the actual Close call goes outside the lock so a slow
	// teardown doesn't block other operations.
	var evicted *Client
	if c.maxEntries > 0 && len(c.entries) >= c.maxEntries {
		if victimKey := c.pickLRUIdleLocked(); victimKey != "" {
			evicted = c.entries[victimKey].client
			delete(c.entries, victimKey)
		}
	}
	c.entries[key] = &cacheEntry{
		client:   client,
		tools:    tools,
		inUse:    1,
		lastUsed: time.Now(),
	}
	c.mu.Unlock()
	if evicted != nil {
		_ = evicted.Close()
		// Eviction here happens server-agnostically — the cache key
		// excludes Name on purpose, so we don't have a single
		// authoritative alias to attribute. Fire with empty server.
		c.notifyEvicted("")
	}
	return client, tools, c.releaseFor(key), nil
}

func (c *SharedClientCache) notifyHit(server string) {
	if c.observer != nil && c.observer.OnHit != nil {
		c.observer.OnHit(server)
	}
}

func (c *SharedClientCache) notifyMiss(server string) {
	if c.observer != nil && c.observer.OnMiss != nil {
		c.observer.OnMiss(server)
	}
}

func (c *SharedClientCache) notifyEvicted(server string) {
	if c.observer != nil && c.observer.OnEvicted != nil {
		c.observer.OnEvicted(server)
	}
}

// pickLRUIdleLocked returns the key of the least-recently-used entry
// with refcount == 0, or "" if no idle entry exists. Caller must hold
// c.mu. O(N) over the cache; cheap enough for a cap of a few hundred.
func (c *SharedClientCache) pickLRUIdleLocked() string {
	var (
		victimKey  string
		victimTime time.Time
	)
	for key, e := range c.entries {
		if e.inUse > 0 {
			continue
		}
		if victimKey == "" || e.lastUsed.Before(victimTime) {
			victimKey = key
			victimTime = e.lastUsed
		}
	}
	return victimKey
}

// Evict removes a cached entry on demand and tears down its Client.
// Use when a Pool.Call returns a transport-closed error, indicating
// the subprocess died — without eviction the next Acquire would
// hand back the same dead client.
//
// No-op if the cfg isn't currently cached. The Client is closed
// outside the cache lock so a slow tear-down doesn't block other
// Acquires.
func (c *SharedClientCache) Evict(cfg ServerConfig) {
	key := configKey(cfg)
	c.mu.Lock()
	e, ok := c.entries[key]
	if ok {
		delete(c.entries, key)
	}
	c.mu.Unlock()
	if ok {
		_ = e.client.Close()
		// Caller has the original cfg, so we know the alias here —
		// fire the observer with the operator's name for richer
		// metrics granularity than the alias-less reaper path.
		c.notifyEvicted(cfg.Name)
	}
}

// Close stops the reaper and tears down every cached Client.
// Idempotent — a second call is a no-op. Errors from individual
// client closes are joined so the operator sees them all.
//
// Callers should ensure no in-flight runs are still holding cached
// clients before Close — typically Runner.Shutdown drains those
// first. Close does NOT wait for refcount=0; it tears entries down
// regardless, so a stragller run will see a transport error.
func (c *SharedClientCache) Close() error {
	c.closeOnce.Do(func() {
		close(c.closeCh)
	})

	// Snapshot + close clients FIRST, before waiting for the reaper
	// loops to exit. The health-check loop may be blocked inside
	// Client.Ping waiting for an upstream that's hanging (the exact
	// failure mode the loop is supposed to detect). Closing the
	// transport interrupts the ping, the loop returns from
	// pingIdleEntries, sees closeCh on its next iteration, and
	// exits — letting reaperWg.Wait below complete.
	//
	// Order swapped from the previous version (Wait → snapshot →
	// close clients), which deadlocked when a ping was in flight at
	// Close time.
	c.mu.Lock()
	clients := make([]*Client, 0, len(c.entries))
	for _, e := range c.entries {
		clients = append(clients, e.client)
	}
	c.entries = make(map[string]*cacheEntry)
	c.mu.Unlock()

	var errs []error
	for _, cl := range clients {
		if err := cl.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	c.reaperWg.Wait()

	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// CacheStats is a snapshot of cache occupancy. Used by tests and the
// /hecate/v1/system/mcp/cache endpoint to confirm the cache is doing useful work.
//
//   - Entries is the number of distinct cached upstreams.
//   - InUse is the SUM of refcounts across all entries — i.e. the
//     total number of live Acquire→Release pairs in flight, NOT the
//     count of entries with at least one acquirer. (An entry held by
//     two concurrent runs contributes 2 to InUse and 0 to Idle.)
//   - Idle is the number of entries with refcount == 0; these are
//     the ones the reaper will evict once their lastUsed crosses the
//     TTL boundary.
//
// Entries == InUse-bucket-entries + Idle.
type CacheStats struct {
	Entries int
	InUse   int
	Idle    int
}

// Stats returns the current cache state. Cheap; safe to call hot.
func (c *SharedClientCache) Stats() CacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := CacheStats{Entries: len(c.entries)}
	for _, e := range c.entries {
		s.InUse += e.inUse
		if e.inUse == 0 {
			s.Idle++
		}
	}
	return s
}

func (c *SharedClientCache) releaseFor(key string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if e, ok := c.entries[key]; ok {
				e.inUse--
				if e.inUse < 0 {
					// Defensive: a double-release would otherwise let
					// the reaper kick out an entry someone is still
					// holding. Clamp to zero rather than panic.
					e.inUse = 0
				}
				e.lastUsed = time.Now()
			}
		})
	}
}

func (c *SharedClientCache) reaperLoop() {
	defer c.reaperWg.Done()
	ticker := time.NewTicker(c.reaper)
	defer ticker.Stop()
	for {
		select {
		case <-c.closeCh:
			return
		case <-ticker.C:
			c.evictIdle()
		}
	}
}

func (c *SharedClientCache) evictIdle() {
	cutoff := time.Now().Add(-c.ttl)
	c.mu.Lock()
	var toClose []*Client
	for key, e := range c.entries {
		if e.inUse == 0 && e.lastUsed.Before(cutoff) {
			toClose = append(toClose, e.client)
			delete(c.entries, key)
		}
	}
	c.mu.Unlock()
	for _, cl := range toClose {
		_ = cl.Close()
		// TTL eviction is alias-agnostic — the cache key omits Name
		// on purpose, so we don't have a stable alias to attribute
		// here. Counters keyed only by event=evicted still answer
		// "how often is the cache reaping things?" without server
		// granularity.
		c.notifyEvicted("")
	}
}

// healthCheckLoop is the proactive liveness probe. Every pingInterval
// tick it pings every IDLE entry whose last successful ping is older
// than pingInterval; entries that fail or time out are evicted. Idle
// entries are the only safe targets — pinging an in-use entry would
// race the active tool call's response on the same channel; the
// active caller will see any failure itself and trip the reactive
// eviction in Pool.Call.
//
// Runs on its own ticker (separate from the TTL reaper) so the two
// cadences can be tuned independently. Stops when c.closeCh fires.
func (c *SharedClientCache) healthCheckLoop() {
	defer c.reaperWg.Done()
	ticker := time.NewTicker(c.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.closeCh:
			return
		case <-ticker.C:
			c.pingIdleEntries()
		}
	}
}

// pingIdleEntries snapshots the idle entries due for a ping, drops
// the lock, pings each one with pingTimeout, then re-takes the lock
// briefly to either update lastPinged (success) or evict (failure).
//
// Concurrency notes:
//   - The ping itself happens OUTSIDE the lock so a slow upstream
//     can't block other Acquire calls.
//   - Between the snapshot and the eviction step an entry may have
//     gone in-use (a fresh Acquire). We re-check inUse under the
//     lock before evicting; if the entry is now in-use, skip
//     eviction — the active caller will see any failure itself.
//   - On Close, c.closeCh closing aborts the loop's outer select but
//     pings already in flight finish; ServerConfig.Close on the
//     transport will fail those that haven't returned, evicting
//     stale entries either way.
func (c *SharedClientCache) pingIdleEntries() {
	type target struct {
		key    string
		client *Client
	}
	cutoff := time.Now().Add(-c.pingInterval)

	c.mu.Lock()
	targets := make([]target, 0, len(c.entries))
	for key, e := range c.entries {
		if e.inUse > 0 {
			continue
		}
		// Fresh entries (lastPinged == zero) get pinged on the first
		// tick after they go idle. Subsequent pings space out by
		// pingInterval.
		if !e.lastPinged.IsZero() && e.lastPinged.After(cutoff) {
			continue
		}
		targets = append(targets, target{key: key, client: e.client})
	}
	c.mu.Unlock()

	if len(targets) == 0 {
		return
	}

	for _, t := range targets {
		ctx, cancel := context.WithTimeout(context.Background(), c.pingTimeout)
		err := t.client.Ping(ctx)
		cancel()

		c.mu.Lock()
		entry, stillCached := c.entries[t.key]
		switch {
		case !stillCached:
			// Entry was already evicted (TTL, manual Evict, race
			// against Close). Nothing to do.
			c.mu.Unlock()
		case entry.inUse > 0:
			// Someone Acquired this entry while we were pinging.
			// Don't evict mid-call; the caller sees its own errors.
			// Update lastPinged on success so we don't re-probe
			// immediately on the next tick.
			if err == nil {
				entry.lastPinged = time.Now()
			}
			c.mu.Unlock()
		case err != nil:
			// Wedged. Evict.
			delete(c.entries, t.key)
			c.mu.Unlock()
			_ = t.client.Close()
			c.notifyEvicted("")
		default:
			entry.lastPinged = time.Now()
			c.mu.Unlock()
		}
	}
}

// configKey is the SHA-256 cache key over a ServerConfig's
// transport-identifying fields. We sort env/header maps before
// hashing so two configs that differ only in iteration order map to
// the same key.
//
// Name is intentionally NOT in the key — it's the operator-chosen
// alias used by Pool to namespace tools, not part of the upstream
// identity. Two tasks aliasing the same upstream as "fs" and
// "filesystem" share one subprocess.
func configKey(cfg ServerConfig) string {
	h := sha256.New()
	h.Write([]byte("cmd|"))
	h.Write([]byte(cfg.Command))
	for _, a := range cfg.Args {
		h.Write([]byte("\x00arg|"))
		h.Write([]byte(a))
	}
	for _, k := range sortedMapKeys(cfg.Env) {
		h.Write([]byte("\x00env|"))
		h.Write([]byte(k))
		h.Write([]byte{'='})
		h.Write([]byte(cfg.Env[k]))
	}
	h.Write([]byte("\x00url|"))
	h.Write([]byte(cfg.URL))
	for _, k := range sortedMapKeys(cfg.Headers) {
		h.Write([]byte("\x00hdr|"))
		h.Write([]byte(k))
		h.Write([]byte{'='})
		h.Write([]byte(cfg.Headers[k]))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func sortedMapKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// IsTransportClosedErr reports whether err is the kind of transport
// failure that warrants evicting the cached client (subprocess died,
// HTTP server hung up, stdio EOF). Pool.Call uses this to decide
// when to call Cache.Evict.
func IsTransportClosedErr(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrTransportClosed)
}
