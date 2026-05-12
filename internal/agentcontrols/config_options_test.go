package agentcontrols

import (
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func TestFromACPOptions_NormalizesSelectAndBoolean(t *testing.T) {
	description := "pick a model"
	category := acp.SessionConfigOptionCategoryOther("model")
	selectOptions := acp.SessionConfigSelectOptionsUngrouped{
		{Value: acp.SessionConfigValueId("fast"), Name: "Fast"},
		{Value: acp.SessionConfigValueId("smart"), Name: "Smart", Description: &description},
	}

	got := FromACPOptions([]acp.SessionConfigOption{
		{Select: &acp.SessionConfigOptionSelect{
			Id:           acp.SessionConfigId("model"),
			Name:         "Model",
			Description:  &description,
			Category:     &acp.SessionConfigOptionCategory{Other: &category},
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
	if got[0].ID != "model" || got[0].Category != "model" || got[0].CurrentValue != "fast" || len(got[0].Options) != 2 {
		t.Fatalf("select option = %#v", got[0])
	}
	if got[0].Options[1].Description != description {
		t.Fatalf("option description = %q, want %q", got[0].Options[1].Description, description)
	}
	if got[1].ID != "auto" || got[1].CurrentBool == nil || !*got[1].CurrentBool {
		t.Fatalf("boolean option = %#v", got[1])
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
