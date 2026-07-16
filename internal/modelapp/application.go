package modelapp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/catalog"
	"github.com/hecatehq/hecate/internal/gateway"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/pkg/types"
)

var (
	ErrServiceNotConfigured = errors.New("model service is not configured")
	ErrProviderAmbiguous    = errors.New("provider identity is ambiguous")
)

type Service interface {
	ListModels(ctx context.Context) (*gateway.ModelsResult, error)
	RefreshModels(ctx context.Context) (*gateway.ModelsResult, error)
	ProviderModelReadiness(ctx context.Context, provider, model string) (*gateway.ProviderModelReadinessResult, error)
}

type Application struct {
	service Service
}

type Options struct {
	Service Service
}

type ListModelsCommand struct {
	Refresh bool
}

// ProviderRoute is the internal provider boundary resolved from one catalog
// snapshot. Instance is opaque and must only be used to fence execution and
// provider-bound runtime state; it is not operator-facing model metadata.
type ProviderRoute struct {
	Name     string
	Instance types.ProviderInstanceIdentity
}

type modelCatalogSnapshot struct {
	models             []types.ModelInfo
	providerIdentities []catalog.ProviderIdentity
}

type ReadinessError struct {
	Cause     error
	Readiness types.ModelReadiness
}

// ProviderAmbiguityError identifies an operator-supplied provider key that
// cannot safely select one configured runtime provider.
type ProviderAmbiguityError struct {
	Provider string
}

func (e ProviderAmbiguityError) Error() string {
	return fmt.Sprintf("provider %q matches multiple configured providers", e.Provider)
}

func (e ProviderAmbiguityError) Unwrap() error {
	return ErrProviderAmbiguous
}

func (e ReadinessError) Error() string {
	if e.Cause == nil {
		return ""
	}
	return e.Cause.Error()
}

func (e ReadinessError) Unwrap() error {
	return e.Cause
}

func New(opts Options) *Application {
	return &Application{service: opts.Service}
}

