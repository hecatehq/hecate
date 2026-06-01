package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/mcp"
)

// ServerConfig is one external MCP server the pool should bring up.
// Exactly one of Command (stdio) or URL (HTTP) must be set.
//
// Stdio: the pool spawns Command (with Args and merged Env) as a
// subprocess and communicates via stdin/stdout.
//
// HTTP: the pool connects to URL using the Streamable HTTP transport.
// Headers are sent on every request (e.g. {"Authorization": "Bearer
// <token>"}). Env is ignored for HTTP servers.
//
// Duplicated from pkg/types.MCPServerConfig so the client package
// stays free of the orchestrator's type tree. The orchestrator
// converts on the way in.
type ServerConfig struct {
	Name string
	// Stdio transport — mutually exclusive with URL.
	Command string
	Args    []string
	Env     map[string]string
	// HTTP transport — mutually exclusive with Command.
	URL     string
	Headers map[string]string
}

// PoolToolName is the namespace prefix every pool-vended tool name
// carries. Built into a separator constant so dispatch can split
// `mcp__filesystem__read_file` back into ("filesystem", "read_file")
// without ambiguity.
const (
	PoolToolNamespacePrefix = "mcp"
	PoolToolNamespaceSep    = "__"
)

// NamespacedTool is one tool surfaced by the pool. Name is already
// `mcp__<server>__<tool>`; Description and Schema come straight from
// the upstream server (no rewriting).
type NamespacedTool struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// Pool runs N MCP-client connections, one per ServerConfig, and
// multiplexes tool dispatch by name. Lifecycle:
//
//   - NewPool spawns every server, runs initialize, lists tools.
//     Partial failure aborts: any clients already up are closed
//     before the error returns. The caller never sees a half-built
//     pool.
//   - Tools() returns the merged catalog (deterministic order: sorted
//     by namespaced name).
//   - Call routes by namespaced name to the right client; tool-level
//     errors come back as (text, isError=true, nil), protocol errors
//     as a non-nil error.
//   - Close shuts every client down. Idempotent.
type Pool struct {
	mu      sync.Mutex
	cache   *SharedClientCache               // optional; nil = per-run lifetime
	clients map[string]*pooledClient         // server name → client + cache release
	tools   []NamespacedTool                 // sorted, stable
	bind    map[string]namespacedToolBinding // namespaced name → routing info
}

// pooledClient pairs a Client with the bookkeeping the Pool needs to
// hand it back. cfg is retained so a transport-closed error from this
// client's calls can be matched to the cache entry for eviction.
// release is the cache's per-acquire release func when the client
// came from the cache, or nil when the Pool owns the client outright
// (uncached path); Close consults it to decide whether to release or
// shut down.
type pooledClient struct {
	client  *Client
	cfg     ServerConfig
	release func()
}

type namespacedToolBinding struct {
	serverName string
	toolName   string
}

// NewPool spawns one client per config, initializes the handshake,
// and lists tools. Returns a fully ready pool or an error (with all
// in-progress clients already torn down). Uncached: Close shuts down
// every client. For caller-shared subprocesses across runs, see
// NewPoolWithCache.
//
// info propagates to every spawned Client as their MCP ClientInfo —
// servers log who connected, so a sensible "hecate-agent-loop /
// <version>" identity helps operators when they read upstream logs.
func NewPool(ctx context.Context, info mcp.ClientInfo, configs []ServerConfig) (*Pool, error) {
	return buildPool(ctx, configs, func(ctx context.Context, cfg ServerConfig) (*Client, []mcp.Tool, func(), error) {
		// nil http.Client → NewHTTPTransport falls back to its
		// per-call default. Uncached pools spawn and tear down
		// per-run, so there's nothing to share.
		client, tools, err := spawnClient(ctx, info, cfg, nil)
		if err != nil {
			return nil, nil, nil, err
		}
		return client, tools, nil, nil
	})
}

