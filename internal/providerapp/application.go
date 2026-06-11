package providerapp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hecatehq/hecate/internal/apperrors"
	"github.com/hecatehq/hecate/internal/config"
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
	Backend() string
	Snapshot(ctx context.Context) (controlplane.State, error)
	UpsertPolicyRule(ctx context.Context, rule config.PolicyRuleConfig) (config.PolicyRuleConfig, error)
	DeletePolicyRule(ctx context.Context, id string) error
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
	config       config.Config
}

type Options struct {
	ControlPlane ControlPlane
	Runtime      Runtime
	Config       config.Config
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

type PolicyRuleCommand struct {
	ID                     string
	Action                 string
	Reason                 string
	Providers              []string
	ProviderKinds          []string
	Models                 []string
	RouteReasons           []string
	MinPromptTokens        int
	MinEstimatedCostMicros int64
	RewriteModelTo         string
}

type ProviderResult struct {
	Provider controlplane.Provider
	State    controlplane.State
}

type StatusResult struct {
	Backend     string
	Providers   []ProviderRecord
	PolicyRules []config.PolicyRuleConfig
	Events      []controlplane.AuditEvent
}

type ProviderRecord struct {
	ID                   string
	Name                 string
	PresetID             string
	CustomName           string
	Kind                 string
	Protocol             string
	BaseURL              string
	APIVersion           string
	DefaultModel         string
	ExplicitFields       []string
	InheritedFields      []string
	CredentialConfigured bool
	CredentialSource     string
}

type ClearAPIKeyResult struct {
	ID     string
	Status string
}

func New(opts Options) *Application {
	return &Application{controlPlane: opts.ControlPlane, runtime: opts.Runtime, config: opts.Config}
}

func (app *Application) Status(ctx context.Context) (*StatusResult, error) {
	if app == nil || app.controlPlane == nil {
		return &StatusResult{Backend: "env"}, nil
	}
	state, err := app.controlPlane.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return &StatusResult{
		Backend:     app.controlPlane.Backend(),
		Providers:   buildProviderRecords(app.config, state),
		PolicyRules: append([]config.PolicyRuleConfig(nil), state.PolicyRules...),
		Events:      append([]controlplane.AuditEvent(nil), state.Events...),
	}, nil
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

func (app *Application) UpsertPolicyRule(ctx context.Context, cmd PolicyRuleCommand) (config.PolicyRuleConfig, error) {
	if app == nil || app.controlPlane == nil {
		return config.PolicyRuleConfig{}, ErrControlPlaneNotConfigured
	}
	rule, err := app.controlPlane.UpsertPolicyRule(ctx, config.PolicyRuleConfig{
		ID:                     cmd.ID,
		Action:                 cmd.Action,
		Reason:                 cmd.Reason,
		Providers:              append([]string(nil), cmd.Providers...),
		ProviderKinds:          append([]string(nil), cmd.ProviderKinds...),
		Models:                 append([]string(nil), cmd.Models...),
		RouteReasons:           append([]string(nil), cmd.RouteReasons...),
		MinPromptTokens:        cmd.MinPromptTokens,
		MinEstimatedCostMicros: cmd.MinEstimatedCostMicros,
		RewriteModelTo:         cmd.RewriteModelTo,
	})
	if err != nil {
		return config.PolicyRuleConfig{}, Validation(err)
	}
	return rule, nil
}

func (app *Application) DeletePolicyRule(ctx context.Context, id string) (string, error) {
	if app == nil || app.controlPlane == nil {
		return "", ErrControlPlaneNotConfigured
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return "", Validation(errors.New("policy rule id is required"))
	}
	if err := app.controlPlane.DeletePolicyRule(ctx, id); err != nil {
		return "", Validation(err)
	}
	return id, nil
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

func buildProviderRecords(cfg config.Config, state controlplane.State) []ProviderRecord {
	envKeyByID := make(map[string]bool)
	for _, pc := range cfg.Providers.OpenAICompatible {
		if pc.APIKey != "" {
			envKeyByID[pc.Name] = true
		}
	}

	presetByID := make(map[string]config.BuiltInProvider)
	for _, b := range config.BuiltInProviders() {
		presetByID[b.ID] = b
	}

	records := make([]ProviderRecord, 0, len(state.Providers))
	for _, cp := range state.Providers {
		preset, hasPreset := presetByID[cp.ID]
		record := ProviderRecord{
			ID:             cp.ID,
			Name:           cp.Name,
			CustomName:     cp.CustomName,
			Kind:           cp.Kind,
			Protocol:       cp.Protocol,
			BaseURL:        cp.BaseURL,
			DefaultModel:   cp.DefaultModel,
			ExplicitFields: append([]string(nil), cp.ExplicitFields...),
		}
		if record.Name == "" {
			record.Name = cp.ID
		}
		if hasPreset {
			record.PresetID = preset.ID
			if record.Kind == "" {
				record.Kind = preset.Kind
			}
			if record.Protocol == "" {
				record.Protocol = preset.Protocol
			}
			if record.BaseURL == "" {
				record.BaseURL = preset.BaseURL
			}
			if record.APIVersion == "" {
				record.APIVersion = preset.APIVersion
			}
			if record.DefaultModel == "" {
				record.DefaultModel = preset.DefaultModel
			}
		}
		for _, secret := range state.ProviderSecrets {
			if secret.ProviderID == cp.ID {
				record.CredentialConfigured = secret.APIKeyEncrypted != ""
				record.CredentialSource = "vault"
				break
			}
		}
		if !record.CredentialConfigured && envKeyByID[cp.ID] {
			record.CredentialConfigured = true
			record.CredentialSource = "env"
		}
		records = append(records, record)
	}

	return records
}

func slugify(name string) string {
	s := strings.ToLower(name)
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}
