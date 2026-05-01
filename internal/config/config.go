package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Server    ServerConfig
	Router    RouterConfig
	Provider  ProviderConfig
	Chat      ChatConfig
	OTel      OTelConfig
	Governor  GovernorConfig
	Cache     CacheConfig
	Retention RetentionConfig
	Postgres  PostgresConfig
	// SQLite is the single-node-durable tier. Subsystems opt in via
	// GATEWAY_*_BACKEND=sqlite; the client is opened lazily once any
	// subsystem actually needs it. See storage.SQLiteClient for the
	// pragmas applied per connection.
	SQLite    SQLiteConfig
	Providers ProvidersConfig
	Pricebook PricebookConfig
	LogLevel  string
}

type ServerConfig struct {
	Address   string
	AuthToken string
	// AuthDisabled forces the gateway into a no-auth mode regardless of
	// AuthToken / store presence. Wired to GATEWAY_AUTH_DISABLED. Used
	// by test envs and reverse-proxy-fronted deployments where auth is
	// terminated upstream. The single-user localhost path is served by
	// the bootstrap-token handshake instead, not by flipping this flag.
	AuthDisabled bool
	// MultiTenant exposes the tenants/keys management surface in the
	// operator UI. Wired to GATEWAY_MULTI_TENANT. Default false: a
	// single-user workspace where Tenants/Keys/Usage tabs are hidden.
	// The data layer is unaffected — multi_tenant is a UI-visibility
	// flag, not a server-side gate.
	MultiTenant                bool
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
	SessionsKey     string
	SessionLimit    int
}

type OTelSignalConfig struct {
	Enabled  bool
	Endpoint string
	Headers  map[string]string
	Timeout  time.Duration
}

type OTelConfig struct {
	ServiceName           string
	ServiceVersion        string
	ServiceInstanceID     string
	DeploymentEnvironment string
	Traces                OTelSignalConfig
	TracesSampler         string
	TracesSamplerArg      float64
	Metrics               OTelSignalConfig
	MetricsInterval       time.Duration
	Logs                  OTelSignalConfig
}

type GovernorConfig struct {
	DenyAll                 bool
	MaxPromptTokens         int
	MaxTotalBudgetMicros    int64
	ModelRewriteTo          string
	PolicyRules             []PolicyRuleConfig `json:"policy_rules"`
	BudgetBackend           string
	BudgetKey               string
	BudgetScope             string
	BudgetTenantFallback    string
	RouteMode               string
	AllowedProviders        []string
	DeniedProviders         []string
	AllowedModels           []string
	DeniedModels            []string
	AllowedProviderKinds    []string
	BudgetWarningThresholds []int
	BudgetHistoryLimit      int
}

type PolicyRuleConfig struct {
	ID                     string   `json:"id"`
	Action                 string   `json:"action"`
	Reason                 string   `json:"reason"`
	Roles                  []string `json:"roles"`
	Tenants                []string `json:"tenants"`
	Providers              []string `json:"providers"`
	ProviderKinds          []string `json:"provider_kinds"`
	Models                 []string `json:"models"`
	RouteReasons           []string `json:"route_reasons"`
	MinPromptTokens        int      `json:"min_prompt_tokens"`
	MinEstimatedCostMicros int64    `json:"min_estimated_cost_micros_usd"`
	RewriteModelTo         string   `json:"rewrite_model_to"`
}

type CacheConfig struct {
	DefaultTTL time.Duration
	Backend    string
	Semantic   SemanticCacheConfig
}

type RetentionConfig struct {
	Enabled         bool
	HistoryBackend  string
	Interval        time.Duration
	TraceSnapshots  RetentionPolicy
	BudgetEvents    RetentionPolicy
	AuditEvents     RetentionPolicy
	ExactCache      RetentionPolicy
	SemanticCache   RetentionPolicy
	ProviderHistory RetentionPolicy
	// TurnEvents prunes `agent.turn.completed` rows from the
	// task-run events table. Other event types (run.started/finished,
	// approval.*) are kept for forensics.
	TurnEvents RetentionPolicy
}

type RetentionPolicy struct {
	MaxAge   time.Duration
	MaxCount int
}

