package controlplane

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hecatehq/hecate/internal/config"
)

// AutoImportEnvProviders mirrors each env-PRECONFIGURED provider config into
// the control-plane store as a Provider row, so the admin UI shows a single
// source of truth instead of a hidden env-only branch. CP store edits win on
// conflict: if a row already exists for the same id we leave it alone and let
// the operator's admin-panel changes stand.
//
// Called once at startup from cmd/hecate/main.go after the provider runtime
// reload. Logs each upsert at info level and tolerates per-row failures
// (logs at warn) so a single bad config can't keep the gateway from booting.
func AutoImportEnvProviders(ctx context.Context, logger *slog.Logger, store Store, configs []config.OpenAICompatibleProviderConfig) error {
	if store == nil || len(configs) == 0 {
		return nil
	}
	state, err := store.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("snapshot control plane: %w", err)
	}
	existing := make(map[string]struct{}, len(state.Providers))
	for _, p := range state.Providers {
		if id := strings.TrimSpace(p.ID); id != "" {
			existing[id] = struct{}{}
		}
		if name := strings.TrimSpace(p.Name); name != "" {
			existing[name] = struct{}{}
		}
	}
	importCtx := WithActor(ctx, "system:env-import")
	for _, c := range configs {
		id := strings.TrimSpace(c.Name)
		if id == "" {
			continue
		}
		if _, ok := existing[id]; ok {
			continue
		}
		provider := Provider{
			ID:           id,
			Name:         id,
			Kind:         c.Kind,
			Protocol:     c.Protocol,
			BaseURL:      c.BaseURL,
			APIVersion:   c.APIVersion,
			DefaultModel: c.DefaultModel,
			Enabled:      true,
		}
		if _, ok := config.BuiltInProviderByID(id); ok {
			provider.PresetID = id
		}
		if _, err := store.UpsertProvider(importCtx, provider, nil); err != nil {
			logger.Warn("auto-import provider failed",
				slog.String("provider", id),
				slog.Any("error", err),
			)
			continue
		}
		logger.Info("auto-imported env-preconfigured provider into control plane", slog.String("provider", id))
	}
	return nil
}
