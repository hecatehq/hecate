package api

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/controlplane"
)

// slugify converts a human name into a stable URL-safe ID.
// "My Anthropic" → "my-anthropic", leading/trailing hyphens stripped.
func slugify(name string) string {
	s := strings.ToLower(name)
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

func (h *Handler) HandleControlPlaneStatus(w http.ResponseWriter, r *http.Request) {
	payload := ControlPlaneResponse{
		Object: "control_plane",
		Data: ControlPlaneResponseItem{
			Backend:     "env",
			Providers:   []ControlPlaneProviderRecord{},
			PolicyRules: []ControlPlanePolicyRuleRecord{},
			Pricebook:   []ControlPlanePricebookRecord{},
			Events:      []ControlPlaneAuditEventRecord{},
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
	for _, record := range buildControlPlaneProviderList(h.config, state) {
		payload.Data.Providers = append(payload.Data.Providers, record)
	}
	for _, rule := range state.PolicyRules {
		payload.Data.PolicyRules = append(payload.Data.PolicyRules, renderControlPlanePolicyRule(rule))
	}
	for _, entry := range state.Pricebook {
		payload.Data.Pricebook = append(payload.Data.Pricebook, renderControlPlanePricebookEntry(entry))
	}
	for _, event := range state.Events {
		payload.Data.Events = append(payload.Data.Events, renderControlPlaneAuditEvent(event))
	}

	WriteJSON(w, http.StatusOK, payload)
}

func (h *Handler) HandleControlPlaneUpdateProvider(w http.ResponseWriter, r *http.Request) {
	if !h.requireControlPlane(w, r) {
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

	ctx := controlplane.WithActor(r.Context(), controlPlaneActor(r))

	state, err := h.controlPlane.Snapshot(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	var existing *controlplane.Provider
	for i := range state.Providers {
		if state.Providers[i].ID == id {
			existing = &state.Providers[i]
			break
		}
	}
	if existing == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, fmt.Sprintf("provider %q not found", id))
		return
	}
	updated := *existing
	if req.BaseURL != nil {
		trimmed := strings.TrimSpace(*req.BaseURL)
		if trimmed == "" {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "base_url cannot be empty")
			return
		}
		updated.BaseURL = trimmed
	}
	if req.Name != nil {
		// Preset providers keep a fixed Name (it's the catalog join key).
		// CustomName is the disambiguator they should reach for instead.
		if existing.PresetID != "" {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "preset providers have a fixed name; use custom_name to add a disambiguating label")
			return
		}
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "name cannot be empty")
			return
		}
		updated.Name = trimmed
	}
	if req.CustomName != nil {
		// CustomName is allowed on any provider, including presets, and
		// can be cleared by passing an empty string.
		updated.CustomName = strings.TrimSpace(*req.CustomName)
	}
	provider, err := h.providerRuntime.Upsert(ctx, updated, "")
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	state, _ = h.controlPlane.Snapshot(r.Context())
	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "control_plane_provider",
		"data":   renderControlPlaneProvider(provider, state.ProviderSecrets),
	})
}

// HandleControlPlaneSetProviderAPIKey is the single endpoint for managing a provider's
// API key. PUT with a non-empty `key` sets/updates it; PUT with an empty `key` clears it.
func (h *Handler) HandleControlPlaneSetProviderAPIKey(w http.ResponseWriter, r *http.Request) {
	if !h.requireControlPlane(w, r) {
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

	ctx := controlplane.WithActor(r.Context(), controlPlaneActor(r))
	if req.Key == "" {
		if err := h.providerRuntime.DeleteCredential(ctx, id); err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{
			"object": "control_plane_provider_api_key",
			"data":   map[string]string{"id": id, "status": "cleared"},
		})
		return
	}

	provider, err := h.providerRuntime.RotateSecret(ctx, id, req.Key)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	state, _ := h.controlPlane.Snapshot(r.Context())
	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "control_plane_provider_api_key",
		"data":   renderControlPlaneProvider(provider, state.ProviderSecrets),
	})
}

func (h *Handler) HandleControlPlaneCreateProvider(w http.ResponseWriter, r *http.Request) {
	if !h.requireControlPlane(w, r) {
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

	// ID is derived from name + custom_name so two instances of the same
	// preset (Name="Anthropic" twice with different CustomName values)
	// land at distinct ids without forcing the operator to type a unique
	// Name. CustomName is empty for the typical single-instance case;
	// the slug then degenerates to slugify(Name) and matches pre-custom_name
	// records.
	idSource := strings.TrimSpace(req.Name)
	if cn := strings.TrimSpace(req.CustomName); cn != "" {
		idSource = idSource + " " + cn
	}
	id := slugify(idSource)
	if id == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "provider name is required")
		return
	}

	state, err := h.controlPlane.Snapshot(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	for _, p := range state.Providers {
		if p.ID == id {
			WriteError(w, http.StatusConflict, errCodeInvalidRequest, fmt.Sprintf("provider with id %q already exists", id))
			return
		}
	}

	if strings.TrimSpace(req.BaseURL) != "" {
		for _, p := range state.Providers {
			existingURL := strings.TrimSpace(p.BaseURL)
			if existingURL == "" {
				continue
			}
			if existingURL == strings.TrimSpace(req.BaseURL) {
				name := p.Name
				if name == "" {
					name = p.ID
				}
				WriteError(w, http.StatusConflict, errCodeInvalidRequest, fmt.Sprintf("base URL already used by provider %q", name))
				return
			}
		}
	}

	kind := req.Kind
	if kind == "" {
		kind = "cloud"
	}
	protocol := req.Protocol
	if protocol == "" {
		protocol = "openai"
	}

	ctx := controlplane.WithActor(r.Context(), controlPlaneActor(r))
	provider, err := h.providerRuntime.Upsert(ctx, controlplane.Provider{
		ID:         id,
		Name:       req.Name,
		PresetID:   req.PresetID,
		CustomName: strings.TrimSpace(req.CustomName),
		Kind:       kind,
		Protocol:   protocol,
		BaseURL:    req.BaseURL,
		Enabled:    true,
	}, req.APIKey)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}

	state, _ = h.controlPlane.Snapshot(r.Context())
	WriteJSON(w, http.StatusCreated, map[string]any{
		"object": "control_plane_provider",
		"data":   renderControlPlaneProvider(provider, state.ProviderSecrets),
	})
}

