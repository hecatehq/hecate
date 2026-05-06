package modelcaps

import (
	"testing"

	"github.com/hecate/agent-runtime/internal/controlplane"
)

func TestResolvePrecedence(t *testing.T) {
	streaming := false
	state := controlplane.State{
		ModelCapabilityProbeState: []controlplane.ModelCapabilityRecord{{
			Provider:    "ollama",
			Model:       "llama3.1",
			ToolCalling: ToolCallingBasic,
		}},
		ModelCapabilityOverrides: []controlplane.ModelCapabilityRecord{{
			Provider:         "ollama",
			Model:            "llama3.1",
			ToolCalling:      ToolCallingNone,
			Streaming:        &streaming,
			MaxContextTokens: 8192,
		}},
	}

	got := Resolve("ollama", "local", "llama3.1", "provider", state)
	if got.ToolCalling != ToolCallingNone {
		t.Fatalf("ToolCalling = %q, want %q", got.ToolCalling, ToolCallingNone)
	}
	if got.Streaming {
		t.Fatalf("Streaming = true, want false from override")
	}
	if got.MaxContextTokens != 8192 {
		t.Fatalf("MaxContextTokens = %d, want 8192", got.MaxContextTokens)
	}
	if got.Source != SourceOperatorOverride {
		t.Fatalf("Source = %q, want %q", got.Source, SourceOperatorOverride)
	}
}

func TestResolveProbeBeatsLocalUnknownDefault(t *testing.T) {
	state := controlplane.State{
		ModelCapabilityProbeState: []controlplane.ModelCapabilityRecord{{
			Provider:    "ollama",
			Model:       "qwen2.5-coder",
			ToolCalling: ToolCallingBasic,
		}},
	}

	got := Resolve("ollama", "local", "qwen2.5-coder", "provider", state)
	if got.ToolCalling != ToolCallingBasic {
		t.Fatalf("ToolCalling = %q, want %q", got.ToolCalling, ToolCallingBasic)
	}
	if got.Source != SourceProbe {
		t.Fatalf("Source = %q, want %q", got.Source, SourceProbe)
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
			got := Resolve(tt.provider, tt.kind, tt.model, source, controlplane.State{})
			if got.ToolCalling != tt.wantTools {
				t.Fatalf("ToolCalling = %q, want %q", got.ToolCalling, tt.wantTools)
			}
			if got.Source != tt.wantSource {
				t.Fatalf("Source = %q, want %q", got.Source, tt.wantSource)
			}
		})
	}
}
