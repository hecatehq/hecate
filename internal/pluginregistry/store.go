package pluginregistry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	ManifestSchemaVersion = "hecate.plugin.v0"

	SourceBuiltin        = "builtin"
	SourceLocalPath      = "local_path"
	SourceImported       = "imported"
	SourceRemoteRegistry = "remote_registry"

	StateValid       = "valid"
	StateInvalid     = "invalid"
	StateUnsupported = "unsupported"

	CapabilityConnector        = "connector"
	CapabilityMCPServer        = "mcp_server"
	CapabilitySkill            = "skill"
	CapabilitySlashCommand     = "slash_command"
	CapabilityProjectMapper    = "project_mapper"
	CapabilityEvidenceProvider = "evidence_provider"
	CapabilityUISurface        = "ui_surface"

	PermissionAdvisory    = "advisory"
	PermissionEnforced    = "enforced"
	PermissionUnsupported = "unsupported"

	AuthToken   = "token"
	AuthOAuth   = "oauth"
	AuthEnv     = "env"
	AuthNone    = "none"
	AuthUnknown = "unknown"

	AuthStatusUnknown    = "unknown"
	AuthStatusConfigured = "configured"
	AuthStatusExpired    = "expired"
	AuthStatusError      = "error"
)

var (
	ErrNotFound = errors.New("plugin not found")
	ErrInvalid  = errors.New("invalid plugin")
	ErrConflict = errors.New("plugin conflict")
)

type Plugin struct {
	ID                    string
	Name                  string
	Description           string
	Version               string
	SourceKind            string
	SourceRef             string
	ManifestSchemaVersion string
	ManifestDigest        string
	ManifestJSON          json.RawMessage
	RequestedPermissions  []Permission
	RegistryState         string
	Enabled               bool
	Warnings              []string
	Capabilities          []Capability
	Auth                  []AuthBinding
	InstalledAt           time.Time
	UpdatedAt             time.Time
}

type Capability struct {
	ID                   string
	PluginID             string
	Kind                 string
	DisplayName          string
	RequestedPermissions []Permission
	Enabled              bool
	ConfigJSON           json.RawMessage
	Warnings             []string
}

type Permission struct {
	Value          string
	Classification string
}

type AuthBinding struct {
	PluginID      string
	CapabilityID  string
	RequestedName string
	Kind          string
	Status        string
	SecretRef     string
	Warnings      []string
}

type Health struct {
	PluginID                 string
	RegistryState            string
	Warnings                 []string
	UnsupportedPermissions   []string
	UnresolvedSecretBindings []string
	DisabledCapabilities     []string
	CommandCollisions        []CommandCollision
}

type CommandCollision struct {
	Command   string
	PluginIDs []string
}

type Store interface {
	Backend() string
	List(ctx context.Context) ([]Plugin, error)
	Get(ctx context.Context, id string) (Plugin, bool, error)
	Upsert(ctx context.Context, plugin Plugin) (Plugin, error)
	Update(ctx context.Context, id string, update func(*Plugin)) (Plugin, error)
	Clear(ctx context.Context) (int, error)
}

type MemoryStore struct {
	mu      sync.Mutex
	plugins map[string]Plugin
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{plugins: make(map[string]Plugin)}
}

func (s *MemoryStore) Backend() string { return "memory" }

func (s *MemoryStore) List(_ context.Context) ([]Plugin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneSortedPlugins(s.plugins), nil
}

func (s *MemoryStore) Get(_ context.Context, id string) (Plugin, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plugin, ok := s.plugins[normalizeID(id)]
	if !ok {
		return Plugin{}, false, nil
	}
	return clonePlugin(plugin), true, nil
}

func (s *MemoryStore) Upsert(_ context.Context, plugin Plugin) (Plugin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if existing, ok := s.plugins[normalizeID(plugin.ID)]; ok && !existing.InstalledAt.IsZero() {
		plugin.InstalledAt = existing.InstalledAt
	}
	plugin = NormalizePlugin(plugin, now)
	if err := ValidatePlugin(plugin); err != nil {
		return Plugin{}, err
	}
	s.plugins[plugin.ID] = clonePlugin(plugin)
	return clonePlugin(plugin), nil
}

