package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/telemetry"
)

type Config struct {
	Server    ServerConfig
	Router    RouterConfig
	Provider  ProviderConfig
	Chat      ChatConfig
	OTel      OTelConfig
	Governor  GovernorConfig
	Retention RetentionConfig
	// SQLite is the single-node durable tier. Subsystems opt in via
	// GATEWAY_*_BACKEND=sqlite; the client is opened lazily once any
	// subsystem actually needs it. See storage.SQLiteClient for the
	// pragmas applied per connection.
	SQLite    SQLiteConfig
	Providers ProvidersConfig
	LogLevel  string
}

type ServerConfig struct {
	Address                    string
	PublicURL                  string
	DataDir                    string
	BootstrapFile              string
	ControlPlaneBackend        string
	ControlPlaneKey            string
	ControlPlaneSecretKey      string
	TasksBackend               string
	TaskApprovalPolicies       []string
	TaskQueueBackend           string
	TaskQueueWorkers           int
	TaskQueueBuffer            int
	TaskQueueLeaseSeconds      int
	TaskMaxConcurrentPerTenant int
	// TaskReconcileInterval controls how often the periodic reconciler
	// scans for runs stuck in "running" past 3× the lease duration.
	// Default 30s. Set via GATEWAY_TASK_RECONCILE_INTERVAL (Go duration
	// string, e.g. "30s", "1m").
	TaskReconcileInterval time.Duration
	// TaskAgentLoopMaxTurns caps how many LLM round-trips an
	// agent_loop run can make. Acts as a runaway-cost safety net.
	// Default 8 (set in NewAgentLoopExecutor when zero).
	TaskAgentLoopMaxTurns int
	// TaskMaxMCPServersPerTask caps how many entries an agent_loop
	// task may declare under `mcp_servers`. Each entry produces one
	// MCP client (subprocess for stdio, persistent connection for
	// HTTP), so an unbounded list is a real-resource attack surface
	// — a single misconfigured task could exhaust file descriptors
	// or pin a tenant's worker on N initialize handshakes. The
	// gateway rejects creates that exceed this with a 400 carrying
	// a concrete diagnostic. Default 16 (set in NewHandler when
	// zero); 0/negative disables the check entirely (not recommended
	// outside test fixtures).
	TaskMaxMCPServersPerTask int
	// TaskMCPClientCacheMaxEntries caps how many distinct cached
	// upstream clients the SharedClientCache holds at once. When
	// inserting at-or-over this size, the cache evicts the
	// least-recently-used IDLE entry first; if every entry is
	// in-use it allows the over-cap insert (rejecting an Acquire
	// would break a legitimate run). Combined with TTL eviction,
	// this keeps a long-lived gateway from accumulating an
	// unbounded set of cached subprocesses across tasks. Default
	// 256 (set in NewAgentMCPClientCache when zero).
	TaskMCPClientCacheMaxEntries int
	// TaskMCPClientCachePingInterval is how often the cache's
	// proactive health-check loop pings each idle cached upstream.
	// Detects subprocesses that are alive but wedged (event-loop
	// deadlock, tight CPU loop) before the next real tool call
	// hits the wall — reactive eviction in Pool.Call only fires
	// after a call has already failed. Default 60s; 0 disables
	// the loop entirely (still leaves reactive eviction in place).
	TaskMCPClientCachePingInterval time.Duration
	// TaskMCPClientCachePingTimeout bounds each individual ping.
	// Failure or deadline-exceeded evicts the entry. Default 5s;
	// long enough for a healthy upstream to answer ping (an
	// empty-result round-trip), tight enough that a wedged
	// subprocess surfaces quickly.
	TaskMCPClientCachePingTimeout time.Duration
	// TaskAgentSystemPrompt is the global default system prompt
	// prepended to every agent_loop conversation. Sits at the
	// broadest layer of the four-level composition (global → tenant
	// → workspace CLAUDE.md/AGENTS.md → per-task). Empty disables
	// the global layer; tenant / workspace / task prompts still apply.
	TaskAgentSystemPrompt string
	// TaskHTTP* knobs govern the agent_loop `http_request` tool —
	// the only outbound-network surface the agent has by default.
	// Defaults are set conservatively: no private-IP access, 30s
	// timeout, 256 KiB response cap. Operators broaden via env.
	TaskHTTPTimeout          time.Duration
	TaskHTTPMaxResponseBytes int
	TaskHTTPAllowPrivateIPs  bool
	// TaskHTTPAllowedHosts, when non-empty, is the only set of hosts
	// the agent can reach. Empty = all public hosts allowed (still
	// blocks private IPs unless TaskHTTPAllowPrivateIPs is true).
	TaskHTTPAllowedHosts []string

	// TaskShell* knobs govern shell_exec network egress when
	// SandboxNetwork is true on the task. Mirrors the http_request
	// policy so a single allowlist can apply to both surfaces. When
	// SandboxNetwork is false (the default), shell network access is
	// rejected outright regardless of these knobs.
	TaskShellAllowPrivateIPs bool
	TaskShellAllowedHosts    []string

	// AgentAdapterApprovalMode controls how Hecate handles ACP
	// RequestPermission reverse-RPCs from external coding-agent
	// adapters (Codex, Claude Code, Cursor Agent). One of:
	//   - "prompt": ask the operator (default; safe).
	//   - "auto":   auto-approve everything. Danger mode kept for
	//               batch / CI / smoke. Logged at WARN on startup.
	//   - "deny":   auto-reject everything. Audit / compliance.
	// Set via GATEWAY_AGENT_ADAPTER_APPROVAL_MODE.
	//
	// Background: prior to this knob the gateway silently auto-approved
	// every adapter request. Default flips to "prompt" because the
	// External Agent Adapters subsystem is alpha; operators who depend
	// on the old behavior must opt into "auto" explicitly.
	//
	// See docs/rfcs/external-adapter-approvals-v1.md.
	AgentAdapterApprovalMode string
	// AgentAdapterApprovalTimeout is how long a pending approval waits
	// before resolving to ACP `Cancelled`. Default 5m. Set via
	// GATEWAY_AGENT_ADAPTER_APPROVAL_TIMEOUT (Go duration string).
	AgentAdapterApprovalTimeout time.Duration
	// ChatMaxTurnsPerSession caps how many user→assistant round-trips
	// a single agent-chat session may execute. 0 (default) means unlimited.
	// When the ceiling is reached, POST
	// /hecate/v1/chat/sessions/{id}/messages returns HTTP 422 with code
	// "chat.session_limit_exceeded".
	// Set via GATEWAY_CHAT_MAX_TURNS_PER_SESSION.
	ChatMaxTurnsPerSession int
	// ChatMaxSessionDuration caps wall-clock age for an agent-chat
	// session. 0 (default) means unlimited.
	// Set via GATEWAY_CHAT_MAX_SESSION_DURATION.
	ChatMaxSessionDuration time.Duration
	// ChatIdleTimeout auto-closes sessions that have not changed for this
	// long. 0 (default) disables the sweeper.
	// Set via GATEWAY_CHAT_IDLE_TIMEOUT.
	ChatIdleTimeout time.Duration

	// TraceBodyCapture enables recording (redacted) request and response bodies
	// in the distributed trace.  Off by default; enable via GATEWAY_TRACE_BODIES=true.
	TraceBodyCapture bool
	// TraceBodyMaxBytes caps each captured body at this many bytes (default 4096).
	TraceBodyMaxBytes int

	// RateLimit controls per-API-key request throttling.
	RateLimit RateLimitConfig
}

