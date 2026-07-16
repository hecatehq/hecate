package api

import (
	"context"

	"github.com/hecatehq/hecate/internal/gateway"
	"github.com/hecatehq/hecate/pkg/types"
)

// directModelRouteSnapshot is the durable execution identity for one direct
// model turn. Requested Auto capabilities are an aggregate; once the gateway
// has selected a route, transcript and session snapshots must describe that
// actual provider/model/generation instead of whichever catalog row happened
// to sort first.
type directModelRouteSnapshot struct {
	Provider         string
	ProviderInstance types.ProviderInstanceIdentity
	Model            string
	Capabilities     types.ModelCapabilities
}

func (h *Handler) directModelResultRouteSnapshot(
	ctx context.Context,
	requestedProvider string,
	requestedProviderInstance types.ProviderInstanceIdentity,
	requestedModel string,
	requestedCapabilities types.ModelCapabilities,
	result *gateway.ChatResult,
) directModelRouteSnapshot {
	snapshot := directModelRouteSnapshot{
		Provider:         requestedProvider,
		ProviderInstance: requestedProviderInstance,
		Model:            requestedModel,
		Capabilities:     requestedCapabilities,
	}
	if result == nil {
		return snapshot
	}

	if result.Response != nil {
		if result.Response.Route.Provider != "" {
			snapshot.Provider = result.Response.Route.Provider
		}
		if result.Response.Route.ProviderInstance.Valid() {
			snapshot.ProviderInstance = result.Response.Route.ProviderInstance
		}
		if result.Response.Route.Model != "" {
			snapshot.Model = result.Response.Route.Model
		}
	}
	failedBeforeResponse := result.Response == nil
	if result.Metadata.Provider != "" && (failedBeforeResponse || snapshot.Provider == "") {
		snapshot.Provider = result.Metadata.Provider
	}
	if result.Metadata.ProviderInstance.Valid() && (failedBeforeResponse || !snapshot.ProviderInstance.Valid()) {
		snapshot.ProviderInstance = result.Metadata.ProviderInstance
	}
	if result.Metadata.Model != "" && (failedBeforeResponse || snapshot.Model == "") {
		snapshot.Model = result.Metadata.Model
	}

	capabilities, err := h.resolveModelCapabilities(ctx, snapshot.Provider, snapshot.Model)
	if err == nil {
		snapshot.Capabilities = capabilities
	} else if h != nil && h.logger != nil {
		// A successful route remains the execution source of truth even if a
		// concurrent catalog refresh makes capability re-resolution unavailable.
		// Retain the pre-route aggregate rather than failing after provider spend.
		h.logger.WarnContext(ctx, "chat.direct_model.route_capabilities_unavailable",
			"provider", snapshot.Provider,
			"model", snapshot.Model,
			"error", err,
		)
	}
	return snapshot
}
