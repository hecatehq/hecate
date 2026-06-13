package providerapp

import (
	"context"
	"errors"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
)

type recordingRuntime struct {
	upsertCalls []struct {
		provider controlplane.Provider
		apiKey   string
	}
	rotateCalls []struct {
		id  string
		key string
	}
	deleteCredentialCalls []string
	deleteCalls           []string
	upsertErr             error
	rotateErr             error
	deleteCredentialErr   error
	deleteErr             error
}

func (r *recordingRuntime) Upsert(_ context.Context, provider controlplane.Provider, apiKey string) (controlplane.Provider, error) {
	r.upsertCalls = append(r.upsertCalls, struct {
		provider controlplane.Provider
		apiKey   string
	}{provider: provider, apiKey: apiKey})
	if r.upsertErr != nil {
		return controlplane.Provider{}, r.upsertErr
	}
	return provider, nil
}

func (r *recordingRuntime) RotateSecret(_ context.Context, id, apiKey string) (controlplane.Provider, error) {
	r.rotateCalls = append(r.rotateCalls, struct {
		id  string
		key string
	}{id: id, key: apiKey})
	if r.rotateErr != nil {
		return controlplane.Provider{}, r.rotateErr
	}
	return controlplane.Provider{ID: id, Name: "Updated"}, nil
}

func (r *recordingRuntime) DeleteCredential(_ context.Context, id string) error {
	r.deleteCredentialCalls = append(r.deleteCredentialCalls, id)
	return r.deleteCredentialErr
}

func (r *recordingRuntime) Delete(_ context.Context, id string) error {
	r.deleteCalls = append(r.deleteCalls, id)
	return r.deleteErr
}

func TestApplication_CreateProviderBuildsRuntimeProvider(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := controlplane.NewMemoryStore()
	runtime := &recordingRuntime{}
	app := New(Options{ControlPlane: store, Runtime: runtime})

	result, err := app.CreateProvider(ctx, CreateProviderCommand{
		Name:       "Anthropic",
		CustomName: "Prod",
		PresetID:   "anthropic",
		APIKey:     "sk-ant",
	})
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	if result.Provider.ID != "anthropic-prod" {
		t.Fatalf("provider id = %q, want anthropic-prod", result.Provider.ID)
	}
	if len(runtime.upsertCalls) != 1 {
		t.Fatalf("upsert calls = %d, want 1", len(runtime.upsertCalls))
	}
	call := runtime.upsertCalls[0]
	if call.provider.Kind != "cloud" || call.provider.Protocol != "openai" || !call.provider.Enabled || call.apiKey != "sk-ant" {
		t.Fatalf("upsert call = %+v, want cloud/openai enabled with key", call)
	}
}

func TestApplication_CreateProviderRejectsLocalProviderInCloudRuntimeByDefault(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := controlplane.NewMemoryStore()
	runtime := &recordingRuntime{}
	app := New(Options{
		ControlPlane: store,
		Runtime:      runtime,
		Config: config.Config{Server: config.ServerConfig{
			CloudRuntimeMode: true,
		}},
	})

	_, err := app.CreateProvider(ctx, CreateProviderCommand{
		Name:     "Ollama",
		PresetID: "ollama",
		Kind:     "local",
		Protocol: "openai",
		BaseURL:  "http://127.0.0.1:11434/v1",
	})
	if !errors.Is(err, ErrLocalProvidersDisabled) {
		t.Fatalf("CreateProvider(local cloud) error = %v, want ErrLocalProvidersDisabled", err)
	}
	if len(runtime.upsertCalls) != 0 {
		t.Fatalf("upsert calls = %d, want 0", len(runtime.upsertCalls))
	}
}

func TestApplication_CreateProviderAllowsLocalProviderInCloudRuntimeWithOptIn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := controlplane.NewMemoryStore()
	runtime := &recordingRuntime{}
	app := New(Options{
		ControlPlane: store,
		Runtime:      runtime,
		Config: config.Config{Server: config.ServerConfig{
			CloudRuntimeMode:         true,
			CloudAllowLocalProviders: true,
		}},
	})

	if _, err := app.CreateProvider(ctx, CreateProviderCommand{
		Name:     "Ollama",
		PresetID: "ollama",
		Kind:     "local",
		Protocol: "openai",
		BaseURL:  "http://127.0.0.1:11434/v1",
	}); err != nil {
		t.Fatalf("CreateProvider(local opt-in) error = %v", err)
	}
	if len(runtime.upsertCalls) != 1 {
		t.Fatalf("upsert calls = %d, want 1", len(runtime.upsertCalls))
	}
}

