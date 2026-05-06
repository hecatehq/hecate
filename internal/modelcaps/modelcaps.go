package modelcaps

import (
	"strings"

	"github.com/hecate/agent-runtime/internal/controlplane"
	"github.com/hecate/agent-runtime/internal/providers"
	"github.com/hecate/agent-runtime/pkg/types"
)

const (
	ToolCallingUnknown  = "unknown"
	ToolCallingNone     = "none"
	ToolCallingBasic    = "basic"
	ToolCallingParallel = "parallel"

	SourceUnknown          = "unknown"
	SourceCatalog          = "catalog"
	SourceProvider         = "provider"
	SourceProbe            = "probe"
	SourceOperatorOverride = "operator_override"
)

// Resolve applies Hecate's capability precedence for one model:
// operator override > manual probe > catalog/default inference > unknown.
func Resolve(provider, providerKind, model, discoverySource string, state controlplane.State) types.ModelCapabilities {
	base := staticCapability(provider, providerKind, model, discoverySource)
	if probe, ok := findRecord(state.ModelCapabilityProbeState, provider, model); ok {
		base = mergeRecord(base, probe, SourceProbe)
	}
	if override, ok := findRecord(state.ModelCapabilityOverrides, provider, model); ok {
		base = mergeRecord(base, override, SourceOperatorOverride)
	}
	return normalize(base)
}

func ToolCapable(cap types.ModelCapabilities) bool {
	switch cap.ToolCalling {
	case ToolCallingNone:
		return false
	default:
		return true
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

func findRecord(records []controlplane.ModelCapabilityRecord, provider, model string) (controlplane.ModelCapabilityRecord, bool) {
	for _, record := range records {
		if strings.EqualFold(strings.TrimSpace(record.Provider), strings.TrimSpace(provider)) &&
			strings.EqualFold(strings.TrimSpace(record.Model), strings.TrimSpace(model)) {
			return record, true
		}
	}
	return controlplane.ModelCapabilityRecord{}, false
}

func mergeRecord(base types.ModelCapabilities, record controlplane.ModelCapabilityRecord, source string) types.ModelCapabilities {
	if value := NormalizeToolCalling(record.ToolCalling); value != ToolCallingUnknown || strings.EqualFold(strings.TrimSpace(record.ToolCalling), ToolCallingUnknown) {
		base.ToolCalling = value
	}
	if record.Streaming != nil {
		base.Streaming = *record.Streaming
	}
	if record.MaxContextTokens > 0 {
		base.MaxContextTokens = record.MaxContextTokens
	}
	base.Source = source
	return base
}

func normalize(cap types.ModelCapabilities) types.ModelCapabilities {
	cap.ToolCalling = NormalizeToolCalling(cap.ToolCalling)
	if strings.TrimSpace(cap.Source) == "" {
		cap.Source = SourceUnknown
	}
	return cap
}
