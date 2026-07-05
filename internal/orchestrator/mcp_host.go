package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/mcp"
	mcpclient "github.com/hecatehq/hecate/internal/mcp/client"
	"github.com/hecatehq/hecate/internal/secrets"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/internal/version"
	"github.com/hecatehq/hecate/pkg/types"
)

// AgentMCPHost is the seam the agent loop uses to talk to a bundle of
// external MCP servers. The production implementation is a
// mcpclient.Pool wrapper; tests substitute an in-memory fake.
//
// Lifetime is one-per-run: built before the loop's first turn, closed
// before Execute returns. Long-lived per-task pooling is a follow-up
// — for now we eat the spawn cost on each run because runs are short
// and the simplicity of "subprocess dies with the run" is worth more
// than the few-hundred-ms savings.
type AgentMCPHost interface {
	// Tools returns the merged tool catalog (already in the LLM's
	// expected shape, names already namespaced). Stable order.
	Tools() []types.Tool
	// Call dispatches a tool by its namespaced name. Returns:
	//   - text:    the upstream content, flattened to a single string
	//   - isError: upstream signaled CallToolResult.IsError
	//   - err:     protocol-level failure (transport, RPC error)
	Call(ctx context.Context, name string, args json.RawMessage) (text string, isError bool, err error)
	// Close shuts every underlying client down. Idempotent.
	Close() error
}

// AgentMCPDetailedHost is implemented by MCP hosts that can return the
// full MCP tool result alongside the model-visible text fallback. The
// agent loop uses it to capture MCP Apps resources for the chat UI
// without changing the legacy host seam used by tests and alternate
// hosts.
type AgentMCPDetailedHost interface {
	CallDetailed(ctx context.Context, name string, args json.RawMessage) (mcpclient.ToolCallResult, error)
}

// AgentMCPHostFactory builds a host from a slice of per-task server
// configs. Returns nil when configs is empty (the agent loop skips
// MCP plumbing entirely in that case). On error the caller treats
// the run as failed — there's no partial-host fallback.
type AgentMCPHostFactory func(ctx context.Context, configs []types.MCPServerConfig) (AgentMCPHost, error)

// DefaultMCPHostFactory is the no-cipher / no-cache default. Use
// NewDefaultMCPHostFactory(cipher, cache) via Runner.SetMCPHostFactory
// when the control-plane cipher is available (so env values stored as
// "enc:<base64>" are decrypted at spawn time) and/or when a shared
// client cache is wired (so subprocesses are reused across runs).
var DefaultMCPHostFactory AgentMCPHostFactory = NewDefaultMCPHostFactory(nil, nil)

// NewDefaultMCPHostFactory returns a production factory that resolves
// secret env values and produces a Pool per run. cipher may be nil —
// enc:-prefixed values that arrive without a cipher return a clear
// error at spawn time so the operator knows the key is missing, rather
// than forwarding ciphertext to the subprocess.
//
// cache may also be nil. When non-nil, every per-server client is
// acquired from the cache and released on Pool.Close, so subsequent
// runs that configure the same upstream skip the spawn cost. When nil,
// the factory falls back to the existing per-run lifetime — every run
// spawns and closes its own subprocesses.
func NewDefaultMCPHostFactory(cipher secrets.Cipher, cache *mcpclient.SharedClientCache) AgentMCPHostFactory {
	return func(ctx context.Context, configs []types.MCPServerConfig) (AgentMCPHost, error) {
		if len(configs) == 0 {
			return nil, nil
		}
		resolved, err := resolveEnvConfigs(configs, cipher)
		if err != nil {
			return nil, err
		}
		clientCfgs := toClientServerConfigs(resolved)
		var pool *mcpclient.Pool
		if cache != nil {
			pool, err = mcpclient.NewPoolWithCache(ctx, clientCfgs, cache)
		} else {
			pool, err = mcpclient.NewPool(ctx, agentClientInfo(), clientCfgs)
		}
		if err != nil {
			return nil, err
		}
		return &poolMCPHost{pool: pool}, nil
	}
}

