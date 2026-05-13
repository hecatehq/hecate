package agentcontrols

import (
	"fmt"

	acp "github.com/coder/acp-go-sdk"
)

const (
	ConfigOptionTypeSelect  = "select"
	ConfigOptionTypeBoolean = "boolean"
	ConfigOptionTypeUnknown = "unknown"
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
	return ConfigOption{
		ID:           string(option.Id),
		Name:         option.Name,
		Description:  derefString(option.Description),
		Category:     categoryString(option.Category),
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
	if category == nil || category.Other == nil {
		return ""
	}
	return string(*category.Other)
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
