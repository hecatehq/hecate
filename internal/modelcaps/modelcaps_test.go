package modelcaps

import (
	"testing"

	"github.com/hecatehq/hecate/pkg/types"
)

func TestResolveProviderCapabilityBeatsCatalog(t *testing.T) {
	got := ResolveWithProviderCapability("ollama", "local", "smollm2:135m", "upstream_v1_models", types.ModelCapabilities{
		ToolCalling:    ToolCallingNone,
		ImageInput:     ImageInputNone,
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
	if got.ImageInput != ImageInputNone {
		t.Fatalf("ImageInput = %q, want provider none", got.ImageInput)
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
	if got.Source != SourceMixed {
		t.Fatalf("Source = %q, want %q because image input remains catalog-derived", got.Source, SourceMixed)
	}
}

func TestResolveCatalogDefaults(t *testing.T) {
	tests := []struct {
		name       string
		provider   string
		kind       string
		model      string
		wantTools  string
		wantImages string
		wantSource string
	}{
		{name: "openai gpt", provider: "openai", kind: "cloud", model: "gpt-5.4-mini", wantTools: ToolCallingParallel, wantImages: ImageInputSupported, wantSource: SourceCatalog},
		{name: "anthropic claude", provider: "anthropic", kind: "cloud", model: "claude-sonnet-4-6", wantTools: ToolCallingBasic, wantImages: ImageInputSupported, wantSource: SourceCatalog},
		{name: "local unknown", provider: "ollama", kind: "local", model: "llama3.1", wantTools: ToolCallingUnknown, wantImages: ImageInputUnknown, wantSource: SourceCatalog},
		{name: "embedding rejects images", provider: "openai", kind: "cloud", model: "text-embedding-3-small", wantTools: ToolCallingNone, wantImages: ImageInputNone, wantSource: SourceCatalog},
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
			if got.ImageInput != tt.wantImages {
				t.Fatalf("ImageInput = %q, want %q", got.ImageInput, tt.wantImages)
			}
			if got.Source != tt.wantSource {
				t.Fatalf("Source = %q, want %q", got.Source, tt.wantSource)
			}
		})
	}
}

func TestResolveDoesNotInferImagesFromGenericVisionSubstring(t *testing.T) {
	got := Resolve("custom", "cloud", "provision-model", "upstream_v1_models")
	if got.ImageInput != ImageInputUnknown {
		t.Fatalf("ImageInput = %q, want fail-closed unknown for an unrecognized provider/model", got.ImageInput)
	}
	if got.Source != SourceCatalog {
		t.Fatalf("Source = %q, want catalog inference even when model discovery was provider-native", got.Source)
	}
}

func TestAggregateReturnsOnlyCapabilitiesCommonToEveryRoute(t *testing.T) {
	got := Aggregate([]types.ModelCapabilities{
		{
			ToolCalling:      ToolCallingParallel,
			ImageInput:       ImageInputNone,
			Streaming:        true,
			StreamingKnown:   true,
			MaxContextTokens: 128000,
			Source:           SourceCatalog,
		},
		{
			ToolCalling:      ToolCallingBasic,
			ImageInput:       ImageInputSupported,
			Streaming:        true,
			StreamingKnown:   true,
			MaxContextTokens: 64000,
			Source:           SourceProvider,
		},
	})
	if got.ToolCalling != ToolCallingUnknown || got.ImageInput != ImageInputUnknown {
		t.Fatalf("aggregate capabilities = %+v, want disagreements to become unknown", got)
	}
	if !got.Streaming || got.MaxContextTokens != 64000 || got.Source != SourceMixed {
		t.Fatalf("aggregate capabilities = %+v, want shared streaming, safe context minimum, mixed source", got)
	}
}

func TestImageCapableRequiresDeclaredSupport(t *testing.T) {
	for _, tt := range []struct {
		cap  string
		want bool
	}{
		{cap: ImageInputSupported, want: true},
		{cap: ImageInputUnknown, want: false},
		{cap: ImageInputNone, want: false},
		{cap: "", want: false},
	} {
		if got := ImageCapable(types.ModelCapabilities{ImageInput: tt.cap}); got != tt.want {
			t.Fatalf("ImageCapable(%q) = %v, want %v", tt.cap, got, tt.want)
		}
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
