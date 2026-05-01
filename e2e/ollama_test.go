//go:build e2e && ollama

// Ollama-backed end-to-end tests.
//
// These tests require a running Ollama instance.  Point them at it with
// OLLAMA_HOST (default: http://127.0.0.1:11434).  The model is pulled
// automatically on first run if it is not already present.
//
// Quick-start with Docker (one-time):
//
//	docker run -d -p 11434:11434 --name ollama ollama/ollama
//	docker exec ollama ollama pull smollm2:135m
//
// Then run the suite:
//
//	go test -tags 'e2e ollama' ./e2e/... -v -timeout 10m
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

// smollm2:135m is ~91 MB — the smallest Ollama chat model worth using.
const ollamaModel = "smollm2:135m"

var (
	suiteOllamaURL string    // base URL of the Ollama server
	suiteOTLP      *otlpSink // in-process OTLP HTTP receiver
	suiteGateway   string    // base URL of the running gateway
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	// ── 1. Resolve Ollama ─────────────────────────────────────────────────────
	base := resolveOllamaBase()
	if base == "" {
		log.Println("Ollama not reachable — start it first (see package doc) and re-run")
		os.Exit(0) // skip, not fail
	}
	suiteOllamaURL = base

	// ── 2. Ensure the model is present ────────────────────────────────────────
	pullCtx, cancelPull := context.WithTimeout(ctx, 5*time.Minute)
	defer cancelPull()
	if err := ensureOllamaModel(pullCtx, base, ollamaModel); err != nil {
		log.Fatalf("ensure model %s: %v", ollamaModel, err)
	}

	// ── 3. In-process OTLP sink ───────────────────────────────────────────────
	suiteOTLP = newOTLPSink()

	// ── 4. Gateway binary ─────────────────────────────────────────────────────
	var err error
	suiteGateway, err = startHecateProcess(
		"PROVIDER_OLLAMA_BASE_URL="+suiteOllamaURL,
		"PROVIDER_OLLAMA_KIND=local",
		"PROVIDER_OLLAMA_DEFAULT_MODEL="+ollamaModel,
		"GATEWAY_DEFAULT_MODEL="+ollamaModel,
		"GATEWAY_OTEL_TRACES_ENABLED=true",
		"GATEWAY_OTEL_TRACES_ENDPOINT=http://"+suiteOTLP.addr(),
		"GATEWAY_OTEL_METRICS_ENABLED=true",
		"GATEWAY_OTEL_METRICS_ENDPOINT=http://"+suiteOTLP.addr(),
		"GATEWAY_OTEL_METRICS_INTERVAL=2s",
	)
	if err != nil {
		log.Fatalf("start gateway: %v", err)
	}

	os.Exit(m.Run())
}

// ─── Codex tests ─────────────────────────────────────────────────────────────

