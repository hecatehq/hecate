package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/providerapp"
)

func (h *Handler) providerApplication() *providerapp.Application {
	if h == nil {
		return providerapp.New(providerapp.Options{})
	}
	return providerapp.New(providerapp.Options{
		ControlPlane: h.controlPlane,
		Runtime:      h.providerRuntime,
	})
}

func (h *Handler) HandleSettingsStatus(w http.ResponseWriter, r *http.Request) {
	payload := SettingsResponse{
		Object: "settings",
		Data: SettingsResponseItem{
			Backend:     "env",
			Providers:   []SettingsProviderRecord{},
			PolicyRules: []SettingsPolicyRuleRecord{},
			Events:      []SettingsAuditEventRecord{},
		},
	}

	if h.controlPlane == nil {
		WriteJSON(w, http.StatusOK, payload)
		return
	}

	state, err := h.controlPlane.Snapshot(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	payload.Data.Backend = h.controlPlane.Backend()
	for _, record := range buildSettingsProviderList(h.config, state) {
		payload.Data.Providers = append(payload.Data.Providers, record)
	}
	for _, rule := range state.PolicyRules {
		payload.Data.PolicyRules = append(payload.Data.PolicyRules, renderSettingsPolicyRule(rule))
	}
	for _, event := range state.Events {
		payload.Data.Events = append(payload.Data.Events, renderSettingsAuditEvent(event))
	}

	WriteJSON(w, http.StatusOK, payload)
}

func (h *Handler) HandleSettingsUpdateProvider(w http.ResponseWriter, r *http.Request) {
	if !h.requireSettings(w, r) {
		return
	}
	if h.providerRuntime == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "dynamic provider runtime is not configured")
		return
	}

	id := r.PathValue("id")
	var req struct {
		BaseURL    *string `json:"base_url,omitempty"`
		Name       *string `json:"name,omitempty"`
		CustomName *string `json:"custom_name,omitempty"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.BaseURL == nil && req.Name == nil && req.CustomName == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "no fields to update (expected base_url, name, or custom_name)")
		return
	}

	result, err := h.providerApplication().UpdateProvider(controlplane.WithActor(r.Context(), settingsActor(r)), providerapp.UpdateProviderCommand{
		ID:         id,
		BaseURL:    req.BaseURL,
		Name:       req.Name,
		CustomName: req.CustomName,
	})
	if err != nil {
		writeProviderAppError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "settings_provider",
		"data":   renderSettingsProvider(result.Provider, result.State.ProviderSecrets),
	})
}

// HandleSettingsSetProviderAPIKey is the single endpoint for managing a provider's
// API key. PUT with a non-empty `key` sets/updates it; PUT with an empty `key` clears it.
func (h *Handler) HandleSettingsSetProviderAPIKey(w http.ResponseWriter, r *http.Request) {
	if !h.requireSettings(w, r) {
		return
	}
	if h.providerRuntime == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "dynamic provider runtime is not configured")
		return
	}

	id := r.PathValue("id")
	var req struct {
		Key string `json:"key"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	result, cleared, err := h.providerApplication().SetAPIKey(controlplane.WithActor(r.Context(), settingsActor(r)), providerapp.SetAPIKeyCommand{
		ID:  id,
		Key: req.Key,
	})
	if err != nil {
		writeProviderAppError(w, err)
		return
	}
	if cleared != nil {
		WriteJSON(w, http.StatusOK, map[string]any{
			"object": "settings_provider_api_key",
			"data":   map[string]string{"id": cleared.ID, "status": cleared.Status},
		})
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "settings_provider_api_key",
		"data":   renderSettingsProvider(result.Provider, result.State.ProviderSecrets),
	})
}

