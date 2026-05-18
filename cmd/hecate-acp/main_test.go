package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hecate/agent-runtime/internal/acp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestGatewayURLFromRuntimeFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hecate.runtime.json")
	if err := os.WriteFile(path, []byte(`{"base_url":"http://127.0.0.1:52341","port":52341}`), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	got, err := gatewayURLFromRuntimeFile(path)
	if err != nil {
		t.Fatalf("gatewayURLFromRuntimeFile() error = %v", err)
	}
	if got != "http://127.0.0.1:52341" {
		t.Fatalf("gatewayURLFromRuntimeFile() = %q", got)
	}
}

func TestGatewayURLFromRuntimeFileRejectsInvalidURL(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hecate.runtime.json")
	if err := os.WriteFile(path, []byte(`{"base_url":"file:///tmp/hecate"}`), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	if _, err := gatewayURLFromRuntimeFile(path); err == nil {
		t.Fatal("gatewayURLFromRuntimeFile() error = nil, want invalid URL error")
	}
}

func TestDiscoverGatewayURLFromRuntimePathsUsesFirstHealthyState(t *testing.T) {
	dir := t.TempDir()
	stalePath := filepath.Join(dir, "stale.json")
	healthyPath := filepath.Join(dir, "healthy.json")
	if err := os.WriteFile(stalePath, []byte(`{"base_url":"http://127.0.0.1:1111"}`), 0o600); err != nil {
		t.Fatalf("write stale state: %v", err)
	}
	if err := os.WriteFile(healthyPath, []byte(`{"base_url":"http://127.0.0.1:2222"}`), 0o600); err != nil {
		t.Fatalf("write healthy state: %v", err)
	}

	got := discoverGatewayURLFromRuntimePaths([]string{
		filepath.Join(dir, "missing.json"),
		stalePath,
		healthyPath,
	}, func(baseURL string) bool {
		return baseURL == "http://127.0.0.1:2222"
	})

	if got != "http://127.0.0.1:2222" {
		t.Fatalf("discoverGatewayURLFromRuntimePaths() = %q", got)
	}
}

func TestHecateRuntimeCandidatePathsIncludesTrustedDataDirOnly(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "custom-data")
	t.Setenv("GATEWAY_DATA_DIR", dataDir)

	paths := hecateRuntimeCandidatePaths()
	joined := strings.Join(paths, "\n")
	if !strings.Contains(joined, filepath.Join(dataDir, hecateRuntimeFile)) {
		t.Fatalf("candidate paths = %#v, want GATEWAY_DATA_DIR state", paths)
	}
	if strings.Contains(joined, filepath.Join(".data", hecateRuntimeFile)) {
		t.Fatalf("candidate paths = %#v, want no cwd-relative runtime state", paths)
	}
}

func TestGatewayHTTPClientListModels(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o-mini"},{"id":"claude-sonnet-4-20250514"},{"id":""}]}`))
	}))
	defer srv.Close()

	client, err := newGatewayHTTPClient(bridgeConfig{
		GatewayURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("newGatewayHTTPClient() error = %v", err)
	}
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if strings.Join(models, ",") != "gpt-4o-mini,claude-sonnet-4-20250514" {
		t.Fatalf("models = %#v", models)
	}
}

func TestGatewayHTTPClientHealth(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Fatalf("path = %q, want /healthz", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client, err := newGatewayHTTPClient(bridgeConfig{GatewayURL: srv.URL})
	if err != nil {
		t.Fatalf("newGatewayHTTPClient() error = %v", err)
	}
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}
}

