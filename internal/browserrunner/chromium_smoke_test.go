package browserrunner

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestChromiumInspectorSmoke is deliberately opt-in: ordinary unit tests do
// not need a locally installed browser. It gives release and developer checks
// a fast real-runtime proof that Hecate launches an isolated profile, captures
// text evidence, and does not carry cookies between inspections.
func TestChromiumInspectorSmoke(t *testing.T) {
	if os.Getenv("HECATE_BROWSER_SMOKE") != "1" {
		t.Skip("set HECATE_BROWSER_SMOKE=1 with HECATE_TASK_BROWSER_EXECUTABLE to run Chromium smoke")
	}
	executable := strings.TrimSpace(os.Getenv("HECATE_TASK_BROWSER_EXECUTABLE"))
	if executable == "" {
		t.Fatal("HECATE_TASK_BROWSER_EXECUTABLE is required when HECATE_BROWSER_SMOKE=1")
	}

	var sawPriorCookie atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if _, err := r.Cookie("hecate_browser_smoke"); err == nil {
			sawPriorCookie.Store(true)
		}
		http.SetCookie(w, &http.Cookie{Name: "hecate_browser_smoke", Value: "fresh-profile", Path: "/"})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Hecate browser smoke</title></head><body><main><h1>Isolated browser evidence</h1><p>local smoke page</p></main></body></html>`))
	}))
	defer server.Close()

	inspector, err := New(Config{
		ExecutablePath:  executable,
		Timeout:         30 * time.Second,
		AllowPrivateIPs: true, // httptest is intentionally loopback.
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	origin := parsed.Scheme + "://" + parsed.Host
	for attempt := 0; attempt < 2; attempt++ {
		t.Logf("inspection attempt %d", attempt+1)
		result, err := inspector.Inspect(context.Background(), InspectRequest{
			URL:            server.URL + "/",
			AllowedOrigins: []string{origin},
		})
		if err != nil {
			t.Fatalf("Inspect() attempt %d error = %v", attempt+1, err)
		}
		if result.FinalOrigin != origin || !strings.Contains(result.Title, "Hecate browser smoke") {
			t.Fatalf("Inspect() attempt %d result = %+v, want %s and page title", attempt+1, result, origin)
		}
		if len(result.Accessibility) == 0 {
			t.Fatalf("Inspect() attempt %d returned no accessibility evidence", attempt+1)
		}
		t.Logf("inspection attempt %d completed", attempt+1)
	}
	// The test server listens on 127.0.0.1 while the target uses localhost.
	// This proves Chromium honors the numeric hostname map produced by the
	// preflight rather than independently resolving the requested hostname.
	pinnedOrigin := "http://localhost:" + parsed.Port()
	pinnedResult, err := inspector.Inspect(context.Background(), InspectRequest{
		URL:            pinnedOrigin + "/",
		AllowedOrigins: []string{pinnedOrigin},
	})
	if err != nil {
		t.Fatalf("Inspect() hostname-pinned target error = %v", err)
	}
	if pinnedResult.FinalOrigin != pinnedOrigin {
		t.Fatalf("Inspect() hostname-pinned result = %+v, want %s", pinnedResult, pinnedOrigin)
	}
	if sawPriorCookie.Load() {
		t.Fatal("browser inspection reused a cookie across fresh profiles")
	}
}

// TestChromiumInspectorSmokeDisablesPageScripts is opt-in for the same reason
// as the basic smoke test. It proves a same-origin iframe cannot use a
// script-created worker or WebSocket to reach a different origin outside the
// ordinary URL-loader interception boundary.
func TestChromiumInspectorSmokeDisablesPageScripts(t *testing.T) {
	if os.Getenv("HECATE_BROWSER_SMOKE") != "1" {
		t.Skip("set HECATE_BROWSER_SMOKE=1 with HECATE_TASK_BROWSER_EXECUTABLE to run Chromium smoke")
	}
	executable := strings.TrimSpace(os.Getenv("HECATE_TASK_BROWSER_EXECUTABLE"))
	if executable == "" {
		t.Fatal("HECATE_TASK_BROWSER_EXECUTABLE is required when HECATE_BROWSER_SMOKE=1")
	}

	var foreignHits atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		foreignHits.Add(1)
	}))
	defer foreign.Close()
	webSocketURL := "ws" + strings.TrimPrefix(foreign.URL, "http") + "/socket"
	var approvedSocketHits atomic.Int32
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/socket" && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			approvedSocketHits.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/script-frame" {
			approvedWebSocketURL := "ws://" + r.Host + "/socket"
			_, _ = fmt.Fprintf(w, `<!doctype html><html><body><script>new Worker("/worker.js"); new WebSocket(%q); new WebSocket(%q)</script><p>frame scripts must not execute</p></body></html>`, approvedWebSocketURL, webSocketURL)
			return
		}
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>static evidence</title></head><body><iframe src="/script-frame"></iframe><p>frame scripts must not execute</p></body></html>`))
	}))
	defer page.Close()
	parsed, err := url.Parse(page.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}

	inspector, err := New(Config{
		ExecutablePath:  executable,
		Timeout:         30 * time.Second,
		AllowPrivateIPs: true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := inspector.Inspect(context.Background(), InspectRequest{
		URL:            page.URL,
		AllowedOrigins: []string{parsed.Scheme + "://" + parsed.Host},
	})
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if !strings.Contains(result.Title, "static evidence") {
		t.Fatalf("Inspect() result = %+v, want static evidence title", result)
	}
	if hits := foreignHits.Load(); hits != 0 {
		t.Fatalf("page script reached foreign origin %d time(s)", hits)
	}
	if hits := approvedSocketHits.Load(); hits != 0 {
		t.Fatalf("page script opened %d WebSocket(s) at the approved origin", hits)
	}
}

// TestChromiumInspectorSmokeCapsChunkedResponses proves the CDP event path
// cancels an actual unknown-length response rather than only accounting for a
// synthetic Network.dataReceived event in a unit test.
func TestChromiumInspectorSmokeCapsChunkedResponses(t *testing.T) {
	if os.Getenv("HECATE_BROWSER_SMOKE") != "1" {
		t.Skip("set HECATE_BROWSER_SMOKE=1 with HECATE_TASK_BROWSER_EXECUTABLE to run Chromium smoke")
	}
	executable := strings.TrimSpace(os.Getenv("HECATE_TASK_BROWSER_EXECUTABLE"))
	if executable == "" {
		t.Fatal("HECATE_TASK_BROWSER_EXECUTABLE is required when HECATE_BROWSER_SMOKE=1")
	}

	clientCanceled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("test server response does not support streaming")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Flush before writing the body so net/http commits a chunked response
		// rather than calculating a Content-Length for the whole page.
		flusher.Flush()
		if _, err := fmt.Fprint(w, "<!doctype html><html><head><title>chunked evidence</title></head><body>"); err != nil {
			return
		}
		flusher.Flush()
		chunk := strings.Repeat("x", 64<<10)
		for sent := 0; sent <= browserResponseCancellationThresholdBytes+(256<<10); sent += len(chunk) {
			if _, err := w.Write([]byte(chunk)); err != nil {
				select {
				case clientCanceled <- struct{}{}:
				default:
				}
				return
			}
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
			select {
			case clientCanceled <- struct{}{}:
			default:
			}
		case <-time.After(3 * time.Second):
		}
	}))
	defer server.Close()

	inspector, err := New(Config{
		ExecutablePath:  executable,
		Timeout:         30 * time.Second,
		AllowPrivateIPs: true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	_, err = inspector.Inspect(context.Background(), InspectRequest{
		URL:            server.URL,
		AllowedOrigins: []string{parsed.Scheme + "://" + parsed.Host},
	})
	if !errors.Is(err, ErrInspectionFailed) {
		t.Fatalf("Inspect() error = %v, want ErrInspectionFailed after response cancellation threshold", err)
	}
	select {
	case <-clientCanceled:
	case <-time.After(3 * time.Second):
		t.Fatal("browser inspection did not cancel the chunked response after the response threshold")
	}
}
