package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/pluginregistry"
	"github.com/hecatehq/hecate/internal/pluginregistryapp"
)

type installLocalPluginRequest struct {
	Manifest  json.RawMessage `json:"manifest"`
	SourceRef string          `json:"source_ref"`
}

type updatePluginRequest struct {
	Enabled      *bool                                    `json:"enabled"`
	Capabilities map[string]updatePluginCapabilityRequest `json:"capabilities"`
}

type updatePluginCapabilityRequest struct {
	Enabled *bool `json:"enabled"`
}

func (h *Handler) HandlePlugins(w http.ResponseWriter, r *http.Request) {
	app := h.pluginRegistryApplication()
	items, err := app.List(r.Context())
	if err != nil {
		writePluginRegistryError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, PluginsResponse{Object: "plugins", Data: renderPlugins(items)})
}

func (h *Handler) HandlePlugin(w http.ResponseWriter, r *http.Request) {
	app := h.pluginRegistryApplication()
	plugin, ok, err := app.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writePluginRegistryError(w, err)
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "plugin not found")
		return
	}
	WriteJSON(w, http.StatusOK, PluginResponse{Object: "plugin", Data: renderPlugin(plugin)})
}

func (h *Handler) HandleInstallLocalPlugin(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "request body must be valid JSON")
		return
	}
	cmd, ok := parseInstallLocalPluginRequest(w, body)
	if !ok {
		return
	}
	plugin, err := h.pluginRegistryApplication().InstallLocal(r.Context(), cmd)
	if err != nil {
		writePluginRegistryError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, PluginResponse{Object: "plugin", Data: renderPlugin(plugin)})
}

func (h *Handler) HandleUpdatePlugin(w http.ResponseWriter, r *http.Request) {
	var req updatePluginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	plugin, err := h.pluginRegistryApplication().Update(r.Context(), r.PathValue("id"), pluginRegistryUpdateCommand(req))
	if err != nil {
		writePluginRegistryError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, PluginResponse{Object: "plugin", Data: renderPlugin(plugin)})
}

func (h *Handler) HandlePluginHealth(w http.ResponseWriter, r *http.Request) {
	health, ok, err := h.pluginRegistryApplication().Health(r.Context(), r.PathValue("id"))
	if err != nil {
		writePluginRegistryError(w, err)
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "plugin not found")
		return
	}
	WriteJSON(w, http.StatusOK, PluginHealthResponse{Object: "plugin_health", Data: renderPluginHealth(health)})
}

func parseInstallLocalPluginRequest(w http.ResponseWriter, body []byte) (pluginregistryapp.InstallLocalCommand, bool) {
	var req installLocalPluginRequest
	if err := json.Unmarshal(body, &req); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "request body must be valid JSON")
		return pluginregistryapp.InstallLocalCommand{}, false
	}
	if len(req.Manifest) == 0 {
		var manifest pluginregistry.Manifest
		if err := json.Unmarshal(body, &manifest); err != nil || strings.TrimSpace(manifest.SchemaVersion) == "" {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "manifest is required")
			return pluginregistryapp.InstallLocalCommand{}, false
		}
		req.Manifest = append(json.RawMessage(nil), body...)
	}
	return pluginregistryapp.InstallLocalCommand{
		Manifest:  req.Manifest,
		SourceRef: req.SourceRef,
	}, true
}

func pluginRegistryUpdateCommand(req updatePluginRequest) pluginregistryapp.UpdateCommand {
	cmd := pluginregistryapp.UpdateCommand{Enabled: req.Enabled}
	if len(req.Capabilities) > 0 {
		cmd.Capabilities = make(map[string]pluginregistryapp.CapabilityUpdate, len(req.Capabilities))
		for id, patch := range req.Capabilities {
			cmd.Capabilities[id] = pluginregistryapp.CapabilityUpdate{Enabled: patch.Enabled}
		}
	}
	return cmd
}

func writePluginRegistryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pluginregistry.ErrNotFound):
		WriteError(w, http.StatusNotFound, errCodeNotFound, err.Error())
	case errors.Is(err, pluginregistry.ErrInvalid):
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
	case errors.Is(err, pluginregistry.ErrConflict):
		WriteError(w, http.StatusConflict, errCodeConflict, err.Error())
	default:
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
	}
}

