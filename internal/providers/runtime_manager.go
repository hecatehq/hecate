package providers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/secrets"
)

type ControlPlaneRuntimeManager struct {
	logger                 *slog.Logger
	baseConfigs            []config.OpenAICompatibleProviderConfig
	store                  controlplane.Store
	cipher                 secrets.Cipher
	registry               *MutableRegistry
	anthropicCacheDisabled bool
}

func NewControlPlaneRuntimeManager(logger *slog.Logger, baseConfigs []config.OpenAICompatibleProviderConfig, store controlplane.Store, cipher secrets.Cipher) *ControlPlaneRuntimeManager {
	items := buildProviders(baseConfigs, logger)
	return &ControlPlaneRuntimeManager{
		logger:      logger,
		baseConfigs: append([]config.OpenAICompatibleProviderConfig(nil), baseConfigs...),
		store:       store,
		cipher:      cipher,
		registry:    NewMutableRegistry(items...),
	}
}

// SetGlobalAnthropicCacheDisabled records the gateway-wide
// HECATE_PROVIDER_ANTHROPIC_CACHE_ENABLED toggle (inverted). Once set,
// every Anthropic-protocol provider that enters the registry through
// Reload — whether it came from env, the control-plane UI, or any
// future on-demand registration path — has its cache flag stamped to
// match. Call once at gateway boot, after construction, before the
// first Reload. Tests that don't care about the toggle can leave the
// default (false → caching enabled) untouched.
func (m *ControlPlaneRuntimeManager) SetGlobalAnthropicCacheDisabled(disabled bool) {
	m.anthropicCacheDisabled = disabled
}

func (m *ControlPlaneRuntimeManager) Registry() Registry {
	return m.registry
}

func (m *ControlPlaneRuntimeManager) SecretStorageEnabled() bool {
	return m.cipher != nil
}

func (m *ControlPlaneRuntimeManager) Reload(ctx context.Context) error {
	configs, err := m.resolvedConfigs(ctx)
	if err != nil {
		return err
	}
	m.registry.Replace(buildProviders(configs, m.logger)...)
	return nil
}

func (m *ControlPlaneRuntimeManager) Upsert(ctx context.Context, provider controlplane.Provider, apiKey string) (controlplane.Provider, error) {
	state, err := m.snapshot(ctx)
	if err != nil {
		return controlplane.Provider{}, err
	}
	if existing := findControlPlaneProvider(state.Providers, provider.ID, provider.Name); existing != nil {
		provider = mergeProviderWithExisting(provider, *existing)
	}
	provider = hydrateControlPlaneProviderDefaults(provider)

	var encryptedSecret *controlplane.ProviderSecret
	if strings.TrimSpace(apiKey) != "" {
		if m.cipher == nil {
			return controlplane.Provider{}, fmt.Errorf("control plane secret storage is not configured")
		}
		encrypted, err := m.cipher.Encrypt(apiKey)
		if err != nil {
			return controlplane.Provider{}, fmt.Errorf("encrypt provider secret: %w", err)
		}
		encryptedSecret = &controlplane.ProviderSecret{
			ProviderID:      provider.ID,
			APIKeyEncrypted: encrypted,
			APIKeyPreview:   previewSecret(apiKey),
		}
	}

	if provider.Kind == "" {
		provider.Kind = string(KindCloud)
	}
	if provider.Protocol == "" {
		provider.Protocol = "openai"
	}
	if provider.Kind == string(KindCloud) && encryptedSecret == nil {
		existing := findControlPlaneProvider(state.Providers, provider.ID, provider.Name)
		if existing == nil || !providerHasSecret(state, existing.ID) {
			return controlplane.Provider{}, fmt.Errorf("cloud providers require an api key")
		}
	}

	saved, err := m.store.UpsertProvider(ctx, provider, encryptedSecret)
	if err != nil {
		return controlplane.Provider{}, err
	}
	if err := m.Reload(ctx); err != nil {
		return controlplane.Provider{}, err
	}
	return saved, nil
}

func (m *ControlPlaneRuntimeManager) RotateSecret(ctx context.Context, id, apiKey string) (controlplane.Provider, error) {
	if m.cipher == nil {
		return controlplane.Provider{}, fmt.Errorf("control plane secret storage is not configured")
	}
	if strings.TrimSpace(apiKey) == "" {
		return controlplane.Provider{}, fmt.Errorf("provider api key is required")
	}
	encrypted, err := m.cipher.Encrypt(apiKey)
	if err != nil {
		return controlplane.Provider{}, fmt.Errorf("encrypt provider secret: %w", err)
	}
	saved, err := m.store.RotateProviderSecret(ctx, id, controlplane.ProviderSecret{
		ProviderID:      id,
		APIKeyEncrypted: encrypted,
		APIKeyPreview:   previewSecret(apiKey),
	})
	if err != nil {
		return controlplane.Provider{}, err
	}
	if err := m.Reload(ctx); err != nil {
		return controlplane.Provider{}, err
	}
	return saved, nil
}

