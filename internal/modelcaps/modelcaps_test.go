package modelcaps

import (
	"testing"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestResolveProviderCapabilityBeatsCatalog(t *testing.T) {
	got := ResolveWithProviderCapability("ollama", "local", "smollm2:135m", "upstream_v1_models", types.ModelCapabilities{
		ToolCalling:    ToolCallingNone,
		Streaming:      true,
		StreamingKnown: true,
		Source:         SourceProvider,
	})
	if got.ToolCalling != ToolCallingNone {
		t.Fatalf("ToolCalling = %q, want provider none", got.ToolCalling)
	}
	if got.Source != SourceProvider {
		t.Fatalf("Source = %q, want %q", got.Source, SourceProvider)
	}
}

func TestResolveProviderCapabilityCanDisableStreaming(t *testing.T) {
	got := ResolveWithProviderCapability("openai", "cloud", "gpt-5.4-mini", "static", types.ModelCapabilities{
		ToolCalling:    ToolCallingParallel,
		Streaming:      false,
		StreamingKnown: true,
		Source:         SourceProvider,
	})
	if got.Streaming {
		t.Fatal("Streaming = true, want provider-discovered false to override catalog default")
	}
	if got.Source != SourceProvider {
		t.Fatalf("Source = %q, want %q", got.Source, SourceProvider)
	}
}

func TestResolveCatalogDefaults(t *testing.T) {
	tests := []struct {
		name       string
		provider   string
		kind       string
		model      string
		wantTools  string
		wantSource string
	}{
		{name: "openai gpt", provider: "openai", kind: "cloud", model: "gpt-5.4-mini", wantTools: ToolCallingParallel, wantSource: SourceCatalog},
		{name: "anthropic claude", provider: "anthropic", kind: "cloud", model: "claude-sonnet-4-6", wantTools: ToolCallingBasic, wantSource: SourceCatalog},
		{name: "local unknown", provider: "ollama", kind: "local", model: "llama3.1", wantTools: ToolCallingUnknown, wantSource: SourceProvider},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := "static"
			if tt.kind == "local" {
				source = "provider"
			}
			got := Resolve(tt.provider, tt.kind, tt.model, source)
			if got.ToolCalling != tt.wantTools {
				t.Fatalf("ToolCalling = %q, want %q", got.ToolCalling, tt.wantTools)
			}
			if got.Source != tt.wantSource {
				t.Fatalf("Source = %q, want %q", got.Source, tt.wantSource)
			}
		})
	}
}

func TestToolCapableRequiresKnownToolSupport(t *testing.T) {
	tests := []struct {
		name string
		cap  string
		want bool
	}{
		{name: "unknown disabled", cap: ToolCallingUnknown, want: false},
		{name: "basic allowed", cap: ToolCallingBasic, want: true},
		{name: "parallel allowed", cap: ToolCallingParallel, want: true},
		{name: "none disabled", cap: ToolCallingNone, want: false},
		{name: "missing disabled", cap: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToolCapable(types.ModelCapabilities{ToolCalling: tt.cap}); got != tt.want {
				t.Fatalf("ToolCapable(%q) = %v, want %v", tt.cap, got, tt.want)
			}
		})
	}
}
