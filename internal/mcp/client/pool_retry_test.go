package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/mcp"
)

// startFlakyMCPHTTPFixture spins up an httptest.Server that fails
// the first failOnInit attempts at `initialize` (with HTTP 500),
// then succeeds on subsequent ones. Lets retry tests trigger a
// real per-attempt failure path through the JSON-RPC layer rather
// than mocking transport internals.
//
// initCount is the live counter so the test can assert how many
// initialize round-trips the server actually saw.
func startFlakyMCPHTTPFixture(t *testing.T, failOnInit int) (url string, initCount *atomic.Int32) {
	t.Helper()
	var inits atomic.Int32
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcp.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			n := inits.Add(1)
			if int(n) <= failOnInit {
				// Hard 500 — the HTTP transport surfaces this as a
				// non-2xx error from Send, which Initialize wraps as
				// the per-request error tests need to assert on.
				http.Error(w, "boot in progress", http.StatusInternalServerError)
				return
			}
			result := mcp.InitializeResult{
				ProtocolVersion: mcp.DeclaredProtocolVersion,
				Capabilities:    mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{}},
				ServerInfo:      mcp.ServerInfo{Name: "flaky", Version: "0"},
			}
			raw, _ := json.Marshal(result)
			_ = json.NewEncoder(w).Encode(mcp.Response{JSONRPC: "2.0", ID: req.ID, Result: raw})
		case "tools/list":
			result := mcp.ListToolsResult{Tools: []mcp.Tool{
				{Name: "ping", InputSchema: json.RawMessage(`{}`)},
			}}
			raw, _ := json.Marshal(result)
			_ = json.NewEncoder(w).Encode(mcp.Response{JSONRPC: "2.0", ID: req.ID, Result: raw})
		case "notifications/initialized":
			return
		default:
			http.Error(w, "unsupported", http.StatusBadRequest)
		}
	}))
	t.Cleanup(hs.Close)
	return hs.URL, &inits
}

// TestSpawnClient_RetryOnTransientFailure pins the headline
// behavior: a server that fails the first initialize but succeeds
// the second one results in a successful spawn, with the server
// having seen exactly two initialize attempts.
func TestSpawnClient_RetryOnTransientFailure(t *testing.T) {
	t.Parallel()
	url, inits := startFlakyMCPHTTPFixture(t, 1) // fail attempt 1, succeed attempt 2

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, tools, err := spawnClient(ctx, mcp.ClientInfo{Name: "test", Version: "0"}, ServerConfig{
		Name: "flaky",
		URL:  url,
	}, nil)
	if err != nil {
		t.Fatalf("spawnClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if got := inits.Load(); got != 2 {
		t.Errorf("server saw %d init attempts, want 2 (one fail + one success)", got)
	}
	if len(tools) != 1 || tools[0].Name != "ping" {
		t.Errorf("tools = %+v, want [ping]", tools)
	}
}

// TestSpawnClient_FailsAfterMaxAttempts: a server that fails every
// attempt returns the last error to the caller. The retry adds
// at most spawnClientBackoff of latency before the final failure
// — a permanent broken config doesn't wedge the run, just delays
// the diagnostic.
func TestSpawnClient_FailsAfterMaxAttempts(t *testing.T) {
	t.Parallel()
	// Fail 100 init attempts — we only ever try spawnClientMaxAttempts.
	url, inits := startFlakyMCPHTTPFixture(t, 100)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	_, _, err := spawnClient(ctx, mcp.ClientInfo{Name: "test", Version: "0"}, ServerConfig{
		Name: "broken",
		URL:  url,
	}, nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error after max attempts, got nil")
	}
	if !strings.Contains(err.Error(), "HTTP 500") && !strings.Contains(err.Error(), "boot in progress") {
		t.Errorf("err = %v, want it to carry the upstream's 500 diagnostic", err)
	}
	if got := int(inits.Load()); got != spawnClientMaxAttempts {
		t.Errorf("server saw %d init attempts, want %d", got, spawnClientMaxAttempts)
	}
	// Backoff between attempts adds (maxAttempts-1) * backoff of
	// latency; we allow some slack but cap the upper bound so a
	// regression that bumped maxAttempts trips the test.
	minExpected := time.Duration(spawnClientMaxAttempts-1) * spawnClientBackoff
	maxExpected := minExpected + 2*time.Second
	if elapsed < minExpected || elapsed > maxExpected {
		t.Errorf("elapsed = %v, want %v..%v", elapsed, minExpected, maxExpected)
	}
}

// TestSpawnClient_NoRetryOnFirstSuccess pins that the retry loop
// runs exactly once on the happy path — a regression that
// double-spawned every healthy server would silently double the
// upstream's startup wire chatter.
func TestSpawnClient_NoRetryOnFirstSuccess(t *testing.T) {
	t.Parallel()
	url, inits := startFlakyMCPHTTPFixture(t, 0) // never fail

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, _, err := spawnClient(ctx, mcp.ClientInfo{Name: "test", Version: "0"}, ServerConfig{
		Name: "healthy",
		URL:  url,
	}, nil)
	if err != nil {
		t.Fatalf("spawnClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if got := inits.Load(); got != 1 {
		t.Errorf("server saw %d init attempts, want 1 (no retry on success)", got)
	}
}

// TestSpawnClient_ContextCancelAbortsRetry: a ctx that fires
// during the backoff between attempts must abort the loop with
// the ctx error rather than waiting out the full backoff. Pins
// the responsiveness contract — runner shutdown needs to interrupt
// in-flight spawns promptly, not block on a half-finished retry.
func TestSpawnClient_ContextCancelAbortsRetry(t *testing.T) {
	t.Parallel()
	url, _ := startFlakyMCPHTTPFixture(t, 100) // fail every attempt

	// Deadline shorter than spawnClientBackoff but longer than the
	// first failed attempt — guarantees we're inside the backoff
	// when ctx fires.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err := spawnClient(ctx, mcp.ClientInfo{Name: "test", Version: "0"}, ServerConfig{
		Name: "broken",
		URL:  url,
	}, nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error from ctx cancellation, got nil")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("err = %v, want it to mention the ctx deadline", err)
	}
	// Should bail well before the full backoff — within ~250ms of
	// the ctx deadline. Slack for cold-start scheduling.
	if elapsed > 400*time.Millisecond {
		t.Errorf("elapsed = %v, want < 400ms (should bail on ctx, not wait full backoff)", elapsed)
	}
}
