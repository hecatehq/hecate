package providerapp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hecatehq/hecate/internal/apperrors"
	"github.com/hecatehq/hecate/internal/controlplane"
)

var (
	ErrControlPlaneNotConfigured = errors.New("control plane is not configured")
	ErrRuntimeNotConfigured      = errors.New("dynamic provider runtime is not configured")
)

type ValidationError = apperrors.ValidationError

func Validation(err error) error {
	return apperrors.Validation(err)
}

func IsValidationError(err error) bool {
	return apperrors.IsValidationError(err)
}

type ConflictError = apperrors.ConflictError

func Conflict(err error) error {
	return apperrors.Conflict(err)
}

func IsConflictError(err error) bool {
	return apperrors.IsConflictError(err)
}

type ControlPlane interface {
	Snapshot(ctx context.Context) (controlplane.State, error)
}

type Runtime interface {
	Upsert(ctx context.Context, provider controlplane.Provider, apiKey string) (controlplane.Provider, error)
	RotateSecret(ctx context.Context, id, apiKey string) (controlplane.Provider, error)
	DeleteCredential(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
}

type Application struct {
	controlPlane ControlPlane
	runtime      Runtime
}

type Options struct {
	ControlPlane ControlPlane
	Runtime      Runtime
}

type UpdateProviderCommand struct {
	ID         string
	BaseURL    *string
	Name       *string
	CustomName *string
}

type CreateProviderCommand struct {
	Name       string
	PresetID   string
	CustomName string
	BaseURL    string
	APIKey     string
	Kind       string
	Protocol   string
}

type SetAPIKeyCommand struct {
	ID  string
	Key string
}

type ProviderResult struct {
	Provider controlplane.Provider
	State    controlplane.State
}

type ClearAPIKeyResult struct {
	ID     string
	Status string
}

func New(opts Options) *Application {
	return &Application{controlPlane: opts.ControlPlane, runtime: opts.Runtime}
}

func (app *Application) UpdateProvider(ctx context.Context, cmd UpdateProviderCommand) (*ProviderResult, error) {
	if app == nil || app.controlPlane == nil {
		return nil, ErrControlPlaneNotConfigured
	}
	if app.runtime == nil {
		return nil, ErrRuntimeNotConfigured
	}
	if cmd.BaseURL == nil && cmd.Name == nil && cmd.CustomName == nil {
		return nil, Validation(errors.New("no fields to update (expected base_url, name, or custom_name)"))
	}
	state, err := app.controlPlane.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	existing := findProvider(state, cmd.ID)
	if existing == nil {
		return nil, Validation(fmt.Errorf("provider %q not found", cmd.ID))
	}
	updated := *existing
	if cmd.BaseURL != nil {
		trimmed := strings.TrimSpace(*cmd.BaseURL)
		if trimmed == "" {
			return nil, Validation(errors.New("base_url cannot be empty"))
		}
		updated.BaseURL = trimmed
	}
	if cmd.Name != nil {
		if existing.PresetID != "" {
			return nil, Validation(errors.New("preset providers have a fixed name; use custom_name to add a disambiguating label"))
		}
		trimmed := strings.TrimSpace(*cmd.Name)
		if trimmed == "" {
			return nil, Validation(errors.New("name cannot be empty"))
		}
		updated.Name = trimmed
	}
	if cmd.CustomName != nil {
		updated.CustomName = strings.TrimSpace(*cmd.CustomName)
	}
	provider, err := app.runtime.Upsert(ctx, updated, "")
	if err != nil {
		return nil, Validation(err)
	}
	state, _ = app.controlPlane.Snapshot(ctx)
	return &ProviderResult{Provider: provider, State: state}, nil
}

func (app *Application) SetAPIKey(ctx context.Context, cmd SetAPIKeyCommand) (*ProviderResult, *ClearAPIKeyResult, error) {
	if app == nil || app.controlPlane == nil {
		return nil, nil, ErrControlPlaneNotConfigured
	}
	if app.runtime == nil {
		return nil, nil, ErrRuntimeNotConfigured
	}
	id := strings.TrimSpace(cmd.ID)
	if cmd.Key == "" {
		if err := app.runtime.DeleteCredential(ctx, id); err != nil {
			return nil, nil, Validation(err)
		}
		return nil, &ClearAPIKeyResult{ID: id, Status: "cleared"}, nil
	}
	provider, err := app.runtime.RotateSecret(ctx, id, cmd.Key)
	if err != nil {
		return nil, nil, Validation(err)
	}
	state, _ := app.controlPlane.Snapshot(ctx)
	return &ProviderResult{Provider: provider, State: state}, nil, nil
}

func (app *Application) CreateProvider(ctx context.Context, cmd CreateProviderCommand) (*ProviderResult, error) {
	if app == nil || app.controlPlane == nil {
		return nil, ErrControlPlaneNotConfigured
	}
	if app.runtime == nil {
		return nil, ErrRuntimeNotConfigured
	}
	idSource := strings.TrimSpace(cmd.Name)
	if customName := strings.TrimSpace(cmd.CustomName); customName != "" {
		idSource = idSource + " " + customName
	}
	id := slugify(idSource)
	if id == "" {
		return nil, Validation(errors.New("provider name is required"))
	}

	state, err := app.controlPlane.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	for _, provider := range state.Providers {
		if provider.ID == id {
			return nil, Conflict(fmt.Errorf("provider with id %q already exists", id))
		}
	}
	baseURL := strings.TrimSpace(cmd.BaseURL)
	if baseURL != "" {
		for _, provider := range state.Providers {
			existingURL := strings.TrimSpace(provider.BaseURL)
			if existingURL == "" || existingURL != baseURL {
				continue
			}
			name := provider.Name
			if name == "" {
				name = provider.ID
			}
			return nil, Conflict(fmt.Errorf("base URL already used by provider %q", name))
		}
	}

	kind := cmd.Kind
	if kind == "" {
		kind = "cloud"
	}
	protocol := cmd.Protocol
	if protocol == "" {
		protocol = "openai"
	}
	provider, err := app.runtime.Upsert(ctx, controlplane.Provider{
		ID:         id,
		Name:       cmd.Name,
		PresetID:   cmd.PresetID,
		CustomName: strings.TrimSpace(cmd.CustomName),
		Kind:       kind,
		Protocol:   protocol,
		BaseURL:    cmd.BaseURL,
		Enabled:    true,
	}, cmd.APIKey)
	if err != nil {
		return nil, Validation(err)
	}
	state, _ = app.controlPlane.Snapshot(ctx)
	return &ProviderResult{Provider: provider, State: state}, nil
}

func (app *Application) DeleteProvider(ctx context.Context, id string) error {
	if app == nil || app.runtime == nil {
		return ErrRuntimeNotConfigured
	}
	if err := app.runtime.Delete(ctx, strings.TrimSpace(id)); err != nil {
		return Validation(err)
	}
	return nil
}

func findProvider(state controlplane.State, id string) *controlplane.Provider {
	id = strings.TrimSpace(id)
	for i := range state.Providers {
		if state.Providers[i].ID == id {
			return &state.Providers[i]
		}
	}
	return nil
}

func slugify(name string) string {
	s := strings.ToLower(name)
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}
