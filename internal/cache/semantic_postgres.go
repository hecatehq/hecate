package cache

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/storage"
	"github.com/hecate/agent-runtime/pkg/types"
)

type PostgresSemanticStore struct {
	db                 *sql.DB
	table              string
	defaultTTL         time.Duration
	maxEntries         int
	embedder           Embedder
	vectorMode         string
	vectorCandidates   int
	pgvectorEnabled    bool
	indexMode          string
	indexType          string
	hnswM              int
	hnswEfConstruction int
	ivfflatLists       int
	searchEf           int
	searchProbes       int
}

type PostgresSemanticOptions struct {
	VectorMode         string
	VectorCandidates   int
	IndexMode          string
	IndexType          string
	HNSWM              int
	HNSWEfConstruction int
	IVFFlatLists       int
	SearchEf           int
	SearchProbes       int
}

func NewPostgresSemanticStore(ctx context.Context, client *storage.PostgresClient, defaultTTL time.Duration, maxEntries int, embedder Embedder, options PostgresSemanticOptions) (*PostgresSemanticStore, error) {
	if client == nil || client.DB() == nil {
		return nil, fmt.Errorf("postgres client is required")
	}
	if maxEntries <= 0 {
		maxEntries = 10_000
	}
	if embedder == nil {
		embedder = LocalSimpleEmbedder{}
	}
	vectorMode := strings.ToLower(strings.TrimSpace(options.VectorMode))
	if vectorMode == "" {
		vectorMode = "auto"
	}
	vectorCandidates := options.VectorCandidates
	if vectorCandidates <= 0 {
		vectorCandidates = min(maxEntries, 200)
	}
	indexMode := normalizeANNMode(options.IndexMode, "auto")
	indexType := normalizeANNIndexType(options.IndexType, "hnsw")
	hnswM := max(options.HNSWM, 4)
	if options.HNSWM == 0 {
		hnswM = 16
	}
	hnswEfConstruction := max(options.HNSWEfConstruction, 8)
	if options.HNSWEfConstruction == 0 {
		hnswEfConstruction = 64
	}
	ivfflatLists := max(options.IVFFlatLists, 1)
	if options.IVFFlatLists == 0 {
		ivfflatLists = 100
	}
	searchEf := max(options.SearchEf, 1)
	if options.SearchEf == 0 {
		searchEf = 80
	}
	searchProbes := max(options.SearchProbes, 1)
	if options.SearchProbes == 0 {
		searchProbes = 10
	}

	store := &PostgresSemanticStore{
		db:                 client.DB(),
		table:              client.QualifiedTable("cache_semantic"),
		defaultTTL:         defaultTTL,
		maxEntries:         maxEntries,
		embedder:           embedder,
		vectorMode:         vectorMode,
		vectorCandidates:   vectorCandidates,
		indexMode:          indexMode,
		indexType:          indexType,
		hnswM:              hnswM,
		hnswEfConstruction: hnswEfConstruction,
		ivfflatLists:       ivfflatLists,
		searchEf:           searchEf,
		searchProbes:       searchProbes,
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *PostgresSemanticStore) Search(ctx context.Context, query SemanticQuery) (*SemanticMatch, bool) {
	text := query.Text
	if query.MaxTextChars > 0 && len(text) > query.MaxTextChars {
		text = text[:query.MaxTextChars]
	}
	queryVector, err := s.embedder.Embed(ctx, text)
	if err != nil || len(queryVector) == 0 {
		return nil, false
	}

	_, _ = s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE expires_at <= NOW()`, s.table))

	if s.pgvectorEnabled {
		match, ok := s.searchWithPGVector(ctx, query, queryVector)
		if ok {
			return match, true
		}
	}

	rows, err := s.db.QueryContext(ctx,
		fmt.Sprintf(`
			SELECT response, embedding
			FROM %s
			WHERE namespace = $1 AND expires_at > NOW()
			ORDER BY created_at DESC
			LIMIT $2
		`, s.table),
		query.Namespace,
		s.maxEntries,
	)
	if err != nil {
		return nil, false
	}
	defer rows.Close()

	var best *SemanticMatch
	for rows.Next() {
		var responsePayload []byte
		var vectorPayload []byte
		if err := rows.Scan(&responsePayload, &vectorPayload); err != nil {
			continue
		}

		var candidateVector []float64
		if err := json.Unmarshal(vectorPayload, &candidateVector); err != nil {
			continue
		}
		score := cosineSimilarity(queryVector, candidateVector)
		if score < query.MinSimilarity {
			continue
		}

		var response types.ChatResponse
		if err := json.Unmarshal(responsePayload, &response); err != nil {
			continue
		}
		if best == nil || score > best.Similarity {
			best = &SemanticMatch{
				Response:   cloneChatResponse(&response),
				Similarity: score,
				Strategy:   "postgres_json_scan",
			}
		}
	}
	if best == nil {
		return nil, false
	}
	return best, true
}

func (s *PostgresSemanticStore) Set(ctx context.Context, entry SemanticEntry) error {
	if entry.Response == nil || strings.TrimSpace(entry.Namespace) == "" || strings.TrimSpace(entry.Text) == "" {
		return nil
	}

	vector, err := s.embedder.Embed(ctx, entry.Text)
	if err != nil || len(vector) == 0 {
		return err
	}

	if entry.ExpiresAt.IsZero() {
		ttl := s.defaultTTL
		if ttl <= 0 {
			ttl = 24 * time.Hour
		}
		entry.ExpiresAt = time.Now().Add(ttl)
	}
	entry.Response = cloneChatResponse(entry.Response)

	responsePayload, err := json.Marshal(entry.Response)
	if err != nil {
		return fmt.Errorf("marshal semantic response: %w", err)
	}
	vectorPayload, err := json.Marshal(vector)
	if err != nil {
		return fmt.Errorf("marshal semantic embedding: %w", err)
	}

	vectorLiteral := formatPGVector(vector)
	if s.pgvectorEnabled {
		_, err = s.db.ExecContext(ctx,
			fmt.Sprintf(`
				INSERT INTO %s (namespace, text_content, response, embedding, embedding_vector, expires_at, created_at)
				VALUES ($1, $2, $3::jsonb, $4::jsonb, $5::vector, $6, NOW())
			`, s.table),
			entry.Namespace,
			entry.Text,
			responsePayload,
			vectorPayload,
			vectorLiteral,
			entry.ExpiresAt.UTC(),
		)
	} else {
		_, err = s.db.ExecContext(ctx,
			fmt.Sprintf(`
			INSERT INTO %s (namespace, text_content, response, embedding, expires_at, created_at)
			VALUES ($1, $2, $3::jsonb, $4::jsonb, $5, NOW())
		`, s.table),
			entry.Namespace,
			entry.Text,
			responsePayload,
			vectorPayload,
			entry.ExpiresAt.UTC(),
		)
	}
	if err != nil {
		return fmt.Errorf("write postgres semantic cache: %w", err)
	}

	_, _ = s.db.ExecContext(ctx, fmt.Sprintf(`
		DELETE FROM %s
		WHERE id IN (
			SELECT id
			FROM %s
			ORDER BY created_at DESC
			OFFSET $1
		)
	`, s.table, s.table), s.maxEntries)

	return nil
}

func (s *PostgresSemanticStore) Stats(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE expires_at > NOW()`, s.table),
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("postgres semantic cache stats: %w", err)
	}
	return count, nil
}

