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
	"github.com/hecate/agent-runtime/internal/controlplane"
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
			Path:       "/usr/local/bin/codex-acp",
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

func TestAgentAdapterProbeDoesNotPromoteClaudeHandshakeToAuthOK(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, controlplane.NewMemoryStore(), nil, nil)
	apiHandler.SetSecretCipher(newTestCipherForAPI(t))
	apiHandler.SetAgentAdapterProbe(func(_ context.Context, id string) agentadapters.ProbeResult {
		if id != "claude_code" {
			t.Fatalf("probe called for %q, want claude_code", id)
		}
		return agentadapters.ProbeResult{
			AdapterID:  "claude_code",
			Status:     agentadapters.ProbeStatusReady,
			Stage:      agentadapters.ProbeStageReady,
			Path:       "/usr/local/bin/claude-agent-acp",
			DurationMS: 42,
		}
	})
	server := NewServer(logger, apiHandler)
	client := newAPITestClient(t, server)

	resp := mustRequestJSON[AgentAdapterProbeResponse](client, http.MethodPost, "/hecate/v1/agent-adapters/claude_code/probe", "")
	if resp.Data.Health.Status != agentadapters.ProbeStatusReady {
		t.Fatalf("health status = %q, want ready", resp.Data.Health.Status)
	}
	if resp.Data.Adapter.AuthStatus == agentadapters.AuthStatusOK {
		t.Fatalf("adapter auth_status = ok after bare ready probe; want onboarding to remain visible")
	}
	if resp.Data.Adapter.CredentialConfigured {
		t.Fatalf("credential_configured = true, want false")
	}
}

func TestAgentAdapterCredentialEndpointsStoreAndDeleteCredential(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := controlplane.NewMemoryStore()
	apiHandler := NewHandler(config.Config{}, logger, nil, store, nil, nil)
	apiHandler.SetSecretCipher(newTestCipherForAPI(t))
	probeCalls := 0
	apiHandler.agentAdapterEnvProbe = func(_ context.Context, id string, env []string) agentadapters.ProbeResult {
		probeCalls++
		if id != "claude_code" {
			t.Fatalf("probe id = %q, want claude_code", id)
		}
		if got, want := strings.Join(env, "\n"), claudeCodeOAuthTokenName+"=sk-valid-token-secret-1234567890"; got != want {
			t.Fatalf("probe env = %q, want %q", got, want)
		}
		return agentadapters.ProbeResult{
			AdapterID: "claude_code",
			Status:    agentadapters.ProbeStatusReady,
			Stage:     agentadapters.ProbeStageReady,
		}
	}
	server := NewServer(logger, apiHandler)
	client := newAPITestClient(t, server)

	set := mustRequestJSON[AgentAdapterCredentialResponse](client, http.MethodPut, "/hecate/v1/agent-adapters/claude_code/credentials", `{"value":"sk-valid-token-secret-1234567890"}`)
	if probeCalls != 1 {
		t.Fatalf("probe calls = %d, want 1", probeCalls)
	}
	if set.Object != "agent_adapter_credential" {
		t.Fatalf("object = %q, want agent_adapter_credential", set.Object)
	}
	if set.Data.AdapterID != "claude_code" || set.Data.Name != claudeCodeOAuthTokenName || !set.Data.Configured {
		t.Fatalf("set response = %#v, want configured claude token", set.Data)
	}
	if strings.Contains(set.Data.Preview, "sk-valid-token-secret-1234567890") {
		t.Fatalf("preview leaked full token: %q", set.Data.Preview)
	}

	state, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if got := len(state.AgentAdapterCredentials); got != 1 {
		t.Fatalf("credentials len = %d, want 1", got)
	}
	if state.AgentAdapterCredentials[0].ValueEncrypted == "sk-valid-token-secret-1234567890" {
		t.Fatal("credential was stored in plaintext")
	}

	env := apiHandler.agentAdapterCredentialEnv(context.Background(), "claude_code")
	if got, want := strings.Join(env, "\n"), claudeCodeOAuthTokenName+"=sk-valid-token-secret-1234567890"; got != want {
		t.Fatalf("credential env = %q, want %q", got, want)
	}

	deleted := mustRequestJSON[AgentAdapterCredentialResponse](client, http.MethodDelete, "/hecate/v1/agent-adapters/claude_code/credentials/CLAUDE_CODE_OAUTH_TOKEN", "")
	if deleted.Data.Configured {
		t.Fatalf("delete response configured = true, want false")
	}
	if env := apiHandler.agentAdapterCredentialEnv(context.Background(), "claude_code"); len(env) != 0 {
		t.Fatalf("credential env after delete = %#v, want empty", env)
	}
}