// RateLimitConfig configures the token-bucket rate limiter applied per API key.
type RateLimitConfig struct {
	// Enabled turns on per-key rate limiting.  Off by default.
	Enabled bool
	// RequestsPerMinute is the steady-state refill rate and the X-RateLimit-Limit
	// value (default 60).
	RequestsPerMinute int64
	// BurstSize is the maximum number of tokens that can accumulate (default equals
	// RequestsPerMinute).
	BurstSize int64
}

type RouterConfig struct {
	DefaultModel string
}

type ProviderConfig struct {
	MaxAttempts                    int
	RetryBackoff                   time.Duration
	FailoverEnabled                bool
	HealthThreshold                int
	HealthCooldown                 time.Duration
	HealthLatencyDegradedThreshold time.Duration
	HistoryBackend                 string
	HistoryLimit                   int
}

type ChatConfig struct {
	SessionsBackend string
}

type OTelSignalConfig struct {
	Enabled   bool
	Endpoint  string
	Headers   map[string]string
	Timeout   time.Duration
	Transport string
}

type OTelConfig struct {
	ServiceName           string
	ServiceVersion        string
	ServiceInstanceID     string
	DeploymentEnvironment string
	Endpoint              string
	Headers               map[string]string
	Timeout               time.Duration
	Transport             string
	Traces                OTelSignalConfig
	TracesSampler         string
	TracesSamplerArg      float64
	Metrics               OTelSignalConfig
	MetricsInterval       time.Duration
	MetricsExemplarFilter string
	Logs                  OTelSignalConfig
}

type GovernorConfig struct {
	DenyAll              bool
	MaxPromptTokens      int
	ModelRewriteTo       string
	PolicyRules          []PolicyRuleConfig `json:"policy_rules"`
	UsageBackend         string
	UsageKey             string
	UsageScope           string
	RouteMode            string
	AllowedProviders     []string
	DeniedProviders      []string
	AllowedModels        []string
	DeniedModels         []string
	AllowedProviderKinds []string
	UsageHistoryLimit    int
}

type PolicyRuleConfig struct {
	ID                     string   `json:"id"`
	Action                 string   `json:"action"`
	Reason                 string   `json:"reason"`
	Providers              []string `json:"providers"`
	ProviderKinds          []string `json:"provider_kinds"`
	Models                 []string `json:"models"`
	RouteReasons           []string `json:"route_reasons"`
	MinPromptTokens        int      `json:"min_prompt_tokens"`
	MinEstimatedCostMicros int64    `json:"min_estimated_cost_micros_usd"`
	RewriteModelTo         string   `json:"rewrite_model_to"`
}

type RetentionConfig struct {
	Enabled         bool
	HistoryBackend  string
	Interval        time.Duration
	TraceSnapshots  RetentionPolicy
	UsageEvents     RetentionPolicy
	AuditEvents     RetentionPolicy
	ProviderHistory RetentionPolicy
	// TurnEvents prunes `turn.completed` rows from the
	// task-run events table. Other event types (run.started/finished,
	// approval.*) are kept for forensics.
	TurnEvents RetentionPolicy
	// ChatApprovals prunes resolved (non-pending) external-adapter
	// approval rows. Pending rows are never auto-pruned — they're caller
	// state, not history. Grants are not subject to MaxAge / MaxCount;
	// only their own ExpiresAt drives deletion (operator-authored intent
	// outlives normal retention windows).
	ChatApprovals RetentionPolicy
}

type RetentionPolicy struct {
	MaxAge   time.Duration
	MaxCount int
}

type SQLiteConfig struct {
	// Path is the on-disk file. Defaults to .data/hecate.db so a fresh
	// `just dev` plus `GATEWAY_*_BACKEND=sqlite` Just Works without
	// extra mkdir or env. Parent directories are auto-created.
	Path        string
	TablePrefix string
	BusyTimeout time.Duration
}

