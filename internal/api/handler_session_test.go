package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/remoteruntime"
)

func TestSessionReportsLocalRuntimeHost(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewServer(logger, NewHandler(config.Config{
		Server: config.ServerConfig{
			RuntimeHostID:    "runtime_00112233445566778899aabb",
			RuntimeHostLabel: "MacBook",
			PublicURL:        "https://hecate.example.test",
		},
	}, logger, nil, nil, nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/hecate/v1/whoami", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response SessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	host := response.Data.RuntimeHost
	if host.ID != "runtime_00112233445566778899aabb" || host.Label != "MacBook" {
		t.Fatalf("runtime_host identity = %+v", host)
	}
	if host.RuntimeMode != "local" || host.OperatorAccess != "local_operator" {
		t.Fatalf("runtime_host posture = %+v", host)
	}
	if !host.LocalOnlyActionsAvailable {
		t.Fatal("local_only_actions_available = false, want true")
	}
	if host.PublicURL != "https://hecate.example.test" {
		t.Fatalf("public_url = %q", host.PublicURL)
	}
}

func TestSessionReportsTrustedRemoteSupervisionHost(t *testing.T) {
	const secret = "cloud-runtime-secret-123456"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewServer(logger, NewHandler(config.Config{
		Server: config.ServerConfig{
			RemoteRuntimeMode:   true,
			RemoteRuntimeSecret: secret,
			RuntimeHostID:       "runtime_00112233445566778899aabb",
			RuntimeHostLabel:    "MacBook",
		},
	}, logger, nil, nil, nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/hecate/v1/whoami", nil)
	req.Header.Set(remoteruntime.HeaderRuntimeSecret, secret)
	req.Header.Set(remoteruntime.HeaderActorID, "actor_1")
	req.Header.Set(remoteruntime.HeaderOrgID, "org_1")
	req.Header.Set(remoteruntime.HeaderProjectID, "proj_1")
	req.Header.Set(remoteruntime.HeaderRuntimeID, "remote_runtime_1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response SessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	host := response.Data.RuntimeHost
	if host.ID != "remote_runtime_1" {
		t.Fatalf("runtime_host.id = %q, want trusted remote runtime id", host.ID)
	}
	if host.Label != "MacBook" || host.RuntimeMode != "remote_runtime" || host.OperatorAccess != "remote_supervision" {
		t.Fatalf("runtime_host posture = %+v", host)
	}
	if host.LocalOnlyActionsAvailable {
		t.Fatal("local_only_actions_available = true, want false")
	}
}

func TestSessionAlwaysReportsRuntimeHostIdentity(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewServer(logger, NewHandler(config.Config{}, logger, nil, nil, nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/hecate/v1/whoami", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var response SessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Data.RuntimeHost.ID == "" || response.Data.RuntimeHost.Label == "" {
		t.Fatalf("runtime_host = %+v, want non-empty identity", response.Data.RuntimeHost)
	}
}
