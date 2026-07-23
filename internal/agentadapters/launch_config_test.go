package agentadapters

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/agentcontrols"
)

func TestLaunchConfig_GrokUsesACPControlsOnly(t *testing.T) {
	adapter, ok := BuiltInByID("grok_build")
	if !ok {
		t.Fatal("grok_build adapter not found")
	}
	got, managed := appendLaunchConfigOptions(context.Background(), "", adapter, nil, nil)
	if len(got) != 0 {
		t.Fatalf("options = %#v, want no Hecate-managed launch options", got)
	}
	if len(managed) != 0 {
		t.Fatalf("managed config ids = %#v, want none", managed)
	}
	if _, ok := LaunchConfigOptionForSet("grok_build", "model", "grok-latest"); ok {
		t.Fatal("LaunchConfigOptionForSet(model) = true, want Grok model owned by ACP")
	}
	if _, ok := LaunchConfigOptionForSet("grok_build", "reasoning_effort", "high"); ok {
		t.Fatal("LaunchConfigOptionForSet(reasoning_effort) = true, want ACP-owned controls only")
	}
}

func TestLaunchConfig_UsesBaseArgsWithoutGrokReasoning(t *testing.T) {
	adapter, ok := BuiltInByID("grok_build")
	if !ok {
		t.Fatal("grok_build adapter not found")
	}
	got := adapterWithLaunchConfig(adapter, nil)
	want := []string{"agent", "stdio"}
	if !sameArgs(got.Args, want) {
		t.Fatalf("args = %#v, want %#v", got.Args, want)
	}
}

func TestLaunchConfig_DoesNotRequireGrokLaunchModelSelection(t *testing.T) {
	adapter, ok := BuiltInByID("grok_build")
	if !ok {
		t.Fatal("grok_build adapter not found")
	}
	if err := validateLaunchConfig(adapter, nil); err != nil {
		t.Fatalf("validateLaunchConfig returned error without Grok launch model: %v", err)
	}
}

func TestLaunchConfig_BuiltInsDoNotRequireOptionalDiagnosticsBeforeSessionCreation(t *testing.T) {
	for _, adapter := range BuiltIns() {
		if err := validateLaunchConfig(adapter, nil); err != nil {
			t.Fatalf("validateLaunchConfig(%s) = %v; required pre-session controls need a passive schema before a built-in can adopt them", adapter.ID, err)
		}
	}
}

func TestLaunchConfig_IgnoresGrokACPModelInLaunchArgs(t *testing.T) {
	adapter, ok := BuiltInByID("grok_build")
	if !ok {
		t.Fatal("grok_build adapter not found")
	}
	got := adapterWithLaunchConfig(adapter, []agentcontrols.ConfigOption{{
		ID:           "model",
		CurrentValue: "model-a",
	}})
	want := []string{"agent", "stdio"}
	if !sameArgs(got.Args, want) {
		t.Fatalf("args = %#v, want %#v", got.Args, want)
	}
}

