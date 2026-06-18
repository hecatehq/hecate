package pluginregistry

import (
	"encoding/json"
	"strings"
	"time"
)

type Manifest struct {
	SchemaVersion string          `json:"schema_version"`
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Version       string          `json:"version"`
	Permissions   []string        `json:"permissions"`
	Auth          []manifestAuth  `json:"auth"`
	Capabilities  json.RawMessage `json:"capabilities"`
}

type manifestCapability struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Kind           string            `json:"kind"`
	DisplayName    string            `json:"display_name"`
	Permissions    []string          `json:"permissions"`
	Enabled        *bool             `json:"enabled"`
	Auth           []manifestAuth    `json:"auth"`
	Config         json.RawMessage   `json:"config"`
	Transport      string            `json:"transport"`
	Command        string            `json:"command"`
	Args           []string          `json:"args"`
	Env            map[string]string `json:"env"`
	URL            string            `json:"url"`
	Headers        map[string]string `json:"headers"`
	ApprovalPolicy string            `json:"approval_policy"`
	raw            json.RawMessage
}

func (item *manifestCapability) UnmarshalJSON(raw []byte) error {
	type manifestCapabilityAlias manifestCapability
	var decoded manifestCapabilityAlias
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	*item = manifestCapability(decoded)
	item.raw = compactJSON(raw)
	return nil
}

type manifestAuth struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	CapabilityID string   `json:"capability_id"`
	SecretRef    string   `json:"secret_ref"`
	Warnings     []string `json:"warnings"`
}

type capabilityGroups struct {
	Connectors        []manifestCapability `json:"connectors"`
	MCPServers        []manifestCapability `json:"mcp_servers"`
	Skills            []manifestCapability `json:"skills"`
	SlashCommands     []manifestCapability `json:"slash_commands"`
	ProjectMappers    []manifestCapability `json:"project_mappers"`
	EvidenceProviders []manifestCapability `json:"evidence_providers"`
	UISurfaces        []manifestCapability `json:"ui_surfaces"`
}

func PluginFromManifest(raw json.RawMessage, sourceKind, sourceRef string) (Plugin, error) {
	raw = compactJSON(raw)
	if len(raw) == 0 {
		return Plugin{}, ErrInvalid
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Plugin{}, ErrInvalid
	}
	if strings.TrimSpace(manifest.SchemaVersion) != ManifestSchemaVersion {
		return Plugin{}, ErrInvalid
	}
	plugin := Plugin{
		ID:                    manifest.ID,
		Name:                  manifest.Name,
		Description:           manifest.Description,
		Version:               manifest.Version,
		SourceKind:            sourceKind,
		SourceRef:             sourceRef,
		ManifestSchemaVersion: strings.TrimSpace(manifest.SchemaVersion),
		ManifestJSON:          raw,
		RequestedPermissions:  permissionsFromStrings(manifest.Permissions),
		RegistryState:         StateValid,
		Enabled:               false,
	}
	capabilities, auth, err := parseManifestCapabilities(manifest.Capabilities)
	if err != nil {
		return Plugin{}, err
	}
	plugin.Capabilities = capabilities
	for _, binding := range manifest.Auth {
		auth = append(auth, authBindingFromManifest("", binding))
	}
	plugin.Auth = auth
	plugin = NormalizePlugin(plugin, time.Now().UTC())
	if err := ValidatePlugin(plugin); err != nil {
		return Plugin{}, err
	}
	return plugin, nil
}

func parseManifestCapabilities(raw json.RawMessage) ([]Capability, []AuthBinding, error) {
	if len(raw) == 0 {
		return nil, nil, nil
	}
	var list []manifestCapability
	if err := json.Unmarshal(raw, &list); err == nil {
		return capabilitiesFromManifest(list, "")
	}
	var groups capabilityGroups
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, nil, ErrInvalid
	}
	var all []Capability
	var auth []AuthBinding
	for _, group := range []struct {
		kind  string
		items []manifestCapability
	}{
		{kind: CapabilityConnector, items: groups.Connectors},
		{kind: CapabilityMCPServer, items: groups.MCPServers},
		{kind: CapabilitySkill, items: groups.Skills},
		{kind: CapabilitySlashCommand, items: groups.SlashCommands},
		{kind: CapabilityProjectMapper, items: groups.ProjectMappers},
		{kind: CapabilityEvidenceProvider, items: groups.EvidenceProviders},
		{kind: CapabilityUISurface, items: groups.UISurfaces},
	} {
		capabilities, bindings, err := capabilitiesFromManifest(group.items, group.kind)
		if err != nil {
			return nil, nil, err
		}
		all = append(all, capabilities...)
		auth = append(auth, bindings...)
	}
	return all, auth, nil
}

