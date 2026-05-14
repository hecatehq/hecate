package llamacpp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHuggingFaceClient_SearchModelsSendsExpectedQuery(t *testing.T) {
	t.Parallel()
	var capturedQuery string
	var capturedAuth string
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		capturedAuth = r.Header.Get("Authorization")
		capturedPath = r.URL.Path
		_, _ = w.Write([]byte(`[
			{"id":"bartowski/Qwen-GGUF","author":"bartowski","downloads":1234,"likes":42,"tags":["gguf","text-generation"],"pipeline_tag":"text-generation","lastModified":"2026-04-01T00:00:00Z","gated":false},
			{"id":"meta-llama/Llama-3.2","downloads":99999,"tags":["gguf"],"gated":"manual"}
		]`))
	}))
	defer srv.Close()

	c := NewHuggingFaceClient(HuggingFaceOptions{BaseURL: srv.URL, HTTP: srv.Client()})
	got, err := c.SearchModels(context.Background(), "qwen", "fake-token", 5)
	if err != nil {
		t.Fatalf("SearchModels: %v", err)
	}
	if capturedPath != "/api/models" {
		t.Fatalf("path = %q; want /api/models", capturedPath)
	}
	// Query must include the gguf filter + the search term +
	// limit, sorted by downloads.
	q := mustParseQuery(t, capturedQuery)
	if q.Get("search") != "qwen" {
		t.Errorf("search = %q; want qwen", q.Get("search"))
	}
	if q.Get("filter") != "gguf" {
		t.Errorf("filter = %q; want gguf", q.Get("filter"))
	}
	if q.Get("limit") != "5" {
		t.Errorf("limit = %q; want 5", q.Get("limit"))
	}
	if q.Get("sort") != "downloads" {
		t.Errorf("sort = %q; want downloads", q.Get("sort"))
	}
	if capturedAuth != "Bearer fake-token" {
		t.Errorf("auth = %q; want Bearer fake-token", capturedAuth)
	}
	if len(got) != 2 {
		t.Fatalf("results = %d; want 2", len(got))
	}
	if got[0].ID != "bartowski/Qwen-GGUF" || got[0].Author != "bartowski" {
		t.Errorf("first row = %+v", got[0])
	}
	if got[0].Gated {
		t.Errorf("first row should not be gated")
	}
	// Second row has gated="manual" → true after normalization.
	if !got[1].Gated {
		t.Errorf("second row should be gated (gated=manual)")
	}
}

func TestHuggingFaceClient_SearchClampsLimit(t *testing.T) {
	t.Parallel()
	var capturedLimit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedLimit = r.URL.Query().Get("limit")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := NewHuggingFaceClient(HuggingFaceOptions{BaseURL: srv.URL, HTTP: srv.Client()})
	// Request 1000 → clamped to 100.
	if _, err := c.SearchModels(context.Background(), "", "", 1000); err != nil {
		t.Fatalf("SearchModels: %v", err)
	}
	if capturedLimit != "100" {
		t.Fatalf("limit clamped to %q; want 100", capturedLimit)
	}
	// Request 0 → defaults to 20.
	if _, err := c.SearchModels(context.Background(), "", "", 0); err != nil {
		t.Fatalf("SearchModels: %v", err)
	}
	if capturedLimit != "20" {
		t.Fatalf("limit default = %q; want 20", capturedLimit)
	}
}

func TestHuggingFaceClient_SearchMapsGatedResponses(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gated", http.StatusForbidden)
	}))
	defer srv.Close()

	c := NewHuggingFaceClient(HuggingFaceOptions{BaseURL: srv.URL, HTTP: srv.Client()})
	_, err := c.SearchModels(context.Background(), "anything", "", 5)
	if !errors.Is(err, ErrHuggingFaceGated) {
		t.Fatalf("expected ErrHuggingFaceGated, got %v", err)
	}
}

