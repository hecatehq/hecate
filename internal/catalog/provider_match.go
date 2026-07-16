package catalog

import (
	"strings"

	"github.com/hecatehq/hecate/internal/providers"
)

// ProviderIdentity describes the configured runtime name and stable aliases
// that may identify one provider.
type ProviderIdentity struct {
	Name    string
	Aliases []string
}

// ProviderIdentityResolution reports the highest-precedence provider match.
// Ambiguous is true when multiple providers match at the same precedence.
type ProviderIdentityResolution struct {
	Index     int
	Found     bool
	Ambiguous bool
}

// EntryMatchesProvider performs a pairwise identity check. Callers selecting
// among multiple entries must use ResolveProviderIdentity to enforce canonical
// precedence and ambiguity handling.
func EntryMatchesProvider(entry Entry, provider string) bool {
	identity := EntryProviderIdentity(entry)
	return ProviderIdentityMatches(identity.Name, identity.Aliases, provider)
}

// EntryProviderIdentity returns the provider identity represented by a catalog
// entry, including aliases reported directly by legacy catalog providers.
func EntryProviderIdentity(entry Entry) ProviderIdentity {
	aliases := entry.ProviderAliases
	if len(aliases) == 0 {
		if reporter, ok := entry.Provider.(providers.AliasReporter); ok {
			aliases = reporter.Aliases()
		}
	}
	return ProviderIdentity{Name: entry.Name, Aliases: aliases}
}

// ProviderIdentityMatches compares one configured runtime name and its stable
// aliases against a requested provider key.
func ProviderIdentityMatches(name string, aliases []string, provider string) bool {
	return providerIdentityMatchRank(ProviderIdentity{Name: name, Aliases: aliases}, provider) > 0
}

// ResolveProviderIdentity selects one provider identity with canonical runtime
// names taking precedence over aliases. Alias-only collisions are ambiguous so
// routing and capability admission can fail closed instead of depending on
// catalog order.
func ResolveProviderIdentity(candidates []ProviderIdentity, provider string) ProviderIdentityResolution {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return ProviderIdentityResolution{Index: -1}
	}

	resolution := ProviderIdentityResolution{Index: -1}
	bestRank := 0
	for index, candidate := range candidates {
		rank := providerIdentityMatchRank(candidate, provider)
		if rank == 0 || rank < bestRank {
			continue
		}
		if rank > bestRank {
			bestRank = rank
			resolution = ProviderIdentityResolution{Index: index, Found: true}
			continue
		}
		resolution.Ambiguous = true
	}
	return resolution
}

func providerIdentityMatchRank(identity ProviderIdentity, provider string) int {
	provider = strings.TrimSpace(provider)
	name := strings.TrimSpace(identity.Name)
	if provider == "" {
		return 0
	}
	if name != "" && name == provider {
		return 3
	}
	if providerNameMatches(name, provider) {
		return 2
	}
	for _, alias := range identity.Aliases {
		if providerNameMatches(alias, provider) {
			return 1
		}
	}
	return 0
}

func providerNameMatches(candidate, provider string) bool {
	candidate = strings.TrimSpace(candidate)
	provider = strings.TrimSpace(provider)
	if candidate == "" || provider == "" {
		return false
	}
	if strings.EqualFold(candidate, provider) {
		return true
	}
	candidateKey := providerLookupKey(candidate)
	providerKey := providerLookupKey(provider)
	return candidateKey != "" && candidateKey == providerKey
}

func providerLookupKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	out := make([]rune, 0, len(value))
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out = append(out, r)
			lastDash = false
			continue
		}
		if !lastDash {
			out = append(out, '-')
			lastDash = true
		}
	}
	return strings.Trim(string(out), "-")
}