// isEnvRef reports whether v is a $VAR_NAME reference. Accepted syntax
// is a dollar sign followed by a POSIX env-var name: the first
// character must be [A-Za-z_] and subsequent characters [A-Za-z0-9_].
// A bare "$", "$123", or "$foo-bar" are not valid references.
func isEnvRef(v string) bool {
	if len(v) < 2 || v[0] != '$' {
		return false
	}
	for i, c := range v[1:] {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c == '_':
			// valid in any position
		case c >= '0' && c <= '9':
			if i == 0 {
				return false // can't start with a digit
			}
		default:
			return false
		}
	}
	return true
}

// resolveEnvValue resolves a single env value at subprocess spawn time:
//
//   - "$VAR_NAME" — looked up via os.LookupEnv; errors if unset or empty
//     (an empty token is almost always a misconfiguration).
//   - "enc:<base64>" — decrypted with cipher; errors if cipher is nil
//     (key not configured) or decryption fails.
//   - starts with "$" but is not a valid name → error (malformed reference).
//   - anything else → returned as a literal, unchanged.
func resolveEnvValue(serverName, key, value string, cipher secrets.Cipher) (string, error) {
	switch {
	case strings.HasPrefix(value, types.MCPEnvEncPrefix):
		if cipher == nil {
			return "", fmt.Errorf("mcp server %q: env %q: value is encrypted (enc:) but no control-plane secret key is configured", serverName, key)
		}
		plaintext, err := cipher.Decrypt(value[len(types.MCPEnvEncPrefix):])
		if err != nil {
			return "", fmt.Errorf("mcp server %q: env %q: decrypt: %w", serverName, key, err)
		}
		return plaintext, nil

	case len(value) > 0 && value[0] == '$':
		if !isEnvRef(value) {
			return "", fmt.Errorf("mcp server %q: env %q: %q looks like a variable reference but is not a valid env-var name (expected $NAME)", serverName, key, value)
		}
		varName := value[1:]
		resolved, exists := os.LookupEnv(varName)
		if !exists {
			return "", fmt.Errorf("mcp server %q: env %q: $%s is not set in the runtime environment", serverName, key, varName)
		}
		if resolved == "" {
			return "", fmt.Errorf("mcp server %q: env %q: $%s is set but empty", serverName, key, varName)
		}
		return resolved, nil

	default:
		return value, nil
	}
}

// resolveEnvConfigs resolves every env value in each config. Returns a
// new slice without mutating the originals. The first resolution error
// aborts the whole set — a partial-resolution pool would spawn servers
// with wrong or missing credentials.
func resolveEnvConfigs(configs []types.MCPServerConfig, cipher secrets.Cipher) ([]types.MCPServerConfig, error) {
	if len(configs) == 0 {
		return configs, nil
	}
	out := make([]types.MCPServerConfig, len(configs))
	for i, cfg := range configs {
		resolved := cfg
		if len(cfg.Env) > 0 {
			env := make(map[string]string, len(cfg.Env))
			for k, v := range cfg.Env {
				rv, err := resolveEnvValue(cfg.Name, k, v, cipher)
				if err != nil {
					return nil, err
				}
				env[k] = rv
			}
			resolved.Env = env
		}
		if len(cfg.Headers) > 0 {
			headers := make(map[string]string, len(cfg.Headers))
			for k, v := range cfg.Headers {
				rv, err := resolveEnvValue(cfg.Name, k, v, cipher)
				if err != nil {
					return nil, err
				}
				headers[k] = rv
			}
			resolved.Headers = headers
		}
		out[i] = resolved
	}
	return out, nil
}

// agentClientInfo is what every spawned MCP server sees as the
// connecting client identity. Stable name so operators reading
// upstream server logs can correlate.
func agentClientInfo() mcp.ClientInfo {
	return mcp.ClientInfo{Name: "hecate-agent-loop", Version: version.Version}
}

// AgentMCPClientCacheOptions bundles the knobs main.go threads into
// the cache. Avoids a 6-param positional constructor as the cache
// grows new options. Zero values fall back to the cache's internal
// defaults (see mcpclient.NewSharedClientCache for each field).
type AgentMCPClientCacheOptions struct {
	TTL          time.Duration
	MaxEntries   int
	PingInterval time.Duration
	PingTimeout  time.Duration
	Metrics      *telemetry.OrchestratorMetrics
}

