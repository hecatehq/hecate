package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/mcp"
	mcpclient "github.com/hecatehq/hecate/internal/mcp/client"
	"github.com/hecatehq/hecate/pkg/types"
)

// fakeMCPHTTPServer spins up an httptest server that speaks just
// enough MCP for the factory test: initialize, tools/list, no
// tools/call needed. Returns the URL plus a counter pointer the
// test reads to detect cache hits vs. misses.
func fakeMCPHTTPServer(t *testing.T, name string) (url string, initCount *int) {
	t.Helper()
	count := 0
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcp.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var result any
		switch req.Method {
		case "initialize":
			count++
			result = mcp.InitializeResult{
				ProtocolVersion: mcp.DeclaredProtocolVersion,
				Capabilities:    mcp.ServerCapabilities{Tools: &mcp.ToolsCapability{}},
				ServerInfo:      mcp.ServerInfo{Name: name, Version: "0"},
			}
		case "tools/list":
			result = mcp.ListToolsResult{Tools: []mcp.Tool{
				{Name: "ping", InputSchema: json.RawMessage(`{}`)},
			}}
		case "notifications/initialized":
			return // notifications get no response
		default:
			http.Error(w, "unsupported", http.StatusBadRequest)
			return
		}
		raw, _ := json.Marshal(result)
		_ = json.NewEncoder(w).Encode(mcp.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  raw,
		})
	}))
	t.Cleanup(hs.Close)
	return hs.URL, &count
}

// TestNewDefaultMCPHostFactory_WithCache_SharesUpstream pins that
// passing a non-nil cache to NewDefaultMCPHostFactory makes two
// factory invocations (= two runs) targeting the same upstream config
// share one Client — i.e. the upstream sees ONE initialize handshake,
// not two. This is the headline reason the cache exists.
//
// Without the cache (cipher-only factory), two factory calls = two
// initialize handshakes. The test runs both shapes to pin the
// distinction.
func TestNewDefaultMCPHostFactory_WithCache_SharesUpstream(t *testing.T) {
	t.Parallel()
	url, initCount := fakeMCPHTTPServer(t, "shared")

	cache := mcpclient.NewSharedClientCache(time.Minute, mcp.ClientInfo{Name: "hecate-factory-test", Version: "0"})
	t.Cleanup(func() { _ = cache.Close() })

	factory := NewDefaultMCPHostFactory(nil, cache)
	configs := []types.MCPServerConfig{
		{Name: "fs", URL: url},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	host1, err := factory(ctx, configs)
	if err != nil {
		t.Fatalf("factory 1: %v", err)
	}
	if host1 == nil {
		t.Fatal("factory 1 returned nil host")
	}
	if err := host1.Close(); err != nil {
		t.Fatalf("host1 Close: %v", err)
	}

	host2, err := factory(ctx, configs)
	if err != nil {
		t.Fatalf("factory 2: %v", err)
	}
	if host2 == nil {
		t.Fatal("factory 2 returned nil host")
	}
	t.Cleanup(func() { _ = host2.Close() })

	if *initCount != 1 {
		t.Errorf("upstream saw %d initialize calls across two factory invocations, want 1 (cache should reuse)", *initCount)
	}
	// Cache should hold exactly one entry — both factory invocations
	// landed on the same key.
	if got := cache.Stats().Entries; got != 1 {
		t.Errorf("cache.Stats.Entries = %d, want 1", got)
	}
}

// TestNewDefaultMCPHostFactory_NoCache_RespawnsEachRun is the
// counter-test: when cache is nil the factory falls back to the
// per-run NewPool path, so two invocations = two initialize
// handshakes. This pins that the cache parameter is actually
// consulted, not just stored and ignored.
func TestNewDefaultMCPHostFactory_NoCache_RespawnsEachRun(t *testing.T) {
	t.Parallel()
	url, initCount := fakeMCPHTTPServer(t, "shared")

	factory := NewDefaultMCPHostFactory(nil, nil)
	configs := []types.MCPServerConfig{
		{Name: "fs", URL: url},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	host1, err := factory(ctx, configs)
	if err != nil {
		t.Fatalf("factory 1: %v", err)
	}
	if err := host1.Close(); err != nil {
		t.Fatalf("host1 Close: %v", err)
	}

	host2, err := factory(ctx, configs)
	if err != nil {
		t.Fatalf("factory 2: %v", err)
	}
	t.Cleanup(func() { _ = host2.Close() })

	if *initCount != 2 {
		t.Errorf("upstream saw %d initialize calls without cache, want 2 (no cache = no reuse)", *initCount)
	}
}
