package agentcontrols

import (
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func TestFromACPOptions_NormalizesSelectAndBoolean(t *testing.T) {
	description := "pick a model"
	category := acp.SessionConfigOptionCategoryModel
	selectOptions := acp.SessionConfigSelectOptionsUngrouped{
		{Value: acp.SessionConfigValueId("fast"), Name: "Fast"},
		{Value: acp.SessionConfigValueId("smart"), Name: "Smart", Description: &description},
	}

	got := FromACPOptions([]acp.SessionConfigOption{
		{Select: &acp.SessionConfigOptionSelect{
			Id:           acp.SessionConfigId("model"),
			Name:         "Model",
			Description:  &description,
			Category:     &category,
			CurrentValue: acp.SessionConfigValueId("fast"),
			Options:      acp.SessionConfigSelectOptions{Ungrouped: &selectOptions},
		}},
		{Boolean: &acp.SessionConfigOptionBoolean{
			Id:           acp.SessionConfigId("auto"),
			Name:         "Auto",
			CurrentValue: true,
		}},
	})

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "model" || got[0].Category != "model" || got[0].Source != ConfigOptionSourceACPModel || got[0].CurrentValue != "fast" || len(got[0].Options) != 2 {
		t.Fatalf("select option = %#v", got[0])
	}
	if got[0].Options[1].Description != description {
		t.Fatalf("option description = %q, want %q", got[0].Options[1].Description, description)
	}
	if got[1].ID != "auto" || got[1].CurrentBool == nil || !*got[1].CurrentBool {
		t.Fatalf("boolean option = %#v", got[1])
	}
}

func TestFromACPOptions_FlattensGroupedSelectOptions(t *testing.T) {
	grouped := acp.SessionConfigSelectOptionsGrouped{
		{
			Group: acp.SessionConfigGroupId("opus"),
			Name:  "Opus",
			Options: []acp.SessionConfigSelectOption{
				{Value: acp.SessionConfigValueId("opus-4.1"), Name: "Opus 4.1"},
			},
		},
		{
			Group: acp.SessionConfigGroupId("sonnet"),
			Name:  "Sonnet",
			Options: []acp.SessionConfigSelectOption{
				{Value: acp.SessionConfigValueId("sonnet-4.5"), Name: "Sonnet 4.5"},
			},
		},
	}

	got := FromACPOptions([]acp.SessionConfigOption{
		{Select: &acp.SessionConfigOptionSelect{
			Id:      acp.SessionConfigId("model"),
			Name:    "Model",
			Options: acp.SessionConfigSelectOptions{Grouped: &grouped},
		}},
	})

	if len(got) != 1 || len(got[0].Options) != 2 {
		t.Fatalf("grouped options = %#v, want two flattened options", got)
	}
	if got[0].Options[0].Group != "opus" || got[0].Options[0].GroupName != "Opus" {
		t.Fatalf("first grouped option = %#v, want Opus metadata", got[0].Options[0])
	}
	if got[0].Options[1].Group != "sonnet" || got[0].Options[1].GroupName != "Sonnet" {
		t.Fatalf("second grouped option = %#v, want Sonnet metadata", got[0].Options[1])
	}
}

func TestFromACPOptions_PreservesUnknownVariants(t *testing.T) {
	got := FromACPOptions([]acp.SessionConfigOption{{}})
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 unknown placeholder", len(got))
	}
	if got[0].Type != ConfigOptionTypeUnknown || got[0].ID != "unknown_1" || got[0].Name == "" {
		t.Fatalf("unknown option = %#v", got[0])
	}
}

func TestFromACPCommands_NormalizesAvailableCommands(t *testing.T) {
	got := FromACPCommands([]acp.AvailableCommand{
		{
			Name:        " web ",
			Description: " Search the web ",
			Input: &acp.AvailableCommandInput{
				Unstructured: &acp.UnstructuredCommandInput{Hint: " query "},
			},
		},
		{Name: "web", Description: "duplicate"},
		{Name: " "},
		{Name: "plan"},
	})

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 commands: %#v", len(got), got)
	}
	if got[0].Name != "web" || got[0].Description != "Search the web" || got[0].InputHint != "query" {
		t.Fatalf("first command = %#v, want normalized web command", got[0])
	}
	if got[1].Name != "plan" {
		t.Fatalf("second command = %#v, want plan", got[1])
	}
}