// NewAgentMCPClientCache builds a SharedClientCache configured with
// the same client identity that uncached agent-loop runs use. main.go
// constructs one of these at startup, hands it to the api.Handler, and
// the handler wires it into the runner's MCP host factory. Letting
// orchestrator own the constructor keeps the agentClientInfo helper
// unexported and ensures the cache and the per-run path can never
// drift on identity strings.
//
// See AgentMCPClientCacheOptions for the knobs and their fallback
// behavior. metrics, when non-nil, gets wired in as a CacheObserver
// so cache hit/miss/evict events show up on the cache-events counter.
func NewAgentMCPClientCache(opts AgentMCPClientCacheOptions) *mcpclient.SharedClientCache {
	cache := mcpclient.NewSharedClientCacheWithOptions(mcpclient.SharedClientCacheOptions{
		TTL:          opts.TTL,
		MaxEntries:   opts.MaxEntries,
		PingInterval: opts.PingInterval,
		PingTimeout:  opts.PingTimeout,
		Info:         agentClientInfo(),
	})
	if opts.Metrics != nil {
		metrics := opts.Metrics
		// Capture metrics in closures so the cache stays free of any
		// telemetry-package dependency. The closures themselves are
		// nil-safe (the metrics SDK no-ops on nil instruments).
		cache.SetObserver(&mcpclient.CacheObserver{
			OnHit: func(server string) {
				metrics.RecordMCPCacheEvent(context.Background(), telemetry.MCPCacheEventRecord{
					Server: server, Event: telemetry.MCPCacheEventHit,
				})
			},
			OnMiss: func(server string) {
				metrics.RecordMCPCacheEvent(context.Background(), telemetry.MCPCacheEventRecord{
					Server: server, Event: telemetry.MCPCacheEventMiss,
				})
			},
			OnEvicted: func(server string) {
				metrics.RecordMCPCacheEvent(context.Background(), telemetry.MCPCacheEventRecord{
					Server: server, Event: telemetry.MCPCacheEventEvicted,
				})
			},
		})
	}
	return cache
}

// MCPProbeResult is the orchestrator-side return shape for
// ProbeMCPServer. ServerName / ServerVersion echo whatever the
// upstream reported on its initialize handshake — useful for
// confirming the operator pointed at the right server before they
// commit a config to a task. Tools is the upstream's tools/list
// catalog with un-namespaced names (the operator-chosen alias does
// the namespacing at task-spawn time).
type MCPProbeResult struct {
	ServerName            string
	ServerVersion         string
	Tools                 []mcpclient.NamespacedTool
	ResourceTemplates     []mcp.ResourceTemplate
	ResourceTemplateError string
}

// ProbeMCPServer brings up a single MCP server with cfg, calls
// tools/list, and tears it down. Returns the upstream's tool catalog
// without ever caching the client — this is a one-shot dry-run, the
// operator is testing a config before committing it to a task.
//
// The same secret-resolution path the agent loop uses runs first, so
// $VAR_NAME and enc:<base64> values resolve identically to a real
// task. cipher may be nil (mirrors the runtime contract: enc:-prefixed
// values without a cipher fail fast with a clear error rather than
// forwarding ciphertext).
//
// Bounded by ctx; the caller's deadline is the only timeout. A typical
// admin endpoint passes a 10s context so a stuck upstream surfaces as
// a clean error rather than wedging the request.
func ProbeMCPServer(ctx context.Context, cfg types.MCPServerConfig, cipher secrets.Cipher) (*MCPProbeResult, error) {
	return probeMCPServer(ctx, cfg, cipher, false)
}

// ProbeMCPServerWithResourceTemplates is the one-shot probe plus an
// operator-diagnostic resources/templates/list call. Keep it explicit so the
// general MCP dry-run probe remains a tools-only check.
func ProbeMCPServerWithResourceTemplates(ctx context.Context, cfg types.MCPServerConfig, cipher secrets.Cipher) (*MCPProbeResult, error) {
	return probeMCPServer(ctx, cfg, cipher, true)
}

func probeMCPServer(ctx context.Context, cfg types.MCPServerConfig, cipher secrets.Cipher, includeResourceTemplates bool) (*MCPProbeResult, error) {
	resolved, err := resolveEnvConfigs([]types.MCPServerConfig{cfg}, cipher)
	if err != nil {
		return nil, err
	}
	clientCfgs := toClientServerConfigs(resolved)
	pool, err := mcpclient.NewPool(ctx, agentClientInfo(), clientCfgs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = pool.Close() }()

	result := mcpProbeResultFromPoolTools(cfg.Name, pool.AllTools())
	if includeResourceTemplates {
		if templates, err := pool.ListResourceTemplates(ctx, cfg.Name); err != nil {
			result.ResourceTemplateError = err.Error()
		} else {
			result.ResourceTemplates = templates
		}
	}
	return result, nil
}

