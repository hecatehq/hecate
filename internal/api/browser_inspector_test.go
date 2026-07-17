package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
)

func TestSettingsStatusBrowserEvidenceReadinessIsPathFree(t *testing.T) {
	t.Parallel()
	executable := filepath.Join(t.TempDir(), "local-browser")
	if err := os.WriteFile(executable, []byte("browser"), 0o700); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	for _, test := range []struct {
		name      string
		cfg       config.Config
		available bool
		status    string
		contains  string
	}{
		{
			name:     "not configured",
			status:   "not_configured",
			contains: "HECATE_TASK_BROWSER_EXECUTABLE",
		},
		{
			name: "remote runtime",
			cfg: config.Config{Server: config.ServerConfig{
				RemoteRuntimeMode:     true,
				TaskBrowserExecutable: executable,
			}},
			status:   "local_only",
			contains: "local Hecate runtime",
		},
		{
			name: "unavailable executable",
			cfg: config.Config{Server: config.ServerConfig{
				TaskBrowserExecutable: filepath.Join(t.TempDir(), "missing-browser"),
			}},
			status:   "unavailable",
			contains: "executable permissions",
		},
		{
			name: "ready",
			cfg: config.Config{Server: config.ServerConfig{
				TaskBrowserExecutable: executable,
			}},
			available: true,
			status:    "ready",
			contains:  "ready on this local runtime",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := NewHandler(test.cfg, quietLogger(), nil, controlplane.NewMemoryStore(), nil, nil)
			rec := httptest.NewRecorder()
			h.HandleSettingsStatus(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/settings", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("HandleSettingsStatus() status = %d body=%s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			var response SettingsResponse
			if err := json.Unmarshal([]byte(body), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			got := response.Data.BrowserEvidence
			if got.Available != test.available || got.Status != test.status || !strings.Contains(got.Message+" "+got.OperatorAction, test.contains) {
				t.Fatalf("browser readiness = %+v, want available=%v status=%q and %q", got, test.available, test.status, test.contains)
			}
			if strings.Contains(body, executable) || strings.Contains(body, "missing-browser") {
				t.Fatalf("settings response leaked an executable path: %s", body)
			}
		})
	}
}
