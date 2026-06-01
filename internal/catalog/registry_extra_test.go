package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestIsSelfReferentialURL(t *testing.T) {
	cases := []struct {
		name           string
		selfListenAddr string
		providerURL    string
		want           bool
	}{
		{"empty self addr", "", "http://127.0.0.1:8080", false},
		{"empty provider URL", "127.0.0.1:8080", "", false},
		{"self addr without port", "localhost", "http://127.0.0.1:8080", false},
		{"loopback IPv4 matching port", "127.0.0.1:8080", "http://127.0.0.1:8080/v1", true},
		{"loopback IPv6 matching port", "[::1]:8080", "http://[::1]:8080/v1", true},
		{"localhost hostname matching port", "127.0.0.1:8080", "http://localhost:8080/v1", true},
		{"non-loopback IP matching port", "127.0.0.1:8080", "http://192.168.1.5:8080/v1", false},
		{"matching host but different port", "127.0.0.1:8080", "http://127.0.0.1:9090/v1", false},
		{"unparseable URL", "127.0.0.1:8080", "://broken", false},
		{"non-loopback hostname", "127.0.0.1:8080", "http://api.example.com:8080/v1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSelfReferentialURL(tc.selfListenAddr, tc.providerURL); got != tc.want {
				t.Errorf("isSelfReferentialURL(%q, %q) = %v, want %v", tc.selfListenAddr, tc.providerURL, got, tc.want)
			}
		})
	}
}

// fakeProviderWithBaseURL lives in fake_provider_test.go.

func TestEntryForProviderSelfReferentialIsDegraded(t *testing.T) {
	provider := &fakeProviderWithBaseURL{
		fakeProvider: &fakeProvider{name: "local", kind: providers.KindLocal, defaultModel: "tiny"},
		baseURL:      "http://127.0.0.1:8080/v1",
	}
	registry := providers.NewRegistry(provider)
	cat := NewRegistryCatalogWithSelfAddr(registry, nil, "127.0.0.1:8080")

	entries := cat.Snapshot(context.Background())
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].Status != "degraded" {
		t.Errorf("Status = %q, want degraded", entries[0].Status)
	}
	if entries[0].DiscoverySource != "self_referential" {
		t.Errorf("DiscoverySource = %q, want self_referential", entries[0].DiscoverySource)
	}
	if entries[0].Healthy {
		t.Error("self-referential provider should be marked unhealthy")
	}
}

// fakeDisabledProvider implements providers.Enabler and reports false so the
// disabled-short-circuit branch of entryForProvider fires.
type fakeDisabledProvider struct {
	*fakeProvider
}

func (p *fakeDisabledProvider) Enabled() bool { return false }

func TestEntryForProviderDisabledShortCircuits(t *testing.T) {
	provider := &fakeDisabledProvider{
		fakeProvider: &fakeProvider{name: "off", kind: providers.KindCloud, defaultModel: "ignored"},
	}
	registry := providers.NewRegistry(provider)
	cat := NewRegistryCatalog(registry, nil)

	entries := cat.Snapshot(context.Background())
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].Status != "disabled" {
		t.Errorf("Status = %q, want disabled", entries[0].Status)
	}
	if entries[0].DiscoverySource != "control_plane" {
		t.Errorf("DiscoverySource = %q, want control_plane", entries[0].DiscoverySource)
	}
	if entries[0].Healthy {
		t.Error("disabled provider should be unhealthy")
	}
}

func TestGetReturnsFalseForUnknownProvider(t *testing.T) {
	registry := providers.NewRegistry(&fakeProvider{name: "openai", kind: providers.KindCloud})
	cat := NewRegistryCatalog(registry, nil)

	if _, ok := cat.Get(context.Background(), "openai"); !ok {
		t.Error("expected ok=true for known provider")
	}
	if _, ok := cat.Get(context.Background(), "missing"); ok {
		t.Error("expected ok=false for unknown provider")
	}
}

func TestSnapshotEmptyRegistry(t *testing.T) {
	cat := NewRegistryCatalog(providers.NewRegistry(), nil)
	if got := cat.Snapshot(context.Background()); len(got) != 0 {
		t.Errorf("Snapshot of empty registry = %d entries, want 0", len(got))
	}
}

// TestEntryForProviderCapabilitiesErrorIsDegraded exercises the path where
// Capabilities() returns an error — entry should still be produced but marked
// degraded with the error attached.
func TestEntryForProviderCapabilitiesErrorIsDegraded(t *testing.T) {
	provider := &fakeProvider{
		name:         "openai",
		kind:         providers.KindCloud,
		defaultModel: "gpt-4o",
		caps:         providers.Capabilities{Name: "openai", DefaultModel: "gpt-4o"},
		capsErr:      errors.New("upstream timeout"),
	}
	registry := providers.NewRegistry(provider)
	cat := NewRegistryCatalog(registry, nil)

	entries := cat.Snapshot(context.Background())
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].Healthy {
		t.Error("entry should be unhealthy when Capabilities returns error")
	}
	if entries[0].Status != "degraded" {
		t.Errorf("Status = %q, want degraded", entries[0].Status)
	}
	if entries[0].Error == "" {
		t.Error("Error should be populated from capabilities failure")
	}
}

func TestEntryForProviderLeavesEmptyModelCapabilitiesNil(t *testing.T) {
	provider := &fakeProvider{
		name:         "ollama",
		kind:         providers.KindLocal,
		defaultModel: "llama3.1",
		caps: providers.Capabilities{
			Name:         "ollama",
			Kind:         providers.KindLocal,
			DefaultModel: "llama3.1",
			Models:       []string{"llama3.1"},
		},
	}
	registry := providers.NewRegistry(provider)
	cat := NewRegistryCatalog(registry, nil)

	entries := cat.Snapshot(context.Background())
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].ModelCapabilities != nil {
		t.Fatalf("ModelCapabilities = %#v, want nil for providers without per-model metadata", entries[0].ModelCapabilities)
	}
}

func TestEntryForProviderCopiesModelCapabilitiesWhenPresent(t *testing.T) {
	provider := &fakeProvider{
		name:         "ollama",
		kind:         providers.KindLocal,
		defaultModel: "llama3.1",
		caps: providers.Capabilities{
			Name:         "ollama",
			Kind:         providers.KindLocal,
			DefaultModel: "llama3.1",
			Models:       []string{"llama3.1"},
			ModelCapabilities: map[string]types.ModelCapabilities{
				"llama3.1": {ToolCalling: "basic", Streaming: true, StreamingKnown: true, Source: "provider"},
			},
		},
	}
	registry := providers.NewRegistry(provider)
	cat := NewRegistryCatalog(registry, nil)

	entries := cat.Snapshot(context.Background())
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].ModelCapabilities == nil {
		t.Fatal("ModelCapabilities = nil, want copied provider metadata")
	}
	got := entries[0].ModelCapabilities["llama3.1"]
	if got.ToolCalling != "basic" || !got.Streaming {
		t.Fatalf("copied capability = %#v, want provider metadata", got)
	}
}