func TestGatewayHTTPClientProviderStatuses(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hecate/v1/providers/status" {
			t.Fatalf("path = %q, want /hecate/v1/providers/status", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"provider_status","data":[{"name":"ollama","status":"blocked","routing_blocked_reason":"no_models","readiness":{"message":"Load at least one model."}}]}`))
	}))
	defer srv.Close()

	client, err := newGatewayHTTPClient(bridgeConfig{GatewayURL: srv.URL})
	if err != nil {
		t.Fatalf("newGatewayHTTPClient() error = %v", err)
	}
	statuses, err := client.ProviderStatuses(context.Background())
	if err != nil {
		t.Fatalf("ProviderStatuses() error = %v", err)
	}
	if len(statuses) != 1 || statuses[0].Name != "ollama" || statuses[0].Readiness.Message != "Load at least one model." {
		t.Fatalf("statuses = %+v", statuses)
	}
}

func TestRunAuthSetupReady(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"llama3.1:8b","metadata":{"readiness":{"ready":true,"routing_ready":true}}}]}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	var stdout bytes.Buffer
	err := runAuthSetup(context.Background(), &stdout, bridgeConfig{GatewayURL: srv.URL})
	if err != nil {
		t.Fatalf("runAuthSetup() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "ACP setup is ready") {
		t.Fatalf("output = %q", output)
	}
}

func TestRunAuthSetupRejectsListedUnreadyModels(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"llama3.1:8b","metadata":{"readiness":{"ready":false,"routing_ready":false}}}]}`))
		case "/hecate/v1/providers/status":
			_, _ = w.Write([]byte(`{"object":"provider_status","data":[{"name":"ollama","status":"blocked","readiness":{"message":"Provider is disabled."}}]}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	var stdout bytes.Buffer
	err := runAuthSetup(context.Background(), &stdout, bridgeConfig{GatewayURL: srv.URL})
	if !errors.Is(err, errAuthSetupFailed) {
		t.Fatalf("runAuthSetup() error = %v, want errAuthSetupFailed", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "1 listed, none ready for routing") || !strings.Contains(output, "Provider is disabled.") {
		t.Fatalf("output = %q", output)
	}
}

func TestRunAuthSetupExplainsMissingModels(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/hecate/v1/providers/status":
			_, _ = w.Write([]byte(`{"object":"provider_status","data":[{"name":"ollama","status":"blocked","readiness":{"message":"Start Ollama and load a model."}}]}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	var stdout bytes.Buffer
	err := runAuthSetup(context.Background(), &stdout, bridgeConfig{GatewayURL: srv.URL})
	if !errors.Is(err, errAuthSetupFailed) {
		t.Fatalf("runAuthSetup() error = %v, want errAuthSetupFailed", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "Models: none available") || !strings.Contains(output, "Start Ollama and load a model.") {
		t.Fatalf("output = %q", output)
	}
}

func TestGatewayHTTPClientCreateAgentLoopTask(t *testing.T) {
	t.Parallel()

	var createdBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/hecate/v1/tasks":
			if err := json.NewDecoder(r.Body).Decode(&createdBody); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			_, _ = w.Write([]byte(`{"object":"task","data":{"id":"task-123"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/hecate/v1/tasks/task-123/start":
			_, _ = w.Write([]byte(`{"object":"task_run","data":{"id":"run-456"}}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client, err := newGatewayHTTPClient(bridgeConfig{GatewayURL: srv.URL})
	if err != nil {
		t.Fatalf("newGatewayHTTPClient() error = %v", err)
	}
	result, err := client.CreateAgentLoopTask(context.Background(), acp.CreateTaskRequest{
		Model:            "gpt-4o-mini",
		WorkingDirectory: "/repo",
		Prompt:           "fix tests",
	})
	if err != nil {
		t.Fatalf("CreateAgentLoopTask() error = %v", err)
	}
	if result.TaskID != "task-123" || result.RunID != "run-456" {
		t.Fatalf("result = %+v", result)
	}
	if createdBody["execution_kind"] != "agent_loop" || createdBody["execution_profile"] != "coding_agent" {
		t.Fatalf("create body = %+v", createdBody)
	}
	if createdBody["requested_model"] != "gpt-4o-mini" || createdBody["working_directory"] != "/repo" || createdBody["prompt"] != "fix tests" {
		t.Fatalf("create body = %+v", createdBody)
	}
}

func TestGatewayHTTPClientContinueAgentLoopTask(t *testing.T) {
	t.Parallel()

	var body map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/hecate/v1/tasks/task-123/runs/run-456/continue" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode continue body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"task_run","data":{"id":"run-789"}}`))
	}))
	defer srv.Close()

	client, err := newGatewayHTTPClient(bridgeConfig{GatewayURL: srv.URL})
	if err != nil {
		t.Fatalf("newGatewayHTTPClient() error = %v", err)
	}
	runID, err := client.ContinueAgentLoopTask(context.Background(), "task-123", "run-456", "next prompt")
	if err != nil {
		t.Fatalf("ContinueAgentLoopTask() error = %v", err)
	}
	if runID != "run-789" {
		t.Fatalf("runID = %q, want run-789", runID)
	}
	if body["prompt"] != "next prompt" {
		t.Fatalf("continue body = %+v", body)
	}
}

func TestGatewayHTTPClientResolveApproval(t *testing.T) {
	t.Parallel()

	var body map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/hecate/v1/tasks/task-123/approvals/approval-456/resolve" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode resolve body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"task_approval","data":{"id":"approval-456","status":"approved"}}`))
	}))
	defer srv.Close()

	client, err := newGatewayHTTPClient(bridgeConfig{GatewayURL: srv.URL})
	if err != nil {
		t.Fatalf("newGatewayHTTPClient() error = %v", err)
	}
	if err := client.ResolveApproval(context.Background(), "task-123", "run-ignored", "approval-456", acp.ApprovalAllow); err != nil {
		t.Fatalf("ResolveApproval() error = %v", err)
	}
	if body["decision"] != "approve" {
		t.Fatalf("resolve body = %+v", body)
	}
}