func (h *Handler) HandleSettingsCreateProvider(w http.ResponseWriter, r *http.Request) {
	if !h.requireSettings(w, r) {
		return
	}
	if h.providerRuntime == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "dynamic provider runtime is not configured")
		return
	}

	var req struct {
		Name       string `json:"name"`
		PresetID   string `json:"preset_id"`
		CustomName string `json:"custom_name"`
		BaseURL    string `json:"base_url"`
		APIKey     string `json:"api_key"`
		Kind       string `json:"kind"`
		Protocol   string `json:"protocol"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	result, err := h.providerApplication().CreateProvider(controlplane.WithActor(r.Context(), settingsActor(r)), providerapp.CreateProviderCommand{
		Name:       req.Name,
		PresetID:   req.PresetID,
		CustomName: req.CustomName,
		BaseURL:    req.BaseURL,
		APIKey:     req.APIKey,
		Kind:       req.Kind,
		Protocol:   req.Protocol,
	})
	if err != nil {
		writeProviderAppError(w, err)
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{
		"object": "settings_provider",
		"data":   renderSettingsProvider(result.Provider, result.State.ProviderSecrets),
	})
}

func (h *Handler) HandleSettingsDeleteProvider(w http.ResponseWriter, r *http.Request) {
	if !h.requireSettings(w, r) {
		return
	}
	if h.providerRuntime == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "dynamic provider runtime is not configured")
		return
	}
	id := r.PathValue("id")
	if err := h.providerApplication().DeleteProvider(controlplane.WithActor(r.Context(), settingsActor(r)), id); err != nil {
		writeProviderAppError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"object": "settings_provider", "id": id, "deleted": true})
}

func (h *Handler) HandleSettingsUpsertPolicyRule(w http.ResponseWriter, r *http.Request) {
	if !h.requireSettings(w, r) {
		return
	}

	var req SettingsPolicyRuleUpsertRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	rule, err := h.controlPlane.UpsertPolicyRule(controlplane.WithActor(r.Context(), settingsActor(r)), config.PolicyRuleConfig{
		ID:                     req.ID,
		Action:                 req.Action,
		Reason:                 req.Reason,
		Providers:              req.Providers,
		ProviderKinds:          req.ProviderKinds,
		Models:                 req.Models,
		RouteReasons:           req.RouteReasons,
		MinPromptTokens:        req.MinPromptTokens,
		MinEstimatedCostMicros: req.MinEstimatedCostMicros,
		RewriteModelTo:         req.RewriteModelTo,
	})
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "settings_policy_rule",
		"data":   renderSettingsPolicyRule(rule),
	})
}

func (h *Handler) HandleSettingsDeletePolicyRule(w http.ResponseWriter, r *http.Request) {
	if !h.requireSettings(w, r) {
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "policy rule id is required")
		return
	}
	if err := h.controlPlane.DeletePolicyRule(controlplane.WithActor(r.Context(), settingsActor(r)), id); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "settings_policy_rule_deleted",
		"data": map[string]string{
			"id": id,
		},
	})
}

func renderSettingsPolicyRule(rule config.PolicyRuleConfig) SettingsPolicyRuleRecord {
	return SettingsPolicyRuleRecord{
		ID:                     rule.ID,
		Action:                 rule.Action,
		Reason:                 rule.Reason,
		Providers:              rule.Providers,
		ProviderKinds:          rule.ProviderKinds,
		Models:                 rule.Models,
		RouteReasons:           rule.RouteReasons,
		MinPromptTokens:        rule.MinPromptTokens,
		MinEstimatedCostMicros: rule.MinEstimatedCostMicros,
		RewriteModelTo:         rule.RewriteModelTo,
	}
}

func renderSettingsAuditEvent(event controlplane.AuditEvent) SettingsAuditEventRecord {
	record := SettingsAuditEventRecord{
		Actor:      event.Actor,
		Action:     event.Action,
		TargetType: event.TargetType,
		TargetID:   event.TargetID,
		Detail:     event.Detail,
	}
	if !event.Timestamp.IsZero() {
		record.Timestamp = event.Timestamp.UTC().Format(time.RFC3339)
	}
	return record
}

// buildSettingsProviderList returns one record per provider in the
// settings store. Providers are explicit: the operator adds them via
// POST /hecate/v1/settings/providers, picking from the preset catalog or
// supplying a custom OpenAI-compatible endpoint. The list starts empty and
// stays empty until the operator adds at least one. The preset catalog is
// served separately at GET /hecate/v1/providers/presets.
func buildSettingsProviderList(cfg config.Config, state controlplane.State) []SettingsProviderRecord {
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

	records := make([]SettingsProviderRecord, 0, len(state.Providers))
	for _, cp := range state.Providers {
		preset, hasPreset := presetByID[cp.ID]
		record := SettingsProviderRecord{
			ID:           cp.ID,
			Name:         cp.Name,
			CustomName:   cp.CustomName,
			Kind:         cp.Kind,
			Protocol:     cp.Protocol,
			BaseURL:      cp.BaseURL,
			DefaultModel: cp.DefaultModel,
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

func renderSettingsProvider(provider controlplane.Provider, secrets []controlplane.ProviderSecret) SettingsProviderRecord {
	inheritedFields := settingsInheritedFields(provider)
	record := SettingsProviderRecord{
		ID:              provider.ID,
		Name:            provider.Name,
		PresetID:        provider.PresetID,
		CustomName:      provider.CustomName,
		Kind:            provider.Kind,
		Protocol:        provider.Protocol,
		BaseURL:         provider.BaseURL,
		APIVersion:      provider.APIVersion,
		DefaultModel:    provider.DefaultModel,
		ExplicitFields:  append([]string(nil), provider.ExplicitFields...),
		InheritedFields: inheritedFields,
	}
	for _, secret := range secrets {
		if secret.ProviderID == provider.ID {
			record.CredentialConfigured = secret.APIKeyEncrypted != ""
			record.CredentialSource = "vault"
			break
		}
	}
	return record
}

func settingsInheritedFields(provider controlplane.Provider) []string {
	builtIn, ok := config.BuiltInProviderByID(firstNonEmpty(provider.PresetID, provider.Name, provider.ID))
	if !ok {
		return nil
	}

	explicit := make(map[string]struct{}, len(provider.ExplicitFields))
	for _, field := range provider.ExplicitFields {
		explicit[field] = struct{}{}
	}

	var inherited []string
	maybeAppend := func(field string, condition bool) {
		if !condition {
			return
		}
		if _, ok := explicit[field]; ok {
			return
		}
		inherited = append(inherited, field)
	}

	maybeAppend("kind", provider.Kind == builtIn.Kind)
	maybeAppend("protocol", provider.Protocol == builtIn.Protocol)
	maybeAppend("base_url", provider.BaseURL == builtIn.BaseURL)
	maybeAppend("api_version", provider.APIVersion == builtIn.APIVersion)
	maybeAppend("default_model", provider.DefaultModel == builtIn.DefaultModel)
	return inherited
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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
