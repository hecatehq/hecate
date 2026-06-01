package catalog

import (
	"context"

	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

// fakeProvider is the package-wide test double for the providers.Provider
// interface. Several test files exercise the catalog/registry code path
// against it; defining it here keeps the fake colocated with its users
// instead of latched to one specific TestX file.
//
// Capabilities() returns the synthesized "single default model" snapshot
// when no explicit caps are seeded — it's what most tests want and saves
// every site from filling the field for the common case.
type fakeProvider struct {
	name         string
	kind         providers.Kind
	defaultModel string
	caps         providers.Capabilities
	capsErr      error
}

func (p *fakeProvider) Name() string         { return p.name }
func (p *fakeProvider) Kind() providers.Kind { return p.kind }
func (p *fakeProvider) DefaultModel() string { return p.defaultModel }
func (p *fakeProvider) Capabilities(context.Context) (providers.Capabilities, error) {
	if p.caps.Name != "" || len(p.caps.Models) > 0 || p.caps.DefaultModel != "" || !p.caps.RefreshedAt.IsZero() {
		return p.caps, p.capsErr
	}
	return providers.Capabilities{
		Name:         p.name,
		Kind:         p.kind,
		DefaultModel: p.defaultModel,
		Models:       []string{p.defaultModel},
	}, p.capsErr
}
func (p *fakeProvider) Chat(context.Context, types.ChatRequest) (*types.ChatResponse, error) {
	return nil, nil
}
func (p *fakeProvider) Supports(string) bool { return true }

// fakeProviderWithBaseURL augments fakeProvider with a BaseURL() method
// so the self-referential branch in entryForProvider can be exercised.
type fakeProviderWithBaseURL struct {
	*fakeProvider
	baseURL string
}

func (p *fakeProviderWithBaseURL) BaseURL() string { return p.baseURL }
