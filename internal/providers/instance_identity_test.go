package providers

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestConfigurationProviderInstanceIdentityRequiresNonSecretGeneration(t *testing.T) {
	t.Parallel()

	identity := configurationProviderInstanceIdentity(config.OpenAICompatibleProviderConfig{
		Name:    "openai",
		BaseURL: "https://api.example.test/v1",
		APIKey:  "must-not-be-fingerprinted",
	})
	if identity.Valid() {
		t.Fatalf("identity = %+v, want runtime fallback without a persisted generation", identity)
	}
}

func TestConfigurationProviderInstanceIdentityTracksGenerationAndConfigWithoutSecrets(t *testing.T) {
	t.Parallel()

	base := config.OpenAICompatibleProviderConfig{
		Name:               "openai",
		InstanceGeneration: "control-plane-v1\x00provider-id\x002026-07-13T12:00:00Z\x002026-07-13T12:01:00Z",
		Kind:               string(KindCloud),
		Protocol:           "openai",
		BaseURL:            "https://api.example.test/v1",
		APIKey:             "first-secret",
		DefaultModel:       "gpt-test",
		Enabled:            true,
	}
	first := configurationProviderInstanceIdentity(base)
	if !first.Valid() || first.Kind != types.ProviderInstanceIdentityConfiguration {
		t.Fatalf("first identity = %+v, want durable configuration identity", first)
	}

	secretOnly := base
	secretOnly.APIKey = "second-secret"
	if got := configurationProviderInstanceIdentity(secretOnly); got != first {
		t.Fatalf("secret-only identity = %+v, want no credential-derived change from %+v", got, first)
	}

	newGeneration := base
	newGeneration.InstanceGeneration += "-rotated"
	if got := configurationProviderInstanceIdentity(newGeneration); got == first {
		t.Fatal("identity did not change when the persisted control-plane generation changed")
	}

	newEndpoint := base
	newEndpoint.BaseURL = "https://replacement.example.test/v1"
	if got := configurationProviderInstanceIdentity(newEndpoint); got == first {
		t.Fatal("identity did not change when dispatch configuration changed")
	}

	newTranscriptionRoute := base
	newTranscriptionRoute.TranscriptionPath = "/audio/transcriptions"
	newTranscriptionRoute.DefaultTranscriptionModel = "speech-model"
	if got := configurationProviderInstanceIdentity(newTranscriptionRoute); got == first {
		t.Fatal("identity did not change when transcription disclosure configuration changed")
	}
}

func TestMutableRegistryRecreationChangesRuntimeIdentityButPreservesDurableGeneration(t *testing.T) {
	t.Parallel()

	unknown := &instanceIdentityTestProvider{name: "custom"}
	registry := NewMutableRegistry(unknown)
	firstRuntime, _ := registry.GetInstance("custom")
	registry.Replace(unknown)
	secondRuntime, _ := registry.GetInstance("custom")
	if !firstRuntime.Identity.Valid() || firstRuntime.Identity.Kind != types.ProviderInstanceIdentityRuntime {
		t.Fatalf("first runtime identity = %+v", firstRuntime.Identity)
	}
	if firstRuntime.Identity == secondRuntime.Identity {
		t.Fatal("runtime identity was reused across registry replacement")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.OpenAICompatibleProviderConfig{
		Name:               "managed",
		InstanceGeneration: "control-plane-v1\x00managed\x00created\x00updated",
		Kind:               string(KindCloud),
		Protocol:           "openai",
		BaseURL:            "https://api.example.test/v1",
		DefaultModel:       "model-a",
		Enabled:            true,
	}
	registry.Replace(NewOpenAICompatibleProvider(cfg, logger))
	firstDurable, _ := registry.GetInstance("managed")
	registry.Replace(NewOpenAICompatibleProvider(cfg, logger))
	secondDurable, _ := registry.GetInstance("managed")
	if firstDurable.Identity != secondDurable.Identity || firstDurable.Identity.Kind != types.ProviderInstanceIdentityConfiguration {
		t.Fatalf("durable identities first=%+v second=%+v, want stable generation", firstDurable.Identity, secondDurable.Identity)
	}
}

type instanceIdentityTestProvider struct {
	name string
}

func (p *instanceIdentityTestProvider) Name() string { return p.name }
func (p *instanceIdentityTestProvider) Kind() Kind   { return KindLocal }
func (p *instanceIdentityTestProvider) DefaultModel() string {
	return "model-a"
}
func (p *instanceIdentityTestProvider) Capabilities(_ context.Context) (Capabilities, error) {
	return Capabilities{Name: p.name, Kind: KindLocal, DefaultModel: "model-a", Models: []string{"model-a"}}, nil
}
func (p *instanceIdentityTestProvider) Chat(_ context.Context, _ types.ChatRequest) (*types.ChatResponse, error) {
	return &types.ChatResponse{Model: "model-a"}, nil
}
func (p *instanceIdentityTestProvider) Supports(model string) bool { return model == "model-a" }
