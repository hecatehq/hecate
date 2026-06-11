package providerapp

import (
	"context"
	"errors"
	"testing"

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
