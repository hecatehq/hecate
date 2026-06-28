package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/telemetry"
)

type Config struct {
	Server    ServerConfig
	Router    RouterConfig
	Provider  ProviderConfig
	Chat      ChatConfig
	Projects  ProjectsConfig
	OTel      OTelConfig
	Governor  GovernorConfig
	Retention RetentionConfig
	// SQLite is the single-node durable tier. Subsystems opt in via
	// HECATE_BACKEND=sqlite; the client is opened lazily once any subsystem
	// actually needs it. See storage.SQLiteClient for the pragmas applied per
	// connection.
	SQLite    SQLiteConfig
	Postgres  PostgresConfig
	Providers ProvidersConfig
	LogLevel  string
}

type ServerConfig struct {
	Address                           string
	AllowNonLoopbackBind              bool
	AllowedOrigins                    []string
	RuntimeToken                      string
	InferenceToken                    string
	RemoteRuntimeMode                 bool
	RemoteRuntimeSecret               string
	RemoteAllowLocalProviders         bool
	RemoteAllowACPTerminals           bool
	PersonalRemoteExternalAgentLogins bool
	PublicURL                         string
	DataDir                           string
	BootstrapFile                     string
	ControlPlaneBackend               string
	ControlPlaneKey                   string
	ControlPlaneSecretKey             string
	TasksBackend                      string
	TaskApprovalPolicies              []string
	TaskQueueBackend                  string
	TaskQueueWorkers                  int
	TaskQueueBuffer                   int
	TaskQueueLeaseSeconds             int
	TaskMaxConcurrentPerTenant        int
	// TaskReconcileInterval controls how often the periodic reconciler
	// scans for runs stuck in "running" past 3× the lease duration.
	// Default 30s. Set via HECATE_TASK_RECONCILE_INTERVAL (Go duration
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
	// Set via HECATE_AGENT_ADAPTER_APPROVAL_MODE.
	//
	// Background: prior to this knob the gateway silently auto-approved
	// every adapter request. Default flips to "prompt" because the
	// External Agent Adapters subsystem is alpha; operators who depend
	// on the old behavior must opt into "auto" explicitly.
	//
	// See docs/design/external-adapter-approvals-v1.md.
	AgentAdapterApprovalMode string
	// AgentAdapterApprovalTimeout is how long a pending approval waits
	// before resolving to ACP `Cancelled`. Default 5m. Set via
	// HECATE_AGENT_ADAPTER_APPROVAL_TIMEOUT (Go duration string).
	AgentAdapterApprovalTimeout time.Duration
	// AgentAdapterTerminals enables ACP terminal/* callbacks for External
	// Agents. Off by default because it is a command-execution surface. Local
	// self-host runtimes opt in with HECATE_AGENT_ADAPTER_TERMINALS=1; remote
	// runtimes must also set HECATE_REMOTE_ALLOW_ACP_TERMINALS=1.
	AgentAdapterTerminals bool
	// OperatorTerminals enables Hecate-owned operator terminal sessions over
	// the runtime API. Off by default because it is a command-execution surface.
	// Set via HECATE_OPERATOR_TERMINALS. Remote runtime mode rejects this flag;
	// use the surrounding hosting shell for remote/container operator access.
	OperatorTerminals bool
	// ChatMaxTurnsPerSession caps how many user→assistant round-trips
	// a single agent-chat session may execute. 0 (default) means unlimited.
	// When the ceiling is reached, POST
	// /hecate/v1/chat/sessions/{id}/messages returns HTTP 422 with code
	// "chat.session_limit_exceeded".
	// Set via HECATE_CHAT_MAX_TURNS_PER_SESSION.
	ChatMaxTurnsPerSession int
	// ChatMaxSessionDuration caps wall-clock age for an agent-chat
	// session. 0 (default) means unlimited.
	// Set via HECATE_CHAT_MAX_SESSION_DURATION.
	ChatMaxSessionDuration time.Duration
	// ChatIdleTimeout auto-closes sessions that have not changed for this
	// long. 0 (default) disables the sweeper.
	// Set via HECATE_CHAT_IDLE_TIMEOUT.
	ChatIdleTimeout time.Duration

	// TraceBodyCapture enables recording request and response body diagnostics
	// in the distributed trace. Off by default; enable via HECATE_TRACE_BODIES=true.
	TraceBodyCapture bool
	// TraceBodyMode controls whether body diagnostics contain metadata only
	// (default) or redacted text snapshots. Set via HECATE_TRACE_BODY_MODE.
	TraceBodyMode string
	// TraceBodyMaxBytes caps each captured body at this many bytes (default 4096).
	// Only applies when TraceBodyMode is "redacted_text".
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

func (c Config) LocalProvidersAllowed() bool {
	return !c.Server.RemoteRuntimeMode || c.Server.RemoteAllowLocalProviders
}

func (c Config) PersonalRemoteExternalAgentLoginsAllowed() bool {
	return c.Server.RemoteRuntimeMode && c.Server.PersonalRemoteExternalAgentLogins
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

type ProjectsConfig struct {
	Backend                 string
	CoordinationBackend     string
	CairnlineReadSource     string
	CairnlineWriteAuthority string
}

func (c Config) ProjectsCoordinationBackend() string {
	return normalizeProjectsCoordinationBackend(c.Projects.CoordinationBackend)
}

func (c Config) ProjectsCairnlineReadSource() string {
	return normalizeProjectsCairnlineReadSource(c.Projects.CairnlineReadSource)
}

func (c Config) ProjectsCairnlineWriteAuthority() []string {
	return normalizeProjectsCairnlineWriteAuthority(c.Projects.CairnlineWriteAuthority)
}

func (c Config) ProjectsCairnlineWriteAuthorityEnabled(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	for _, item := range c.ProjectsCairnlineWriteAuthority() {
		if item == name {
			return true
		}
	}
	return false
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
	// `just dev` plus `HECATE_BACKEND=sqlite` Just Works without extra
	// mkdir or env. Parent directories are auto-created.
	Path        string
	TablePrefix string
	BusyTimeout time.Duration
}

type PostgresConfig struct {
	DatabaseURL  string
	TablePrefix  string
	MaxOpenConns int
	MaxIdleConns int
}

type ProvidersConfig struct {
	OpenAICompatible []OpenAICompatibleProviderConfig
	// AnthropicCacheDisabled is the gateway-wide value of
	// HECATE_PROVIDER_ANTHROPIC_CACHE_ENABLED (inverted). It applies
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
	// KnownModels is an operator-supplied static catalog override. Populated only
	// via PROVIDER_<NAME>_MODELS env (comma-separated), it populates the static
	// capabilities when no API key is set and live discovery is skipped. Empty
	// by default — Hecate prefers live `/v1/models` discovery over hard-coded
	// lists that bit-rot as upstream catalogs churn.
	KnownModels []string `json:"known_models,omitempty"`
	// AnthropicCacheDisabled opts the Anthropic adapter out of
	// auto-attaching `cache_control: {"type":"ephemeral"}` markers on
	// the last `system` block and the last `tools` entry of outbound
	// Messages-API requests. Auto-marking is on by default — cache
	// hits cut input-token cost ~90% on the static prefix of long
	// agent_loop / chat sessions with no latency penalty, so the
	// safe default is to leave it on and let operators flip the
	// global HECATE_PROVIDER_ANTHROPIC_CACHE_ENABLED=false toggle
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
	storageBackend := strings.ToLower(strings.TrimSpace(getEnv("HECATE_BACKEND", "memory")))
	providersCfg := loadProvidersFromEnv()
	remoteRuntimeMode := getEnvBool("HECATE_REMOTE_RUNTIME_MODE", false)
	remoteAllowLocalProviders := getEnvBool("HECATE_REMOTE_ALLOW_LOCAL_PROVIDERS", false)
	remoteAllowACPTerminals := getEnvBool("HECATE_REMOTE_ALLOW_ACP_TERMINALS", false)
	personalRemoteExternalAgentLogins := getEnvBool("HECATE_PERSONAL_REMOTE_EXTERNAL_AGENT_LOGINS", false)
	allowedProviderKinds := splitCSV(getEnv("HECATE_ALLOWED_PROVIDER_KINDS", ""))
	if remoteRuntimeMode && !remoteAllowLocalProviders && len(allowedProviderKinds) == 0 {
		allowedProviderKinds = []string{"cloud"}
	}
	return Config{
		Server: ServerConfig{
			Address:              getEnv("HECATE_ADDRESS", "127.0.0.1:8765"),
			AllowNonLoopbackBind: getEnvBool("HECATE_ALLOW_NON_LOOPBACK_BIND", false),
			AllowedOrigins:       splitCSV(getEnv("HECATE_ALLOWED_ORIGINS", "")),
			RuntimeToken:         getEnv("HECATE_RUNTIME_TOKEN", ""),
			InferenceToken:       getEnv("HECATE_INFERENCE_TOKEN", ""),
			RemoteRuntimeMode:    remoteRuntimeMode,
			RemoteRuntimeSecret:  getEnv("HECATE_REMOTE_RUNTIME_SECRET", ""),
			// Hosted runtimes deny local model servers by default. Operators
			// running an isolated sidecar can opt in explicitly.
			RemoteAllowLocalProviders:         remoteAllowLocalProviders,
			RemoteAllowACPTerminals:           remoteAllowACPTerminals,
			PersonalRemoteExternalAgentLogins: personalRemoteExternalAgentLogins,
			// PublicURL is written to hecate.runtime.json for local
			// diagnostics. Empty means derive from Address.
			PublicURL: getEnv("HECATE_PUBLIC_URL", ""),
			// Default `.data/` keeps the auto-generated bootstrap file
			// (AES-GCM key for persisted provider secrets) out of the repo root so a stray
			// `git add .` can't sweep it up. Docker overrides this to /data
			// via the Dockerfile.
			DataDir:                        getEnv("HECATE_DATA_DIR", ".data"),
			BootstrapFile:                  getEnv("HECATE_BOOTSTRAP_FILE", ""),
			ControlPlaneBackend:            storageBackend,
			ControlPlaneKey:                getEnv("HECATE_CONTROL_PLANE_KEY", "control-plane"),
			ControlPlaneSecretKey:          getEnv("HECATE_CONTROL_PLANE_SECRET_KEY", ""),
			TasksBackend:                   storageBackend,
			TaskApprovalPolicies:           splitCSV(getEnvApprovalPolicies()),
			TaskQueueBackend:               storageBackend,
			TaskQueueWorkers:               getEnvInt("HECATE_TASK_QUEUE_WORKERS", 1),
			TaskQueueBuffer:                getEnvInt("HECATE_TASK_QUEUE_BUFFER", 128),
			TaskQueueLeaseSeconds:          getEnvInt("HECATE_TASK_QUEUE_LEASE_SECONDS", 30),
			TaskReconcileInterval:          getEnvDuration("HECATE_TASK_RECONCILE_INTERVAL", 30*time.Second),
			TaskAgentLoopMaxTurns:          getEnvInt("HECATE_TASK_AGENT_LOOP_MAX_TURNS", 8),
			TaskMaxMCPServersPerTask:       getEnvInt("HECATE_TASK_MAX_MCP_SERVERS_PER_TASK", 16),
			TaskMCPClientCacheMaxEntries:   getEnvInt("HECATE_TASK_MCP_CLIENT_CACHE_MAX_ENTRIES", 256),
			TaskMCPClientCachePingInterval: getEnvDuration("HECATE_TASK_MCP_CLIENT_CACHE_PING_INTERVAL", 60*time.Second),
			TaskMCPClientCachePingTimeout:  getEnvDuration("HECATE_TASK_MCP_CLIENT_CACHE_PING_TIMEOUT", 5*time.Second),
			TaskAgentSystemPrompt:          getEnv("HECATE_TASK_AGENT_SYSTEM_PROMPT", ""),
			TaskHTTPTimeout:                getEnvDuration("HECATE_TASK_HTTP_TIMEOUT", 30*time.Second),
			TaskHTTPMaxResponseBytes:       getEnvInt("HECATE_TASK_HTTP_MAX_RESPONSE_BYTES", 256*1024),
			TaskHTTPAllowPrivateIPs:        getEnvBool("HECATE_TASK_HTTP_ALLOW_PRIVATE_IPS", false),
			TaskHTTPAllowedHosts:           splitCSV(getEnv("HECATE_TASK_HTTP_ALLOWED_HOSTS", "")),
			TaskShellAllowPrivateIPs:       getEnvBool("HECATE_TASK_SHELL_ALLOW_PRIVATE_IPS", false),
			TaskShellAllowedHosts:          splitCSV(getEnv("HECATE_TASK_SHELL_ALLOWED_HOSTS", "")),
			AgentAdapterApprovalMode:       getEnv("HECATE_AGENT_ADAPTER_APPROVAL_MODE", "prompt"),
			AgentAdapterApprovalTimeout:    getEnvDuration("HECATE_AGENT_ADAPTER_APPROVAL_TIMEOUT", 5*time.Minute),
			AgentAdapterTerminals:          getEnvBool("HECATE_AGENT_ADAPTER_TERMINALS", false),
			OperatorTerminals:              getEnvBool("HECATE_OPERATOR_TERMINALS", false),
			ChatMaxTurnsPerSession:         getEnvInt("HECATE_CHAT_MAX_TURNS_PER_SESSION", 0),
			ChatMaxSessionDuration:         getEnvDuration("HECATE_CHAT_MAX_SESSION_DURATION", 0),
			ChatIdleTimeout:                getEnvDuration("HECATE_CHAT_IDLE_TIMEOUT", 0),
			TaskMaxConcurrentPerTenant:     getEnvInt("HECATE_TASK_MAX_CONCURRENT_PER_TENANT", 0),
			TraceBodyCapture:               getEnvBool("HECATE_TRACE_BODIES", false),
			TraceBodyMode:                  getEnv("HECATE_TRACE_BODY_MODE", "metadata"),
			TraceBodyMaxBytes:              getEnvInt("HECATE_TRACE_BODY_MAX_BYTES", 4096),
			RateLimit: RateLimitConfig{
				Enabled:           getEnvBool("HECATE_RATE_LIMIT_ENABLED", false),
				RequestsPerMinute: getEnvInt64("HECATE_RATE_LIMIT_RPM", 60),
				BurstSize:         getEnvInt64("HECATE_RATE_LIMIT_BURST", 0), // 0 = same as RPM
			},
		},
		Router: RouterConfig{
			DefaultModel: "",
		},
		Provider: ProviderConfig{
			MaxAttempts:                    getEnvInt("HECATE_PROVIDER_MAX_ATTEMPTS", 2),
			RetryBackoff:                   getEnvDuration("HECATE_PROVIDER_RETRY_BACKOFF", 200*time.Millisecond),
			FailoverEnabled:                getEnvBool("HECATE_PROVIDER_FAILOVER_ENABLED", true),
			HealthThreshold:                getEnvInt("HECATE_PROVIDER_HEALTH_FAILURE_THRESHOLD", 3),
			HealthCooldown:                 getEnvDuration("HECATE_PROVIDER_HEALTH_COOLDOWN", 30*time.Second),
			HealthLatencyDegradedThreshold: getEnvDuration("HECATE_PROVIDER_HEALTH_LATENCY_DEGRADED_THRESHOLD", 0),
			HistoryBackend:                 storageBackend,
			HistoryLimit:                   getEnvInt("HECATE_PROVIDER_HISTORY_LIMIT", 100),
		},
		Chat: ChatConfig{
			SessionsBackend: storageBackend,
		},
		Projects: ProjectsConfig{
			Backend:                 storageBackend,
			CoordinationBackend:     strings.ToLower(strings.TrimSpace(getEnv("HECATE_PROJECTS_COORDINATION_BACKEND", "hecate"))),
			CairnlineReadSource:     strings.ToLower(strings.TrimSpace(getEnv("HECATE_PROJECTS_CAIRNLINE_READ_SOURCE", "auto"))),
			CairnlineWriteAuthority: getEnv("HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY", "none"),
		},
		OTel: loadOTelFromEnv(),
		Governor: GovernorConfig{
			DenyAll:              getEnvBool("HECATE_DENY_ALL", false),
			MaxPromptTokens:      getEnvInt("HECATE_MAX_PROMPT_TOKENS", 64_000),
			ModelRewriteTo:       getEnv("HECATE_MODEL_REWRITE_TO", ""),
			UsageBackend:         storageBackend,
			UsageKey:             getEnv("HECATE_USAGE_KEY", "global"),
			UsageScope:           getEnv("HECATE_USAGE_SCOPE", "global"),
			RouteMode:            getEnv("HECATE_ROUTE_MODE", "any"),
			AllowedProviders:     splitCSV(getEnv("HECATE_ALLOWED_PROVIDERS", "")),
			DeniedProviders:      splitCSV(getEnv("HECATE_DENIED_PROVIDERS", "")),
			AllowedModels:        splitCSV(getEnv("HECATE_ALLOWED_MODELS", "")),
			DeniedModels:         splitCSV(getEnv("HECATE_DENIED_MODELS", "")),
			AllowedProviderKinds: allowedProviderKinds,
			UsageHistoryLimit:    getEnvInt("HECATE_USAGE_HISTORY_LIMIT", 20),
		},
		Retention: RetentionConfig{
			Enabled:         getEnvBool("HECATE_RETENTION_ENABLED", false),
			HistoryBackend:  storageBackend,
			Interval:        getEnvDuration("HECATE_RETENTION_INTERVAL", 15*time.Minute),
			TraceSnapshots:  loadRetentionPolicyFromEnv("HECATE_RETENTION_TRACES_", 24*time.Hour, 2000),
			UsageEvents:     loadRetentionPolicyFromEnv("HECATE_RETENTION_USAGE_EVENTS_", 30*24*time.Hour, 200),
			AuditEvents:     loadRetentionPolicyFromEnv("HECATE_RETENTION_AUDIT_EVENTS_", 30*24*time.Hour, 500),
			ProviderHistory: loadRetentionPolicyFromEnv("HECATE_RETENTION_PROVIDER_HISTORY_", 7*24*time.Hour, 10_000),
			// turn.completed events accumulate fast on long
			// agent runs (one per LLM round-trip). 7d/100k is a
			// generous default; tune down on busy installs.
			TurnEvents: loadRetentionPolicyFromEnv("HECATE_RETENTION_TURN_EVENTS_", 7*24*time.Hour, 100_000),
			// External-adapter approval history. Only resolved rows
			// are pruned; pending rows stay until startup reconcile
			// flips them. Default 30d/10k mirrors task_approvals.
			ChatApprovals: loadRetentionPolicyFromEnv("HECATE_RETENTION_CHAT_APPROVALS_", 30*24*time.Hour, 10_000),
		},
		SQLite: SQLiteConfig{
			Path:        getEnv("HECATE_SQLITE_PATH", ".data/hecate.db"),
			TablePrefix: getEnv("HECATE_SQLITE_TABLE_PREFIX", "hecate"),
			BusyTimeout: getEnvDuration("HECATE_SQLITE_BUSY_TIMEOUT", 5*time.Second),
		},
		Postgres: PostgresConfig{
			DatabaseURL:  firstNonEmpty(getEnv("HECATE_POSTGRES_URL", ""), getEnv("DATABASE_URL", "")),
			TablePrefix:  getEnv("HECATE_POSTGRES_TABLE_PREFIX", "hecate"),
			MaxOpenConns: getEnvInt("HECATE_POSTGRES_MAX_OPEN_CONNS", 10),
			MaxIdleConns: getEnvInt("HECATE_POSTGRES_MAX_IDLE_CONNS", 5),
		},
		Providers: providersCfg,
		LogLevel:  getEnv("LOG_LEVEL", "INFO"),
	}
}

func (c Config) storageBackends() []string {
	return []string{
		c.Server.ControlPlaneBackend,
		c.Server.TasksBackend,
		c.Server.TaskQueueBackend,
		c.Chat.SessionsBackend,
		c.Projects.Backend,
		c.Governor.UsageBackend,
		c.Retention.HistoryBackend,
		c.Provider.HistoryBackend,
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

	for _, backend := range c.storageBackends() {
		validateBackend("HECATE_BACKEND", backend, "memory", "sqlite", "postgres")
	}
	validateBackend("HECATE_PROJECTS_COORDINATION_BACKEND", c.ProjectsCoordinationBackend(), "hecate", "cairnline")
	validateBackend("HECATE_PROJECTS_CAIRNLINE_READ_SOURCE", c.ProjectsCairnlineReadSource(), "auto", "snapshot", "embedded")
	for _, item := range c.ProjectsCairnlineWriteAuthority() {
		validateBackend("HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY", item, "project-memory")
	}
	if postgresRequired(c) && strings.TrimSpace(c.Postgres.DatabaseURL) == "" {
		errs = append(errs, errors.New("HECATE_POSTGRES_URL or DATABASE_URL is required when HECATE_BACKEND=postgres"))
	}
	if publicURL := strings.TrimSpace(c.Server.PublicURL); publicURL != "" {
		u, err := url.ParseRequestURI(publicURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			errs = append(errs, fmt.Errorf("HECATE_PUBLIC_URL must be an absolute http(s) URL (got %q)", publicURL))
		}
	}
	for _, origin := range c.Server.AllowedOrigins {
		if !validAllowedOrigin(origin) {
			errs = append(errs, fmt.Errorf("HECATE_ALLOWED_ORIGINS entries must be exact http(s) origins without path/query/fragment (got %q)", origin))
		}
	}
	if token := strings.TrimSpace(c.Server.RuntimeToken); token != "" && len(token) < 24 {
		errs = append(errs, errors.New("HECATE_RUNTIME_TOKEN must be at least 24 characters when set"))
	}
	if token := strings.TrimSpace(c.Server.InferenceToken); token != "" && len(token) < 24 {
		errs = append(errs, errors.New("HECATE_INFERENCE_TOKEN must be at least 24 characters when set"))
	}
	if c.Server.RemoteRuntimeMode {
		if secret := strings.TrimSpace(c.Server.RemoteRuntimeSecret); len(secret) < 24 {
			errs = append(errs, errors.New("HECATE_REMOTE_RUNTIME_SECRET must be at least 24 characters when HECATE_REMOTE_RUNTIME_MODE is enabled"))
		}
		if !c.Server.RemoteAllowLocalProviders && stringSliceContainsFold(c.Governor.AllowedProviderKinds, "local") {
			errs = append(errs, errors.New("HECATE_REMOTE_ALLOW_LOCAL_PROVIDERS=1 is required before allowing local provider kind in remote runtime mode"))
		}
		if c.Server.AgentAdapterTerminals && !c.Server.RemoteAllowACPTerminals {
			errs = append(errs, errors.New("HECATE_REMOTE_ALLOW_ACP_TERMINALS=1 is required before enabling HECATE_AGENT_ADAPTER_TERMINALS in remote runtime mode"))
		}
		if c.Server.OperatorTerminals {
			errs = append(errs, errors.New("HECATE_OPERATOR_TERMINALS cannot be enabled in remote runtime mode"))
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
			errs = append(errs, fmt.Errorf("HECATE_TASK_APPROVAL_POLICIES: unknown policy name %q (valid: shell_exec, git_exec, file_write, network_egress, read_file, all_tools)", p))
		}
	}

	if c.Retention.Enabled && c.Retention.Interval <= 0 {
		errs = append(errs, errors.New("HECATE_RETENTION_INTERVAL must be positive when retention is enabled"))
	}
	for label, policy := range map[string]RetentionPolicy{
		"HECATE_RETENTION_TRACES":           c.Retention.TraceSnapshots,
		"HECATE_RETENTION_USAGE_EVENTS":     c.Retention.UsageEvents,
		"HECATE_RETENTION_AUDIT_EVENTS":     c.Retention.AuditEvents,
		"HECATE_RETENTION_PROVIDER_HISTORY": c.Retention.ProviderHistory,
		"HECATE_RETENTION_TURN_EVENTS":      c.Retention.TurnEvents,
	} {
		if policy.MaxAge < 0 {
			errs = append(errs, fmt.Errorf("%s_MAX_AGE must be zero or positive", label))
		}
		if policy.MaxCount < 0 {
			errs = append(errs, fmt.Errorf("%s_MAX_COUNT must be zero or positive", label))
		}
	}
	if c.Provider.MaxAttempts <= 0 {
		errs = append(errs, errors.New("HECATE_PROVIDER_MAX_ATTEMPTS must be positive"))
	}
	if c.Provider.HealthThreshold < 0 {
		errs = append(errs, errors.New("HECATE_PROVIDER_HEALTH_FAILURE_THRESHOLD must be zero or positive"))
	}
	if c.Provider.HealthLatencyDegradedThreshold < 0 {
		errs = append(errs, errors.New("HECATE_PROVIDER_HEALTH_LATENCY_DEGRADED_THRESHOLD must be zero or positive"))
	}
	if c.Provider.HistoryLimit < 0 {
		errs = append(errs, errors.New("HECATE_PROVIDER_HISTORY_LIMIT must be zero or positive"))
	}
	if c.Server.TaskQueueWorkers <= 0 {
		errs = append(errs, errors.New("HECATE_TASK_QUEUE_WORKERS must be positive"))
	}
	if c.Server.TaskQueueBuffer < 0 {
		errs = append(errs, errors.New("HECATE_TASK_QUEUE_BUFFER must be zero or positive"))
	}
	if c.Server.ChatMaxTurnsPerSession < 0 {
		errs = append(errs, errors.New("HECATE_CHAT_MAX_TURNS_PER_SESSION must be zero or positive"))
	}
	if c.Server.ChatMaxSessionDuration < 0 {
		errs = append(errs, errors.New("HECATE_CHAT_MAX_SESSION_DURATION must be zero or positive"))
	}
	if c.Server.ChatIdleTimeout < 0 {
		errs = append(errs, errors.New("HECATE_CHAT_IDLE_TIMEOUT must be zero or positive"))
	}
	if c.Server.RateLimit.Enabled && c.Server.RateLimit.RequestsPerMinute <= 0 {
		errs = append(errs, errors.New("HECATE_RATE_LIMIT_RPM must be positive when rate limiting is enabled"))
	}
	if c.Server.RateLimit.BurstSize < 0 {
		errs = append(errs, errors.New("HECATE_RATE_LIMIT_BURST must be zero or positive"))
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
		{"HECATE_OTEL_TRANSPORT", c.OTel.Transport},
		{"HECATE_OTEL_TRACES_TRANSPORT", c.OTel.Traces.Transport},
		{"HECATE_OTEL_METRICS_TRANSPORT", c.OTel.Metrics.Transport},
		{"HECATE_OTEL_LOGS_TRANSPORT", c.OTel.Logs.Transport},
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
		errs = append(errs, fmt.Errorf("HECATE_OTEL_METRICS_EXEMPLAR_FILTER must be one of trace_based, always_on, or always_off"))
	}
	switch strings.ToLower(strings.TrimSpace(c.Server.TraceBodyMode)) {
	case "", "metadata", "redacted_text":
	default:
		errs = append(errs, fmt.Errorf("HECATE_TRACE_BODY_MODE must be one of metadata or redacted_text"))
	}

	return errors.Join(errs...)
}

func postgresRequired(c Config) bool {
	for _, backend := range c.storageBackends() {
		if backend == "postgres" {
			return true
		}
	}
	return false
}

func durationEnvKeys() []string {
	return []string{
		"HECATE_TASK_MCP_CLIENT_CACHE_PING_INTERVAL",
		"HECATE_TASK_MCP_CLIENT_CACHE_PING_TIMEOUT",
		"HECATE_TASK_HTTP_TIMEOUT",
		"HECATE_PROVIDER_RETRY_BACKOFF",
		"HECATE_PROVIDER_HEALTH_COOLDOWN",
		"HECATE_PROVIDER_HEALTH_LATENCY_DEGRADED_THRESHOLD",
		"HECATE_OTEL_TIMEOUT",
		"HECATE_OTEL_TRACES_TIMEOUT",
		"HECATE_OTEL_METRICS_TIMEOUT",
		"HECATE_OTEL_LOGS_TIMEOUT",
		"HECATE_OTEL_METRICS_INTERVAL",
		"HECATE_RETENTION_INTERVAL",
		"HECATE_RETENTION_TRACES_MAX_AGE",
		"HECATE_RETENTION_USAGE_EVENTS_MAX_AGE",
		"HECATE_RETENTION_AUDIT_EVENTS_MAX_AGE",
		"HECATE_RETENTION_PROVIDER_HISTORY_MAX_AGE",
		"HECATE_RETENTION_TURN_EVENTS_MAX_AGE",
		"HECATE_CHAT_MAX_SESSION_DURATION",
		"HECATE_CHAT_IDLE_TIMEOUT",
		"HECATE_SQLITE_BUSY_TIMEOUT",
	}
}

func validAllowedOrigin(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return u.Path == "" || u.Path == "/"
}

func loadOTelFromEnv() OTelConfig {
	sharedEndpoint := strings.TrimSpace(getEnv("HECATE_OTEL_ENDPOINT", ""))
	sharedHeaders := parseEnvMap(getEnv("HECATE_OTEL_HEADERS", ""))
	sharedTimeout := getEnvDuration("HECATE_OTEL_TIMEOUT", 5*time.Second)
	sharedTransport := normalizeOTelTransport(getEnv("HECATE_OTEL_TRANSPORT", "http"))

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
		ServiceName:           getEnv("HECATE_OTEL_SERVICE_NAME", "hecate"),
		ServiceVersion:        getEnv("HECATE_OTEL_SERVICE_VERSION", ""),
		ServiceInstanceID:     getEnv("HECATE_OTEL_SERVICE_INSTANCE_ID", ""),
		DeploymentEnvironment: getEnv("HECATE_OTEL_DEPLOYMENT_ENVIRONMENT", ""),
		Endpoint:              sharedEndpoint,
		Headers:               cloneStringMap(sharedHeaders),
		Timeout:               sharedTimeout,
		Transport:             sharedTransport,
		MetricsInterval:       getEnvDuration("HECATE_OTEL_METRICS_INTERVAL", 30*time.Second),
		MetricsExemplarFilter: getEnv("HECATE_OTEL_METRICS_EXEMPLAR_FILTER", ""),
		TracesSampler:         getEnv("HECATE_OTEL_TRACES_SAMPLER", ""),
		TracesSamplerArg:      getEnvFloat64("HECATE_OTEL_TRACES_SAMPLER_ARG", 1.0),
		Traces:                traces,
		Metrics:               metrics,
		Logs:                  logs,
	}
}

func loadOTelSignalFromEnv(signal, sharedEndpoint, httpPath string, sharedHeaders map[string]string, sharedTimeout time.Duration, sharedTransport string) OTelSignalConfig {
	prefix := "HECATE_OTEL_" + signal + "_"
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

func (cfg ServerConfig) ValidateNetworkExposure() error {
	if ListenAddressIsLoopback(cfg.Address) || cfg.AllowNonLoopbackBind {
		return nil
	}
	return fmt.Errorf("HECATE_ADDRESS %q binds beyond loopback; set HECATE_ALLOW_NON_LOOPBACK_BIND=1 only when a firewall, reverse proxy, or equivalent access-control layer protects the gateway", cfg.Address)
}

func ListenAddressIsLoopback(address string) bool {
	host := strings.TrimSpace(address)
	if host == "" {
		return false
	}
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
// HECATE_PROVIDER_ANTHROPIC_CACHE_ENABLED=false only for cost-tier
// comparisons or to debug a suspected cache-related issue. Returns
// the inverted (`disabled`) form so the zero-value bool stamped on
// every provider config means "caching on" — CP-stored records and
// direct test constructions then inherit the safe default without
// each call site remembering to opt in.
func anthropicCacheDisabledFromEnv() bool {
	return !getEnvBool("HECATE_PROVIDER_ANTHROPIC_CACHE_ENABLED", true)
}

func loadProvidersFromEnv() ProvidersConfig {
	// Only register providers that have at least one explicit env var
	// (PROVIDER_<NAME>_BASE_URL / _API_KEY / etc.).
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
	for _, suffix := range []string{"_API_KEY", "_BASE_URL"} {
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

	cfg := providerDefaults(name)
	prefixes := []string{providerEnvPrefix(name)}
	for _, prefix := range prefixes {
		cfg.Kind = getEnv(prefix+"KIND", cfg.Kind)
		cfg.BaseURL = getEnv(prefix+"BASE_URL", cfg.BaseURL)
		cfg.APIKey = getEnv(prefix+"API_KEY", cfg.APIKey)
		cfg.StubMode = getEnvBool(prefix+"STUB_MODE", cfg.StubMode)
		cfg.StubResponse = getEnv(prefix+"STUB_RESPONSE", cfg.StubResponse)
		if models := splitCSV(getEnv(prefix+"MODELS", "")); len(models) > 0 {
			cfg.KnownModels = models
		}
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

func providerDefaults(name string) OpenAICompatibleProviderConfig {
	if builtIn, ok := BuiltInProviderByID(name); ok {
		return builtIn.RuntimeConfig()
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

// getEnvApprovalPolicies reads HECATE_TASK_APPROVAL_POLICIES and honours an
// explicitly empty value (KEY=) as "no policies". os.Getenv cannot distinguish
// "not set" from "set to empty"; os.LookupEnv can. An empty value is the
// documented opt-out for fully-trusted environments, so we must not fall back
// to the default in that case.
func getEnvApprovalPolicies() string {
	const key = "HECATE_TASK_APPROVAL_POLICIES"
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func normalizeProjectsCoordinationBackend(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "hecate"
	}
	return value
}

func normalizeProjectsCairnlineReadSource(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "auto"
	}
	return value
}

func normalizeProjectsCairnlineWriteAuthority(value string) []string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "none" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" || part == "none" {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
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

func stringSliceContainsFold(values []string, needle string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), needle) {
			return true
		}
	}
	return false
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