func TestAgentAdapterCredentialEndpointRejectsMalformedClaudeTokenBeforeProbe(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := controlplane.NewMemoryStore()
	apiHandler := NewHandler(config.Config{}, logger, nil, store, nil, nil)
	apiHandler.SetSecretCipher(newTestCipherForAPI(t))
	probeCalls := 0
	apiHandler.agentAdapterEnvProbe = func(context.Context, string, []string) agentadapters.ProbeResult {
		probeCalls++
		return agentadapters.ProbeResult{Status: agentadapters.ProbeStatusReady}
	}
	server := NewServer(logger, apiHandler)
	client := newAPITestClient(t, server)

	rec := client.mustRequestStatus(http.StatusBadRequest, http.MethodPut, "/hecate/v1/agent-adapters/claude_code/credentials", `{"value":"random text"}`)
	if !strings.Contains(rec.Body.String(), "does not look like a Claude Code setup token") {
		t.Fatalf("response = %s, want malformed-token guidance", rec.Body.String())
	}
	if probeCalls != 0 {
		t.Fatalf("probe calls = %d, want 0 for malformed token", probeCalls)
	}
	state, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if got := len(state.AgentAdapterCredentials); got != 0 {
		t.Fatalf("credentials len = %d, want 0", got)
	}
}

func TestAgentAdapterCredentialEndpointDoesNotStoreInvalidClaudeToken(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := controlplane.NewMemoryStore()
	apiHandler := NewHandler(config.Config{}, logger, nil, store, nil, nil)
	apiHandler.SetSecretCipher(newTestCipherForAPI(t))
	apiHandler.agentAdapterEnvProbe = func(_ context.Context, id string, env []string) agentadapters.ProbeResult {
		if id != "claude_code" {
			t.Fatalf("probe id = %q, want claude_code", id)
		}
		if got, want := strings.Join(env, "\n"), claudeCodeOAuthTokenName+"=sk-invalid-token-1234567890"; got != want {
			t.Fatalf("probe env = %q, want %q", got, want)
		}
		return agentadapters.ProbeResult{
			AdapterID: "claude_code",
			Status:    agentadapters.ProbeStatusAuthRequired,
			Stage:     agentadapters.ProbeStageNewSession,
			Hint:      "Claude Code rejected the token",
		}
	}
	server := NewServer(logger, apiHandler)
	client := newAPITestClient(t, server)

	rec := client.mustRequestStatus(http.StatusConflict, http.MethodPut, "/hecate/v1/agent-adapters/claude_code/credentials", `{"value":"sk-invalid-token-1234567890"}`)
	if !strings.Contains(rec.Body.String(), "did not save") {
		t.Fatalf("response = %s, want did not save message", rec.Body.String())
	}
	state, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if got := len(state.AgentAdapterCredentials); got != 0 {
		t.Fatalf("credentials len = %d, want 0", got)
	}
}

func TestAgentAdapterCredentialEndpointRequiresSecretStorage(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	apiHandler := NewHandler(config.Config{}, logger, nil, controlplane.NewMemoryStore(), nil, nil)
	server := NewServer(logger, apiHandler)
	client := newAPITestClient(t, server)

	rec := client.mustRequestStatus(http.StatusBadRequest, http.MethodPut, "/hecate/v1/agent-adapters/claude_code/credentials", `{"value":"token"}`)
	if !strings.Contains(rec.Body.String(), "secret storage") {
		t.Fatalf("response = %s, want secret storage error", rec.Body.String())
	}
}