func TestGatewayHTTPClientStreamRunEvents(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hecate/v1/tasks/task-1/runs/run-1/stream" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: snapshot\n")
		fmt.Fprint(w, `data: {"object":"task_run_stream_event","data":{"sequence":1,"event_type":"tool.completed","terminal":false}}`)
		fmt.Fprint(w, "\n\n")
		fmt.Fprint(w, "event: done\n")
		fmt.Fprint(w, `data: {"object":"task_run_stream_event","data":{"sequence":2,"event_type":"run.finished","terminal":true}}`)
		fmt.Fprint(w, "\n\n")
	}))
	defer srv.Close()

	client, err := newGatewayHTTPClient(bridgeConfig{GatewayURL: srv.URL})
	if err != nil {
		t.Fatalf("newGatewayHTTPClient() error = %v", err)
	}
	events, err := client.StreamRunEvents(context.Background(), "task-1", "run-1")
	if err != nil {
		t.Fatalf("StreamRunEvents() error = %v", err)
	}
	var got []string
	for event := range events {
		got = append(got, event.Type)
	}
	if strings.Join(got, ",") != "tool.completed,run.finished" {
		t.Fatalf("events = %#v", got)
	}
}

func TestGatewayHTTPClientInjectsTraceContext(t *testing.T) {
	old := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	t.Cleanup(func() {
		otel.SetTextMapPropagator(old)
	})

	const wantTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	seen := map[string]http.Header{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o-mini"}]}`))
		case "/hecate/v1/tasks/task-123/runs/run-456/continue":
			_, _ = w.Write([]byte(`{"object":"task_run","data":{"id":"run-789"}}`))
		case "/hecate/v1/tasks/task-123/runs/run-789/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `data: {"object":"task_run_stream_event","data":{"event_type":"run.finished","terminal":true}}`)
			fmt.Fprint(w, "\n\n")
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client, err := newGatewayHTTPClient(bridgeConfig{GatewayURL: srv.URL})
	if err != nil {
		t.Fatalf("newGatewayHTTPClient() error = %v", err)
	}
	ctx := testTraceContext(t)
	if _, err := client.ListModels(ctx); err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if _, err := client.ContinueAgentLoopTask(ctx, "task-123", "run-456", "next prompt"); err != nil {
		t.Fatalf("ContinueAgentLoopTask() error = %v", err)
	}
	events, err := client.StreamRunEvents(ctx, "task-123", "run-789")
	if err != nil {
		t.Fatalf("StreamRunEvents() error = %v", err)
	}
	for range events {
	}

	for _, requestPath := range []string{"/v1/models", "/hecate/v1/tasks/task-123/runs/run-456/continue", "/hecate/v1/tasks/task-123/runs/run-789/stream"} {
		header, ok := seen[requestPath]
		if !ok {
			t.Fatalf("missing request %s in %#v", requestPath, seen)
		}
		if got := header.Get("traceparent"); got != wantTraceparent {
			t.Fatalf("%s traceparent = %q, want %q", requestPath, got, wantTraceparent)
		}
		if got := header.Get("baggage"); got != "tenant=local" {
			t.Fatalf("%s baggage = %q, want tenant=local", requestPath, got)
		}
	}
}

func TestBridgeOTelFromEnvUsesACPServiceIdentity(t *testing.T) {
	t.Setenv("GATEWAY_OTEL_ENDPOINT", "http://collector:4318")
	t.Setenv("GATEWAY_OTEL_TRACES_ENABLED", "true")
	t.Setenv("GATEWAY_OTEL_SERVICE_NAME", "hecate-gateway")

	cfg := bridgeOTelFromEnv()
	if !cfg.TracesEnabled {
		t.Fatal("TracesEnabled = false, want true")
	}
	if cfg.TracesEndpoint != "http://collector:4318/v1/traces" {
		t.Fatalf("TracesEndpoint = %q", cfg.TracesEndpoint)
	}
	if cfg.ServiceName != "hecate-acp" {
		t.Fatalf("ServiceName = %q, want hecate-acp", cfg.ServiceName)
	}
}

func TestRunInitializeOverStdio(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"llama3.1:8b"}]}`))
	}))
	defer srv.Close()

	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"0.1","clientCapabilities":{"permissions":{},"auth":{"terminal":true}}}}` + "\n")
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), input, &stdout, &stderr, bridgeConfig{
		GatewayURL:    srv.URL,
		AgentName:     "Hecate",
		AgentVersion:  "test",
		WorkspaceMode: "hecate-owned",
		ApprovalRoute: "editor",
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(stderr.String(), "hecate-acp: started") {
		t.Fatalf("stderr missing startup line: %q", stderr.String())
	}
	var resp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code int `json:"code"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, stdout.String())
	}
	if resp.Error != nil {
		t.Fatalf("response error = %+v", resp.Error)
	}
	var result struct {
		AvailableModels []struct {
			ID string `json:"id"`
		} `json:"availableModels"`
		AuthMethods []struct {
			ID   string   `json:"id"`
			Type string   `json:"type"`
			Args []string `json:"args"`
		} `json:"authMethods"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(result.AvailableModels) != 1 || result.AvailableModels[0].ID != "llama3.1:8b" {
		t.Fatalf("availableModels = %#v", result.AvailableModels)
	}
	if len(result.AuthMethods) != 1 || result.AuthMethods[0].ID != "hecate-setup" || result.AuthMethods[0].Type != "terminal" || strings.Join(result.AuthMethods[0].Args, " ") != "auth setup" {
		t.Fatalf("authMethods = %#v", result.AuthMethods)
	}
}

func testTraceContext(t *testing.T) context.Context {
	t.Helper()
	traceID, err := oteltrace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatalf("TraceIDFromHex() error = %v", err)
	}
	spanID, err := oteltrace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("SpanIDFromHex() error = %v", err)
	}
	ctx := oteltrace.ContextWithSpanContext(context.Background(), oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: oteltrace.FlagsSampled,
	}))
	bag, err := baggage.Parse("tenant=local")
	if err != nil {
		t.Fatalf("baggage.Parse() error = %v", err)
	}
	return baggage.ContextWithBaggage(ctx, bag)
}

func TestRunWritesParseError(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), strings.NewReader("{not-json}\n"), &stdout, &stderr, bridgeConfig{
		GatewayURL: defaultGatewayURL,
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	var resp struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, stdout.String())
	}
	if resp.Error == nil || resp.Error.Code != -32700 {
		t.Fatalf("error = %+v, want parse error", resp.Error)
	}
}