func (m *ControlPlaneRuntimeManager) DeleteCredential(ctx context.Context, id string) error {
	if _, err := m.store.DeleteProviderCredential(ctx, id); err != nil {
		return err
	}
	return m.Reload(ctx)
}

func (m *ControlPlaneRuntimeManager) Delete(ctx context.Context, id string) error {
	if err := m.store.DeleteProvider(ctx, id); err != nil {
		return err
	}
	return m.Reload(ctx)
}

func (m *ControlPlaneRuntimeManager) resolvedConfigs(ctx context.Context) ([]config.OpenAICompatibleProviderConfig, error) {
	configs := append([]config.OpenAICompatibleProviderConfig(nil), m.baseConfigs...)
	if m.store == nil {
		return configs, nil
	}

	state, err := m.store.Snapshot(ctx)
	if err != nil {
		return nil, err
	}

	byName := make(map[string]config.OpenAICompatibleProviderConfig, len(configs))
	order := make([]string, 0, len(configs)+len(state.Providers))
	for _, cfg := range configs {
		cfg.Enabled = true
		byName[cfg.Name] = cfg
		order = append(order, cfg.Name)
	}

	for _, item := range state.Providers {
		if !item.Enabled {
			// Mark matching env-configured provider as disabled rather than removing it,
			// so it stays in the registry and reports status "disabled" in health checks.
			name := item.Name
			if name == "" {
				name = item.ID
			}
			if existing, ok := byName[name]; ok {
				existing.Enabled = false
				byName[name] = existing
			} else if name != "" {
				// CP-only entry (no base config): add a disabled placeholder.
				order = append(order, name)
				byName[name] = config.OpenAICompatibleProviderConfig{Name: name, Enabled: false}
			}
			continue
		}
		item = hydrateControlPlaneProviderDefaults(item)
		if strings.TrimSpace(item.Name) == "" {
			m.logger.Warn("skipping control-plane provider with empty name", slog.String("provider_id", item.ID))
			continue
		}
		apiKey := ""
		if item.CredentialID != "" {
			if m.cipher == nil {
				m.logger.Warn("skipping control-plane provider without secret storage configured", slog.String("provider", item.Name))
				continue
			}
			secret := controlPlaneProviderSecretByProviderID(state.ProviderSecrets, item.ID)
			if secret == nil {
				m.logger.Warn("skipping control-plane provider with missing secret", slog.String("provider", item.Name))
				continue
			}
			decrypted, err := m.cipher.Decrypt(secret.APIKeyEncrypted)
			if err != nil {
				m.logger.Warn("skipping control-plane provider with undecryptable secret", slog.String("provider", item.Name), slog.Any("error", err))
				continue
			}
			apiKey = decrypted
		}
		cfg := config.OpenAICompatibleProviderConfig{
			Name:         item.Name,
			Kind:         item.Kind,
			Protocol:     item.Protocol,
			BaseURL:      item.BaseURL,
			APIKey:       apiKey,
			APIVersion:   item.APIVersion,
			DefaultModel: item.DefaultModel,
			Timeout:      config.DefaultProviderTimeout(item.Kind),
			Enabled:      true,
		}
		// Apply the gateway-wide Anthropic cache toggle to any
		// provider whose protocol respects it. The flag is global by
		// design (the AnthropicProvider's caching behavior is the
		// same conceptual knob whether the operator added the
		// provider via env or via the Providers tab); inheriting it
		// only via name-match left CP-only Anthropic providers stuck
		// at the default. SetGlobalAnthropicCacheDisabled is the
		// single source of truth.
		if entry, _ := lookupProtocolDispatch(cfg.Protocol); entry.SupportsAnthropicCache {
			cfg.AnthropicCacheDisabled = m.anthropicCacheDisabled
		}
		if builtIn, ok := builtInForControlPlaneProvider(item); ok {
			cfg.ChatPath = builtIn.ChatPath
			cfg.ModelsPath = builtIn.ModelsPath
		}
		if _, ok := byName[cfg.Name]; !ok {
			order = append(order, cfg.Name)
		}
		byName[cfg.Name] = cfg
	}

	out := make([]config.OpenAICompatibleProviderConfig, 0, len(order))
	seen := make(map[string]struct{}, len(order))
	for _, name := range order {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		cfg := byName[name]
		if cfg.Timeout == 0 {
			cfg.Timeout = config.DefaultProviderTimeout(cfg.Kind)
		}
		out = append(out, cfg)
	}
	return out, nil
}