type ProvidersConfig struct {
	OpenAICompatible []OpenAICompatibleProviderConfig
	// AnthropicCacheDisabled is the gateway-wide value of
	// GATEWAY_PROVIDER_ANTHROPIC_CACHE_ENABLED (inverted). It applies
	// to every Anthropic-protocol provider regardless of how the
	// provider was added (env, control-plane UI, programmatic) — the
	// runtime manager stamps it onto resolved provider configs at
	// reload time. The per-config field on
	// OpenAICompatibleProviderConfig still exists as a propagation
	// slot the AnthropicProvider reads, but its source of truth is
	// this global value, not the per-config record.
	AnthropicCacheDisabled bool
}

type OpenAICompatibleProviderConfig struct {
	Name         string        `json:"name"`
	Kind         string        `json:"kind"`
	Protocol     string        `json:"protocol"`
	BaseURL      string        `json:"base_url"`
	APIKey       string        `json:"api_key"`
	APIVersion   string        `json:"api_version"`
	ChatPath     string        `json:"chat_path,omitempty"`
	ModelsPath   string        `json:"models_path,omitempty"`
	Timeout      time.Duration `json:"timeout"`
	StubMode     bool          `json:"stub_mode"`
	StubResponse string        `json:"stub_response"`
	DefaultModel string        `json:"default_model"`
	Enabled      bool          `json:"enabled"`
	// KnownModels is the curated catalog from the built-in preset. It populates the
	// static capabilities when no API key is set and live discovery is skipped.
	KnownModels []string `json:"known_models,omitempty"`
	// AnthropicCacheDisabled opts the Anthropic adapter out of
	// auto-attaching `cache_control: {"type":"ephemeral"}` markers on
	// the last `system` block and the last `tools` entry of outbound
	// Messages-API requests. Auto-marking is on by default — cache
	// hits cut input-token cost ~90% on the static prefix of long
	// agent_loop / chat sessions with no latency penalty, so the
	// safe default is to leave it on and let operators flip the
	// global GATEWAY_PROVIDER_ANTHROPIC_CACHE_ENABLED=false toggle
	// for cost-tier comparisons or debugging. The env loader
	// inverts that toggle into this field so CP-stored provider
	// records (zero-valued bool) and direct test constructions also
	// default to caching-on without each call site remembering to
	// opt in. Non-Anthropic adapters ignore the field — keeping it
	// on the shared provider config (instead of a separate
	// Anthropic-only struct) avoids reshaping the runtime-manager
	// dispatch boundary for one bool.
	AnthropicCacheDisabled bool `json:"anthropic_cache_disabled,omitempty"`
}