func (s *MemoryStore) Update(_ context.Context, id string, update func(*Plugin)) (Plugin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = normalizeID(id)
	plugin, ok := s.plugins[id]
	if !ok {
		return Plugin{}, ErrNotFound
	}
	plugin = clonePlugin(plugin)
	originalID := plugin.ID
	installedAt := plugin.InstalledAt
	if update != nil {
		update(&plugin)
	}
	plugin.ID = originalID
	plugin.InstalledAt = installedAt
	plugin.UpdatedAt = time.Now().UTC()
	plugin = NormalizePlugin(plugin, plugin.UpdatedAt)
	if err := ValidatePlugin(plugin); err != nil {
		return Plugin{}, err
	}
	s.plugins[id] = clonePlugin(plugin)
	return clonePlugin(plugin), nil
}

func (s *MemoryStore) Clear(_ context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := len(s.plugins)
	s.plugins = make(map[string]Plugin)
	return count, nil
}

func NormalizePlugin(plugin Plugin, now time.Time) Plugin {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	plugin.ID = normalizeID(plugin.ID)
	plugin.Name = strings.TrimSpace(plugin.Name)
	plugin.Description = strings.TrimSpace(plugin.Description)
	plugin.Version = strings.TrimSpace(plugin.Version)
	plugin.SourceKind = strings.TrimSpace(plugin.SourceKind)
	plugin.SourceRef = strings.TrimSpace(plugin.SourceRef)
	plugin.ManifestSchemaVersion = strings.TrimSpace(plugin.ManifestSchemaVersion)
	plugin.ManifestDigest = strings.TrimSpace(plugin.ManifestDigest)
	plugin.RegistryState = strings.TrimSpace(plugin.RegistryState)
	if plugin.Name == "" {
		plugin.Name = titleFromID(plugin.ID)
	}
	if plugin.SourceKind == "" {
		plugin.SourceKind = SourceLocalPath
	}
	if plugin.ManifestSchemaVersion == "" {
		plugin.ManifestSchemaVersion = ManifestSchemaVersion
	}
	if plugin.RegistryState == "" {
		plugin.RegistryState = StateValid
	}
	if plugin.InstalledAt.IsZero() {
		plugin.InstalledAt = now
	}
	if plugin.UpdatedAt.IsZero() {
		plugin.UpdatedAt = plugin.InstalledAt
	}
	plugin.ManifestJSON = compactJSON(plugin.ManifestJSON)
	if len(plugin.ManifestJSON) > 0 && plugin.ManifestDigest == "" {
		plugin.ManifestDigest = digestJSON(plugin.ManifestJSON)
	}
	plugin.RequestedPermissions = NormalizePermissions(plugin.RequestedPermissions)
	plugin.Warnings = normalizeStringSlice(plugin.Warnings)
	for idx := range plugin.Capabilities {
		plugin.Capabilities[idx] = NormalizeCapability(plugin.ID, plugin.Capabilities[idx])
	}
	sortCapabilities(plugin.Capabilities)
	for idx := range plugin.Auth {
		plugin.Auth[idx] = NormalizeAuthBinding(plugin.ID, plugin.Auth[idx])
	}
	sortAuthBindings(plugin.Auth)
	return plugin
}

func NormalizeCapability(pluginID string, capability Capability) Capability {
	capability.PluginID = normalizeID(pluginID)
	capability.ID = normalizeID(capability.ID)
	capability.Kind = strings.TrimSpace(capability.Kind)
	capability.DisplayName = strings.TrimSpace(capability.DisplayName)
	if capability.DisplayName == "" {
		capability.DisplayName = titleFromID(capability.ID)
	}
	capability.RequestedPermissions = NormalizePermissions(capability.RequestedPermissions)
	capability.ConfigJSON = compactJSON(capability.ConfigJSON)
	capability.Warnings = normalizeStringSlice(capability.Warnings)
	return capability
}

func NormalizeAuthBinding(pluginID string, binding AuthBinding) AuthBinding {
	binding.PluginID = normalizeID(pluginID)
	binding.CapabilityID = normalizeID(binding.CapabilityID)
	binding.RequestedName = normalizeID(binding.RequestedName)
	binding.Kind = strings.TrimSpace(binding.Kind)
	binding.Status = strings.TrimSpace(binding.Status)
	binding.SecretRef = strings.TrimSpace(binding.SecretRef)
	if binding.Kind == "" {
		binding.Kind = AuthUnknown
	}
	if binding.Status == "" {
		binding.Status = AuthStatusUnknown
	}
	binding.Warnings = normalizeStringSlice(binding.Warnings)
	return binding
}

