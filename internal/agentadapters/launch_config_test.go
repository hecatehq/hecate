package agentadapters

import (
	"context"
	"testing"

	"github.com/hecate/agent-runtime/internal/agentcontrols"
)

func TestLaunchConfig_AppendsGrokModelOptionWithUnsetSelection(t *testing.T) {
	adapter, ok := BuiltInByID("grok_build")
	if !ok {
		t.Fatal("grok_build adapter not found")
	}
	got, managed := appendLaunchConfigOptions(context.Background(), "", adapter, nil, nil)
	if len(got) != 2 {
		t.Fatalf("options = %#v, want model and reasoning launch options", got)
	}
	if _, ok := managed["model"]; !ok {
		t.Fatalf("managed config ids = %#v, want model", managed)
	}
	if _, ok := managed["reasoning_effort"]; !ok {
		t.Fatalf("managed config ids = %#v, want reasoning_effort", managed)
	}
	option := got[0]
	if option.ID != "model" || option.Category != "model" || option.CurrentValue != launchModelUnsetValue {
		t.Fatalf("launch model option = %#v, want model category with unset current", option)
	}
	if len(option.Options) != 1 || option.Options[0].Value != launchModelUnsetValue {
		t.Fatalf("model candidates = %#v, want unset option only without discovery", option.Options)
	}
	if option.Options[0].Name != "Pick a model" {
		t.Fatalf("unset option name = %q, want Pick a model", option.Options[0].Name)
	}
	reasoning := got[1]
	if reasoning.ID != "reasoning_effort" || reasoning.Category != "thought_level" {
		t.Fatalf("reasoning option = %#v, want thought_level launch option", reasoning)
	}
	if len(reasoning.Options) != 6 || reasoning.Options[0].Name != "Pick reasoning" {
		t.Fatalf("reasoning candidates = %#v, want unset plus levels", reasoning.Options)
	}
}

func TestLaunchConfig_UsesBaseArgsUntilModelSelected(t *testing.T) {
	adapter, ok := BuiltInByID("grok_build")
	if !ok {
		t.Fatal("grok_build adapter not found")
	}
	got := adapterWithLaunchConfig(adapter, []agentcontrols.ConfigOption{{
		ID:           "model",
		CurrentValue: launchModelUnsetValue,
	}})
	want := []string{"agent", "stdio"}
	if !sameArgs(got.Args, want) {
		t.Fatalf("args = %#v, want %#v", got.Args, want)
	}
}

func TestLaunchConfig_RequiresExplicitModelSelection(t *testing.T) {
	adapter, ok := BuiltInByID("grok_build")
	if !ok {
		t.Fatal("grok_build adapter not found")
	}
	if err := validateLaunchConfig(adapter, nil); err == nil {
		t.Fatal("validateLaunchConfig accepted a missing launch model")
	}
	if err := validateLaunchConfig(adapter, []agentcontrols.ConfigOption{{ID: "model", CurrentValue: launchModelUnsetValue}}); err == nil {
		t.Fatal("validateLaunchConfig accepted an unset launch model")
	}
	if err := validateLaunchConfig(adapter, []agentcontrols.ConfigOption{{ID: "model", CurrentValue: "model-a"}}); err != nil {
		t.Fatalf("validateLaunchConfig returned error with selected model: %v", err)
	}
}

func TestLaunchConfig_UsesSelectedModelInArgs(t *testing.T) {
	adapter, ok := BuiltInByID("grok_build")
	if !ok {
		t.Fatal("grok_build adapter not found")
	}
	got := adapterWithLaunchConfig(adapter, []agentcontrols.ConfigOption{{
		ID:           "model",
		CurrentValue: "model-a",
	}})
	want := []string{"agent", "--model", "model-a", "stdio"}
	if !sameArgs(got.Args, want) {
		t.Fatalf("args = %#v, want %#v", got.Args, want)
	}
}