func TestOllamaCodexNonStreaming(t *testing.T) {
	t.Parallel()
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Reply with one word: pong"}]}`, ollamaModel)
	resp := postJSON(t, suiteGateway+"/v1/chat/completions", body, map[string]string{
		"Authorization": "Bearer test-token",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", resp.StatusCode, bodyString(resp))
	}
	var result map[string]interface{}
	decodeJSON(t, resp.Body, &result)
	choices, _ := result["choices"].([]interface{})
	if len(choices) == 0 {
		t.Fatalf("no choices in response: %v", result)
	}
	content, _ := choices[0].(map[string]interface{})["message"].(map[string]interface{})["content"].(string)
	if strings.TrimSpace(content) == "" {
		t.Fatal("empty content in response")
	}
	t.Logf("model replied (Codex): %q", content)
}

func TestOllamaCodexStreaming(t *testing.T) {
	t.Parallel()
	body := fmt.Sprintf(`{"model":%q,"stream":true,"messages":[{"role":"user","content":"Say hi briefly"}]}`, ollamaModel)
	resp := postJSON(t, suiteGateway+"/v1/chat/completions", body, map[string]string{
		"Authorization": "Bearer test-token",
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %s", ct)
	}
	events := readSSE(t, resp)
	if len(events) == 0 {
		t.Fatal("no SSE events received")
	}
	if events[len(events)-1].Data != "[DONE]" {
		t.Fatalf("last SSE event should be [DONE], got %q", events[len(events)-1].Data)
	}
	t.Logf("received %d SSE chunks (Codex streaming)", len(events))
}

// ─── Claude Code tests ───────────────────────────────────────────────────────

func TestOllamaClaudeCodeNonStreaming(t *testing.T) {
	t.Parallel()
	body := fmt.Sprintf(`{"model":%q,"max_tokens":64,"messages":[{"role":"user","content":"Reply with one word: pong"}]}`, ollamaModel)
	resp := postJSON(t, suiteGateway+"/v1/messages", body, map[string]string{
		"x-api-key":         "test-token",
		"anthropic-version": "2023-06-01",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", resp.StatusCode, bodyString(resp))
	}
	var result map[string]interface{}
	decodeJSON(t, resp.Body, &result)
	if result["type"] != "message" {
		t.Fatalf("expected type=message, got %v", result["type"])
	}
	content, _ := result["content"].([]interface{})
	if len(content) == 0 {
		t.Fatal("empty content array in Anthropic response")
	}
	text, _ := content[0].(map[string]interface{})["text"].(string)
	if strings.TrimSpace(text) == "" {
		t.Fatal("empty text in Anthropic content block")
	}
	t.Logf("model replied (Claude Code): %q", text)
}

func TestOllamaClaudeCodeStreaming(t *testing.T) {
	t.Parallel()
	body := fmt.Sprintf(`{"model":%q,"max_tokens":64,"stream":true,"messages":[{"role":"user","content":"Say hi briefly"}]}`, ollamaModel)
	resp := postJSON(t, suiteGateway+"/v1/messages", body, map[string]string{
		"x-api-key":         "test-token",
		"anthropic-version": "2023-06-01",
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %s", ct)
	}
	events := readSSE(t, resp)
	if len(events) == 0 {
		t.Fatal("no SSE events from /v1/messages")
	}

	var first map[string]interface{}
	if err := json.Unmarshal([]byte(events[0].Data), &first); err != nil {
		t.Fatalf("parse first SSE event: %v", err)
	}
	if first["type"] != "message_start" {
		t.Fatalf("expected message_start, got %v", first["type"])
	}
	t.Logf("received %d SSE events (Claude Code streaming)", len(events))
}

// ─── OTel export tests ───────────────────────────────────────────────────────

// TestOllamaTracesExported makes a real request and confirms the gateway
// exported a "gateway.request" span to the in-process OTLP sink.
func TestOllamaTracesExported(t *testing.T) {
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ping"}]}`, ollamaModel)
	resp := postJSON(t, suiteGateway+"/v1/chat/completions", body, map[string]string{
		"Authorization": "Bearer test-token",
	})
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("request returned %d — skipping OTel assertion", resp.StatusCode)
	}

	if !suiteOTLP.waitForSpan("gateway.request", 8*time.Second) {
		t.Fatalf("no span %q within 8 s; got: %v", "gateway.request", suiteOTLP.spanNames())
	}
	t.Logf("spans: %v", suiteOTLP.spanNames())
}

// TestOllamaMetricsExported confirms that token-usage metrics are pushed to
// the OTLP sink within the 2 s export interval configured in TestMain.
func TestOllamaMetricsExported(t *testing.T) {
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"count to three"}]}`, ollamaModel)
	resp := postJSON(t, suiteGateway+"/v1/chat/completions", body, map[string]string{
		"Authorization": "Bearer test-token",
	})
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("request returned %d — skipping OTel assertion", resp.StatusCode)
	}

	if !suiteOTLP.waitForMetric("gen_ai.client.tokens", 10*time.Second) {
		t.Fatalf("no gen_ai.client.tokens.* metric within 10 s; got: %v", suiteOTLP.metricNames())
	}
	t.Logf("metrics: %v", suiteOTLP.metricNames())
}

// ─── Ollama client helpers ────────────────────────────────────────────────────

// resolveOllamaBase returns the Ollama base URL if the server is reachable,
// or "" if not.
func resolveOllamaBase() string {
	base := os.Getenv("OLLAMA_HOST")
	if base == "" {
		base = "http://127.0.0.1:11434"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	resp.Body.Close()
	return base
}

// ensureOllamaModel pulls model if it is not already in the Ollama library.
func ensureOllamaModel(ctx context.Context, base, model string) error {
	if ollamaHasModel(base, model) {
		return nil
	}
	log.Printf("pulling Ollama model %s (first run — may take a minute)…", model)
	return ollamaPull(ctx, base, model)
}

func ollamaHasModel(base, model string) bool {
	resp, err := http.Get(base + "/api/tags") //nolint:noctx
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false
	}
	for _, m := range body.Models {
		// Ollama names may carry a ":latest" suffix; match by prefix.
		if m.Name == model || strings.HasPrefix(m.Name, model+":") || strings.HasPrefix(model, strings.Split(m.Name, ":")[0]) {
			return true
		}
	}
	return false
}

func ollamaPull(ctx context.Context, base, model string) error {
	payload, _ := json.Marshal(map[string]interface{}{"model": model, "stream": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/pull", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pull returned %d", resp.StatusCode)
	}
	// Drain the NDJSON progress stream until EOF.
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		// Each line is a JSON progress object; just discard.
	}
	return scanner.Err()
}

// ─── in-process OTLP HTTP sink ───────────────────────────────────────────────

type otlpSink struct {
	ln      net.Listener
	srv     *http.Server
	mu      sync.Mutex
	spans   []string
	metrics []string
}

func newOTLPSink() *otlpSink {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("otlpSink listen: %v", err)
	}
	s := &otlpSink{ln: ln}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/traces", s.handleTraces)
	mux.HandleFunc("/v1/metrics", s.handleMetrics)
	s.srv = &http.Server{Handler: mux}
	go s.srv.Serve(ln) //nolint:errcheck
	return s
}

func (s *otlpSink) addr() string { return s.ln.Addr().String() }

func (s *otlpSink) handleTraces(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req coltrace.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	for _, rs := range req.GetResourceSpans() {
		for _, ss := range rs.GetScopeSpans() {
			for _, span := range ss.GetSpans() {
				s.spans = append(s.spans, span.GetName())
			}
		}
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (s *otlpSink) handleMetrics(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req colmetrics.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	for _, rm := range req.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				s.metrics = append(s.metrics, m.GetName())
			}
		}
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (s *otlpSink) spanNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.spans))
	copy(out, s.spans)
	return out
}

func (s *otlpSink) metricNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.metrics))
	copy(out, s.metrics)
	return out
}

func (s *otlpSink) waitForSpan(substr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, name := range s.spanNames() {
			if strings.Contains(name, substr) {
				return true
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

func (s *otlpSink) waitForMetric(substr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, name := range s.metricNames() {
			if strings.Contains(name, substr) {
				return true
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// ─── gateway process lifecycle (TestMain-scoped, no *testing.T) ────────────

// startHecateProcess builds the gateway binary once, starts it with the
// given extra env vars plus a fixed admin token (so existing "Bearer
// test-token" headers in tests authenticate) and a per-process temp data
// dir for the bootstrap file. Waits for /healthz, returns the base URL.
// The process is intentionally not tracked for cleanup: it is killed by
// the OS when the test binary exits.
func startHecateProcess(extraEnv ...string) (string, error) {
	bin, err := buildGatewayBin()
	if err != nil {
		return "", fmt.Errorf("build: %w", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	addr := ln.Addr().String()
	ln.Close()
	baseURL := "http://" + addr

	// See gateway_test.go for the auth/env rationale: explicit token + a
	// temp data dir so the bootstrap file lands somewhere ephemeral. We use
	// MkdirTemp here (not t.TempDir) because this helper is called from
	// TestMain where no *testing.T is in scope.
	dataDir, err := os.MkdirTemp("", "gateway-e2e-data-*")
	if err != nil {
		return "", fmt.Errorf("mkdir temp data dir: %w", err)
	}
	env := append(os.Environ(),
		"GATEWAY_ADDRESS="+addr,
		"GATEWAY_AUTH_TOKEN=test-token",
		"GATEWAY_DATA_DIR="+dataDir,
	)
	env = append(env, extraEnv...)
	env = append(env, autoPreconfiguredEnv(extraEnv)...)

	cmd := exec.Command(bin)
	cmd.Env = env
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start: %w", err)
	}
	// Best-effort: kill the child when the parent exits.
	go func() { _ = cmd.Wait() }()

	if err := waitHealthyDirect(baseURL, 15*time.Second); err != nil {
		_ = cmd.Process.Kill()
		return "", err
	}
	return baseURL, nil
}

func buildGatewayBin() (string, error) {
	if bin := os.Getenv("E2E_GATEWAY_BIN"); bin != "" {
		return bin, nil
	}
	dir, err := os.MkdirTemp("", "gateway-e2e-*")
	if err != nil {
		return "", err
	}
	bin := dir + "/gateway"
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/gateway")
	cmd.Dir = moduleRootDir() // defined in gateway_test.go
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build: %v\n%s", err, out)
	}
	return bin, nil
}

func waitHealthyDirect(baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/healthz") //nolint:noctx
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("gateway at %s did not become healthy within %s", baseURL, timeout)
}

// ─── misc helpers ─────────────────────────────────────────────────────────────

func decodeJSON(t *testing.T, r io.Reader, v interface{}) {
	t.Helper()
	if err := json.NewDecoder(r).Decode(v); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

func bodyString(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return string(b)
}