// ProbeCachedMCPServer is the cached-client counterpart to
// ProbeMCPServer. It validates/resolves cfg, acquires the upstream MCP
// process from cache, exposes the tools/list snapshot, then releases
// the client back to the cache instead of tearing it down. Use this
// for operator-controlled infrastructure clients where "connect and
// inspect" should leave a warm subprocess for future calls.
func ProbeCachedMCPServer(ctx context.Context, cfg types.MCPServerConfig, cipher secrets.Cipher, cache *mcpclient.SharedClientCache) (*MCPProbeResult, error) {
	return probeCachedMCPServer(ctx, cfg, cipher, cache, false)
}

// ProbeCachedMCPServerWithResourceTemplates is the cached-client counterpart to
// ProbeMCPServerWithResourceTemplates.
func ProbeCachedMCPServerWithResourceTemplates(ctx context.Context, cfg types.MCPServerConfig, cipher secrets.Cipher, cache *mcpclient.SharedClientCache) (*MCPProbeResult, error) {
	return probeCachedMCPServer(ctx, cfg, cipher, cache, true)
}

func probeCachedMCPServer(ctx context.Context, cfg types.MCPServerConfig, cipher secrets.Cipher, cache *mcpclient.SharedClientCache, includeResourceTemplates bool) (*MCPProbeResult, error) {
	if cache == nil {
		return nil, errors.New("mcp cached probe: cache is required")
	}
	resolved, err := resolveEnvConfigs([]types.MCPServerConfig{cfg}, cipher)
	if err != nil {
		return nil, err
	}
	clientCfgs := toClientServerConfigs(resolved)
	pool, err := mcpclient.NewPoolWithCache(ctx, clientCfgs, cache)
	if err != nil {
		return nil, err
	}
	defer func() { _ = pool.Close() }()

	result := mcpProbeResultFromPoolTools(cfg.Name, pool.AllTools())
	if includeResourceTemplates {
		if templates, err := pool.ListResourceTemplates(ctx, cfg.Name); err != nil {
			result.ResourceTemplateError = err.Error()
		} else {
			result.ResourceTemplates = templates
		}
	}
	return result, nil
}

// CachedMCPToolCallResult is the diagnostic shape returned by
// CallCachedMCPServerTool. It keeps the full MCP result for operator-facing
// smoke checks while preserving the flattened text fallback used by the agent
// loop.
type CachedMCPToolCallResult struct {
	ToolName string
	Text     string
	IsError  bool
	Result   mcp.CallToolResult
}

// CachedMCPResourceReadResult is the diagnostic shape returned by
// ReadCachedMCPServerResource. It proves a persistent MCP client can read
// concrete resources without wiring model-origin dispatch to that sidecar.
type CachedMCPResourceReadResult struct {
	URI    string
	Result mcp.ReadResourceResult
}

// CallCachedMCPServerTool invokes a single tool on one MCP server through a
// SharedClientCache. It is intentionally a narrow operator-diagnostic seam: the
// agent loop still uses AgentMCPHostFactory, while sidecar/readiness probes can
// prove a persistent client can do real work without wiring model-origin tool
// dispatch to that sidecar.
func CallCachedMCPServerTool(ctx context.Context, cfg types.MCPServerConfig, cipher secrets.Cipher, cache *mcpclient.SharedClientCache, toolName string, args json.RawMessage) (*CachedMCPToolCallResult, error) {
	if cache == nil {
		return nil, errors.New("cached mcp tool call requires a client cache")
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return nil, errors.New("cached mcp tool call requires a tool name")
	}
	resolved, err := resolveEnvConfigs([]types.MCPServerConfig{cfg}, cipher)
	if err != nil {
		return nil, err
	}
	clientCfgs := toClientServerConfigs(resolved)
	pool, err := mcpclient.NewPoolWithCache(ctx, clientCfgs, cache)
	if err != nil {
		return nil, err
	}
	defer func() { _ = pool.Close() }()
	result, err := pool.CallDetailed(ctx, mcpclient.NamespacedToolName(strings.TrimSpace(cfg.Name), toolName), args)
	if err != nil {
		return nil, err
	}
	return &CachedMCPToolCallResult{
		ToolName: toolName,
		Text:     result.Text,
		IsError:  result.IsError,
		Result:   result.Result,
	}, nil
}

