package api

import (
	"context"
	"encoding/json"
	"errors"
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
				AgentInfo: &agentadapters.ProbeAgentInfo{
					Name:    "codex-acp-adapter",
					Title:   "Codex ACP Adapter",
					Version: "0.1.0-alpha.28",
				},
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
			if tc.name == "ready" {
				if resp.Data.AgentInfo == nil || resp.Data.AgentInfo.Name != "codex-acp-adapter" || resp.Data.AgentInfo.Version != "0.1.0-alpha.28" {
					t.Fatalf("AgentInfo = %#v, want initialized adapter metadata", resp.Data.AgentInfo)
				}
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

func TestAgentAdapterProbeAppliesLiveCapabilitiesToAdapterRow(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name             string
		adapterID        string
		result           agentadapters.ProbeResult
		wantAuthenticate bool
		wantLogout       bool
	}{
		{
			name:      "live capabilities can disable static authenticate",
			adapterID: "codex",
			result: agentadapters.ProbeResult{
				AdapterID:            "codex",
				Status:               agentadapters.ProbeStatusReady,
				Stage:                agentadapters.ProbeStageReady,
				CapabilitiesKnown:    true,
				SupportsAuthenticate: false,
				SupportsLogout:       false,
			},
			wantAuthenticate: false,
			wantLogout:       false,
		},
		{
			name:      "live capabilities can enable future adapter authenticate",
			adapterID: "cursor_agent",
			result: agentadapters.ProbeResult{
				AdapterID:            "cursor_agent",
				Status:               agentadapters.ProbeStatusReady,
				Stage:                agentadapters.ProbeStageReady,
				CapabilitiesKnown:    true,
				SupportsAuthenticate: true,
				SupportsLogout:       true,
			},
			wantAuthenticate: true,
			wantLogout:       true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
			apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
			apiHandler.SetAgentAdapterProbe(func(_ context.Context, id string) agentadapters.ProbeResult {
				if id != tc.adapterID {
					t.Fatalf("probe called for %q, want %s", id, tc.adapterID)
				}
				return tc.result
			})
			server := NewServer(logger, apiHandler)
			client := newAPITestClient(t, server)

			resp := mustRequestJSON[AgentAdapterProbeResponse](client, http.MethodPost, "/hecate/v1/agent-adapters/"+tc.adapterID+"/probe", "")
			if resp.Data.Adapter.SupportsAuthenticate != tc.wantAuthenticate {
				t.Fatalf("adapter supports_authenticate = %v, want %v", resp.Data.Adapter.SupportsAuthenticate, tc.wantAuthenticate)
			}
			if resp.Data.Adapter.SupportsLogout != tc.wantLogout {
				t.Fatalf("adapter supports_logout = %v, want %v", resp.Data.Adapter.SupportsLogout, tc.wantLogout)
			}
			if !resp.Data.Health.CapabilitiesKnown {
				t.Fatalf("health capabilities_known = false, want true")
			}
		})
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

func TestAgentAdapterLogoutEndpointReturnsResult(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	calls := 0
	apiHandler.SetAgentAdapterLogout(func(_ context.Context, id string) (agentadapters.LogoutResult, error) {
		calls++
		if id != "codex" {
			t.Fatalf("logout called for %q, want codex", id)
		}
		return agentadapters.LogoutResult{
			AdapterID:  id,
			Status:     agentadapters.LogoutStatusLoggedOut,
			Path:       "/usr/local/bin/codex-acp-adapter",
			DurationMS: 12,
		}, nil
	})
	server := NewServer(logger, apiHandler)
	client := newAPITestClient(t, server)

	resp := mustRequestJSON[AgentAdapterLogoutResponse](client, http.MethodPost, "/hecate/v1/agent-adapters/codex/logout", "")
	if calls != 1 {
		t.Fatalf("logout call count = %d, want 1", calls)
	}
	if resp.Object != "agent_adapter_logout" {
		t.Fatalf("object = %q, want agent_adapter_logout", resp.Object)
	}
	if resp.Data.AdapterID != "codex" || resp.Data.Status != agentadapters.LogoutStatusLoggedOut {
		t.Fatalf("logout response = %#v, want codex logged_out", resp.Data)
	}
	if resp.Data.Path != "/usr/local/bin/codex-acp-adapter" || resp.Data.DurationMS != 12 {
		t.Fatalf("logout diagnostics = %#v, want path + duration", resp.Data)
	}
}

func TestAgentAdapterAuthenticateEndpointReturnsResult(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	calls := 0
	apiHandler.SetAgentAdapterAuthenticate(func(_ context.Context, id string) (agentadapters.AuthenticateResult, error) {
		calls++
		if id != "codex" {
			t.Fatalf("authenticate called for %q, want codex", id)
		}
		return agentadapters.AuthenticateResult{
			AdapterID:  id,
			Status:     agentadapters.AuthenticateStatusAuthenticated,
			MethodID:   agentadapters.ACPAuthMethodAgentLogin,
			Path:       "/usr/local/bin/codex-acp-adapter",
			DurationMS: 12,
		}, nil
	})
	server := NewServer(logger, apiHandler)
	client := newAPITestClient(t, server)

	resp := mustRequestJSON[AgentAdapterAuthenticateResponse](client, http.MethodPost, "/hecate/v1/agent-adapters/codex/authenticate", "")
	if calls != 1 {
		t.Fatalf("authenticate call count = %d, want 1", calls)
	}
	if resp.Object != "agent_adapter_authenticate" {
		t.Fatalf("object = %q, want agent_adapter_authenticate", resp.Object)
	}
	if resp.Data.AdapterID != "codex" || resp.Data.Status != agentadapters.AuthenticateStatusAuthenticated || resp.Data.MethodID != agentadapters.ACPAuthMethodAgentLogin {
		t.Fatalf("authenticate response = %#v, want codex authenticated", resp.Data)
	}
	if resp.Data.Path != "/usr/local/bin/codex-acp-adapter" || resp.Data.DurationMS != 12 {
		t.Fatalf("authenticate diagnostics = %#v, want path + duration", resp.Data)
	}
}

func TestAgentAdapterLogoutMapsFailureToUnavailable(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	apiHandler.SetAgentAdapterLogout(func(context.Context, string) (agentadapters.LogoutResult, error) {
		return agentadapters.LogoutResult{}, errors.New("adapter refused logout")
	})
	server := NewServer(logger, apiHandler)
	client := newAPITestClient(t, server)

	recorder := client.mustRequestStatus(http.StatusBadGateway, http.MethodPost, "/hecate/v1/agent-adapters/codex/logout", "")
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	errBody, _ := body["error"].(map[string]any)
	if errBody == nil {
		t.Fatalf("response missing error body: %s", recorder.Body.String())
	}
	if typ, _ := errBody["type"].(string); typ != errCodeAgentAdapterUnavailable {
		t.Fatalf("error.type = %q, want %q", typ, errCodeAgentAdapterUnavailable)
	}
	if msg, _ := errBody["message"].(string); !strings.Contains(msg, "adapter refused logout") {
		t.Fatalf("error.message = %q, want logout diagnostic", msg)
	}
}

func TestAgentAdapterAuthenticateMapsFailureToUnavailable(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	apiHandler.SetAgentAdapterAuthenticate(func(context.Context, string) (agentadapters.AuthenticateResult, error) {
		return agentadapters.AuthenticateResult{}, errors.New("adapter refused login")
	})
	server := NewServer(logger, apiHandler)
	client := newAPITestClient(t, server)

	recorder := client.mustRequestStatus(http.StatusBadGateway, http.MethodPost, "/hecate/v1/agent-adapters/codex/authenticate", "")
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	errBody, _ := body["error"].(map[string]any)
	if errBody == nil {
		t.Fatalf("response missing error body: %s", recorder.Body.String())
	}
	if typ, _ := errBody["type"].(string); typ != errCodeAgentAdapterUnavailable {
		t.Fatalf("error.type = %q, want %q", typ, errCodeAgentAdapterUnavailable)
	}
	if msg, _ := errBody["message"].(string); !strings.Contains(msg, "adapter refused login") {
		t.Fatalf("error.message = %q, want authenticate diagnostic", msg)
	}
}

func TestAgentAdapterLogout404OnUnknownAdapter(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	logoutCalls := 0
	apiHandler.SetAgentAdapterLogout(func(context.Context, string) (agentadapters.LogoutResult, error) {
		logoutCalls++
		return agentadapters.LogoutResult{}, nil
	})
	server := NewServer(logger, apiHandler)
	client := newAPITestClient(t, server)

	recorder := client.mustRequestStatus(http.StatusNotFound, http.MethodPost, "/hecate/v1/agent-adapters/no-such-adapter/logout", "")
	if logoutCalls != 0 {
		t.Fatalf("logout call count = %d, want 0 (404 must short-circuit)", logoutCalls)
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

func TestAgentAdapterAuthenticate404OnUnknownAdapter(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	authenticateCalls := 0
	apiHandler.SetAgentAdapterAuthenticate(func(context.Context, string) (agentadapters.AuthenticateResult, error) {
		authenticateCalls++
		return agentadapters.AuthenticateResult{}, nil
	})
	server := NewServer(logger, apiHandler)
	client := newAPITestClient(t, server)

	recorder := client.mustRequestStatus(http.StatusNotFound, http.MethodPost, "/hecate/v1/agent-adapters/no-such-adapter/authenticate", "")
	if authenticateCalls != 0 {
		t.Fatalf("authenticate call count = %d, want 0 (404 must short-circuit)", authenticateCalls)
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