func TestLaunchConfig_DoesNotAppendModelWhenACPAlreadyProvidesModel(t *testing.T) {
	adapter := Adapter{
		ID:   "custom",
		Name: "Custom",
		LaunchModel: LaunchModelConfig{
			ConfigID:    "model",
			ArgTemplate: []string{"--model", "{model}"},
		},
		LaunchOptions: []LaunchSelectConfig{{
			ConfigID:    "reasoning_effort",
			Name:        "Reasoning",
			Category:    "thought_level",
			ArgTemplate: []string{"--reasoning", "{reasoning_effort}"},
			Options:     []LaunchSelectOption{{ID: "high", Name: "High"}},
		}},
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
model-c - Model C (default)
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
	if !got[0].Default {
		t.Fatalf("model[0].Default = false, want true for the CLI default")
	}
	if !got[3].Default {
		t.Fatalf("model[3].Default = false, want true for inline default marker")
	}
	for _, i := range []int{1, 2} {
		if got[i].Default {
			t.Fatalf("model[%d].Default = true, want false", i)
		}
	}
}

func TestLaunchConfig_SelectsDiscoveredDefaultModel(t *testing.T) {
	resetLaunchDiscoveryCacheForTest(t)
	t.Setenv("XAI_LAUNCH_CONFIG_HELPER", "1")
	countFile := filepath.Join(t.TempDir(), "count")
	adapter := Adapter{
		ID:   "grok_build",
		Name: "Grok Build",
		Args: []string{"-test.run=TestLaunchConfigHelperProcess", "--", "agent", countFile},
		LaunchModel: LaunchModelConfig{
			ArgTemplate: []string{"--model", "{model}"},
			ListArgs:    []string{"-test.run=TestLaunchConfigHelperProcess", "--", "models-default", countFile},
		},
	}

	got, managed := appendLaunchConfigOptions(context.Background(), os.Args[0], adapter, nil, nil)
	if _, ok := managed["model"]; !ok {
		t.Fatalf("managed config ids = %#v, want model", managed)
	}
	if len(got) != 1 {
		t.Fatalf("options = %#v, want one model option", got)
	}
	if got[0].CurrentValue != "model-a" {
		t.Fatalf("current model = %q, want discovered default model-a", got[0].CurrentValue)
	}
	if len(got[0].Options) != 3 || got[0].Options[1].Value != "model-a" || got[0].Options[2].Value != "model-b" {
		t.Fatalf("model candidates = %#v, want unset plus discovered models", got[0].Options)
	}
}

func TestLaunchConfig_PreservesExplicitUnsetSelection(t *testing.T) {
	resetLaunchDiscoveryCacheForTest(t)
	t.Setenv("XAI_LAUNCH_CONFIG_HELPER", "1")
	countFile := filepath.Join(t.TempDir(), "count")
	adapter := Adapter{
		ID:   "grok_build",
		Name: "Grok Build",
		Args: []string{"-test.run=TestLaunchConfigHelperProcess", "--", "agent", countFile},
		LaunchModel: LaunchModelConfig{
			ArgTemplate: []string{"--model", "{model}"},
			ListArgs:    []string{"-test.run=TestLaunchConfigHelperProcess", "--", "models-default", countFile},
		},
	}

	got, _ := appendLaunchConfigOptions(context.Background(), os.Args[0], adapter, nil, []agentcontrols.ConfigOption{{
		ID:           "model",
		CurrentValue: launchModelUnsetValue,
	}})
	if len(got) != 1 {
		t.Fatalf("options = %#v, want one model option", got)
	}
	if got[0].CurrentValue != launchModelUnsetValue {
		t.Fatalf("current model = %q, want explicit unset selection", got[0].CurrentValue)
	}
}

func TestLaunchConfig_CachesLaunchDiscovery(t *testing.T) {
	resetLaunchDiscoveryCacheForTest(t)
	t.Setenv("XAI_LAUNCH_CONFIG_HELPER", "1")
	countFile := filepath.Join(t.TempDir(), "count")
	adapter := Adapter{
		ID:   "grok_build",
		Name: "Grok Build",
		Args: []string{"-test.run=TestLaunchConfigHelperProcess", "--", "agent", countFile},
		LaunchModel: LaunchModelConfig{
			ArgTemplate: []string{"--model", "{model}"},
			ListArgs:    []string{"-test.run=TestLaunchConfigHelperProcess", "--", "models", countFile},
		},
	}

	for i := 0; i < 2; i++ {
		got, managed := appendLaunchConfigOptions(context.Background(), os.Args[0], adapter, nil, nil)
		if len(got) != 1 {
			t.Fatalf("options on iteration %d = %#v, want model option", i, got)
		}
		if _, ok := managed["model"]; !ok {
			t.Fatalf("managed config ids on iteration %d = %#v, want model", i, managed)
		}
		if len(got[0].Options) != 2 || got[0].Options[1].Value != "model-a" {
			t.Fatalf("model candidates on iteration %d = %#v, want discovered model", i, got[0].Options)
		}
	}

	raw, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatalf("read helper count: %v", err)
	}
	if got := strings.Count(string(raw), "\n"); got != 2 {
		t.Fatalf("helper invocations = %d, want one help and one model-list command", got)
	}
}

func TestLaunchConfigHelperProcess(t *testing.T) {
	if os.Getenv("XAI_LAUNCH_CONFIG_HELPER") != "1" {
		return
	}
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || separator+2 >= len(os.Args) {
		os.Exit(2)
	}
	mode := os.Args[separator+1]
	countFile := os.Args[separator+2]
	recordLaunchHelperCall(countFile)
	switch mode {
	case "agent":
		fmt.Println("Usage: fake agent --model <MODEL>")
	case "models":
		fmt.Println("Available models:")
		fmt.Println("model-a - Model A")
	case "models-default":
		fmt.Println("Default model: model-a")
		fmt.Println("Available models:")
		fmt.Println("* model-a (default)")
		fmt.Println("- model-b")
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func recordLaunchHelperCall(path string) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		os.Exit(2)
	}
	defer file.Close()
	if _, err := file.WriteString("x\n"); err != nil {
		os.Exit(2)
	}
}

func resetLaunchDiscoveryCacheForTest(t *testing.T) {
	t.Helper()
	launchDiscoveryCache.Lock()
	launchDiscoveryCache.items = map[launchDiscoveryCacheKey]launchDiscoveryCacheEntry{}
	launchDiscoveryCache.Unlock()
	t.Cleanup(func() {
		launchDiscoveryCache.Lock()
		launchDiscoveryCache.items = map[launchDiscoveryCacheKey]launchDiscoveryCacheEntry{}
		launchDiscoveryCache.Unlock()
	})
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