func LoadFromEnv() Config {
	providersCfg := loadProvidersFromEnv()
	return Config{
		Server: ServerConfig{
			Address: getEnv("GATEWAY_ADDRESS", "127.0.0.1:8765"),
			// PublicURL is written to hecate.runtime.json so local helper
			// processes such as hecate-acp can discover the externally
			// reachable gateway URL. Empty means derive from Address.
			PublicURL: getEnv("GATEWAY_PUBLIC_URL", ""),
			// Default `.data/` keeps the auto-generated bootstrap file
			// (AES-GCM key for persisted provider secrets) out of the repo root so a stray
			// `git add .` can't sweep it up. Docker overrides this to /data
			// via the Dockerfile.
			DataDir:       getEnv("GATEWAY_DATA_DIR", ".data"),
			BootstrapFile: getEnv("GATEWAY_BOOTSTRAP_FILE", ""),
			// Default is "memory" to match every other backend selector
			// (chat sessions, tasks, cache, …).
			ControlPlaneBackend:            getEnv("GATEWAY_CONTROL_PLANE_BACKEND", "memory"),
			ControlPlaneKey:                getEnv("GATEWAY_CONTROL_PLANE_KEY", "control-plane"),
			ControlPlaneSecretKey:          getEnv("GATEWAY_CONTROL_PLANE_SECRET_KEY", ""),
			TasksBackend:                   getEnv("GATEWAY_TASKS_BACKEND", "memory"),
			TaskApprovalPolicies:           splitCSV(getEnvApprovalPolicies()),
			TaskQueueBackend:               getEnv("GATEWAY_TASK_QUEUE_BACKEND", "memory"),
			TaskQueueWorkers:               getEnvInt("GATEWAY_TASK_QUEUE_WORKERS", 1),
			TaskQueueBuffer:                getEnvInt("GATEWAY_TASK_QUEUE_BUFFER", 128),
			TaskQueueLeaseSeconds:          getEnvInt("GATEWAY_TASK_QUEUE_LEASE_SECONDS", 30),
			TaskReconcileInterval:          getEnvDuration("GATEWAY_TASK_RECONCILE_INTERVAL", 30*time.Second),
			TaskAgentLoopMaxTurns:          getEnvInt("GATEWAY_TASK_AGENT_LOOP_MAX_TURNS", 8),
			TaskMaxMCPServersPerTask:       getEnvInt("GATEWAY_TASK_MAX_MCP_SERVERS_PER_TASK", 16),
			TaskMCPClientCacheMaxEntries:   getEnvInt("GATEWAY_TASK_MCP_CLIENT_CACHE_MAX_ENTRIES", 256),
			TaskMCPClientCachePingInterval: getEnvDuration("GATEWAY_TASK_MCP_CLIENT_CACHE_PING_INTERVAL", 60*time.Second),
			TaskMCPClientCachePingTimeout:  getEnvDuration("GATEWAY_TASK_MCP_CLIENT_CACHE_PING_TIMEOUT", 5*time.Second),
			TaskAgentSystemPrompt:          getEnv("GATEWAY_TASK_AGENT_SYSTEM_PROMPT", ""),
			TaskHTTPTimeout:                getEnvDuration("GATEWAY_TASK_HTTP_TIMEOUT", 30*time.Second),
			TaskHTTPMaxResponseBytes:       getEnvInt("GATEWAY_TASK_HTTP_MAX_RESPONSE_BYTES", 256*1024),
			TaskHTTPAllowPrivateIPs:        getEnvBool("GATEWAY_TASK_HTTP_ALLOW_PRIVATE_IPS", false),
			TaskHTTPAllowedHosts:           splitCSV(getEnv("GATEWAY_TASK_HTTP_ALLOWED_HOSTS", "")),
			TaskShellAllowPrivateIPs:       getEnvBool("GATEWAY_TASK_SHELL_ALLOW_PRIVATE_IPS", false),
			TaskShellAllowedHosts:          splitCSV(getEnv("GATEWAY_TASK_SHELL_ALLOWED_HOSTS", "")),
			AgentAdapterApprovalMode:       getEnv("GATEWAY_AGENT_ADAPTER_APPROVAL_MODE", "prompt"),
			AgentAdapterApprovalTimeout:    getEnvDuration("GATEWAY_AGENT_ADAPTER_APPROVAL_TIMEOUT", 5*time.Minute),
			ChatMaxTurnsPerSession:         getEnvInt("GATEWAY_CHAT_MAX_TURNS_PER_SESSION", 0),
			ChatMaxSessionDuration:         getEnvDuration("GATEWAY_CHAT_MAX_SESSION_DURATION", 0),
			ChatIdleTimeout:                getEnvDuration("GATEWAY_CHAT_IDLE_TIMEOUT", 0),
			TaskMaxConcurrentPerTenant:     getEnvInt("GATEWAY_TASK_MAX_CONCURRENT_PER_TENANT", 0),
			TraceBodyCapture:               getEnvBool("GATEWAY_TRACE_BODIES", false),
			TraceBodyMaxBytes:              getEnvInt("GATEWAY_TRACE_BODY_MAX_BYTES", 4096),
			RateLimit: RateLimitConfig{
				Enabled:           getEnvBool("GATEWAY_RATE_LIMIT_ENABLED", false),
				RequestsPerMinute: getEnvInt64("GATEWAY_RATE_LIMIT_RPM", 60),
				BurstSize:         getEnvInt64("GATEWAY_RATE_LIMIT_BURST", 0), // 0 = same as RPM
			},
		},
		Router: RouterConfig{
			DefaultModel: getEnv("GATEWAY_DEFAULT_MODEL", "gpt-5.4-mini"),
		},
		Provider: ProviderConfig{
			MaxAttempts:                    getEnvInt("GATEWAY_PROVIDER_MAX_ATTEMPTS", 2),
			RetryBackoff:                   getEnvDuration("GATEWAY_PROVIDER_RETRY_BACKOFF", 200*time.Millisecond),
			FailoverEnabled:                getEnvBool("GATEWAY_PROVIDER_FAILOVER_ENABLED", true),
			HealthThreshold:                getEnvInt("GATEWAY_PROVIDER_HEALTH_FAILURE_THRESHOLD", 3),
			HealthCooldown:                 getEnvDuration("GATEWAY_PROVIDER_HEALTH_COOLDOWN", 30*time.Second),
			HealthLatencyDegradedThreshold: getEnvDuration("GATEWAY_PROVIDER_HEALTH_LATENCY_DEGRADED_THRESHOLD", 0),
			HistoryBackend:                 getEnv("GATEWAY_PROVIDER_HISTORY_BACKEND", "memory"),
			HistoryLimit:                   getEnvInt("GATEWAY_PROVIDER_HISTORY_LIMIT", 100),
		},
		Chat: ChatConfig{
			SessionsBackend: getEnv("GATEWAY_CHAT_SESSIONS_BACKEND", "memory"),
		},
		OTel: loadOTelFromEnv(),
		Governor: GovernorConfig{
			DenyAll:              getEnvBool("GATEWAY_DENY_ALL", false),
			MaxPromptTokens:      getEnvInt("GATEWAY_MAX_PROMPT_TOKENS", 64_000),
			ModelRewriteTo:       getEnv("GATEWAY_MODEL_REWRITE_TO", ""),
			UsageBackend:         getEnv("GATEWAY_USAGE_BACKEND", "memory"),
			UsageKey:             getEnv("GATEWAY_USAGE_KEY", "global"),
			UsageScope:           getEnv("GATEWAY_USAGE_SCOPE", "global"),
			RouteMode:            getEnv("GATEWAY_ROUTE_MODE", "any"),
			AllowedProviders:     splitCSV(getEnv("GATEWAY_ALLOWED_PROVIDERS", "")),
			DeniedProviders:      splitCSV(getEnv("GATEWAY_DENIED_PROVIDERS", "")),
			AllowedModels:        splitCSV(getEnv("GATEWAY_ALLOWED_MODELS", "")),
			DeniedModels:         splitCSV(getEnv("GATEWAY_DENIED_MODELS", "")),
			AllowedProviderKinds: splitCSV(getEnv("GATEWAY_ALLOWED_PROVIDER_KINDS", "")),
			UsageHistoryLimit:    getEnvInt("GATEWAY_USAGE_HISTORY_LIMIT", 20),
		},
		Retention: RetentionConfig{
			Enabled:         getEnvBool("GATEWAY_RETENTION_ENABLED", false),
			HistoryBackend:  getEnv("GATEWAY_RETENTION_HISTORY_BACKEND", "memory"),
			Interval:        getEnvDuration("GATEWAY_RETENTION_INTERVAL", 15*time.Minute),
			TraceSnapshots:  loadRetentionPolicyFromEnv("GATEWAY_RETENTION_TRACES_", 24*time.Hour, 2000),
			UsageEvents:     loadRetentionPolicyFromEnv("GATEWAY_RETENTION_USAGE_EVENTS_", 30*24*time.Hour, 200),
			AuditEvents:     loadRetentionPolicyFromEnv("GATEWAY_RETENTION_AUDIT_EVENTS_", 30*24*time.Hour, 500),
			ProviderHistory: loadRetentionPolicyFromEnv("GATEWAY_RETENTION_PROVIDER_HISTORY_", 7*24*time.Hour, 10_000),
			// turn.completed events accumulate fast on long
			// agent runs (one per LLM round-trip). 7d/100k is a
			// generous default; tune down on busy installs.
			TurnEvents: loadRetentionPolicyFromEnv("GATEWAY_RETENTION_TURN_EVENTS_", 7*24*time.Hour, 100_000),
			// External-adapter approval history. Only resolved rows
			// are pruned; pending rows stay until startup reconcile
			// flips them. Default 30d/10k mirrors task_approvals.
			ChatApprovals: loadRetentionPolicyFromEnv("GATEWAY_RETENTION_CHAT_APPROVALS_", 30*24*time.Hour, 10_000),
		},
		SQLite: SQLiteConfig{
			Path:        getEnv("GATEWAY_SQLITE_PATH", ".data/hecate.db"),
			TablePrefix: getEnv("GATEWAY_SQLITE_TABLE_PREFIX", "hecate"),
			BusyTimeout: getEnvDuration("GATEWAY_SQLITE_BUSY_TIMEOUT", 5*time.Second),
		},
		Providers: providersCfg,
		LogLevel:  getEnv("LOG_LEVEL", "INFO"),
	}
}