// ReadCachedMCPServerResource reads a concrete resource from one MCP server
// through a SharedClientCache. It intentionally stays separate from the
// agent-loop host seam; resources are operator/client diagnostics, not LLM tool
// calls.
func ReadCachedMCPServerResource(ctx context.Context, cfg types.MCPServerConfig, cipher secrets.Cipher, cache *mcpclient.SharedClientCache, uri string) (*CachedMCPResourceReadResult, error) {
	if cache == nil {
		return nil, errors.New("cached mcp resource read requires a client cache")
	}
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return nil, errors.New("cached mcp resource read requires a resource uri")
	}
	resolved, err := resolveEnvConfigs([]types.MCPServerConfig{cfg}, cipher)
	if err != nil {
		return nil, err
	}
	clientCfgs := toClientServerConfigs(resolved)
	pool, err := mcpclient.NewPoolWithCache(ctx, clientCfgs, cache)
	if err != nil {
		return nil, err
	}
	defer func() { _ = pool.Close() }()
	result, err := pool.ReadResource(ctx, strings.TrimSpace(cfg.Name), uri)
	if err != nil {
		return nil, err
	}
	return &CachedMCPResourceReadResult{
		URI:    uri,
		Result: result,
	}, nil
}

func mcpProbeResultFromPoolTools(serverName string, tools []mcpclient.NamespacedTool) *MCPProbeResult {
	out := &MCPProbeResult{Tools: make([]mcpclient.NamespacedTool, 0, len(tools))}
	prefix := mcpclient.NamespacedToolName(strings.TrimSpace(serverName), "")
	for _, t := range tools {
		stripped := t
		// Strip the "mcp__<name>__" prefix to surface the upstream
		// tool name. Pool always builds names this way; if a future
		// change skips namespacing, this falls through cleanly via
		// HasPrefix.
		if strings.HasPrefix(t.Name, prefix) {
			stripped.Name = strings.TrimPrefix(t.Name, prefix)
		}
		out.Tools = append(out.Tools, stripped)
	}
	return out
}

// toClientServerConfigs converts the orchestrator-side config slice
// into the client package's shape. Duplicated representation by
// design: the client package owns its own types so it stays free of
// the orchestrator's tree.
func toClientServerConfigs(configs []types.MCPServerConfig) []mcpclient.ServerConfig {
	out := make([]mcpclient.ServerConfig, 0, len(configs))
	for _, c := range configs {
		out = append(out, mcpclient.ServerConfig{
			Name:    c.Name,
			Command: c.Command,
			Args:    c.Args,
			Env:     c.Env,
			URL:     c.URL,
			Headers: c.Headers,
		})
	}
	return out
}

// poolMCPHost adapts mcpclient.Pool to AgentMCPHost. The conversion
// from NamespacedTool to types.Tool happens here so the agent_loop
// gets a uniform tool catalog (built-ins + MCP) it can hand the LLM
// without any further marshaling.
type poolMCPHost struct {
	pool *mcpclient.Pool
}

func (h *poolMCPHost) Tools() []types.Tool {
	src := h.pool.Tools()
	out := make([]types.Tool, 0, len(src))
	for _, t := range src {
		// Schemas come straight from upstream MCP servers as JSON
		// Schema documents — the LLM's tool-call format expects the
		// same shape, so we forward verbatim. An empty schema becomes
		// a permissive `{"type":"object"}` so the LLM still sees a
		// well-formed tool descriptor.
		schema := t.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out = append(out, types.Tool{
			Type: "function",
			Function: types.ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  schema,
			},
		})
	}
	return out
}

func (h *poolMCPHost) Call(ctx context.Context, name string, args json.RawMessage) (string, bool, error) {
	return h.pool.Call(ctx, name, args)
}

func (h *poolMCPHost) CallDetailed(ctx context.Context, name string, args json.RawMessage) (mcpclient.ToolCallResult, error) {
	return h.pool.CallDetailed(ctx, name, args)
}

func (h *poolMCPHost) Close() error {
	return h.pool.Close()
}