// NewPoolWithCache is the caching counterpart: each per-server client
// is acquired from cache (spawning on a miss). Close releases the
// clients back to the cache instead of shutting them down — actual
// teardown happens via the cache's TTL eviction or its Close method.
//
// info comes from cache.info (set at cache construction), not as a
// parameter here; sharing one cache across multiple identities would
// blur upstream logs and is intentionally not supported.
func NewPoolWithCache(ctx context.Context, configs []ServerConfig, cache *SharedClientCache) (*Pool, error) {
	if cache == nil {
		return nil, errors.New("mcp pool: cache is required for NewPoolWithCache (use NewPool for uncached)")
	}
	p, err := buildPool(ctx, configs, func(ctx context.Context, cfg ServerConfig) (*Client, []mcp.Tool, func(), error) {
		return cache.Acquire(ctx, cfg)
	})
	if err != nil {
		return nil, err
	}
	p.cache = cache
	return p, nil
}

// clientFactory is the per-config bring-up callback used by buildPool.
// Returns the client, its tools snapshot, and an optional release func
// (nil when the pool itself owns the client lifetime).
type clientFactory func(ctx context.Context, cfg ServerConfig) (*Client, []mcp.Tool, func(), error)

// buildPool is the shared NewPool / NewPoolWithCache implementation.
// Validates each config, calls factory for the per-config client, and
// stitches the namespaced tool bindings.
func buildPool(ctx context.Context, configs []ServerConfig, factory clientFactory) (*Pool, error) {
	p := &Pool{
		clients: make(map[string]*pooledClient, len(configs)),
		bind:    make(map[string]namespacedToolBinding),
	}
	// Cleanup on partial-failure aborts: tear down whatever's already
	// up so a bad config doesn't leak subprocess handles or cache refs.
	cleanup := func() {
		_ = p.Close()
	}

	for _, cfg := range configs {
		name := strings.TrimSpace(cfg.Name)
		if name == "" {
			cleanup()
			return nil, errors.New("mcp pool: server name is required")
		}
		if _, dup := p.clients[name]; dup {
			cleanup()
			return nil, fmt.Errorf("mcp pool: duplicate server name %q", name)
		}
		if err := validateTransportConfig(cfg); err != nil {
			cleanup()
			return nil, fmt.Errorf("mcp pool: server %q: %w", name, err)
		}

		client, serverTools, release, err := factory(ctx, cfg)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("mcp pool: server %q: %w", name, err)
		}

		p.clients[name] = &pooledClient{client: client, cfg: cfg, release: release}
		for _, t := range serverTools {
			ns := NamespacedToolName(name, t.Name)
			if _, dup := p.bind[ns]; dup {
				// Same upstream server vending the same tool name twice
				// is the only realistic way to hit this; treat as a
				// server bug and abort rather than silently shadow.
				cleanup()
				return nil, fmt.Errorf("mcp pool: server %q vended duplicate tool %q", name, t.Name)
			}
			p.bind[ns] = namespacedToolBinding{serverName: name, toolName: t.Name}
			p.tools = append(p.tools, NamespacedTool{
				Name:        ns,
				Description: t.Description,
				Schema:      t.InputSchema,
			})
		}
	}
	sort.Slice(p.tools, func(i, j int) bool { return p.tools[i].Name < p.tools[j].Name })
	return p, nil
}

// Bounded retry constants for spawnClient. Two attempts total (one
// retry) with a 500ms backoff is the smallest useful window — enough
// to absorb a network blip or a slow-booting subprocess that's
// printing its first frame, tight enough that a permanent failure
// (missing binary, bad args, auth rejected) only adds 500ms of
// latency before the operator sees the diagnostic. We don't expose
// these as env vars: operators don't have a meaningful signal to
// tune them with, and a 3rd attempt would push permanent-failure
// latency past 1s for no benefit.
const (
	spawnClientMaxAttempts = 2
	spawnClientBackoff     = 500 * time.Millisecond
)

