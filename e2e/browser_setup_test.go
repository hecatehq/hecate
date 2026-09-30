//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/remoteruntime"
)

func TestBrowserSetupEnvironmentIsPassiveE2E(t *testing.T) {
	executable, marker := buildE2EBrowserSentinel(t)
	baseURL := browserSetupServer(t,
		"HECATE_TASK_BROWSER_EXECUTABLE="+executable,
		"HECATE_E2E_BROWSER_MARKER="+marker,
	)
	for range 2 {
		settings := readE2EBrowserSettings(t, baseURL, http.MethodGet, "")
		if settings.Data.Source != "environment" || settings.Data.Selected == nil || settings.Data.Selected.Path != executable || settings.Data.Selected.ID == "" {
			t.Fatalf("environment browser selection = %+v", settings.Data)
		}
		assertE2EBrowserSetupState(t, settings.Data.Readiness, "configured", true)
		assertE2EBrowserReadiness(t, baseURL, nil, "configured", true, executable)
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		body := browserSetupRequest(t, baseURL+"/hecate/v1/settings/browser", method, `{"candidate_id":"not-a-discovered-candidate"}`, nil, http.StatusConflict)
		var response struct {
			Error struct {
				Type string `json:"type"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatal(err)
		}
		if response.Error.Type != "browser.environment_managed" {
			t.Fatalf("%s conflict type = %q", method, response.Error.Type)
		}
	}
	// Losing the explicitly configured installation must not select a discovered
	// browser instead. The sentinel belongs exclusively to this temporary test.
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	settings := readE2EBrowserSettings(t, baseURL, http.MethodGet, "")
	if settings.Data.Source != "environment" || settings.Data.Selected == nil || settings.Data.Selected.Path != executable {
		t.Fatalf("missing environment installation fell back: %+v", settings.Data)
	}
	assertE2EBrowserSetupState(t, settings.Data.Readiness, "unavailable", false)
	assertE2EBrowserReadiness(t, baseURL, nil, "unavailable", false, executable)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("browser setup executed the sentinel: %v", err)
	}
}

func TestBrowserSetupInstalledSelectionE2E(t *testing.T) {
	baseURL := browserSetupServer(t)
	settings := readE2EBrowserSettings(t, baseURL, http.MethodGet, "")
	if settings.Data.Source != "none" || settings.Data.Selected != nil {
		t.Fatalf("discovery automatically selected a browser: %+v", settings.Data)
	}
	assertE2EBrowserSetupState(t, settings.Data.Readiness, "not_configured", false)
	assertE2EBrowserReadiness(t, baseURL, nil, "not_configured", false)
	t.Run("enable and disable discovered installation", func(t *testing.T) {
		if len(settings.Data.Candidates) == 0 {
			t.Skip("no supported installed browser; passive discovery assertions still ran")
		}
		candidate := settings.Data.Candidates[0]
		if candidate.ID == "" || candidate.Name == "" || candidate.Path == "" {
			t.Fatalf("incomplete discovered candidate: %+v", candidate)
		}
		enabled := readE2EBrowserSettings(t, baseURL, http.MethodPut, fmt.Sprintf(`{"candidate_id":%q}`, candidate.ID))
		if enabled.Data.Source != "settings" || enabled.Data.Selected == nil || *enabled.Data.Selected != candidate {
			t.Fatalf("enabled browser = %+v, want %+v", enabled.Data, candidate)
		}
		assertE2EBrowserSetupState(t, enabled.Data.Readiness, "configured", true)
		readback := readE2EBrowserSettings(t, baseURL, http.MethodGet, "")
		if readback.Data.Selected == nil || *readback.Data.Selected != candidate || readback.Data.Source != "settings" {
			t.Fatalf("saved browser readback = %+v", readback.Data)
		}
		assertE2EBrowserReadiness(t, baseURL, nil, "configured", true, candidate.Path)
		disabled := readE2EBrowserSettings(t, baseURL, http.MethodDelete, "")
		if disabled.Data.Source != "none" || disabled.Data.Selected != nil {
			t.Fatalf("disabled browser selection = %+v", disabled.Data)
		}
		assertE2EBrowserSetupState(t, disabled.Data.Readiness, "not_configured", false)
		assertE2EBrowserReadiness(t, baseURL, nil, "not_configured", false, candidate.Path)
	})
}

func TestBrowserSetupRemoteRuntimeBoundaryE2E(t *testing.T) {
	const secret = "browser-setup-remote-secret-for-tests"
	baseURL := browserSetupServer(t,
		"HECATE_REMOTE_RUNTIME_MODE=1",
		"HECATE_REMOTE_RUNTIME_SECRET="+secret,
	)
	headers := map[string]string{
		remoteruntime.HeaderRuntimeSecret: secret,
		remoteruntime.HeaderActorID:       "browser-setup-actor",
		remoteruntime.HeaderOrgID:         "browser-setup-org",
		remoteruntime.HeaderRuntimeID:     "browser-setup-runtime",
	}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		body := browserSetupRequest(t, baseURL+"/hecate/v1/settings/browser", method, `{"candidate_id":"unavailable"}`, headers, http.StatusForbidden)
		if strings.Contains(string(body), `"candidates"`) || strings.Contains(string(body), `"selected"`) || strings.Contains(string(body), `"path"`) {
			t.Fatalf("remote setup exposed installation details: %s", body)
		}
	}
	assertE2EBrowserReadiness(t, baseURL, headers, "local_only", false)
}

type e2eBrowserSetupReadiness struct {
	Available bool   `json:"available"`
	Status    string `json:"status"`
}

type e2eBrowserSetupCandidate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type e2eBrowserSetupResponse struct {
	Object string `json:"object"`
	Data   struct {
		Readiness  e2eBrowserSetupReadiness   `json:"readiness"`
		Source     string                     `json:"source"`
		Selected   *e2eBrowserSetupCandidate  `json:"selected"`
		Candidates []e2eBrowserSetupCandidate `json:"candidates"`
		Backend    string                     `json:"backend"`
	} `json:"data"`
}

func browserSetupServer(t *testing.T, extraEnv ...string) string {
	t.Helper()
	env := []string{
		"HECATE_BACKEND=sqlite", "HECATE_TASK_BROWSER_EXECUTABLE=", "HECATE_TASK_BROWSER_TIMEOUT=20s",
		"HECATE_REMOTE_RUNTIME_MODE=0", "HECATE_REMOTE_RUNTIME_SECRET=",
		"HECATE_RUNTIME_TOKEN=", "HECATE_INFERENCE_TOKEN=",
		"HECATE_OPERATOR_TERMINALS=0", "HECATE_AGENT_ADAPTER_TERMINALS=0",
	}
	return gatewayServer(t, append(env, extraEnv...)...)
}

func readE2EBrowserSettings(t *testing.T, baseURL, method, requestBody string) e2eBrowserSetupResponse {
	t.Helper()
	body := browserSetupRequest(t, baseURL+"/hecate/v1/settings/browser", method, requestBody, nil, http.StatusOK)
	var settings e2eBrowserSetupResponse
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Object != "browser_settings" || settings.Data.Backend != "sqlite" || settings.Data.Candidates == nil {
		t.Fatalf("browser settings envelope = %s", body)
	}
	return settings
}

func browserSetupRequest(t *testing.T, url, method, body string, headers map[string]string, wantStatus int) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	result := readBody(t, response)
	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s status=%d, want %d: %s", method, url, response.StatusCode, wantStatus, result)
	}
	if strings.HasSuffix(url, "/settings/browser") && wantStatus != http.StatusForbidden && response.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("browser setup cache policy = %q", response.Header.Get("Cache-Control"))
	}
	return []byte(result)
}

func assertE2EBrowserSetupState(t *testing.T, readiness e2eBrowserSetupReadiness, status string, available bool) {
	t.Helper()
	if readiness.Status != status || readiness.Available != available {
		t.Fatalf("browser readiness = %+v, want status=%s available=%t", readiness, status, available)
	}
}

func assertE2EBrowserReadiness(t *testing.T, baseURL string, headers map[string]string, status string, available bool, privatePaths ...string) {
	t.Helper()
	body := browserSetupRequest(t, baseURL+"/hecate/v1/settings", http.MethodGet, "", headers, http.StatusOK)
	var settings struct {
		Object string `json:"object"`
		Data   struct {
			Readiness json.RawMessage `json:"browser_evidence"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Object != "settings" || len(settings.Data.Readiness) == 0 {
		t.Fatalf("general settings envelope = %s", body)
	}
	var readiness e2eBrowserSetupReadiness
	if err := json.Unmarshal(settings.Data.Readiness, &readiness); err != nil {
		t.Fatal(err)
	}
	assertE2EBrowserSetupState(t, readiness, status, available)
	var fields map[string]any
	if err := json.Unmarshal(settings.Data.Readiness, &fields); err != nil {
		t.Fatal(err)
	}
	for name, value := range fields {
		switch name {
		case "available", "status", "message", "operator_action":
		default:
			t.Fatalf("shared browser readiness exposed unexpected field %q", name)
		}
		if text, ok := value.(string); ok {
			for _, path := range privatePaths {
				if path != "" && strings.Contains(text, path) {
					t.Fatalf("shared browser readiness exposed installation path in %s", name)
				}
			}
		}
	}
}
