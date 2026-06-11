package api

import (
	"context"

	"github.com/hecatehq/hecate/internal/modelapp"
	"github.com/hecatehq/hecate/pkg/types"
)

func (h *Handler) modelApplication() *modelapp.Application {
	if h == nil {
		return modelapp.New(modelapp.Options{})
	}
	return modelapp.New(modelapp.Options{Service: h.service})
}

func (h *Handler) resolveModelCapabilities(ctx context.Context, provider, model string) (types.ModelCapabilities, error) {
	return h.modelApplication().ResolveCapabilities(ctx, provider, model)
}
