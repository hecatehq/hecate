package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/controlplane"
)

// bootstrapTokenDirectFixture returns a fully-wired *Handler plus the
// http.Handler the tests should request against. Callers can mutate
// the handler (e.g. SetBootstrapTokenExposable) before issuing
// requests. Built directly via NewHandler / NewServer so the test
// owns the pointer instead of having to reach through the existing
// budget-test helpers that hide it.
func bootstrapTokenDirectFixture(t *testing.T, authToken string) (*Handler, http.Handler) {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := controlplane.NewMemoryStore()
	handler := NewHandler(config.Config{
		Server: config.ServerConfig{
			AuthToken: authToken,
		},
	}, logger, nil, store, nil, nil)
	return handler, NewServer(logger, handler)
}

// loopbackRequest builds a request whose RemoteAddr is on the loopback
// interface. httptest.NewRequest defaults RemoteAddr to a non-loopback
// RFC5737 documentation address, so the bootstrap-token endpoint
// refuses by default — tests opt in by overwriting RemoteAddr.
func loopbackRequest(method, path, remote string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = remote
	return req
}

func TestBootstrapToken_LoopbackV4_ReturnsToken(t *testing.T) {
	t.Parallel()
	handler, server := bootstrapTokenDirectFixture(t, "admin-secret")
	handler.SetBootstrapTokenExposable(true)

	req := loopbackRequest(http.MethodGet, "/v1/bootstrap-token", "127.0.0.1:54321")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Object string            `json:"object"`
		Data   map[string]string `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Object != "bootstrap_token" {
		t.Fatalf("object = %q, want bootstrap_token", resp.Object)
	}
	if resp.Data["token"] != "admin-secret" {
		t.Fatalf("token = %q, want admin-secret", resp.Data["token"])
	}
}

func TestBootstrapToken_LoopbackV6_ReturnsToken(t *testing.T) {
	t.Parallel()
	handler, server := bootstrapTokenDirectFixture(t, "admin-secret")
	handler.SetBootstrapTokenExposable(true)

	req := loopbackRequest(http.MethodGet, "/v1/bootstrap-token", "[::1]:54321")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestBootstrapToken_NonLoopback_Refused(t *testing.T) {
	t.Parallel()
	handler, server := bootstrapTokenDirectFixture(t, "admin-secret")
	handler.SetBootstrapTokenExposable(true)

	// httptest's default 192.0.2.1 (RFC5737) is non-loopback; the
	// handler must refuse so a remote browser can't snatch the token
	// from a network-exposed gateway.
	req := loopbackRequest(http.MethodGet, "/v1/bootstrap-token", "192.0.2.1:54321")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// TestBootstrapToken_IgnoresXForwardedFor pins that a reverse proxy
// can't trick the gateway into handing out the token by setting
// X-Forwarded-For to 127.0.0.1. The check reads the raw connection
// peer (RemoteAddr), not headers.
func TestBootstrapToken_IgnoresXForwardedFor(t *testing.T) {
	t.Parallel()
	handler, server := bootstrapTokenDirectFixture(t, "admin-secret")
	handler.SetBootstrapTokenExposable(true)

	req := loopbackRequest(http.MethodGet, "/v1/bootstrap-token", "203.0.113.5:54321")
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (XFF must not bypass loopback gate)", rec.Code)
	}
}

// TestBootstrapToken_ExposableOff_Refused: when the operator supplied
// GATEWAY_AUTH_TOKEN, the gateway doesn't hand it out even on
// loopback. The flag is the source-of-truth gate; loopback alone
// doesn't unlock the surface.
func TestBootstrapToken_ExposableOff_Refused(t *testing.T) {
	t.Parallel()
	handler, server := bootstrapTokenDirectFixture(t, "operator-supplied")
	handler.SetBootstrapTokenExposable(false)

	req := loopbackRequest(http.MethodGet, "/v1/bootstrap-token", "127.0.0.1:54321")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// TestBootstrapToken_CrossOrigin_Refused: when an Origin header is
// present and doesn't match the request's Host, refuse. Browsers
// reaching localhost:8765 from any other origin will land here.
func TestBootstrapToken_CrossOrigin_Refused(t *testing.T) {
	t.Parallel()
	handler, server := bootstrapTokenDirectFixture(t, "admin-secret")
	handler.SetBootstrapTokenExposable(true)

	req := loopbackRequest(http.MethodGet, "/v1/bootstrap-token", "127.0.0.1:54321")
	req.Header.Set("Origin", "http://evil.example.com")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// TestBootstrapToken_SameOrigin_Allowed: a same-origin Origin header
// passes the cross-origin check.
func TestBootstrapToken_SameOrigin_Allowed(t *testing.T) {
	t.Parallel()
	handler, server := bootstrapTokenDirectFixture(t, "admin-secret")
	handler.SetBootstrapTokenExposable(true)

	req := loopbackRequest(http.MethodGet, "/v1/bootstrap-token", "127.0.0.1:54321")
	// httptest.NewRequest sets req.Host to "example.com" by default,
	// so we mirror that on the Origin header for the same-origin path.
	req.Header.Set("Origin", "http://"+req.Host)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// TestBootstrapToken_LoopbackOriginMismatchedHost_Allowed: in dev a
// Vite proxy forwards browser requests from http://localhost:5173 to
// the gateway on http://127.0.0.1:8765. Origin and Host disagree but
// both endpoints are loopback — and the loopback peer check above
// already gates entry. We accept this case so `bun run dev` doesn't
// trip the cross-origin guard.
func TestBootstrapToken_LoopbackOriginMismatchedHost_Allowed(t *testing.T) {
	t.Parallel()
	handler, server := bootstrapTokenDirectFixture(t, "admin-secret")
	handler.SetBootstrapTokenExposable(true)

	cases := []string{
		"http://localhost:5173",
		"http://127.0.0.1:5173",
		"http://[::1]:5173",
	}
	for _, origin := range cases {
		t.Run(origin, func(t *testing.T) {
			req := loopbackRequest(http.MethodGet, "/v1/bootstrap-token", "127.0.0.1:54321")
			// Force a Host that differs from Origin to mimic the proxy.
			req.Host = "127.0.0.1:8765"
			req.Header.Set("Origin", origin)
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestBootstrapToken_EmptyToken_Refused: nothing to hand out, refuse.
func TestBootstrapToken_EmptyToken_Refused(t *testing.T) {
	t.Parallel()
	handler, server := bootstrapTokenDirectFixture(t, "")
	handler.SetBootstrapTokenExposable(true)

	req := loopbackRequest(http.MethodGet, "/v1/bootstrap-token", "127.0.0.1:54321")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}
