package modelapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/catalog"
	"github.com/hecatehq/hecate/internal/gateway"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/internal/modelprobe"
	"github.com/hecatehq/hecate/pkg/types"
)

var (
	ErrServiceNotConfigured  = errors.New("model service is not configured")
	ErrProviderAmbiguous     = errors.New("provider identity is ambiguous")
	ErrToolProbeUnavailable  = errors.New("model tool capability probing is unavailable")
	ErrToolProbeNotNeeded    = errors.New("model tool capability is already known")
	ErrToolProbeRouteChanged = errors.New("model tool capability probe route changed")
)

type Service interface {
	ListModels(ctx context.Context) (*gateway.ModelsResult, error)
	RefreshModels(ctx context.Context) (*gateway.ModelsResult, error)
	ProviderModelReadiness(ctx context.Context, provider, model string) (*gateway.ProviderModelReadinessResult, error)
}

type ToolProbeService interface {
	ProbeToolCalling(ctx context.Context, input gateway.ToolCallingProbeRequest) (gateway.ToolCallingProbeResult, error)
}

type Application struct {
	service              Service
	toolProbeStore       modelprobe.Store
	toolProbeCoordinator *modelprobe.Coordinator
}

type Options struct {
	Service              Service
	ToolProbeStore       modelprobe.Store
	ToolProbeCoordinator *modelprobe.Coordinator
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

// ToolProbeResult is the operator-facing result of an explicit model tool
// verification. Provider instance identity is intentionally absent: it is
// persisted only as an internal generation fence.
type ToolProbeResult struct {
	Provider     string
	Model        string
	Capabilities types.ModelCapabilities
	Verification *types.ToolCapabilityVerification
	TraceID      string
	Performed    bool
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
	store := opts.ToolProbeStore
	if store == nil {
		store = modelprobe.NewMemoryStore()
	}
	coordinator := opts.ToolProbeCoordinator
	if coordinator == nil {
		coordinator = modelprobe.NewCoordinator(store)
	}
	return &Application{
		service:              opts.Service,
		toolProbeStore:       store,
		toolProbeCoordinator: coordinator,
	}
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
	probeRecords := app.toolProbeRecords(ctx, result.Models)
	now := time.Now().UTC()
	out := make([]types.ModelInfo, 0, len(result.Models))
	for _, item := range result.Models {
		out = append(out, app.modelWithResolvedCapabilities(item, probeRecords, now))
	}
	providerIdentities := make([]catalog.ProviderIdentity, 0, len(result.ProviderIdentities))
	for _, identity := range result.ProviderIdentities {
		identity.Aliases = append([]string(nil), identity.Aliases...)
		providerIdentities = append(providerIdentities, identity)
	}
	return modelCatalogSnapshot{models: out, providerIdentities: providerIdentities}, nil
}

// VerifyToolCalling sends one harmless, forced tool-call diagnostic to the
// exact configured provider/model route. It never executes the returned tool.
// Only otherwise-unknown capability values can be changed by the proof;
// provider-native and catalog-known values remain authoritative.
func (app *Application) VerifyToolCalling(ctx context.Context, provider, model string) (ToolProbeResult, error) {
	provider = normalizeProvider(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return ToolProbeResult{}, fmt.Errorf("provider and model are required")
	}
	if app == nil || app.service == nil || app.toolProbeCoordinator == nil {
		return ToolProbeResult{}, ErrToolProbeUnavailable
	}
	snapshot, err := app.loadModelCatalog(ctx, ListModelsCommand{})
	if err != nil {
		return ToolProbeResult{}, err
	}
	item, ok, resolveErr := resolveProviderModel(snapshot.models, snapshot.providerIdentities, provider, model)
	if resolveErr != nil {
		return ToolProbeResult{}, resolveErr
	}
	if !ok {
		err := fmt.Errorf("model %q is not available from provider %q", model, provider)
		return ToolProbeResult{}, app.withModelReadiness(ctx, provider, model, err)
	}
	if !modelRouteReady(item) {
		err := fmt.Errorf("model %q is not routable from provider %q", item.ID, item.Provider)
		return ToolProbeResult{}, app.withModelReadiness(ctx, item.Provider, item.ID, err)
	}
	if !item.ProviderInstance.Valid() {
		return ToolProbeResult{}, ErrToolProbeUnavailable
	}
	if item.Capabilities.ToolCalling != modelcaps.ToolCallingUnknown {
		return ToolProbeResult{
			Provider:     item.Provider,
			Model:        item.ID,
			Capabilities: item.Capabilities,
			Verification: item.Capabilities.ToolVerification,
		}, ErrToolProbeNotNeeded
	}
	service, ok := app.service.(ToolProbeService)
	if !ok {
		return ToolProbeResult{}, ErrToolProbeUnavailable
	}

	key := modelprobe.Key{
		Provider: item.Provider,
		Model:    item.ID,
		Instance: item.ProviderInstance,
		Version:  modelprobe.ProbeVersion,
	}
	traceID := ""
	record, performed, err := app.toolProbeCoordinator.Verify(ctx, key, func(probeCtx context.Context) modelprobe.Outcome {
		result, probeErr := service.ProbeToolCalling(probeCtx, gateway.ToolCallingProbeRequest{
			Provider:         item.Provider,
			Model:            item.ID,
			ProviderInstance: item.ProviderInstance,
		})
		traceID = result.TraceID
		if probeErr != nil {
			reason := result.Reason
			if strings.TrimSpace(reason) == "" {
				reason = toolProbeFailureReason(probeErr)
			}
			return modelprobe.Outcome{
				Status: modelprobe.StatusInconclusive,
				Reason: reason,
			}
		}
		// A provider reload after dispatch must not turn a stale proof into a
		// valid proof for the replacement generation. The old row remains
		// unreachable because the opaque identity is part of its key.
		current, routeErr := app.ResolveProviderRoute(probeCtx, item.Provider, item.ID)
		if routeErr != nil || current.Name != item.Provider || current.Instance != item.ProviderInstance {
			return modelprobe.Outcome{Status: modelprobe.StatusInconclusive, Reason: modelprobe.ReasonProviderChanged}
		}
		return modelprobe.Outcome{Status: result.Status, Reason: result.Reason}
	})
	if err != nil {
		return ToolProbeResult{}, err
	}

	// Read through the normal projection path so /v1/models and Hecate Chat
	// both observe the same effective capability after a completed probe.
	refreshed, err := app.loadModelCatalog(ctx, ListModelsCommand{})
	if err != nil {
		return ToolProbeResult{}, err
	}
	updated, found, resolveErr := resolveProviderModel(refreshed.models, refreshed.providerIdentities, item.Provider, item.ID)
	if resolveErr != nil {
		return ToolProbeResult{}, resolveErr
	}
	if !found {
		return ToolProbeResult{}, fmt.Errorf("model tool capability probe route is no longer configured")
	}
	if updated.Provider != item.Provider || updated.ProviderInstance != item.ProviderInstance {
		// The completed record is bound to the old opaque provider generation
		// and cannot project onto the replacement. Do not return that stale
		// observation as if it described the newly configured route.
		return ToolProbeResult{}, ErrToolProbeRouteChanged
	}
	return ToolProbeResult{
		Provider:     updated.Provider,
		Model:        updated.ID,
		Capabilities: updated.Capabilities,
		Verification: record.Public(),
		TraceID:      traceID,
		Performed:    performed,
	}, nil
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

func (app *Application) modelWithResolvedCapabilities(item types.ModelInfo, probeRecords map[modelprobe.Key]modelprobe.Record, now time.Time) types.ModelInfo {
	item.Capabilities = modelcaps.ResolveWithProviderCapability(item.ProviderFamily, item.Kind, item.ID, item.DiscoverySource, item.Capabilities)
	item.Capabilities = applyToolVerification(item.Provider, item.ID, item.ProviderInstance, item.Capabilities, probeRecords, now)
	item.ProviderAliases = append([]string(nil), item.ProviderAliases...)
	item.Readiness.SuggestedModels = append([]string(nil), item.Readiness.SuggestedModels...)
	return item
}

// toolProbeRecords reads current generation-bound observations for the whole
// catalog in one bounded batch when the configured store supports it. The
// fallback preserves the Store contract for narrow test doubles, while the
// production memory and SQL stores avoid a query per model on hot list and
// chat-admission paths.
func (app *Application) toolProbeRecords(ctx context.Context, models []types.ModelInfo) map[modelprobe.Key]modelprobe.Record {
	if app == nil || app.toolProbeStore == nil {
		return nil
	}
	keys := make([]modelprobe.Key, 0, len(models))
	for _, item := range models {
		if !item.ProviderInstance.Valid() {
			continue
		}
		keys = append(keys, modelprobe.Key{
			Provider: item.Provider,
			Model:    item.ID,
			Instance: item.ProviderInstance,
			Version:  modelprobe.ProbeVersion,
		})
	}
	if len(keys) == 0 {
		return nil
	}
	if batch, ok := app.toolProbeStore.(modelprobe.BatchStore); ok {
		records, err := batch.GetMany(ctx, keys)
		if err == nil {
			return records
		}
		return nil
	}
	records := make(map[modelprobe.Key]modelprobe.Record, len(keys))
	for _, key := range keys {
		record, found, err := app.toolProbeStore.Get(ctx, key)
		if err != nil || !found {
			continue
		}
		records[key] = record
	}
	return records
}

func applyToolVerification(
	provider, model string,
	instance types.ProviderInstanceIdentity,
	capabilities types.ModelCapabilities,
	probeRecords map[modelprobe.Key]modelprobe.Record,
	now time.Time,
) types.ModelCapabilities {
	// Provider/catalog metadata never supplies this Hecate-owned observation.
	// Clear any copied internal metadata before considering the generation-bound
	// store row for this exact model route.
	capabilities.ToolVerification = nil
	capabilities.ToolCallingVerificationApplied = false
	if !instance.Valid() || len(probeRecords) == 0 {
		return capabilities
	}
	record, found := probeRecords[modelprobe.Key{
		Provider: provider,
		Model:    model,
		Instance: instance,
		Version:  modelprobe.ProbeVersion,
	}]
	if !found || !record.Active(now) {
		return capabilities
	}
	capabilities.ToolVerification = record.Public()
	if capabilities.ToolCalling != modelcaps.ToolCallingUnknown {
		return capabilities
	}
	switch record.Status {
	case modelprobe.StatusSupported:
		capabilities.ToolCalling = modelcaps.ToolCallingBasic
		// The effective tool value now combines provider/catalog discovery with
		// Hecate-owned manual evidence. Keep provenance honest rather than
		// presenting the projected value as provider-native metadata.
		capabilities.Source = modelcaps.SourceMixed
		capabilities.ToolCallingVerificationApplied = true
	case modelprobe.StatusUnsupported:
		capabilities.ToolCalling = modelcaps.ToolCallingNone
		capabilities.Source = modelcaps.SourceMixed
		capabilities.ToolCallingVerificationApplied = true
	}
	return capabilities
}

func toolProbeFailureReason(err error) string {
	switch {
	case errors.Is(err, gateway.ErrToolProbeModelRewritten):
		return modelprobe.ReasonPolicyDenied
	case errors.Is(err, gateway.ErrToolProbeUnavailable):
		return modelprobe.ReasonConfiguration
	case errors.Is(err, gateway.ErrToolProbeInvalid):
		return modelprobe.ReasonConfiguration
	default:
		return modelprobe.ReasonProviderFailure
	}
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