func TestApplication_LocalProvidersAllowedFailsClosedForNilApp(t *testing.T) {
	t.Parallel()

	var app *Application
	if app.localProvidersAllowed() {
		t.Fatal("nil Application localProvidersAllowed() = true, want false")
	}
}

func TestApplication_StatusBuildsProviderRecordsAndPolicyRules(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := controlplane.NewMemoryStore()
	if _, err := store.UpsertProvider(ctx, controlplane.Provider{
		ID:       "anthropic",
		Name:     "Anthropic",
		PresetID: "anthropic",
		Kind:     "cloud",
		Protocol: "openai",
		BaseURL:  "https://api.anthropic.com",
		Enabled:  true,
	}, &controlplane.ProviderSecret{ProviderID: "anthropic", APIKeyEncrypted: "cipher"}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	if _, err := store.UpsertPolicyRule(ctx, config.PolicyRuleConfig{
		ID:     "deny-cloud",
		Action: "deny",
		Reason: "local only",
	}); err != nil {
		t.Fatalf("UpsertPolicyRule: %v", err)
	}
	app := New(Options{ControlPlane: store})

	result, err := app.Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if result.Backend == "" {
		t.Fatal("backend is empty, want control-plane backend")
	}
	if len(result.Providers) != 1 {
		t.Fatalf("providers = %+v, want one provider", result.Providers)
	}
	provider := result.Providers[0]
	if provider.ID != "anthropic" || provider.PresetID != "anthropic" || provider.BaseURL == "" {
		t.Fatalf("provider record = %+v, want preset metadata joined", provider)
	}
	if !provider.CredentialConfigured || provider.CredentialSource != "vault" {
		t.Fatalf("credential fields = %v/%q, want vault credential", provider.CredentialConfigured, provider.CredentialSource)
	}
	if len(result.PolicyRules) != 1 || result.PolicyRules[0].ID != "deny-cloud" {
		t.Fatalf("policy rules = %+v, want deny-cloud", result.PolicyRules)
	}
}

func TestApplication_StatusWithoutControlPlaneReturnsEnvDefaults(t *testing.T) {
	t.Parallel()

	result, err := New(Options{}).Status(context.Background())
	if err != nil {
		t.Fatalf("Status(no control plane) error = %v", err)
	}
	if result.Backend != "env" || len(result.Providers) != 0 || len(result.PolicyRules) != 0 || len(result.Events) != 0 {
		t.Fatalf("result = %+v, want env empty status", result)
	}
}

func TestApplication_CreateProviderRejectsDuplicates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := controlplane.NewMemoryStore()
	if _, err := store.UpsertProvider(ctx, controlplane.Provider{
		ID:      "anthropic",
		Name:    "Anthropic",
		Kind:    "cloud",
		BaseURL: "https://api.anthropic.com",
		Enabled: true,
	}, nil); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	app := New(Options{ControlPlane: store, Runtime: &recordingRuntime{}})
	if _, err := app.CreateProvider(ctx, CreateProviderCommand{Name: "Anthropic"}); !IsConflictError(err) {
		t.Fatalf("CreateProvider(duplicate id) error = %v, want conflict", err)
	}
	if _, err := app.CreateProvider(ctx, CreateProviderCommand{Name: "Other", BaseURL: "https://api.anthropic.com"}); !IsConflictError(err) {
		t.Fatalf("CreateProvider(duplicate base url) error = %v, want conflict", err)
	}
}

