package catalog

import (
	"context"

	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

type Entry struct {
	Provider             providers.Provider
	ProviderInstance     types.ProviderInstanceIdentity
	Name                 string
	ProviderAliases      []string
	ProviderFamily       string
	Kind                 providers.Kind
	BaseURL              string
	CredentialState      string
	DefaultModel         string
	Models               []string
	ModelCapabilities    map[string]types.ModelCapabilities
	DiscoveredModelCount int
	DiscoverySource      string
	RefreshedAt          string
	LastCheckedAt        string
	LastError            string
	HealthReason         string
	OpenUntil            string
	LastLatencyMS        int64
	ConsecutiveFailures  int
	TotalSuccesses       int64
	TotalFailures        int64
	Timeouts             int64
	ServerErrors         int64
	RateLimits           int64
	Healthy              bool
	Status               string
	Error                string
}

type Catalog interface {
	Snapshot(ctx context.Context) []Entry
	Get(ctx context.Context, name string) (Entry, bool)
}
