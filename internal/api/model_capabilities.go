package api

import (
	"context"

	"github.com/hecatehq/hecate/pkg/types"
)

func (h *Handler) resolveModelCapabilities(ctx context.Context, provider, model string) (types.ModelCapabilities, error) {
	return h.modelApplication().ResolveCapabilities(ctx, provider, model)
}
