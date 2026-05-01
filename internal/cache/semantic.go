package cache

// SemanticStore backend coverage:
//
//   - memory   → in-RAM cosine similarity. Fine for ≲10k entries;
//                rebuilds on restart.
//   - postgres → pgvector with HNSW or IVFFlat for indexed search at
//                >100k entries.
//   - sqlite   → INTENTIONALLY UNSUPPORTED. Indexed vector search in
//                SQLite needs the sqlite-vec extension, which is C and
//                only loads into native (mattn, CGO) or WASM (ncruces,
//                Wazero) SQLite drivers. The gateway uses
//                modernc.org/sqlite — a pure-Go ccgo translation — to
//                keep the single-static-binary story; modernc cannot
//                load native extensions. To change this either (a)
//                accept Wazero by switching to ncruces, or (b) accept
//                CGO by switching to mattn. Until then, deploys that
//                need persistent semantic cache should run Postgres
//                for THIS subsystem only — backends are selected
//                per-subsystem, so the rest of the state can still
//                live in SQLite. Bare-bones single-node deploys
//                default to memory and that's the right call for
//                small entry counts.

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hecate/agent-runtime/internal/models"
	"github.com/hecate/agent-runtime/internal/requestscope"
	"github.com/hecate/agent-runtime/pkg/types"
)

type SemanticStore interface {
	Search(ctx context.Context, query SemanticQuery) (*SemanticMatch, bool)
	Set(ctx context.Context, entry SemanticEntry) error
	// Stats returns the count of non-expired entries currently held by the
	// store. Used by the admin status endpoint; errors are non-fatal.
	Stats(ctx context.Context) (int, error)
	// List returns entry metadata in reverse-insertion order, newest first.
	// limit/offset provide simple pagination. Used by the admin entries
	// endpoint; returns nil slice (not error) when the store is empty.
	List(ctx context.Context, limit, offset int) ([]SemanticEntryMeta, error)
}

// SemanticEntryMeta is a lightweight view of a cached entry returned by
// SemanticStore.List — it carries just enough for the admin UI to show a
// useful table row without deserialising the full ChatResponse.
type SemanticEntryMeta struct {
	Namespace   string
	TextSnippet string // first ≤200 chars of the indexed text
	ExpiresAt   time.Time
	StoredAt    time.Time
}

type SemanticQuery struct {
	Namespace     string
	Text          string
	MinSimilarity float64
	MaxTextChars  int
}

type SemanticMatch struct {
	Response   *types.ChatResponse
	Similarity float64
	Strategy   string
	IndexType  string
}

type SemanticEntry struct {
	Namespace string
	Text      string
	Response  *types.ChatResponse
	ExpiresAt time.Time
}

type MemorySemanticStore struct {
	mu         sync.RWMutex
	entries    []semanticRecord
	defaultTTL time.Duration
	maxEntries int
	embedder   Embedder
}

type semanticRecord struct {
	entry    SemanticEntry
	vector   []float64
	storedAt time.Time
}

func NewMemorySemanticStore(defaultTTL time.Duration, maxEntries int, embedder Embedder) *MemorySemanticStore {
	if maxEntries <= 0 {
		maxEntries = 10_000
	}
	if embedder == nil {
		embedder = LocalSimpleEmbedder{}
	}
	return &MemorySemanticStore{
		entries:    make([]semanticRecord, 0, maxEntries),
		defaultTTL: defaultTTL,
		maxEntries: maxEntries,
		embedder:   embedder,
	}
}

