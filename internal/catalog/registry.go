package catalog

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

type RegistryCatalog struct {
	registry       providers.Registry
	healthTracker  providers.HealthTracker
	selfListenAddr string
}

type baseURLer interface {
	BaseURL() string
}

func NewRegistryCatalog(registry providers.Registry, healthTracker providers.HealthTracker) *RegistryCatalog {
	return &RegistryCatalog{registry: registry, healthTracker: healthTracker}
}

func NewRegistryCatalogWithSelfAddr(registry providers.Registry, healthTracker providers.HealthTracker, selfListenAddr string) *RegistryCatalog {
	return &RegistryCatalog{registry: registry, healthTracker: healthTracker, selfListenAddr: selfListenAddr}
}

func (c *RegistryCatalog) Snapshot(ctx context.Context) []Entry {
	return c.snapshot(ctx, false)
}

func (c *RegistryCatalog) SnapshotRefresh(ctx context.Context) []Entry {
	return c.snapshot(ctx, true)
}

func (c *RegistryCatalog) snapshot(ctx context.Context, refresh bool) []Entry {
	items := c.registry.AllInstances()
	out := make([]Entry, 0, len(items))
	for _, instance := range items {
		out = append(out, c.entryForProvider(ctx, instance, refresh))
	}
	return out
}

func (c *RegistryCatalog) Get(ctx context.Context, name string) (Entry, bool) {
	instance, ok := c.registry.GetInstance(name)
	if !ok {
		return Entry{}, false
	}
	return c.entryForProvider(ctx, instance, false), true
}

func (c *RegistryCatalog) entryForProvider(ctx context.Context, instance providers.ProviderInstance, refresh bool) Entry {
	provider := instance.Provider
	baseURL := providerBaseURL(provider)
	credentialState := providerCredentialState(provider)
	providerAliases := providerAliases(provider)
	providerFamily := providerCapabilityFamily(provider)

	if e, ok := provider.(providers.Enabler); ok && !e.Enabled() {
		return Entry{
			Provider:         provider,
			ProviderInstance: instance.Identity,
			Name:             provider.Name(),
			ProviderAliases:  providerAliases,
			ProviderFamily:   providerFamily,
			Kind:             provider.Kind(),
			BaseURL:          baseURL,
			CredentialState:  credentialState,
			DiscoverySource:  "control_plane",
			Healthy:          false,
			Status:           "disabled",
		}
	}

	if c.selfListenAddr != "" {
		if baseURL != "" {
			if isSelfReferentialURL(c.selfListenAddr, baseURL) {
				return Entry{
					Provider:         provider,
					ProviderInstance: instance.Identity,
					Name:             provider.Name(),
					ProviderAliases:  providerAliases,
					ProviderFamily:   providerFamily,
					Kind:             provider.Kind(),
					BaseURL:          baseURL,
					CredentialState:  credentialState,
					DiscoverySource:  "self_referential",
					Healthy:          false,
					Status:           "degraded",
					LastError:        fmt.Sprintf("provider base URL %q points to the gateway's own address — run the local provider on a different port", baseURL),
					Error:            fmt.Sprintf("provider base URL %q points to the gateway's own address — run the local provider on a different port", baseURL),
				}
			}
		}
	}

	var caps providers.Capabilities
	var err error
	if refresh {
		if refresher, ok := provider.(providers.CapabilityRefresher); ok {
			caps, err = refresher.RefreshCapabilities(ctx)
		} else {
			caps, err = provider.Capabilities(ctx)
		}
	} else {
		caps, err = provider.Capabilities(ctx)
	}

	defaultModel := caps.DefaultModel
	if defaultModel == "" {
		defaultModel = provider.DefaultModel()
	}

	models := append([]string(nil), caps.Models...)
	var modelCapabilities map[string]types.ModelCapabilities
	if len(caps.ModelCapabilities) > 0 {
		modelCapabilities = make(map[string]types.ModelCapabilities, len(caps.ModelCapabilities))
		for model, cap := range caps.ModelCapabilities {
			modelCapabilities[model] = cap
		}
	}
	discoveredModelCount := len(models)
	if len(models) == 0 && defaultModel != "" {
		models = []string{defaultModel}
	}

	discoverySource := caps.DiscoverySource
	if discoverySource == "" {
		discoverySource = "provider_default"
	}

	entry := Entry{
		Provider:             provider,
		ProviderInstance:     instance.Identity,
		Name:                 provider.Name(),
		ProviderAliases:      providerAliases,
		ProviderFamily:       providerFamily,
		Kind:                 provider.Kind(),
		BaseURL:              baseURL,
		CredentialState:      credentialState,
		DefaultModel:         defaultModel,
		Models:               models,
		ModelCapabilities:    modelCapabilities,
		DiscoveredModelCount: discoveredModelCount,
		DiscoverySource:      discoverySource,
		Healthy:              err == nil,
		Status:               "healthy",
		LastError:            caps.LastError,
	}
	if !caps.RefreshedAt.IsZero() {
		refreshedAt := caps.RefreshedAt.UTC().Format(time.RFC3339)
		entry.RefreshedAt = refreshedAt
		entry.LastCheckedAt = refreshedAt
	}
	if err != nil {
		entry.Healthy = false
		entry.Status = "degraded"
		entry.LastError = err.Error()
		entry.Error = err.Error()
	} else if entry.LastError != "" {
		entry.Status = "degraded"
		entry.Error = entry.LastError
	}

	if c.healthTracker != nil {
		state := c.healthTracker.State(provider.Name())
		if checkedAt := latestHealthTimestamp(state); !checkedAt.IsZero() {
			entry.LastCheckedAt = checkedAt.UTC().Format(time.RFC3339)
		}
		if !state.OpenUntil.IsZero() {
			entry.OpenUntil = state.OpenUntil.UTC().Format(time.RFC3339)
		}
		entry.LastLatencyMS = state.LastLatency.Milliseconds()
		entry.ConsecutiveFailures = state.ConsecutiveFailures
		entry.TotalSuccesses = state.TotalSuccesses
		entry.TotalFailures = state.TotalFailures
		entry.Timeouts = state.Timeouts
		entry.ServerErrors = state.ServerErrors
		entry.RateLimits = state.RateLimits
		if !state.Available {
			entry.Healthy = false
			entry.Status = string(state.Status)
			entry.HealthReason = providers.HealthStateReason(state)
			entry.LastError = providers.FormatHealthStateError(provider.Name(), state)
			entry.Error = entry.LastError
		} else if state.Status != "" {
			entry.Status = string(state.Status)
			entry.HealthReason = providers.HealthStateReason(state)
		}
	}

	return entry
}

