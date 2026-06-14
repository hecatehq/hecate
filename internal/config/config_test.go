package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDefaultProviderTimeoutBranchesOnKind(t *testing.T) {
	// Local LLM servers (LMStudio, Ollama) can spend 30–120s loading a
	// model before the first token comes back. The previous 30s default
	// tripped agent loops on cold local models with
	// `context deadline exceeded`. Cloud providers stay on a tighter
	// budget — p99 chat completions don't approach 60s.
	cases := []struct {
		kind string
		want time.Duration
	}{
		{"local", 5 * time.Minute},
		{"LOCAL", 5 * time.Minute},     // case-insensitive
		{"  local  ", 5 * time.Minute}, // whitespace-tolerant
		{"cloud", 60 * time.Second},
		{"", 60 * time.Second},       // unset → cloud default
		{"hosted", 60 * time.Second}, // unknown kind → cloud default (safer)
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			got := DefaultProviderTimeout(tc.kind)
			if got != tc.want {
				t.Fatalf("DefaultProviderTimeout(%q) = %v, want %v", tc.kind, got, tc.want)
			}
		})
	}
}

func TestLoadFromEnvBackendFansOutToDurableStores(t *testing.T) {
	t.Setenv("HECATE_BACKEND", " Postgres ")
	t.Setenv("HECATE_POSTGRES_URL", "postgres://hecate:hecate@localhost:5432/hecate?sslmode=disable")
	t.Setenv("HECATE_POSTGRES_TABLE_PREFIX", "dogfood")

	cfg := LoadFromEnv()
	got := []string{
		cfg.Server.ControlPlaneBackend,
		cfg.Server.TasksBackend,
		cfg.Server.TaskQueueBackend,
		cfg.Provider.HistoryBackend,
		cfg.Chat.SessionsBackend,
		cfg.Projects.Backend,
		cfg.Governor.UsageBackend,
		cfg.Retention.HistoryBackend,
	}
	for _, backend := range got {
		if backend != "postgres" {
			t.Fatalf("backend fanout = %#v, want all postgres", got)
		}
	}
	if cfg.Postgres.DatabaseURL != "postgres://hecate:hecate@localhost:5432/hecate?sslmode=disable" {
		t.Fatalf("Postgres.DatabaseURL = %q, want configured URL", cfg.Postgres.DatabaseURL)
	}
	if cfg.Postgres.TablePrefix != "dogfood" {
		t.Fatalf("Postgres.TablePrefix = %q, want dogfood", cfg.Postgres.TablePrefix)
	}
}

func TestValidateRequiresPostgresURLForEveryBackendSelector(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"control plane", func(c *Config) { c.Server.ControlPlaneBackend = "postgres" }},
		{"tasks", func(c *Config) { c.Server.TasksBackend = "postgres" }},
		{"task queue", func(c *Config) { c.Server.TaskQueueBackend = "postgres" }},
		{"chat sessions", func(c *Config) { c.Chat.SessionsBackend = "postgres" }},
		{"projects bundle", func(c *Config) { c.Projects.Backend = "postgres" }},
		{"usage", func(c *Config) { c.Governor.UsageBackend = "postgres" }},
		{"retention history", func(c *Config) { c.Retention.HistoryBackend = "postgres" }},
		{"provider history", func(c *Config) { c.Provider.HistoryBackend = "postgres" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HECATE_BACKEND", "memory")
			t.Setenv("HECATE_POSTGRES_URL", "")
			t.Setenv("DATABASE_URL", "")

			cfg := LoadFromEnv()
			cfg.Postgres.DatabaseURL = ""
			tc.mutate(&cfg)

			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), "HECATE_POSTGRES_URL or DATABASE_URL") {
				t.Fatalf("Validate() error = %v, want missing Postgres URL", err)
			}
		})
	}
}

func TestLoadFromEnvTraceBodyModeDefaultsToMetadata(t *testing.T) {
	cfg := LoadFromEnv()
	if cfg.Server.TraceBodyMode != "metadata" {
		t.Fatalf("TraceBodyMode = %q, want metadata", cfg.Server.TraceBodyMode)
	}
}

func TestLoadFromEnvNonLoopbackBindAcknowledgement(t *testing.T) {
	t.Setenv("HECATE_ALLOW_NON_LOOPBACK_BIND", "true")

	cfg := LoadFromEnv()
	if !cfg.Server.AllowNonLoopbackBind {
		t.Fatal("AllowNonLoopbackBind = false, want true")
	}
}

func TestLoadFromEnvNonLoopbackBindAcknowledgementZeroStaysFalse(t *testing.T) {
	t.Setenv("HECATE_ALLOW_NON_LOOPBACK_BIND", "0")

	cfg := LoadFromEnv()
	if cfg.Server.AllowNonLoopbackBind {
		t.Fatal("AllowNonLoopbackBind = true for HECATE_ALLOW_NON_LOOPBACK_BIND=0, want false")
	}
}

