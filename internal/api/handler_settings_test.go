package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/hecate/agent-runtime/internal/catalog"
	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/controlplane"
	"github.com/hecate/agent-runtime/internal/gateway"
	"github.com/hecate/agent-runtime/internal/governor"
	"github.com/hecate/agent-runtime/internal/profiler"
	"github.com/hecate/agent-runtime/internal/providers"
	"github.com/hecate/agent-runtime/internal/router"
	"github.com/hecate/agent-runtime/internal/telemetry"
)

// fakeProviderRuntime implements api.ProviderRuntime in memory so the
// provider lifecycle handlers can be exercised without the real loader
// plumbing (which pulls in a secret store, a registry rebuild, etc.).
// The fake records the calls so tests can assert on the payload that
// reaches it.
type fakeProviderRuntime struct {
	mu          sync.Mutex
	store       controlplane.Store
	upsertCalls []struct {
		Provider controlplane.Provider
		APIKey   string
	}
	rotateCalls []struct {
		ID  string
		Key string
	}
	deleteCalls     []string
	deleteCredCalls []string
	provider        controlplane.Provider
	upsertErr       error
	rotateErr       error
	deleteErr       error
	deleteCredErr   error
}

func (f *fakeProviderRuntime) Reload(_ context.Context) error { return nil }
func (f *fakeProviderRuntime) SecretStorageEnabled() bool     { return true }
func (f *fakeProviderRuntime) Upsert(ctx context.Context, p controlplane.Provider, key string) (controlplane.Provider, error) {
	f.mu.Lock()
	f.upsertCalls = append(f.upsertCalls, struct {
		Provider controlplane.Provider
		APIKey   string
	}{p, key})
	upsertErr := f.upsertErr
	store := f.store
	f.mu.Unlock()
	if upsertErr != nil {
		return controlplane.Provider{}, upsertErr
	}
	// Mirror production: cloud providers require a key unless one already
	// exists in the store. Without this the create handler would silently
	// accept cloud kind without an api_key.
	if p.Kind == "cloud" && key == "" {
		hasSecret := false
		if store != nil {
			state, _ := store.Snapshot(ctx)
			for _, s := range state.ProviderSecrets {
				if s.ProviderID == p.ID && s.APIKeyEncrypted != "" {
					hasSecret = true
					break
				}
			}
		}
		if !hasSecret {
			return controlplane.Provider{}, errors.New("cloud providers require an api key")
		}
	}
	if store != nil {
		// Mirror runtime_manager: hydrate built-in preset defaults so a
		// cloud preset (e.g. "Anthropic" with no base_url supplied)
		// passes the store's base_url-required check.
		if p.BaseURL == "" {
			// Hydrate by PresetID first (so a "Anthropic Prod" instance with
			// id="anthropic-prod" still inherits the catalog base URL via
			// preset_id="anthropic"), then fall back to the id-as-preset
			// shortcut for legacy single-instance creates.
			lookupID := p.PresetID
			if lookupID == "" {
				lookupID = p.ID
			}
			if builtIn, ok := config.BuiltInProviderByID(lookupID); ok {
				p.BaseURL = builtIn.BaseURL
			}
		}
		var secret *controlplane.ProviderSecret
		if key != "" {
			secret = &controlplane.ProviderSecret{
				ProviderID:      p.ID,
				APIKeyEncrypted: "encrypted:" + key,
				APIKeyPreview:   key,
			}
		}
		saved, err := store.UpsertProvider(ctx, p, secret)
		if err != nil {
			return controlplane.Provider{}, err
		}
		return saved, nil
	}
	if p.ID == "" {
		p.ID = f.provider.ID
	}
	if p.Name == "" {
		p.Name = f.provider.Name
	}
	return p, nil
}
func (f *fakeProviderRuntime) Delete(ctx context.Context, id string) error {
	f.mu.Lock()
	f.deleteCalls = append(f.deleteCalls, id)
	deleteErr := f.deleteErr
	store := f.store
	f.mu.Unlock()
	if deleteErr != nil {
		return deleteErr
	}
	if store != nil {
		return store.DeleteProvider(ctx, id)
	}
	return nil
}

