package pluginregistryapp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/hecatehq/hecate/internal/pluginregistry"
)

type Store interface {
	Backend() string
	List(ctx context.Context) ([]pluginregistry.Plugin, error)
	Get(ctx context.Context, id string) (pluginregistry.Plugin, bool, error)
	Upsert(ctx context.Context, plugin pluginregistry.Plugin) (pluginregistry.Plugin, error)
	Update(ctx context.Context, id string, update func(*pluginregistry.Plugin)) (pluginregistry.Plugin, error)
	Clear(ctx context.Context) (int, error)
}

type Options struct {
	Store Store
}

type Application struct {
	store Store
}

func New(options Options) *Application {
	return &Application{store: options.Store}
}

type InstallLocalCommand struct {
	Manifest  json.RawMessage
	SourceRef string
}

type UpdateCommand struct {
	Enabled      *bool
	Capabilities map[string]CapabilityUpdate
}

type CapabilityUpdate struct {
	Enabled *bool
}

func (app *Application) List(ctx context.Context) ([]pluginregistry.Plugin, error) {
	if app == nil || app.store == nil {
		return nil, nil
	}
	return app.store.List(ctx)
}

func (app *Application) Get(ctx context.Context, id string) (pluginregistry.Plugin, bool, error) {
	if app == nil || app.store == nil {
		return pluginregistry.Plugin{}, false, nil
	}
	return app.store.Get(ctx, id)
}

func (app *Application) InstallLocal(ctx context.Context, cmd InstallLocalCommand) (pluginregistry.Plugin, error) {
	if app == nil || app.store == nil {
		return pluginregistry.Plugin{}, pluginregistry.ErrInvalid
	}
	plugin, err := pluginregistry.PluginFromManifest(cmd.Manifest, pluginregistry.SourceLocalPath, strings.TrimSpace(cmd.SourceRef))
	if err != nil {
		return pluginregistry.Plugin{}, err
	}
	return app.store.Upsert(ctx, plugin)
}

func (app *Application) Update(ctx context.Context, id string, cmd UpdateCommand) (pluginregistry.Plugin, error) {
	if app == nil || app.store == nil {
		return pluginregistry.Plugin{}, pluginregistry.ErrInvalid
	}
	if len(cmd.Capabilities) > 0 {
		plugin, ok, err := app.store.Get(ctx, id)
		if err != nil {
			return pluginregistry.Plugin{}, err
		}
		if !ok {
			return pluginregistry.Plugin{}, pluginregistry.ErrNotFound
		}
		known := make(map[string]bool, len(plugin.Capabilities))
		for _, capability := range plugin.Capabilities {
			known[capability.ID] = true
		}
		for capabilityID := range cmd.Capabilities {
			if !known[capabilityID] {
				return pluginregistry.Plugin{}, pluginregistry.ErrInvalid
			}
		}
	}
	return app.store.Update(ctx, id, func(plugin *pluginregistry.Plugin) {
		if cmd.Enabled != nil {
			plugin.Enabled = *cmd.Enabled
		}
		if len(cmd.Capabilities) > 0 {
			for idx := range plugin.Capabilities {
				if patch, ok := cmd.Capabilities[plugin.Capabilities[idx].ID]; ok && patch.Enabled != nil {
					plugin.Capabilities[idx].Enabled = *patch.Enabled
				}
			}
		}
	})
}

func (app *Application) Health(ctx context.Context, id string) (pluginregistry.Health, bool, error) {
	if app == nil || app.store == nil {
		return pluginregistry.Health{}, false, nil
	}
	plugin, ok, err := app.store.Get(ctx, id)
	if err != nil || !ok {
		return pluginregistry.Health{}, ok, err
	}
	all, err := app.store.List(ctx)
	if err != nil {
		return pluginregistry.Health{}, false, err
	}
	return pluginregistry.HealthFor(plugin, all), true, nil
}