func TestLoadFromEnvUnsafeEmbeddedTerminalOptIn(t *testing.T) {
	t.Setenv("HECATE_UNSAFE_ENABLE_EMBEDDED_TERMINAL", "")

	cfg := LoadFromEnv()
	if cfg.Server.UnsafeEnableEmbeddedTerminal {
		t.Fatal("UnsafeEnableEmbeddedTerminal = true by default, want false")
	}

	t.Setenv("HECATE_UNSAFE_ENABLE_EMBEDDED_TERMINAL", "true")

	cfg = LoadFromEnv()
	if !cfg.Server.UnsafeEnableEmbeddedTerminal {
		t.Fatal("UnsafeEnableEmbeddedTerminal = false with opt-in env, want true")
	}
}

func TestLoadFromEnvAllowedOrigins(t *testing.T) {
	t.Setenv("HECATE_ALLOWED_ORIGINS", "http://127.0.0.1:5173, http://localhost:5173")

	cfg := LoadFromEnv()
	want := []string{"http://127.0.0.1:5173", "http://localhost:5173"}
	if !reflect.DeepEqual(cfg.Server.AllowedOrigins, want) {
		t.Fatalf("AllowedOrigins = %#v, want %#v", cfg.Server.AllowedOrigins, want)
	}
}

func TestLoadFromEnvRuntimeToken(t *testing.T) {
	t.Setenv("HECATE_RUNTIME_TOKEN", "local-runtime-token-123456")

	cfg := LoadFromEnv()
	if cfg.Server.RuntimeToken != "local-runtime-token-123456" {
		t.Fatalf("RuntimeToken = %q, want configured token", cfg.Server.RuntimeToken)
	}
}

func TestLoadFromEnvInferenceToken(t *testing.T) {
	t.Setenv("HECATE_INFERENCE_TOKEN", "local-inference-token-123456")

	cfg := LoadFromEnv()
	if cfg.Server.InferenceToken != "local-inference-token-123456" {
		t.Fatalf("InferenceToken = %q, want configured token", cfg.Server.InferenceToken)
	}
}

func TestLoadFromEnvCloudRuntimeMode(t *testing.T) {
	t.Setenv("HECATE_CLOUD_RUNTIME_MODE", "true")
	t.Setenv("HECATE_CLOUD_RUNTIME_SECRET", "cloud-runtime-secret-123456")

	cfg := LoadFromEnv()
	if !cfg.Server.CloudRuntimeMode {
		t.Fatal("CloudRuntimeMode = false, want true")
	}
	if cfg.Server.CloudRuntimeSecret != "cloud-runtime-secret-123456" {
		t.Fatalf("CloudRuntimeSecret = %q, want configured secret", cfg.Server.CloudRuntimeSecret)
	}
}

func TestLoadFromEnvCloudRuntimeDisablesLocalProvidersByDefault(t *testing.T) {
	t.Setenv("HECATE_CLOUD_RUNTIME_MODE", "true")
	t.Setenv("HECATE_CLOUD_RUNTIME_SECRET", "cloud-runtime-secret-123456")

	cfg := LoadFromEnv()
	if cfg.LocalProvidersAllowed() {
		t.Fatal("LocalProvidersAllowed() = true, want false in cloud runtime mode by default")
	}
	if !reflect.DeepEqual(cfg.Governor.AllowedProviderKinds, []string{"cloud"}) {
		t.Fatalf("AllowedProviderKinds = %#v, want [cloud]", cfg.Governor.AllowedProviderKinds)
	}
}

func TestLoadFromEnvCloudRuntimeCanOptIntoLocalProviders(t *testing.T) {
	t.Setenv("HECATE_CLOUD_RUNTIME_MODE", "true")
	t.Setenv("HECATE_CLOUD_RUNTIME_SECRET", "cloud-runtime-secret-123456")
	t.Setenv("HECATE_CLOUD_ALLOW_LOCAL_PROVIDERS", "true")

	cfg := LoadFromEnv()
	if !cfg.LocalProvidersAllowed() {
		t.Fatal("LocalProvidersAllowed() = false, want true with explicit cloud opt-in")
	}
	if len(cfg.Governor.AllowedProviderKinds) != 0 {
		t.Fatalf("AllowedProviderKinds = %#v, want unset when local providers are explicitly allowed", cfg.Governor.AllowedProviderKinds)
	}
}

func TestListenAddressIsLoopback(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		address string
		want    bool
	}{
		{name: "ipv4 loopback", address: "127.0.0.1:8765", want: true},
		{name: "ipv6 loopback", address: "[::1]:8765", want: true},
		{name: "localhost", address: "localhost:8765", want: true},
		{name: "wildcard ipv4", address: "0.0.0.0:8765", want: false},
		{name: "wildcard ipv6", address: "[::]:8765", want: false},
		{name: "empty host", address: ":8765", want: false},
		{name: "public ip", address: "203.0.113.10:8765", want: false},
		{name: "host name", address: "hecate.example.com:8765", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ListenAddressIsLoopback(tc.address); got != tc.want {
				t.Fatalf("ListenAddressIsLoopback(%q) = %v, want %v", tc.address, got, tc.want)
			}
		})
	}
}

