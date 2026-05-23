package agentadapters

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/hecate/agent-runtime/internal/agentcontrols"
)

const launchHelpDiscoveryTimeout = 2 * time.Second
const launchModelDiscoveryTimeout = 5 * time.Second
const launchDiscoveryCacheTTL = 30 * time.Second

const launchModelUnsetValue = "__hecate_no_model_selected__"

type launchDiscoveryCacheKey struct {
	kind      string
	adapterID string
	command   string
	args      string
}

type launchDiscoveryCacheEntry struct {
	help    string
	models  []LaunchModel
	expires time.Time
}

var launchDiscoveryCache = struct {
	sync.Mutex
	items map[launchDiscoveryCacheKey]launchDiscoveryCacheEntry
}{
	items: map[launchDiscoveryCacheKey]launchDiscoveryCacheEntry{},
}

func adapterWithLaunchConfig(adapter Adapter, options []agentcontrols.ConfigOption) Adapter {
	if !launchConfigEnabled(adapter) {
		return adapter
	}
	args := append([]string(nil), adapter.Args...)
	for _, config := range launchSelectConfigs(adapter) {
		value := selectedLaunchValue(config, options)
		if value == "" {
			value = strings.TrimSpace(config.Default)
		}
		if launchSelectIsUnset(config, value) || len(config.ArgTemplate) == 0 {
			continue
		}
		args = append(args, expandLaunchSelectArgs(config.ArgTemplate, config.ConfigID, value)...)
	}
	args = append(args, adapter.LaunchSuffixArgs...)
	adapter.Args = args
	return adapter
}

func validateLaunchConfig(adapter Adapter, options []agentcontrols.ConfigOption) error {
	if !launchModelRequiresSelection(adapter.LaunchModel) {
		return nil
	}
	value := selectedLaunchModel(adapter.LaunchModel, options)
	if launchModelIsUnset(value) {
		return fmt.Errorf("%w: select a model for %s before starting the external agent", ErrLaunchModelRequired, adapter.Name)
	}
	return nil
}

func appendLaunchConfigOptions(ctx context.Context, command string, adapter Adapter, options, selected []agentcontrols.ConfigOption) ([]agentcontrols.ConfigOption, map[string]struct{}) {
	if !launchConfigEnabled(adapter) {
		return options, nil
	}
	help := discoverLaunchHelp(ctx, command, adapter)
	out := append([]agentcontrols.ConfigOption(nil), options...)
	managed := map[string]struct{}{}
	for _, config := range launchSelectConfigs(adapter) {
		if hasLaunchConfigOption(out, config) || !launchHelpSupportsTemplate(help, config.ArgTemplate) {
			continue
		}
		current, selectedPresent := selectedLaunchValueWithPresence(config, selected)
		if current == "" {
			current = strings.TrimSpace(config.Default)
		}
		if current == "" || launchSelectIsUnset(config, current) {
			current = launchSelectUnsetValue(config)
		}
		candidates := config.Options
		if config.ConfigID == launchModelConfigID(adapter.LaunchModel) && strings.EqualFold(config.Category, "model") {
			models := discoverLaunchModels(ctx, command, adapter)
			if len(models) == 0 {
				models = cloneLaunchModels(adapter.LaunchModel.FallbackModels)
			}
			if !selectedPresent && launchSelectIsUnset(config, current) {
				if defaultModel := defaultLaunchModel(models); defaultModel != "" {
					current = defaultModel
				}
			}
			candidates = launchModelsToSelectOptions(models)
		}
		out = append(out, agentcontrols.ConfigOption{
			ID:           config.ConfigID,
			Name:         config.Name,
			Description:  strings.TrimSpace(config.Description),
			Category:     strings.TrimSpace(config.Category),
			Type:         agentcontrols.ConfigOptionTypeSelect,
			CurrentValue: current,
			Options:      launchSelectOptions(config, candidates, current),
		})
		managed[config.ConfigID] = struct{}{}
	}
	if len(managed) == 0 {
		return out, nil
	}
	return out, managed
}

