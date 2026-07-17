package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
)

func newAgentPresetsTestServer() http.Handler {
	handler := NewHandler(config.Config{}, quietLogger(), nil, nil, nil, nil)
	return NewServer(quietLogger(), handler)
}

func TestAgentPresetsAPI_CRUD(t *testing.T) {
	t.Parallel()
	server := newAgentPresetsTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/agent-presets", bytes.NewReader([]byte(`{
		"id":"prof_backend",
		"name":"Backend implementer",
		"description":"Go runtime work",
		"instructions":"Prefer narrow tests.",
		"surface":"hecate_task",
		"provider_hint":"anthropic",
		"model_hint":"claude-sonnet-4",
		"execution_profile":"implementation",
		"tools_enabled":true,
		"writes_allowed":true,
		"network_allowed":false,
		"browser_allowed":true,
		"browser_allowed_origins":["https://app.example.test/"],
		"approval_policy":"require",
		"project_memory_policy":"visible_only",
		"context_source_policy":"include_enabled",
		"skill_ids":["backend","providers"],
		"external_agent_kind":"codex",
		"external_agent_options":{"effort":"high"}
	}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created AgentPresetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Object != "agent_preset" || created.Data.ID != "prof_backend" || created.Data.ExecutionProfile != "implementation" {
		t.Fatalf("created = %+v, want preset envelope", created)
	}
	if created.Data.BuiltIn {
		t.Fatalf("created preset is marked built-in")
	}
	if !created.Data.ToolsEnabled || !created.Data.WritesAllowed || created.Data.NetworkAllowed {
		t.Fatalf("posture = tools=%v writes=%v network=%v, want true/true/false", created.Data.ToolsEnabled, created.Data.WritesAllowed, created.Data.NetworkAllowed)
	}
	if !created.Data.BrowserAllowed || len(created.Data.BrowserAllowedOrigins) != 1 || created.Data.BrowserAllowedOrigins[0] != "https://app.example.test" {
		t.Fatalf("browser posture = %+v, want enabled exact normalized origin", created.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/agent-presets/prof_backend", bytes.NewReader([]byte(`{
		"name":"Backend reviewer",
		"writes_allowed":false,
		"browser_allowed_origins":["https://console.example.test"],
		"approval_policy":"block"
	}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var updated AgentPresetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if updated.Data.Name != "Backend reviewer" || updated.Data.WritesAllowed || updated.Data.ApprovalPolicy != "block" || !updated.Data.BrowserAllowed || len(updated.Data.BrowserAllowedOrigins) != 1 || updated.Data.BrowserAllowedOrigins[0] != "https://console.example.test" {
		t.Fatalf("updated = %+v, want patched preset", updated.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/agent-presets", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var listed AgentPresetsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if listed.Object != "agent_presets" || !agentPresetResponseIDExists(listed.Data, "implementation") || !agentPresetResponseIDExists(listed.Data, "prof_backend") {
		t.Fatalf("listed = %+v, want built-ins plus created preset", listed)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/agent-presets/prof_backend", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s, want 204", rec.Code, rec.Body.String())
	}
}

func TestAgentPresetsAPI_PatchClearsIneligibleBrowserPosture(t *testing.T) {
	t.Parallel()
	server := newAgentPresetsTestServer()
	tests := []struct {
		name   string
		patch  string
		assert func(t *testing.T, profile AgentPresetResponseItem)
	}{
		{
			name:  "browser disabled",
			patch: `{"browser_allowed":false}`,
			assert: func(t *testing.T, profile AgentPresetResponseItem) {
				t.Helper()
				if profile.BrowserAllowed {
					t.Fatalf("browser_allowed = true, want false")
				}
			},
		},
		{
			name:  "tools disabled",
			patch: `{"tools_enabled":false}`,
			assert: func(t *testing.T, profile AgentPresetResponseItem) {
				t.Helper()
				if profile.ToolsEnabled {
					t.Fatalf("tools_enabled = true, want false")
				}
			},
		},
		{
			name:  "surface changed",
			patch: `{"surface":"hecate_chat"}`,
			assert: func(t *testing.T, profile AgentPresetResponseItem) {
				t.Helper()
				if profile.Surface != "hecate_chat" {
					t.Fatalf("surface = %q, want hecate_chat", profile.Surface)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			id := "prof_browser_patch_" + strings.ReplaceAll(test.name, " ", "_")
			create := `{"id":"` + id + `","name":"Browser preset","surface":"hecate_task","tools_enabled":true,"browser_allowed":true,"browser_allowed_origins":["https://app.example.test"]}`
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/agent-presets", bytes.NewReader([]byte(create))))
			if rec.Code != http.StatusCreated {
				t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
			}

			rec = httptest.NewRecorder()
			server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/agent-presets/"+id, bytes.NewReader([]byte(test.patch))))
			if rec.Code != http.StatusOK {
				t.Fatalf("patch status = %d body=%s, want 200", rec.Code, rec.Body.String())
			}
			var updated AgentPresetResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
				t.Fatalf("decode patch response: %v", err)
			}
			test.assert(t, updated.Data)
			if updated.Data.BrowserAllowed || len(updated.Data.BrowserAllowedOrigins) != 0 {
				t.Fatalf("browser posture = %+v, want disabled with no stale origins", updated.Data)
			}
		})
	}
}

func TestAgentPresetsAPI_OldProfileRouteRemoved(t *testing.T) {
	t.Parallel()
	server := newAgentPresetsTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/agent-profiles", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("old route status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestAgentPresetsAPI_BuiltInPresetsAreReadOnly(t *testing.T) {
	t.Parallel()
	server := newAgentPresetsTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/agent-presets/implementation", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get built-in status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var got AgentPresetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if !got.Data.BuiltIn || got.Data.ExecutionProfile != "coding_agent" || !got.Data.WritesAllowed {
		t.Fatalf("built-in preset = %+v, want implementation posture", got.Data)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/agent-presets", bytes.NewReader([]byte(`{"id":"implementation","name":"Override"}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("create built-in status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/hecate/v1/agent-presets/implementation", bytes.NewReader([]byte(`{"name":"Override"}`))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("patch built-in status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/agent-presets/implementation", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete built-in status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
}

func TestAgentPresetsAPI_RejectsInvalidEnums(t *testing.T) {
	t.Parallel()
	server := newAgentPresetsTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/agent-presets", bytes.NewReader([]byte(`{
		"id":"prof_bad",
		"name":"Bad",
		"surface":"terminal"
	}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create bad enum status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func agentPresetResponseIDExists(items []AgentPresetResponseItem, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func TestAgentPresetsAPI_GeneratesIDsAndReturnsNotFound(t *testing.T) {
	t.Parallel()
	server := newAgentPresetsTestServer()

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/hecate/v1/agent-presets", bytes.NewReader([]byte(`{"name":"Generated"}`))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var created AgentPresetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if !strings.HasPrefix(created.Data.ID, "prof_") {
		t.Fatalf("generated id = %q, want prof_ prefix", created.Data.ID)
	}

	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/agent-presets/prof_missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get missing status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/hecate/v1/agent-presets/prof_missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing status = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}
