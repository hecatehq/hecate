package catalog

import (
	"strings"

	"github.com/hecatehq/hecate/internal/providers"
)

func EntryMatchesProvider(entry Entry, provider string) bool {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return false
	}
	if providerNameMatches(entry.Name, provider) {
		return true
	}
	if reporter, ok := entry.Provider.(providers.AliasReporter); ok {
		for _, alias := range reporter.Aliases() {
			if providerNameMatches(alias, provider) {
				return true
			}
		}
	}
	return false
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