// LaunchConfigOptions returns Hecate-managed adapter options that can be
// selected before a concrete ACP session exists.
func LaunchConfigOptions(ctx context.Context, status Status) []agentcontrols.ConfigOption {
	if !launchConfigEnabled(status.Adapter) {
		return nil
	}
	command := strings.TrimSpace(status.Path)
	if command == "" && status.Available {
		command = strings.TrimSpace(status.Command)
	}
	options, _ := appendLaunchConfigOptions(ctx, command, status.Adapter, nil, nil)
	return options
}

func launchConfigEnabled(adapter Adapter) bool {
	return len(launchSelectConfigs(adapter)) > 0
}

func launchSelectConfigs(adapter Adapter) []LaunchSelectConfig {
	var configs []LaunchSelectConfig
	if launchModelEnabled(adapter.LaunchModel) {
		configs = append(configs, launchModelSelectConfig(adapter.LaunchModel))
	}
	configs = append(configs, adapter.LaunchOptions...)
	for i := range configs {
		configs[i].ConfigID = strings.TrimSpace(configs[i].ConfigID)
		if configs[i].ConfigID == "" {
			configs[i].ConfigID = fmt.Sprintf("launch_option_%d", i+1)
		}
		configs[i].Name = strings.TrimSpace(configs[i].Name)
		if configs[i].Name == "" {
			configs[i].Name = humanizeModelID(configs[i].ConfigID)
		}
	}
	return configs
}

func launchModelEnabled(config LaunchModelConfig) bool {
	return len(config.ArgTemplate) > 0 || len(config.ListArgs) > 0
}

func launchModelRequiresSelection(config LaunchModelConfig) bool {
	return len(config.ArgTemplate) > 0
}

func launchModelSelectConfig(config LaunchModelConfig) LaunchSelectConfig {
	return LaunchSelectConfig{
		ConfigID:         launchModelConfigID(config),
		Name:             "Model",
		Description:      "Model passed to the external-agent process when Hecate starts or restarts it.",
		Category:         "model",
		UnsetValue:       launchModelUnsetValue,
		UnsetName:        "Pick a model",
		UnsetDescription: "Do not pass --model when starting the external agent.",
		ArgTemplate:      config.ArgTemplate,
	}
}

func launchModelConfigID(config LaunchModelConfig) string {
	if id := strings.TrimSpace(config.ConfigID); id != "" {
		return id
	}
	return "model"
}

func selectedLaunchModel(config LaunchModelConfig, options []agentcontrols.ConfigOption) string {
	return selectedLaunchValue(launchModelSelectConfig(config), options)
}

func selectedLaunchValue(config LaunchSelectConfig, options []agentcontrols.ConfigOption) string {
	value, _ := selectedLaunchValueWithPresence(config, options)
	return value
}

func selectedLaunchValueWithPresence(config LaunchSelectConfig, options []agentcontrols.ConfigOption) (string, bool) {
	configID := strings.TrimSpace(config.ConfigID)
	for _, option := range options {
		if option.ID == configID {
			return strings.TrimSpace(option.CurrentValue), true
		}
	}
	return "", false
}

func launchModelIsUnset(value string) bool {
	return launchSelectIsUnset(LaunchSelectConfig{UnsetValue: launchModelUnsetValue}, value)
}

func launchSelectIsUnset(config LaunchSelectConfig, value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || value == launchSelectUnsetValue(config)
}

func launchSelectUnsetValue(config LaunchSelectConfig) string {
	if value := strings.TrimSpace(config.UnsetValue); value != "" {
		return value
	}
	return fmt.Sprintf("__hecate_no_%s_selected__", strings.TrimSpace(config.ConfigID))
}

func expandLaunchModelArgs(template []string, model string) []string {
	return expandLaunchSelectArgs(template, "model", model)
}

func expandLaunchSelectArgs(template []string, configID, value string) []string {
	args := make([]string, len(template))
	for i, arg := range template {
		arg = strings.ReplaceAll(arg, "{model}", value)
		arg = strings.ReplaceAll(arg, "{"+configID+"}", value)
		args[i] = arg
	}
	return args
}

func hasModelConfigOption(options []agentcontrols.ConfigOption) bool {
	return hasLaunchConfigOption(options, LaunchSelectConfig{ConfigID: "model", Name: "model", Category: "model"})
}