func renderPlugins(items []pluginregistry.Plugin) []PluginResponseItem {
	out := make([]PluginResponseItem, 0, len(items))
	for _, item := range items {
		out = append(out, renderPlugin(item))
	}
	return out
}

func renderPlugin(item pluginregistry.Plugin) PluginResponseItem {
	return PluginResponseItem{
		ID:                    item.ID,
		Name:                  item.Name,
		Description:           item.Description,
		Version:               item.Version,
		SourceKind:            item.SourceKind,
		SourceRef:             item.SourceRef,
		ManifestSchemaVersion: item.ManifestSchemaVersion,
		ManifestDigest:        item.ManifestDigest,
		RequestedPermissions:  renderPluginPermissions(item.RequestedPermissions),
		RegistryState:         item.RegistryState,
		Enabled:               item.Enabled,
		Warnings:              item.Warnings,
		Capabilities:          renderPluginCapabilities(item.Capabilities),
		Auth:                  renderPluginAuth(item.Auth),
		InstalledAt:           formatPluginTime(item.InstalledAt),
		UpdatedAt:             formatPluginTime(item.UpdatedAt),
	}
}

func renderPluginCapabilities(items []pluginregistry.Capability) []PluginCapabilityRecord {
	out := make([]PluginCapabilityRecord, 0, len(items))
	for _, item := range items {
		out = append(out, PluginCapabilityRecord{
			ID:                   item.ID,
			Kind:                 item.Kind,
			DisplayName:          item.DisplayName,
			RequestedPermissions: renderPluginPermissions(item.RequestedPermissions),
			Enabled:              item.Enabled,
			MCPServer:            renderPluginMCPServer(item),
			Warnings:             item.Warnings,
		})
	}
	return out
}

func renderPluginMCPServer(item pluginregistry.Capability) *PluginMCPServerRecord {
	if item.Kind != pluginregistry.CapabilityMCPServer {
		return nil
	}
	cfg, err := pluginregistry.ParseMCPServerConfig(item.ID, item.ConfigJSON)
	if err != nil {
		return nil
	}
	return &PluginMCPServerRecord{
		Name:           cfg.Name,
		Transport:      cfg.Transport,
		Command:        cfg.Command,
		Args:           append([]string(nil), cfg.Args...),
		Env:            cloneStringMap(cfg.Env),
		URL:            cfg.URL,
		Headers:        cloneStringMap(cfg.Headers),
		ApprovalPolicy: cfg.ApprovalPolicy,
	}
}

func renderPluginPermissions(items []pluginregistry.Permission) []PluginPermissionRecord {
	out := make([]PluginPermissionRecord, 0, len(items))
	for _, item := range items {
		out = append(out, PluginPermissionRecord{Value: item.Value, Classification: item.Classification})
	}
	return out
}

func renderPluginAuth(items []pluginregistry.AuthBinding) []PluginAuthBindingRecord {
	out := make([]PluginAuthBindingRecord, 0, len(items))
	for _, item := range items {
		out = append(out, PluginAuthBindingRecord{
			CapabilityID:  item.CapabilityID,
			RequestedName: item.RequestedName,
			Kind:          item.Kind,
			Status:        item.Status,
			SecretRef:     item.SecretRef,
			Warnings:      item.Warnings,
		})
	}
	return out
}

func renderPluginHealth(item pluginregistry.Health) PluginHealthRecord {
	return PluginHealthRecord{
		PluginID:                 item.PluginID,
		RegistryState:            item.RegistryState,
		Warnings:                 item.Warnings,
		UnsupportedPermissions:   item.UnsupportedPermissions,
		UnresolvedSecretBindings: item.UnresolvedSecretBindings,
		DisabledCapabilities:     item.DisabledCapabilities,
		CommandCollisions:        renderPluginCommandCollisions(item.CommandCollisions),
	}
}

func renderPluginCommandCollisions(items []pluginregistry.CommandCollision) []PluginCommandCollisionRecord {
	out := make([]PluginCommandCollisionRecord, 0, len(items))
	for _, item := range items {
		out = append(out, PluginCommandCollisionRecord{Command: item.Command, PluginIDs: item.PluginIDs})
	}
	return out
}

func formatPluginTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