// spawnClient runs the per-config bring-up: build transport, init
// handshake, list tools. Used by NewPool (uncached path) and by the
// shared cache on a miss.
//
// Wraps spawnClientOnce in a bounded retry. The motivating failure
// mode is a transient first-attempt failure during initialize or
// tools/list — a network blip, a stdio server that hadn't finished
// booting yet, an HTTP server with a brief 503. The retry rebuilds
// the transport from scratch (a fresh exec.Command for stdio, a
// fresh HTTP client for URL configs), so a stuck connection from
// the failed attempt doesn't poison the second one.
//
// Permanent failures (missing binary, bad args, auth rejected) fail
// twice and return the last error; the operator sees the same
// diagnostic, just delayed by spawnClientBackoff. We don't try to
// distinguish "transient" from "permanent" at this layer — both
// surface as wrapped errors and the cost of retrying a permanent
// failure is bounded.
//
// Respects ctx: cancellation between attempts aborts the retry
// loop with the ctx error rather than waiting out the backoff.
//
// httpClient is the *http.Client every HTTP-transport attempt uses;
// nil falls back to NewHTTPTransport's per-call default. Stdio
// transports ignore it. Threading through here lets the
// SharedClientCache reuse a single client across every HTTP MCP
// server it spawns — the seam for proxy / mTLS / custom-timeout
// deploys.
func spawnClient(ctx context.Context, info mcp.ClientInfo, cfg ServerConfig, httpClient *http.Client) (*Client, []mcp.Tool, error) {
	var lastErr error
	for attempt := 1; attempt <= spawnClientMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				// Surface BOTH errors: the underlying spawn failure
				// (which is what the operator usually wants) and the
				// ctx error (so they know we didn't get to retry).
				return nil, nil, fmt.Errorf("%w (after %d attempt(s); ctx: %v)", lastErr, attempt-1, err)
			}
			return nil, nil, err
		}
		client, tools, err := spawnClientOnce(ctx, info, cfg, httpClient)
		if err == nil {
			return client, tools, nil
		}
		lastErr = err
		if attempt < spawnClientMaxAttempts {
			// Don't sleep past ctx — a cancelled run shouldn't add
			// latency before bailing.
			select {
			case <-time.After(spawnClientBackoff):
			case <-ctx.Done():
				return nil, nil, fmt.Errorf("%w (after %d attempt(s); ctx: %v)", lastErr, attempt, ctx.Err())
			}
		}
	}
	return nil, nil, lastErr
}

// spawnClientOnce is one bring-up attempt: build transport, run
// initialize, run tools/list. Failures close the transport before
// returning so the retry path doesn't leak file descriptors or
// goroutines on a partially-initialized client.
func spawnClientOnce(ctx context.Context, info mcp.ClientInfo, cfg ServerConfig, httpClient *http.Client) (*Client, []mcp.Tool, error) {
	transport, err := buildTransport(ctx, cfg, httpClient)
	if err != nil {
		return nil, nil, err
	}
	client := New(transport, info)
	if _, err := client.Initialize(ctx); err != nil {
		// Surface stderr from stdio servers — the JSON-RPC error
		// alone (often "EOF") rarely names the root cause (missing
		// deps, bad arg, auth failure). HTTP transports have no
		// stderr; the HTTP status error already carries the detail.
		var diag string
		if st, ok := transport.(*StdioTransport); ok {
			diag = st.Stderr()
		}
		_ = client.Close()
		if strings.TrimSpace(diag) != "" {
			return nil, nil, fmt.Errorf("initialize: %w; stderr: %s", err, strings.TrimSpace(diag))
		}
		return nil, nil, fmt.Errorf("initialize: %w", err)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("list tools: %w", err)
	}
	return client, tools, nil
}