type SemanticCacheConfig struct {
	Enabled                          bool
	Backend                          string
	DefaultTTL                       time.Duration
	MinSimilarity                    float64
	MaxEntries                       int
	MaxTextChars                     int
	Embedder                         string
	EmbedderProvider                 string
	EmbedderModel                    string
	EmbedderBaseURL                  string
	EmbedderAPIKey                   string
	EmbedderTimeout                  time.Duration
	PostgresVectorMode               string
	PostgresVectorCandidates         int
	PostgresVectorIndexMode          string
	PostgresVectorIndexType          string
	PostgresVectorHNSWM              int
	PostgresVectorHNSWEfConstruction int
	PostgresVectorIVFFlatLists       int
	PostgresVectorSearchEf           int
	PostgresVectorSearchProbes       int
}

type PostgresConfig struct {
	DSN          string
	Schema       string
	TablePrefix  string
	MaxOpenConns int
	MaxIdleConns int
}

type SQLiteConfig struct {
	// Path is the on-disk file. Defaults to .data/hecate.db so a fresh
	// `make dev` plus `GATEWAY_*_BACKEND=sqlite` Just Works without
	// extra mkdir or env. Parent directories are auto-created.
	Path        string
	TablePrefix string
	BusyTimeout time.Duration
}

type ProvidersConfig struct {
	OpenAICompatible []OpenAICompatibleProviderConfig
}

type PricebookConfig struct {
	UnknownModelPolicy string             `json:"unknown_model_policy"`
	Entries            []ModelPriceConfig `json:"entries"`
	// AutoImportInterval ticks the LiteLLM bulk-import on a schedule.
	// Zero or negative disables it (the default — operators opt in by
	// setting GATEWAY_PRICEBOOK_AUTO_IMPORT_INTERVAL to e.g. 24h). The
	// scheduler runs once on start, then on every interval. It applies
	// blanket (no key filter), which means manual rows are always
	// preserved per the operator-protection contract.
	AutoImportInterval time.Duration `json:"auto_import_interval,omitempty"`
}

type ModelPriceConfig struct {
	Provider                             string `json:"provider"`
	Model                                string `json:"model"`
	InputMicrosUSDPerMillionTokens       int64  `json:"input_micros_usd_per_million_tokens"`
	OutputMicrosUSDPerMillionTokens      int64  `json:"output_micros_usd_per_million_tokens"`
	CachedInputMicrosUSDPerMillionTokens int64  `json:"cached_input_micros_usd_per_million_tokens"`
	// Source records who set this entry: "manual" if a human edited it
	// through the admin UI / API, "imported" if it came from a LiteLLM
	// pricing-data import. Manual entries are protected from being
	// overwritten by subsequent imports. Empty == "manual" for backward
	// compatibility (every pre-existing row was set by a human).
	Source string `json:"source,omitempty"`
}

// Pricebook source constants. Free-form strings rather than a typed enum
// because they round-trip through JSON to the UI; keeping the wire
// format stable is more important than compile-time exhaustiveness.
const (
	PricebookSourceManual   = "manual"
	PricebookSourceImported = "imported"
)

type OpenAICompatibleProviderConfig struct {
	Name         string        `json:"name"`
	Kind         string        `json:"kind"`
	Protocol     string        `json:"protocol"`
	BaseURL      string        `json:"base_url"`
	APIKey       string        `json:"api_key"`
	APIVersion   string        `json:"api_version"`
	Timeout      time.Duration `json:"timeout"`
	StubMode     bool          `json:"stub_mode"`
	StubResponse string        `json:"stub_response"`
	DefaultModel string        `json:"default_model"`
	Enabled      bool          `json:"enabled"`
	// KnownModels is the curated catalog from the built-in preset. It populates the
	// static capabilities when no API key is set and live discovery is skipped.
	KnownModels []string `json:"known_models,omitempty"`
}

