package providers

import (
	"context"
	"encoding/base64"
	"log/slog"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/secrets"
)

func TestControlPlaneRuntimeManagerUpsertReloadsRegistryAndEncryptsSecrets(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	store := controlplane.NewMemoryStore()
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cipher, err := secrets.NewAESGCMCipher(key)
	if err != nil {
		t.Fatalf("NewAESGCMCipher() error = %v", err)
	}

	manager := NewControlPlaneRuntimeManager(logger, []config.OpenAICompatibleProviderConfig{
		{Name: "openai", Kind: "cloud", Protocol: "openai", BaseURL: "https://api.openai.com", APIKey: "env-secret", DefaultModel: "gpt-4o-mini"},
	}, store, cipher)

	if err := manager.Reload(context.Background()); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	if _, err := manager.Upsert(context.Background(), controlplane.Provider{
		Name:         "groq",
		Kind:         "cloud",
		Protocol:     "openai",
		BaseURL:      "https://api.groq.com/openai/v1",
		DefaultModel: "llama-3.3-70b-versatile",
		Enabled:      true,
	}, "groq-secret"); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	registry := manager.Registry()
	groq, ok := registry.Get("groq")
	if !ok {
		t.Fatal("expected groq provider in registry after reload")
	}
	if groq.Kind() != KindCloud {
		t.Fatalf("groq.Kind() = %q, want cloud", groq.Kind())
	}

	state, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(state.ProviderSecrets) != 1 {
		t.Fatalf("provider secret count = %d, want 1", len(state.ProviderSecrets))
	}
	if state.ProviderSecrets[0].APIKeyEncrypted == "groq-secret" {
		t.Fatal("expected provider secret to be encrypted at rest")
	}
	if state.ProviderSecrets[0].APIKeyPreview == "" {
		t.Fatal("expected provider secret preview to be stored")
	}
}

func TestControlPlaneRuntimeManagerHydratesBuiltInProviderDefaults(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	store := controlplane.NewMemoryStore()
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cipher, err := secrets.NewAESGCMCipher(key)
	if err != nil {
		t.Fatalf("NewAESGCMCipher() error = %v", err)
	}

	manager := NewControlPlaneRuntimeManager(logger, nil, store, cipher)
	if _, err := manager.Upsert(context.Background(), controlplane.Provider{
		Name:    "groq",
		Enabled: true,
	}, "groq-secret"); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	state, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(state.Providers) != 1 {
		t.Fatalf("provider count = %d, want 1", len(state.Providers))
	}
	got := state.Providers[0]
	if got.BaseURL != "https://api.groq.com/openai/v1" {
		t.Fatalf("base url = %q, want groq default", got.BaseURL)
	}
	if got.Protocol != "openai" {
		t.Fatalf("protocol = %q, want openai", got.Protocol)
	}
	if got.Kind != "cloud" {
		t.Fatalf("kind = %q, want cloud", got.Kind)
	}
	if got.PresetID != "groq" {
		t.Fatalf("preset id = %q, want groq", got.PresetID)
	}
}

func TestControlPlaneRuntimeManagerRejectsLocalProvidersWhenDisabled(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	store := controlplane.NewMemoryStore()
	manager := NewControlPlaneRuntimeManager(logger, nil, store, nil)
	manager.SetLocalProvidersAllowed(false)

	_, err := manager.Upsert(context.Background(), controlplane.Provider{
		ID:       "ollama",
		Name:     "Ollama",
		PresetID: "ollama",
		Kind:     "local",
		Protocol: "openai",
		BaseURL:  "http://127.0.0.1:11434/v1",
		Enabled:  true,
	}, "")
	if err == nil {
		t.Fatal("Upsert(local provider) error = nil, want disabled-local-provider error")
	}
	if !strings.Contains(err.Error(), "local providers are disabled") {
		t.Fatalf("Upsert(local provider) error = %v, want local providers disabled", err)
	}
}