func TestFromACPImplementation_TrimsAndOmitsEmptyValues(t *testing.T) {
	if got := FromACPImplementation(nil); got != nil {
		t.Fatalf("FromACPImplementation(nil) = %#v, want nil", got)
	}
	blankTitle := " "
	if got := FromACPImplementation(&acp.Implementation{Name: " ", Title: &blankTitle, Version: " "}); got != nil {
		t.Fatalf("FromACPImplementation(blank) = %#v, want nil", got)
	}
	title := " Codex ACP Adapter "
	got := FromACPImplementation(&acp.Implementation{
		Name:    " codex-acp-adapter ",
		Title:   &title,
		Version: " 0.1.0-alpha.28 ",
	})
	if got == nil || got.Name != "codex-acp-adapter" || got.Title != "Codex ACP Adapter" || got.Version != "0.1.0-alpha.28" {
		t.Fatalf("FromACPImplementation() = %#v, want trimmed implementation info", got)
	}
}

func TestBuildACPSetRequest_SelectAndBoolean(t *testing.T) {
	selectReq, err := BuildACPSetRequest(SetConfigOptionRequest{SessionID: "sess", ConfigID: "model", Value: "smart"})
	if err != nil {
		t.Fatalf("select request error: %v", err)
	}
	if selectReq.ValueId == nil || string(selectReq.ValueId.SessionId) != "sess" || string(selectReq.ValueId.ConfigId) != "model" || string(selectReq.ValueId.Value) != "smart" {
		t.Fatalf("select request = %#v", selectReq)
	}

	value := true
	booleanReq, err := BuildACPSetRequest(SetConfigOptionRequest{SessionID: "sess", ConfigID: "auto", BoolValue: &value})
	if err != nil {
		t.Fatalf("boolean request error: %v", err)
	}
	if booleanReq.Boolean == nil || !booleanReq.Boolean.Value || booleanReq.Boolean.Type != ConfigOptionTypeBoolean {
		t.Fatalf("boolean request = %#v", booleanReq)
	}
}

func TestBuildACPSetRequest_Validation(t *testing.T) {
	trueVal := true
	tests := []struct {
		name string
		req  SetConfigOptionRequest
	}{
		{
			name: "missing session",
			req:  SetConfigOptionRequest{ConfigID: "model", Value: "smart"},
		},
		{
			name: "missing config id",
			req:  SetConfigOptionRequest{SessionID: "sess", Value: "smart"},
		},
		{
			name: "missing select value",
			req:  SetConfigOptionRequest{SessionID: "sess", ConfigID: "model"},
		},
		{
			name: "both bool and value supplied",
			req:  SetConfigOptionRequest{SessionID: "sess", ConfigID: "auto", Value: "smart", BoolValue: &trueVal},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := BuildACPSetRequest(tt.req); err == nil {
				t.Fatal("BuildACPSetRequest() error = nil, want validation error")
			}
		})
	}
}

func TestFromACPOptions_PreservesUnknownVariantsAlongsideKnownOptions(t *testing.T) {
	got := FromACPOptions([]acp.SessionConfigOption{
		{},
		{Boolean: &acp.SessionConfigOptionBoolean{
			Id:           acp.SessionConfigId("auto"),
			Name:         "Auto",
			CurrentValue: true,
		}},
	})

	if len(got) != 2 {
		t.Fatalf("FromACPOptions len = %d, want 2", len(got))
	}
	if got[0].Type != ConfigOptionTypeUnknown || got[0].ID != "unknown_1" {
		t.Fatalf("unknown option = %#v", got[0])
	}
	if got[1].ID != "auto" {
		t.Fatalf("got[1].ID = %q, want auto", got[1].ID)
	}
}
