package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/hecate/agent-runtime/internal/agentadapters"
	"github.com/hecate/agent-runtime/internal/config"
)

// TestAgentAdapterHealthSurfacesProbeResult covers the happy-path
// classifications (`ready`, `auth_required`, `not_installed`,
// `error`). Each one reaches the wire as a 200 with the typed
// payload — the probe completing successfully is itself a 200; the
// adapter's status lives in the body.
func TestAgentAdapterHealthSurfacesProbeResult(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		result agentadapters.ProbeResult
	}{
		{
			name: "ready",
			result: agentadapters.ProbeResult{
				AdapterID: "codex",
				Status:    agentadapters.ProbeStatusReady,
				Stage:     agentadapters.ProbeStageReady,
				Path:      "/usr/local/bin/codex-acp",
			},
		},
		{
			name: "auth_required",
			result: agentadapters.ProbeResult{
				AdapterID: "codex",
				Status:    agentadapters.ProbeStatusAuthRequired,
				Stage:     agentadapters.ProbeStageInitialize,
				Path:      "/usr/local/bin/codex-acp",
				Error:     "Authentication required",
				Hint:      "Run codex login",
			},
		},
		{
			name: "not_installed",
			result: agentadapters.ProbeResult{
				AdapterID: "codex",
				Status:    agentadapters.ProbeStatusNotInstalled,
				Stage:     agentadapters.ProbeStageLookup,
				Error:     "exec: codex-acp not found",
				Hint:      "Install Codex and ensure it's on PATH.",
			},
		},
		{
			name: "error",
			result: agentadapters.ProbeResult{
				AdapterID: "codex",
				Status:    agentadapters.ProbeStatusError,
				Stage:     agentadapters.ProbeStageInitialize,
				Path:      "/usr/local/bin/codex-acp",
				Error:     "unexpected ACP protocol version",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
			apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
			calls := 0
			apiHandler.SetAgentAdapterProbe(func(_ context.Context, id string) agentadapters.ProbeResult {
				calls++
				if id != "codex" {
					t.Fatalf("probe called for %q, want codex", id)
				}
				return tc.result
			})
			server := NewServer(logger, apiHandler)
			client := newAPITestClient(t, server)

			resp := mustRequestJSON[AgentAdapterHealthResponse](client, http.MethodGet, "/v1/agent-adapters/codex/health", "")
			if calls != 1 {
				t.Fatalf("probe call count = %d, want 1", calls)
			}
			if resp.Object != "agent_adapter_health" {
				t.Fatalf("Object = %q, want agent_adapter_health", resp.Object)
			}
			if resp.Data.Status != tc.result.Status {
				t.Fatalf("Status = %q, want %q", resp.Data.Status, tc.result.Status)
			}
			if resp.Data.Stage != tc.result.Stage {
				t.Fatalf("Stage = %q, want %q", resp.Data.Stage, tc.result.Stage)
			}
			if resp.Data.AdapterID != "codex" {
				t.Fatalf("AdapterID = %q, want codex", resp.Data.AdapterID)
			}
		})
	}
}

// TestAgentAdapterHealth404OnUnknownAdapter — we 404 BEFORE invoking
// the probe so phantom binaries can't be spawned by URL-fuzzing.
func TestAgentAdapterHealth404OnUnknownAdapter(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	probeCalls := 0
	apiHandler.SetAgentAdapterProbe(func(context.Context, string) agentadapters.ProbeResult {
		probeCalls++
		return agentadapters.ProbeResult{}
	})
	server := NewServer(logger, apiHandler)
	client := newAPITestClient(t, server)

	recorder := client.mustRequestStatus(http.StatusNotFound, http.MethodGet, "/v1/agent-adapters/no-such-adapter/health", "")
	if probeCalls != 0 {
		t.Fatalf("probe call count = %d, want 0 (404 must short-circuit)", probeCalls)
	}

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	errBody, _ := body["error"].(map[string]any)
	if errBody == nil {
		t.Fatalf("response missing error body: %s", recorder.Body.String())
	}
	if msg, _ := errBody["message"].(string); !strings.Contains(strings.ToLower(msg), "not found") {
		t.Fatalf("error.message = %q, want substring %q", msg, "not found")
	}
}
