package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/hecate/agent-runtime/internal/modelcaps"
	"github.com/hecate/agent-runtime/pkg/types"
)

func (h *Handler) resolveModelCapabilities(ctx context.Context, provider, model string) (types.ModelCapabilities, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return types.ModelCapabilities{}, fmt.Errorf("model is required")
	}
	state, err := h.settingsState(ctx)
	if err != nil {
		return types.ModelCapabilities{}, err
	}
	if h.service == nil {
		return modelcaps.Resolve(provider, "", model, "", state), nil
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
		return modelcaps.Resolve(item.Provider, item.Kind, item.ID, item.DiscoverySource, state), nil
	}
	if provider == "" {
		return types.ModelCapabilities{}, fmt.Errorf("model %q is not available from any configured provider", model)
	}
	return types.ModelCapabilities{}, fmt.Errorf("model %q is not available from provider %q", model, provider)
}
