package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/browserapp"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
)

func browserSettingsRequest(method, body string) *http.Request {
	r := httptest.NewRequest(method, "/hecate/v1/settings/browser", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	return r
}

func TestBrowserSettingsRejectRemoteNonLoopbackAndForwardedClients(t *testing.T) {
	t.Parallel()
	for _, boundary := range []string{"remote", "non-loopback", "forwarded"} {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			t.Run(boundary+method, func(t *testing.T) {
				cfg := config.Config{Server: config.ServerConfig{RemoteRuntimeMode: boundary == "remote"}}
				h := NewHandler(cfg, quietLogger(), nil, controlplane.NewMemoryStore(), nil, nil)
				r := browserSettingsRequest(method, `{"candidate_id":"candidate"}`)
				if boundary == "non-loopback" {
					r.RemoteAddr = "192.0.2.2:12345"
				}
				if boundary == "forwarded" {
					r.Header.Set("X-Forwarded-For", "127.0.0.1")
				}
				rec := httptest.NewRecorder()
				switch method {
				case http.MethodGet:
					h.HandleBrowserSettings(rec, r)
				case http.MethodPut:
					h.HandleBrowserSettingsEnable(rec, r)
				case http.MethodDelete:
					h.HandleBrowserSettingsDisable(rec, r)
				}
				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
				}
				if rec.Header().Get("Cache-Control") != "private, no-store" {
					t.Fatal("host browser response is cacheable")
				}
				if remoteRuntimeEndpointBlockReason(method, r.URL.Path) != remoteRuntimeLocalOnlyMessage {
					t.Fatal("route not local-only")
				}
			})
		}
	}
}

func TestBrowserSettingsEnableRejectsMalformedAndStaleSelections(t *testing.T) {
	t.Parallel()
	h := NewHandler(config.Config{}, quietLogger(), nil, controlplane.NewMemoryStore(), nil, nil)
	for _, test := range []struct {
		body   string
		status int
	}{
		{`{}`, 400}, {`null`, 400}, {`{"candidate_id":""}`, 400},
		{`{"candidate_id":"id","path":"/arbitrary"}`, 400},
		{`{"candidate_id":"id"} {}`, 400}, {`{"candidate_id":"id"} trailing`, 400},
		{`{"candidate_id":"` + strings.Repeat("x", 4096) + `"}`, 400},
		{`{"candidate_id":"unknown-or-stale"}`, 409},
	} {
		rec := httptest.NewRecorder()
		h.HandleBrowserSettingsEnable(rec, browserSettingsRequest(http.MethodPut, test.body))
		if rec.Code != test.status {
			t.Fatalf("status = %d want %d: %s", rec.Code, test.status, rec.Body.String())
		}
	}
	if h.browserReadiness(t.Context()).Available {
		t.Fatal("invalid request enabled browser runtime")
	}
}

func TestBrowserSettingsRestoredSelectionAndDisableRefreshReadiness(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "browser.exe")
	if err := os.WriteFile(path, []byte("not executed"), 0o700); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	store := browserapp.NewMemoryStore()
	if err := store.Put(t.Context(), browserapp.Selection{RuntimeHostID: "browser-test-host", Name: "Test browser", Path: path, CanonicalPath: canonical}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(config.Config{Server: config.ServerConfig{RuntimeHostID: "browser-test-host"}}, quietLogger(), nil, controlplane.NewMemoryStore(), nil, nil)
	h.SetBrowserSettingsStore(store)
	rec := httptest.NewRecorder()
	h.HandleBrowserSettings(rec, browserSettingsRequest(http.MethodGet, ""))
	var response BrowserSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || response.Object != "browser_settings" || response.Data.Source != "settings" || response.Data.Selected == nil || response.Data.Selected.Path != path || response.Data.Readiness.Status != "configured" || !response.Data.Readiness.Available {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	// Selection and reads are passive: the fixture isn't an executable image.
	settingsRec := httptest.NewRecorder()
	h.HandleSettingsStatus(settingsRec, browserSettingsRequest(http.MethodGet, ""))
	if strings.Contains(settingsRec.Body.String(), path) || strings.Contains(settingsRec.Body.String(), "Test browser") {
		t.Fatal("remote-safe settings leaked browser path/name")
	}
	rec = httptest.NewRecorder()
	h.HandleBrowserSettingsDisable(rec, browserSettingsRequest(http.MethodDelete, ""))
	if rec.Code != 200 || h.browserReadiness(t.Context()).Available {
		t.Fatalf("disable = %d %s", rec.Code, rec.Body.String())
	}
}

func TestBrowserSettingsEnvironmentOverrideCannotBeEditedOrFallBack(t *testing.T) {
	t.Parallel()
	h := NewHandler(config.Config{Server: config.ServerConfig{TaskBrowserExecutable: filepath.Join(t.TempDir(), "missing")}}, quietLogger(), nil, controlplane.NewMemoryStore(), nil, nil)
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		r := browserSettingsRequest(method, `{"candidate_id":"anything"}`)
		if method == http.MethodPut {
			h.HandleBrowserSettingsEnable(rec, r)
		} else {
			h.HandleBrowserSettingsDisable(rec, r)
		}
		if rec.Code != 409 || !strings.Contains(rec.Body.String(), "browser.environment_managed") {
			t.Fatalf("mutation = %d %s", rec.Code, rec.Body.String())
		}
	}
	if h.browserReadiness(t.Context()).Available {
		t.Fatal("invalid environment browser fell back")
	}
}