func (f *fakeProviderRuntime) RotateSecret(_ context.Context, id, key string) (controlplane.Provider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rotateCalls = append(f.rotateCalls, struct {
		ID  string
		Key string
	}{id, key})
	if f.rotateErr != nil {
		return controlplane.Provider{}, f.rotateErr
	}
	out := f.provider
	out.ID = id
	return out, nil
}

func (f *fakeProviderRuntime) DeleteCredential(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCredCalls = append(f.deleteCredCalls, id)
	return f.deleteCredErr
}

// Compile-time assertion: the fake satisfies the ProviderRuntime interface.
var _ ProviderRuntime = (*fakeProviderRuntime)(nil)

// newProviderRuntimeTestHandler wires a Handler with a real settings
// store + the fake provider runtime, then returns an admin-authenticated
// client and the fake so tests can assert on what the handler dispatched.
func newProviderRuntimeTestHandler(t *testing.T, runtime ProviderRuntime) (apiTestClient, controlplane.Store) {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	prov := &fakeProvider{name: "openai"}
	registry := providers.NewRegistry(prov)
	providerCatalog := catalog.NewRegistryCatalog(registry, nil)
	store := controlplane.NewMemoryStore()
	// Wire the fake's CP-store handle so Upsert / Delete write through to
	// the same store the handler reads from. Without this, create handlers
	// can't observe their own previous create when checking duplicates.
	if rt, ok := runtime.(*fakeProviderRuntime); ok {
		rt.mu.Lock()
		rt.store = store
		rt.mu.Unlock()
	}
	cfg := config.Config{}
	service := gateway.NewService(gateway.Dependencies{
		Logger:    logger,
		Router:    router.NewRuleRouter("gpt-4o-mini", providerCatalog),
		Catalog:   providerCatalog,
		Governor:  governor.NewStaticGovernor(mergeGovernorDefaults(cfg.Governor), governor.NewMemoryBudgetStore(), governor.NewMemoryBudgetStore()),
		Providers: registry,
		Tracer:    profiler.NewInMemoryTracer(nil),
		Metrics:   telemetry.NewMetrics(),
	})
	handler := NewHandler(cfg, logger, service, store, nil, nil, runtime)
	server := NewServer(logger, handler)
	return newAPITestClient(t, server).withBearerToken("admin-secret"), store
}

func TestSettingsUpdateProviderRequires400WhenRuntimeNotConfigured(t *testing.T) {
	t.Parallel()
	// Pass nil runtime so the handler falls into the
	// `dynamic provider runtime is not configured` branch — this is
	// the in-memory / file-config deployment path the UI must handle
	// gracefully rather than 500-ing.
	admin, _ := newProviderRuntimeTestHandler(t, nil)

	rec := admin.mustRequestStatus(http.StatusBadRequest, http.MethodPatch, "/hecate/v1/settings/providers/anthropic", `{"base_url":"https://example.com/v1"}`)
	if !contains([]string{"invalid_request"}, decodeErrorType(t, rec.Body.Bytes())) {
		t.Errorf("error type = %q, want invalid_request", decodeErrorType(t, rec.Body.Bytes()))
	}
}