// Validate checks configuration combinations that would otherwise fail later
// with confusing store/provider errors. It is intentionally strict for alpha:
// unsupported backend names, impossible retention policies, and missing shared
// connection strings fail startup with one clear diagnostic.
func (c Config) Validate() error {
	var errs []error

	validateBackend := func(label, value string, allowed ...string) {
		value = strings.TrimSpace(value)
		for _, item := range allowed {
			if value == item {
				return
			}
		}
		errs = append(errs, fmt.Errorf("%s must be one of %s (got %q)", label, strings.Join(allowed, ", "), value))
	}

	validateBackend("GATEWAY_CONTROL_PLANE_BACKEND", c.Server.ControlPlaneBackend, "memory", "sqlite")
	validateBackend("GATEWAY_TASKS_BACKEND", c.Server.TasksBackend, "memory", "sqlite")
	validateBackend("GATEWAY_TASK_QUEUE_BACKEND", c.Server.TaskQueueBackend, "memory", "sqlite")
	validateBackend("GATEWAY_CHAT_SESSIONS_BACKEND", c.Chat.SessionsBackend, "memory", "sqlite")
	validateBackend("GATEWAY_USAGE_BACKEND", c.Governor.UsageBackend, "memory", "sqlite")
	validateBackend("GATEWAY_RETENTION_HISTORY_BACKEND", c.Retention.HistoryBackend, "memory", "sqlite")
	validateBackend("GATEWAY_PROVIDER_HISTORY_BACKEND", c.Provider.HistoryBackend, "memory", "sqlite")
	if publicURL := strings.TrimSpace(c.Server.PublicURL); publicURL != "" {
		u, err := url.ParseRequestURI(publicURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			errs = append(errs, fmt.Errorf("GATEWAY_PUBLIC_URL must be an absolute http(s) URL (got %q)", publicURL))
		}
	}

	validPolicies := map[string]struct{}{
		"shell_exec": {}, "git_exec": {}, "file_write": {},
		"network_egress": {}, "read_file": {}, "all_tools": {},
	}
	for _, p := range c.Server.TaskApprovalPolicies {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := validPolicies[p]; !ok {
			errs = append(errs, fmt.Errorf("GATEWAY_TASK_APPROVAL_POLICIES: unknown policy name %q (valid: shell_exec, git_exec, file_write, network_egress, read_file, all_tools)", p))
		}
	}

	if c.Retention.Enabled && c.Retention.Interval <= 0 {
		errs = append(errs, errors.New("GATEWAY_RETENTION_INTERVAL must be positive when retention is enabled"))
	}
	for label, policy := range map[string]RetentionPolicy{
		"GATEWAY_RETENTION_TRACES":           c.Retention.TraceSnapshots,
		"GATEWAY_RETENTION_USAGE_EVENTS":     c.Retention.UsageEvents,
		"GATEWAY_RETENTION_AUDIT_EVENTS":     c.Retention.AuditEvents,
		"GATEWAY_RETENTION_PROVIDER_HISTORY": c.Retention.ProviderHistory,
		"GATEWAY_RETENTION_TURN_EVENTS":      c.Retention.TurnEvents,
	} {
		if policy.MaxAge < 0 {
			errs = append(errs, fmt.Errorf("%s_MAX_AGE must be zero or positive", label))
		}
		if policy.MaxCount < 0 {
			errs = append(errs, fmt.Errorf("%s_MAX_COUNT must be zero or positive", label))
		}
	}
	if c.Provider.MaxAttempts <= 0 {
		errs = append(errs, errors.New("GATEWAY_PROVIDER_MAX_ATTEMPTS must be positive"))
	}
	if c.Provider.HealthThreshold < 0 {
		errs = append(errs, errors.New("GATEWAY_PROVIDER_HEALTH_FAILURE_THRESHOLD must be zero or positive"))
	}
	if c.Provider.HealthLatencyDegradedThreshold < 0 {
		errs = append(errs, errors.New("GATEWAY_PROVIDER_HEALTH_LATENCY_DEGRADED_THRESHOLD must be zero or positive"))
	}
	if c.Provider.HistoryLimit < 0 {
		errs = append(errs, errors.New("GATEWAY_PROVIDER_HISTORY_LIMIT must be zero or positive"))
	}
	if c.Server.TaskQueueWorkers <= 0 {
		errs = append(errs, errors.New("GATEWAY_TASK_QUEUE_WORKERS must be positive"))
	}
	if c.Server.TaskQueueBuffer < 0 {
		errs = append(errs, errors.New("GATEWAY_TASK_QUEUE_BUFFER must be zero or positive"))
	}
	if c.Server.ChatMaxTurnsPerSession < 0 {
		errs = append(errs, errors.New("GATEWAY_CHAT_MAX_TURNS_PER_SESSION must be zero or positive"))
	}
	if c.Server.ChatMaxSessionDuration < 0 {
		errs = append(errs, errors.New("GATEWAY_CHAT_MAX_SESSION_DURATION must be zero or positive"))
	}
	if c.Server.ChatIdleTimeout < 0 {
		errs = append(errs, errors.New("GATEWAY_CHAT_IDLE_TIMEOUT must be zero or positive"))
	}
	if c.Server.RateLimit.Enabled && c.Server.RateLimit.RequestsPerMinute <= 0 {
		errs = append(errs, errors.New("GATEWAY_RATE_LIMIT_RPM must be positive when rate limiting is enabled"))
	}
	if c.Server.RateLimit.BurstSize < 0 {
		errs = append(errs, errors.New("GATEWAY_RATE_LIMIT_BURST must be zero or positive"))
	}
	for _, item := range durationEnvKeys() {
		if raw := strings.TrimSpace(os.Getenv(item)); raw != "" {
			if _, err := time.ParseDuration(raw); err != nil {
				errs = append(errs, fmt.Errorf("%s must be a valid Go duration: %w", item, err))
			}
		}
	}
	for _, item := range []struct {
		key   string
		value string
	}{
		{"GATEWAY_OTEL_TRANSPORT", c.OTel.Transport},
		{"GATEWAY_OTEL_TRACES_TRANSPORT", c.OTel.Traces.Transport},
		{"GATEWAY_OTEL_METRICS_TRANSPORT", c.OTel.Metrics.Transport},
		{"GATEWAY_OTEL_LOGS_TRANSPORT", c.OTel.Logs.Transport},
	} {
		switch item.value {
		case "", "http", "grpc":
		default:
			errs = append(errs, fmt.Errorf("%s must be one of http or grpc", item.key))
		}
	}
	switch strings.ToLower(strings.TrimSpace(c.OTel.MetricsExemplarFilter)) {
	case "", "trace_based", "tracebased", "sampled", "always_on", "alwayson", "always_off", "alwaysoff":
	default:
		errs = append(errs, fmt.Errorf("GATEWAY_OTEL_METRICS_EXEMPLAR_FILTER must be one of trace_based, always_on, or always_off"))
	}

	return errors.Join(errs...)
}

