package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/llamacpp"
)

// makeHandlerWithLocalModels wires a bare Handler with just the
// localModels field set, since the full NewHandler constructor takes
// nine dependencies most local-models tests don't care about.
func makeHandlerWithLocalModels(t *testing.T, svc *llamacpp.Service) *Handler {
	t.Helper()
	h := &Handler{}
	if svc != nil {
		h.SetLocalModelsService(svc)
	}
	return h
}

// makeRealService stands up a real llamacpp.Service against a memory
// controlplane store and a tmp dataDir. Useful for the happy-path
// handler tests; the heavyweight installer/runtime tests live in the
// llamacpp package itself.
func makeRealService(t *testing.T) (*llamacpp.Service, string, *controlplane.MemoryStore) {
	t.Helper()
	dir := t.TempDir()
	// Fake binary so FeatureAvailability reports Available=true.
	bin := filepath.Join(t.TempDir(), "llama-server")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	store := controlplane.NewMemoryStore()
	svc, err := llamacpp.NewService(llamacpp.ServiceOptions{
		BinaryPath: bin,
		DataDir:    dir,
		Store:      store,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, dir, store
}

func TestHandleLocalModelsCatalog_Dormant503(t *testing.T) {
	t.Parallel()
	h := makeHandlerWithLocalModels(t, nil)
	rec := httptest.NewRecorder()
	h.HandleLocalModelsCatalog(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/local-models/catalog", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), errCodeLocalModelsUnavailable) {
		t.Fatalf("body missing code: %q", rec.Body.String())
	}
}

// TestHandleLocalModelsCatalog_DormantOnUnusableBinary covers the
// reviewer-flagged case: HECATE_LOCAL_MODELS=on but no usable binary
// resolves. The service is wired but FeatureAvailability().Available
// is false. Non-runtime handlers must still 503 — otherwise the
// dormant invariant promised in docs/local-models.md breaks.
func TestHandleLocalModelsCatalog_DormantOnUnusableBinary(t *testing.T) {
	t.Parallel()
	store := controlplane.NewMemoryStore()
	svc, err := llamacpp.NewService(llamacpp.ServiceOptions{
		// BinaryPath empty — service wired but feature dormant.
		DataDir: t.TempDir(),
		Store:   store,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	h := makeHandlerWithLocalModels(t, svc)
	rec := httptest.NewRecorder()
	h.HandleLocalModelsCatalog(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/local-models/catalog", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503 (body=%q)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), errCodeLocalModelsUnavailable) {
		t.Fatalf("body missing %q: %q", errCodeLocalModelsUnavailable, rec.Body.String())
	}
}

// TestHandleLocalModelsRuntimeStatus_DormantOnUnusableBinary confirms
// the introspection handler still returns 200 + availability=false
// when the binary is unresolved — operators need this body to know
// *why* the feature is dormant.
func TestHandleLocalModelsRuntimeStatus_DormantOnUnusableBinary(t *testing.T) {
	t.Parallel()
	store := controlplane.NewMemoryStore()
	svc, err := llamacpp.NewService(llamacpp.ServiceOptions{
		DataDir: t.TempDir(),
		Store:   store,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	h := makeHandlerWithLocalModels(t, svc)
	rec := httptest.NewRecorder()
	h.HandleLocalModelsRuntimeStatus(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/local-models/runtime", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"available":false`) {
		t.Fatalf("body missing dormant marker: %q", rec.Body.String())
	}
}

func TestHandleLocalModelsCatalog_ReturnsCuratedList(t *testing.T) {
	t.Parallel()
	svc, _, _ := makeRealService(t)
	h := makeHandlerWithLocalModels(t, svc)
	rec := httptest.NewRecorder()
	h.HandleLocalModelsCatalog(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/local-models/catalog", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	var resp LocalModelsCatalogResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Object != "local_models.catalog" {
		t.Fatalf("object = %q; want local_models.catalog", resp.Object)
	}
	if len(resp.Data) == 0 {
		t.Fatal("expected non-empty catalog")
	}
	// Every entry must have a non-empty ID; the `installed` flag
	// must be false because we haven't installed anything in this
	// test.
	for _, e := range resp.Data {
		if e.ID == "" {
			t.Fatalf("entry has empty id: %+v", e)
		}
		if e.Installed {
			t.Fatalf("entry %q reports installed on a fresh service", e.ID)
		}
	}
}

func TestHandleLocalModelsInstalled_EmptyOnFresh(t *testing.T) {
	t.Parallel()
	svc, _, _ := makeRealService(t)
	h := makeHandlerWithLocalModels(t, svc)
	rec := httptest.NewRecorder()
	h.HandleLocalModelsInstalled(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/local-models/installed", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%q", rec.Code, rec.Body.String())
	}
	var resp LocalModelsInstalledResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 0 {
		t.Fatalf("Data on fresh service should be empty; got %+v", resp.Data)
	}
}

func TestHandleLocalModelsInstalled_ReflectsRegistry(t *testing.T) {
	t.Parallel()
	svc, dir, store := makeRealService(t)
	h := makeHandlerWithLocalModels(t, svc)

	// Seed a registry row with a real file on disk so ListInstalled's
	// boot reconciliation keeps it.
	abs := filepath.Join(dir, "models", "round-trip.gguf")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte("gguf"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := store.UpsertInstalledModel(context.Background(), llamacpp.InstalledModel{
		ID:       "round-trip",
		FilePath: "models/round-trip.gguf",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rec := httptest.NewRecorder()
	h.HandleLocalModelsInstalled(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/local-models/installed", nil))
	var resp LocalModelsInstalledResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Data) != 1 || resp.Data[0].ID != "round-trip" {
		t.Fatalf("expected one installed row; got %+v", resp.Data)
	}
}

func TestHandleLocalModelsRuntimeStatus_DormantSurfacesAvailability(t *testing.T) {
	t.Parallel()
	// Dormant build: handler must NOT 503 — instead it returns an
	// availability=false response the UI can render directly.
	h := makeHandlerWithLocalModels(t, nil)
	rec := httptest.NewRecorder()
	h.HandleLocalModelsRuntimeStatus(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/local-models/runtime", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (dormant path)", rec.Code)
	}
	var resp LocalModelsRuntimeResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Available {
		t.Fatalf("dormant build should report available=false")
	}
	if resp.Reason != "binary_not_found" {
		t.Fatalf("reason = %q; want binary_not_found", resp.Reason)
	}
}

func TestHandleLocalModelsRuntimeStatus_AvailableWhenWired(t *testing.T) {
	t.Parallel()
	svc, _, _ := makeRealService(t)
	h := makeHandlerWithLocalModels(t, svc)
	rec := httptest.NewRecorder()
	h.HandleLocalModelsRuntimeStatus(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/local-models/runtime", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp LocalModelsRuntimeResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Available {
		t.Fatalf("expected available=true; got %+v", resp)
	}
	if resp.State != llamacpp.RuntimeIdle {
		t.Fatalf("state = %q; want idle", resp.State)
	}
}

func TestHandleLocalModelsInstall_RejectsEmptySpec(t *testing.T) {
	t.Parallel()
	svc, _, _ := makeRealService(t)
	h := makeHandlerWithLocalModels(t, svc)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/local-models/install",
		bytes.NewBufferString(`{}`))
	h.HandleLocalModelsInstall(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400 (body=%q)", rec.Code, rec.Body.String())
	}
}

func TestHandleLocalModelsInstall_RejectsUnknownCatalogID(t *testing.T) {
	t.Parallel()
	svc, _, _ := makeRealService(t)
	h := makeHandlerWithLocalModels(t, svc)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/local-models/install",
		bytes.NewBufferString(`{"catalog_id":"not-a-real-model"}`))
	h.HandleLocalModelsInstall(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rec.Code)
	}
}

func TestHandleLocalModelsRuntimeStop_IdempotentOnIdle(t *testing.T) {
	t.Parallel()
	svc, _, _ := makeRealService(t)
	h := makeHandlerWithLocalModels(t, svc)
	rec := httptest.NewRecorder()
	h.HandleLocalModelsRuntimeStop(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/local-models/runtime/stop", bytes.NewBufferString(`{}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandleLocalModelsUninstall_RejectsMissingID(t *testing.T) {
	t.Parallel()
	svc, _, _ := makeRealService(t)
	h := makeHandlerWithLocalModels(t, svc)
	rec := httptest.NewRecorder()
	// PathValue empty when the route hasn't matched a wildcard;
	// simulate by hand-crafting the request.
	h.HandleLocalModelsUninstall(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/local-models/installed/", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400 for missing model id (body=%q)", rec.Code, rec.Body.String())
	}
}

func TestHandleLocalModelsUninstall_NotFound(t *testing.T) {
	t.Parallel()
	svc, _, _ := makeRealService(t)
	h := makeHandlerWithLocalModels(t, svc)

	// Use the real mux so r.PathValue gets populated.
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /hecate/v1/local-models/installed/{model_id}", h.HandleLocalModelsUninstall)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/hecate/v1/local-models/installed/no-such-model", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 (body=%q)", rec.Code, rec.Body.String())
	}
}

func TestHandleLocalModelsCancelInstall_NotFound(t *testing.T) {
	t.Parallel()
	svc, _, _ := makeRealService(t)
	h := makeHandlerWithLocalModels(t, svc)
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /hecate/v1/local-models/install/{install_id}", h.HandleLocalModelsCancelInstall)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/hecate/v1/local-models/install/no-such-install", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rec.Code)
	}
}

func TestHandleLocalModelsRuntimeStart_ValidatesModelID(t *testing.T) {
	t.Parallel()
	svc, _, _ := makeRealService(t)
	h := makeHandlerWithLocalModels(t, svc)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/hecate/v1/local-models/runtime/start",
		bytes.NewBufferString(`{}`))
	h.HandleLocalModelsRuntimeStart(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rec.Code)
	}
}