func TestApplication_UpsertAndDeletePolicyRule(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := controlplane.NewMemoryStore()
	app := New(Options{ControlPlane: store})

	rule, err := app.UpsertPolicyRule(ctx, PolicyRuleCommand{
		ID:        "deny-cloud",
		Action:    "deny",
		Providers: []string{"anthropic"},
	})
	if err != nil {
		t.Fatalf("UpsertPolicyRule() error = %v", err)
	}
	if rule.ID != "deny-cloud" || rule.Action != "deny" {
		t.Fatalf("rule = %+v, want deny-cloud deny", rule)
	}
	deleted, err := app.DeletePolicyRule(ctx, " deny-cloud ")
	if err != nil {
		t.Fatalf("DeletePolicyRule() error = %v", err)
	}
	if deleted != "deny-cloud" {
		t.Fatalf("deleted id = %q, want deny-cloud", deleted)
	}
	result, err := app.Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if len(result.PolicyRules) != 0 {
		t.Fatalf("policy rules = %+v, want deleted", result.PolicyRules)
	}
}

func TestApplication_PolicyRuleValidationAndDependencies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	if _, err := New(Options{}).UpsertPolicyRule(ctx, PolicyRuleCommand{ID: "x", Action: "deny"}); !errors.Is(err, ErrControlPlaneNotConfigured) {
		t.Fatalf("UpsertPolicyRule(no control plane) error = %v, want ErrControlPlaneNotConfigured", err)
	}
	app := New(Options{ControlPlane: controlplane.NewMemoryStore()})
	if _, err := app.UpsertPolicyRule(ctx, PolicyRuleCommand{ID: "x", Action: ""}); !IsValidationError(err) {
		t.Fatalf("UpsertPolicyRule(invalid) error = %v, want validation", err)
	}
	if _, err := app.DeletePolicyRule(ctx, " "); !IsValidationError(err) {
		t.Fatalf("DeletePolicyRule(empty) error = %v, want validation", err)
	}
}

func TestApplication_UpdateProviderValidatesAndDispatches(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := controlplane.NewMemoryStore()
	if _, err := store.UpsertProvider(ctx, controlplane.Provider{
		ID:      "custom",
		Name:    "Custom",
		Kind:    "local",
		BaseURL: "http://localhost:11434/v1",
		Enabled: true,
	}, nil); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	runtime := &recordingRuntime{}
	app := New(Options{ControlPlane: store, Runtime: runtime})
	name := "Renamed"

	if _, err := app.UpdateProvider(ctx, UpdateProviderCommand{ID: "custom", Name: &name}); err != nil {
		t.Fatalf("UpdateProvider() error = %v", err)
	}
	if len(runtime.upsertCalls) != 1 || runtime.upsertCalls[0].provider.Name != "Renamed" {
		t.Fatalf("upsert calls = %+v, want renamed provider", runtime.upsertCalls)
	}
	if _, err := app.UpdateProvider(ctx, UpdateProviderCommand{ID: "missing", Name: &name}); !IsValidationError(err) {
		t.Fatalf("UpdateProvider(missing) error = %v, want validation", err)
	}
	if _, err := app.UpdateProvider(ctx, UpdateProviderCommand{ID: "custom"}); !IsValidationError(err) {
		t.Fatalf("UpdateProvider(no fields) error = %v, want validation", err)
	}
}

func TestApplication_SetAPIKeyRotatesOrClears(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := controlplane.NewMemoryStore()
	runtime := &recordingRuntime{}
	app := New(Options{ControlPlane: store, Runtime: runtime})

	provider, cleared, err := app.SetAPIKey(ctx, SetAPIKeyCommand{ID: "anthropic", Key: "sk-ant"})
	if err != nil {
		t.Fatalf("SetAPIKey(rotate) error = %v", err)
	}
	if cleared != nil || provider == nil || provider.Provider.ID != "anthropic" {
		t.Fatalf("rotate result provider=%+v cleared=%+v, want provider only", provider, cleared)
	}
	provider, cleared, err = app.SetAPIKey(ctx, SetAPIKeyCommand{ID: "anthropic", Key: ""})
	if err != nil {
		t.Fatalf("SetAPIKey(clear) error = %v", err)
	}
	if provider != nil || cleared == nil || cleared.ID != "anthropic" || cleared.Status != "cleared" {
		t.Fatalf("clear result provider=%+v cleared=%+v, want cleared marker", provider, cleared)
	}
	if len(runtime.rotateCalls) != 1 || runtime.rotateCalls[0].key != "sk-ant" {
		t.Fatalf("rotate calls = %+v, want sk-ant", runtime.rotateCalls)
	}
	if len(runtime.deleteCredentialCalls) != 1 || runtime.deleteCredentialCalls[0] != "anthropic" {
		t.Fatalf("delete credential calls = %+v, want anthropic", runtime.deleteCredentialCalls)
	}
}