func TestControlPlaneRuntimeManagerSkipsExistingLocalProvidersWhenDisabled(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	store := controlplane.NewMemoryStore()
	if _, err := store.UpsertProvider(context.Background(), controlplane.Provider{
		ID:       "ollama",
		Name:     "ollama",
		PresetID: "ollama",
		Kind:     "local",
		Protocol: "openai",
		BaseURL:  "http://127.0.0.1:11434/v1",
		Enabled:  true,
	}, nil); err != nil {
		t.Fatalf("UpsertProvider(local): %v", err)
	}
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cipher, err := secrets.NewAESGCMCipher(key)
	if err != nil {
		t.Fatalf("NewAESGCMCipher() error = %v", err)
	}
	if _, err := store.UpsertProvider(context.Background(), controlplane.Provider{
		ID:       "anthropic",
		Name:     "anthropic",
		PresetID: "anthropic",
		Kind:     "cloud",
		Protocol: "anthropic",
		BaseURL:  "https://api.anthropic.com",
		Enabled:  true,
	}, &controlplane.ProviderSecret{ProviderID: "anthropic", APIKeyEncrypted: mustEncryptForTest(t, cipher, "anthropic-secret")}); err != nil {
		t.Fatalf("UpsertProvider(cloud): %v", err)
	}

	manager := NewControlPlaneRuntimeManager(logger, nil, store, cipher)
	manager.SetLocalProvidersAllowed(false)
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	registry := manager.Registry()
	if _, ok := registry.Get("ollama"); ok {
		t.Fatal("registry contains ollama, want local provider skipped")
	}
	if _, ok := registry.Get("anthropic"); !ok {
		t.Fatal("registry missing anthropic cloud provider")
	}
}

func TestControlPlaneRuntimeManagerSkipsBaseLocalProvidersWhenDisabled(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	manager := NewControlPlaneRuntimeManager(logger, []config.OpenAICompatibleProviderConfig{
		{Name: "ollama", Kind: "local", Protocol: "openai", BaseURL: "http://127.0.0.1:11434/v1"},
		{Name: "openai", Kind: "cloud", Protocol: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "sk-openai"},
	}, nil, nil)
	manager.SetLocalProvidersAllowed(false)
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	registry := manager.Registry()
	if _, ok := registry.Get("ollama"); ok {
		t.Fatal("registry contains ollama, want base local provider skipped")
	}
	if _, ok := registry.Get("openai"); !ok {
		t.Fatal("registry missing openai cloud provider")
	}
}

func TestControlPlaneRuntimeManagerDefaultsToSkippingLocalProviders(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	store := controlplane.NewMemoryStore()
	if _, err := store.UpsertProvider(context.Background(), controlplane.Provider{
		ID:       "ollama-cp",
		Name:     "ollama-cp",
		Kind:     "local",
		Protocol: "openai",
		BaseURL:  "http://127.0.0.1:11434/v1",
		Enabled:  true,
	}, nil); err != nil {
		t.Fatalf("UpsertProvider(local): %v", err)
	}

	manager := NewControlPlaneRuntimeManager(logger, []config.OpenAICompatibleProviderConfig{
		{Name: "ollama", Kind: "local", Protocol: "openai", BaseURL: "http://127.0.0.1:11434/v1"},
		{Name: "openai", Kind: "cloud", Protocol: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "sk-openai"},
	}, store, nil)
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	registry := manager.Registry()
	if _, ok := registry.Get("ollama"); ok {
		t.Fatal("registry contains base local provider, want default skip")
	}
	if _, ok := registry.Get("ollama-cp"); ok {
		t.Fatal("registry contains control-plane local provider, want default skip")
	}
	if _, ok := registry.Get("openai"); !ok {
		t.Fatal("registry missing openai cloud provider")
	}
}

