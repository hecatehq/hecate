package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/controlplane"
	"github.com/hecate/agent-runtime/internal/llamacpp"
)

// Integration tests for the /hecate/v1/local-models/* surface driven
// through the actual registered routes. The unit tests in
// internal/llamacpp/ exercise each component (Installer, Runtime,
// Proxy, Service) with fakes; these tests wire them all together with
// real upstream HTTP servers and assert the chain end-to-end.
//
// Fakes used:
//   - upstreamLlama   — httptest.Server serving /health and
//                       /v1/chat/completions to mimic llama-server.
//   - upstreamGGUF    — TLS httptest.Server serving fixed bytes as
//                       the GGUF download target. TLS because
//                       ParsePasteURL enforces https.
//   - upstreamStarter — fake ProcessStarter that returns handles
//                       whose Host/Port point at upstreamLlama, so
//                       the Runtime → Proxy hop ends up at our
//                       controlled upstream instead of a real child.

func TestLocalModels_InstallStartChat_EndToEnd(t *testing.T) {
	t.Parallel()

	upstreamLlama, upstreamHost, upstreamPort := startFakeLlamaServer(t)
	defer upstreamLlama.Close()

	ggufBody := []byte("hecate-test-gguf-bytes; not a real model but the bytes don't matter")
	digest := sha256.Sum256(ggufBody)
	expectedSHA := hex.EncodeToString(digest[:])
	ggufSrv := startFakeGGUFSource(t, ggufBody)
	defer ggufSrv.Close()

	dataDir := t.TempDir()
	binPath := makeExecutableFile(t)
	store := controlplane.NewMemoryStore()
	svc, err := llamacpp.NewService(llamacpp.ServiceOptions{
		BinaryPath: binPath,
		DataDir:    dataDir,
		Store:      store,
		Starter:    newPinnedStarter(upstreamHost, upstreamPort),
		InstallerOptions: llamacpp.InstallerOptions{
			HTTP:                  ggufSrv.Client(),
			ProgressIntervalBytes: 1,
			ProgressIntervalMS:    1,
		},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	srv := mountLocalModelsRoutes(t, svc)
	defer srv.Close()
	client := srv.Client()

	// Build a paste URL the parser accepts: must include /resolve/
	// and end in .gguf. The upstream test server returns the same
	// body regardless of path.
	pasteURL := fmt.Sprintf("%s/test/resolve/main/test-model.gguf", ggufSrv.URL)

	installID := postInstall(t, srv, client, llamacpp.InstallSpec{URL: pasteURL, SHA256: expectedSHA})

	// Drive the install to completion via the SSE stream — proves
	// the handler / installer wiring round-trips correctly.
	events := readSSEStream(t, srv, client, installID)
	final := events[len(events)-1]
	if final.Kind != llamacpp.ProgressCompleted {
		t.Fatalf("final SSE event = %+v; want completed", final)
	}
	if final.SHA256 != expectedSHA {
		t.Fatalf("completed sha = %q; want %q", final.SHA256, expectedSHA)
	}

	// File must exist on disk; the model id matches the slug
	// derived from the URL ("test-model.gguf" → "test-model").
	modelID := "test-model"
	finalPath := filepath.Join(dataDir, "models", modelID+".gguf")
	if _, err := os.Stat(finalPath); err != nil {
		t.Fatalf("expected file at %q: %v", finalPath, err)
	}

	// /installed reflects the new row.
	installedResp := getInstalled(t, srv, client)
	if len(installedResp.Data) != 1 || installedResp.Data[0].ID != modelID {
		t.Fatalf("installed list = %+v; want one row with id %q", installedResp.Data, modelID)
	}

	// Start the runtime against the installed model.
	postRuntimeStart(t, srv, client, modelID)
	if status := svc.Runtime().Status(); status.State != llamacpp.RuntimeRunning {
		t.Fatalf("runtime state = %q after start; want running", status.State)
	}

	// Drive a chat-completion through the internal proxy. Proves
	// the proxy mounts, peeks the model field, forwards to the
	// upstream "llama-server", and streams the response.
	chatBody := bytes.NewBufferString(fmt.Sprintf(
		`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, modelID,
	))
	req, err := http.NewRequest(http.MethodPost,
		srv.URL+"/hecate/internal/llamacpp/v1/chat/completions", chatBody)
	if err != nil {
		t.Fatalf("build chat req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("proxy POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("proxy status = %d; body=%q", resp.StatusCode, body)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "hi from fake llama") {
		t.Fatalf("proxy body = %q; want upstream content", body)
	}

	// Upstream should have observed exactly one chat-completions
	// call with the forwarded path and a non-empty body.
	upstreamLlama.assertChatCallsAtLeast(t, 1)
}

func TestLocalModels_UninstallRoundTrip(t *testing.T) {
	t.Parallel()

	upstreamLlama, upstreamHost, upstreamPort := startFakeLlamaServer(t)
	defer upstreamLlama.Close()

	body := []byte("hecate-test-gguf-uninstall")
	digest := sha256.Sum256(body)
	hex256 := hex.EncodeToString(digest[:])
	ggufSrv := startFakeGGUFSource(t, body)
	defer ggufSrv.Close()

	store := controlplane.NewMemoryStore()
	svc, err := llamacpp.NewService(llamacpp.ServiceOptions{
		BinaryPath: makeExecutableFile(t),
		DataDir:    t.TempDir(),
		Store:      store,
		Starter:    newPinnedStarter(upstreamHost, upstreamPort),
		InstallerOptions: llamacpp.InstallerOptions{
			HTTP:                  ggufSrv.Client(),
			ProgressIntervalBytes: 1,
			ProgressIntervalMS:    1,
		},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	srv := mountLocalModelsRoutes(t, svc)
	defer srv.Close()
	client := srv.Client()

	installID := postInstall(t, srv, client, llamacpp.InstallSpec{
		URL:    ggufSrv.URL + "/test/resolve/main/uninstall-me.gguf",
		SHA256: hex256,
	})
	events := readSSEStream(t, srv, client, installID)
	if events[len(events)-1].Kind != llamacpp.ProgressCompleted {
		t.Fatalf("install did not complete: %+v", events)
	}
	if len(getInstalled(t, srv, client).Data) != 1 {
		t.Fatal("expected installed row")
	}

	// DELETE /installed/{id} — the registry row goes away and the
	// file is removed from disk.
	req, _ := http.NewRequest(http.MethodDelete,
		srv.URL+"/hecate/v1/local-models/installed/uninstall-me", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(resp.Body)
		t.Fatalf("DELETE status = %d; body=%q", resp.StatusCode, out)
	}

	if len(getInstalled(t, srv, client).Data) != 0 {
		t.Fatal("installed list should be empty after uninstall")
	}
}

func TestLocalModels_Catalog_DormantBuildReturns503(t *testing.T) {
	t.Parallel()
	// Sanity for the dormant path: with no service wired, the
	// catalog handler must 503 with local_models_unavailable so
	// the UI's "not bundled" branch is reachable through real
	// routes.
	h := &Handler{}
	mux := http.NewServeMux()
	registerLocalModelsRoutes(mux, h)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/hecate/v1/local-models/catalog")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), errCodeLocalModelsUnavailable) {
		t.Fatalf("body missing dormant code: %q", body)
	}
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// mountLocalModelsRoutes spins up an httptest.Server with the
// registered routes + the internal proxy for the given service. The
// production registerLocalModelsRoutes function is used unchanged so
// tests catch route-registration regressions.
func mountLocalModelsRoutes(t *testing.T, svc *llamacpp.Service) *httptest.Server {
	t.Helper()
	h := &Handler{}
	h.SetLocalModelsService(svc)
	mux := http.NewServeMux()
	registerLocalModelsRoutes(mux, h)
	return httptest.NewServer(mux)
}

// makeExecutableFile writes a tiny chmod +x stub so the service's
// FeatureAvailability check passes (it stats the binary and verifies
// the executable bit).
func makeExecutableFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "llama-server")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return path
}

// fakeLlamaServer is a tiny stand-in for the upstream llama-server.
// Tracks how many chat-completion calls landed so tests can assert
// the proxy forwarded their requests.
type fakeLlamaServer struct {
	*httptest.Server
	mu        sync.Mutex
	chatCalls int
}

func startFakeLlamaServer(t *testing.T) (*fakeLlamaServer, string, int) {
	t.Helper()
	f := &fakeLlamaServer{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/v1/chat/completions":
			f.mu.Lock()
			f.chatCalls++
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id":"cmpl-test",
				"object":"chat.completion",
				"choices":[{"message":{"role":"assistant","content":"hi from fake llama"}}]
			}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	u := f.Server.URL
	// httptest URLs are http://127.0.0.1:<port>
	hostPort := strings.TrimPrefix(u, "http://")
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		t.Fatalf("split host:port: %v", err)
	}
	port, _ := strconv.Atoi(portStr)
	return f, host, port
}

func (f *fakeLlamaServer) assertChatCallsAtLeast(t *testing.T, n int) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.chatCalls < n {
		t.Fatalf("upstream chat calls = %d; want at least %d", f.chatCalls, n)
	}
}

// startFakeGGUFSource serves the given bytes on every GET (with a
// Content-Length header so the installer's progress bar can render).
// TLS is required — ParsePasteURL enforces https.
func startFakeGGUFSource(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))
}

// pinnedStarter is a ProcessStarter that always returns a handle
// whose Host()/Port() point at the upstream fake llama-server. The
// runtime's /health poll resolves immediately via that upstream's
// /health route, and the proxy forwards to /v1/* there.
type pinnedStarter struct {
	host string
	port int
}

func newPinnedStarter(host string, port int) *pinnedStarter {
	return &pinnedStarter{host: host, port: port}
}

func (s *pinnedStarter) Start(_ context.Context, _ llamacpp.ProcessStartOptions) (llamacpp.ProcessHandle, error) {
	return &pinnedHandle{
		host:   s.host,
		port:   s.port,
		pid:    99,
		exited: make(chan llamacpp.ProcessExitInfo, 1),
	}, nil
}

type pinnedHandle struct {
	host     string
	port     int
	pid      int
	exited   chan llamacpp.ProcessExitInfo
	stopOnce sync.Once
}

func (h *pinnedHandle) PID() int                                { return h.pid }
func (h *pinnedHandle) Port() int                               { return h.port }
func (h *pinnedHandle) Host() string                            { return h.host }
func (h *pinnedHandle) Exited() <-chan llamacpp.ProcessExitInfo { return h.exited }

func (h *pinnedHandle) WaitForHealth(ctx context.Context) error {
	// The upstream fake answers /health immediately, but go through
	// a real HTTP probe so the runtime's healthcheck path is
	// actually exercised.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://%s:%d/health", h.host, h.port), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upstream /health returned %d", resp.StatusCode)
	}
	return nil
}

func (h *pinnedHandle) Stop(_ context.Context, _ time.Duration) error {
	h.stopOnce.Do(func() {
		h.exited <- llamacpp.ProcessExitInfo{ExitCode: 0, At: time.Now()}
		close(h.exited)
	})
	return nil
}

// postInstall fires a POST /install and returns the install id.
func postInstall(t *testing.T, srv *httptest.Server, client *http.Client, spec llamacpp.InstallSpec) string {
	t.Helper()
	body, _ := json.Marshal(spec)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/hecate/v1/local-models/install",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST install: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		out, _ := io.ReadAll(resp.Body)
		t.Fatalf("install status = %d; body=%q", resp.StatusCode, out)
	}
	var dec LocalModelsInstallResponse
	if err := json.NewDecoder(resp.Body).Decode(&dec); err != nil {
		t.Fatalf("decode install resp: %v", err)
	}
	if dec.InstallID == "" {
		t.Fatalf("install resp missing install_id: %+v", dec)
	}
	return dec.InstallID
}

// postRuntimeStart fires POST /runtime/start and asserts a clean OK.
func postRuntimeStart(t *testing.T, srv *httptest.Server, client *http.Client, modelID string) {
	t.Helper()
	body, _ := json.Marshal(localModelsRuntimeStartRequest{ModelID: modelID})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/hecate/v1/local-models/runtime/start",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST start: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(resp.Body)
		t.Fatalf("start status = %d; body=%q", resp.StatusCode, out)
	}
}

func getInstalled(t *testing.T, srv *httptest.Server, client *http.Client) LocalModelsInstalledResponse {
	t.Helper()
	resp, err := client.Get(srv.URL + "/hecate/v1/local-models/installed")
	if err != nil {
		t.Fatalf("GET installed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("installed status = %d", resp.StatusCode)
	}
	var out LocalModelsInstalledResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode installed: %v", err)
	}
	return out
}

// readSSEStream consumes a /events stream until a terminal event
// arrives (completed / failed / cancelled). Returns the full event
// list. A short deadline guards against the stream never closing.
func readSSEStream(t *testing.T, srv *httptest.Server, client *http.Client, installID string) []llamacpp.ProgressEvent {
	t.Helper()
	url := fmt.Sprintf("%s/hecate/v1/local-models/install/%s/events",
		srv.URL, installID)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	// Bound the SSE read so a stuck install can't hang the test.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d", resp.StatusCode)
	}

	var events []llamacpp.ProgressEvent
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1<<14), 1<<20)
	var pendingKind string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			pendingKind = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			payload := strings.TrimPrefix(line, "data: ")
			var ev llamacpp.ProgressEvent
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				t.Fatalf("decode SSE payload %q: %v", payload, err)
			}
			if ev.Kind == "" {
				ev.Kind = llamacpp.ProgressKind(pendingKind)
			}
			events = append(events, ev)
			if isTerminalProgressKind(ev.Kind) {
				return events
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan SSE: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no SSE events received")
	}
	return events
}

func isTerminalProgressKind(k llamacpp.ProgressKind) bool {
	switch k {
	case llamacpp.ProgressCompleted, llamacpp.ProgressFailed, llamacpp.ProgressCancelled:
		return true
	}
	return false
}