func (app *Application) ListModels(ctx context.Context, cmd ListModelsCommand) ([]types.ModelInfo, error) {
	snapshot, err := app.loadModelCatalog(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return snapshot.models, nil
}

func (app *Application) loadModelCatalog(ctx context.Context, cmd ListModelsCommand) (modelCatalogSnapshot, error) {
	if app == nil || app.service == nil {
		return modelCatalogSnapshot{}, ErrServiceNotConfigured
	}
	var (
		result *gateway.ModelsResult
		err    error
	)
	if cmd.Refresh {
		result, err = app.service.RefreshModels(ctx)
	} else {
		result, err = app.service.ListModels(ctx)
	}
	if err != nil {
		return modelCatalogSnapshot{}, err
	}
	out := make([]types.ModelInfo, 0, len(result.Models))
	for _, item := range result.Models {
		out = append(out, modelWithResolvedCapabilities(item))
	}
	providerIdentities := make([]catalog.ProviderIdentity, 0, len(result.ProviderIdentities))
	for _, identity := range result.ProviderIdentities {
		identity.Aliases = append([]string(nil), identity.Aliases...)
		providerIdentities = append(providerIdentities, identity)
	}
	return modelCatalogSnapshot{models: out, providerIdentities: providerIdentities}, nil
}

func (app *Application) ResolveCapabilities(ctx context.Context, provider, model string) (types.ModelCapabilities, error) {
	provider = normalizeProvider(provider)
	model = strings.TrimSpace(model)
	if model == "" {
		return types.ModelCapabilities{}, fmt.Errorf("model is required")
	}
	if app == nil || app.service == nil {
		return modelcaps.Resolve(provider, "", model, ""), nil
	}
	snapshot, err := app.loadModelCatalog(ctx, ListModelsCommand{})
	if err != nil {
		return types.ModelCapabilities{}, err
	}
	models := snapshot.models
	if provider != "" {
		item, ok, resolveErr := resolveProviderModel(models, snapshot.providerIdentities, provider, model)
		if resolveErr != nil {
			return types.ModelCapabilities{}, resolveErr
		}
		if ok {
			return item.Capabilities, nil
		}
		err = fmt.Errorf("model %q is not available from provider %q", model, provider)
		return types.ModelCapabilities{}, app.withModelReadiness(ctx, provider, model, err)
	}

	matches := make([]types.ModelInfo, 0, 2)
	for _, item := range models {
		if !strings.EqualFold(item.ID, model) {
			continue
		}
		matches = append(matches, item)
	}
	if len(matches) > 0 {
		capabilities := make([]types.ModelCapabilities, 0, len(matches))
		for _, item := range matches {
			if modelRouteReady(item) {
				capabilities = append(capabilities, item.Capabilities)
			}
		}
		// Capability discovery remains useful for a temporarily blocked model,
		// but a routable subset takes precedence whenever one exists so Auto
		// never snapshots a route that cannot actually be selected.
		if len(capabilities) == 0 {
			for _, item := range matches {
				capabilities = append(capabilities, item.Capabilities)
			}
		}
		return modelcaps.Aggregate(capabilities), nil
	}
	err = fmt.Errorf("model %q is not available from any configured provider", model)
	return types.ModelCapabilities{}, app.withModelReadiness(ctx, provider, model, err)
}

// SupportsImageInput reports whether at least one configured route matching
// provider/model explicitly supports image input. An empty or "auto" provider
// checks every matching provider so admission cannot accidentally inspect a
// different route from the router.
func (app *Application) SupportsImageInput(ctx context.Context, provider, model string) (bool, error) {
	provider = normalizeProvider(provider)
	model = strings.TrimSpace(model)
	if model == "" {
		return false, fmt.Errorf("model is required")
	}
	if app == nil || app.service == nil {
		return modelcaps.ImageCapable(modelcaps.Resolve(provider, "", model, "")), nil
	}
	snapshot, err := app.loadModelCatalog(ctx, ListModelsCommand{})
	if err != nil {
		return false, err
	}
	models := snapshot.models
	if provider != "" {
		item, ok, resolveErr := resolveProviderModel(models, snapshot.providerIdentities, provider, model)
		if resolveErr != nil {
			return false, resolveErr
		}
		return ok && modelRouteReady(item) && modelcaps.ImageCapable(item.Capabilities), nil
	}
	for _, item := range models {
		if strings.EqualFold(item.ID, model) && modelRouteReady(item) && modelcaps.ImageCapable(item.Capabilities) {
			return true, nil
		}
	}
	return false, nil
}

// ResolveProviderName maps a requested control-plane id, preset id, or runtime
// name to the configured runtime name used in route snapshots. Auto routing
// intentionally remains unresolved so historical image reuse stays fail-closed
// until a concrete provider has been selected.
func (app *Application) ResolveProviderName(ctx context.Context, provider, model string) (string, error) {
	route, err := app.ResolveProviderRoute(ctx, provider, model)
	return route.Name, err
}

// ResolveProviderRoute maps a requested control-plane id, preset id, or runtime
// name to both the canonical runtime name and the exact provider instance seen
// by model admission. Auto intentionally remains unresolved.
func (app *Application) ResolveProviderRoute(ctx context.Context, provider, model string) (ProviderRoute, error) {
	provider = normalizeProvider(provider)
	model = strings.TrimSpace(model)
	if provider == "" {
		return ProviderRoute{}, nil
	}
	if model == "" {
		return ProviderRoute{}, fmt.Errorf("model is required")
	}
	if app == nil || app.service == nil {
		return ProviderRoute{Name: provider}, nil
	}
	snapshot, err := app.loadModelCatalog(ctx, ListModelsCommand{})
	if err != nil {
		return ProviderRoute{}, err
	}
	item, ok, resolveErr := resolveProviderModel(snapshot.models, snapshot.providerIdentities, provider, model)
	if resolveErr != nil {
		return ProviderRoute{}, resolveErr
	}
	if ok {
		return ProviderRoute{Name: item.Provider, Instance: item.ProviderInstance}, nil
	}
	err = fmt.Errorf("model %q is not available from provider %q", model, provider)
	return ProviderRoute{}, app.withModelReadiness(ctx, provider, model, err)
}

func modelRouteReady(item types.ModelInfo) bool {
	return item.Readiness.Ready && item.Readiness.RoutingReady
}

func (app *Application) withModelReadiness(ctx context.Context, provider, model string, err error) error {
	if err == nil || app == nil || app.service == nil {
		return err
	}
	result, readinessErr := app.service.ProviderModelReadiness(ctx, provider, model)
	if readinessErr != nil {
		return err
	}
	return ReadinessError{Cause: err, Readiness: result.Readiness.ToModelReadiness()}
}

func modelWithResolvedCapabilities(item types.ModelInfo) types.ModelInfo {
	item.Capabilities = modelcaps.ResolveWithProviderCapability(item.ProviderFamily, item.Kind, item.ID, item.DiscoverySource, item.Capabilities)
	item.ProviderAliases = append([]string(nil), item.ProviderAliases...)
	item.Readiness.SuggestedModels = append([]string(nil), item.Readiness.SuggestedModels...)
	return item
}

func resolveProviderModel(models []types.ModelInfo, providerIdentities []catalog.ProviderIdentity, provider, model string) (types.ModelInfo, bool, error) {
	resolution := catalog.ResolveProviderIdentity(providerIdentities, provider)
	if resolution.Ambiguous {
		return types.ModelInfo{}, false, ProviderAmbiguityError{Provider: provider}
	}
	if !resolution.Found {
		return types.ModelInfo{}, false, nil
	}
	canonicalProvider := providerIdentities[resolution.Index].Name
	for _, item := range models {
		if item.Provider == canonicalProvider && strings.EqualFold(item.ID, model) {
			return item, true, nil
		}
	}
	return types.ModelInfo{}, false, nil
}

func normalizeProvider(provider string) string {
	provider = strings.TrimSpace(provider)
	if strings.EqualFold(provider, "auto") {
		return ""
	}
	return provider
}
