package modelcaps

import (
	"strings"

	"github.com/hecate/agent-runtime/internal/providers"
	"github.com/hecate/agent-runtime/pkg/types"
)

const (
	ToolCallingUnknown  = "unknown"
	ToolCallingNone     = "none"
	ToolCallingBasic    = "basic"
	ToolCallingParallel = "parallel"

	SourceUnknown  = "unknown"
	SourceCatalog  = "catalog"
	SourceProvider = "provider"
)

// Resolve applies Hecate's capability precedence for one model:
// provider-discovered capability > catalog/default inference > unknown.
func Resolve(provider, providerKind, model, discoverySource string) types.ModelCapabilities {
	return ResolveWithProviderCapability(provider, providerKind, model, discoverySource, types.ModelCapabilities{})
}

func ResolveWithProviderCapability(provider, providerKind, model, discoverySource string, providerCap types.ModelCapabilities) types.ModelCapabilities {
	base := staticCapability(provider, providerKind, model, discoverySource)
	if hasProviderCapability(providerCap) {
		base = mergeCapability(base, providerCap, SourceProvider)
	}
	return normalize(base)
}

func hasProviderCapability(cap types.ModelCapabilities) bool {
	return cap.ToolCalling != "" || cap.Streaming || cap.MaxContextTokens > 0 || strings.TrimSpace(cap.Source) != ""
}

func ToolCapable(cap types.ModelCapabilities) bool {
	switch cap.ToolCalling {
	case ToolCallingBasic, ToolCallingParallel:
		return true
	default:
		return false
	}
}

func NormalizeToolCalling(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ToolCallingNone:
		return ToolCallingNone
	case ToolCallingBasic, "true", "yes", "supported":
		return ToolCallingBasic
	case ToolCallingParallel:
		return ToolCallingParallel
	default:
		return ToolCallingUnknown
	}
}

func staticCapability(provider, providerKind, model, discoverySource string) types.ModelCapabilities {
	source := SourceCatalog
	if strings.TrimSpace(discoverySource) != "" && !strings.EqualFold(discoverySource, "static") {
		source = SourceProvider
	}
	if strings.EqualFold(providerKind, string(providers.KindLocal)) {
		return types.ModelCapabilities{
			ToolCalling: ToolCallingUnknown,
			Streaming:   true,
			Source:      source,
		}
	}

	modelKey := strings.ToLower(strings.TrimSpace(model))
	providerKey := strings.ToLower(strings.TrimSpace(provider))
	toolCalling := ToolCallingUnknown
	switch {
	case strings.Contains(modelKey, "embedding"), strings.Contains(modelKey, "whisper"):
		toolCalling = ToolCallingNone
	case providerKey == "openai" && (strings.HasPrefix(modelKey, "gpt-4") || strings.HasPrefix(modelKey, "gpt-5") || strings.HasPrefix(modelKey, "o")):
		toolCalling = ToolCallingParallel
	case providerKey == "anthropic" && strings.HasPrefix(modelKey, "claude-"):
		toolCalling = ToolCallingBasic
	case providerKey == "gemini" && strings.HasPrefix(modelKey, "gemini-"):
		toolCalling = ToolCallingBasic
	case providerKey == "mistral" && (strings.Contains(modelKey, "mistral") || strings.Contains(modelKey, "codestral")):
		toolCalling = ToolCallingBasic
	case providerKey == "deepseek" && strings.HasPrefix(modelKey, "deepseek-"):
		toolCalling = ToolCallingBasic
	case providerKey == "xai" && strings.HasPrefix(modelKey, "grok-"):
		toolCalling = ToolCallingBasic
	}

	return types.ModelCapabilities{
		ToolCalling: toolCalling,
		Streaming:   true,
		Source:      source,
	}
}

func mergeCapability(base types.ModelCapabilities, cap types.ModelCapabilities, defaultSource string) types.ModelCapabilities {
	if strings.TrimSpace(cap.ToolCalling) != "" {
		base.ToolCalling = NormalizeToolCalling(cap.ToolCalling)
	}
	if cap.Streaming {
		base.Streaming = cap.Streaming
	}
	if cap.MaxContextTokens > 0 {
		base.MaxContextTokens = cap.MaxContextTokens
	}
	if strings.TrimSpace(cap.Source) != "" {
		base.Source = cap.Source
	} else {
		base.Source = defaultSource
	}
	return base
}

func normalize(cap types.ModelCapabilities) types.ModelCapabilities {
	cap.ToolCalling = NormalizeToolCalling(cap.ToolCalling)
	if strings.TrimSpace(cap.Source) == "" {
		cap.Source = SourceUnknown
	}
	return cap
}