// validateTransportConfig enforces the stdio-XOR-HTTP invariant on a
// single ServerConfig. Pure validation — does not touch processes or
// network. Trims whitespace.
func validateTransportConfig(cfg ServerConfig) error {
	command := strings.TrimSpace(cfg.Command)
	rawURL := strings.TrimSpace(cfg.URL)
	if command != "" && rawURL != "" {
		return errors.New("command and url are mutually exclusive")
	}
	if command == "" && rawURL == "" {
		return errors.New("either command or url is required")
	}
	return nil
}

// buildTransport materializes a Transport from a (validated) config.
// Stdio configs become a *StdioTransport over an exec.CommandContext;
// URL configs become a *HTTPTransport using httpClient (or a fresh
// default-construction if nil — preserves the prior per-transport
// behavior). Validation is the caller's responsibility — call
// validateTransportConfig first.
//
// httpClient is ignored for stdio transports.
func buildTransport(ctx context.Context, cfg ServerConfig, httpClient *http.Client) (Transport, error) {
	rawURL := strings.TrimSpace(cfg.URL)
	if rawURL != "" {
		t, err := NewHTTPTransport(rawURL, cfg.Headers, httpClient)
		if err != nil {
			return nil, fmt.Errorf("http: %w", err)
		}
		return t, nil
	}
	cmd := exec.CommandContext(ctx, strings.TrimSpace(cfg.Command), cfg.Args...)
	cmd.Env = mergeEnv(sanitizedStdioEnv(os.Environ()), cfg.Env)
	t, err := NewStdioTransport(cmd)
	if err != nil {
		return nil, fmt.Errorf("spawn: %w", err)
	}
	return t, nil
}

// Tools returns the merged tool catalog. Stable order across calls
// (sorted by namespaced name) so the LLM sees a deterministic list.
// The slice is a copy — callers may not mutate it.
func (p *Pool) Tools() []NamespacedTool {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]NamespacedTool, len(p.tools))
	copy(out, p.tools)
	return out
}

// Call dispatches a namespaced tool call. Returns:
//   - text: the concatenated text content from the upstream
//     CallToolResult (one block per line). MCP allows non-text
//     content blocks; this layer flattens them to text since
//     agent_loop's tool-result message is text-only.
//   - isError: true when the upstream marked CallToolResult.IsError.
//   - err: non-nil for protocol-level failures (unknown tool, RPC
//     error, transport closed). Tool-level failures come back via
//     isError=true with err=nil.
func (p *Pool) Call(ctx context.Context, name string, args json.RawMessage) (text string, isError bool, err error) {
	p.mu.Lock()
	bind, ok := p.bind[name]
	pc := p.clients[bind.serverName]
	cache := p.cache
	p.mu.Unlock()
	if !ok {
		return "", false, fmt.Errorf("mcp pool: unknown tool %q", name)
	}
	if pc == nil {
		// Defensive — shouldn't happen since bind and clients are
		// populated together. Surface as an error rather than
		// panicking.
		return "", false, fmt.Errorf("mcp pool: server for tool %q is not connected", name)
	}
	res, err := pc.client.CallTool(ctx, bind.toolName, args)
	if err != nil {
		// Reactive health check: if the call failed because the
		// transport is gone, evict from cache so the next run respawns
		// instead of being handed back the same dead client. Uncached
		// pools own the client outright, so there's nothing to evict;
		// the next NewPool will spawn fresh anyway.
		if cache != nil && IsTransportClosedErr(err) {
			cache.Evict(pc.cfg)
		}
		return "", false, err
	}
	return flattenContent(res.Content), res.IsError, nil
}