func capabilitiesFromManifest(items []manifestCapability, forcedKind string) ([]Capability, []AuthBinding, error) {
	var capabilities []Capability
	var auth []AuthBinding
	for _, item := range items {
		kind := strings.TrimSpace(item.Kind)
		if forcedKind != "" {
			kind = forcedKind
		}
		id := item.ID
		if id == "" && kind == CapabilitySlashCommand {
			id = strings.TrimPrefix(item.Name, "/")
		}
		displayName := item.DisplayName
		if displayName == "" && kind == CapabilitySlashCommand && item.Name != "" {
			displayName = "/" + strings.TrimPrefix(item.Name, "/")
		}
		enabled := true
		if item.Enabled != nil {
			enabled = *item.Enabled
		}
		capability := Capability{
			ID:                   id,
			Kind:                 kind,
			DisplayName:          displayName,
			RequestedPermissions: permissionsFromStrings(item.Permissions),
			Enabled:              enabled,
			ConfigJSON:           item.Config,
		}
		if kind == CapabilityMCPServer {
			configJSON, err := mcpServerConfigJSONFromManifest(id, item)
			if err != nil {
				return nil, nil, err
			}
			capability.ConfigJSON = configJSON
		}
		capability = NormalizeCapability("", capability)
		if capability.ID == "" || capability.Kind == "" {
			return nil, nil, ErrInvalid
		}
		capabilities = append(capabilities, capability)
		for _, binding := range item.Auth {
			auth = append(auth, authBindingFromManifest(capability.ID, binding))
		}
	}
	return capabilities, auth, nil
}

func mcpServerConfigJSONFromManifest(capabilityID string, item manifestCapability) (json.RawMessage, error) {
	if err := validateMCPServerCapabilityFields(item.raw); err != nil {
		return nil, err
	}
	hasConfig := len(compactJSON(item.Config)) > 0
	hasInline := hasInlineMCPServerConfig(item)
	if hasConfig && hasInline {
		return nil, ErrInvalid
	}
	if hasConfig {
		return normalizeMCPServerConfigJSON(capabilityID, item.Config)
	}
	cfg := MCPServerConfig{
		Name:           item.Name,
		Transport:      item.Transport,
		Command:        item.Command,
		Args:           append([]string(nil), item.Args...),
		Env:            cloneStringMap(item.Env),
		URL:            item.URL,
		Headers:        cloneStringMap(item.Headers),
		ApprovalPolicy: item.ApprovalPolicy,
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, ErrInvalid
	}
	return normalizeMCPServerConfigJSON(capabilityID, raw)
}

func validateMCPServerCapabilityFields(raw json.RawMessage) error {
	if len(raw) == 0 {
		return ErrInvalid
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return ErrInvalid
	}
	for field := range fields {
		switch field {
		case "id", "name", "kind", "display_name", "permissions", "enabled", "auth",
			"config", "transport", "command", "args", "env", "url", "headers", "approval_policy":
		default:
			return ErrInvalid
		}
	}
	return nil
}

func hasInlineMCPServerConfig(item manifestCapability) bool {
	return strings.TrimSpace(item.Transport) != "" ||
		strings.TrimSpace(item.Command) != "" ||
		len(item.Args) > 0 ||
		len(item.Env) > 0 ||
		strings.TrimSpace(item.URL) != "" ||
		len(item.Headers) > 0 ||
		strings.TrimSpace(item.ApprovalPolicy) != ""
}

func cloneStringMap(items map[string]string) map[string]string {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]string, len(items))
	for key, value := range items {
		out[key] = value
	}
	return out
}

func authBindingFromManifest(capabilityID string, item manifestAuth) AuthBinding {
	if capabilityID == "" {
		capabilityID = item.CapabilityID
	}
	return AuthBinding{
		CapabilityID:  capabilityID,
		RequestedName: item.Name,
		Kind:          item.Kind,
		Status:        AuthStatusUnknown,
		Warnings:      item.Warnings,
	}
}

func permissionsFromStrings(values []string) []Permission {
	if len(values) == 0 {
		return nil
	}
	out := make([]Permission, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, Permission{Value: value, Classification: ClassifyPermission(value)})
	}
	return NormalizePermissions(out)
}
