//go:build e2e

// Package e2e contains true end-to-end tests that build and start the real
// hecate binary, send real HTTP requests (optionally against real upstream
// providers), and verify the response shape.
//
// Run with:
//
//	go test -tags e2e ./e2e/... -v
//
// Tests that hit a real LLM provider require at least one of:
//
//	PROVIDER_ANTHROPIC_API_KEY  — for Claude Code (/v1/messages) tests
//	PROVIDER_OPENAI_API_KEY     — for Codex (/v1/chat/completions) tests
//
// Without those keys the real-provider tests are skipped. The binary-startup
// tests never require keys and always run.
package e2e

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ─── binary lifecycle ────────────────────────────────────────────────────────

var (
	buildOnce    sync.Once
	builtBinPath string
	builtBinErr  error
)

const gatewayStartupTimeout = 30 * time.Second

type tailBuffer struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

func newTailBuffer(limit int) *tailBuffer {
	return &tailBuffer{limit: limit}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.limit <= 0 {
		return len(p), nil
	}
	if len(p) >= b.limit {
		b.buf = append(b.buf[:0], p[len(p)-b.limit:]...)
		return len(p), nil
	}
	b.buf = append(b.buf, p...)
	if overflow := len(b.buf) - b.limit; overflow > 0 {
		copy(b.buf, b.buf[overflow:])
		b.buf = b.buf[:b.limit]
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.buf) == 0 {
		return "(no gateway output captured)"
	}
	return string(b.buf)
}

// hecateBinary returns the path to the compiled hecate binary.
// The binary is built exactly once per test-binary execution using sync.Once,
// so parallel tests don't trigger redundant go build invocations.
// Set E2E_HECATE_BIN to a pre-built path to skip the build entirely (CI).
func hecateBinary(t *testing.T) string {
	t.Helper()
	if bin := os.Getenv("E2E_HECATE_BIN"); bin != "" {
		return bin
	}
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "hecate-e2e-*")
		if err != nil {
			builtBinErr = err
			return
		}
		builtBinPath = dir + "/hecate"
		cmd := exec.Command("go", "build", "-o", builtBinPath, "./cmd/hecate")
		cmd.Dir = moduleRootDir()
		out, err := cmd.CombinedOutput()
		if err != nil {
			builtBinErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if builtBinErr != nil {
		t.Fatalf("build hecate binary: %v", builtBinErr)
	}
	return builtBinPath
}

// moduleRootDir returns the repository root by reading go env GOMOD.
func moduleRootDir() string {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		panic("go env GOMOD: " + err.Error())
	}
	mod := strings.TrimSpace(string(out))
	return mod[:strings.LastIndex(mod, string(os.PathSeparator))]
}

// gatewayServer starts the hecate binary on a free port and returns the base
// URL once /healthz responds 200.  The process is killed when the test ends.
func gatewayServer(t *testing.T, extraEnv ...string) string {
	t.Helper()

	bin := hecateBinary(t)
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	baseURL := "http://" + addr

	// HECATE_DATA_DIR points at a per-test temp dir so any state file the
	// runtime touches (bootstrap, control plane) lands under the test's
	// own filesystem and gets cleaned up automatically.
	env := append(os.Environ(),
		"HECATE_ADDRESS="+addr,
		"HECATE_DATA_DIR="+t.TempDir(),
	)
	env = append(env, extraEnv...)
	env = append(env, autoPreconfiguredEnv(extraEnv)...)

	cmd := exec.Command(bin)
	cmd.Env = env
	output := newTailBuffer(64 * 1024)
	cmd.Stdout = output
	cmd.Stderr = output

	if err := cmd.Start(); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	waitHealthyProcess(t, baseURL, gatewayStartupTimeout, cmd, output)
	return baseURL
}

// autoPreconfiguredEnv scans extraEnv for PROVIDER_<NAME>_<FIELD>
// pairs and returns PROVIDER_<NAME>_PRECONFIGURED=1 entries for each
// distinct name. The auto-import contract requires the gate var to be
// set before env-supplied credentials materialize a provider in the
// CP store; e2e tests describe providers via PROVIDER_<NAME>_* vars
// and expect them routable, so this helper opts them all in without
// each test site having to repeat the gate.
func autoPreconfiguredEnv(extraEnv []string) []string {
	preconfigured := map[string]bool{}
	for _, kv := range extraEnv {
		const prefix = "PROVIDER_"
		if !strings.HasPrefix(kv, prefix) {
			continue
		}
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		rest := kv[len(prefix):eq]
		nameEnd := strings.IndexByte(rest, '_')
		if nameEnd <= 0 {
			continue
		}
		name := rest[:nameEnd]
		// Skip the gate var itself so we don't recurse into it.
		if rest[nameEnd+1:] == "PRECONFIGURED" {
			continue
		}
		preconfigured[name] = true
	}
	out := make([]string, 0, len(preconfigured))
	for name := range preconfigured {
		out = append(out, "PROVIDER_"+name+"_PRECONFIGURED=1")
	}
	return out
}

// waitHealthy polls GET /healthz until it returns 200 or the deadline expires.
func waitHealthy(t *testing.T, baseURL string, timeout time.Duration) {
	t.Helper()
	waitHealthyWithDiagnostics(t, baseURL, timeout, nil, nil)
}

func waitHealthyProcess(t *testing.T, baseURL string, timeout time.Duration, cmd *exec.Cmd, output *tailBuffer) {
	t.Helper()
	waitHealthyWithDiagnostics(t, baseURL, timeout, cmd, output)
}

func waitHealthyWithDiagnostics(t *testing.T, baseURL string, timeout time.Duration, cmd *exec.Cmd, output *tailBuffer) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	lastProbe := "not attempted"
	client := &http.Client{Timeout: time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/healthz") //nolint:noctx
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return
		}
		if err != nil {
			lastProbe = err.Error()
		} else {
			lastProbe = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		if err := cmd.Wait(); err != nil {
			lastProbe += "; process wait: " + err.Error()
		}
	}
	if output != nil {
		t.Fatalf("gateway at %s never became healthy within %s (last probe: %s)\n--- gateway output tail ---\n%s", baseURL, timeout, lastProbe, output.String())
	}
	t.Fatalf("gateway at %s never became healthy within %s (last probe: %s)", baseURL, timeout, lastProbe)
}