func NormalizePermissions(items []Permission) []Permission {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(items))
	out := make([]Permission, 0, len(items))
	for _, item := range items {
		value := strings.TrimSpace(item.Value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		classification := strings.TrimSpace(item.Classification)
		if classification == "" {
			classification = ClassifyPermission(value)
		}
		out = append(out, Permission{Value: value, Classification: classification})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out
}

func ClassifyPermission(value string) string {
	value = strings.TrimSpace(value)
	switch {
	case strings.HasPrefix(value, "network:"):
		return PermissionAdvisory
	case strings.HasPrefix(value, "secret:"):
		return PermissionAdvisory
	case strings.HasPrefix(value, "mcp:"):
		return PermissionAdvisory
	case strings.HasPrefix(value, "project:"):
		return PermissionAdvisory
	case strings.HasPrefix(value, "workspace:"):
		return PermissionAdvisory
	case strings.HasPrefix(value, "external-ref:"):
		return PermissionAdvisory
	default:
		return PermissionUnsupported
	}
}

func ValidatePlugin(plugin Plugin) error {
	if plugin.ID == "" || plugin.Name == "" || plugin.Version == "" {
		return ErrInvalid
	}
	if !oneOf(plugin.SourceKind, SourceBuiltin, SourceLocalPath, SourceImported, SourceRemoteRegistry) {
		return ErrInvalid
	}
	if plugin.ManifestSchemaVersion != ManifestSchemaVersion {
		return ErrInvalid
	}
	if !oneOf(plugin.RegistryState, StateValid, StateInvalid, StateUnsupported) {
		return ErrInvalid
	}
	if !validPermissionClassifications(plugin.RequestedPermissions) {
		return ErrInvalid
	}
	seenCapabilities := make(map[string]bool, len(plugin.Capabilities))
	for _, capability := range plugin.Capabilities {
		if capability.PluginID != plugin.ID || capability.ID == "" {
			return ErrInvalid
		}
		if seenCapabilities[capability.ID] {
			return ErrInvalid
		}
		seenCapabilities[capability.ID] = true
		if !oneOf(capability.Kind, CapabilityConnector, CapabilityMCPServer, CapabilitySkill, CapabilitySlashCommand, CapabilityProjectMapper, CapabilityEvidenceProvider, CapabilityUISurface) {
			return ErrInvalid
		}
		if capability.Kind == CapabilityMCPServer {
			if _, err := ParseMCPServerConfig(capability.ID, capability.ConfigJSON); err != nil {
				return err
			}
		}
		if !validPermissionClassifications(capability.RequestedPermissions) {
			return ErrInvalid
		}
	}
	for _, binding := range plugin.Auth {
		if binding.PluginID != plugin.ID || binding.RequestedName == "" {
			return ErrInvalid
		}
		if binding.CapabilityID != "" && !seenCapabilities[binding.CapabilityID] {
			return ErrInvalid
		}
		if !oneOf(binding.Kind, AuthToken, AuthOAuth, AuthEnv, AuthNone, AuthUnknown) {
			return ErrInvalid
		}
		if !oneOf(binding.Status, AuthStatusUnknown, AuthStatusConfigured, AuthStatusExpired, AuthStatusError) {
			return ErrInvalid
		}
	}
	return nil
}

func HealthFor(plugin Plugin, all []Plugin) Health {
	plugin = NormalizePlugin(plugin, time.Now().UTC())
	health := Health{
		PluginID:      plugin.ID,
		RegistryState: plugin.RegistryState,
		Warnings:      append([]string(nil), plugin.Warnings...),
	}
	for _, perm := range plugin.RequestedPermissions {
		if perm.Classification == PermissionUnsupported {
			health.UnsupportedPermissions = appendUniqueString(health.UnsupportedPermissions, perm.Value)
		}
	}
	for _, capability := range plugin.Capabilities {
		if !capability.Enabled {
			health.DisabledCapabilities = appendUniqueString(health.DisabledCapabilities, capability.ID)
		}
		for _, perm := range capability.RequestedPermissions {
			if perm.Classification == PermissionUnsupported {
				health.UnsupportedPermissions = appendUniqueString(health.UnsupportedPermissions, perm.Value)
			}
		}
		health.Warnings = appendUniqueStrings(health.Warnings, capability.Warnings...)
	}
	for _, binding := range plugin.Auth {
		if binding.Kind != AuthNone && binding.SecretRef == "" && binding.Status != AuthStatusConfigured {
			health.UnresolvedSecretBindings = appendUniqueString(health.UnresolvedSecretBindings, binding.RequestedName)
		}
		health.Warnings = appendUniqueStrings(health.Warnings, binding.Warnings...)
	}
	health.CommandCollisions = commandCollisions(plugin, all)
	sort.Strings(health.UnsupportedPermissions)
	sort.Strings(health.UnresolvedSecretBindings)
	sort.Strings(health.DisabledCapabilities)
	sort.Strings(health.Warnings)
	return health
}

func cloneSortedPlugins(items map[string]Plugin) []Plugin {
	out := make([]Plugin, 0, len(items))
	for _, item := range items {
		out = append(out, clonePlugin(item))
	}
	sortPlugins(out)
	return out
}

func clonePlugin(plugin Plugin) Plugin {
	plugin.ManifestJSON = cloneRaw(plugin.ManifestJSON)
	plugin.RequestedPermissions = append([]Permission(nil), plugin.RequestedPermissions...)
	plugin.Warnings = append([]string(nil), plugin.Warnings...)
	plugin.Capabilities = append([]Capability(nil), plugin.Capabilities...)
	for idx := range plugin.Capabilities {
		plugin.Capabilities[idx].RequestedPermissions = append([]Permission(nil), plugin.Capabilities[idx].RequestedPermissions...)
		plugin.Capabilities[idx].ConfigJSON = cloneRaw(plugin.Capabilities[idx].ConfigJSON)
		plugin.Capabilities[idx].Warnings = append([]string(nil), plugin.Capabilities[idx].Warnings...)
	}
	plugin.Auth = append([]AuthBinding(nil), plugin.Auth...)
	for idx := range plugin.Auth {
		plugin.Auth[idx].Warnings = append([]string(nil), plugin.Auth[idx].Warnings...)
	}
	return plugin
}

func sortPlugins(items []Plugin) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].ID < items[j].ID
	})
}

