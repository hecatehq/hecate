// Package requestscope normalizes the routing hints carried on each
// inbound chat request. In single-user mode the only field is the
// provider hint; the multi-tenant scoping that previously rode through
// here was removed.
package requestscope

import (
	"strings"

	"github.com/hecate/agent-runtime/pkg/types"
)

// Build constructs a RequestScope for a single-user gateway request.
// `provider` is the optional routing hint.
func Build(provider string) types.RequestScope {
	return types.RequestScope{ProviderHint: strings.TrimSpace(provider)}
}

// Normalize trims the provider hint. Kept as a separate function for
// callers that already have a RequestScope from elsewhere.
func Normalize(scope types.RequestScope) types.RequestScope {
	scope.ProviderHint = strings.TrimSpace(scope.ProviderHint)
	return scope
}