// makeServiceWithHF wires a service whose HF client points at the
// given httptest server so the handler can be exercised end-to-end
// without hitting huggingface.co.
func makeServiceWithHF(t *testing.T, hfBaseURL string) *llamacpp.Service {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(t.TempDir(), "llama-server")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	store := controlplane.NewMemoryStore()
	svc, err := llamacpp.NewService(llamacpp.ServiceOptions{
		BinaryPath:         bin,
		DataDir:            dir,
		Store:              store,
		HuggingFaceOptions: llamacpp.HuggingFaceOptions{BaseURL: hfBaseURL},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func TestHandleLocalModelsHFSearch_ReturnsResults(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "filter=gguf") {
			t.Errorf("query missing filter=gguf: %q", r.URL.RawQuery)
		}
		if !strings.Contains(r.URL.RawQuery, "search=qwen") {
			t.Errorf("query missing search=qwen: %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`[
			{"id":"bartowski/Qwen-GGUF","author":"bartowski","downloads":12,"tags":["gguf"]}
		]`))
	}))
	defer srv.Close()

	svc := makeServiceWithHF(t, srv.URL)
	h := makeHandlerWithLocalModels(t, svc)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hecate/v1/local-models/huggingface/search?q=qwen&limit=5", nil)
	h.HandleLocalModelsHFSearch(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "bartowski/Qwen-GGUF") {
		t.Fatalf("body missing result id: %q", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"object":"local_models.huggingface.search"`) {
		t.Fatalf("body missing object: %q", rec.Body.String())
	}
}

func TestHandleLocalModelsHFSearch_MapsGatedToErrorCode(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gated", http.StatusForbidden)
	}))
	defer srv.Close()

	svc := makeServiceWithHF(t, srv.URL)
	h := makeHandlerWithLocalModels(t, svc)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hecate/v1/local-models/huggingface/search?q=anything", nil)
	h.HandleLocalModelsHFSearch(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), errCodeHuggingFaceGated) {
		t.Fatalf("body missing %q: %q", errCodeHuggingFaceGated, rec.Body.String())
	}
}

