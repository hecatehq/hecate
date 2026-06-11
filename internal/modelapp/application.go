package modelapp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/gateway"
	"github.com/hecatehq/hecate/internal/modelcaps"
	"github.com/hecatehq/hecate/pkg/types"
)

var ErrServiceNotConfigured = errors.New("model service is not configured")

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

type ReadinessError struct {
	Cause     error
	Readiness types.ModelReadiness
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
	if app == nil || app.service == nil {
		return nil, ErrServiceNotConfigured
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
		return nil, err
	}
	out := make([]types.ModelInfo, 0, len(result.Models))
	for _, item := range result.Models {
		out = append(out, modelWithResolvedCapabilities(item))
	}
	return out, nil
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
	models, err := app.ListModels(ctx, ListModelsCommand{})
	if err != nil {
		return types.ModelCapabilities{}, err
	}
	for _, item := range models {
		if provider != "" && !strings.EqualFold(item.Provider, provider) {
			continue
		}
		if !strings.EqualFold(item.ID, model) {
			continue
		}
		return item.Capabilities, nil
	}
	if provider == "" {
		err := fmt.Errorf("model %q is not available from any configured provider", model)
		return types.ModelCapabilities{}, app.withModelReadiness(ctx, provider, model, err)
	}
	err = fmt.Errorf("model %q is not available from provider %q", model, provider)
	return types.ModelCapabilities{}, app.withModelReadiness(ctx, provider, model, err)
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
	item.Capabilities = modelcaps.ResolveWithProviderCapability(item.Provider, item.Kind, item.ID, item.DiscoverySource, item.Capabilities)
	item.Readiness.SuggestedModels = append([]string(nil), item.Readiness.SuggestedModels...)
	return item
}

func normalizeProvider(provider string) string {
	provider = strings.TrimSpace(provider)
	if strings.EqualFold(provider, "auto") {
		return ""
	}
	return provider
}
