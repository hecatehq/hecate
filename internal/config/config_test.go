package config

import (
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

func TestLoadFromEnvUsesCurrentOpenAIDefaultModel(t *testing.T) {
	t.Setenv("HECATE_DEFAULT_MODEL", "")

	cfg := LoadFromEnv()
	if cfg.Router.DefaultModel != "gpt-5.4-mini" {
		t.Fatalf("default model = %q, want gpt-5.4-mini", cfg.Router.DefaultModel)
	}
}

func TestLoadFromEnvBackendFansOutToDurableStores(t *testing.T) {
	t.Setenv("HECATE_BACKEND", " SQLite ")

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
		if backend != "sqlite" {
			t.Fatalf("backend fanout = %#v, want all sqlite", got)
		}
	}
}

func TestLoadFromEnvTraceBodyModeDefaultsToMetadata(t *testing.T) {
	cfg := LoadFromEnv()
	if cfg.Server.TraceBodyMode != "metadata" {
		t.Fatalf("TraceBodyMode = %q, want metadata", cfg.Server.TraceBodyMode)
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
	cfg.Projects.Backend = "postgres"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want invalid backend error")
	}
	if !strings.Contains(err.Error(), "HECATE_BACKEND") {
		t.Fatalf("Validate() error = %q, want HECATE_BACKEND", err)
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

func TestBuiltInProviderCatalogDefaults(t *testing.T) {
	t.Parallel()

	openai, ok := BuiltInProviderByID("openai")
	if !ok {
		t.Fatal("BuiltInProviderByID(openai) = not found")
	}
	if openai.DefaultModel != "gpt-5.4-mini" {
		t.Fatalf("openai built-in default model = %q, want gpt-5.4-mini", openai.DefaultModel)
	}
	if got := openai.RuntimeConfig("gpt-5.4").DefaultModel; got != "gpt-5.4" {
		t.Fatalf("openai runtime default model = %q, want overridden global default", got)
	}

	anthropic, ok := BuiltInProviderByID("anthropic")
	if !ok {
		t.Fatal("BuiltInProviderByID(anthropic) = not found")
	}
	if anthropic.Protocol != "anthropic" {
		t.Fatalf("anthropic protocol = %q, want anthropic", anthropic.Protocol)
	}
	if got := anthropic.RuntimeConfig("ignored").DefaultModel; got != "claude-sonnet-4-6" {
		t.Fatalf("anthropic default model = %q, want claude-sonnet-4-6", got)
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
	if got := deepseek.RuntimeConfig("ignored").DefaultModel; got != "deepseek-chat" {
		t.Fatalf("deepseek default model = %q, want deepseek-chat", got)
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
	if got := gemini.RuntimeConfig("ignored").DefaultModel; got != "gemini-2.5-flash" {
		t.Fatalf("gemini default model = %q, want gemini-2.5-flash", got)
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
	if got := xai.RuntimeConfig("ignored").DefaultModel; got != "grok-3-mini" {
		t.Fatalf("xai default model = %q, want grok-3-mini", got)
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
	if got := mistral.RuntimeConfig("ignored").DefaultModel; got != "mistral-small-latest" {
		t.Fatalf("mistral default model = %q, want mistral-small-latest", got)
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
	if got := perplexity.RuntimeConfig("ignored").DefaultModel; got != "sonar" {
		t.Fatalf("perplexity default model = %q, want sonar", got)
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
	if got := together.RuntimeConfig("ignored").DefaultModel; got != "meta-llama/Meta-Llama-3.1-8B-Instruct-Turbo" {
		t.Fatalf("together_ai default model = %q, want meta-llama/Meta-Llama-3.1-8B-Instruct-Turbo", got)
	}

	for _, id := range []string{"ollama", "LM Studio", "localai", "llamacpp"} {
		local, ok := BuiltInProviderByID(id)
		if !ok {
			t.Fatalf("BuiltInProviderByID(%s) = not found", id)
		}
		if local.DefaultModel != "" {
			t.Fatalf("%s built-in default model = %q, want empty for discovery", local.ID, local.DefaultModel)
		}
		if got := local.RuntimeConfig("ignored").DefaultModel; got != "" {
			t.Fatalf("%s runtime default model = %q, want empty for discovery", local.ID, got)
		}
	}
}

func TestLoadProvidersFromEnvIncludesCustomProviderFromCoreEnvKeys(t *testing.T) {
	t.Setenv("PROVIDER_CUSTOM_PRECONFIGURED", "1")
	t.Setenv("PROVIDER_CUSTOM_BASE_URL", "https://example.com/v1")
	t.Setenv("PROVIDER_CUSTOM_API_KEY", "custom-secret")

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
