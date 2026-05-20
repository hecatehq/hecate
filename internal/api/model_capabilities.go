package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/hecate/agent-runtime/internal/gateway"
	"github.com/hecate/agent-runtime/internal/modelcaps"
	"github.com/hecate/agent-runtime/pkg/types"
)

type modelReadinessError struct {
	err       error
	readiness gateway.ProviderModelReadiness
}

func (e modelReadinessError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e modelReadinessError) Unwrap() error {
	return e.err
}

func (h *Handler) resolveModelCapabilities(ctx context.Context, provider, model string) (types.ModelCapabilities, error) {
	provider = strings.TrimSpace(provider)
	if strings.EqualFold(provider, "auto") {
		provider = ""
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return types.ModelCapabilities{}, fmt.Errorf("model is required")
	}
	if h.service == nil {
		return modelcaps.Resolve(provider, "", model, ""), nil
	}
	result, err := h.service.ListModels(ctx)
	if err != nil {
		return types.ModelCapabilities{}, err
	}
	for _, item := range result.Models {
		if provider != "" && !strings.EqualFold(item.Provider, provider) {
			continue
		}
		if !strings.EqualFold(item.ID, model) {
			continue
		}
		return modelcaps.ResolveWithProviderCapability(item.Provider, item.Kind, item.ID, item.DiscoverySource, item.Capabilities), nil
	}
	if provider == "" {
		err := fmt.Errorf("model %q is not available from any configured provider", model)
		return types.ModelCapabilities{}, h.withModelReadiness(ctx, provider, model, err)
	}
	err = fmt.Errorf("model %q is not available from provider %q", model, provider)
	return types.ModelCapabilities{}, h.withModelReadiness(ctx, provider, model, err)
}

func (h *Handler) withModelReadiness(ctx context.Context, provider, model string, err error) error {
	if err == nil || h.service == nil {
		return err
	}
	result, readinessErr := h.service.ProviderModelReadiness(ctx, provider, model)
	if readinessErr != nil {
		return err
	}
	return modelReadinessError{err: err, readiness: result.Readiness}
}