func (s *PostgresSemanticStore) List(ctx context.Context, limit, offset int) ([]SemanticEntryMeta, error) {
	rows, err := s.db.QueryContext(ctx,
		fmt.Sprintf(`
			SELECT namespace, text_content, expires_at, created_at
			FROM %s
			WHERE expires_at > NOW()
			ORDER BY created_at DESC
			LIMIT $1 OFFSET $2
		`, s.table),
		limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres semantic cache list: %w", err)
	}
	defer rows.Close()

	var out []SemanticEntryMeta
	for rows.Next() {
		var m SemanticEntryMeta
		var snippet string
		if err := rows.Scan(&m.Namespace, &snippet, &m.ExpiresAt, &m.StoredAt); err != nil {
			return nil, fmt.Errorf("postgres semantic cache list scan: %w", err)
		}
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		m.TextSnippet = snippet
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *PostgresSemanticStore) Prune(ctx context.Context, maxAge time.Duration, maxCount int) (int, error) {
	deleted := int64(0)

	result, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE expires_at <= NOW()`, s.table))
	if err != nil {
		return 0, fmt.Errorf("delete expired postgres semantic cache rows: %w", err)
	}
	count, _ := result.RowsAffected()
	deleted += count

	if maxAge > 0 {
		result, err = s.db.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE created_at < $1`, s.table),
			time.Now().Add(-maxAge).UTC(),
		)
		if err != nil {
			return 0, fmt.Errorf("delete aged postgres semantic cache rows: %w", err)
		}
		count, _ = result.RowsAffected()
		deleted += count
	}

	if maxCount > 0 {
		result, err = s.db.ExecContext(ctx, fmt.Sprintf(`
			DELETE FROM %s
			WHERE id IN (
				SELECT id
				FROM %s
				ORDER BY created_at DESC
				OFFSET $1
			)
		`, s.table, s.table), maxCount)
		if err != nil {
			return 0, fmt.Errorf("enforce postgres semantic cache max count: %w", err)
		}
		count, _ = result.RowsAffected()
		deleted += count
	}

	return int(deleted), nil
}