func hasLaunchConfigOption(options []agentcontrols.ConfigOption, config LaunchSelectConfig) bool {
	configID := strings.TrimSpace(config.ConfigID)
	category := strings.TrimSpace(config.Category)
	name := strings.TrimSpace(config.Name)
	for _, option := range options {
		if configID != "" && strings.EqualFold(strings.TrimSpace(option.ID), configID) {
			return true
		}
		if category != "" && strings.EqualFold(strings.TrimSpace(option.Category), category) {
			return true
		}
		if name != "" && strings.EqualFold(strings.TrimSpace(option.Name), name) {
			return true
		}
	}
	return false
}

func discoverLaunchHelp(ctx context.Context, command string, adapter Adapter) string {
	if strings.TrimSpace(command) == "" {
		return ""
	}
	args := append([]string(nil), adapter.Args...)
	args = append(args, "--help")
	key := launchDiscoveryKey("help", command, adapter.ID, args)
	if entry, ok := lookupLaunchDiscoveryCache(key); ok {
		return entry.help
	}
	discoverCtx, cancel := context.WithTimeout(ctx, launchHelpDiscoveryTimeout)
	defer cancel()
	cmd := exec.CommandContext(discoverCtx, command, args...)
	cmd.Env = sanitizedEnvForAdapter(adapter.ID, os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		storeLaunchDiscoveryCache(key, launchDiscoveryCacheEntry{})
		return ""
	}
	help := string(out)
	storeLaunchDiscoveryCache(key, launchDiscoveryCacheEntry{help: help})
	return help
}

func launchHelpSupportsTemplate(help string, template []string) bool {
	help = strings.TrimSpace(help)
	if help == "" {
		return true
	}
	for _, arg := range template {
		if strings.HasPrefix(arg, "--") && !strings.Contains(help, arg) {
			return false
		}
	}
	return true
}

func discoverLaunchModels(ctx context.Context, command string, adapter Adapter) []LaunchModel {
	if len(adapter.LaunchModel.ListArgs) == 0 || strings.TrimSpace(command) == "" {
		return nil
	}
	key := launchDiscoveryKey("models", command, adapter.ID, adapter.LaunchModel.ListArgs)
	if entry, ok := lookupLaunchDiscoveryCache(key); ok {
		return cloneLaunchModels(entry.models)
	}
	discoverCtx, cancel := context.WithTimeout(ctx, launchModelDiscoveryTimeout)
	defer cancel()
	cmd := exec.CommandContext(discoverCtx, command, adapter.LaunchModel.ListArgs...)
	cmd.Env = sanitizedEnvForAdapter(adapter.ID, os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		storeLaunchDiscoveryCache(key, launchDiscoveryCacheEntry{})
		return nil
	}
	models := parseLaunchModelList(string(out))
	storeLaunchDiscoveryCache(key, launchDiscoveryCacheEntry{models: cloneLaunchModels(models)})
	return models
}

func launchDiscoveryKey(kind, command, adapterID string, args []string) launchDiscoveryCacheKey {
	return launchDiscoveryCacheKey{
		kind:      kind,
		adapterID: strings.TrimSpace(adapterID),
		command:   strings.TrimSpace(command),
		args:      strings.Join(args, "\x00"),
	}
}

func lookupLaunchDiscoveryCache(key launchDiscoveryCacheKey) (launchDiscoveryCacheEntry, bool) {
	launchDiscoveryCache.Lock()
	defer launchDiscoveryCache.Unlock()
	entry, ok := launchDiscoveryCache.items[key]
	if !ok {
		return launchDiscoveryCacheEntry{}, false
	}
	if time.Now().After(entry.expires) {
		delete(launchDiscoveryCache.items, key)
		return launchDiscoveryCacheEntry{}, false
	}
	entry.models = cloneLaunchModels(entry.models)
	return entry, true
}

func storeLaunchDiscoveryCache(key launchDiscoveryCacheKey, entry launchDiscoveryCacheEntry) {
	entry.expires = time.Now().Add(launchDiscoveryCacheTTL)
	entry.models = cloneLaunchModels(entry.models)
	launchDiscoveryCache.Lock()
	defer launchDiscoveryCache.Unlock()
	launchDiscoveryCache.items[key] = entry
}