// freePort asks the OS for an available TCP port.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// ─── HTTP helpers ────────────────────────────────────────────────────────────

func postJSON(t *testing.T, url, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func getJSON[T any](t *testing.T, url string) T {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: HTTP %d — body: %s", url, resp.StatusCode, readBody(t, resp))
	}
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode GET %s: %v", url, err)
	}
	return out
}

func postJSONDecode[T any](t *testing.T, url, body string) T {
	t.Helper()
	resp := postJSON(t, url, body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: HTTP %d — body: %s", url, resp.StatusCode, readBody(t, resp))
	}
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode POST %s: %v", url, err)
	}
	return out
}

type e2eAgentAdapterList struct {
	Data []e2eAgentAdapter `json:"data"`
}

type e2eAgentAdapterProbe struct {
	Data struct {
		Adapter e2eAgentAdapter       `json:"adapter"`
		Health  e2eAgentAdapterHealth `json:"health"`
	} `json:"data"`
}

type e2eAgentAdapter struct {
	ID         string `json:"id"`
	Available  bool   `json:"available"`
	Path       string `json:"path,omitempty"`
	AuthStatus string `json:"auth_status,omitempty"`
	AuthError  string `json:"auth_error,omitempty"`
}

type e2eAgentAdapterHealth struct {
	Status string `json:"status"`
	Path   string `json:"path,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

func findE2EAdapter(t *testing.T, adapters []e2eAgentAdapter, id string) e2eAgentAdapter {
	t.Helper()
	for _, adapter := range adapters {
		if adapter.ID == id {
			return adapter
		}
	}
	t.Fatalf("adapter %q not found in %+v", id, adapters)
	return e2eAgentAdapter{}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

type sseEvent struct {
	Data string
}

func readSSE(t *testing.T, resp *http.Response) []sseEvent {
	t.Helper()
	defer resp.Body.Close()
	var events []sseEvent
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			events = append(events, sseEvent{Data: strings.TrimPrefix(line, "data: ")})
		}
	}
	return events
}

// ─── startup tests (no API key required) ────────────────────────────────────

// TestGatewayStartsAndRespondsHealthy verifies that the binary starts, binds
// the port, and returns 200 on /healthz.
func TestGatewayStartsAndRespondsHealthy(t *testing.T) {
	t.Parallel()
	base := gatewayServer(t)

	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

// TestGatewayRejectsMissingAuth checks that the gateway returns 401 when no
// token or API key is supplied (admin-mode is still on but no bearer/x-api-key
// is present — the single-user admin mode auto-injects a user so we expect 200
// here, but requests without any credentials to a non-admin endpoint still get
// processed; this test validates that a well-formed unauthenticated request
// reaches the router and gets a 502 because no provider is configured).
func TestGatewayNoProviderConfiguredReturns502(t *testing.T) {
	t.Parallel()
	// No PROVIDER_* env — no providers registered.
	base := gatewayServer(t)

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	resp := postJSON(t, base+"/v1/chat/completions", body, nil)
	defer resp.Body.Close()
	// With no providers registered the router can't route the request; it
	// returns either 500 or 502 depending on the error path.
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected non-200 with no provider configured, got 200")
	}
}

func TestEnvExampleDoesNotExposeAgentAdapterFixtureOverrides(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(filepath.Join(moduleRootDir(), ".env.example"))
	if err != nil {
		t.Fatalf("read .env.example: %v", err)
	}
	text := string(content)
	for _, name := range []string{
		"HECATE_AGENT_ADAPTER_DISCOVERY_OVERRIDES",
		"HECATE_AGENT_ADAPTER_DEV_OVERRIDES",
	} {
		if strings.Contains(text, name) {
			t.Fatalf(".env.example exposes %s; fixture-only adapter overrides must stay out of operator env templates", name)
		}
	}
}

func TestAgentAdapterDevOverridesDriveCatalogAndProbeOnly(t *testing.T) {
	t.Parallel()

	base := gatewayServer(t,
		"HECATE_AGENT_ADAPTER_DEV_OVERRIDES=codex=ready,claude_code=no_auth,cursor_agent=app_missing",
	)

	adapters := getJSON[e2eAgentAdapterList](t, base+"/hecate/v1/agent-adapters")
	codex := findE2EAdapter(t, adapters.Data, "codex")
	if !codex.Available || codex.AuthStatus != "ok" || !strings.HasPrefix(codex.Path, "dev-override://") {
		t.Fatalf("codex catalog = %+v, want forced ready with dev override path", codex)
	}
	claude := findE2EAdapter(t, adapters.Data, "claude_code")
	if !claude.Available || claude.AuthStatus != "unauthenticated" || !strings.Contains(claude.AuthError, "claude /login") {
		t.Fatalf("claude catalog = %+v, want forced missing-auth guidance", claude)
	}
	cursor := findE2EAdapter(t, adapters.Data, "cursor_agent")
	if !cursor.Available || !strings.HasPrefix(cursor.Path, "dev-override://") {
		t.Fatalf("cursor catalog = %+v, want forced app-missing fixture path", cursor)
	}

	codexProbe := postJSONDecode[e2eAgentAdapterProbe](t, base+"/hecate/v1/agent-adapters/codex/probe", "")
	if codexProbe.Data.Health.Status != "ready" {
		t.Fatalf("codex probe status = %q, want ready", codexProbe.Data.Health.Status)
	}
	claudeProbe := postJSONDecode[e2eAgentAdapterProbe](t, base+"/hecate/v1/agent-adapters/claude_code/probe", "")
	if claudeProbe.Data.Health.Status != "auth_required" || !strings.Contains(claudeProbe.Data.Health.Hint, "claude /login") {
		t.Fatalf("claude probe = %+v, want auth_required with login hint", claudeProbe.Data.Health)
	}
	cursorProbe := postJSONDecode[e2eAgentAdapterProbe](t, base+"/hecate/v1/agent-adapters/cursor_agent/probe", "")
	if cursorProbe.Data.Health.Status != "error" || !strings.Contains(cursorProbe.Data.Health.Hint, "Install Cursor") {
		t.Fatalf("cursor probe = %+v, want visual app-missing setup hint", cursorProbe.Data.Health)
	}
	if strings.Contains(cursorProbe.Data.Health.Path, "cursor-agent") && !strings.HasPrefix(cursorProbe.Data.Health.Path, "dev-override://") {
		t.Fatalf("cursor probe path = %q, fixture should not resolve a real adapter process", cursorProbe.Data.Health.Path)
	}
}

func TestAgentAdapterDevOverridesDoNotBypassRealAdapterStartup(t *testing.T) {
	t.Parallel()

	base := gatewayServer(t,
		"HOME="+t.TempDir(),
		"PATH="+t.TempDir(),
		"HECATE_AGENT_ADAPTER_DEV_OVERRIDES=cursor_agent=ready",
	)

	probe := postJSONDecode[e2eAgentAdapterProbe](t, base+"/hecate/v1/agent-adapters/cursor_agent/probe", "")
	if probe.Data.Health.Status != "ready" || !strings.HasPrefix(probe.Data.Health.Path, "dev-override://") {
		t.Fatalf("probe = %+v, want forced ready dev override", probe.Data.Health)
	}

	body := fmt.Sprintf(`{"agent_id":"cursor_agent","workspace":%q}`, t.TempDir())
	resp := postJSON(t, base+"/hecate/v1/chat/sessions", body, nil)
	if resp.StatusCode != http.StatusOK {
		_ = readBody(t, resp)
		return
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		resp.Body.Close()
		t.Fatalf("decode forced-ready chat session: %v", err)
	}
	resp.Body.Close()

	msgResp := postJSON(t, base+"/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"hello"}`, nil)
	if msgResp.StatusCode == http.StatusOK {
		t.Fatalf("forced-ready fixture made a real chat send succeed; dev overrides must remain visual-only")
	}
	_ = readBody(t, msgResp)
}

