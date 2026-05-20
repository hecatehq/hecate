package catalog

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/hecate/agent-runtime/internal/providers"
	"github.com/hecate/agent-runtime/pkg/types"
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
	items := c.registry.All()
	out := make([]Entry, 0, len(items))
	for _, provider := range items {
		out = append(out, c.entryForProvider(ctx, provider))
	}
	return out
}

func (c *RegistryCatalog) Get(ctx context.Context, name string) (Entry, bool) {
	provider, ok := c.registry.Get(name)
	if !ok {
		return Entry{}, false
	}
	return c.entryForProvider(ctx, provider), true
}

func (c *RegistryCatalog) entryForProvider(ctx context.Context, provider providers.Provider) Entry {
	baseURL := providerBaseURL(provider)
	credentialState := providerCredentialState(provider)

	if e, ok := provider.(providers.Enabler); ok && !e.Enabled() {
		return Entry{
			Provider:        provider,
			Name:            provider.Name(),
			Kind:            provider.Kind(),
			BaseURL:         baseURL,
			CredentialState: credentialState,
			DiscoverySource: "control_plane",
			Healthy:         false,
			Status:          "disabled",
		}
	}

	if c.selfListenAddr != "" {
		if baseURL != "" {
			if isSelfReferentialURL(c.selfListenAddr, baseURL) {
				return Entry{
					Provider:        provider,
					Name:            provider.Name(),
					Kind:            provider.Kind(),
					BaseURL:         baseURL,
					CredentialState: credentialState,
					DiscoverySource: "self_referential",
					Healthy:         false,
					Status:          "degraded",
					LastError:       fmt.Sprintf("provider base URL %q points to the gateway's own address — run the local provider on a different port", baseURL),
					Error:           fmt.Sprintf("provider base URL %q points to the gateway's own address — run the local provider on a different port", baseURL),
				}
			}
		}
	}

	caps, err := provider.Capabilities(ctx)

	defaultModel := caps.DefaultModel
	if defaultModel == "" {
		defaultModel = provider.DefaultModel()
	}

	models := append([]string(nil), caps.Models...)
	modelCapabilities := make(map[string]types.ModelCapabilities, len(caps.ModelCapabilities))
	for model, cap := range caps.ModelCapabilities {
		modelCapabilities[model] = cap
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
		Name:                 provider.Name(),
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