// Close tears every client down (uncached pools) or releases them
// back to the cache (cached pools). Errors from individual closes are
// joined into a single error so the operator sees them all without
// losing the first failure to log truncation.
//
// Idempotent: a second Close is a no-op (the clients map is cleared
// on the first call and Close re-runs over an empty map).
func (p *Pool) Close() error {
	p.mu.Lock()
	clients := p.clients
	cached := p.cache != nil
	p.clients = make(map[string]*pooledClient)
	p.bind = make(map[string]namespacedToolBinding)
	p.tools = nil
	p.mu.Unlock()

	var errs []error
	for name, pc := range clients {
		if cached && pc.release != nil {
			// Cached path: hand the client back to the cache. The
			// cache decides when to actually close it via TTL or its
			// own Close.
			pc.release()
			continue
		}
		if err := pc.client.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close %q: %w", name, err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// NamespacedToolName builds the wire name for a server's tool. Public
// so callers can predict the name without consulting the pool — agent
// loop uses this when emitting telemetry that references a tool by
// its un-namespaced upstream name.
func NamespacedToolName(serverName, toolName string) string {
	return PoolToolNamespacePrefix + PoolToolNamespaceSep + serverName + PoolToolNamespaceSep + toolName
}

// SplitNamespacedToolName is the inverse: splits `mcp__<server>__<tool>`
// back into (server, tool, true). Returns ("", "", false) on anything
// that doesn't match the prefix or has too few segments. The tool name
// itself may contain double-underscores (some upstream servers use
// them); we honor the FIRST split after the server segment, treating
// the rest as the tool name.
func SplitNamespacedToolName(ns string) (serverName, toolName string, ok bool) {
	prefix := PoolToolNamespacePrefix + PoolToolNamespaceSep
	if !strings.HasPrefix(ns, prefix) {
		return "", "", false
	}
	rest := ns[len(prefix):]
	idx := strings.Index(rest, PoolToolNamespaceSep)
	if idx < 0 {
		return "", "", false
	}
	server := rest[:idx]
	tool := rest[idx+len(PoolToolNamespaceSep):]
	if server == "" || tool == "" {
		return "", "", false
	}
	return server, tool, true
}

// flattenContent collapses a CallToolResult.Content slice into a
// single text string. We join blocks with newlines; non-text blocks
// (image, resource) are rendered as a placeholder so the LLM at
// least sees that something was returned. agent_loop will surface
// images / resources directly once we ship multi-modal tool results.
func flattenContent(blocks []mcp.ContentBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	var b strings.Builder
	for i, blk := range blocks {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch blk.Type {
		case "text", "":
			b.WriteString(blk.Text)
		default:
			fmt.Fprintf(&b, "[%s content omitted]", blk.Type)
		}
	}
	return b.String()
}

func sanitizedStdioEnv(env []string) []string {
	allowedPrefixes := []string{
		"PATH=",
		"Path=",
		"HOME=",
		"USERPROFILE=",
		"HOMEDRIVE=",
		"HOMEPATH=",
		"TMPDIR=",
		"TEMP=",
		"TMP=",
		"LANG=",
		"LC_",
		"TERM=",
		"USER=",
		"USERNAME=",
		"LOGNAME=",
		"APPDATA=",
		"LOCALAPPDATA=",
		"XDG_",
		"VOLTA_",
		"SSL_CERT_FILE=",
		"SSL_CERT_DIR=",
		"NODE_EXTRA_CA_CERTS=",
		"SystemRoot=",
		"WINDIR=",
		"ComSpec=",
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		for _, prefix := range allowedPrefixes {
			if strings.HasPrefix(entry, prefix) {
				out = append(out, entry)
				break
			}
		}
	}
	return out
}

// mergeEnv layers the per-server Env map onto the sanitized process
// environment. Runtime essentials come from the parent process, while
// credentials must be supplied explicitly in the MCP server config.
// Explicit values win. Returns a new slice; doesn't mutate the input.
func mergeEnv(parent []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return append([]string(nil), parent...)
	}
	// Index parent by key for O(1) override.
	idx := make(map[string]int, len(parent))
	out := make([]string, len(parent), len(parent)+len(overrides))
	copy(out, parent)
	for i, kv := range out {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		idx[kv[:eq]] = i
	}
	for k, v := range overrides {
		entry := k + "=" + v
		if i, ok := idx[k]; ok {
			out[i] = entry
		} else {
			out = append(out, entry)
		}
	}
	return out
}