func (s *MemorySemanticStore) Search(ctx context.Context, query SemanticQuery) (*SemanticMatch, bool) {
	text := query.Text
	if query.MaxTextChars > 0 && len(text) > query.MaxTextChars {
		text = text[:query.MaxTextChars]
	}
	queryVector, err := s.embedder.Embed(ctx, text)
	if err != nil {
		return nil, false
	}
	if len(queryVector) == 0 {
		return nil, false
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	filtered := s.entries[:0]
	var best *SemanticMatch
	for _, record := range s.entries {
		if !record.entry.ExpiresAt.IsZero() && now.After(record.entry.ExpiresAt) {
			continue
		}
		filtered = append(filtered, record)
		if record.entry.Namespace != query.Namespace {
			continue
		}
		score := cosineSimilarity(queryVector, record.vector)
		if score < query.MinSimilarity {
			continue
		}
		if best == nil || score > best.Similarity {
			cloned := cloneChatResponse(record.entry.Response)
			best = &SemanticMatch{
				Response:   cloned,
				Similarity: score,
				Strategy:   "memory_scan",
			}
		}
	}
	s.entries = filtered
	return best, best != nil
}

func (s *MemorySemanticStore) Set(ctx context.Context, entry SemanticEntry) error {
	if entry.Response == nil || strings.TrimSpace(entry.Namespace) == "" || strings.TrimSpace(entry.Text) == "" {
		return nil
	}
	vector, err := s.embedder.Embed(ctx, entry.Text)
	if err != nil || len(vector) == 0 {
		return err
	}

	if entry.ExpiresAt.IsZero() && s.defaultTTL > 0 {
		entry.ExpiresAt = time.Now().Add(s.defaultTTL)
	}
	entry.Response = cloneChatResponse(entry.Response)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries = append(s.entries, semanticRecord{
		entry:    entry,
		vector:   append([]float64(nil), vector...),
		storedAt: time.Now().UTC(),
	})
	if len(s.entries) > s.maxEntries {
		s.entries = append([]semanticRecord(nil), s.entries[len(s.entries)-s.maxEntries:]...)
	}
	return nil
}

func (s *MemorySemanticStore) Stats(_ context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now()
	count := 0
	for _, rec := range s.entries {
		if rec.entry.ExpiresAt.IsZero() || rec.entry.ExpiresAt.After(now) {
			count++
		}
	}
	return count, nil
}

func (s *MemorySemanticStore) List(_ context.Context, limit, offset int) ([]SemanticEntryMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now()
	// Collect active entries newest-first (entries slice is append-only so
	// iterating in reverse gives insertion-newest order).
	active := make([]semanticRecord, 0, len(s.entries))
	for i := len(s.entries) - 1; i >= 0; i-- {
		rec := s.entries[i]
		if rec.entry.ExpiresAt.IsZero() || rec.entry.ExpiresAt.After(now) {
			active = append(active, rec)
		}
	}
	if offset >= len(active) {
		return nil, nil
	}
	end := offset + limit
	if end > len(active) {
		end = len(active)
	}
	out := make([]SemanticEntryMeta, 0, end-offset)
	for _, rec := range active[offset:end] {
		snippet := rec.entry.Text
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		out = append(out, SemanticEntryMeta{
			Namespace:   rec.entry.Namespace,
			TextSnippet: snippet,
			ExpiresAt:   rec.entry.ExpiresAt,
			StoredAt:    rec.storedAt,
		})
	}
	return out, nil
}

func (s *MemorySemanticStore) Prune(_ context.Context, maxAge time.Duration, maxCount int) (int, error) {
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	deleted := 0
	kept := s.entries[:0]
	for _, record := range s.entries {
		if (!record.entry.ExpiresAt.IsZero() && now.After(record.entry.ExpiresAt)) || (maxAge > 0 && !record.storedAt.IsZero() && record.storedAt.Before(now.Add(-maxAge))) {
			deleted++
			continue
		}
		kept = append(kept, record)
	}
	s.entries = kept

	if maxCount > 0 && len(s.entries) > maxCount {
		deleted += len(s.entries) - maxCount
		s.entries = append([]semanticRecord(nil), s.entries[len(s.entries)-maxCount:]...)
	}

	return deleted, nil
}

func BuildSemanticNamespace(req types.ChatRequest, decision types.RouteDecision) string {
	tenant := requestscope.EffectiveTenant(requestscope.Normalize(req.Scope), "anonymous")
	parts := []string{
		"tenant:" + tenant,
		"provider:" + decision.Provider,
		"model:" + models.Canonicalize(decision.Model),
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

func BuildSemanticText(req types.ChatRequest, maxChars int) string {
	var lines []string
	for _, msg := range req.Messages {
		role := strings.TrimSpace(strings.ToLower(msg.Role))
		if role == "" {
			role = "message"
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		lines = append(lines, role+": "+content)
	}
	text := strings.Join(lines, "\n")
	if maxChars > 0 && len(text) > maxChars {
		text = text[:maxChars]
	}
	return text
}

var semanticUnsafePattern = regexp.MustCompile(`\b(today|latest|current|recent|news|price|stock|score|weather)\b`)

func EligibleForSemanticCache(req types.ChatRequest, maxChars int) bool {
	text := strings.TrimSpace(BuildSemanticText(req, maxChars))
	if text == "" {
		return false
	}
	if semanticUnsafePattern.MatchString(strings.ToLower(text)) {
		return false
	}
	return true
}

func cloneChatResponse(resp *types.ChatResponse) *types.ChatResponse {
	if resp == nil {
		return nil
	}
	cloned := *resp
	cloned.Choices = append([]types.ChatChoice(nil), resp.Choices...)
	return &cloned
}
