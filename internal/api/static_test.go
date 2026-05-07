package api

import (
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// fakeUIFS is a minimal in-memory UI bundle. It uses fstest.MapFS so every
// test gets a deterministic FS regardless of whether ui/dist is populated on
// the developer's machine.
func fakeUIFS() fs.FS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte("<!doctype html><title>real ui</title>"),
		},
		"assets/app.js": &fstest.MapFile{
			Data: []byte("console.log('hello')"),
		},
	}
}

// emptyUIFS represents the dev case: only the .gitkeep placeholder is in
// ui/dist, no index.html.
func emptyUIFS() fs.FS {
	return fstest.MapFS{
		".gitkeep": &fstest.MapFile{Data: []byte("placeholder\n")},
	}
}

func TestStaticHandlerServesIndexAtRoot(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	staticUIHandlerFromFS(fakeUIFS()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "real ui") {
		t.Errorf("expected index.html body, got: %s", body)
	}
}

func TestStaticHandlerServesAssets(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	staticUIHandlerFromFS(fakeUIFS()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, expected javascript", ct)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "console.log") {
		t.Errorf("asset body unexpected: %s", body)
	}
}

// TestStaticHandlerSPAFallback covers the core SPA-routing requirement:
// requests to client-side routes that don't correspond to embedded files
// must serve index.html so React Router can take over.
func TestStaticHandlerSPAFallback(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	staticUIHandlerFromFS(fakeUIFS()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SPA fallback to index.html)", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "real ui") {
		t.Errorf("SPA fallback didn't serve index.html: %s", body)
	}
}

func TestStaticHandlerDoesNotFallbackForUnknownAPIPath(t *testing.T) {
	for _, requestPath := range []string{
		"/v1",
		"/v1/tasks",
		"/hecate/v1",
		"/hecate/v1/unknown",
		"/admin",
		"/admin/control-plane",
	} {
		t.Run(requestPath, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, requestPath, nil)
			staticUIHandlerFromFS(fakeUIFS()).ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
		})
	}
}

// TestStaticHandlerServesFallbackWhenUINotBuilt covers the common dev case
// where the binary was built without first running `make ui-build` — the
// embedded ui/dist contains only the .gitkeep placeholder. The handler must
// not 500 or 404; it returns a friendly HTML page that points at the fix.
func TestStaticHandlerServesFallbackWhenUINotBuilt(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	staticUIHandlerFromFS(emptyUIFS()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "UI bundle wasn't embedded") {
		t.Errorf("fallback HTML missing expected marker; got: %s", body)
	}
}

func TestStaticHandlerServesFallbackWhenFSIsNil(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	staticUIHandlerFromFS(nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "UI bundle wasn't embedded") {
		t.Errorf("fallback HTML missing expected marker; got: %s", body)
	}
}

// TestStaticHandlerRejectsTraversal hard-404s any path containing "..". The
// fs.FS contract on most implementations would already reject this, but the
// explicit guard prevents a future FS swap from quietly opening a hole.
func TestStaticHandlerRejectsTraversal(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/../../etc/passwd", nil)
	staticUIHandlerFromFS(fakeUIFS()).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for traversal attempt", rec.Code)
	}
}

// TestServerMountsStaticHandler verifies the static handler is reachable
// through the full middleware chain and that API routes still take
// precedence over the catch-all. We can't drive the static handler with a
// fake FS through NewServer (the wiring uses the package-default), so this
// test checks routing behavior, not body content.
func TestServerMountsStaticHandler(t *testing.T) {
	srv := NewServer(quietLogger(), &Handler{})

	// /v1/models is an API route → must NOT be the static fallback page.
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	body, _ := io.ReadAll(rec.Body)
	if strings.Contains(string(body), "UI bundle wasn't embedded") {
		t.Error("/v1/models incorrectly served by static fallback")
	}

	for _, requestPath := range []string{"/v1", "/hecate/v1", "/admin"} {
		t.Run(requestPath, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, requestPath, nil))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
		})
	}

	// An unknown root path → must reach the static handler. Whether it
	// serves the fallback or a real bundle depends on whether ui/dist is
	// populated on disk during `go test`; either way the response should be
	// 200 and look like HTML, not 404.
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/some-spa-route", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d for SPA route, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}
