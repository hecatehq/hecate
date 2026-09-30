package api

import (
	"context"

	"github.com/hecatehq/hecate/internal/browserapp"
	"github.com/hecatehq/hecate/internal/browserrunner"
	"github.com/hecatehq/hecate/internal/config"
)

// The local runtime keeps a stable delegate so setup takes effect without
// rebuilding active executors. Remote composition never receives that delegate.
func browserRuntimeFromConfig(cfg config.Config, hostID string) (*browserapp.Service, browserrunner.Inspector, browserrunner.FlowRunner) {
	service := browserapp.New(browserapp.Options{
		RuntimeHostID:      hostID,
		Remote:             cfg.Server.RemoteRuntimeMode,
		ExecutableOverride: cfg.Server.TaskBrowserExecutable,
		Timeout:            cfg.Server.TaskBrowserTimeout,
		AllowPrivateIPs:    cfg.Server.TaskBrowserAllowPrivateIPs,
	})
	if cfg.Server.RemoteRuntimeMode {
		return service, nil, nil
	}
	return service, service, service
}

func (h *Handler) browserReadiness(ctx context.Context) BrowserEvidenceRuntimeReadinessResponse {
	readiness := h.browserRuntime.Readiness(ctx)
	return BrowserEvidenceRuntimeReadinessResponse{
		Available:      readiness.Available,
		Status:         readiness.Status,
		Message:        readiness.Message,
		OperatorAction: readiness.OperatorAction,
	}
}

// SetBrowserSettingsStore is startup wiring, before any requests or task work.
func (h *Handler) SetBrowserSettingsStore(store browserapp.Store) {
	if h != nil && h.browserRuntime != nil && store != nil {
		h.browserRuntime.SetStore(store)
	}
}