func durationEnvKeys() []string {
	return []string{
		"GATEWAY_TASK_MCP_CLIENT_CACHE_PING_INTERVAL",
		"GATEWAY_TASK_MCP_CLIENT_CACHE_PING_TIMEOUT",
		"GATEWAY_TASK_HTTP_TIMEOUT",
		"GATEWAY_PROVIDER_RETRY_BACKOFF",
		"GATEWAY_PROVIDER_HEALTH_COOLDOWN",
		"GATEWAY_PROVIDER_HEALTH_LATENCY_DEGRADED_THRESHOLD",
		"GATEWAY_OTEL_TIMEOUT",
		"GATEWAY_OTEL_TRACES_TIMEOUT",
		"GATEWAY_OTEL_METRICS_TIMEOUT",
		"GATEWAY_OTEL_LOGS_TIMEOUT",
		"GATEWAY_OTEL_METRICS_INTERVAL",
		"GATEWAY_RETENTION_INTERVAL",
		"GATEWAY_RETENTION_TRACES_MAX_AGE",
		"GATEWAY_RETENTION_USAGE_EVENTS_MAX_AGE",
		"GATEWAY_RETENTION_AUDIT_EVENTS_MAX_AGE",
		"GATEWAY_RETENTION_PROVIDER_HISTORY_MAX_AGE",
		"GATEWAY_RETENTION_TURN_EVENTS_MAX_AGE",
		"GATEWAY_CHAT_MAX_SESSION_DURATION",
		"GATEWAY_CHAT_IDLE_TIMEOUT",
		"GATEWAY_SQLITE_BUSY_TIMEOUT",
	}
}