func TestServerConfigValidateNetworkExposure(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		cfg     ServerConfig
		wantErr bool
	}{
		{name: "loopback default", cfg: ServerConfig{Address: "127.0.0.1:8765"}},
		{name: "empty host without acknowledgement", cfg: ServerConfig{Address: ":8765"}, wantErr: true},
		{name: "wildcard without acknowledgement", cfg: ServerConfig{Address: "0.0.0.0:8765"}, wantErr: true},
		{name: "wildcard with acknowledgement", cfg: ServerConfig{Address: "0.0.0.0:8765", AllowNonLoopbackBind: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.cfg.ValidateNetworkExposure()
			if tc.wantErr && err == nil {
				t.Fatal("ValidateNetworkExposure() error = nil, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateNetworkExposure() error = %v, want nil", err)
			}
		})
	}
}

func TestLoadFromEnvTraceBodyModeOverride(t *testing.T) {
	t.Setenv("HECATE_TRACE_BODY_MODE", "redacted_text")

	cfg := LoadFromEnv()
	if cfg.Server.TraceBodyMode != "redacted_text" {
		t.Fatalf("TraceBodyMode = %q, want redacted_text", cfg.Server.TraceBodyMode)
	}
}

func TestLoadFromEnvOTelSharedDefaults(t *testing.T) {
	t.Setenv("HECATE_OTEL_ENDPOINT", "http://collector:4318")
	t.Setenv("HECATE_OTEL_HEADERS", "x-api-key=secret,tenant=local")
	t.Setenv("HECATE_OTEL_TIMEOUT", "9s")
	t.Setenv("HECATE_OTEL_TRANSPORT", "http")
	t.Setenv("HECATE_OTEL_TRACES_ENABLED", "true")
	t.Setenv("HECATE_OTEL_METRICS_ENABLED", "true")
	t.Setenv("HECATE_OTEL_LOGS_ENABLED", "true")

	cfg := LoadFromEnv()
	if cfg.OTel.Endpoint != "http://collector:4318" {
		t.Fatalf("shared endpoint = %q, want http://collector:4318", cfg.OTel.Endpoint)
	}
	if cfg.OTel.Traces.Endpoint != "http://collector:4318/v1/traces" {
		t.Fatalf("traces endpoint = %q", cfg.OTel.Traces.Endpoint)
	}
	if cfg.OTel.Metrics.Endpoint != "http://collector:4318/v1/metrics" {
		t.Fatalf("metrics endpoint = %q", cfg.OTel.Metrics.Endpoint)
	}
	if cfg.OTel.Logs.Endpoint != "http://collector:4318/v1/logs" {
		t.Fatalf("logs endpoint = %q", cfg.OTel.Logs.Endpoint)
	}
	if cfg.OTel.Traces.Headers["x-api-key"] != "secret" || cfg.OTel.Metrics.Headers["tenant"] != "local" {
		t.Fatalf("signal headers did not inherit shared headers: %#v %#v", cfg.OTel.Traces.Headers, cfg.OTel.Metrics.Headers)
	}
	if cfg.OTel.Traces.Timeout != 9*time.Second || cfg.OTel.Metrics.Timeout != 9*time.Second || cfg.OTel.Logs.Timeout != 9*time.Second {
		t.Fatalf("signal timeouts did not inherit shared timeout: traces=%s metrics=%s logs=%s", cfg.OTel.Traces.Timeout, cfg.OTel.Metrics.Timeout, cfg.OTel.Logs.Timeout)
	}
}

func TestLoadFromEnvOTelGRPCSharedEndpoint(t *testing.T) {
	t.Setenv("HECATE_OTEL_ENDPOINT", "http://collector:4317")
	t.Setenv("HECATE_OTEL_TRANSPORT", "grpc")
	t.Setenv("HECATE_OTEL_METRICS_ENDPOINT", "https://metrics-collector:4317")
	t.Setenv("HECATE_OTEL_METRICS_TRANSPORT", "grpc")
	t.Setenv("HECATE_OTEL_METRICS_EXEMPLAR_FILTER", "always_on")

	cfg := LoadFromEnv()
	if cfg.OTel.Traces.Endpoint != "http://collector:4317" || cfg.OTel.Traces.Transport != "grpc" {
		t.Fatalf("traces config = endpoint %q transport %q", cfg.OTel.Traces.Endpoint, cfg.OTel.Traces.Transport)
	}
	if cfg.OTel.Metrics.Endpoint != "https://metrics-collector:4317" || cfg.OTel.Metrics.Transport != "grpc" {
		t.Fatalf("metrics config = endpoint %q transport %q", cfg.OTel.Metrics.Endpoint, cfg.OTel.Metrics.Transport)
	}
	if cfg.OTel.MetricsExemplarFilter != "always_on" {
		t.Fatalf("metrics exemplar filter = %q, want always_on", cfg.OTel.MetricsExemplarFilter)
	}
}

