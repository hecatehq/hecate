package modelcaps

import (
	"strings"

	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

const (
	ToolCallingUnknown  = "unknown"
	ToolCallingNone     = "none"
	ToolCallingBasic    = "basic"
	ToolCallingParallel = "parallel"

	ImageInputUnknown   = "unknown"
	ImageInputNone      = "none"
	ImageInputSupported = "supported"

	SourceUnknown  = "unknown"
	SourceCatalog  = "catalog"
	SourceProvider = "provider"
	SourceMixed    = "mixed"
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
	return cap.ToolCalling != "" || cap.ImageInput != "" || cap.StreamingKnown || cap.Streaming || cap.MaxContextTokens > 0
}

func ToolCapable(cap types.ModelCapabilities) bool {
	switch cap.ToolCalling {
	case ToolCallingBasic, ToolCallingParallel:
		return true
	default:
		return false
	}
}

func ImageCapable(cap types.ModelCapabilities) bool {
	return cap.ImageInput == ImageInputSupported
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

func NormalizeImageInput(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ImageInputNone, "false", "no", "unsupported":
		return ImageInputNone
	case ImageInputSupported, "true", "yes":
		return ImageInputSupported
	default:
		return ImageInputUnknown
	}
}

func staticCapability(provider, providerKind, model, _ string) types.ModelCapabilities {
	if strings.EqualFold(providerKind, string(providers.KindLocal)) {
		return types.ModelCapabilities{
			ToolCalling: ToolCallingUnknown,
			Streaming:   true,
			Source:      SourceCatalog,
		}
	}

	modelKey := strings.ToLower(strings.TrimSpace(model))
	providerKey := strings.ToLower(strings.TrimSpace(provider))
	toolCalling := ToolCallingUnknown
	imageInput := ImageInputUnknown
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
	switch {
	case strings.Contains(modelKey, "embedding"), strings.Contains(modelKey, "whisper"), strings.Contains(modelKey, "tts"):
		imageInput = ImageInputNone
	case providerKey == "openai" && (strings.HasPrefix(modelKey, "gpt-4o") || strings.HasPrefix(modelKey, "gpt-4.1") || strings.HasPrefix(modelKey, "gpt-5")):
		imageInput = ImageInputSupported
	case providerKey == "anthropic" && (strings.HasPrefix(modelKey, "claude-3") || strings.HasPrefix(modelKey, "claude-sonnet-4") || strings.HasPrefix(modelKey, "claude-opus-4") || strings.HasPrefix(modelKey, "claude-haiku-4")):
		imageInput = ImageInputSupported
	case providerKey == "gemini" && strings.HasPrefix(modelKey, "gemini-"):
		imageInput = ImageInputSupported
	case providerKey == "mistral" && strings.Contains(modelKey, "pixtral"):
		imageInput = ImageInputSupported
	}

	return types.ModelCapabilities{
		ToolCalling: toolCalling,
		ImageInput:  imageInput,
		Streaming:   true,
		Source:      SourceCatalog,
	}
}

func mergeCapability(base types.ModelCapabilities, cap types.ModelCapabilities, defaultSource string) types.ModelCapabilities {
	toolCallingOverride := strings.TrimSpace(cap.ToolCalling) != ""
	imageInputOverride := strings.TrimSpace(cap.ImageInput) != ""
	streamingOverride := cap.StreamingKnown || cap.Streaming
	maxContextOverride := cap.MaxContextTokens > 0
	if toolCallingOverride {
		base.ToolCalling = NormalizeToolCalling(cap.ToolCalling)
	}
	if imageInputOverride {
		base.ImageInput = NormalizeImageInput(cap.ImageInput)
	}
	if streamingOverride {
		base.Streaming = cap.Streaming
		base.StreamingKnown = cap.StreamingKnown
	}
	if maxContextOverride {
		base.MaxContextTokens = cap.MaxContextTokens
	}
	base.Source = mergedSource(base.Source, cap.Source, defaultSource,
		toolCallingOverride,
		imageInputOverride,
		streamingOverride,
		maxContextOverride,
		base.MaxContextTokens,
	)
	return base
}

func mergedSource(
	baseSource string,
	capSource string,
	defaultSource string,
	toolCallingOverride bool,
	imageInputOverride bool,
	streamingOverride bool,
	maxContextOverride bool,
	effectiveMaxContext int,
) string {
	incomingSource := strings.TrimSpace(capSource)
	if incomingSource == "" {
		incomingSource = defaultSource
	}
	if incomingSource == SourceMixed {
		return SourceMixed
	}
	allEffectiveDimensionsOverridden := toolCallingOverride && imageInputOverride && streamingOverride
	if effectiveMaxContext > 0 {
		allEffectiveDimensionsOverridden = allEffectiveDimensionsOverridden && maxContextOverride
	}
	if allEffectiveDimensionsOverridden || baseSource == incomingSource {
		return incomingSource
	}
	return SourceMixed
}

// Aggregate returns the capability guarantees common to every candidate route.
// A disagreement becomes unknown (or false/zero) so an auto-routed session does
// not accidentally persist one arbitrary provider's stronger capability as if
// it applied to every eligible provider.
func Aggregate(capabilities []types.ModelCapabilities) types.ModelCapabilities {
	if len(capabilities) == 0 {
		return normalize(types.ModelCapabilities{})
	}
	result := normalize(capabilities[0])
	for _, raw := range capabilities[1:] {
		capability := normalize(raw)
		if result.ToolCalling != capability.ToolCalling {
			result.ToolCalling = ToolCallingUnknown
		}
		if result.ImageInput != capability.ImageInput {
			result.ImageInput = ImageInputUnknown
		}
		result.Streaming = result.Streaming && capability.Streaming
		result.StreamingKnown = result.StreamingKnown && capability.StreamingKnown
		if result.MaxContextTokens <= 0 || capability.MaxContextTokens <= 0 {
			result.MaxContextTokens = 0
		} else if capability.MaxContextTokens < result.MaxContextTokens {
			result.MaxContextTokens = capability.MaxContextTokens
		}
		if result.Source != capability.Source {
			result.Source = SourceMixed
		}
	}
	return result
}

func normalize(cap types.ModelCapabilities) types.ModelCapabilities {
	cap.ToolCalling = NormalizeToolCalling(cap.ToolCalling)
	cap.ImageInput = NormalizeImageInput(cap.ImageInput)
	if strings.TrimSpace(cap.Source) == "" {
		cap.Source = SourceUnknown
	}
	return cap
}
