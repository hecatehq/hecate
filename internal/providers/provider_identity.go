package providers

import (
	"strings"

	"github.com/hecatehq/hecate/internal/config"
)

func configuredCapabilityFamily(cfg config.OpenAICompatibleProviderConfig) string {
	family := strings.TrimSpace(cfg.ProviderFamily)
	if builtIn, ok := config.BuiltInProviderByID(family); ok {
		return builtIn.ID
	}
	if family != "" {
		return strings.ToLower(family)
	}
	// The native Anthropic protocol itself is a sufficient capability
	// identity. OpenAI-compatible is intentionally not: it covers many
	// unrelated upstream families and custom endpoints.
	if strings.EqualFold(strings.TrimSpace(cfg.Protocol), "anthropic") {
		return "anthropic"
	}
	return ""
}
