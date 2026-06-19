package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/config"
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
				Path:      "/usr/local/bin/codex-acp-adapter",
			},
		},
		{
			name: "auth_required",
			result: agentadapters.ProbeResult{
				AdapterID: "codex",
				Status:    agentadapters.ProbeStatusAuthRequired,
				Stage:     agentadapters.ProbeStageInitialize,
				Path:      "/usr/local/bin/codex-acp-adapter",
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
				Error:     "exec: codex-acp-adapter not found",
				Hint:      "Install Codex and ensure it's on PATH.",
			},
		},
		{
			name: "error",
			result: agentadapters.ProbeResult{
				AdapterID: "codex",
				Status:    agentadapters.ProbeStatusError,
				Stage:     agentadapters.ProbeStageInitialize,
				Path:      "/usr/local/bin/codex-acp-adapter",
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

			resp := mustRequestJSON[AgentAdapterHealthResponse](client, http.MethodGet, "/hecate/v1/agent-adapters/codex/health", "")
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

	recorder := client.mustRequestStatus(http.StatusNotFound, http.MethodGet, "/hecate/v1/agent-adapters/no-such-adapter/health", "")
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

func TestAgentAdapterProbeEndpointReturnsFreshAdapterAndHealth(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	apiHandler.SetAgentAdapterProbe(func(_ context.Context, id string) agentadapters.ProbeResult {
		if id != "codex" {
			t.Fatalf("probe called for %q, want codex", id)
		}
		return agentadapters.ProbeResult{
			AdapterID:  "codex",
			Status:     agentadapters.ProbeStatusReady,
			Stage:      agentadapters.ProbeStageReady,
			Path:       "/usr/local/bin/codex-acp-adapter",
			DurationMS: 42,
		}
	})
	server := NewServer(logger, apiHandler)
	client := newAPITestClient(t, server)

	resp := mustRequestJSON[AgentAdapterProbeResponse](client, http.MethodPost, "/hecate/v1/agent-adapters/codex/probe", "")
	if resp.Object != "agent_adapter_probe" {
		t.Fatalf("object = %q, want agent_adapter_probe", resp.Object)
	}
	if resp.Data.Adapter.ID != "codex" || resp.Data.Health.AdapterID != "codex" {
		t.Fatalf("probe response = %#v, want codex adapter and health", resp.Data)
	}
	if resp.Data.Adapter.AuthStatus != agentadapters.AuthStatusOK {
		t.Fatalf("adapter auth_status = %q, want ok", resp.Data.Adapter.AuthStatus)
	}
	if resp.Data.Health.Status != agentadapters.ProbeStatusReady || resp.Data.Health.DurationMS != 42 {
		t.Fatalf("health = %#v, want ready duration 42", resp.Data.Health)
	}
}

func TestAgentAdapterProbePromotesClaudeHandshakeToAuthOK(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	apiHandler.SetAgentAdapterProbe(func(_ context.Context, id string) agentadapters.ProbeResult {
		if id != "claude_code" {
			t.Fatalf("probe called for %q, want claude_code", id)
		}
		return agentadapters.ProbeResult{
			AdapterID:  "claude_code",
			Status:     agentadapters.ProbeStatusReady,
			Stage:      agentadapters.ProbeStageReady,
			Path:       "/usr/local/bin/claude-code-acp-adapter",
			DurationMS: 42,
		}
	})
	server := NewServer(logger, apiHandler)
	client := newAPITestClient(t, server)

	resp := mustRequestJSON[AgentAdapterProbeResponse](client, http.MethodPost, "/hecate/v1/agent-adapters/claude_code/probe", "")
	if resp.Data.Health.Status != agentadapters.ProbeStatusReady {
		t.Fatalf("health status = %q, want ready", resp.Data.Health.Status)
	}
	if resp.Data.Adapter.AuthStatus != agentadapters.AuthStatusOK {
		t.Fatalf("adapter auth_status = %q, want ok after ready probe", resp.Data.Adapter.AuthStatus)
	}
}

func TestAgentAdapterProbeUsesSyntheticStatusWhenDevOverrideActive(t *testing.T) {
	t.Setenv("HECATE_AGENT_ADAPTER_DEV_OVERRIDES", "codex=no_auth")

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	apiHandler.SetAgentAdapterProbe(func(_ context.Context, id string) agentadapters.ProbeResult {
		t.Fatalf("probe override called for %q, want dev override synthetic probe", id)
		return agentadapters.ProbeResult{}
	})
	server := NewServer(logger, apiHandler)
	client := newAPITestClient(t, server)

	resp := mustRequestJSON[AgentAdapterProbeResponse](client, http.MethodPost, "/hecate/v1/agent-adapters/codex/probe", "")
	if resp.Data.Adapter.AuthStatus != agentadapters.AuthStatusUnauthenticated {
		t.Fatalf("adapter auth_status = %q, want synthetic unauthenticated", resp.Data.Adapter.AuthStatus)
	}
	if resp.Data.Health.Status != agentadapters.ProbeStatusAuthRequired {
		t.Fatalf("health status = %q, want synthetic auth_required", resp.Data.Health.Status)
	}
	if !strings.Contains(resp.Data.Health.Hint, "codex login") {
		t.Fatalf("health hint = %q, want codex login guidance", resp.Data.Health.Hint)
	}
}