func loadOTelFromEnv() OTelConfig {
	sharedEndpoint := strings.TrimSpace(getEnv("GATEWAY_OTEL_ENDPOINT", ""))
	sharedHeaders := parseEnvMap(getEnv("GATEWAY_OTEL_HEADERS", ""))
	sharedTimeout := getEnvDuration("GATEWAY_OTEL_TIMEOUT", 5*time.Second)
	sharedTransport := normalizeOTelTransport(getEnv("GATEWAY_OTEL_TRANSPORT", "http"))

	traces := loadOTelSignalFromEnv(
		"TRACES",
		sharedEndpoint,
		"traces",
		sharedHeaders,
		sharedTimeout,
		sharedTransport,
	)
	metrics := loadOTelSignalFromEnv(
		"METRICS",
		sharedEndpoint,
		"metrics",
		sharedHeaders,
		sharedTimeout,
		sharedTransport,
	)
	logs := loadOTelSignalFromEnv(
		"LOGS",
		sharedEndpoint,
		"logs",
		sharedHeaders,
		sharedTimeout,
		sharedTransport,
	)
	if logs.Endpoint == "" && traces.Endpoint != "" {
		logs.Endpoint = traces.Endpoint
		logs.Transport = traces.Transport
		if len(logs.Headers) == 0 {
			logs.Headers = cloneStringMap(traces.Headers)
		}
	}

	return OTelConfig{
		ServiceName:           getEnv("GATEWAY_OTEL_SERVICE_NAME", "hecate-gateway"),
		ServiceVersion:        getEnv("GATEWAY_OTEL_SERVICE_VERSION", ""),
		ServiceInstanceID:     getEnv("GATEWAY_OTEL_SERVICE_INSTANCE_ID", ""),
		DeploymentEnvironment: getEnv("GATEWAY_OTEL_DEPLOYMENT_ENVIRONMENT", ""),
		Endpoint:              sharedEndpoint,
		Headers:               cloneStringMap(sharedHeaders),
		Timeout:               sharedTimeout,
		Transport:             sharedTransport,
		MetricsInterval:       getEnvDuration("GATEWAY_OTEL_METRICS_INTERVAL", 30*time.Second),
		MetricsExemplarFilter: getEnv("GATEWAY_OTEL_METRICS_EXEMPLAR_FILTER", ""),
		TracesSampler:         getEnv("GATEWAY_OTEL_TRACES_SAMPLER", ""),
		TracesSamplerArg:      getEnvFloat64("GATEWAY_OTEL_TRACES_SAMPLER_ARG", 1.0),
		Traces:                traces,
		Metrics:               metrics,
		Logs:                  logs,
	}
}

func loadOTelSignalFromEnv(signal, sharedEndpoint, httpPath string, sharedHeaders map[string]string, sharedTimeout time.Duration, sharedTransport string) OTelSignalConfig {
	prefix := "GATEWAY_OTEL_" + signal + "_"
	transport := normalizeOTelTransport(getEnv(prefix+"TRANSPORT", sharedTransport))
	endpoint := strings.TrimSpace(getEnv(prefix+"ENDPOINT", ""))
	if endpoint == "" && sharedEndpoint != "" {
		endpoint = deriveOTelSignalEndpoint(sharedEndpoint, transport, httpPath)
	}
	headers := parseEnvMap(getEnv(prefix+"HEADERS", ""))
	if len(headers) == 0 {
		headers = cloneStringMap(sharedHeaders)
	}
	return OTelSignalConfig{
		Enabled:   getEnvBool(prefix+"ENABLED", false),
		Endpoint:  endpoint,
		Headers:   headers,
		Timeout:   getEnvDuration(prefix+"TIMEOUT", sharedTimeout),
		Transport: transport,
	}
}

func deriveOTelSignalEndpoint(endpoint, transport, httpPath string) string {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return ""
	}
	if normalizeOTelTransport(transport) == "grpc" {
		return endpoint
	}
	if strings.HasSuffix(endpoint, "/v1/"+httpPath) {
		return endpoint
	}
	return endpoint + "/v1/" + httpPath
}