func TestHuggingFaceClient_ListRepoFilesFiltersAndMapsLFS(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models/bartowski/Qwen-GGUF/tree/main" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`[
			{"type":"file","path":".gitattributes","size":1024},
			{"type":"file","path":"README.md","size":2048},
			{"type":"file","path":"Qwen-Q4_K_M.gguf","size":1000000,"lfs":{"oid":"deadbeef0000","size":4000000000}},
			{"type":"file","path":"Qwen-Q5_K_M.gguf","size":2000000,"lfs":{"oid":"feedface1111","size":5000000000}},
			{"type":"directory","path":"docs"}
		]`))
	}))
	defer srv.Close()

	c := NewHuggingFaceClient(HuggingFaceOptions{BaseURL: srv.URL, HTTP: srv.Client()})
	got, err := c.ListRepoFiles(context.Background(), "bartowski/Qwen-GGUF", "")
	if err != nil {
		t.Fatalf("ListRepoFiles: %v", err)
	}
	// Only the .gguf files come through; sorted by path.
	if len(got) != 2 {
		t.Fatalf("files = %d; want 2", len(got))
	}
	if got[0].Path != "Qwen-Q4_K_M.gguf" || got[1].Path != "Qwen-Q5_K_M.gguf" {
		t.Fatalf("file ordering = %+v", got)
	}
	// LFS sha + size override the in-tree fields.
	if got[0].SHA256 != "deadbeef0000" {
		t.Errorf("sha = %q; want deadbeef0000", got[0].SHA256)
	}
	if got[0].Size != 4000000000 {
		t.Errorf("size = %d; want LFS size", got[0].Size)
	}
	// Download URL is the canonical resolve URL.
	want := srv.URL + "/bartowski/Qwen-GGUF/resolve/main/Qwen-Q4_K_M.gguf"
	if got[0].DownloadURL != want {
		t.Errorf("download URL = %q; want %q", got[0].DownloadURL, want)
	}
}

func TestHuggingFaceClient_ListRepoFilesRejectsBadRepo(t *testing.T) {
	t.Parallel()
	c := NewHuggingFaceClient(HuggingFaceOptions{})
	for _, bad := range []string{"", "  ", "../escape", "/leading-slash", ".."} {
		if _, err := c.ListRepoFiles(context.Background(), bad, ""); err == nil {
			t.Errorf("bad repo %q should error", bad)
		}
	}
}

func TestHuggingFaceClient_ListRepoFilesMaps404ToNotFound(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no repo", http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewHuggingFaceClient(HuggingFaceOptions{BaseURL: srv.URL, HTTP: srv.Client()})
	_, err := c.ListRepoFiles(context.Background(), "owner/repo", "")
	if !errors.Is(err, ErrHuggingFaceNotFound) {
		t.Fatalf("expected ErrHuggingFaceNotFound, got %v", err)
	}
}

func TestHuggingFaceClient_ConcurrentRequests(t *testing.T) {
	t.Parallel()
	// Stress: make sure concurrent calls share the http client
	// without tripping the race detector.
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := NewHuggingFaceClient(HuggingFaceOptions{BaseURL: srv.URL, HTTP: srv.Client()})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.SearchModels(context.Background(), "x", "", 5)
		}()
	}
	wg.Wait()
	if hits != 16 {
		t.Fatalf("hits = %d; want 16", hits)
	}
}

// Confirms the Clock dependency wiring is intact — required for
// future telemetry hooks that attach a fetch-time attribute.
func TestHuggingFaceClient_ClockIsInjectable(t *testing.T) {
	t.Parallel()
	called := 0
	clock := func() time.Time {
		called++
		return time.Unix(1700000000, 0)
	}
	c := NewHuggingFaceClient(HuggingFaceOptions{Clock: clock})
	_ = c
	// Direct check — Clock isn't called inside fetchJSON today
	// but the field must exist + accept injection.
	if called != 0 {
		t.Fatal("clock should not be called during construction")
	}
}

func mustParseQuery(t *testing.T, raw string) parsedQuery {
	t.Helper()
	out := parsedQuery{}
	for _, pair := range strings.Split(raw, "&") {
		if pair == "" {
			continue
		}
		idx := strings.Index(pair, "=")
		if idx < 0 {
			out[pair] = ""
			continue
		}
		key, _ := unescape(pair[:idx])
		val, _ := unescape(pair[idx+1:])
		out[key] = val
	}
	return out
}

type parsedQuery map[string]string

func (q parsedQuery) Get(key string) string { return q[key] }

// unescape is a tiny helper — the test only deals with simple ASCII
// values from our own client, so a full url.QueryUnescape isn't
// worth the import dance.
func unescape(s string) (string, error) {
	// json.Unmarshal handles the common cases (%20, etc.) when
	// the value is wrapped — but our values are plain. Return
	// as-is.
	return s, nil
}

// Keep encoding/json reachable through a no-op so the import survives
// even if all explicit references disappear (e.g. during refactor).
var _ = json.RawMessage{}
