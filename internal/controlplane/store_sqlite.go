package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/storage"
)

// SQLiteStore mirrors the memory Store-interface surface while keeping
// a single-row-per-key JSON payload shape for simple local persistence.
//
// SQLite-specific choices that aren't accidental:
//   - state column is TEXT (SQLite has no JSONB; we marshal/unmarshal in
//     Go regardless, so the on-disk type is moot).
//   - placeholders are `?` rather than `$N`.
//   - the upsert uses ON CONFLICT (store_key) DO UPDATE.
//   - updated_at is stored as TEXT in RFC3339; SQLite has no native
//     timestamptz type and the column is informational (we never read it
//     back), so a plain ISO string is enough.
type SQLiteStore struct {
	db    *sql.DB
	table string
	key   string
	mu    sync.Mutex
}

func NewSQLiteStore(ctx context.Context, client *storage.SQLiteClient, key string) (*SQLiteStore, error) {
	if client == nil || client.DB() == nil {
		return nil, fmt.Errorf("sqlite client is required")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = "control-plane"
	}

	store := &SQLiteStore{
		db:    client.DB(),
		table: client.QualifiedTable("control_plane"),
		key:   key,
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	if _, err := store.readState(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Backend() string {
	return "sqlite"
}

func (s *SQLiteStore) Snapshot(ctx context.Context) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readState(ctx)
}

func (s *SQLiteStore) UpsertProvider(ctx context.Context, provider Provider, secret *ProviderSecret) (Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState(ctx)
	if err != nil {
		return Provider{}, err
	}
	provider, err = applyProviderUpsert(ctx, &state, provider, secret)
	if err != nil {
		return Provider{}, err
	}
	if err := s.writeState(ctx, state); err != nil {
		return Provider{}, err
	}
	return provider, nil
}

func (s *SQLiteStore) RotateProviderSecret(ctx context.Context, id string, secret ProviderSecret) (Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState(ctx)
	if err != nil {
		return Provider{}, err
	}
	provider, err := applyRotateProviderSecret(ctx, &state, id, secret)
	if err != nil {
		return Provider{}, err
	}
	if err := s.writeState(ctx, state); err != nil {
		return Provider{}, err
	}
	return provider, nil
}

func (s *SQLiteStore) DeleteProviderCredential(ctx context.Context, id string) (Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState(ctx)
	if err != nil {
		return Provider{}, err
	}
	provider, err := applyDeleteProviderCredential(ctx, &state, id)
	if err != nil {
		return Provider{}, err
	}
	if err := s.writeState(ctx, state); err != nil {
		return Provider{}, err
	}
	return provider, nil
}

func (s *SQLiteStore) DeleteProvider(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState(ctx)
	if err != nil {
		return err
	}
	if err := applyDeleteProvider(ctx, &state, id); err != nil {
		return err
	}
	return s.writeState(ctx, state)
}

func (s *SQLiteStore) UpsertPolicyRule(ctx context.Context, rule config.PolicyRuleConfig) (config.PolicyRuleConfig, error) {
	rule, err := normalizePolicyRule(rule)
	if err != nil {
		return config.PolicyRuleConfig{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState(ctx)
	if err != nil {
		return config.PolicyRuleConfig{}, err
	}
	action := upsertPolicyRule(&state, rule)
	appendAuditEvent(&state, newAuditEvent(ctx, action, "policy_rule", rule.ID, rule.Action))
	if err := s.writeState(ctx, state); err != nil {
		return config.PolicyRuleConfig{}, err
	}
	return rule, nil
}

func (s *SQLiteStore) DeletePolicyRule(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("policy rule id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState(ctx)
	if err != nil {
		return err
	}
	index := policyRuleIndex(state.PolicyRules, id)
	if index < 0 {
		return fmt.Errorf("policy rule %q not found", id)
	}
	appendAuditEvent(&state, newAuditEvent(ctx, "policy_rule.deleted", "policy_rule", state.PolicyRules[index].ID, state.PolicyRules[index].Action))
	state.PolicyRules = append(state.PolicyRules[:index], state.PolicyRules[index+1:]...)
	return s.writeState(ctx, state)
}

func (s *SQLiteStore) UpsertPricebookEntry(ctx context.Context, entry config.ModelPriceConfig) (config.ModelPriceConfig, error) {
	entry, err := normalizePricebookEntry(entry)
	if err != nil {
		return config.ModelPriceConfig{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState(ctx)
	if err != nil {
		return config.ModelPriceConfig{}, err
	}
	action := upsertPricebookEntry(&state, entry)
	appendAuditEvent(&state, newAuditEvent(ctx, action, "pricebook_entry", pricebookEntryID(entry.Provider, entry.Model), ""))
	if err := s.writeState(ctx, state); err != nil {
		return config.ModelPriceConfig{}, err
	}
	return entry, nil
}

func (s *SQLiteStore) DeletePricebookEntry(ctx context.Context, provider, model string) error {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return fmt.Errorf("pricebook provider and model are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState(ctx)
	if err != nil {
		return err
	}
	index := pricebookEntryIndex(state.Pricebook, provider, model)
	if index < 0 {
		return fmt.Errorf("pricebook entry %q not found", pricebookEntryID(provider, model))
	}
	appendAuditEvent(&state, newAuditEvent(ctx, "pricebook_entry.deleted", "pricebook_entry", pricebookEntryID(provider, model), ""))
	state.Pricebook = append(state.Pricebook[:index], state.Pricebook[index+1:]...)
	return s.writeState(ctx, state)
}

func (s *SQLiteStore) PruneAuditEvents(ctx context.Context, maxAge time.Duration, maxCount int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState(ctx)
	if err != nil {
		return 0, err
	}
	deleted := pruneAuditEvents(&state, maxAge, maxCount)
	if deleted == 0 {
		return 0, nil
	}
	if err := s.writeState(ctx, state); err != nil {
		return 0, err
	}
	return deleted, nil
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	// state is TEXT (no JSONB in SQLite). updated_at is also TEXT in RFC3339;
	// we never read it back, so the lighter-touch type is fine.
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			store_key TEXT PRIMARY KEY,
			state TEXT NOT NULL,
			updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`, s.table))
	if err != nil {
		return fmt.Errorf("migrate sqlite control plane store: %w", err)
	}
	return nil
}

func (s *SQLiteStore) readState(ctx context.Context) (State, error) {
	var raw sql.NullString
	err := s.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT state FROM %s WHERE store_key = ?`, s.table),
		s.key,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read control plane sqlite state: %w", err)
	}
	if !raw.Valid || raw.String == "" {
		return State{}, nil
	}

	var state State
	if err := json.Unmarshal([]byte(raw.String), &state); err != nil {
		return State{}, fmt.Errorf("decode control plane sqlite state: %w", err)
	}
	return cloneState(state), nil
}

func (s *SQLiteStore) writeState(ctx context.Context, state State) error {
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode control plane sqlite state: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		fmt.Sprintf(`
			INSERT INTO %s (store_key, state, updated_at)
			VALUES (?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT (store_key)
			DO UPDATE SET state = excluded.state, updated_at = CURRENT_TIMESTAMP
		`, s.table),
		s.key,
		string(payload),
	)
	if err != nil {
		return fmt.Errorf("write control plane sqlite state: %w", err)
	}
	return nil
}