func normalizeOTelTransport(value string) string {
	return telemetry.NormalizeOTLPTransport(value)
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func loadRetentionPolicyFromEnv(prefix string, defaultAge time.Duration, defaultCount int) RetentionPolicy {
	return RetentionPolicy{
		MaxAge:   getEnvDuration(prefix+"MAX_AGE", defaultAge),
		MaxCount: getEnvInt(prefix+"MAX_COUNT", defaultCount),
	}
}

// anthropicCacheDisabledFromEnv reads the global toggle that controls
// whether the Anthropic adapter auto-attaches prompt-cache markers on
// outbound system + tools sections. Default is enabled — caching is
// the safe, cheaper option; operators flip
// GATEWAY_PROVIDER_ANTHROPIC_CACHE_ENABLED=false only for cost-tier
// comparisons or to debug a suspected cache-related issue. Returns
// the inverted (`disabled`) form so the zero-value bool stamped on
// every provider config means "caching on" — CP-stored records and
// direct test constructions then inherit the safe default without
// each call site remembering to opt in.
func anthropicCacheDisabledFromEnv() bool {
	return !getEnvBool("GATEWAY_PROVIDER_ANTHROPIC_CACHE_ENABLED", true)
}

func loadProvidersFromEnv() ProvidersConfig {
	// Only register providers that have at least one explicit env var
	// (PROVIDER_<NAME>_BASE_URL / _API_KEY / _DEFAULT_MODEL / etc.).
	// Previously this seeded the list with every built-in preset and
	// relied on providerConfigFromEnv falling back to the catalog's
	// base_url to keep them — which auto-registered all 12 presets at
	// startup, polluting the runtime registry with stub entries that
	// surfaced as route candidates marked "unsupported_model" on every
	// request. The CP-store add flow is the source of truth for
	// "configured providers"; env vars are a deployment-time bootstrap
	// for the same record. No env var → no registration.
	names := deriveProviderNamesFromEnv()
	items := make([]OpenAICompatibleProviderConfig, 0, len(names))
	cacheDisabled := anthropicCacheDisabledFromEnv()
	for _, name := range names {
		cfg, ok := providerConfigFromEnv(name)
		if !ok {
			continue
		}
		cfg.AnthropicCacheDisabled = cacheDisabled
		items = append(items, cfg)
	}
	normalizeProviders(items)
	return ProvidersConfig{
		OpenAICompatible:       items,
		AnthropicCacheDisabled: cacheDisabled,
	}
}

func deriveProviderNamesFromEnv() []string {
	const prefix = "PROVIDER_"

	order := make([]string, 0, 4)
	seen := make(map[string]struct{})
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(key, prefix) {
			continue
		}
		name, ok := providerNameFromEnvKey(key)
		if !ok {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		order = append(order, name)
	}
	return order
}

func providerNameFromEnvKey(key string) (string, bool) {
	const prefix = "PROVIDER_"

	if !strings.HasPrefix(key, prefix) {
		return "", false
	}
	nameAndField := strings.TrimPrefix(key, prefix)
	for _, suffix := range []string{"_API_KEY", "_BASE_URL", "_DEFAULT_MODEL"} {
		if strings.HasSuffix(nameAndField, suffix) {
			name := strings.TrimSuffix(nameAndField, suffix)
			name = strings.ToLower(name)
			name = strings.TrimSpace(name)
			if name == "" {
				return "", false
			}
			return name, true
		}
	}
	return "", false
}

func providerConfigFromEnv(name string) (OpenAICompatibleProviderConfig, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return OpenAICompatibleProviderConfig{}, false
	}

	cfg := providerDefaults(name, getEnv("GATEWAY_DEFAULT_MODEL", "gpt-5.4-mini"))
	prefixes := []string{providerEnvPrefix(name)}
	for _, prefix := range prefixes {
		cfg.Kind = getEnv(prefix+"KIND", cfg.Kind)
		cfg.BaseURL = getEnv(prefix+"BASE_URL", cfg.BaseURL)
		cfg.APIKey = getEnv(prefix+"API_KEY", cfg.APIKey)
		cfg.StubMode = getEnvBool(prefix+"STUB_MODE", cfg.StubMode)
		cfg.StubResponse = getEnv(prefix+"STUB_RESPONSE", cfg.StubResponse)
		cfg.DefaultModel = getEnv(prefix+"DEFAULT_MODEL", cfg.DefaultModel)
	}

	// Auto-registration is gated on an explicit opt-in flag. Other
	// PROVIDER_<NAME>_* env vars (BASE_URL, API_KEY, …) are deployment
	// hints — they don't pre-add the provider to the runtime registry
	// on their own. Operators who want a provider live from first boot
	// set PROVIDER_<NAME>_PRECONFIGURED=1 alongside the rest. Otherwise
	// the provider is only configured when added explicitly via the UI
	// or POST /hecate/v1/settings/providers.
	if !getEnvBool(providerEnvPrefix(name)+"PRECONFIGURED", false) {
		return OpenAICompatibleProviderConfig{}, false
	}

	if strings.TrimSpace(cfg.BaseURL) == "" {
		return OpenAICompatibleProviderConfig{}, false
	}
	return cfg, true
}

func providerDefaults(name, globalDefaultModel string) OpenAICompatibleProviderConfig {
	if builtIn, ok := BuiltInProviderByID(name); ok {
		return builtIn.RuntimeConfig(globalDefaultModel)
	}
	return OpenAICompatibleProviderConfig{
		Name:         strings.ToLower(strings.TrimSpace(name)),
		Kind:         "cloud",
		Protocol:     "openai",
		Timeout:      DefaultProviderTimeout("cloud"),
		StubMode:     false,
		StubResponse: "Stubbed response from the AI Agent Runtime MVP.",
	}
}

func providerEnvPrefix(name string) string {
	normalized := strings.ToUpper(strings.TrimSpace(name))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, ".", "_")
	return "PROVIDER_" + normalized + "_"
}

func normalizeProviders(items []OpenAICompatibleProviderConfig) {
	for i := range items {
		if items[i].Name == "" {
			items[i].Name = "provider"
		}
		if items[i].Kind == "" {
			items[i].Kind = "cloud"
		}
		if items[i].Protocol == "" {
			items[i].Protocol = "openai"
		}
		if items[i].Timeout == 0 {
			// At this point Kind has been normalized above ("" → "cloud"),
			// so DefaultProviderTimeout sees a non-empty kind.
			items[i].Timeout = DefaultProviderTimeout(items[i].Kind)
		}
		if items[i].StubResponse == "" {
			items[i].StubResponse = "Stubbed response from the AI Agent Runtime MVP."
		}
	}
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// getEnvApprovalPolicies reads GATEWAY_TASK_APPROVAL_POLICIES and honours an
// explicitly empty value (KEY=) as "no policies". os.Getenv cannot distinguish
// "not set" from "set to empty"; os.LookupEnv can. An empty value is the
// documented opt-out for fully-trusted environments, so we must not fall back
// to the default in that case.
func getEnvApprovalPolicies() string {
	const key = "GATEWAY_TASK_APPROVAL_POLICIES"
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return "shell_exec,git_exec,file_write"
}

func getEnvBool(key string, fallback bool) bool {
	if value := os.Getenv(key); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		parsed, err := strconv.Atoi(value)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	if value := os.Getenv(key); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		parsed, err := time.ParseDuration(value)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func getEnvFloat64(key string, fallback float64) float64 {
	if value := os.Getenv(key); value != "" {
		parsed, err := strconv.ParseFloat(value, 64)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func splitCSV(value string) []string {
	if value == "" {
		return nil
	}
	return normalizeValues(strings.Split(value, ","))
}

func normalizeValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func parseEnvCSVInts(value string) []int {
	parts := splitCSV(value)
	if len(parts) == 0 {
		return nil
	}

	out := make([]int, 0, len(parts))
	for _, part := range parts {
		item, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		out = append(out, item)
	}
	return out
}

func parseEnvMap(raw string) map[string]string {
	items := splitCSV(raw)
	if len(items) == 0 {
		return nil
	}

	out := make(map[string]string, len(items))
	for _, item := range items {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" {
			continue
		}
		out[key] = value
	}
	return out
}