func TestHandleLocalModelsHFSearch_Dormant503(t *testing.T) {
	t.Parallel()
	h := makeHandlerWithLocalModels(t, nil)
	rec := httptest.NewRecorder()
	h.HandleLocalModelsHFSearch(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/local-models/huggingface/search", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", rec.Code)
	}
}

func TestHandleLocalModelsHFRepoFiles_ReturnsGGUFFiles(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/models/bartowski/Qwen/tree/main") {
			t.Errorf("unexpected path: %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[
			{"type":"file","path":"Qwen-Q4.gguf","size":100,"lfs":{"oid":"deadbeef","size":4000}},
			{"type":"file","path":"README.md","size":200}
		]`))
	}))
	defer srv.Close()

	svc := makeServiceWithHF(t, srv.URL)
	h := makeHandlerWithLocalModels(t, svc)
	// Need PathValue wired — use the real mux registration.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hecate/v1/local-models/huggingface/repos/{owner}/{name}", h.HandleLocalModelsHFRepoFiles)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/local-models/huggingface/repos/bartowski/Qwen", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Qwen-Q4.gguf") {
		t.Fatalf("body missing gguf file: %q", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "README.md") {
		t.Fatalf("body should not include non-gguf file: %q", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"sha256":"deadbeef"`) {
		t.Fatalf("body missing LFS sha: %q", rec.Body.String())
	}
}

