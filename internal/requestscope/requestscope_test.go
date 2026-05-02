package requestscope

import (
	"testing"

	"github.com/hecate/agent-runtime/pkg/types"
)

func TestBuildTrimsProviderHint(t *testing.T) {
	t.Parallel()

	if got := Build("  ollama  "); got.ProviderHint != "ollama" {
		t.Fatalf("provider_hint = %q, want ollama", got.ProviderHint)
	}
}

func TestNormalizeTrimsProviderHint(t *testing.T) {
	t.Parallel()

	got := Normalize(types.RequestScope{ProviderHint: "  openai "})
	if got.ProviderHint != "openai" {
		t.Fatalf("provider_hint = %q, want openai", got.ProviderHint)
	}
}