func (s *PostgresSemanticStore) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id BIGSERIAL PRIMARY KEY,
			namespace TEXT NOT NULL,
			text_content TEXT NOT NULL,
			response JSONB NOT NULL,
			embedding JSONB NOT NULL,
			expires_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`, s.table))
	if err != nil {
		return fmt.Errorf("migrate postgres semantic cache: %w", err)
	}
	if err := s.enablePGVector(ctx); err != nil {
		return err
	}
	return nil
}

func (s *PostgresSemanticStore) enablePGVector(ctx context.Context) error {
	switch s.vectorMode {
	case "off", "disabled", "false":
		return nil
	case "required", "on", "enabled", "true", "auto":
	default:
		s.vectorMode = "auto"
	}

	_, err := s.db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`)
	if err != nil {
		if s.vectorMode == "required" || s.vectorMode == "on" || s.vectorMode == "enabled" || s.vectorMode == "true" {
			return fmt.Errorf("enable pgvector extension: %w", err)
		}
		return nil
	}

	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`
		ALTER TABLE %s
		ADD COLUMN IF NOT EXISTS embedding_vector vector
	`, s.table))
	if err != nil {
		if s.vectorMode == "required" || s.vectorMode == "on" || s.vectorMode == "enabled" || s.vectorMode == "true" {
			return fmt.Errorf("add pgvector column: %w", err)
		}
		return nil
	}

	s.pgvectorEnabled = true
	if err := s.ensureANNIndex(ctx); err != nil {
		return err
	}
	return nil
}

func (s *PostgresSemanticStore) searchWithPGVector(ctx context.Context, query SemanticQuery, queryVector []float64) (*SemanticMatch, bool) {
	if tunedCtx, done, ok := s.configureVectorSearch(ctx); ok {
		defer done()
		ctx = tunedCtx
	}
	vectorLiteral := formatPGVector(queryVector)
	rows, err := semanticQueryable(ctx, s.db).QueryContext(ctx,
		fmt.Sprintf(`
			SELECT response, 1 - (embedding_vector <=> $2::vector) AS similarity
			FROM %s
			WHERE namespace = $1
			  AND expires_at > NOW()
			  AND embedding_vector IS NOT NULL
			  AND 1 - (embedding_vector <=> $2::vector) >= $3
			ORDER BY embedding_vector <=> $2::vector, created_at DESC
			LIMIT $4
		`, s.table),
		query.Namespace,
		vectorLiteral,
		query.MinSimilarity,
		s.vectorCandidates,
	)
	if err != nil {
		return nil, false
	}
	defer rows.Close()

	var best *SemanticMatch
	for rows.Next() {
		var responsePayload []byte
		var similarity float64
		if err := rows.Scan(&responsePayload, &similarity); err != nil {
			continue
		}
		var response types.ChatResponse
		if err := json.Unmarshal(responsePayload, &response); err != nil {
			continue
		}
		if best == nil || similarity > best.Similarity {
			best = &SemanticMatch{
				Response:   cloneChatResponse(&response),
				Similarity: similarity,
				Strategy:   "postgres_pgvector",
				IndexType:  s.indexType,
			}
		}
	}
	if best == nil {
		return nil, false
	}
	return best, true
}