func TestLaunchConfig_UsesSelectedReasoningInArgs(t *testing.T) {
	adapter, ok := BuiltInByID("grok_build")
	if !ok {
		t.Fatal("grok_build adapter not found")
	}
	got := adapterWithLaunchConfig(adapter, []agentcontrols.ConfigOption{
		{ID: "model", CurrentValue: "model-a"},
		{ID: "reasoning_effort", CurrentValue: "high"},
	})
	want := []string{"agent", "--model", "model-a", "--reasoning-effort", "high", "stdio"}
	if !sameArgs(got.Args, want) {
		t.Fatalf("args = %#v, want %#v", got.Args, want)
	}
}

func TestLaunchConfig_DoesNotAppendModelWhenACPAlreadyProvidesModel(t *testing.T) {
	adapter, ok := BuiltInByID("grok_build")
	if !ok {
		t.Fatal("grok_build adapter not found")
	}
	options := []agentcontrols.ConfigOption{{
		ID:       "native_model",
		Name:     "Native Model",
		Category: "model",
		Type:     agentcontrols.ConfigOptionTypeSelect,
	}}
	got, managed := appendLaunchConfigOptions(context.Background(), "", adapter, options, nil)
	if len(got) != 2 || got[0].ID != "native_model" || got[1].ID != "reasoning_effort" {
		t.Fatalf("options = %#v, want native model plus managed reasoning", got)
	}
	if len(managed) != 1 {
		t.Fatalf("managed config ids = %#v, want reasoning only", managed)
	}
	if _, ok := managed["reasoning_effort"]; !ok {
		t.Fatalf("managed config ids = %#v, want reasoning_effort", managed)
	}
}

func TestLaunchConfig_ParsesCLIModelLists(t *testing.T) {
	raw := `
You are logged in with grok.com.

Default model: model-a

Available models:
  * model-a (default)
  - model-b
auto - Auto
model-c - Model C
Tip: use --model <id>
`
	got := parseLaunchModelList(raw)
	wantIDs := []string{"model-a", "model-b", "auto", "model-c"}
	if len(got) != len(wantIDs) {
		t.Fatalf("models = %#v, want %d ids", got, len(wantIDs))
	}
	for i, want := range wantIDs {
		if got[i].ID != want {
			t.Fatalf("model[%d] = %#v, want id %q", i, got[i], want)
		}
	}
	if got[3].Name != "Model C" {
		t.Fatalf("cursor-style model name = %q, want Model C", got[3].Name)
	}
}

func TestACPSessionManagedConfigOptionUpdatesSnapshot(t *testing.T) {
	session := &acpSession{
		adapter:       Adapter{Name: "Grok Build"},
		managedConfig: map[string]struct{}{"model": {}},
		configOptions: []agentcontrols.ConfigOption{{
			ID:           "model",
			Name:         "Model",
			Category:     "model",
			Type:         agentcontrols.ConfigOptionTypeSelect,
			CurrentValue: launchModelUnsetValue,
			Options: []agentcontrols.ConfigSelectOption{
				{Value: launchModelUnsetValue, Name: "Pick a model"},
				{Value: "model-a", Name: "Model A"},
			},
		}},
	}
	got, err := session.SetManagedConfigOption(SetSessionConfigOptionRequest{
		ConfigID: "model",
		Value:    "model-a",
	})
	if err != nil {
		t.Fatalf("SetManagedConfigOption returned error: %v", err)
	}
	if got.ConfigOptions[0].CurrentValue != "model-a" {
		t.Fatalf("result options = %#v, want selected model", got.ConfigOptions)
	}
	if session.configOptionsSnapshot()[0].CurrentValue != "model-a" {
		t.Fatalf("session snapshot = %#v, want selected model", session.configOptionsSnapshot())
	}
	if _, err := session.SetManagedConfigOption(SetSessionConfigOptionRequest{ConfigID: "model", Value: "missing"}); err == nil {
		t.Fatal("SetManagedConfigOption accepted an unavailable model")
	}
}