func hydrateControlPlaneProviderDefaults(provider controlplane.Provider) controlplane.Provider {
	// Order: PresetID first (an explicit instance like "Anthropic Prod"
	// with id="anthropic-prod" must still hydrate from the "anthropic"
	// preset), then id-as-preset (legacy single-instance creates), then
	// name (catches old data with empty PresetID).
	for _, candidate := range []string{provider.PresetID, provider.ID, provider.Name} {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		builtIn, ok := config.BuiltInProviderByID(candidate)
		if !ok {
			continue
		}
		if strings.TrimSpace(provider.PresetID) == "" {
			provider.PresetID = builtIn.ID
		}
		provider.ExplicitFields = normalizeFieldNames(provider.ExplicitFields)
		// Force Name = builtIn.ID for preset-based providers. Name is
		// the catalog join key — it's what shows up as
		// `metadata.provider` on /v1/models — and it must match the
		// canonical lowercase preset id so the UI's provider picker
		// (which uses cp.id, also lowercase) finds models for it.
		// Operators who added a preset via the UI form previously had
		// Name set to the preset's display name ("Ollama"), which
		// case-mismatched cp.id ("ollama") and left the model picker
		// empty. The display label still resolves through preset
		// lookup on the UI side, so clobbering Name is safe.
		// Custom (non-preset) providers don't reach this branch and
		// keep their operator-typed Name.
		provider.Name = builtIn.ID
		if strings.TrimSpace(provider.Kind) == "" {
			provider.Kind = builtIn.Kind
		}
		if strings.TrimSpace(provider.Protocol) == "" {
			provider.Protocol = builtIn.Protocol
		}
		if strings.TrimSpace(provider.BaseURL) == "" {
			provider.BaseURL = builtIn.BaseURL
		}
		if strings.TrimSpace(provider.APIVersion) == "" {
			provider.APIVersion = builtIn.APIVersion
		}
		if strings.TrimSpace(provider.DefaultModel) == "" {
			provider.DefaultModel = builtIn.DefaultModel
		}
		return provider
	}
	return provider
}

func builtInForControlPlaneProvider(provider controlplane.Provider) (config.BuiltInProvider, bool) {
	for _, candidate := range []string{provider.PresetID, provider.ID, provider.Name} {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if builtIn, ok := config.BuiltInProviderByID(candidate); ok {
			return builtIn, true
		}
	}
	return config.BuiltInProvider{}, false
}

func normalizeFieldNames(fields []string) []string {
	out := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	return out
}

func mergeProviderWithExisting(next, existing controlplane.Provider) controlplane.Provider {
	explicit := make(map[string]struct{}, len(next.ExplicitFields))
	for _, field := range next.ExplicitFields {
		explicit[field] = struct{}{}
	}

	if strings.TrimSpace(next.Name) == "" {
		next.Name = existing.Name
	}
	if strings.TrimSpace(next.PresetID) == "" {
		next.PresetID = existing.PresetID
	}
	if _, ok := explicit["kind"]; !ok && strings.TrimSpace(next.Kind) == "" {
		next.Kind = existing.Kind
	}
	if _, ok := explicit["protocol"]; !ok && strings.TrimSpace(next.Protocol) == "" {
		next.Protocol = existing.Protocol
	}
	if _, ok := explicit["base_url"]; !ok && strings.TrimSpace(next.BaseURL) == "" {
		next.BaseURL = existing.BaseURL
	}
	if _, ok := explicit["api_version"]; !ok && strings.TrimSpace(next.APIVersion) == "" {
		next.APIVersion = existing.APIVersion
	}
	if _, ok := explicit["default_model"]; !ok && strings.TrimSpace(next.DefaultModel) == "" {
		next.DefaultModel = existing.DefaultModel
	}
	next.ExplicitFields = normalizeFieldNames(append(append([]string(nil), existing.ExplicitFields...), next.ExplicitFields...))
	return next
}

func (m *ControlPlaneRuntimeManager) snapshot(ctx context.Context) (controlplane.State, error) {
	if m.store == nil {
		return controlplane.State{}, nil
	}
	return m.store.Snapshot(ctx)
}

func buildProviders(configs []config.OpenAICompatibleProviderConfig, logger *slog.Logger) []Provider {
	items := make([]Provider, 0, len(configs))
	for _, providerCfg := range configs {
		if strings.TrimSpace(providerCfg.Name) == "" {
			logger.Warn("skipping provider with empty name")
			continue
		}
		entry, _ := lookupProtocolDispatch(providerCfg.Protocol)
		items = append(items, entry.Constructor(providerCfg, logger))
	}
	return items
}

func findControlPlaneProvider(items []controlplane.Provider, id, name string) *controlplane.Provider {
	id = strings.TrimSpace(id)
	name = strings.TrimSpace(name)
	for i := range items {
		if id != "" && items[i].ID == id {
			return &items[i]
		}
		if name != "" && items[i].Name == name {
			return &items[i]
		}
	}
	return nil
}

func providerHasSecret(state controlplane.State, id string) bool {
	for _, secret := range state.ProviderSecrets {
		if secret.ProviderID == id && secret.APIKeyEncrypted != "" {
			return true
		}
	}
	return false
}

func controlPlaneProviderSecretByProviderID(items []controlplane.ProviderSecret, providerID string) *controlplane.ProviderSecret {
	for i := range items {
		if items[i].ProviderID == providerID {
			return &items[i]
		}
	}
	return nil
}

func previewSecret(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) <= 2 {
		return secret
	}
	if len(secret) <= 8 {
		return secret[:2] + "..." + secret[len(secret)-2:]
	}
	return secret[:4] + "..." + secret[len(secret)-4:]
}
