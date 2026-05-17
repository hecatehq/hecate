package providers

import (
	"log/slog"
	"strings"

	"github.com/hecate/agent-runtime/internal/config"
)

// protocolDispatch describes one inbound-protocol family the gateway can
// instantiate. It centralizes the per-protocol facts that callers used
// to discover via scattered `Protocol == "anthropic"` checks:
//
//   - Constructor — how to build a Provider for this protocol.
//   - SupportsAnthropicCache — whether the gateway-wide Anthropic prompt
//     cache toggle (config.Providers.AnthropicCacheDisabled) is
//     meaningful for instances on this protocol. Only the Anthropic
//     adapter respects the flag today; OpenAI-compatible providers
//     ignore it because the upstream has no equivalent knob.
//
// Add a third protocol by registering one entry below. The two original
// dispatch sites (buildProviders constructor switch and the cache-marker
// gating in hydrateMutableProviders) now read from this table instead of
// inline string compares.
type protocolDispatch struct {
	Constructor            func(config.OpenAICompatibleProviderConfig, *slog.Logger) Provider
	SupportsAnthropicCache bool
}

// protocolDispatchByName maps the canonical (lowercase, trimmed)
// protocol value to its dispatch entry. Unknown protocols fall back to
// the OpenAI-compatible adapter via lookupProtocolDispatch — keeping
// the same default semantics buildProviders had before the table.
var protocolDispatchByName = map[string]protocolDispatch{
	"anthropic": {
		Constructor: func(cfg config.OpenAICompatibleProviderConfig, logger *slog.Logger) Provider {
			return NewAnthropicProvider(cfg, logger)
		},
		SupportsAnthropicCache: true,
	},
	"openai": {
		Constructor: func(cfg config.OpenAICompatibleProviderConfig, logger *slog.Logger) Provider {
			return NewOpenAICompatibleProvider(cfg, logger)
		},
		SupportsAnthropicCache: false,
	},
}

// lookupProtocolDispatch returns the dispatch entry for the given
// protocol string (case-insensitive, whitespace tolerant). Unknown
// protocols default to the OpenAI-compatible entry; the boolean
// signals whether the protocol was explicitly registered (vs. falling
// through to default).
func lookupProtocolDispatch(protocol string) (protocolDispatch, bool) {
	key := strings.ToLower(strings.TrimSpace(protocol))
	if entry, ok := protocolDispatchByName[key]; ok {
		return entry, true
	}
	return protocolDispatchByName["openai"], false
}
