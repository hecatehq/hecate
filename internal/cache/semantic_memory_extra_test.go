package cache

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/pkg/types"
)

func TestMemorySemanticStorePruneByMaxAge(t *testing.T) {
	store := NewMemorySemanticStore(0, 100, LocalSimpleEmbedder{Dimensions: 32})
	ctx := context.Background()

	for _, text := range []string{"hello world alpha", "another quick fox", "the third entry"} {
		if err := store.Set(ctx, SemanticEntry{
			Namespace: "tenant-a",
			Text:      text,
			Response:  &types.ChatResponse{ID: "resp"},
		}); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	// Backdate the first two records so a Prune(maxAge=1h, _) drops them.
	store.mu.Lock()
	store.entries[0].storedAt = time.Now().Add(-2 * time.Hour)
	store.entries[1].storedAt = time.Now().Add(-2 * time.Hour)
	store.mu.Unlock()

	deleted, err := store.Prune(ctx, time.Hour, 0)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	store.mu.RLock()
	defer store.mu.RUnlock()
	if len(store.entries) != 1 {
		t.Errorf("remaining entries = %d, want 1", len(store.entries))
	}
}

func TestMemorySemanticStorePruneByMaxCount(t *testing.T) {
	store := NewMemorySemanticStore(0, 100, LocalSimpleEmbedder{Dimensions: 32})
	ctx := context.Background()

	for _, text := range []string{"first entry alpha", "second entry beta", "third entry gamma", "fourth entry delta"} {
		if err := store.Set(ctx, SemanticEntry{Namespace: "ns", Text: text, Response: &types.ChatResponse{}}); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	deleted, err := store.Prune(ctx, 0, 2)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2 (4 entries trimmed to 2)", deleted)
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	if len(store.entries) != 2 {
		t.Errorf("remaining entries = %d, want 2", len(store.entries))
	}
}

func TestMemorySemanticStorePruneRemovesExpired(t *testing.T) {
	store := NewMemorySemanticStore(0, 100, LocalSimpleEmbedder{Dimensions: 32})
	ctx := context.Background()

	if err := store.Set(ctx, SemanticEntry{Namespace: "ns", Text: "expiring text alpha", Response: &types.ChatResponse{}, ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Set(ctx, SemanticEntry{Namespace: "ns", Text: "still valid beta", Response: &types.ChatResponse{}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	deleted, err := store.Prune(ctx, 0, 0)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1 (expired entry only)", deleted)
	}
}

func TestMemorySemanticStoreSetEnforcesMaxEntries(t *testing.T) {
	store := NewMemorySemanticStore(0, 2, LocalSimpleEmbedder{Dimensions: 32})
	ctx := context.Background()

	for _, text := range []string{"one alpha bravo", "two charlie delta", "three echo foxtrot", "four golf hotel"} {
		if err := store.Set(ctx, SemanticEntry{Namespace: "ns", Text: text, Response: &types.ChatResponse{}}); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	store.mu.RLock()
	defer store.mu.RUnlock()
	if len(store.entries) != 2 {
		t.Fatalf("entries length = %d, want 2 (oldest must be evicted)", len(store.entries))
	}
	// The two surviving entries should be the most-recently inserted, so
	// "three" and "four" should appear and "one"/"two" should not.
	keptTexts := []string{store.entries[0].entry.Text, store.entries[1].entry.Text}
	for _, want := range []string{"three echo foxtrot", "four golf hotel"} {
		found := false
		for _, kt := range keptTexts {
			if kt == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %q to remain, kept = %v", want, keptTexts)
		}
	}
}

func TestEligibleForSemanticCacheRejectsTimeSensitiveQueries(t *testing.T) {
	cases := []struct {
		name string
		req  types.ChatRequest
		want bool
	}{
		{
			name: "stable factual question is eligible",
			req:  types.ChatRequest{Messages: []types.Message{{Role: "user", Content: "What is the speed of light?"}}},
			want: true,
		},
		{
			name: "today is excluded",
			req:  types.ChatRequest{Messages: []types.Message{{Role: "user", Content: "Tell me about today"}}},
			want: false,
		},
		{
			name: "stock price is excluded",
			req:  types.ChatRequest{Messages: []types.Message{{Role: "user", Content: "What's the AAPL stock price?"}}},
			want: false,
		},
		{
			name: "weather is excluded",
			req:  types.ChatRequest{Messages: []types.Message{{Role: "user", Content: "Will it rain? weather forecast please"}}},
			want: false,
		},
		{
			name: "empty messages is not eligible",
			req:  types.ChatRequest{},
			want: false,
		},
		{
			name: "messages with empty content are not eligible",
			req:  types.ChatRequest{Messages: []types.Message{{Role: "user", Content: "   "}}},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EligibleForSemanticCache(tc.req, 4096); got != tc.want {
				t.Errorf("EligibleForSemanticCache = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildSemanticTextSkipsEmptyContent(t *testing.T) {
	req := types.ChatRequest{Messages: []types.Message{
		{Role: "user", Content: "  "},
		{Role: "", Content: "anonymous body"},
		{Role: "assistant", Content: "the answer"},
	}}
	got := BuildSemanticText(req, 4096)
	// The empty-content row is skipped; the no-role row falls back to "message".
	if got == "" {
		t.Fatal("BuildSemanticText returned empty")
	}
	wantSubstrings := []string{"message: anonymous body", "assistant: the answer"}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("BuildSemanticText output %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "user:") {
		t.Errorf("user line with empty content should be skipped, got %q", got)
	}
}

func TestMemorySemanticStoreStatsCountsNonExpired(t *testing.T) {
	t.Parallel()

	store := NewMemorySemanticStore(0, 100, LocalSimpleEmbedder{Dimensions: 32})
	ctx := context.Background()

	for i := range 4 {
		if err := store.Set(ctx, SemanticEntry{
			Namespace: "ns",
			Text:      fmt.Sprintf("entry %d alpha beta gamma", i),
			Response:  &types.ChatResponse{},
			ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatalf("Set(%d): %v", i, err)
		}
	}
	// Expire two entries in place.
	store.mu.Lock()
	store.entries[0].entry.ExpiresAt = time.Now().Add(-time.Minute)
	store.entries[1].entry.ExpiresAt = time.Now().Add(-time.Minute)
	store.mu.Unlock()

	count, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats(): %v", err)
	}
	if count != 2 {
		t.Errorf("Stats() = %d, want 2 (only non-expired entries)", count)
	}
}

func TestMemorySemanticStoreStatsZeroOnEmpty(t *testing.T) {
	t.Parallel()

	store := NewMemorySemanticStore(0, 100, LocalSimpleEmbedder{Dimensions: 32})
	count, err := store.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats(): %v", err)
	}
	if count != 0 {
		t.Errorf("Stats() = %d, want 0 on empty store", count)
	}
}

func TestMemorySemanticStoreListNewestFirst(t *testing.T) {
	t.Parallel()

	store := NewMemorySemanticStore(0, 100, LocalSimpleEmbedder{Dimensions: 32})
	ctx := context.Background()
	texts := []string{
		"first entry alpha bravo",
		"second entry charlie delta",
		"third entry echo foxtrot",
	}
	for _, text := range texts {
		if err := store.Set(ctx, SemanticEntry{
			Namespace: "ns",
			Text:      text,
			Response:  &types.ChatResponse{},
			ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatalf("Set(%q): %v", text, err)
		}
	}

	metas, err := store.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(metas) != 3 {
		t.Fatalf("List() len = %d, want 3", len(metas))
	}
	// Newest (last inserted) should be first.
	if metas[0].TextSnippet != "third entry echo foxtrot" {
		t.Errorf("metas[0].TextSnippet = %q, want third entry", metas[0].TextSnippet)
	}
	if metas[2].TextSnippet != "first entry alpha bravo" {
		t.Errorf("metas[2].TextSnippet = %q, want first entry", metas[2].TextSnippet)
	}
	for _, m := range metas {
		if m.Namespace != "ns" {
			t.Errorf("Namespace = %q, want ns", m.Namespace)
		}
		if m.StoredAt.IsZero() {
			t.Errorf("StoredAt is zero")
		}
	}
}

func TestMemorySemanticStoreListExcludesExpired(t *testing.T) {
	t.Parallel()

	store := NewMemorySemanticStore(0, 100, LocalSimpleEmbedder{Dimensions: 32})
	ctx := context.Background()

	if err := store.Set(ctx, SemanticEntry{
		Namespace: "ns",
		Text:      "expired entry alpha beta",
		Response:  &types.ChatResponse{},
		ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Set(ctx, SemanticEntry{
		Namespace: "ns",
		Text:      "live entry charlie delta",
		Response:  &types.ChatResponse{},
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	metas, err := store.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("List() len = %d, want 1 (expired entry excluded)", len(metas))
	}
	if metas[0].TextSnippet != "live entry charlie delta" {
		t.Errorf("metas[0].TextSnippet = %q, want live entry", metas[0].TextSnippet)
	}
}

func TestMemorySemanticStoreListPagination(t *testing.T) {
	t.Parallel()

	store := NewMemorySemanticStore(0, 100, LocalSimpleEmbedder{Dimensions: 32})
	ctx := context.Background()

	for i := range 5 {
		if err := store.Set(ctx, SemanticEntry{
			Namespace: "ns",
			Text:      fmt.Sprintf("paginated entry %d alpha beta", i),
			Response:  &types.ChatResponse{},
			ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatalf("Set(%d): %v", i, err)
		}
	}

	page1, err := store.List(ctx, 2, 0)
	if err != nil {
		t.Fatalf("List(limit=2, offset=0): %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page1))
	}

	page2, err := store.List(ctx, 2, 2)
	if err != nil {
		t.Fatalf("List(limit=2, offset=2): %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2 len = %d, want 2", len(page2))
	}

	page3, err := store.List(ctx, 2, 4)
	if err != nil {
		t.Fatalf("List(limit=2, offset=4): %v", err)
	}
	if len(page3) != 1 {
		t.Fatalf("page3 len = %d, want 1 (last page)", len(page3))
	}

	// Pages should be disjoint.
	seen := make(map[string]bool)
	for _, p := range [][]SemanticEntryMeta{page1, page2, page3} {
		for _, m := range p {
			if seen[m.TextSnippet] {
				t.Errorf("duplicate entry %q across pages", m.TextSnippet)
			}
			seen[m.TextSnippet] = true
		}
	}
}

func TestMemorySemanticStoreListSnippetTruncated(t *testing.T) {
	t.Parallel()

	store := NewMemorySemanticStore(0, 100, LocalSimpleEmbedder{Dimensions: 32})
	ctx := context.Background()

	longText := strings.Repeat("a", 300) + " extra words"
	if err := store.Set(ctx, SemanticEntry{
		Namespace: "ns",
		Text:      longText,
		Response:  &types.ChatResponse{},
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	metas, err := store.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("List() len = %d, want 1", len(metas))
	}
	// Snippet must be ≤200 chars + "…" suffix.
	if len([]rune(metas[0].TextSnippet)) > 201 {
		t.Errorf("TextSnippet rune len = %d, want ≤201 (200 chars + ellipsis)", len([]rune(metas[0].TextSnippet)))
	}
	if !strings.HasSuffix(metas[0].TextSnippet, "…") {
		t.Errorf("TextSnippet should end with ellipsis, got %q", metas[0].TextSnippet)
	}
}

func TestMemorySemanticStoreListOffsetBeyondEnd(t *testing.T) {
	t.Parallel()

	store := NewMemorySemanticStore(0, 100, LocalSimpleEmbedder{Dimensions: 32})
	ctx := context.Background()

	if err := store.Set(ctx, SemanticEntry{
		Namespace: "ns",
		Text:      "only entry alpha beta",
		Response:  &types.ChatResponse{},
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	metas, err := store.List(ctx, 10, 100)
	if err != nil {
		t.Fatalf("List(offset=100): %v", err)
	}
	if metas != nil {
		t.Errorf("List(offset beyond end) = %v, want nil", metas)
	}
}

func TestMemorySemanticStoreRetentionRoundtrip(t *testing.T) {
	t.Parallel()

	store := NewMemorySemanticStore(0, 100, LocalSimpleEmbedder{Dimensions: 32})
	ctx := context.Background()

	// Insert three entries; backdate two so a Prune(maxAge=1h, maxCount=0) removes them.
	for i := range 3 {
		if err := store.Set(ctx, SemanticEntry{
			Namespace: "ns",
			Text:      fmt.Sprintf("retention entry %d alpha beta gamma", i),
			Response:  &types.ChatResponse{},
			ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatalf("Set(%d): %v", i, err)
		}
	}
	store.mu.Lock()
	store.entries[0].storedAt = time.Now().Add(-2 * time.Hour)
	store.entries[1].storedAt = time.Now().Add(-2 * time.Hour)
	store.mu.Unlock()

	// Stats before: 3 entries.
	before, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats before: %v", err)
	}
	if before != 3 {
		t.Fatalf("Stats before = %d, want 3", before)
	}

	// Prune entries older than 1 hour.
	deleted, err := store.Prune(ctx, time.Hour, 0)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted != 2 {
		t.Errorf("Prune deleted = %d, want 2", deleted)
	}

	// Stats after: 1 entry.
	after, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats after: %v", err)
	}
	if after != 1 {
		t.Errorf("Stats after = %d, want 1", after)
	}

	// List after: 1 entry, newest first.
	metas, err := store.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("List after: %v", err)
	}
	if len(metas) != 1 {
		t.Errorf("List len after = %d, want 1", len(metas))
	}
}