func sortCapabilities(items []Capability) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].ID < items[j].ID
	})
}

func sortAuthBindings(items []AuthBinding) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].CapabilityID != items[j].CapabilityID {
			return items[i].CapabilityID < items[j].CapabilityID
		}
		return items[i].RequestedName < items[j].RequestedName
	})
}

func commandCollisions(plugin Plugin, all []Plugin) []CommandCollision {
	byCommand := make(map[string][]string)
	for _, item := range all {
		for _, capability := range item.Capabilities {
			if capability.Kind != CapabilitySlashCommand {
				continue
			}
			command := strings.TrimPrefix(capability.ID, "/")
			command = strings.TrimSpace(command)
			if command == "" {
				continue
			}
			byCommand[command] = appendUniqueString(byCommand[command], item.ID)
		}
	}
	var out []CommandCollision
	for command, pluginIDs := range byCommand {
		if len(pluginIDs) < 2 || !containsString(pluginIDs, plugin.ID) {
			continue
		}
		sort.Strings(pluginIDs)
		out = append(out, CommandCollision{Command: command, PluginIDs: pluginIDs})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Command < out[j].Command })
	return out
}

func validPermissionClassifications(items []Permission) bool {
	for _, item := range items {
		if !oneOf(item.Classification, PermissionAdvisory, PermissionEnforced, PermissionUnsupported) {
			return false
		}
	}
	return true
}

func compactJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return cloneRaw(raw)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return cloneRaw(raw)
	}
	return encoded
}

func digestJSON(raw json.RawMessage) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizeID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '_' || r == '.' || r == '-':
			b.WriteRune(r)
			lastDash = r == '-'
		case unicode.IsSpace(r), r == '/', r == ':':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-_.")
}

func titleFromID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "Untitled Plugin"
	}
	parts := strings.FieldsFunc(id, func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	})
	for idx, part := range parts {
		if part == "" {
			continue
		}
		parts[idx] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func normalizeStringSlice(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func appendUniqueString(items []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || containsString(items, value) {
		return items
	}
	return append(items, value)
}

func appendUniqueStrings(items []string, values ...string) []string {
	for _, value := range values {
		items = appendUniqueString(items, value)
	}
	return items
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}