func LoadFromEnv() Config {
	providersCfg := loadProvidersFromEnv()
	return Config{
		Server: ServerConfig{
			Address:      getEnv("GATEWAY_ADDRESS", ":8765"),
			AuthToken:    getEnv("GATEWAY_AUTH_TOKEN", ""),
			AuthDisabled: getEnvBool("GATEWAY_AUTH_DISABLED", false),
			MultiTenant:  getEnvBool("GATEWAY_MULTI_TENANT", false),
			// Default `.data/` keeps the auto-generated bootstrap file
			// (admin token + AES-GCM key) out of the repo root so a stray
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
			SessionsKey:     getEnv("GATEWAY_CHAT_SESSIONS_KEY", "chat-sessions"),
			SessionLimit:    getEnvInt("GATEWAY_CHAT_SESSIONS_LIMIT", 50),
		},
		OTel: OTelConfig{
			ServiceName:           getEnv("GATEWAY_OTEL_SERVICE_NAME", "hecate-gateway"),
			ServiceVersion:        getEnv("GATEWAY_OTEL_SERVICE_VERSION", ""),
			ServiceInstanceID:     getEnv("GATEWAY_OTEL_SERVICE_INSTANCE_ID", ""),
			DeploymentEnvironment: getEnv("GATEWAY_OTEL_DEPLOYMENT_ENVIRONMENT", ""),
			MetricsInterval:       getEnvDuration("GATEWAY_OTEL_METRICS_INTERVAL", 30*time.Second),
			TracesSampler:         getEnv("GATEWAY_OTEL_TRACES_SAMPLER", ""),
			TracesSamplerArg:      getEnvFloat64("GATEWAY_OTEL_TRACES_SAMPLER_ARG", 1.0),
			Traces: OTelSignalConfig{
				Enabled:  getEnvBool("GATEWAY_OTEL_TRACES_ENABLED", false),
				Endpoint: getEnv("GATEWAY_OTEL_TRACES_ENDPOINT", ""),
				Headers:  parseEnvMap(getEnv("GATEWAY_OTEL_TRACES_HEADERS", "")),
				Timeout:  getEnvDuration("GATEWAY_OTEL_TRACES_TIMEOUT", 5*time.Second),
			},
			Metrics: OTelSignalConfig{
				Enabled:  getEnvBool("GATEWAY_OTEL_METRICS_ENABLED", false),
				Endpoint: getEnv("GATEWAY_OTEL_METRICS_ENDPOINT", ""),
				Headers:  parseEnvMap(getEnv("GATEWAY_OTEL_METRICS_HEADERS", "")),
				Timeout:  getEnvDuration("GATEWAY_OTEL_METRICS_TIMEOUT", 5*time.Second),
			},
			Logs: OTelSignalConfig{
				Enabled:  getEnvBool("GATEWAY_OTEL_LOGS_ENABLED", false),
				Endpoint: getEnv("GATEWAY_OTEL_LOGS_ENDPOINT", ""),
				Headers:  parseEnvMap(getEnv("GATEWAY_OTEL_LOGS_HEADERS", "")),
				Timeout:  getEnvDuration("GATEWAY_OTEL_LOGS_TIMEOUT", 5*time.Second),
			},
		},
		Governor: GovernorConfig{
			DenyAll:                 getEnvBool("GATEWAY_DENY_ALL", false),
			MaxPromptTokens:         getEnvInt("GATEWAY_MAX_PROMPT_TOKENS", 64_000),
			MaxTotalBudgetMicros:    getEnvInt64("GATEWAY_MAX_BUDGET_MICROS_USD", 5_000_000),
			ModelRewriteTo:          getEnv("GATEWAY_MODEL_REWRITE_TO", ""),
			BudgetBackend:           getEnv("GATEWAY_BUDGET_BACKEND", "memory"),
			BudgetKey:               getEnv("GATEWAY_BUDGET_KEY", "global"),
			BudgetScope:             getEnv("GATEWAY_BUDGET_SCOPE", "global"),
			BudgetTenantFallback:    getEnv("GATEWAY_BUDGET_TENANT_FALLBACK", "anonymous"),
			RouteMode:               getEnv("GATEWAY_ROUTE_MODE", "any"),
			AllowedProviders:        splitCSV(getEnv("GATEWAY_ALLOWED_PROVIDERS", "")),
			DeniedProviders:         splitCSV(getEnv("GATEWAY_DENIED_PROVIDERS", "")),
			AllowedModels:           splitCSV(getEnv("GATEWAY_ALLOWED_MODELS", "")),
			DeniedModels:            splitCSV(getEnv("GATEWAY_DENIED_MODELS", "")),
			AllowedProviderKinds:    splitCSV(getEnv("GATEWAY_ALLOWED_PROVIDER_KINDS", "")),
			BudgetWarningThresholds: parseEnvCSVInts(getEnv("GATEWAY_BUDGET_WARNING_THRESHOLDS", "50,80,95")),
			BudgetHistoryLimit:      getEnvInt("GATEWAY_BUDGET_HISTORY_LIMIT", 20),
		},
		Cache: CacheConfig{
			DefaultTTL: getEnvDuration("GATEWAY_CACHE_TTL", 5*time.Minute),
			Backend:    getEnv("GATEWAY_CACHE_BACKEND", "memory"),
			Semantic: SemanticCacheConfig{
				Enabled:                          getEnvBool("GATEWAY_SEMANTIC_CACHE_ENABLED", false),
				Backend:                          getEnv("GATEWAY_SEMANTIC_CACHE_BACKEND", "memory"),
				DefaultTTL:                       getEnvDuration("GATEWAY_SEMANTIC_CACHE_TTL", 24*time.Hour),
				MinSimilarity:                    getEnvFloat64("GATEWAY_SEMANTIC_CACHE_MIN_SIMILARITY", 0.92),
				MaxEntries:                       getEnvInt("GATEWAY_SEMANTIC_CACHE_MAX_ENTRIES", 10_000),
				MaxTextChars:                     getEnvInt("GATEWAY_SEMANTIC_CACHE_MAX_TEXT_CHARS", 8_000),
				Embedder:                         getEnv("GATEWAY_SEMANTIC_CACHE_EMBEDDER", "local_simple"),
				EmbedderProvider:                 getEnv("GATEWAY_SEMANTIC_CACHE_EMBEDDER_PROVIDER", ""),
				EmbedderModel:                    getEnv("GATEWAY_SEMANTIC_CACHE_EMBEDDER_MODEL", ""),
				EmbedderBaseURL:                  getEnv("GATEWAY_SEMANTIC_CACHE_EMBEDDER_BASE_URL", ""),
				EmbedderAPIKey:                   getEnv("GATEWAY_SEMANTIC_CACHE_EMBEDDER_API_KEY", ""),
				EmbedderTimeout:                  getEnvDuration("GATEWAY_SEMANTIC_CACHE_EMBEDDER_TIMEOUT", 30*time.Second),
				PostgresVectorMode:               getEnv("GATEWAY_SEMANTIC_CACHE_POSTGRES_VECTOR_MODE", "auto"),
				PostgresVectorCandidates:         getEnvInt("GATEWAY_SEMANTIC_CACHE_POSTGRES_VECTOR_CANDIDATES", 200),
				PostgresVectorIndexMode:          getEnv("GATEWAY_SEMANTIC_CACHE_POSTGRES_VECTOR_INDEX_MODE", "auto"),
				PostgresVectorIndexType:          getEnv("GATEWAY_SEMANTIC_CACHE_POSTGRES_VECTOR_INDEX_TYPE", "hnsw"),
				PostgresVectorHNSWM:              getEnvInt("GATEWAY_SEMANTIC_CACHE_POSTGRES_VECTOR_HNSW_M", 16),
				PostgresVectorHNSWEfConstruction: getEnvInt("GATEWAY_SEMANTIC_CACHE_POSTGRES_VECTOR_HNSW_EF_CONSTRUCTION", 64),
				PostgresVectorIVFFlatLists:       getEnvInt("GATEWAY_SEMANTIC_CACHE_POSTGRES_VECTOR_IVFFLAT_LISTS", 100),
				PostgresVectorSearchEf:           getEnvInt("GATEWAY_SEMANTIC_CACHE_POSTGRES_VECTOR_SEARCH_EF", 80),
				PostgresVectorSearchProbes:       getEnvInt("GATEWAY_SEMANTIC_CACHE_POSTGRES_VECTOR_SEARCH_PROBES", 10),
			},
		},
		Retention: RetentionConfig{
			Enabled:         getEnvBool("GATEWAY_RETENTION_ENABLED", false),
			HistoryBackend:  getEnv("GATEWAY_RETENTION_HISTORY_BACKEND", "memory"),
			Interval:        getEnvDuration("GATEWAY_RETENTION_INTERVAL", 15*time.Minute),
			TraceSnapshots:  loadRetentionPolicyFromEnv("GATEWAY_RETENTION_TRACES_", 24*time.Hour, 2000),
			BudgetEvents:    loadRetentionPolicyFromEnv("GATEWAY_RETENTION_BUDGET_EVENTS_", 30*24*time.Hour, 200),
			AuditEvents:     loadRetentionPolicyFromEnv("GATEWAY_RETENTION_AUDIT_EVENTS_", 30*24*time.Hour, 500),
			ExactCache:      loadRetentionPolicyFromEnv("GATEWAY_RETENTION_EXACT_CACHE_", 24*time.Hour, 10_000),
			SemanticCache:   loadRetentionPolicyFromEnv("GATEWAY_RETENTION_SEMANTIC_CACHE_", 7*24*time.Hour, 10_000),
			ProviderHistory: loadRetentionPolicyFromEnv("GATEWAY_RETENTION_PROVIDER_HISTORY_", 7*24*time.Hour, 10_000),
			// agent.turn.completed events accumulate fast on long
			// agent runs (one per LLM round-trip). 7d/100k is a
			// generous default for a single-tenant operator; tune
			// down on busy multi-tenant deployments.
			TurnEvents: loadRetentionPolicyFromEnv("GATEWAY_RETENTION_TURN_EVENTS_", 7*24*time.Hour, 100_000),
		},
		Postgres: PostgresConfig{
			DSN:          getEnv("POSTGRES_DSN", ""),
			Schema:       getEnv("POSTGRES_SCHEMA", "public"),
			TablePrefix:  getEnv("POSTGRES_TABLE_PREFIX", "hecate"),
			MaxOpenConns: getEnvInt("POSTGRES_MAX_OPEN_CONNS", 10),
			MaxIdleConns: getEnvInt("POSTGRES_MAX_IDLE_CONNS", 5),
		},
		SQLite: SQLiteConfig{
			Path:        getEnv("GATEWAY_SQLITE_PATH", ".data/hecate.db"),
			TablePrefix: getEnv("GATEWAY_SQLITE_TABLE_PREFIX", "hecate"),
			BusyTimeout: getEnvDuration("GATEWAY_SQLITE_BUSY_TIMEOUT", 5*time.Second),
		},
		Providers: providersCfg,
		Pricebook: loadPricebookFromEnv(),
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

	validateBackend("GATEWAY_CONTROL_PLANE_BACKEND", c.Server.ControlPlaneBackend, "memory", "sqlite", "postgres")
	validateBackend("GATEWAY_TASKS_BACKEND", c.Server.TasksBackend, "memory", "sqlite", "postgres")
	validateBackend("GATEWAY_TASK_QUEUE_BACKEND", c.Server.TaskQueueBackend, "memory", "sqlite", "postgres")
	validateBackend("GATEWAY_CHAT_SESSIONS_BACKEND", c.Chat.SessionsBackend, "memory", "sqlite", "postgres")
	validateBackend("GATEWAY_CACHE_BACKEND", c.Cache.Backend, "memory", "sqlite", "postgres")
	validateBackend("GATEWAY_BUDGET_BACKEND", c.Governor.BudgetBackend, "memory", "sqlite", "postgres")
	validateBackend("GATEWAY_RETENTION_HISTORY_BACKEND", c.Retention.HistoryBackend, "memory", "sqlite", "postgres")
	validateBackend("GATEWAY_SEMANTIC_CACHE_BACKEND", c.Cache.Semantic.Backend, "memory", "sqlite", "postgres")
	validateBackend("GATEWAY_PROVIDER_HISTORY_BACKEND", c.Provider.HistoryBackend, "memory", "sqlite", "postgres")

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

	if postgresRequired(c) && strings.TrimSpace(c.Postgres.DSN) == "" {
		errs = append(errs, errors.New("POSTGRES_DSN is required when any backend is postgres"))
	}
	if c.Cache.Semantic.Enabled && strings.EqualFold(strings.TrimSpace(c.Cache.Semantic.Backend), "sqlite") {
		errs = append(errs, errors.New("GATEWAY_SEMANTIC_CACHE_BACKEND=sqlite is unsupported; use memory or postgres"))
	}
	if c.Retention.Enabled && c.Retention.Interval <= 0 {
		errs = append(errs, errors.New("GATEWAY_RETENTION_INTERVAL must be positive when retention is enabled"))
	}
	for label, policy := range map[string]RetentionPolicy{
		"GATEWAY_RETENTION_TRACES":           c.Retention.TraceSnapshots,
		"GATEWAY_RETENTION_BUDGET_EVENTS":    c.Retention.BudgetEvents,
		"GATEWAY_RETENTION_AUDIT_EVENTS":     c.Retention.AuditEvents,
		"GATEWAY_RETENTION_EXACT_CACHE":      c.Retention.ExactCache,
		"GATEWAY_RETENTION_SEMANTIC_CACHE":   c.Retention.SemanticCache,
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
	if c.Server.RateLimit.Enabled && c.Server.RateLimit.RequestsPerMinute <= 0 {
		errs = append(errs, errors.New("GATEWAY_RATE_LIMIT_RPM must be positive when rate limiting is enabled"))
	}
	if c.Server.RateLimit.BurstSize < 0 {
		errs = append(errs, errors.New("GATEWAY_RATE_LIMIT_BURST must be zero or positive"))
	}
	if c.Cache.Semantic.MinSimilarity < 0 || c.Cache.Semantic.MinSimilarity > 1 {
		errs = append(errs, errors.New("GATEWAY_SEMANTIC_CACHE_MIN_SIMILARITY must be between 0 and 1"))
	}
	if c.Cache.Semantic.MaxEntries < 0 {
		errs = append(errs, errors.New("GATEWAY_SEMANTIC_CACHE_MAX_ENTRIES must be zero or positive"))
	}
	if c.Cache.Semantic.MaxTextChars < 0 {
		errs = append(errs, errors.New("GATEWAY_SEMANTIC_CACHE_MAX_TEXT_CHARS must be zero or positive"))
	}

	for _, item := range durationEnvKeys() {
		if raw := strings.TrimSpace(os.Getenv(item)); raw != "" {
			if _, err := time.ParseDuration(raw); err != nil {
				errs = append(errs, fmt.Errorf("%s must be a valid Go duration: %w", item, err))
			}
		}
	}

	return errors.Join(errs...)
}

func postgresRequired(cfg Config) bool {
	return cfg.Cache.Backend == "postgres" ||
		(cfg.Cache.Semantic.Enabled && cfg.Cache.Semantic.Backend == "postgres") ||
		cfg.Governor.BudgetBackend == "postgres" ||
		cfg.Server.ControlPlaneBackend == "postgres" ||
		cfg.Chat.SessionsBackend == "postgres" ||
		cfg.Server.TasksBackend == "postgres" ||
		cfg.Server.TaskQueueBackend == "postgres" ||
		cfg.Provider.HistoryBackend == "postgres" ||
		cfg.Retention.HistoryBackend == "postgres"
}

func durationEnvKeys() []string {
	return []string{
		"GATEWAY_TASK_MCP_CLIENT_CACHE_PING_INTERVAL",
		"GATEWAY_TASK_MCP_CLIENT_CACHE_PING_TIMEOUT",
		"GATEWAY_TASK_HTTP_TIMEOUT",
		"GATEWAY_PROVIDER_RETRY_BACKOFF",
		"GATEWAY_PROVIDER_HEALTH_COOLDOWN",
		"GATEWAY_PROVIDER_HEALTH_LATENCY_DEGRADED_THRESHOLD",
		"GATEWAY_OTEL_TRACES_TIMEOUT",
		"GATEWAY_OTEL_METRICS_TIMEOUT",
		"GATEWAY_OTEL_LOGS_TIMEOUT",
		"GATEWAY_OTEL_METRICS_INTERVAL",
		"GATEWAY_CACHE_TTL",
		"GATEWAY_SEMANTIC_CACHE_TTL",
		"GATEWAY_SEMANTIC_CACHE_EMBEDDER_TIMEOUT",
		"GATEWAY_RETENTION_INTERVAL",
		"GATEWAY_RETENTION_TRACES_MAX_AGE",
		"GATEWAY_RETENTION_BUDGET_EVENTS_MAX_AGE",
		"GATEWAY_RETENTION_AUDIT_EVENTS_MAX_AGE",
		"GATEWAY_RETENTION_EXACT_CACHE_MAX_AGE",
		"GATEWAY_RETENTION_SEMANTIC_CACHE_MAX_AGE",
		"GATEWAY_RETENTION_PROVIDER_HISTORY_MAX_AGE",
		"GATEWAY_RETENTION_TURN_EVENTS_MAX_AGE",
		"GATEWAY_SQLITE_BUSY_TIMEOUT",
		"GATEWAY_PRICEBOOK_AUTO_IMPORT_INTERVAL",
	}
}

func loadRetentionPolicyFromEnv(prefix string, defaultAge time.Duration, defaultCount int) RetentionPolicy {
	return RetentionPolicy{
		MaxAge:   getEnvDuration(prefix+"MAX_AGE", defaultAge),
		MaxCount: getEnvInt(prefix+"MAX_COUNT", defaultCount),
	}
}

func loadPricebookFromEnv() PricebookConfig {
	cfg := defaultPricebookConfig()
	cfg.AutoImportInterval = getEnvDuration("GATEWAY_PRICEBOOK_AUTO_IMPORT_INTERVAL", 0)
	return cfg
}

func defaultPricebookConfig() PricebookConfig {
	return PricebookConfig{
		UnknownModelPolicy: "error",
		Entries: []ModelPriceConfig{
			// Seeded from OpenAI's published API pricing/model docs as of 2026-04-23.
			// Keep this list small and explicit for sane defaults, but this is not a long-term
			// source of truth. Hecate still needs a proper pricebook ingestion/update path.
			// Source: https://developers.openai.com/api/docs/models
			{
				Provider:                             "openai",
				Model:                                "gpt-5.4",
				InputMicrosUSDPerMillionTokens:       2_500_000,
				OutputMicrosUSDPerMillionTokens:      15_000_000,
				CachedInputMicrosUSDPerMillionTokens: 250_000,
			},
			{
				Provider:                             "openai",
				Model:                                "gpt-5.4-mini",
				InputMicrosUSDPerMillionTokens:       750_000,
				OutputMicrosUSDPerMillionTokens:      4_500_000,
				CachedInputMicrosUSDPerMillionTokens: 75_000,
			},
			{
				Provider:                             "openai",
				Model:                                "gpt-5.4-nano",
				InputMicrosUSDPerMillionTokens:       200_000,
				OutputMicrosUSDPerMillionTokens:      1_250_000,
				CachedInputMicrosUSDPerMillionTokens: 20_000,
			},
			{
				Provider:                             "openai",
				Model:                                "gpt-4.1",
				InputMicrosUSDPerMillionTokens:       2_000_000,
				OutputMicrosUSDPerMillionTokens:      8_000_000,
				CachedInputMicrosUSDPerMillionTokens: 500_000,
			},
			{
				Provider:                             "openai",
				Model:                                "gpt-4.1-mini",
				InputMicrosUSDPerMillionTokens:       400_000,
				OutputMicrosUSDPerMillionTokens:      1_600_000,
				CachedInputMicrosUSDPerMillionTokens: 100_000,
			},
			{
				Provider:                             "openai",
				Model:                                "gpt-4.1-nano",
				InputMicrosUSDPerMillionTokens:       100_000,
				OutputMicrosUSDPerMillionTokens:      400_000,
				CachedInputMicrosUSDPerMillionTokens: 25_000,
			},
			{
				Provider:                             "openai",
				Model:                                "gpt-4o",
				InputMicrosUSDPerMillionTokens:       2_500_000,
				OutputMicrosUSDPerMillionTokens:      10_000_000,
				CachedInputMicrosUSDPerMillionTokens: 1_250_000,
			},
			{
				Provider:                             "openai",
				Model:                                "gpt-4o-mini",
				InputMicrosUSDPerMillionTokens:       150_000,
				OutputMicrosUSDPerMillionTokens:      600_000,
				CachedInputMicrosUSDPerMillionTokens: 75_000,
			},
			{
				Provider: "openai",
				Model:    "omni-moderation",
			},
			{
				Provider: "openai",
				Model:    "omni-moderation-latest",
			},
			{
				Provider: "openai",
				Model:    "text-moderation-latest",
			},
			// Seeded from Anthropic's published pricing/docs as of 2026-04-23.
			// Source: https://www.anthropic.com/claude/sonnet
			// Claude Sonnet 4.6: $3 / MTok input, $15 / MTok output, $0.30 / MTok cache reads.
			{
				Provider:                             "anthropic",
				Model:                                "claude-sonnet-4-6",
				InputMicrosUSDPerMillionTokens:       3_000_000,
				OutputMicrosUSDPerMillionTokens:      15_000_000,
				CachedInputMicrosUSDPerMillionTokens: 300_000,
			},
			// Claude Sonnet 4: $3 / MTok input, $15 / MTok output, $0.30 / MTok cache reads.
			{
				Provider:                             "anthropic",
				Model:                                "claude-sonnet-4-20250514",
				InputMicrosUSDPerMillionTokens:       3_000_000,
				OutputMicrosUSDPerMillionTokens:      15_000_000,
				CachedInputMicrosUSDPerMillionTokens: 300_000,
			},
			// Claude Haiku 3.5: $0.80 / MTok input, $4 / MTok output, $0.08 / MTok cache reads.
			{
				Provider:                             "anthropic",
				Model:                                "claude-haiku-3-5-20241022",
				InputMicrosUSDPerMillionTokens:       800_000,
				OutputMicrosUSDPerMillionTokens:      4_000_000,
				CachedInputMicrosUSDPerMillionTokens: 80_000,
			},
			// Seeded from Groq's published model docs as of 2026-04-23.
			// Source: https://console.groq.com/docs/models
			{
				Provider:                             "groq",
				Model:                                "llama-3.3-70b-versatile",
				InputMicrosUSDPerMillionTokens:       590_000,
				OutputMicrosUSDPerMillionTokens:      790_000,
				CachedInputMicrosUSDPerMillionTokens: 0,
			},
			{
				Provider:                             "groq",
				Model:                                "llama-3.1-8b-instant",
				InputMicrosUSDPerMillionTokens:       50_000,
				OutputMicrosUSDPerMillionTokens:      80_000,
				CachedInputMicrosUSDPerMillionTokens: 0,
			},
			{
				Provider:                             "groq",
				Model:                                "openai/gpt-oss-120b",
				InputMicrosUSDPerMillionTokens:       150_000,
				OutputMicrosUSDPerMillionTokens:      600_000,
				CachedInputMicrosUSDPerMillionTokens: 0,
			},
			{
				Provider:                             "groq",
				Model:                                "openai/gpt-oss-20b",
				InputMicrosUSDPerMillionTokens:       75_000,
				OutputMicrosUSDPerMillionTokens:      300_000,
				CachedInputMicrosUSDPerMillionTokens: 0,
			},
			// Seeded from Google Gemini API pricing docs as of 2026-04-23.
			// Source: https://ai.google.dev/gemini-api/docs/pricing
			{
				Provider:                             "gemini",
				Model:                                "gemini-2.5-flash",
				InputMicrosUSDPerMillionTokens:       300_000,
				OutputMicrosUSDPerMillionTokens:      2_500_000,
				CachedInputMicrosUSDPerMillionTokens: 30_000,
			},
			{
				Provider:                             "gemini",
				Model:                                "gemini-2.5-flash-lite",
				InputMicrosUSDPerMillionTokens:       100_000,
				OutputMicrosUSDPerMillionTokens:      400_000,
				CachedInputMicrosUSDPerMillionTokens: 10_000,
			},
		},
	}
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
	for _, name := range names {
		cfg, ok := providerConfigFromEnv(name)
		if !ok {
			continue
		}
		items = append(items, cfg)
	}
	normalizeProviders(items)
	return ProvidersConfig{OpenAICompatible: items}
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
	// or POST /admin/control-plane/providers.
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
		Timeout:      30 * time.Second,
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
			items[i].Timeout = 30 * time.Second
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