func TestLoadFromEnvOTelLogsFallbackToTraceSignal(t *testing.T) {
	t.Setenv("HECATE_OTEL_TRACES_ENDPOINT", "127.0.0.1:4317")
	t.Setenv("HECATE_OTEL_TRACES_TRANSPORT", "grpc")
	t.Setenv("HECATE_OTEL_TRACES_HEADERS", "trace=true")

	cfg := LoadFromEnv()
	if cfg.OTel.Logs.Endpoint != "127.0.0.1:4317" {
		t.Fatalf("logs endpoint = %q, want trace endpoint fallback", cfg.OTel.Logs.Endpoint)
	}
	if cfg.OTel.Logs.Transport != "grpc" {
		t.Fatalf("logs transport = %q, want grpc fallback", cfg.OTel.Logs.Transport)
	}
	if cfg.OTel.Logs.Headers["trace"] != "true" {
		t.Fatalf("logs headers = %#v, want trace header fallback", cfg.OTel.Logs.Headers)
	}
}

func TestValidateAcceptsDefaultConfig(t *testing.T) {
	cfg := LoadFromEnv()

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() default config error = %v, want nil", err)
	}
}

func TestValidateRejectsInvalidOTelTransport(t *testing.T) {
	t.Setenv("HECATE_OTEL_TRANSPORT", "smtp")
	cfg := LoadFromEnv()

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want invalid OTel transport error")
	}
	if !strings.Contains(err.Error(), "HECATE_OTEL_TRANSPORT") {
		t.Fatalf("Validate() error = %q, want HECATE_OTEL_TRANSPORT", err)
	}
}

func TestValidateRejectsInvalidOTelMetricsExemplarFilter(t *testing.T) {
	t.Setenv("HECATE_OTEL_METRICS_EXEMPLAR_FILTER", "sometimes")
	cfg := LoadFromEnv()

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want invalid OTel metrics exemplar filter error")
	}
	if !strings.Contains(err.Error(), "HECATE_OTEL_METRICS_EXEMPLAR_FILTER") {
		t.Fatalf("Validate() error = %q, want HECATE_OTEL_METRICS_EXEMPLAR_FILTER", err)
	}
}

func TestValidateRejectsInvalidTraceBodyMode(t *testing.T) {
	t.Setenv("HECATE_TRACE_BODY_MODE", "raw")
	cfg := LoadFromEnv()

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want invalid trace body mode error")
	}
	if !strings.Contains(err.Error(), "HECATE_TRACE_BODY_MODE") {
		t.Fatalf("Validate() error = %q, want HECATE_TRACE_BODY_MODE", err)
	}
}

func TestValidateRejectsInvalidBackendNames(t *testing.T) {
	cfg := LoadFromEnv()
	cfg.Server.ControlPlaneBackend = "redis"
	cfg.Projects.Backend = "sqlite"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want invalid backend error")
	}
	if !strings.Contains(err.Error(), "HECATE_BACKEND") {
		t.Fatalf("Validate() error = %q, want HECATE_BACKEND", err)
	}
}

func TestValidateRejectsPostgresBackendWithoutDatabaseURL(t *testing.T) {
	cfg := LoadFromEnv()
	cfg.Server.ControlPlaneBackend = "postgres"
	cfg.Postgres.DatabaseURL = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want missing postgres URL error")
	}
	if !strings.Contains(err.Error(), "HECATE_POSTGRES_URL") {
		t.Fatalf("Validate() error = %q, want HECATE_POSTGRES_URL", err)
	}
}

func TestValidateRejectsInvalidPublicURL(t *testing.T) {
	cfg := LoadFromEnv()
	cfg.Server.PublicURL = "file:///tmp/hecate"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want invalid public URL error")
	}
	if !strings.Contains(err.Error(), "HECATE_PUBLIC_URL") {
		t.Fatalf("Validate() error = %q, want HECATE_PUBLIC_URL", err)
	}
}

func TestValidateRejectsInvalidAllowedOrigin(t *testing.T) {
	cfg := LoadFromEnv()
	cfg.Server.AllowedOrigins = []string{"http://localhost:5173/app"}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want invalid allowed origin error")
	}
	if !strings.Contains(err.Error(), "HECATE_ALLOWED_ORIGINS") {
		t.Fatalf("Validate() error = %q, want HECATE_ALLOWED_ORIGINS", err)
	}
}

func TestValidateRejectsShortRuntimeToken(t *testing.T) {
	cfg := LoadFromEnv()
	cfg.Server.RuntimeToken = "short"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want invalid runtime token error")
	}
	if !strings.Contains(err.Error(), "HECATE_RUNTIME_TOKEN") {
		t.Fatalf("Validate() error = %q, want HECATE_RUNTIME_TOKEN", err)
	}
}

func TestValidateRejectsShortInferenceToken(t *testing.T) {
	cfg := LoadFromEnv()
	cfg.Server.InferenceToken = "short"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want invalid inference token error")
	}
	if !strings.Contains(err.Error(), "HECATE_INFERENCE_TOKEN") {
		t.Fatalf("Validate() error = %q, want HECATE_INFERENCE_TOKEN", err)
	}
}