func TestApplication_DeleteProviderDispatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runtime := &recordingRuntime{}
	app := New(Options{Runtime: runtime})

	if err := app.DeleteProvider(ctx, " anthropic "); err != nil {
		t.Fatalf("DeleteProvider() error = %v", err)
	}
	if len(runtime.deleteCalls) != 1 || runtime.deleteCalls[0] != "anthropic" {
		t.Fatalf("delete calls = %+v, want trimmed id", runtime.deleteCalls)
	}
}

func TestApplication_StatusFiltersLocalProvidersInCloudRuntimeByDefault(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := controlplane.NewMemoryStore()
	if _, err := store.UpsertProvider(ctx, controlplane.Provider{
		ID:       "ollama",
		Name:     "ollama",
		PresetID: "ollama",
		Kind:     "local",
		Protocol: "openai",
		BaseURL:  "http://127.0.0.1:11434/v1",
		Enabled:  true,
	}, nil); err != nil {
		t.Fatalf("UpsertProvider(local): %v", err)
	}
	if _, err := store.UpsertProvider(ctx, controlplane.Provider{
		ID:       "anthropic",
		Name:     "Anthropic",
		PresetID: "anthropic",
		Kind:     "cloud",
		Protocol: "openai",
		BaseURL:  "https://api.anthropic.com",
		Enabled:  true,
	}, nil); err != nil {
		t.Fatalf("UpsertProvider(cloud): %v", err)
	}
	app := New(Options{
		ControlPlane: store,
		Config: config.Config{Server: config.ServerConfig{
			CloudRuntimeMode: true,
		}},
	})

	result, err := app.Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if len(result.Providers) != 1 || result.Providers[0].ID != "anthropic" {
		t.Fatalf("providers = %#v, want only anthropic", result.Providers)
	}
}

func TestApplication_UpdateProviderRejectsLocalProviderInCloudRuntimeByDefault(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := controlplane.NewMemoryStore()
	if _, err := store.UpsertProvider(ctx, controlplane.Provider{
		ID:       "ollama",
		Name:     "ollama",
		PresetID: "ollama",
		Kind:     "local",
		Protocol: "openai",
		BaseURL:  "http://127.0.0.1:11434/v1",
		Enabled:  true,
	}, nil); err != nil {
		t.Fatalf("UpsertProvider(local): %v", err)
	}
	runtime := &recordingRuntime{}
	baseURL := "http://127.0.0.1:11435/v1"
	app := New(Options{
		ControlPlane: store,
		Runtime:      runtime,
		Config: config.Config{Server: config.ServerConfig{
			CloudRuntimeMode: true,
		}},
	})

	_, err := app.UpdateProvider(ctx, UpdateProviderCommand{ID: "ollama", BaseURL: &baseURL})
	if !errors.Is(err, ErrLocalProvidersDisabled) {
		t.Fatalf("UpdateProvider(local cloud) error = %v, want ErrLocalProvidersDisabled", err)
	}
	if len(runtime.upsertCalls) != 0 {
		t.Fatalf("upsert calls = %d, want 0", len(runtime.upsertCalls))
	}
}

func TestApplication_DependencyAndRuntimeErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	if _, err := New(Options{}).CreateProvider(ctx, CreateProviderCommand{Name: "x"}); !errors.Is(err, ErrControlPlaneNotConfigured) {
		t.Fatalf("CreateProvider(no control plane) error = %v, want ErrControlPlaneNotConfigured", err)
	}
	store := controlplane.NewMemoryStore()
	if _, err := New(Options{ControlPlane: store}).CreateProvider(ctx, CreateProviderCommand{Name: "x"}); !errors.Is(err, ErrRuntimeNotConfigured) {
		t.Fatalf("CreateProvider(no runtime) error = %v, want ErrRuntimeNotConfigured", err)
	}
	runtime := &recordingRuntime{upsertErr: errors.New("runtime rejected")}
	if _, err := New(Options{ControlPlane: store, Runtime: runtime}).CreateProvider(ctx, CreateProviderCommand{Name: "x"}); !IsValidationError(err) {
		t.Fatalf("CreateProvider(runtime error) error = %v, want validation", err)
	}
}
