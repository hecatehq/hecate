package llamacpp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeProxyRuntime exposes a controllable EnsureLoaded + Available.
// Tests pre-load the base URL or the error EnsureLoaded should return.
type fakeProxyRuntime struct {
	available bool
	baseURL   string
	ensureErr error

	mu          sync.Mutex
	loadedModel string
	loadCalls   int
}

func (r *fakeProxyRuntime) Available() bool { return r.available }

func (r *fakeProxyRuntime) EnsureLoaded(_ context.Context, modelID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loadCalls++
	if r.ensureErr != nil {
		return "", r.ensureErr
	}
	r.loadedModel = modelID
	return r.baseURL, nil
}

// proxyTestUpstream is the "llama-server" the proxy forwards to. It
// records the inbound request and replays a canned response so tests
// can assert both the forwarded path/body and that streaming works.
type proxyTestUpstream struct {
	mu             sync.Mutex
	gotPath        string
	gotBody        []byte
	gotMethod      string
	gotAuthHeader  string
	responseBody   string
	responseStatus int
	flushChunks    []string // when set, write each chunk + flush so the test can prove streaming
}

func (u *proxyTestUpstream) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.gotPath = r.URL.Path
		u.gotBody = body
		u.gotMethod = r.Method
		u.gotAuthHeader = r.Header.Get("Authorization")
		u.mu.Unlock()
		if u.responseStatus != 0 {
			w.WriteHeader(u.responseStatus)
		}
		if len(u.flushChunks) > 0 {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			for _, c := range u.flushChunks {
				_, _ = w.Write([]byte(c))
				if flusher != nil {
					flusher.Flush()
				}
			}
			return
		}
		_, _ = w.Write([]byte(u.responseBody))
	}
}

func TestProxy_ServeHTTPHappyPath(t *testing.T) {
	t.Parallel()

	upstream := &proxyTestUpstream{
		responseBody: `{"id":"cmpl-1","object":"chat.completion","choices":[]}`,
	}
	upstreamSrv := httptest.NewServer(upstream.handler())
	defer upstreamSrv.Close()

	rt := &fakeProxyRuntime{available: true, baseURL: upstreamSrv.URL}
	proxy := NewProxy(rt)

	body := bytes.NewBufferString(`{"model":"qwen","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/hecate/internal/llamacpp/v1/chat/completions", body)
	req.Header.Set("Authorization", "Bearer should-be-stripped")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "cmpl-1") {
		t.Fatalf("response body = %q; want upstream response", rec.Body.String())
	}

	upstream.mu.Lock()
	defer upstream.mu.Unlock()
	if upstream.gotPath != "/v1/chat/completions" {
		t.Fatalf("upstream got path = %q; want /v1/chat/completions", upstream.gotPath)
	}
	if upstream.gotMethod != http.MethodPost {
		t.Fatalf("upstream method = %q; want POST", upstream.gotMethod)
	}
	if !strings.Contains(string(upstream.gotBody), `"model":"qwen"`) {
		t.Fatalf("upstream body did not carry the model field: %q", upstream.gotBody)
	}
	if upstream.gotAuthHeader != "" {
		t.Fatalf("Authorization should be stripped; got %q", upstream.gotAuthHeader)
	}
	if rt.loadCalls != 1 || rt.loadedModel != "qwen" {
		t.Fatalf("runtime: loadCalls=%d loadedModel=%q", rt.loadCalls, rt.loadedModel)
	}
}

func TestProxy_StreamingForwardsFlushedChunks(t *testing.T) {
	t.Parallel()

	upstream := &proxyTestUpstream{
		flushChunks: []string{
			`data: {"choices":[{"delta":{"content":"hello"}}]}` + "\n\n",
			`data: {"choices":[{"delta":{"content":" world"}}]}` + "\n\n",
			"data: [DONE]\n\n",
		},
	}
	upstreamSrv := httptest.NewServer(upstream.handler())
	defer upstreamSrv.Close()

	rt := &fakeProxyRuntime{available: true, baseURL: upstreamSrv.URL}
	proxy := NewProxy(rt)
	req := httptest.NewRequest(http.MethodPost, "/hecate/internal/llamacpp/v1/chat/completions",
		bytes.NewBufferString(`{"model":"qwen","stream":true}`))
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// Body must contain all three chunks in order.
	out := rec.Body.String()
	want := []string{"hello", "world", "[DONE]"}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Fatalf("response missing chunk %q in body %q", w, out)
		}
	}
}

func TestProxy_RuntimeUnavailableReturns503(t *testing.T) {
	t.Parallel()

	rt := &fakeProxyRuntime{available: false}
	proxy := NewProxy(rt)
	req := httptest.NewRequest(http.MethodPost, "/hecate/internal/llamacpp/v1/chat/completions",
		bytes.NewBufferString(`{"model":"qwen"}`))
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "local_models_unavailable") {
		t.Fatalf("body missing stable code: %q", rec.Body.String())
	}
}

func TestProxy_EnsureLoadedFailureMappedToErrorCode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		err      error
		wantCode int
		wantBody string
	}{
		{"unavailable", ErrRuntimeUnavailable, http.StatusServiceUnavailable, "local_models_unavailable"},
		{"not running", ErrRuntimeNotRunning, http.StatusServiceUnavailable, "local_model_runtime_unavailable"},
		{"wrong model", ErrRuntimeWrongModel, http.StatusServiceUnavailable, "local_model_runtime_unavailable"},
		{"not installed", errors.New("model not found"), http.StatusNotFound, "local_model_not_installed"},
		{"unknown", errors.New("strange failure"), http.StatusInternalServerError, "local_model_runtime_unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := &fakeProxyRuntime{available: true, ensureErr: tc.err}
			proxy := NewProxy(rt)
			req := httptest.NewRequest(http.MethodPost,
				"/hecate/internal/llamacpp/v1/chat/completions",
				bytes.NewBufferString(`{"model":"qwen"}`))
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d; want %d", rec.Code, tc.wantCode)
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("body = %q; want code %q", rec.Body.String(), tc.wantBody)
			}
		})
	}
}

func TestProxy_BadJSONReturns400(t *testing.T) {
	t.Parallel()
	rt := &fakeProxyRuntime{available: true, baseURL: "http://127.0.0.1:9"}
	proxy := NewProxy(rt)
	req := httptest.NewRequest(http.MethodPost,
		"/hecate/internal/llamacpp/v1/chat/completions",
		bytes.NewBufferString(`not json`))
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rec.Code)
	}
}

func TestProxy_EmptyModelReturns400(t *testing.T) {
	t.Parallel()
	rt := &fakeProxyRuntime{available: true, baseURL: "http://127.0.0.1:9"}
	proxy := NewProxy(rt)
	req := httptest.NewRequest(http.MethodPost,
		"/hecate/internal/llamacpp/v1/chat/completions",
		bytes.NewBufferString(`{"messages":[]}`))
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "model field") {
		t.Fatalf("body should mention missing model field: %q", rec.Body.String())
	}
}

// writeJSONError shape check — keeps the format stable for the UI's
// error-handler.
func TestWriteJSONErrorShape(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeJSONError(rec, http.StatusServiceUnavailable, "x_unavailable", "down")
	if rec.Code != 503 {
		t.Fatalf("status = %d", rec.Code)
	}
	var payload map[string]map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["error"]["code"] != "x_unavailable" {
		t.Fatalf("code = %q", payload["error"]["code"])
	}
	if payload["error"]["message"] != "down" {
		t.Fatalf("message = %q", payload["error"]["message"])
	}
}
