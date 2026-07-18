package agentcontrols

import (
	"fmt"
	"strings"

	acp "github.com/coder/acp-go-sdk"
)

const (
	ConfigOptionTypeSelect  = "select"
	ConfigOptionTypeBoolean = "boolean"
	ConfigOptionTypeUnknown = "unknown"

	ConfigOptionSourceLaunch   = "launch"
	ConfigOptionSourceACPModel = "acp_model"
)

// ConfigOption is Hecate's stable projection of ACP session config
// options. ACP owns the source-of-truth labels/categories; Hecate keeps
// the shape simple so sessions can persist it without depending on the
// SDK's generated union internals.
type ConfigOption struct {
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	Description  string               `json:"description,omitempty"`
	Category     string               `json:"category,omitempty"`
	Source       string               `json:"source,omitempty"`
	Type         string               `json:"type"`
	CurrentValue string               `json:"current_value,omitempty"`
	CurrentBool  *bool                `json:"current_bool,omitempty"`
	Options      []ConfigSelectOption `json:"options,omitempty"`
}

type ConfigSelectOption struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Group       string `json:"group,omitempty"`
	GroupName   string `json:"group_name,omitempty"`
}

// Command is Hecate's stable projection of ACP available slash commands.
// ACP owns the command names and descriptions; Hecate keeps only the
// operator-facing metadata needed to render hints and send the command text
// back through session/prompt.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputHint   string `json:"input_hint,omitempty"`
}

