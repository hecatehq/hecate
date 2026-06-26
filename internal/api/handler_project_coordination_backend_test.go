package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
)

func TestProjectCoordinationBackendStatus_DefaultHecateAuthoritative(t *testing.T) {
	handler := &Handler{config: config.Config{
		Projects: config.ProjectsConfig{Backend: "sqlite"},
	}}

	status := handler.projectCoordinationBackendStatus()
	if status.ConfiguredBackend != "hecate" || status.AuthoritativeBackend != "hecate" || status.StorageBackend != "sqlite" {
		t.Fatalf("status = %+v, want Hecate authoritative over sqlite project storage", status)
	}
	if !status.CairnlineBridgeReady || status.CairnlineAuthoritative || status.ReadModelSwitchReady || status.WriteAdapterReady || len(status.Warnings) != 0 {
		t.Fatalf("status = %+v, want bridge-ready but inactive Cairnline adapter flags", status)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineConfiguredMissingSources(t *testing.T) {
	handler := &Handler{config: config.Config{
		Projects: config.ProjectsConfig{
			Backend:             "sqlite",
			CoordinationBackend: "cairnline",
		},
	}}

	status := handler.projectCoordinationBackendStatus()
	if status.ConfiguredBackend != "cairnline" || status.AuthoritativeBackend != "hecate" || status.Status != "cairnline_configured_read_adapter_missing_sources" {
		t.Fatalf("status = %+v, want configured Cairnline with missing read-adapter sources", status)
	}
	if status.CairnlineAuthoritative || status.ReadModelSwitchReady || status.WriteAdapterReady || len(status.Warnings) == 0 {
		t.Fatalf("status = %+v, want read adapter missing-source warnings", status)
	}
}

func TestProjectCoordinationBackendStatus_CairnlineConfiguredOperationsReadRouteReady(t *testing.T) {
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{
			Backend:             "sqlite",
			CoordinationBackend: "cairnline",
		},
	}, quietLogger(), nil, nil, nil, nil)

	status := handler.projectCoordinationBackendStatus()
	if status.ConfiguredBackend != "cairnline" || status.AuthoritativeBackend != "hecate" || status.Status != "cairnline_operations_read_route_ready" {
		t.Fatalf("status = %+v, want configured Cairnline with operations read route ready", status)
	}
	if status.CairnlineAuthoritative || !status.ReadModelSwitchReady || status.WriteAdapterReady {
		t.Fatalf("status = %+v, want read adapter ready but Hecate still authoritative", status)
	}
	if len(status.Warnings) == 0 {
		t.Fatalf("status = %+v, want write-adapter/migration warning", status)
	}
}

func TestProjectCoordinationBackendStatusRoute(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{
		Projects: config.ProjectsConfig{
			Backend:             "sqlite",
			CoordinationBackend: "cairnline",
		},
	}
	server := newTestHTTPHandlerForProviders(logger, nil, cfg)

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/backend-status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("backend status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}

	var response ProjectCoordinationBackendStatusEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode backend status: %v", err)
	}
	if response.Object != "project_coordination_backend_status" || response.Data.ConfiguredBackend != "cairnline" || response.Data.AuthoritativeBackend != "hecate" {
		t.Fatalf("response = %+v, want Cairnline configured but Hecate authoritative", response)
	}
	if !response.Data.ReadModelSwitchReady || response.Data.Status != "cairnline_operations_read_route_ready" {
		t.Fatalf("response = %+v, want Cairnline operations read route ready for fully wired test handler", response)
	}
}