func TestControlPlaneRuntimeManagerHydratesPresetEndpointPaths(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	store := controlplane.NewMemoryStore()
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cipher, err := secrets.NewAESGCMCipher(key)
	if err != nil {
		t.Fatalf("NewAESGCMCipher() error = %v", err)
	}

	manager := NewControlPlaneRuntimeManager(logger, nil, store, cipher)
	if _, err := manager.Upsert(context.Background(), controlplane.Provider{
		ID:         "perplexity-eu",
		Name:       "Perplexity",
		PresetID:   "perplexity",
		CustomName: "EU",
		Enabled:    true,
	}, "pplx-secret"); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	registry := manager.Registry()
	provider, ok := registry.Get("perplexity")
	if !ok {
		t.Fatal("expected perplexity provider in registry")
	}
	openaiProvider, ok := provider.(*OpenAICompatibleProvider)
	if !ok {
		t.Fatalf("provider type = %T, want *OpenAICompatibleProvider", provider)
	}
	if openaiProvider.config.ChatPath != "/chat/completions" {
		t.Fatalf("chat path = %q, want /chat/completions", openaiProvider.config.ChatPath)
	}
	if openaiProvider.config.ModelsPath != "/v1/models" {
		t.Fatalf("models path = %q, want /v1/models", openaiProvider.config.ModelsPath)
	}
}

func TestControlPlaneRuntimeManagerAppliesFireworksAccountID(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	store := controlplane.NewMemoryStore()
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cipher, err := secrets.NewAESGCMCipher(key)
	if err != nil {
		t.Fatalf("NewAESGCMCipher() error = %v", err)
	}

	manager := NewControlPlaneRuntimeManager(logger, nil, store, cipher)
	if _, err := manager.Upsert(context.Background(), controlplane.Provider{
		ID:        "fireworks-ai",
		Name:      "fireworks",
		PresetID:  "fireworks",
		AccountID: "team-alpha",
		Enabled:   true,
	}, "fw-secret"); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	registry := manager.Registry()
	provider, ok := registry.Get("fireworks")
	if !ok {
		t.Fatal("expected fireworks provider in registry")
	}
	openaiProvider, ok := provider.(*OpenAICompatibleProvider)
	if !ok {
		t.Fatalf("provider type = %T, want *OpenAICompatibleProvider", provider)
	}
	if openaiProvider.config.ModelsPath != config.FireworksModelsPath("team-alpha") {
		t.Fatalf("models path = %q, want team account endpoint", openaiProvider.config.ModelsPath)
	}
}

func TestControlPlaneRuntimeManagerPreservesExistingOverridesOnMinimalUpdate(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	store := controlplane.NewMemoryStore()
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cipher, err := secrets.NewAESGCMCipher(key)
	if err != nil {
		t.Fatalf("NewAESGCMCipher() error = %v", err)
	}

	manager := NewControlPlaneRuntimeManager(logger, nil, store, cipher)
	if _, err := manager.Upsert(context.Background(), controlplane.Provider{
		Name:           "groq",
		DefaultModel:   "openai/gpt-oss-20b",
		ExplicitFields: []string{"default_model"},
		Enabled:        true,
	}, "groq-secret"); err != nil {
		t.Fatalf("initial Upsert() error = %v", err)
	}

	if _, err := manager.Upsert(context.Background(), controlplane.Provider{
		ID:      "groq",
		Name:    "groq",
		Enabled: true,
	}, ""); err != nil {
		t.Fatalf("minimal Upsert() error = %v", err)
	}

	state, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	got := state.Providers[0]
	if got.DefaultModel != "openai/gpt-oss-20b" {
		t.Fatalf("default model = %q, want preserved explicit override", got.DefaultModel)
	}
	if len(got.ExplicitFields) != 1 || got.ExplicitFields[0] != "default_model" {
		t.Fatalf("explicit fields = %#v, want [default_model]", got.ExplicitFields)
	}
}

