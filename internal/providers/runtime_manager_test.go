package providers

import (
	"context"
	"encoding/base64"
	"log/slog"
	"testing"

	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/controlplane"
	"github.com/hecate/agent-runtime/internal/secrets"
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

type testWriter struct {
	t *testing.T
}

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}