func (s *PostgresSemanticStore) ensureANNIndex(ctx context.Context) error {
	switch s.indexMode {
	case "off", "disabled", "false":
		return nil
	case "required", "on", "enabled", "true", "auto":
	default:
		s.indexMode = "auto"
	}

	if err := s.createANNIndex(ctx, s.indexType); err != nil {
		if s.indexMode == "required" || s.indexMode == "on" || s.indexMode == "enabled" || s.indexMode == "true" {
			return err
		}
		if s.indexType != "ivfflat" {
			if fallbackErr := s.createANNIndex(ctx, "ivfflat"); fallbackErr == nil {
				s.indexType = "ivfflat"
				return nil
			}
		}
		return nil
	}
	return nil
}

func (s *PostgresSemanticStore) createANNIndex(ctx context.Context, indexType string) error {
	indexName := semanticANNIndexName(indexType)
	switch indexType {
	case "hnsw":
		_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
			CREATE INDEX IF NOT EXISTS %s
			ON %s
			USING hnsw (embedding_vector vector_cosine_ops)
			WITH (m = %d, ef_construction = %d)
		`, indexName, s.table, s.hnswM, s.hnswEfConstruction))
		if err != nil {
			return fmt.Errorf("create hnsw index: %w", err)
		}
		return nil
	case "ivfflat":
		_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
			CREATE INDEX IF NOT EXISTS %s
			ON %s
			USING ivfflat (embedding_vector vector_cosine_ops)
			WITH (lists = %d)
		`, indexName, s.table, s.ivfflatLists))
		if err != nil {
			return fmt.Errorf("create ivfflat index: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported vector index type %q", indexType)
	}
}

func (s *PostgresSemanticStore) configureVectorSearch(ctx context.Context) (context.Context, func(), bool) {
	switch s.indexType {
	case "hnsw":
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return ctx, func() {}, false
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`SET LOCAL hnsw.ef_search = %d`, s.searchEf)); err != nil {
			_ = tx.Rollback()
			return ctx, func() {}, false
		}
		return withSemanticTx(ctx, tx), func() { _ = tx.Commit() }, true
	case "ivfflat":
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return ctx, func() {}, false
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`SET LOCAL ivfflat.probes = %d`, s.searchProbes)); err != nil {
			_ = tx.Rollback()
			return ctx, func() {}, false
		}
		return withSemanticTx(ctx, tx), func() { _ = tx.Commit() }, true
	default:
		return ctx, func() {}, false
	}
}

type semanticTxKey struct{}

type queryable interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func withSemanticTx(ctx context.Context, tx *sql.Tx) context.Context {
	return context.WithValue(ctx, semanticTxKey{}, tx)
}

func semanticQueryable(ctx context.Context, db *sql.DB) queryable {
	if tx, ok := ctx.Value(semanticTxKey{}).(*sql.Tx); ok && tx != nil {
		return tx
	}
	return db
}

func normalizeANNMode(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "auto", "required", "off", "disabled", "false", "on", "enabled", "true":
		return value
	case "":
		return fallback
	default:
		return fallback
	}
}

func normalizeANNIndexType(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "hnsw", "ivfflat":
		return value
	case "":
		return fallback
	default:
		return fallback
	}
}

func semanticANNIndexName(indexType string) string {
	return "hecate_cache_semantic_" + normalizeANNIndexType(indexType, "hnsw") + "_idx"
}

func formatPGVector(vector []float64) string {
	if len(vector) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, value := range vector {
		if i > 0 {
			b.WriteByte(',')
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			value = 0
		}
		b.WriteString(strconv.FormatFloat(value, 'f', -1, 64))
	}
	b.WriteByte(']')
	return b.String()
}
