package controlplane

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/hecate/agent-runtime/internal/config"
)

func newImportTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestAutoImportEnvProviders_AddsMissing(t *testing.T) {
	store := NewMemoryStore()
	cfgs := []config.OpenAICompatibleProviderConfig{{
		Name:         "openai",
		Kind:         "cloud",
		Protocol:     "openai",
		BaseURL:      "https://api.openai.com/v1",
		APIKey:       "sk-test",
		DefaultModel: "gpt-4o-mini",
	}}
	if err := AutoImportEnvProviders(context.Background(), newImportTestLogger(), store, cfgs); err != nil {
		t.Fatalf("AutoImportEnvProviders: %v", err)
	}
	state, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(state.Providers) != 1 {
		t.Fatalf("want 1 provider in CP store, got %d", len(state.Providers))
	}
	got := state.Providers[0]
	if got.ID != "openai" || got.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("unexpected imported provider: %+v", got)
	}
}

func TestAutoImportEnvProviders_PreservesExisting(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	if _, err := store.UpsertProvider(ctx, Provider{
		ID: "openai", Name: "openai", Kind: "cloud", Protocol: "openai",
		BaseURL: "https://operator-edited.example/v1",
	}, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfgs := []config.OpenAICompatibleProviderConfig{{
		Name:    "openai",
		Kind:    "cloud",
		BaseURL: "https://api.openai.com/v1",
	}}
	if err := AutoImportEnvProviders(ctx, newImportTestLogger(), store, cfgs); err != nil {
		t.Fatalf("AutoImportEnvProviders: %v", err)
	}
	state, _ := store.Snapshot(ctx)
	if len(state.Providers) != 1 {
		t.Fatalf("want 1 provider, got %d", len(state.Providers))
	}
	if state.Providers[0].BaseURL != "https://operator-edited.example/v1" {
		t.Fatalf("CP edit was overwritten by env import: %+v", state.Providers[0])
	}
}

func TestAutoImportEnvProviders_NilStoreNoop(t *testing.T) {
	if err := AutoImportEnvProviders(context.Background(), newImportTestLogger(), nil, []config.OpenAICompatibleProviderConfig{{Name: "x"}}); err != nil {
		t.Fatalf("nil store should noop, got %v", err)
	}
}