func TestValidateRejectsCloudRuntimeModeWithoutStrongSecret(t *testing.T) {
	cfg := LoadFromEnv()
	cfg.Server.CloudRuntimeMode = true
	cfg.Server.CloudRuntimeSecret = "short"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want invalid cloud runtime secret error")
	}
	if !strings.Contains(err.Error(), "HECATE_CLOUD_RUNTIME_SECRET") {
		t.Fatalf("Validate() error = %q, want HECATE_CLOUD_RUNTIME_SECRET", err)
	}
}

func TestValidateAllowsCloudRuntimeSecretOnlyWhenCloudModeDisabled(t *testing.T) {
	cfg := LoadFromEnv()
	cfg.Server.CloudRuntimeMode = false
	cfg.Server.CloudRuntimeSecret = "short"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil when cloud runtime mode is disabled", err)
	}
}

func TestValidateRejectsLocalProviderKindInCloudRuntimeWithoutOptIn(t *testing.T) {
	cfg := LoadFromEnv()
	cfg.Server.CloudRuntimeMode = true
	cfg.Server.CloudRuntimeSecret = "cloud-runtime-secret-123456"
	cfg.Server.CloudAllowLocalProviders = false
	cfg.Governor.AllowedProviderKinds = []string{"local"}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want local-provider cloud opt-in error")
	}
	if !strings.Contains(err.Error(), "HECATE_CLOUD_ALLOW_LOCAL_PROVIDERS") {
		t.Fatalf("Validate() error = %q, want HECATE_CLOUD_ALLOW_LOCAL_PROVIDERS", err)
	}
}

func TestValidateRejectsInvalidDurationEnvValues(t *testing.T) {
	t.Setenv("HECATE_RETENTION_INTERVAL", "tomorrow-ish")
	cfg := LoadFromEnv()

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want invalid duration error")
	}
	if !strings.Contains(err.Error(), "HECATE_RETENTION_INTERVAL") {
		t.Fatalf("Validate() error = %q, want HECATE_RETENTION_INTERVAL", err)
	}
}