func TestHandleLocalModelsHFRepoFiles_MapsNotFound(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no repo", http.StatusNotFound)
	}))
	defer srv.Close()

	svc := makeServiceWithHF(t, srv.URL)
	h := makeHandlerWithLocalModels(t, svc)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hecate/v1/local-models/huggingface/repos/{owner}/{name}", h.HandleLocalModelsHFRepoFiles)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/local-models/huggingface/repos/owner/missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 (body=%q)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), errCodeHuggingFaceNotFound) {
		t.Fatalf("body missing %q: %q", errCodeHuggingFaceNotFound, rec.Body.String())
	}
}

func TestHandleLocalModelsHFSearch_ForwardsTokenFromHeader(t *testing.T) {
	t.Parallel()
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	svc := makeServiceWithHF(t, srv.URL)
	h := makeHandlerWithLocalModels(t, svc)
	rec := httptest.NewRecorder()
	// Header path — the only supported transport for the HF token.
	// Query-string tokens (?token=) are ignored on purpose so the
	// secret can't leak through access logs.
	req := httptest.NewRequest(http.MethodGet, "/hecate/v1/local-models/huggingface/search", nil)
	req.Header.Set("Authorization", "Bearer secret-xyz")
	h.HandleLocalModelsHFSearch(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if capturedAuth != "Bearer secret-xyz" {
		t.Fatalf("Authorization = %q; want Bearer secret-xyz", capturedAuth)
	}
}

func TestHandleLocalModelsHFSearch_IgnoresQueryStringToken(t *testing.T) {
	t.Parallel()
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	svc := makeServiceWithHF(t, srv.URL)
	h := makeHandlerWithLocalModels(t, svc)
	rec := httptest.NewRecorder()
	// Old query-string transport is removed — a ?token= in the URL
	// must not leak through to HF.
	req := httptest.NewRequest(http.MethodGet, "/hecate/v1/local-models/huggingface/search?token=should-be-ignored", nil)
	h.HandleLocalModelsHFSearch(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if capturedAuth != "" {
		t.Fatalf("Authorization = %q; want empty (query-string token must be ignored)", capturedAuth)
	}
}