func (h *Handler) HandleControlPlaneDeleteProvider(w http.ResponseWriter, r *http.Request) {
	if !h.requireControlPlane(w, r) {
		return
	}
	if h.providerRuntime == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "dynamic provider runtime is not configured")
		return
	}
	id := r.PathValue("id")
	ctx := controlplane.WithActor(r.Context(), controlPlaneActor(r))
	if err := h.providerRuntime.Delete(ctx, id); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"object": "control_plane_provider", "id": id, "deleted": true})
}

func (h *Handler) HandleControlPlaneUpsertPolicyRule(w http.ResponseWriter, r *http.Request) {
	if !h.requireControlPlane(w, r) {
		return
	}

	var req ControlPlanePolicyRuleUpsertRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	rule, err := h.controlPlane.UpsertPolicyRule(controlplane.WithActor(r.Context(), controlPlaneActor(r)), config.PolicyRuleConfig{
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
		"object": "control_plane_policy_rule",
		"data":   renderControlPlanePolicyRule(rule),
	})
}

func (h *Handler) HandleControlPlaneDeletePolicyRule(w http.ResponseWriter, r *http.Request) {
	if !h.requireControlPlane(w, r) {
		return
	}

	var req ControlPlanePolicyRuleLifecycleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := h.controlPlane.DeletePolicyRule(controlplane.WithActor(r.Context(), controlPlaneActor(r)), req.ID); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "control_plane_policy_rule_deleted",
		"data": map[string]string{
			"id": req.ID,
		},
	})
}

func (h *Handler) HandleControlPlaneUpsertPricebookEntry(w http.ResponseWriter, r *http.Request) {
	if !h.requireControlPlane(w, r) {
		return
	}

	var req ControlPlanePricebookUpsertRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	entry, err := h.controlPlane.UpsertPricebookEntry(controlplane.WithActor(r.Context(), controlPlaneActor(r)), config.ModelPriceConfig{
		Provider:                             req.Provider,
		Model:                                req.Model,
		InputMicrosUSDPerMillionTokens:       req.InputMicrosUSDPerMillionTokens,
		OutputMicrosUSDPerMillionTokens:      req.OutputMicrosUSDPerMillionTokens,
		CachedInputMicrosUSDPerMillionTokens: req.CachedInputMicrosUSDPerMillionTokens,
		Source:                               req.Source,
	})
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "control_plane_pricebook_entry",
		"data":   renderControlPlanePricebookEntry(entry),
	})
}

func (h *Handler) HandleControlPlaneDeletePricebookEntry(w http.ResponseWriter, r *http.Request) {
	if !h.requireControlPlane(w, r) {
		return
	}

	var req ControlPlanePricebookLifecycleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := h.controlPlane.DeletePricebookEntry(controlplane.WithActor(r.Context(), controlPlaneActor(r)), req.Provider, req.Model); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "control_plane_pricebook_entry_deleted",
		"data": map[string]string{
			"provider": req.Provider,
			"model":    req.Model,
		},
	})
}

func renderControlPlanePolicyRule(rule config.PolicyRuleConfig) ControlPlanePolicyRuleRecord {
	return ControlPlanePolicyRuleRecord{
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

func renderControlPlanePricebookEntry(entry config.ModelPriceConfig) ControlPlanePricebookRecord {
	return ControlPlanePricebookRecord{
		Provider:                             entry.Provider,
		Model:                                entry.Model,
		InputMicrosUSDPerMillionTokens:       entry.InputMicrosUSDPerMillionTokens,
		OutputMicrosUSDPerMillionTokens:      entry.OutputMicrosUSDPerMillionTokens,
		CachedInputMicrosUSDPerMillionTokens: entry.CachedInputMicrosUSDPerMillionTokens,
		Source:                               entry.Source,
	}
}

func renderControlPlaneAuditEvent(event controlplane.AuditEvent) ControlPlaneAuditEventRecord {
	record := ControlPlaneAuditEventRecord{
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

// buildControlPlaneProviderList returns one record per provider in the
// control-plane store. Providers are explicit: the operator adds them via
// POST /admin/control-plane/providers, picking from the preset catalog or
// supplying a custom OpenAI-compatible endpoint. The list starts empty and
// stays empty until the operator adds at least one. The preset catalog is
// served separately at GET /v1/provider-presets.
func buildControlPlaneProviderList(cfg config.Config, state controlplane.State) []ControlPlaneProviderRecord {
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

	records := make([]ControlPlaneProviderRecord, 0, len(state.Providers))
	for _, cp := range state.Providers {
		preset, hasPreset := presetByID[cp.ID]
		record := ControlPlaneProviderRecord{
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

func renderControlPlaneProvider(provider controlplane.Provider, secrets []controlplane.ProviderSecret) ControlPlaneProviderRecord {
	inheritedFields := controlPlaneInheritedFields(provider)
	record := ControlPlaneProviderRecord{
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

func controlPlaneInheritedFields(provider controlplane.Provider) []string {
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