func parseLaunchModelList(raw string) []LaunchModel {
	seen := map[string]struct{}{}
	var models []LaunchModel
	defaultID := ""
	inModels := false
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "default model:") {
			if _, value, ok := strings.Cut(line, ":"); ok {
				defaultID = strings.TrimSpace(value)
			}
			continue
		}
		if line == "" || strings.HasPrefix(line, "Tip:") || strings.HasPrefix(lower, "you are logged in") {
			continue
		}
		if strings.TrimSuffix(lower, ":") == "available models" {
			inModels = true
			continue
		}
		if !inModels {
			continue
		}
		line = strings.TrimPrefix(line, "*")
		line = strings.TrimPrefix(line, "-")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, " - ") {
			parts := strings.SplitN(line, " - ", 2)
			name := strings.TrimSpace(parts[1])
			isDefault := strings.Contains(strings.ToLower(name), "(default)")
			name = strings.TrimSpace(strings.TrimSuffix(name, "(default)"))
			models = appendLaunchModel(models, seen, parts[0], name, isDefault)
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		id := fields[0]
		name := strings.TrimSpace(strings.TrimPrefix(line, id))
		isDefault := strings.Contains(strings.ToLower(name), "(default)")
		name = strings.TrimSpace(strings.TrimSuffix(name, "(default)"))
		models = appendLaunchModel(models, seen, id, name, isDefault)
	}
	if defaultID != "" {
		for i := range models {
			if models[i].ID == defaultID {
				models[i].Default = true
			}
		}
	}
	return models
}

func appendLaunchModel(models []LaunchModel, seen map[string]struct{}, id, name string, isDefault ...bool) []LaunchModel {
	id = strings.TrimSpace(id)
	if id == "" {
		return models
	}
	if _, ok := seen[id]; ok {
		return models
	}
	seen[id] = struct{}{}
	name = strings.TrimSpace(name)
	if name == "" {
		name = humanizeModelID(id)
	}
	model := LaunchModel{ID: id, Name: name}
	for _, value := range isDefault {
		model.Default = model.Default || value
	}
	return append(models, model)
}

func defaultLaunchModel(models []LaunchModel) string {
	for _, model := range models {
		if model.Default && strings.TrimSpace(model.ID) != "" {
			return strings.TrimSpace(model.ID)
		}
	}
	return ""
}

func launchModelSelectOptions(models []LaunchModel, current string) []agentcontrols.ConfigSelectOption {
	return launchSelectOptions(launchModelSelectConfig(LaunchModelConfig{ConfigID: "model"}), launchModelsToSelectOptions(models), current)
}

func launchSelectOptions(config LaunchSelectConfig, options []LaunchSelectOption, current string) []agentcontrols.ConfigSelectOption {
	unsetValue := launchSelectUnsetValue(config)
	unsetName := strings.TrimSpace(config.UnsetName)
	if unsetName == "" {
		unsetName = "Pick an option"
	}
	out := []agentcontrols.ConfigSelectOption{{
		Value:       unsetValue,
		Name:        unsetName,
		Description: strings.TrimSpace(config.UnsetDescription),
	}}
	seen := map[string]struct{}{unsetValue: {}}
	for _, option := range options {
		id := strings.TrimSpace(option.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		name := strings.TrimSpace(option.Name)
		if name == "" {
			name = humanizeModelID(id)
		}
		out = append(out, agentcontrols.ConfigSelectOption{
			Value:       id,
			Name:        name,
			Description: strings.TrimSpace(option.Description),
		})
	}
	current = strings.TrimSpace(current)
	if launchSelectIsUnset(config, current) {
		current = unsetValue
	}
	if current != "" {
		if _, ok := seen[current]; !ok {
			out = append([]agentcontrols.ConfigSelectOption{{
				Value: current,
				Name:  humanizeModelID(current),
			}}, out...)
		}
	}
	return out
}

func launchModelsToSelectOptions(models []LaunchModel) []LaunchSelectOption {
	out := make([]LaunchSelectOption, 0, len(models))
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		out = append(out, LaunchSelectOption{ID: id, Name: model.Name, Description: model.Description})
	}
	return out
}

func cloneLaunchModels(models []LaunchModel) []LaunchModel {
	if models == nil {
		return nil
	}
	out := make([]LaunchModel, len(models))
	copy(out, models)
	return out
}

func humanizeModelID(id string) string {
	parts := strings.FieldsFunc(id, func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	})
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%s", id)
	}
	return strings.Join(parts, " ")
}