func providerAliases(provider providers.Provider) []string {
	reporter, ok := provider.(providers.AliasReporter)
	if !ok {
		return nil
	}
	aliases := reporter.Aliases()
	out := make([]string, 0, len(aliases))
	seen := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		key := providerLookupKey(alias)
		if alias == "" || key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, alias)
	}
	return out
}

func providerCapabilityFamily(provider providers.Provider) string {
	if reporter, ok := provider.(providers.CapabilityFamilyReporter); ok {
		return strings.TrimSpace(reporter.CapabilityFamily())
	}
	// Provider implementations that predate the explicit family contract are
	// treated as having their routing name as their canonical identity. This
	// keeps third-party/test providers compatible without weakening concrete
	// OpenAI-compatible providers, which implement the reporter and may return
	// an intentionally empty family for custom endpoints.
	return strings.TrimSpace(provider.Name())
}

func providerBaseURL(provider providers.Provider) string {
	if b, ok := provider.(baseURLer); ok {
		return b.BaseURL()
	}
	return ""
}

func providerCredentialState(provider providers.Provider) string {
	if reporter, ok := provider.(providers.CredentialReporter); ok {
		return string(reporter.CredentialState())
	}
	return string(providers.CredentialStateUnknown)
}

func latestHealthTimestamp(state providers.HealthState) time.Time {
	switch {
	case state.LastSuccessAt.IsZero():
		return state.LastFailureAt
	case state.LastFailureAt.IsZero():
		return state.LastSuccessAt
	case state.LastFailureAt.After(state.LastSuccessAt):
		return state.LastFailureAt
	default:
		return state.LastSuccessAt
	}
}

// isSelfReferentialURL returns true when providerBaseURL points to the same
// loopback port that the gateway is listening on.
func isSelfReferentialURL(selfListenAddr, providerBaseURL string) bool {
	if selfListenAddr == "" || providerBaseURL == "" {
		return false
	}

	_, selfPort, err := net.SplitHostPort(selfListenAddr)
	if err != nil || selfPort == "" {
		return false
	}

	u, err := url.Parse(providerBaseURL)
	if err != nil {
		return false
	}

	providerHost, providerPort, err := net.SplitHostPort(u.Host)
	if err != nil {
		return false
	}

	if providerPort != selfPort {
		return false
	}

	ip := net.ParseIP(providerHost)
	if ip != nil {
		return ip.IsLoopback()
	}
	return providerHost == "localhost"
}