func TestSettingsSetProviderAPIKeyRotatesWhenKeyPresent(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{provider: controlplane.Provider{ID: "anthropic", Name: "Anthropic"}}
	admin, _ := newProviderRuntimeTestHandler(t, rt)

	rec := admin.mustRequest(http.MethodPut, "/hecate/v1/settings/providers/anthropic/api-key", `{"key":"sk-ant-new"}`)
	var resp struct {
		Object string                 `json:"object"`
		Data   SettingsProviderRecord `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.Object != "settings_provider_api_key" {
		t.Errorf("object = %q, want settings_provider_api_key", resp.Object)
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.rotateCalls) != 1 {
		t.Fatalf("RotateSecret calls = %d, want 1", len(rt.rotateCalls))
	}
	if rt.rotateCalls[0].ID != "anthropic" || rt.rotateCalls[0].Key != "sk-ant-new" {
		t.Errorf("rotate call = %+v, want anthropic/sk-ant-new", rt.rotateCalls[0])
	}
	if len(rt.deleteCredCalls) != 0 {
		t.Errorf("DeleteCredential called %d times when key was non-empty; want 0", len(rt.deleteCredCalls))
	}
}

func TestSettingsSetProviderAPIKeyClearsWhenKeyEmpty(t *testing.T) {
	t.Parallel()
	// Empty key → DeleteCredential branch. The response contains a
	// {"id": ..., "status": "cleared"} stub rather than a full
	// provider record — the contract the UI relies on for the
	// "API key removed" toast.
	rt := &fakeProviderRuntime{}
	admin, _ := newProviderRuntimeTestHandler(t, rt)

	rec := admin.mustRequest(http.MethodPut, "/hecate/v1/settings/providers/anthropic/api-key", `{"key":""}`)
	var resp struct {
		Object string            `json:"object"`
		Data   map[string]string `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.Data["id"] != "anthropic" || resp.Data["status"] != "cleared" {
		t.Errorf("data = %+v, want {id: anthropic, status: cleared}", resp.Data)
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.deleteCredCalls) != 1 {
		t.Fatalf("DeleteCredential calls = %d, want 1", len(rt.deleteCredCalls))
	}
	if rt.deleteCredCalls[0] != "anthropic" {
		t.Errorf("delete call id = %q, want anthropic", rt.deleteCredCalls[0])
	}
	if len(rt.rotateCalls) != 0 {
		t.Errorf("RotateSecret called %d times when key was empty; want 0", len(rt.rotateCalls))
	}
}

func TestSettingsSetProviderAPIKeySurfacesRuntimeError(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{rotateErr: errors.New("secret store is read-only")}
	admin, _ := newProviderRuntimeTestHandler(t, rt)

	rec := admin.mustRequestStatus(http.StatusBadRequest, http.MethodPut, "/hecate/v1/settings/providers/anthropic/api-key", `{"key":"sk-ant"}`)
	if msg := decodeErrorMessage(t, rec.Body.Bytes()); msg != "secret store is read-only" {
		t.Errorf("error.message = %q, want runtime error verbatim", msg)
	}
}

func TestSettingsSetProviderAPIKeyRequires400WhenRuntimeNotConfigured(t *testing.T) {
	t.Parallel()
	admin, _ := newProviderRuntimeTestHandler(t, nil)

	admin.mustRequestStatus(http.StatusBadRequest, http.MethodPut, "/hecate/v1/settings/providers/anthropic/api-key", `{"key":"sk-ant"}`)
}

// TestSettingsSetProviderAPIKeyRejectsAnonymous proves the auth
// gate fires before any handler-specific logic: a request with no
// bearer must 401, never invoke the runtime. Without this, a
// regression that drops `requireSettings` would open the
// dynamic-runtime endpoints to anyone.

func TestSettingsCreateProvider_Cloud_Success(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{}
	admin, store := newProviderRuntimeTestHandler(t, rt)

	body := `{"name":"Anthropic","kind":"cloud","protocol":"openai","api_key":"sk-ant-test"}`
	rec := admin.mustRequestStatus(http.StatusCreated, http.MethodPost, "/hecate/v1/settings/providers", body)
	var resp struct {
		Object string                 `json:"object"`
		Data   SettingsProviderRecord `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.Data.ID != "anthropic" || resp.Data.Name != "Anthropic" {
		t.Errorf("data = %+v, want id=anthropic name=Anthropic", resp.Data)
	}
	state, _ := store.Snapshot(context.Background())
	if len(state.Providers) != 1 || state.Providers[0].ID != "anthropic" {
		t.Fatalf("store providers = %+v, want 1 record id=anthropic", state.Providers)
	}
}

func TestSettingsCreateProvider_Local_Success(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{}
	admin, store := newProviderRuntimeTestHandler(t, rt)

	body := `{"name":"Ollama","kind":"local","protocol":"openai","base_url":"http://127.0.0.1:11434/v1"}`
	admin.mustRequestStatus(http.StatusCreated, http.MethodPost, "/hecate/v1/settings/providers", body)
	state, _ := store.Snapshot(context.Background())
	if len(state.Providers) != 1 || state.Providers[0].Kind != "local" {
		t.Fatalf("store providers = %+v, want 1 local record", state.Providers)
	}
}

func TestSettingsCreateProvider_NameRequired(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{}
	admin, _ := newProviderRuntimeTestHandler(t, rt)

	rec := admin.mustRequestStatus(http.StatusBadRequest, http.MethodPost, "/hecate/v1/settings/providers", `{"name":"","kind":"cloud","api_key":"sk"}`)
	if msg := decodeErrorMessage(t, rec.Body.Bytes()); msg != "provider name is required" {
		t.Errorf("error.message = %q, want 'provider name is required'", msg)
	}
}

func TestSettingsCreateProvider_DuplicateID(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{}
	admin, _ := newProviderRuntimeTestHandler(t, rt)

	body := `{"name":"Anthropic","kind":"cloud","protocol":"openai","api_key":"sk-1"}`
	admin.mustRequestStatus(http.StatusCreated, http.MethodPost, "/hecate/v1/settings/providers", body)
	rec := admin.mustRequestStatus(http.StatusConflict, http.MethodPost, "/hecate/v1/settings/providers", body)
	if msg := decodeErrorMessage(t, rec.Body.Bytes()); !strings.Contains(msg, "already exists") {
		t.Errorf("error.message = %q, want substring 'already exists'", msg)
	}
}

func TestSettingsCreateProvider_BaseURLConflict(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{}
	admin, _ := newProviderRuntimeTestHandler(t, rt)

	first := `{"name":"Primary","kind":"local","protocol":"openai","base_url":"http://127.0.0.1:11434/v1"}`
	admin.mustRequestStatus(http.StatusCreated, http.MethodPost, "/hecate/v1/settings/providers", first)
	second := `{"name":"Secondary","kind":"local","protocol":"openai","base_url":"http://127.0.0.1:11434/v1"}`
	rec := admin.mustRequestStatus(http.StatusConflict, http.MethodPost, "/hecate/v1/settings/providers", second)
	msg := decodeErrorMessage(t, rec.Body.Bytes())
	if !strings.Contains(msg, "Primary") {
		t.Errorf("error.message = %q, want substring 'Primary' (existing provider name)", msg)
	}
}

func TestSettingsCreateProvider_CloudWithoutKey(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{}
	admin, _ := newProviderRuntimeTestHandler(t, rt)

	rec := admin.mustRequestStatus(http.StatusBadRequest, http.MethodPost, "/hecate/v1/settings/providers", `{"name":"OpenAI","kind":"cloud","protocol":"openai"}`)
	if msg := decodeErrorMessage(t, rec.Body.Bytes()); !strings.Contains(msg, "api key") {
		t.Errorf("error.message = %q, want substring 'api key'", msg)
	}
}

func TestSettingsCreateProvider_SlugifiesName(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{}
	admin, _ := newProviderRuntimeTestHandler(t, rt)

	body := `{"name":"My Custom Provider","kind":"local","protocol":"openai","base_url":"http://127.0.0.1:9999/v1"}`
	rec := admin.mustRequestStatus(http.StatusCreated, http.MethodPost, "/hecate/v1/settings/providers", body)
	var resp struct {
		Data SettingsProviderRecord `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.Data.ID != "my-custom-provider" {
		t.Errorf("data.id = %q, want my-custom-provider", resp.Data.ID)
	}
}

func TestSettingsDeleteProvider_Success(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{}
	admin, store := newProviderRuntimeTestHandler(t, rt)

	body := `{"name":"Ollama","kind":"local","protocol":"openai","base_url":"http://127.0.0.1:11434/v1"}`
	admin.mustRequestStatus(http.StatusCreated, http.MethodPost, "/hecate/v1/settings/providers", body)
	admin.mustRequest(http.MethodDelete, "/hecate/v1/settings/providers/ollama", "")
	state, _ := store.Snapshot(context.Background())
	if len(state.Providers) != 0 {
		t.Fatalf("store providers = %+v, want empty after delete", state.Providers)
	}
}

func TestSettingsDeleteProvider_Unknown(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{}
	admin, _ := newProviderRuntimeTestHandler(t, rt)

	admin.mustRequestStatus(http.StatusBadRequest, http.MethodDelete, "/hecate/v1/settings/providers/nonexistent", "")
}

func TestSettingsUpdateProvider_BaseURL(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{}
	admin, store := newProviderRuntimeTestHandler(t, rt)

	create := `{"name":"Ollama","kind":"local","protocol":"openai","base_url":"http://127.0.0.1:11434/v1"}`
	admin.mustRequestStatus(http.StatusCreated, http.MethodPost, "/hecate/v1/settings/providers", create)
	patch := `{"base_url":"http://192.168.1.10:11434/v1"}`
	admin.mustRequest(http.MethodPatch, "/hecate/v1/settings/providers/ollama", patch)
	state, _ := store.Snapshot(context.Background())
	if len(state.Providers) != 1 || state.Providers[0].BaseURL != "http://192.168.1.10:11434/v1" {
		t.Fatalf("provider base_url = %q, want updated value", state.Providers[0].BaseURL)
	}
}

// TestSettingsUpdateProvider_RenameCustom pins that a custom provider
// (preset_id == "") can be renamed via PATCH. Custom providers are the
// only ones with a free-form name; presets keep their catalog name as
// the join key, so renaming them is rejected.
func TestSettingsUpdateProvider_RenameCustom(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{}
	admin, store := newProviderRuntimeTestHandler(t, rt)

	create := `{"name":"My Local","kind":"local","protocol":"openai","base_url":"http://127.0.0.1:9000/v1"}`
	admin.mustRequestStatus(http.StatusCreated, http.MethodPost, "/hecate/v1/settings/providers", create)
	patch := `{"name":"Workstation"}`
	admin.mustRequest(http.MethodPatch, "/hecate/v1/settings/providers/my-local", patch)
	state, _ := store.Snapshot(context.Background())
	if len(state.Providers) != 1 || state.Providers[0].Name != "Workstation" {
		t.Fatalf("provider name = %q, want 'Workstation'", state.Providers[0].Name)
	}
	// ID is the slugified original name and stays stable — renaming the
	// display name doesn't reslugify the ID, otherwise existing tenant
	// scopes / pricebook entries / audit history would all dangle.
	if state.Providers[0].ID != "my-local" {
		t.Errorf("provider id = %q, want stable 'my-local'", state.Providers[0].ID)
	}
}

// TestSettingsUpdateProvider_RenamePresetRejected pins that a
// preset-based provider's Name is fixed — it's the catalog join key
// (brand color, default base URL, docs link). Operators reach for
// custom_name instead when they need to disambiguate.
func TestSettingsUpdateProvider_RenamePresetRejected(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{}
	admin, _ := newProviderRuntimeTestHandler(t, rt)

	create := `{"name":"Anthropic","preset_id":"anthropic","kind":"cloud","protocol":"openai","api_key":"sk"}`
	admin.mustRequestStatus(http.StatusCreated, http.MethodPost, "/hecate/v1/settings/providers", create)
	rec := admin.mustRequestStatus(http.StatusBadRequest, http.MethodPatch, "/hecate/v1/settings/providers/anthropic", `{"name":"Frank"}`)
	if msg := decodeErrorMessage(t, rec.Body.Bytes()); !strings.Contains(msg, "fixed name") {
		t.Errorf("error.message = %q, want substring 'fixed name'", msg)
	}
}

// TestSettingsUpdateProvider_SetCustomName pins the disambiguator
// path: a preset provider can carry an operator-supplied label that
// the table renders alongside Name to tell instances apart.
func TestSettingsUpdateProvider_SetCustomName(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{}
	admin, store := newProviderRuntimeTestHandler(t, rt)

	create := `{"name":"Anthropic","preset_id":"anthropic","kind":"cloud","protocol":"openai","api_key":"sk"}`
	admin.mustRequestStatus(http.StatusCreated, http.MethodPost, "/hecate/v1/settings/providers", create)
	admin.mustRequest(http.MethodPatch, "/hecate/v1/settings/providers/anthropic", `{"custom_name":"Prod"}`)

	state, _ := store.Snapshot(context.Background())
	if len(state.Providers) != 1 {
		t.Fatalf("providers = %+v, want 1", state.Providers)
	}
	got := state.Providers[0]
	if got.CustomName != "Prod" {
		t.Errorf("custom_name = %q, want 'Prod'", got.CustomName)
	}
	if got.Name != "Anthropic" {
		t.Errorf("name = %q, want unchanged 'Anthropic' (preset name is fixed)", got.Name)
	}
}

// TestSettingsCreateProvider_TwoPresetInstances pins that two
// instances of the same preset can coexist when the second supplies a
// custom_name — the slug includes both, producing distinct ids.
func TestSettingsCreateProvider_TwoPresetInstances(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{}
	admin, store := newProviderRuntimeTestHandler(t, rt)

	first := `{"name":"Anthropic","preset_id":"anthropic","kind":"cloud","protocol":"openai","api_key":"sk-1"}`
	admin.mustRequestStatus(http.StatusCreated, http.MethodPost, "/hecate/v1/settings/providers", first)
	second := `{"name":"Anthropic","preset_id":"anthropic","custom_name":"Prod","kind":"cloud","protocol":"openai","api_key":"sk-2"}`
	admin.mustRequestStatus(http.StatusCreated, http.MethodPost, "/hecate/v1/settings/providers", second)

	state, _ := store.Snapshot(context.Background())
	if len(state.Providers) != 2 {
		t.Fatalf("providers count = %d, want 2", len(state.Providers))
	}
	ids := []string{state.Providers[0].ID, state.Providers[1].ID}
	if !((ids[0] == "anthropic" && ids[1] == "anthropic-prod") || (ids[0] == "anthropic-prod" && ids[1] == "anthropic")) {
		t.Errorf("ids = %v, want set {anthropic, anthropic-prod}", ids)
	}
}

// TestSettingsUpdateProvider_NoFields rejects an empty PATCH body —
// the handler used to read base_url as a required string but now both
// fields are optional pointers, and "neither supplied" must still be a
// 400 instead of silently no-op'ing through Upsert.
func TestSettingsUpdateProvider_NoFields(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{}
	admin, _ := newProviderRuntimeTestHandler(t, rt)

	create := `{"name":"Ollama","kind":"local","protocol":"openai","base_url":"http://127.0.0.1:11434/v1"}`
	admin.mustRequestStatus(http.StatusCreated, http.MethodPost, "/hecate/v1/settings/providers", create)
	rec := admin.mustRequestStatus(http.StatusBadRequest, http.MethodPatch, "/hecate/v1/settings/providers/ollama", `{}`)
	if msg := decodeErrorMessage(t, rec.Body.Bytes()); !strings.Contains(msg, "no fields to update") {
		t.Errorf("error.message = %q, want substring 'no fields to update'", msg)
	}
}

// TestSettingsUpdateProvider_BaseURL_PropagatesToRuntime pins that a
// PATCH base_url update goes through Upsert (which calls Reload), so the
// runtime registry actually swaps to the new endpoint instead of the
// store and the runtime drifting apart.
func TestSettingsUpdateProvider_BaseURL_PropagatesToRuntime(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{}
	admin, _ := newProviderRuntimeTestHandler(t, rt)

	create := `{"name":"Ollama","kind":"local","protocol":"openai","base_url":"http://127.0.0.1:11434/v1"}`
	admin.mustRequestStatus(http.StatusCreated, http.MethodPost, "/hecate/v1/settings/providers", create)
	patch := `{"base_url":"http://192.168.1.10:11434/v1"}`
	admin.mustRequest(http.MethodPatch, "/hecate/v1/settings/providers/ollama", patch)

	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.upsertCalls) != 2 {
		t.Fatalf("Upsert call count = %d, want 2 (create + patch)", len(rt.upsertCalls))
	}
	last := rt.upsertCalls[len(rt.upsertCalls)-1].Provider
	if last.BaseURL != "http://192.168.1.10:11434/v1" {
		t.Errorf("runtime received base_url = %q, want updated value", last.BaseURL)
	}
}

// TestBuildSettingsProviderList_EmptyStore confirms the list endpoint
// returns no records when no provider has been added — the new "explicit
// add" model. Before the redesign, this returned one record per built-in
// preset; that behavior is gone and shouldn't regress.
func TestBuildSettingsProviderList_EmptyStore(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{}
	admin, _ := newProviderRuntimeTestHandler(t, rt)

	rec := admin.mustRequest(http.MethodGet, "/hecate/v1/settings", "")
	var resp struct {
		Data struct {
			Providers []SettingsProviderRecord `json:"providers"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(resp.Data.Providers) != 0 {
		t.Errorf("providers = %+v, want empty list on a fresh store", resp.Data.Providers)
	}
}

// TestBuildSettingsProviderList_PresetMetadataJoin pins that a record
// created via a preset_id has its kind / base_url / protocol filled in
// from the preset catalog when the operator didn't override them. The UI
// renders these fields directly so a regression here would mean rows
// missing brand color, protocol label, or endpoint text.
func TestBuildSettingsProviderList_PresetMetadataJoin(t *testing.T) {
	t.Parallel()
	rt := &fakeProviderRuntime{}
	admin, _ := newProviderRuntimeTestHandler(t, rt)

	body := `{"name":"Anthropic","preset_id":"anthropic","kind":"cloud","protocol":"openai","api_key":"sk-ant-test"}`
	admin.mustRequestStatus(http.StatusCreated, http.MethodPost, "/hecate/v1/settings/providers", body)

	rec := admin.mustRequest(http.MethodGet, "/hecate/v1/settings", "")
	var resp struct {
		Data struct {
			Providers []SettingsProviderRecord `json:"providers"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(resp.Data.Providers) != 1 {
		t.Fatalf("providers length = %d, want 1", len(resp.Data.Providers))
	}
	got := resp.Data.Providers[0]
	if got.PresetID != "anthropic" {
		t.Errorf("preset_id = %q, want 'anthropic'", got.PresetID)
	}
	if got.BaseURL == "" {
		t.Errorf("base_url empty, want preset default to be joined in")
	}
	if got.Kind != "cloud" {
		t.Errorf("kind = %q, want 'cloud'", got.Kind)
	}
	if !got.CredentialConfigured {
		t.Errorf("credential_configured = false, want true after a key was supplied at create time")
	}
}

func TestSettingsDeletePolicyRule_UsesPathID(t *testing.T) {
	t.Parallel()
	admin, _ := newProviderRuntimeTestHandler(t, nil)

	admin.mustRequest(http.MethodPost, "/hecate/v1/settings/policy-rules", `{"id":"deny-cloud","action":"deny","reason":"local only"}`)

	rec := admin.mustRequest(http.MethodDelete, "/hecate/v1/settings/policy-rules/deny-cloud", "")
	var deleted struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&deleted); err != nil {
		t.Fatalf("decode delete policy response: %v", err)
	}
	if deleted.Data.ID != "deny-cloud" {
		t.Fatalf("deleted id = %q, want deny-cloud", deleted.Data.ID)
	}

	status := admin.mustRequest(http.MethodGet, "/hecate/v1/settings", "")
	var snapshot struct {
		Data struct {
			PolicyRules []SettingsPolicyRuleRecord `json:"policy_rules"`
		} `json:"data"`
	}
	if err := json.NewDecoder(status.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode settings response: %v", err)
	}
	if len(snapshot.Data.PolicyRules) != 0 {
		t.Fatalf("policy_rules = %+v, want empty after delete", snapshot.Data.PolicyRules)
	}
}

func TestSettingsDeletePricebookEntry_UsesPathProviderAndModel(t *testing.T) {
	t.Parallel()
	admin, _ := newProviderRuntimeTestHandler(t, nil)

	admin.mustRequest(http.MethodPost, "/hecate/v1/settings/pricebook", `{
		"provider":"mistral",
		"model":"ministral-3:latest",
		"input_micros_usd_per_million_tokens":100,
		"output_micros_usd_per_million_tokens":200
	}`)

	rec := admin.mustRequest(http.MethodDelete, "/hecate/v1/settings/pricebook/mistral/ministral-3:latest", "")
	var deleted struct {
		Data struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&deleted); err != nil {
		t.Fatalf("decode delete pricebook response: %v", err)
	}
	if deleted.Data.Provider != "mistral" || deleted.Data.Model != "ministral-3:latest" {
		t.Fatalf("deleted = %+v, want mistral/ministral-3:latest", deleted.Data)
	}

	status := admin.mustRequest(http.MethodGet, "/hecate/v1/settings", "")
	var snapshot struct {
		Data struct {
			Pricebook []SettingsPricebookRecord `json:"pricebook"`
		} `json:"data"`
	}
	if err := json.NewDecoder(status.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode settings response: %v", err)
	}
	if len(snapshot.Data.Pricebook) != 0 {
		t.Fatalf("pricebook = %+v, want empty after delete", snapshot.Data.Pricebook)
	}
}

// decodeErrorType / decodeErrorMessage extract fields from the standard
// {"error":{"type":..., "message":...}} envelope. Inline since each
// test uses them once or twice.
func decodeErrorType(t *testing.T, body []byte) string {
	t.Helper()
	var payload struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return payload.Error.Type
}

func decodeErrorMessage(t *testing.T, body []byte) string {
	t.Helper()
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return payload.Error.Message
}