// ImplementationInfo is Hecate's stable projection of ACP implementation
// metadata. ACP owns the raw initialize shape; Hecate persists this trimmed
// subset so operators can see which agent implementation handled a session.
type ImplementationInfo struct {
	Name    string `json:"name,omitempty"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

type SetConfigOptionRequest struct {
	SessionID string
	ConfigID  string
	Value     string
	BoolValue *bool
}

// FromACPOptions converts ACP session config options to Hecate's stable
// projection. Unknown option variants are preserved as explicit placeholders
// so new ACP union shapes stay visible instead of disappearing from the UI.
func FromACPOptions(options []acp.SessionConfigOption) []ConfigOption {
	if len(options) == 0 {
		return nil
	}
	out := make([]ConfigOption, 0, len(options))
	for _, option := range options {
		switch {
		case option.Select != nil:
			out = append(out, fromACPSelect(*option.Select))
		case option.Boolean != nil:
			out = append(out, fromACPBoolean(*option.Boolean))
		default:
			out = append(out, unknownACPOption(len(out)))
		}
	}
	return out
}

// FromACPCommands converts ACP available commands to Hecate's stable wire
// shape. A non-nil but empty input remains an explicit empty output so an ACP
// replacement snapshot can clear prior command hints. Commands with empty names
// are ignored because the name is the prompt token the operator sends back to
// the agent, usually as /name.
func FromACPCommands(commands []acp.AvailableCommand) []Command {
	if commands == nil {
		return nil
	}
	out := make([]Command, 0, len(commands))
	seen := make(map[string]struct{}, len(commands))
	for _, command := range commands {
		name := trimString(command.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, Command{
			Name:        name,
			Description: trimString(command.Description),
			InputHint:   acpCommandInputHint(command.Input),
		})
	}
	return out
}

// FromACPImplementation converts ACP initialize implementation metadata to the
// stable Hecate projection. Empty metadata is omitted.
func FromACPImplementation(info *acp.Implementation) *ImplementationInfo {
	if info == nil {
		return nil
	}
	out := ImplementationInfo{
		Name:    trimString(info.Name),
		Version: trimString(info.Version),
	}
	if info.Title != nil {
		out.Title = trimString(*info.Title)
	}
	if out.Name == "" && out.Title == "" && out.Version == "" {
		return nil
	}
	return &out
}

// BuildACPSetRequest converts a SetConfigOptionRequest to the ACP wire shape.
// Exactly one of BoolValue (for boolean options) or Value (for select options)
// must be set; supplying both is an error.
func BuildACPSetRequest(req SetConfigOptionRequest) (acp.SetSessionConfigOptionRequest, error) {
	if req.SessionID == "" {
		return acp.SetSessionConfigOptionRequest{}, fmt.Errorf("session id is required")
	}
	if req.ConfigID == "" {
		return acp.SetSessionConfigOptionRequest{}, fmt.Errorf("config id is required")
	}
	if req.BoolValue != nil && req.Value != "" {
		return acp.SetSessionConfigOptionRequest{}, fmt.Errorf("provide either value or bool value, not both")
	}
	if req.BoolValue != nil {
		return acp.SetSessionConfigOptionRequest{
			Boolean: &acp.SetSessionConfigOptionBoolean{
				SessionId: acp.SessionId(req.SessionID),
				ConfigId:  acp.SessionConfigId(req.ConfigID),
				Type:      ConfigOptionTypeBoolean,
				Value:     *req.BoolValue,
			},
		}, nil
	}
	if req.Value == "" {
		return acp.SetSessionConfigOptionRequest{}, fmt.Errorf("value is required")
	}
	return acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: acp.SessionId(req.SessionID),
			ConfigId:  acp.SessionConfigId(req.ConfigID),
			Value:     acp.SessionConfigValueId(req.Value),
		},
	}, nil
}

func unknownACPOption(index int) ConfigOption {
	return ConfigOption{
		ID:          fmt.Sprintf("unknown_%d", index+1),
		Name:        "Unsupported option",
		Description: "This adapter returned a config option shape this Hecate version does not understand.",
		Type:        ConfigOptionTypeUnknown,
	}
}

func fromACPSelect(option acp.SessionConfigOptionSelect) ConfigOption {
	category := categoryString(option.Category)
	return ConfigOption{
		ID:           string(option.Id),
		Name:         option.Name,
		Description:  derefString(option.Description),
		Category:     category,
		Source:       acpSelectSource(category),
		Type:         ConfigOptionTypeSelect,
		CurrentValue: string(option.CurrentValue),
		Options:      flattenACPSelectOptions(option.Options),
	}
}

func fromACPBoolean(option acp.SessionConfigOptionBoolean) ConfigOption {
	value := option.CurrentValue
	return ConfigOption{
		ID:          string(option.Id),
		Name:        option.Name,
		Description: derefString(option.Description),
		Category:    categoryString(option.Category),
		Type:        ConfigOptionTypeBoolean,
		CurrentBool: &value,
	}
}

func flattenACPSelectOptions(options acp.SessionConfigSelectOptions) []ConfigSelectOption {
	if options.Ungrouped != nil {
		items := make([]ConfigSelectOption, 0, len(*options.Ungrouped))
		for _, option := range *options.Ungrouped {
			items = append(items, fromACPSelectOption(option, "", ""))
		}
		return items
	}
	if options.Grouped != nil {
		var items []ConfigSelectOption
		for _, group := range *options.Grouped {
			for _, option := range group.Options {
				items = append(items, fromACPSelectOption(option, string(group.Group), group.Name))
			}
		}
		return items
	}
	return nil
}

func fromACPSelectOption(option acp.SessionConfigSelectOption, group, groupName string) ConfigSelectOption {
	return ConfigSelectOption{
		Value:       string(option.Value),
		Name:        option.Name,
		Description: derefString(option.Description),
		Group:       group,
		GroupName:   groupName,
	}
}

func categoryString(category *acp.SessionConfigOptionCategory) string {
	if category == nil {
		return ""
	}
	return string(*category)
}

func acpCommandInputHint(input *acp.AvailableCommandInput) string {
	if input == nil || input.Unstructured == nil {
		return ""
	}
	return trimString(input.Unstructured.Hint)
}

func acpSelectSource(category string) string {
	if category == string(acp.SessionConfigOptionCategoryModel) {
		return ConfigOptionSourceACPModel
	}
	return ""
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func trimString(value string) string {
	return strings.TrimSpace(value)
}
