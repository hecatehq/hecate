package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
)

func TestPluginRegistryAPI_InstallListPatchAndHealth(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)

	installRec := httptest.NewRecorder()
	server.ServeHTTP(installRec, httptest.NewRequest(http.MethodPost, "/hecate/v1/plugins/install-local", pluginAPITestJSONBody(t, map[string]any{
		"source_ref": "/plugins/github/plugin.json",
		"manifest": map[string]any{
			"schema_version": "hecate.plugin.v0",
			"id":             "github",
			"name":           "GitHub",
			"description":    "Read and link GitHub work.",
			"version":        "0.1.0",
			"permissions":    []string{"network:github.com", "unsupported:host-hook"},
			"capabilities": map[string]any{
				"connectors": []map[string]any{{
					"id":           "issues",
					"display_name": "Issues",
					"permissions":  []string{"secret:github_token"},
					"auth":         []map[string]any{{"name": "github_token", "kind": "token"}},
				}},
			},
		},
	})))
	if installRec.Code != http.StatusOK {
		t.Fatalf("install status = %d body=%s, want 200", installRec.Code, installRec.Body.String())
	}
	var installed PluginResponse
	if err := json.Unmarshal(installRec.Body.Bytes(), &installed); err != nil {
		t.Fatalf("decode install response: %v", err)
	}
	if installed.Object != "plugin" || installed.Data.ID != "github" || installed.Data.Enabled {
		t.Fatalf("installed = %+v, want disabled github plugin", installed)
	}
	if len(installed.Data.Capabilities) != 1 || len(installed.Data.Auth) != 1 {
		t.Fatalf("installed data = %+v, want projected capabilities and auth", installed.Data)
	}

	patchRec := httptest.NewRecorder()
	server.ServeHTTP(patchRec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/plugins/github", pluginAPITestJSONBody(t, map[string]any{
		"enabled":      true,
		"capabilities": map[string]any{"issues": map[string]any{"enabled": false}},
	})))
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s, want 200", patchRec.Code, patchRec.Body.String())
	}
	var patched PluginResponse
	if err := json.Unmarshal(patchRec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if !patched.Data.Enabled || patched.Data.Capabilities[0].Enabled {
		t.Fatalf("patched = %+v, want plugin enabled and capability disabled", patched.Data)
	}

	badPatchRec := httptest.NewRecorder()
	server.ServeHTTP(badPatchRec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/plugins/github", pluginAPITestJSONBody(t, map[string]any{
		"capabilities": map[string]any{"missing": map[string]any{"enabled": false}},
	})))
	if badPatchRec.Code != http.StatusBadRequest {
		t.Fatalf("bad patch status = %d body=%s, want 400", badPatchRec.Code, badPatchRec.Body.String())
	}

	listRec := httptest.NewRecorder()
	server.ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/hecate/v1/plugins", nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", listRec.Code, listRec.Body.String())
	}
	var listed PluginsResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed.Data) != 1 || listed.Data[0].ID != "github" {
		t.Fatalf("listed = %+v, want github plugin", listed)
	}

	healthRec := httptest.NewRecorder()
	server.ServeHTTP(healthRec, httptest.NewRequest(http.MethodGet, "/hecate/v1/plugins/github/health", nil))
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status = %d body=%s, want 200", healthRec.Code, healthRec.Body.String())
	}
	var health PluginHealthResponse
	if err := json.Unmarshal(healthRec.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if len(health.Data.UnsupportedPermissions) != 1 || health.Data.UnsupportedPermissions[0] != "unsupported:host-hook" {
		t.Fatalf("health = %+v, want unsupported permission", health.Data)
	}
	if len(health.Data.UnresolvedSecretBindings) != 1 || health.Data.UnresolvedSecretBindings[0] != "github_token" {
		t.Fatalf("health = %+v, want unresolved secret", health.Data)
	}
	if len(health.Data.DisabledCapabilities) != 1 || health.Data.DisabledCapabilities[0] != "issues" {
		t.Fatalf("health = %+v, want disabled capability", health.Data)
	}
}

func TestPluginRegistryAPI_RejectsInvalidManifest(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/plugins/install-local", pluginAPITestJSONBody(t, map[string]any{
		"manifest": map[string]any{
			"schema_version": "other",
			"id":             "bad",
			"name":           "Bad",
			"version":        "0.1.0",
		},
	})))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func TestPluginRegistryAPI_RejectsDuplicateCapabilityIDsAsInvalidRequest(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/plugins/install-local", pluginAPITestJSONBody(t, map[string]any{
		"manifest": map[string]any{
			"schema_version": "hecate.plugin.v0",
			"id":             "github",
			"name":           "GitHub",
			"version":        "0.1.0",
			"capabilities": []map[string]any{
				{"id": "issues", "kind": "connector"},
				{"id": "issues", "kind": "slash_command"},
			},
		},
	})))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400 for duplicate capability id", rec.Code, rec.Body.String())
	}
}

func pluginAPITestJSONBody(t *testing.T, payload any) *bytes.Reader {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return bytes.NewReader(raw)
}
