package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestChatWorkspaceTelemetryNeverRecordsPathsContentOrRevisions(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exporter)))
	defer provider.Shutdown(t.Context())
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	chatWorkspaceTracer = provider.Tracer("github.com/hecatehq/hecate/internal/api/chat-workspace")
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		chatWorkspaceTracer = otel.Tracer("github.com/hecatehq/hecate/internal/api/chat-workspace")
	})

	workspace := t.TempDir()
	runTestGit(t, workspace, "init")
	runTestGit(t, workspace, "config", "user.email", "hecate@example.test")
	runTestGit(t, workspace, "config", "user.name", "Hecate Test")
	trackedPath := filepath.Join(workspace, "private-token.txt")
	if err := os.WriteFile(trackedPath, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, workspace, "add", "private-token.txt")
	runTestGit(t, workspace, "commit", "-m", "initial")
	if err := os.WriteFile(trackedPath, []byte("secret-content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "api-key.env"), []byte("API_KEY=hidden\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store := chat.NewMemoryStore()
	const sessionID = "workspace_telemetry_privacy"
	if _, err := store.Create(context.Background(), chat.Session{ID: sessionID, AgentID: "codex", Workspace: workspace, Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewHandler(config.Config{}, logger, nil, nil, nil, nil)
	handler.SetAgentChatStore(store)
	client := newAPITestClient(t, NewServer(logger, handler))
	review := mustRequestJSON[ChatWorkspaceDiffResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/"+sessionID+"/workspace-diff", "")
	body := `{"paths":["private-token.txt"],"expected_revision":"` + review.Data.Discard.Revision + `"}`
	client.mustRequestStatus(http.StatusOK, http.MethodPost, "/hecate/v1/chat/sessions/"+sessionID+"/workspace-diff/revert", body)

	foundReview, foundDiscard := false, false
	for _, span := range exporter.GetSpans() {
		switch span.Name {
		case "chat.workspace.review":
			foundReview = true
		case "chat.workspace.discard":
			foundDiscard = true
		}
		for _, field := range span.Attributes {
			if field.Value.Type() != attribute.STRING {
				continue
			}
			value := field.Value.AsString()
			for _, secret := range []string{"private-token.txt", "api-key.env", "secret-content", "API_KEY", "sha256:"} {
				if strings.Contains(value, secret) {
					t.Fatalf("span %q attribute %q exposed %q in %q", span.Name, field.Key, secret, value)
				}
			}
		}
	}
	if !foundReview || !foundDiscard {
		t.Fatalf("workspace spans found review=%v discard=%v", foundReview, foundDiscard)
	}
}