// TestControlPlaneRuntimeManagerNormalizesPresetNameCasing is the
// regression for the "Ollama added via UI form, model dropdown empty"
// bug. The UI's create form pre-fills `name` with the preset's display
// name ("Ollama" with capital O), but cp.id is slugified to lowercase
// ("ollama"). The catalog's provider name (used as model
// metadata.provider on /v1/models) must match cp.id so the UI's
// provider picker — which uses cp.id as the option value — finds the
// catalog's models. hydrateControlPlaneProviderDefaults is the
// normalization seam.
func TestControlPlaneRuntimeManagerNormalizesPresetNameCasing(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	store := controlplane.NewMemoryStore()

	manager := NewControlPlaneRuntimeManager(logger, nil, store, nil)
	manager.SetLocalProvidersAllowed(true)
	// Mimic what the UI form sends when the operator picks the Ollama
	// preset: name = preset.Name ("Ollama"), kind = "local", no
	// secret. cp.ID is slugified to "ollama" inside the create
	// handler before reaching Upsert; we pre-set it here.
	if _, err := manager.Upsert(context.Background(), controlplane.Provider{
		ID:       "ollama",
		Name:     "Ollama",
		PresetID: "ollama",
		Kind:     "local",
		Protocol: "openai",
		BaseURL:  "http://127.0.0.1:11434/v1",
		Enabled:  true,
	}, ""); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	registry := manager.Registry()
	if _, ok := registry.Get("ollama"); !ok {
		t.Fatal(`registry should expose the provider under its canonical lowercase id ("ollama")`)
	}
	if _, ok := registry.Get("Ollama"); ok {
		t.Fatal(`registry should NOT expose the provider under the display name ("Ollama"); it is the operator-facing label, not a catalog key`)
	}
}

// TestControlPlaneRuntimeManagerAppliesAnthropicCacheToggleByProtocol pins
// that the global HECATE_PROVIDER_ANTHROPIC_CACHE_ENABLED toggle reaches
// every Anthropic-protocol provider regardless of how it ended up in the
// registry. The earlier name-match-only fallback left CP-only Anthropic
// providers stuck at the default; this test guards against a regression.
func TestControlPlaneRuntimeManagerAppliesAnthropicCacheToggleByProtocol(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	store := controlplane.NewMemoryStore()
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cipher, err := secrets.NewAESGCMCipher(key)
	if err != nil {
		t.Fatalf("NewAESGCMCipher() error = %v", err)
	}

	manager := NewControlPlaneRuntimeManager(logger, nil, store, cipher)
	manager.SetGlobalAnthropicCacheDisabled(true)

	// CP-only Anthropic provider, no env-derived base config — exactly
	// the case the prior name-match fallback missed. PresetID="anthropic"
	// causes hydrateControlPlaneProviderDefaults to clobber Name to
	// "anthropic" (the canonical preset id), so the registry key is
	// "anthropic".
	if _, err := manager.Upsert(context.Background(), controlplane.Provider{
		ID:       "anthropic",
		Name:     "Anthropic",
		PresetID: "anthropic",
		Enabled:  true,
	}, "anthropic-secret"); err != nil {
		t.Fatalf("Upsert(anthropic) error = %v", err)
	}
	// CP-only OpenAI provider on the same manager — must NOT inherit the
	// Anthropic-specific flag.
	if _, err := manager.Upsert(context.Background(), controlplane.Provider{
		ID:       "openai",
		Name:     "OpenAI",
		PresetID: "openai",
		Enabled:  true,
	}, "openai-secret"); err != nil {
		t.Fatalf("Upsert(openai) error = %v", err)
	}

	registry := manager.Registry()
	anth, ok := registry.Get("anthropic")
	if !ok {
		t.Fatal("expected Anthropic provider in registry")
	}
	anthropicProvider, ok := anth.(*AnthropicProvider)
	if !ok {
		t.Fatalf("provider type = %T, want *AnthropicProvider", anth)
	}
	if !anthropicProvider.config.AnthropicCacheDisabled {
		t.Fatal("CP-only Anthropic provider missed the global cache toggle")
	}

	op, ok := registry.Get("openai")
	if !ok {
		t.Fatal("expected OpenAI provider in registry")
	}
	openaiProvider, ok := op.(*OpenAICompatibleProvider)
	if !ok {
		t.Fatalf("provider type = %T, want *OpenAICompatibleProvider", op)
	}
	if openaiProvider.config.AnthropicCacheDisabled {
		t.Fatal("non-Anthropic provider should NOT inherit AnthropicCacheDisabled")
	}
}

func mustEncryptForTest(t *testing.T, cipher secrets.Cipher, value string) string {
	t.Helper()
	encrypted, err := cipher.Encrypt(value)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	return encrypted
}

type testWriter struct {
	t *testing.T
}

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}