// TestGatewayFakeUpstreamNonStreamingCodex starts the gateway pointing at a
// local fake OpenAI-compatible HTTP server and verifies a complete non-streaming
// Codex request round-trip.
func TestGatewayFakeUpstreamNonStreamingCodex(t *testing.T) {
	t.Parallel()

	// Start fake OpenAI upstream.
	fakeResp := `{"id":"chatcmpl-e2e","object":"chat.completion","created":1700000000,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"Hello from fake upstream"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":8,"total_tokens":13}}`
	upstream := fakeOpenAIServer(t, "/v1/chat/completions", fakeResp, false)

	base := gatewayServer(t,
		"PROVIDER_FAKE_API_KEY=dummy",
		"PROVIDER_FAKE_BASE_URL="+upstream,
		"PROVIDER_FAKE_KIND=local",
	)

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}`
	resp := postJSON(t, base+"/v1/chat/completions", body, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", resp.StatusCode, readBody(t, resp))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	choices, ok := result["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		t.Fatalf("expected choices in response: %v", result)
	}
	choice := choices[0].(map[string]interface{})
	msg := choice["message"].(map[string]interface{})
	if msg["content"] != "Hello from fake upstream" {
		t.Fatalf("unexpected content: %v", msg["content"])
	}
}

func TestGatewayFakeUpstreamProviderDefaultModelIsRoutable(t *testing.T) {
	t.Parallel()

	fakeResp := `{"id":"chatcmpl-provider-default","object":"chat.completion","created":1700000000,"model":"fake-e2e-model","choices":[{"index":0,"message":{"role":"assistant","content":"provider default ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`
	upstream := fakeOpenAIServer(t, "/v1/chat/completions", fakeResp, false)

	base := gatewayServer(t,
		"PROVIDER_FAKE_API_KEY=dummy",
		"PROVIDER_FAKE_BASE_URL="+upstream,
		"PROVIDER_FAKE_DEFAULT_MODEL=fake-e2e-model",
		"PROVIDER_FAKE_KIND=local",
	)

	body := `{"model":"fake-e2e-model","messages":[{"role":"user","content":"hello"}]}`
	resp := postJSON(t, base+"/v1/chat/completions", body, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected provider default model to route, got %d — body: %s", resp.StatusCode, readBody(t, resp))
	}
}

// TestGatewayFakeUpstreamExportsOTLPTracesAndMetrics verifies that the
// standard e2e path exports both traces and metrics to an OTLP/HTTP receiver
// without requiring a real model runtime such as Ollama.
func TestGatewayFakeUpstreamExportsOTLPTracesAndMetrics(t *testing.T) {
	t.Parallel()

	sink := newOTLPSink()
	t.Cleanup(sink.close)

	fakeResp := `{"id":"chatcmpl-otlp","object":"chat.completion","created":1700000000,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"OTLP ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":8,"total_tokens":13}}`
	upstream := fakeOpenAIServer(t, "/v1/chat/completions", fakeResp, false)

	base := gatewayServer(t,
		"PROVIDER_FAKE_API_KEY=dummy",
		"PROVIDER_FAKE_BASE_URL="+upstream,
		"PROVIDER_FAKE_KIND=local",
		"HECATE_OTEL_TRACES_ENABLED=true",
		"HECATE_OTEL_TRACES_ENDPOINT=http://"+sink.addr(),
		"HECATE_OTEL_METRICS_ENABLED=true",
		"HECATE_OTEL_METRICS_ENDPOINT=http://"+sink.addr(),
		"HECATE_OTEL_METRICS_INTERVAL=200ms",
	)

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}`
	resp := postJSON(t, base+"/v1/chat/completions", body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", resp.StatusCode, readBody(t, resp))
	}
	io.Copy(io.Discard, resp.Body) //nolint:errcheck

	if !sink.waitForSpan("gateway.provider", 8*time.Second) {
		t.Fatalf("no gateway.provider span within 8s; got spans: %v", sink.spanNames())
	}
	if !sink.waitForMetric("hecate.provider.calls", 10*time.Second) {
		t.Fatalf("no hecate.provider.calls metric within 10s; got metrics: %v", sink.metricNames())
	}
}

// TestGatewayFakeUpstreamStreamingCodex verifies that the gateway streams SSE
// chunks from the upstream through to the client correctly.
func TestGatewayFakeUpstreamStreamingCodex(t *testing.T) {
	t.Parallel()

	upstream := fakeOpenAIServer(t, "/v1/chat/completions", "", true)

	base := gatewayServer(t,
		"PROVIDER_FAKE_API_KEY=dummy",
		"PROVIDER_FAKE_BASE_URL="+upstream,
		"PROVIDER_FAKE_KIND=local",
	)

	body := `{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"hello"}]}`
	resp := postJSON(t, base+"/v1/chat/completions", body, nil)

	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("expected 200, got %d — body: %s", resp.StatusCode, body)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream content-type, got %s", ct)
	}

	events := readSSE(t, resp)
	if len(events) == 0 {
		t.Fatal("expected at least one SSE event")
	}
	// Last event should be [DONE].
	last := events[len(events)-1]
	if last.Data != "[DONE]" {
		t.Fatalf("expected last SSE data to be [DONE], got %q", last.Data)
	}
}

// TestGatewayFakeUpstreamClaudeCode verifies the /v1/messages (Anthropic)
// endpoint using a fake OpenAI-compatible upstream.
func TestGatewayFakeUpstreamClaudeCode(t *testing.T) {
	t.Parallel()

	fakeResp := `{"id":"chatcmpl-e2e","object":"chat.completion","created":1700000000,"model":"claude-sonnet-4-20250514","choices":[{"index":0,"message":{"role":"assistant","content":"Hello from fake upstream"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":8,"total_tokens":13}}`
	upstream := fakeOpenAIServer(t, "/v1/chat/completions", fakeResp, false)

	base := gatewayServer(t,
		"PROVIDER_FAKE_API_KEY=dummy",
		"PROVIDER_FAKE_BASE_URL="+upstream,
		"PROVIDER_FAKE_KIND=local",
	)

	body := `{"model":"claude-sonnet-4-20250514","max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`
	resp := postJSON(t, base+"/v1/messages", body, map[string]string{
		"anthropic-version": "2023-06-01",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", resp.StatusCode, readBody(t, resp))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// Anthropic response shape: {"type":"message","content":[{"type":"text","text":"..."}],...}
	if result["type"] != "message" {
		t.Fatalf("expected type=message, got: %v", result["type"])
	}
	content, ok := result["content"].([]interface{})
	if !ok || len(content) == 0 {
		t.Fatalf("expected content array in response: %v", result)
	}
	block := content[0].(map[string]interface{})
	if block["type"] != "text" {
		t.Fatalf("expected content[0].type=text, got %v", block["type"])
	}
	if block["text"] != "Hello from fake upstream" {
		t.Fatalf("unexpected text: %v", block["text"])
	}
}

// TestGatewayMultimodalCodexImageURLPassthrough exercises the
// full multi-modal pipe end-to-end on the OpenAI route: the
// caller posts a content array (text + image_url) to the real
// hecate binary, and the fake upstream must receive the array
// form on the wire with the image_url block intact.
//
// This catches regressions that the unit tests can't: the JSON
// decode → normalize → provider serialize chain runs across the
// real binary and a real HTTP roundtrip. A subtle break (e.g.
// flattening blocks to a string at any layer) shows up here as
// the upstream receiving a string instead of an array.
func TestGatewayMultimodalCodexImageURLPassthrough(t *testing.T) {
	t.Parallel()

	fakeResp := `{"id":"chatcmpl-mm","object":"chat.completion","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"It's a cat."},"finish_reason":"stop"}],"usage":{"prompt_tokens":50,"completion_tokens":4,"total_tokens":54}}`
	upstream, captured := fakeUpstreamCapturing(t, "/v1/chat/completions", fakeResp)

	base := gatewayServer(t,
		"PROVIDER_FAKE_API_KEY=dummy",
		"PROVIDER_FAKE_BASE_URL="+upstream,
		"PROVIDER_FAKE_KIND=local",
	)

	body := `{
		"model":"gpt-4o",
		"messages":[{"role":"user","content":[
			{"type":"text","text":"describe this"},
			{"type":"image_url","image_url":{"url":"https://example.com/cat.png","detail":"high"}}
		]}]
	}`
	resp := postJSON(t, base+"/v1/chat/completions", body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", resp.StatusCode, readBody(t, resp))
	}

	// Inspect what the gateway forwarded to the upstream.
	upstreamBody := captured.lastBody()
	if upstreamBody == nil {
		t.Fatal("upstream received no request body")
	}
	msgs, _ := upstreamBody["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("upstream messages = %d, want 1", len(msgs))
	}
	first, _ := msgs[0].(map[string]any)
	contentArr, ok := first["content"].([]any)
	if !ok {
		t.Fatalf("upstream received content as %T (not array form): %v", first["content"], first["content"])
	}
	if len(contentArr) != 2 {
		t.Fatalf("upstream content blocks = %d, want 2", len(contentArr))
	}
	imgBlock, _ := contentArr[1].(map[string]any)
	if imgBlock["type"] != "image_url" {
		t.Errorf("blocks[1].type = %v, want image_url", imgBlock["type"])
	}
	imgURL, _ := imgBlock["image_url"].(map[string]any)
	if imgURL["url"] != "https://example.com/cat.png" {
		t.Errorf("upstream got image URL %v, want https://example.com/cat.png", imgURL["url"])
	}
	if imgURL["detail"] != "high" {
		t.Errorf("upstream got image detail %v, want high (detail must survive the gateway hop)", imgURL["detail"])
	}
}

// TestGatewayMultimodalAnthropicImageURLTranslation exercises the
// cross-provider path: an OpenAI-shaped /v1/chat/completions
// request lands on an Anthropic upstream, and the gateway must
// translate the image_url block into Anthropic's image+source
// shape on the wire.
//
// A regression that left blocks in the OpenAI shape would 400 the
// Anthropic upstream — exactly the sort of failure this test
// catches before users hit it.
func TestGatewayMultimodalAnthropicImageURLTranslation(t *testing.T) {
	t.Parallel()

	// Fake Anthropic upstream — minimal Messages-API response.
	fakeResp := `{"id":"msg_e2e_mm","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"It's a cat."}],"stop_reason":"end_turn","usage":{"input_tokens":50,"output_tokens":4}}`
	upstream, captured := fakeUpstreamCapturing(t, "/v1/messages", fakeResp)

	base := gatewayServer(t,
		"PROVIDER_ANTHROPIC_API_KEY=dummy",
		"PROVIDER_ANTHROPIC_BASE_URL="+upstream,
	)

	// Caller posts the OpenAI shape — this is the cross-provider
	// route operators care about ("I use the OpenAI SDK but route
	// to Claude").
	body := `{
		"model":"claude-sonnet-4-6",
		"max_tokens":128,
		"messages":[{"role":"user","content":[
			{"type":"text","text":"describe this"},
			{"type":"image_url","image_url":{"url":"https://example.com/cat.png"}}
		]}]
	}`
	resp := postJSON(t, base+"/v1/chat/completions", body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", resp.StatusCode, readBody(t, resp))
	}

	upstreamBody := captured.lastBody()
	if upstreamBody == nil {
		t.Fatal("upstream received no request body")
	}
	msgs, _ := upstreamBody["messages"].([]any)
	first, _ := msgs[0].(map[string]any)
	contentArr, _ := first["content"].([]any)
	if len(contentArr) != 2 {
		t.Fatalf("anthropic upstream content blocks = %d, want 2 (text + image)", len(contentArr))
	}
	imgBlock, _ := contentArr[1].(map[string]any)
	// Anthropic uses type=image (not image_url) and source=...
	if imgBlock["type"] != "image" {
		t.Errorf("blocks[1].type = %v, want image (translated from image_url)", imgBlock["type"])
	}
	source, _ := imgBlock["source"].(map[string]any)
	if source["type"] != "url" {
		t.Errorf("source.type = %v, want url", source["type"])
	}
	if source["url"] != "https://example.com/cat.png" {
		t.Errorf("source.url = %v, want passthrough", source["url"])
	}
}

// TestGatewayMultimodalAnthropicDataURITranslation exercises the
// other half of the cross-provider image story: a `data:image/...`
// URI (the common shape for client-side embedded images) gets
// parsed by the gateway and re-emitted as Anthropic's base64
// source form. Saves Anthropic from having to handle a pseudo-URL
// and works on older Anthropic API versions that only accept
// base64.
func TestGatewayMultimodalAnthropicDataURITranslation(t *testing.T) {
	t.Parallel()

	fakeResp := `{"id":"msg_e2e_b64","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":80,"output_tokens":1}}`
	upstream, captured := fakeUpstreamCapturing(t, "/v1/messages", fakeResp)

	base := gatewayServer(t,
		"PROVIDER_ANTHROPIC_API_KEY=dummy",
		"PROVIDER_ANTHROPIC_BASE_URL="+upstream,
	)

	// `data:image/jpeg;base64,...` — the shape an OpenAI client
	// produces when reading a local file with the official SDK.
	body := `{
		"model":"claude-sonnet-4-6",
		"max_tokens":128,
		"messages":[{"role":"user","content":[
			{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,/9j/4AAQ"}}
		]}]
	}`
	resp := postJSON(t, base+"/v1/chat/completions", body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", resp.StatusCode, readBody(t, resp))
	}

	upstreamBody := captured.lastBody()
	msgs, _ := upstreamBody["messages"].([]any)
	first, _ := msgs[0].(map[string]any)
	contentArr, _ := first["content"].([]any)
	imgBlock, _ := contentArr[0].(map[string]any)
	source, _ := imgBlock["source"].(map[string]any)
	if source["type"] != "base64" {
		t.Errorf("source.type = %v, want base64 (data URI must be parsed inline)", source["type"])
	}
	if source["media_type"] != "image/jpeg" {
		t.Errorf("source.media_type = %v, want image/jpeg from URI", source["media_type"])
	}
	if source["data"] != "/9j/4AAQ" {
		t.Errorf("source.data = %v, want extracted base64 payload", source["data"])
	}
	if _, present := source["url"]; present {
		t.Errorf("source.url should be absent on base64 form; got %v", source["url"])
	}
}

// TestGatewayRuntimeProviderHeader verifies that the gateway injects the
// X-Runtime-Provider header into successful responses.
func TestGatewayRuntimeProviderHeader(t *testing.T) {
	t.Parallel()

	fakeResp := `{"id":"chatcmpl-e2e","object":"chat.completion","created":1700000000,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`
	upstream := fakeOpenAIServer(t, "/v1/chat/completions", fakeResp, false)

	base := gatewayServer(t,
		"PROVIDER_FAKE_API_KEY=dummy",
		"PROVIDER_FAKE_BASE_URL="+upstream,
		"PROVIDER_FAKE_KIND=local",
	)

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	resp := postJSON(t, base+"/v1/chat/completions", body, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if h := resp.Header.Get("X-Runtime-Provider"); h == "" {
		t.Fatal("expected X-Runtime-Provider header in response")
	}
}

// ─── real-provider tests (skipped without API keys) ─────────────────────────

// TestRealAnthropicClaudeCode sends a real request to Anthropic via the
// gateway's /v1/messages endpoint.  Skipped when PROVIDER_ANTHROPIC_API_KEY
// is not set.
func TestRealAnthropicClaudeCode(t *testing.T) {
	t.Parallel()
	apiKey := os.Getenv("PROVIDER_ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("PROVIDER_ANTHROPIC_API_KEY not set — skipping real-provider test")
	}

	base := gatewayServer(t,
		"PROVIDER_ANTHROPIC_API_KEY="+apiKey,
	)

	body := `{"model":"claude-haiku-4-5-20251001","max_tokens":64,"messages":[{"role":"user","content":"Reply with exactly the word: pong"}]}`
	resp := postJSON(t, base+"/v1/messages", body, map[string]string{
		"anthropic-version": "2023-06-01",
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", resp.StatusCode, readBody(t, resp))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["type"] != "message" {
		t.Fatalf("expected type=message, got %v", result["type"])
	}
	content, _ := result["content"].([]interface{})
	if len(content) == 0 {
		t.Fatalf("empty content in real Anthropic response: %v", result)
	}
}

// TestRealAnthropicClaudeCodeStreaming sends a streaming request to Anthropic
// via the gateway's /v1/messages endpoint and validates SSE format.
func TestRealAnthropicClaudeCodeStreaming(t *testing.T) {
	t.Parallel()
	apiKey := os.Getenv("PROVIDER_ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("PROVIDER_ANTHROPIC_API_KEY not set — skipping real-provider test")
	}

	base := gatewayServer(t,
		"PROVIDER_ANTHROPIC_API_KEY="+apiKey,
	)

	body := `{"model":"claude-haiku-4-5-20251001","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"Say hi"}]}`
	resp := postJSON(t, base+"/v1/messages", body, map[string]string{
		"anthropic-version": "2023-06-01",
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", resp.StatusCode, readBody(t, resp))
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %s", ct)
	}

	events := readSSE(t, resp)
	if len(events) == 0 {
		t.Fatal("no SSE events received from streaming Anthropic response")
	}

	// Anthropic SSE stream starts with message_start.
	var firstEvent map[string]interface{}
	if err := json.Unmarshal([]byte(events[0].Data), &firstEvent); err != nil {
		t.Fatalf("parse first SSE event: %v", err)
	}
	if firstEvent["type"] != "message_start" {
		t.Fatalf("expected first event type=message_start, got %v", firstEvent["type"])
	}
}

// TestRealOpenAICodex sends a real request to OpenAI via the gateway's
// /v1/chat/completions endpoint.  Skipped when PROVIDER_OPENAI_API_KEY is
// not set.
func TestRealOpenAICodex(t *testing.T) {
	t.Parallel()
	apiKey := os.Getenv("PROVIDER_OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("PROVIDER_OPENAI_API_KEY not set — skipping real-provider test")
	}

	base := gatewayServer(t,
		"PROVIDER_OPENAI_API_KEY="+apiKey,
	)

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Reply with exactly the word: pong"}]}`
	resp := postJSON(t, base+"/v1/chat/completions", body, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", resp.StatusCode, readBody(t, resp))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	choices, _ := result["choices"].([]interface{})
	if len(choices) == 0 {
		t.Fatalf("empty choices in real OpenAI response: %v", result)
	}
}

// TestRealOpenAICodexStreaming sends a streaming request to OpenAI via the
// gateway's /v1/chat/completions endpoint and validates SSE format.
func TestRealOpenAICodexStreaming(t *testing.T) {
	t.Parallel()
	apiKey := os.Getenv("PROVIDER_OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("PROVIDER_OPENAI_API_KEY not set — skipping real-provider test")
	}

	base := gatewayServer(t,
		"PROVIDER_OPENAI_API_KEY="+apiKey,
	)

	body := `{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"Say hi"}]}`
	resp := postJSON(t, base+"/v1/chat/completions", body, nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", resp.StatusCode, readBody(t, resp))
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %s", ct)
	}

	events := readSSE(t, resp)
	if len(events) == 0 {
		t.Fatal("no SSE events received from streaming OpenAI response")
	}
	last := events[len(events)-1]
	if last.Data != "[DONE]" {
		t.Fatalf("expected last event to be [DONE], got %q", last.Data)
	}
}

// ─── additional endpoint tests ───────────────────────────────────────────────

// TestGatewayModelsEndpoint verifies that GET /v1/models returns the expected
// OpenAI-compatible list envelope even when no provider is configured.
func TestGatewayModelsEndpoint(t *testing.T) {
	t.Parallel()
	base := gatewayServer(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/v1/models", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["object"] != "list" {
		t.Fatalf("expected object=list, got %v", result["object"])
	}
	if _, ok := result["data"]; !ok {
		t.Fatal("expected 'data' key in /v1/models response")
	}
}

// TestGatewayWhoAmI verifies that GET /hecate/v1/whoami returns the operator
// session envelope. There's no auth in single-user mode; the endpoint
// just confirms the role on the other end.
func TestGatewayWhoAmI(t *testing.T) {
	t.Parallel()
	base := gatewayServer(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/hecate/v1/whoami", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /hecate/v1/whoami: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["object"] != "session" {
		t.Fatalf("expected object=session, got %v", result["object"])
	}
	data, ok := result["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'data' object in response, got %T", result["data"])
	}
	if role, _ := data["role"].(string); role != "operator" {
		t.Fatalf("expected role=operator, got %q", role)
	}
}

// TestGatewayProviderStatus verifies that GET /hecate/v1/providers/status returns
// the provider-status envelope shape.
func TestGatewayProviderStatus(t *testing.T) {
	t.Parallel()
	base := gatewayServer(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/hecate/v1/providers/status", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /hecate/v1/providers/status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["object"] != "provider_status" {
		t.Fatalf("expected object=provider_status, got %v", result["object"])
	}
	if _, ok := result["data"]; !ok {
		t.Fatal("expected 'data' key in /hecate/v1/providers/status response")
	}
}

// TestGatewayRuntimeStats verifies that GET /hecate/v1/system/stats returns
// the runtime-stats envelope shape.
func TestGatewayRuntimeStats(t *testing.T) {
	t.Parallel()
	base := gatewayServer(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/hecate/v1/system/stats", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /hecate/v1/system/stats: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["object"] != "runtime_stats" {
		t.Fatalf("expected object=runtime_stats, got %v", result["object"])
	}
	if _, ok := result["data"]; !ok {
		t.Fatal("expected 'data' key in /hecate/v1/system/stats response")
	}
}

// TestGatewayProviderPresets verifies that GET /hecate/v1/providers/presets returns a
// non-empty list of built-in provider presets.
func TestGatewayProviderPresets(t *testing.T) {
	t.Parallel()
	base := gatewayServer(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/hecate/v1/providers/presets", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /hecate/v1/providers/presets: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["object"] != "provider_presets" {
		t.Fatalf("expected object=provider_presets, got %v", result["object"])
	}
	data, _ := result["data"].([]interface{})
	if len(data) == 0 {
		t.Fatal("expected at least one provider preset in /hecate/v1/providers/presets response")
	}
}

// TestGatewayInvalidJSONBodyReturns400 verifies that sending a malformed JSON
// body to POST /v1/chat/completions results in a 400 Bad Request.
func TestGatewayInvalidJSONBodyReturns400(t *testing.T) {
	t.Parallel()
	base := gatewayServer(t)

	resp := postJSON(t, base+"/v1/chat/completions", `{not valid json`, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON body, got %d", resp.StatusCode)
	}
	var errResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if _, ok := errResp["error"]; !ok {
		t.Fatal("expected 'error' key in 400 response body")
	}
}

// TestGatewayRateLimitHeaders verifies that when rate limiting is enabled the
// gateway injects X-RateLimit-Limit, X-RateLimit-Remaining, and
// X-RateLimit-Reset response headers.
func TestGatewayRateLimitHeaders(t *testing.T) {
	t.Parallel()

	fakeResp := `{"id":"chatcmpl-e2e","object":"chat.completion","created":1700000000,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`
	upstream := fakeOpenAIServer(t, "/v1/chat/completions", fakeResp, false)

	base := gatewayServer(t,
		"PROVIDER_FAKE_API_KEY=dummy",
		"PROVIDER_FAKE_BASE_URL="+upstream,
		"PROVIDER_FAKE_KIND=local",
		"HECATE_RATE_LIMIT_ENABLED=true",
		"HECATE_RATE_LIMIT_RPM=120",
	)

	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	resp := postJSON(t, base+"/v1/chat/completions", body, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	for _, hdr := range []string{"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"} {
		if resp.Header.Get(hdr) == "" {
			t.Fatalf("expected %s header in response", hdr)
		}
	}
	t.Logf("X-RateLimit-Limit=%s X-RateLimit-Remaining=%s",
		resp.Header.Get("X-RateLimit-Limit"),
		resp.Header.Get("X-RateLimit-Remaining"),
	)
}

// TestGatewayFakeUpstreamStreamingClaudeCode verifies the /v1/messages
// endpoint streams SSE with the Anthropic event envelope when the upstream
// is an OpenAI-compatible fake server.
func TestGatewayFakeUpstreamStreamingClaudeCode(t *testing.T) {
	t.Parallel()

	upstream := fakeOpenAIServer(t, "/v1/chat/completions", "", true)

	base := gatewayServer(t,
		"PROVIDER_FAKE_API_KEY=dummy",
		"PROVIDER_FAKE_BASE_URL="+upstream,
		"PROVIDER_FAKE_KIND=local",
	)

	body := `{"model":"claude-sonnet-4-20250514","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"hello"}]}`
	resp := postJSON(t, base+"/v1/messages", body, map[string]string{
		"anthropic-version": "2023-06-01",
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", resp.StatusCode, readBody(t, resp))
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream content-type, got %s", ct)
	}

	events := readSSE(t, resp)
	if len(events) == 0 {
		t.Fatal("expected at least one SSE event from /v1/messages streaming")
	}

	var first map[string]interface{}
	if err := json.Unmarshal([]byte(events[0].Data), &first); err != nil {
		t.Fatalf("parse first SSE event: %v", err)
	}
	if first["type"] != "message_start" {
		t.Fatalf("expected first SSE event type=message_start, got %v", first["type"])
	}
	t.Logf("received %d SSE events (fake upstream Claude Code streaming)", len(events))
}

// ─── fake upstream helper ────────────────────────────────────────────────────

// fakeOpenAIServer starts an httptest.Server that mimics an OpenAI-compatible
// upstream.  If streaming=true it returns chunked SSE; otherwise it returns a
// plain JSON response.  The server is shut down when the test ends.
// capturedRequests is the recorder side of fakeUpstreamCapturing.
// Tests inspect what the gateway forwarded to the (faked) upstream
// — used by the multi-modal e2e tests to verify the wire body
// carries the right shape after translation.
type capturedRequests struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func (c *capturedRequests) record(body map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bodies = append(c.bodies, body)
}

func (c *capturedRequests) lastBody() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bodies) == 0 {
		return nil
	}
	return c.bodies[len(c.bodies)-1]
}

// fakeUpstreamCapturing is fakeOpenAIServer's introspectable
// sibling: it records every inbound JSON body so a test can assert
// what the gateway actually forwarded. The response body is fixed
// (callers tailor to the protocol they're emulating). Reused
// across protocols — OpenAI's /v1/chat/completions and Anthropic's
// /v1/messages have the same JSON-decode contract at this layer.
func fakeUpstreamCapturing(t *testing.T, path, response string) (string, *capturedRequests) {
	t.Helper()
	captured := &capturedRequests{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", fakeModelsHandler)
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		// Decode best-effort; non-JSON bodies still record an
		// empty map so the test can detect "request reached
		// upstream" separately from "request was the right shape."
		_ = json.NewDecoder(r.Body).Decode(&body)
		captured.record(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, response)
	})
	srv := &http.Server{Handler: mux}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fakeUpstreamCapturing listen: %v", err)
	}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { srv.Close() })
	return "http://" + ln.Addr().String(), captured
}

func fakeOpenAIServer(t *testing.T, path, body string, streaming bool) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", fakeModelsHandler)
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if streaming {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			chunks := []string{
				`{"id":"chatcmpl-e2e","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}`,
				`{"id":"chatcmpl-e2e","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
				`{"id":"chatcmpl-e2e","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			}
			for _, chunk := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, body)
		}
	})
	srv := &http.Server{Handler: mux}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fakeOpenAIServer listen: %v", err)
	}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { srv.Close() })
	return "http://" + ln.Addr().String()
}

func fakeModelsHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"object":"list","data":[{"id":"gpt-4o-mini","object":"model"},{"id":"gpt-4o","object":"model"},{"id":"claude-sonnet-4-20250514","object":"model"}]}`)
}

// TestBootstrapAutoGenerationDefaultPath proves the no-env-overrides
// first-run path: with no HECATE_DATA_DIR override, the gateway must
//   - create `.data/hecate.bootstrap.json` (the default location)
//     under its working directory, mode 0600,
//   - persist a base64 control-plane secret in there,
//   - accept anonymous /v1/models calls (single-user mode is no-auth),
//   - reuse the same file on a second start so the persisted secret
//     survives restarts.
//
// The standard gatewayServer() helper pins HECATE_DATA_DIR, so this is
// the only test that exercises the auto-generation default-path code in
// the binary-only suite. The Docker smoke covers the same contract
// through the `/data` volume; this is the cheap counterpart.
func TestBootstrapAutoGenerationDefaultPath(t *testing.T) {
	t.Parallel()

	bin := hecateBinary(t)
	workDir := t.TempDir()

	// First start: no token / no data dir env. Cwd-rooted defaults apply,
	// so the bootstrap file should land at <workDir>/.data/...
	addr1 := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	cmd1 := exec.Command(bin)
	cmd1.Dir = workDir
	cmd1.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + workDir, // isolate any HOME-relative defaults
		"HECATE_ADDRESS=" + addr1,
	}
	cmd1.Stdout = io.Discard
	cmd1.Stderr = io.Discard
	if err := cmd1.Start(); err != nil {
		t.Fatalf("first start: %v", err)
	}
	waitHealthy(t, "http://"+addr1, gatewayStartupTimeout)

	bootstrapPath := filepath.Join(workDir, ".data", "hecate.bootstrap.json")
	info, err := os.Stat(bootstrapPath)
	if err != nil {
		t.Fatalf("bootstrap file not at expected default path %q: %v", bootstrapPath, err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("bootstrap file mode = %v, want 0600", mode)
	}

	raw, err := os.ReadFile(bootstrapPath)
	if err != nil {
		t.Fatalf("read bootstrap file: %v", err)
	}
	var boot struct {
		ControlPlaneSecretKey string `json:"control_plane_secret_key"`
	}
	if err := json.Unmarshal(raw, &boot); err != nil {
		t.Fatalf("decode bootstrap json: %v", err)
	}
	if boot.ControlPlaneSecretKey == "" {
		t.Fatal("control_plane_secret_key empty in bootstrap file")
	}
	// Secret should base64-decode to exactly 32 bytes — secrets.NewAESGCMCipher
	// rejects anything else, so a regression here would crash the runtime.
	if decoded, err := base64.StdEncoding.DecodeString(boot.ControlPlaneSecretKey); err != nil {
		t.Fatalf("control_plane_secret_key not valid base64: %v", err)
	} else if len(decoded) != 32 {
		t.Fatalf("control_plane_secret_key decoded length = %d, want 32 bytes", len(decoded))
	}

	// Single-user mode is no-auth: anonymous /v1/models 200s.
	resp, err := http.Get("http://" + addr1 + "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models anonymous: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/models anonymous = %d, want 200 (single-user mode is no-auth)", resp.StatusCode)
	}

	if err := cmd1.Process.Kill(); err != nil {
		t.Fatalf("kill first run: %v", err)
	}
	_ = cmd1.Wait()

	// Second start in the same workDir: the bootstrap file should be
	// reused, not regenerated, so the persisted control-plane secret
	// survives restart.
	addr2 := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	cmd2 := exec.Command(bin)
	cmd2.Dir = workDir
	cmd2.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + workDir,
		"HECATE_ADDRESS=" + addr2,
	}
	cmd2.Stdout = io.Discard
	cmd2.Stderr = io.Discard
	if err := cmd2.Start(); err != nil {
		t.Fatalf("second start: %v", err)
	}
	t.Cleanup(func() { _ = cmd2.Process.Kill(); _ = cmd2.Wait() })
	waitHealthy(t, "http://"+addr2, gatewayStartupTimeout)

	resp, err = http.Get("http://" + addr2 + "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models on second run: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/models on second run = %d, want 200", resp.StatusCode)
	}

	// Bootstrap file should be unchanged between starts.
	raw2, err := os.ReadFile(bootstrapPath)
	if err != nil {
		t.Fatalf("re-read bootstrap file: %v", err)
	}
	if string(raw2) != string(raw) {
		t.Fatalf("bootstrap file regenerated on second start; want identical contents")
	}
}

// TestGatewayVersionFlag verifies the `--version` short-circuit prints
// the build version to stdout and exits 0 without binding a port. This
// is the path the README points at and what `goreleaser`'s ldflag
// injection feeds into; if the flag handling regresses or the variable
// isn't wired, downstream "which build is this prod box running?"
// debugging breaks silently.
//
// The default test build doesn't pass an -X ldflag, so we expect "dev".
// goreleaser releases will see whatever git tag triggered the build.
func TestGatewayVersionFlag(t *testing.T) {
	t.Parallel()

	bin := hecateBinary(t)
	for _, flag := range []string{"--version", "-v", "version"} {
		flag := flag
		t.Run(flag, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, flag)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("gateway %s: %v (stderr: %s)", flag, err, errOutput(err))
			}
			got := strings.TrimSpace(string(out))
			if got == "" {
				t.Fatalf("gateway %s printed empty stdout", flag)
			}
			// We don't assert exact equality with "dev" because tooling
			// might inject a version even in test builds; the contract
			// is "prints something non-empty and exits 0".
			if strings.Contains(got, "\n") {
				t.Fatalf("gateway %s should print a single line, got %q", flag, got)
			}
		})
	}
}

// errOutput pulls stderr out of an *exec.ExitError if available, so test
// failure messages include why the binary refused to print its version.
func errOutput(err error) string {
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(exitErr.Stderr)
	}
	return ""
}