func TestValidateRejectsImpossibleRuntimeValues(t *testing.T) {
	cfg := LoadFromEnv()
	cfg.Retention.Enabled = true
	cfg.Retention.Interval = 0
	cfg.Retention.TraceSnapshots.MaxAge = -time.Second
	cfg.Retention.TraceSnapshots.MaxCount = -1
	cfg.Provider.MaxAttempts = 0
	cfg.Provider.HealthThreshold = -1
	cfg.Provider.HealthLatencyDegradedThreshold = -time.Millisecond
	cfg.Provider.HistoryLimit = -1
	cfg.Server.TaskQueueWorkers = 0
	cfg.Server.TaskQueueBuffer = -1
	cfg.Server.ChatMaxTurnsPerSession = -1
	cfg.Server.ChatMaxSessionDuration = -time.Second
	cfg.Server.ChatIdleTimeout = -time.Second
	cfg.Server.RateLimit.Enabled = true
	cfg.Server.RateLimit.RequestsPerMinute = 0
	cfg.Server.RateLimit.BurstSize = -1

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want aggregate validation error")
	}
	for _, want := range []string{
		"HECATE_RETENTION_INTERVAL",
		"HECATE_RETENTION_TRACES_MAX_AGE",
		"HECATE_RETENTION_TRACES_MAX_COUNT",
		"HECATE_PROVIDER_MAX_ATTEMPTS",
		"HECATE_PROVIDER_HEALTH_FAILURE_THRESHOLD",
		"HECATE_PROVIDER_HEALTH_LATENCY_DEGRADED_THRESHOLD",
		"HECATE_PROVIDER_HISTORY_LIMIT",
		"HECATE_TASK_QUEUE_WORKERS",
		"HECATE_TASK_QUEUE_BUFFER",
		"HECATE_CHAT_MAX_TURNS_PER_SESSION",
		"HECATE_CHAT_MAX_SESSION_DURATION",
		"HECATE_CHAT_IDLE_TIMEOUT",
		"HECATE_RATE_LIMIT_RPM",
		"HECATE_RATE_LIMIT_BURST",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Validate() error = %q, want %s", err, want)
		}
	}
}
func TestSplitCSVTrimsAndDropsEmptyValues(t *testing.T) {
	t.Parallel()

	got := splitCSV(" openai, , ollama ,")
	want := []string{"openai", "ollama"}
	if len(got) != len(want) {
		t.Fatalf("len(splitCSV) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitCSV[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLoadProvidersFromEnvUsesGenericProviderPrefixes(t *testing.T) {
	// PROVIDER_<NAME>_PRECONFIGURED=1 is the auto-registration opt-in
	// gate — without it the other env vars are deployment hints only.
	t.Setenv("PROVIDER_OPENAI_PRECONFIGURED", "1")
	t.Setenv("PROVIDER_OPENAI_API_KEY", "openai-secret")
	t.Setenv("PROVIDER_OLLAMA_PRECONFIGURED", "1")
	t.Setenv("PROVIDER_OLLAMA_BASE_URL", "http://127.0.0.1:11434/v1")
	t.Setenv("PROVIDER_ANTHROPIC_PRECONFIGURED", "1")
	t.Setenv("PROVIDER_ANTHROPIC_API_KEY", "anthropic-secret")

	cfg := LoadFromEnv()
	// Only providers with an explicit env var register — three vars set
	// → three providers. Built-ins without env vars stay out of the
	// runtime registry; the explicit-add (CP store) flow is the
	// canonical path for unconfigured providers.
	if len(cfg.Providers.OpenAICompatible) != 3 {
		t.Fatalf("provider count = %d, want 3 (env-only)", len(cfg.Providers.OpenAICompatible))
	}
	openai, ok := testProviderByName(cfg.Providers.OpenAICompatible, "openai")
	if !ok {
		t.Fatal("openai provider missing")
	}
	if openai.APIKey != "openai-secret" {
		t.Fatalf("openai api key = %q, want openai-secret", openai.APIKey)
	}
	ollama, ok := testProviderByName(cfg.Providers.OpenAICompatible, "ollama")
	if !ok {
		t.Fatal("ollama provider missing")
	}
	if ollama.Kind != "local" {
		t.Fatalf("ollama kind = %q, want local", ollama.Kind)
	}
	anthropic, ok := testProviderByName(cfg.Providers.OpenAICompatible, "anthropic")
	if !ok {
		t.Fatal("anthropic provider missing")
	}
	if anthropic.Protocol != "anthropic" {
		t.Fatalf("anthropic protocol = %q, want anthropic", anthropic.Protocol)
	}
}

func TestBuiltInProviderCatalogMetadata(t *testing.T) {
	t.Parallel()

	openai, ok := BuiltInProviderByID("openai")
	if !ok {
		t.Fatal("BuiltInProviderByID(openai) = not found")
	}
	if openai.Protocol != "openai" {
		t.Fatalf("openai protocol = %q, want openai", openai.Protocol)
	}

	anthropic, ok := BuiltInProviderByID("anthropic")
	if !ok {
		t.Fatal("BuiltInProviderByID(anthropic) = not found")
	}
	if anthropic.Protocol != "anthropic" {
		t.Fatalf("anthropic protocol = %q, want anthropic", anthropic.Protocol)
	}

	for _, tc := range []struct {
		id      string
		baseURL string
		env     string
	}{
		{
			id:      "alibaba",
			baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
			env:     "PROVIDER_ALIBABA_API_KEY",
		},
		{
			id:      "cerebras",
			baseURL: "https://api.cerebras.ai/v1",
			env:     "PROVIDER_CEREBRAS_API_KEY",
		},
		{
			id:      "deepinfra",
			baseURL: "https://api.deepinfra.com/v1/openai",
			env:     "PROVIDER_DEEPINFRA_API_KEY",
		},
		{
			id:      "moonshot",
			baseURL: "https://api.moonshot.ai/v1",
			env:     "PROVIDER_MOONSHOT_API_KEY",
		},
		{
			id:      "openrouter",
			baseURL: "https://openrouter.ai/api/v1",
			env:     "PROVIDER_OPENROUTER_API_KEY",
		},
		{
			id:      "requesty",
			baseURL: "https://router.requesty.ai/v1",
			env:     "PROVIDER_REQUESTY_API_KEY",
		},
		{
			id:      "vercel_ai_gateway",
			baseURL: "https://ai-gateway.vercel.sh/v1",
			env:     "PROVIDER_VERCEL_AI_GATEWAY_API_KEY",
		},
	} {
		t.Run(tc.id, func(t *testing.T) {
			provider, ok := BuiltInProviderByID(tc.id)
			if !ok {
				t.Fatalf("BuiltInProviderByID(%s) = not found", tc.id)
			}
			if provider.Protocol != "openai" {
				t.Fatalf("%s protocol = %q, want openai", tc.id, provider.Protocol)
			}
			if provider.BaseURL != tc.baseURL {
				t.Fatalf("%s base url = %q, want %s", tc.id, provider.BaseURL, tc.baseURL)
			}
			if provider.APIKeyEnv != tc.env {
				t.Fatalf("%s api key env = %q, want %s", tc.id, provider.APIKeyEnv, tc.env)
			}
		})
	}

	deepseek, ok := BuiltInProviderByID("deepseek")
	if !ok {
		t.Fatal("BuiltInProviderByID(deepseek) = not found")
	}
	if deepseek.Protocol != "openai" {
		t.Fatalf("deepseek protocol = %q, want openai", deepseek.Protocol)
	}
	if deepseek.BaseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("deepseek base url = %q, want https://api.deepseek.com/v1", deepseek.BaseURL)
	}

	gemini, ok := BuiltInProviderByID("gemini")
	if !ok {
		t.Fatal("BuiltInProviderByID(gemini) = not found")
	}
	if gemini.Protocol != "openai" {
		t.Fatalf("gemini protocol = %q, want openai", gemini.Protocol)
	}
	if gemini.BaseURL != "https://generativelanguage.googleapis.com/v1beta/openai" {
		t.Fatalf("gemini base url = %q, want https://generativelanguage.googleapis.com/v1beta/openai", gemini.BaseURL)
	}

	xai, ok := BuiltInProviderByID("xai")
	if !ok {
		t.Fatal("BuiltInProviderByID(xai) = not found")
	}
	if xai.Protocol != "openai" {
		t.Fatalf("xai protocol = %q, want openai", xai.Protocol)
	}
	if xai.BaseURL != "https://api.x.ai/v1" {
		t.Fatalf("xai base url = %q, want https://api.x.ai/v1", xai.BaseURL)
	}

	mistral, ok := BuiltInProviderByID("mistral")
	if !ok {
		t.Fatal("BuiltInProviderByID(mistral) = not found")
	}
	if mistral.Protocol != "openai" {
		t.Fatalf("mistral protocol = %q, want openai", mistral.Protocol)
	}
	if mistral.BaseURL != "https://api.mistral.ai/v1" {
		t.Fatalf("mistral base url = %q, want https://api.mistral.ai/v1", mistral.BaseURL)
	}

	perplexity, ok := BuiltInProviderByID("perplexity")
	if !ok {
		t.Fatal("BuiltInProviderByID(perplexity) = not found")
	}
	if perplexity.Protocol != "openai" {
		t.Fatalf("perplexity protocol = %q, want openai", perplexity.Protocol)
	}
	if perplexity.BaseURL != "https://api.perplexity.ai" {
		t.Fatalf("perplexity base url = %q, want https://api.perplexity.ai", perplexity.BaseURL)
	}
	if perplexity.ChatPath != "/chat/completions" {
		t.Fatalf("perplexity chat path = %q, want /chat/completions", perplexity.ChatPath)
	}

	together, ok := BuiltInProviderByID("together_ai")
	if !ok {
		t.Fatal("BuiltInProviderByID(together_ai) = not found")
	}
	if together.Protocol != "openai" {
		t.Fatalf("together_ai protocol = %q, want openai", together.Protocol)
	}
	if together.BaseURL != "https://api.together.xyz/v1" {
		t.Fatalf("together_ai base url = %q, want https://api.together.xyz/v1", together.BaseURL)
	}

	cohere, ok := BuiltInProviderByID("cohere")
	if !ok {
		t.Fatal("BuiltInProviderByID(cohere) = not found")
	}
	if cohere.Protocol != "openai" {
		t.Fatalf("cohere protocol = %q, want openai", cohere.Protocol)
	}
	if cohere.BaseURL != "https://api.cohere.ai/compatibility/v1" {
		t.Fatalf("cohere base url = %q, want https://api.cohere.ai/compatibility/v1", cohere.BaseURL)
	}

	fireworks, ok := BuiltInProviderByID("fireworks")
	if !ok {
		t.Fatal("BuiltInProviderByID(fireworks) = not found")
	}
	if fireworks.Protocol != "openai" {
		t.Fatalf("fireworks protocol = %q, want openai", fireworks.Protocol)
	}
	if fireworks.BaseURL != "https://api.fireworks.ai/inference/v1" {
		t.Fatalf("fireworks base url = %q, want https://api.fireworks.ai/inference/v1", fireworks.BaseURL)
	}

	huggingface, ok := BuiltInProviderByID("huggingface")
	if !ok {
		t.Fatal("BuiltInProviderByID(huggingface) = not found")
	}
	if huggingface.Protocol != "openai" {
		t.Fatalf("huggingface protocol = %q, want openai", huggingface.Protocol)
	}
	if huggingface.BaseURL != "https://router.huggingface.co/v1" {
		t.Fatalf("huggingface base url = %q, want https://router.huggingface.co/v1", huggingface.BaseURL)
	}

	nvidia, ok := BuiltInProviderByID("nvidia")
	if !ok {
		t.Fatal("BuiltInProviderByID(nvidia) = not found")
	}
	if nvidia.Protocol != "openai" {
		t.Fatalf("nvidia protocol = %q, want openai", nvidia.Protocol)
	}
	if nvidia.BaseURL != "https://integrate.api.nvidia.com/v1" {
		t.Fatalf("nvidia base url = %q, want https://integrate.api.nvidia.com/v1", nvidia.BaseURL)
	}

	zai, ok := BuiltInProviderByID("zai")
	if !ok {
		t.Fatal("BuiltInProviderByID(zai) = not found")
	}
	if zai.Protocol != "openai" {
		t.Fatalf("zai protocol = %q, want openai", zai.Protocol)
	}
	if zai.BaseURL != "https://api.z.ai/api/paas/v4" {
		t.Fatalf("zai base url = %q, want https://api.z.ai/api/paas/v4", zai.BaseURL)
	}

	for _, id := range []string{"ollama", "LM Studio", "localai", "llamacpp"} {
		_, ok := BuiltInProviderByID(id)
		if !ok {
			t.Fatalf("BuiltInProviderByID(%s) = not found", id)
		}
	}
}

func TestLoadProvidersFromEnvIncludesCustomProviderFromCoreEnvKeys(t *testing.T) {
	t.Setenv("PROVIDER_CUSTOM_PRECONFIGURED", "1")
	t.Setenv("PROVIDER_CUSTOM_BASE_URL", "https://example.com/v1")
	t.Setenv("PROVIDER_CUSTOM_API_KEY", "custom-secret")
	t.Setenv("PROVIDER_CUSTOM_MODELS", "custom-fast, custom-large")

	cfg := LoadFromEnv()
	// Only providers with explicit env vars register — one custom var
	// pair → one provider. Built-ins without env vars stay out.
	if len(cfg.Providers.OpenAICompatible) != 1 {
		t.Fatalf("provider count = %d, want 1 (custom only)", len(cfg.Providers.OpenAICompatible))
	}
	custom, ok := testProviderByName(cfg.Providers.OpenAICompatible, "custom")
	if !ok {
		t.Fatal("custom provider missing")
	}
	if custom.BaseURL != "https://example.com/v1" {
		t.Fatalf("custom base_url = %q, want https://example.com/v1", custom.BaseURL)
	}
	if custom.APIKey != "custom-secret" {
		t.Fatalf("custom api key = %q, want custom-secret", custom.APIKey)
	}
	if len(custom.KnownModels) != 2 || custom.KnownModels[0] != "custom-fast" || custom.KnownModels[1] != "custom-large" {
		t.Fatalf("custom known models = %#v, want custom-fast/custom-large", custom.KnownModels)
	}
}

func TestLoadProvidersFromEnvSupportsXAIProviderEnv(t *testing.T) {
	t.Setenv("PROVIDER_XAI_PRECONFIGURED", "1")
	t.Setenv("PROVIDER_XAI_API_KEY", "xai-secret")

	cfg := LoadFromEnv()
	xai, ok := testProviderByName(cfg.Providers.OpenAICompatible, "xai")
	if !ok {
		t.Fatal("xai provider missing")
	}
	if xai.APIKey != "xai-secret" {
		t.Fatalf("xai api key = %q, want xai-secret", xai.APIKey)
	}
}

func TestLoadProvidersFromEnvSupportsPerplexityProviderEnv(t *testing.T) {
	t.Setenv("PROVIDER_PERPLEXITY_PRECONFIGURED", "1")
	t.Setenv("PROVIDER_PERPLEXITY_API_KEY", "pplx-secret")

	cfg := LoadFromEnv()
	perplexity, ok := testProviderByName(cfg.Providers.OpenAICompatible, "perplexity")
	if !ok {
		t.Fatal("perplexity provider missing")
	}
	if perplexity.APIKey != "pplx-secret" {
		t.Fatalf("perplexity api key = %q, want pplx-secret", perplexity.APIKey)
	}
	if perplexity.ChatPath != "/chat/completions" {
		t.Fatalf("perplexity chat path = %q, want /chat/completions", perplexity.ChatPath)
	}
	if perplexity.ModelsPath != "/v1/models" {
		t.Fatalf("perplexity models path = %q, want /v1/models", perplexity.ModelsPath)
	}
}

func testProviderByName(items []OpenAICompatibleProviderConfig, name string) (OpenAICompatibleProviderConfig, bool) {
	for _, item := range items {
		if item.Name == name {
			return item, true
		}
	}
	return OpenAICompatibleProviderConfig{}, false
}

func TestLoadFromEnvDataDirDefault(t *testing.T) {
	t.Setenv("HECATE_DATA_DIR", "")

	cfg := LoadFromEnv()
	if cfg.Server.DataDir != ".data" {
		t.Fatalf("DataDir default = %q, want .data", cfg.Server.DataDir)
	}
}

func TestLoadFromEnvDataDirOverride(t *testing.T) {
	t.Setenv("HECATE_DATA_DIR", "/var/hecate")

	cfg := LoadFromEnv()
	if cfg.Server.DataDir != "/var/hecate" {
		t.Fatalf("DataDir override = %q, want /var/hecate", cfg.Server.DataDir)
	}
}

func TestLoadFromEnvBootstrapFileDefault(t *testing.T) {
	t.Setenv("HECATE_BOOTSTRAP_FILE", "")

	cfg := LoadFromEnv()
	if cfg.Server.BootstrapFile != "" {
		t.Fatalf("BootstrapFile default = %q, want empty (cmd/hecate derives it from DataDir)", cfg.Server.BootstrapFile)
	}
}

func TestValidateRejectsUnknownApprovalPolicyNames(t *testing.T) {
	cfg := LoadFromEnv()
	cfg.Server.TaskApprovalPolicies = []string{"shell_exec", "typo_policy"}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want unknown policy error")
	}
	if !strings.Contains(err.Error(), "typo_policy") {
		t.Fatalf("Validate() error = %q, want it to name the bad policy", err)
	}
}

func TestValidateAcceptsAllValidApprovalPolicyNames(t *testing.T) {
	cfg := LoadFromEnv()
	cfg.Server.TaskApprovalPolicies = []string{
		"shell_exec", "git_exec", "file_write",
		"network_egress", "read_file", "all_tools",
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil for all valid policy names", err)
	}
}

func TestValidateAcceptsEmptyApprovalPolicies(t *testing.T) {
	cfg := LoadFromEnv()
	cfg.Server.TaskApprovalPolicies = nil

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil for empty policies (trusted env path)", err)
	}
}
