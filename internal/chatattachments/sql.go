package chatattachments

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
)

type SQLStore struct {
	client                   storage.SQLClient
	backend                  string
	table                    string
	maxStoredBytesPerSession int64
	maxStoredBytesTotal      int64
}

func NewSQLiteStore(ctx context.Context, client *storage.SQLiteClient) (*SQLStore, error) {
	return newSQLStore(ctx, client)
}

func NewPostgresStore(ctx context.Context, client *storage.PostgresClient) (*SQLStore, error) {
	return newSQLStore(ctx, client)
}

func newSQLStore(ctx context.Context, client storage.SQLClient) (*SQLStore, error) {
	if client == nil || client.DB() == nil {
		return nil, errors.New("sql client is required")
	}
	store := &SQLStore{
		client:                   client,
		backend:                  client.Backend(),
		table:                    client.QualifiedTable("chat_attachments"),
		maxStoredBytesPerSession: MaxStoredBytesPerSession,
		maxStoredBytesTotal:      MaxDurableStoredBytesTotal,
	}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLStore) Backend() string { return s.backend }

func (s *SQLStore) setMaxStoredBytesPerSession(limit int64) {
	s.maxStoredBytesPerSession = limit
}

func (s *SQLStore) setMaxStoredBytesTotal(limit int64) {
	s.maxStoredBytesTotal = limit
}

func (s *SQLStore) Create(ctx context.Context, attachment StoredAttachment) (StoredAttachment, error) {
	if attachment.SizeBytes != int64(len(attachment.Data)) {
		return StoredAttachment{}, ErrInvalidMetadata
	}
	tx, err := s.beginCreateTx(ctx, attachment.SessionID)
	if err != nil {
		return StoredAttachment{}, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	if attachment.CreatedAt.IsZero() {
		attachment.CreatedAt = now
	} else {
		attachment.CreatedAt = attachment.CreatedAt.UTC()
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+s.table+`
WHERE lifecycle_state = ? AND created_at < ?`,
		lifecycleDraft,
		now.Add(-DraftTTL),
	); err != nil {
		return StoredAttachment{}, fmt.Errorf("reclaim stale %s chat attachment drafts: %w", s.backend, err)
	}
	var draftCount int
	var draftBytes int64
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*), COALESCE(SUM(size_bytes), 0)
FROM `+s.table+`
WHERE session_id = ? AND lifecycle_state <> ?`, attachment.SessionID, lifecycleLinked).Scan(&draftCount, &draftBytes); err != nil {
		return StoredAttachment{}, fmt.Errorf("read %s chat attachment draft usage: %w", s.backend, err)
	}
	if draftCount >= MaxDraftAttachmentsPerSession || attachment.SizeBytes > MaxDraftBytesPerSession-draftBytes {
		return StoredAttachment{}, ErrDraftQuota
	}
	var storedBytes int64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(SUM(size_bytes), 0)
FROM `+s.table+`
WHERE session_id = ?`, attachment.SessionID).Scan(&storedBytes); err != nil {
		return StoredAttachment{}, fmt.Errorf("read %s chat attachment stored usage: %w", s.backend, err)
	}
	if attachment.SizeBytes > s.maxStoredBytesPerSession-storedBytes {
		return StoredAttachment{}, ErrSessionQuota
	}
	var totalStoredBytes int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(size_bytes), 0) FROM `+s.table).Scan(&totalStoredBytes); err != nil {
		return StoredAttachment{}, fmt.Errorf("read %s total chat attachment stored usage: %w", s.backend, err)
	}
	if attachment.SizeBytes > s.maxStoredBytesTotal-totalStoredBytes {
		return StoredAttachment{}, TotalQuotaError{LimitBytes: s.maxStoredBytesTotal}
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO `+s.table+` (
    session_id, id, filename, media_type, size_bytes, sha256, created_at, data, lifecycle_state
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id, id) DO NOTHING`,
		attachment.SessionID,
		attachment.ID,
		attachment.Filename,
		attachment.MediaType,
		attachment.SizeBytes,
		attachment.SHA256,
		attachment.CreatedAt,
		attachment.Data,
		lifecycleDraft,
	)
	if err != nil {
		return StoredAttachment{}, fmt.Errorf("create %s chat attachment: %w", s.backend, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return StoredAttachment{}, fmt.Errorf("inspect %s chat attachment create: %w", s.backend, err)
	}
	if rows == 0 {
		return StoredAttachment{}, ErrAlreadyExists
	}
	if err := tx.Commit(); err != nil {
		return StoredAttachment{}, fmt.Errorf("commit %s chat attachment create: %w", s.backend, err)
	}
	return cloneStoredAttachment(attachment), nil
}

func (s *SQLStore) Get(ctx context.Context, sessionID, id string) (StoredAttachment, bool, error) {
	var attachment StoredAttachment
	err := s.client.DB().QueryRowContext(ctx, `
SELECT session_id, id, filename, media_type, size_bytes, sha256, created_at, data
FROM `+s.table+`
WHERE session_id = ? AND id = ?`, sessionID, id).Scan(
		&attachment.SessionID,
		&attachment.ID,
		&attachment.Filename,
		&attachment.MediaType,
		&attachment.SizeBytes,
		&attachment.SHA256,
		&attachment.CreatedAt,
		&attachment.Data,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredAttachment{}, false, nil
	}
	if err != nil {
		return StoredAttachment{}, false, fmt.Errorf("read %s chat attachment: %w", s.backend, err)
	}
	attachment.CreatedAt = attachment.CreatedAt.UTC()
	return attachment, true, nil
}

func (s *SQLStore) List(ctx context.Context, sessionID string) ([]Attachment, error) {
	rows, err := s.client.DB().QueryContext(ctx, `
SELECT session_id, id, filename, media_type, size_bytes, sha256, created_at
FROM `+s.table+`
WHERE session_id = ?
ORDER BY created_at ASC, id ASC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list %s chat attachments: %w", s.backend, err)
	}
	defer rows.Close()

	var items []Attachment
	for rows.Next() {
		var attachment Attachment
		if err := rows.Scan(
			&attachment.SessionID,
			&attachment.ID,
			&attachment.Filename,
			&attachment.MediaType,
			&attachment.SizeBytes,
			&attachment.SHA256,
			&attachment.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan %s chat attachment: %w", s.backend, err)
		}
		attachment.CreatedAt = attachment.CreatedAt.UTC()
		items = append(items, attachment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s chat attachments: %w", s.backend, err)
	}
	return items, nil
}

func (s *SQLStore) Claim(ctx context.Context, ref ClaimRef) ([]StoredAttachment, error) {
	tx, err := s.beginSessionTx(ctx, ref.SessionID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	claimed := make([]StoredAttachment, 0, len(ref.AttachmentIDs))
	states := make(map[string]lifecycleState, len(ref.AttachmentIDs))
	for _, id := range ref.AttachmentIDs {
		attachment, state, messageID, err := s.getInTx(ctx, tx, ref.SessionID, id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if state != lifecycleDraft && !(state == lifecycleClaimed && messageID == ref.MessageID) {
			return nil, ErrNotDraft
		}
		states[id] = state
		claimed = append(claimed, attachment)
	}
	now := time.Now().UTC()
	for _, id := range ref.AttachmentIDs {
		if states[id] == lifecycleClaimed {
			continue
		}
		result, err := tx.ExecContext(ctx, `UPDATE `+s.table+`
SET lifecycle_state = ?, claimed_message_id = ?, claimed_at = ?
WHERE session_id = ? AND id = ? AND lifecycle_state = ?`, lifecycleClaimed, ref.MessageID, now, ref.SessionID, id, lifecycleDraft)
		if err != nil {
			return nil, fmt.Errorf("claim %s chat attachment: %w", s.backend, err)
		}
		if err := requireOneRow(result, ErrNotDraft); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit %s chat attachment claim: %w", s.backend, err)
	}
	return claimed, nil
}

func (s *SQLStore) DeleteDraft(ctx context.Context, sessionID, id string) error {
	tx, err := s.beginSessionTx(ctx, sessionID)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var state lifecycleState
	if err := tx.QueryRowContext(ctx, `SELECT lifecycle_state FROM `+s.table+`
WHERE session_id = ? AND id = ?`, sessionID, id).Scan(&state); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("read %s chat attachment lifecycle: %w", s.backend, err)
	}
	if state != lifecycleDraft {
		return ErrNotDraft
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM `+s.table+`
WHERE session_id = ? AND id = ? AND lifecycle_state = ?`, sessionID, id, lifecycleDraft)
	if err != nil {
		return fmt.Errorf("delete %s chat attachment draft: %w", s.backend, err)
	}
	if err := requireOneRow(result, ErrNotDraft); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s chat attachment draft delete: %w", s.backend, err)
	}
	return nil
}

func (s *SQLStore) DeleteBySessionID(ctx context.Context, sessionID string) error {
	tx, err := s.beginSessionTx(ctx, sessionID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+s.table+` WHERE session_id = ?`, sessionID); err != nil {
		return fmt.Errorf("delete %s chat session attachments: %w", s.backend, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s chat session attachment delete: %w", s.backend, err)
	}
	return nil
}

func (s *SQLStore) ResolveClaim(ctx context.Context, ref ClaimRef, resolution ClaimResolution) error {
	tx, err := s.beginSessionTx(ctx, ref.SessionID)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	states := make(map[string]lifecycleState, len(ref.AttachmentIDs))
	for _, id := range ref.AttachmentIDs {
		var state lifecycleState
		var messageID string
		if err := tx.QueryRowContext(ctx, `SELECT lifecycle_state, claimed_message_id FROM `+s.table+`
WHERE session_id = ? AND id = ?`, ref.SessionID, id).Scan(&state, &messageID); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return fmt.Errorf("read %s chat attachment lifecycle: %w", s.backend, err)
		}
		if messageID != ref.MessageID {
			return ErrClaimLost
		}
		switch resolution {
		case ClaimLinked:
			if state != lifecycleClaimed && state != lifecycleLinked {
				return ErrClaimLost
			}
		case ClaimReleased:
			if state != lifecycleClaimed && state != lifecycleDraft {
				return ErrClaimLost
			}
		default:
			return ErrClaimLost
		}
		states[id] = state
	}
	for _, id := range ref.AttachmentIDs {
		if states[id] != lifecycleClaimed {
			continue
		}
		to := lifecycleDraft
		if resolution == ClaimLinked {
			to = lifecycleLinked
		}
		result, err := tx.ExecContext(ctx, `UPDATE `+s.table+`
SET lifecycle_state = ?
WHERE session_id = ? AND id = ? AND lifecycle_state = ? AND claimed_message_id = ?`,
			to, ref.SessionID, id, lifecycleClaimed, ref.MessageID)
		if err != nil {
			return fmt.Errorf("transition %s chat attachment lifecycle: %w", s.backend, err)
		}
		if err := requireOneRow(result, ErrClaimLost); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s chat attachment lifecycle transition: %w", s.backend, err)
	}
	return nil
}

func (s *SQLStore) ListPendingClaims(ctx context.Context) ([]PendingClaim, error) {
	rows, err := s.client.DB().QueryContext(ctx, `
SELECT session_id, id, filename, media_type, size_bytes, sha256, created_at, claimed_message_id, claimed_at
FROM `+s.table+`
WHERE lifecycle_state = ?
ORDER BY session_id ASC, claimed_message_id ASC, created_at ASC, id ASC`, lifecycleClaimed)
	if err != nil {
		return nil, fmt.Errorf("list %s pending chat attachment claims: %w", s.backend, err)
	}
	defer rows.Close()

	var items []PendingClaim
	for rows.Next() {
		var attachment Attachment
		var messageID string
		var claimedAt sql.NullTime
		if err := rows.Scan(
			&attachment.SessionID,
			&attachment.ID,
			&attachment.Filename,
			&attachment.MediaType,
			&attachment.SizeBytes,
			&attachment.SHA256,
			&attachment.CreatedAt,
			&messageID,
			&claimedAt,
		); err != nil {
			return nil, fmt.Errorf("scan %s pending chat attachment claim: %w", s.backend, err)
		}
		attachment.CreatedAt = attachment.CreatedAt.UTC()
		if len(items) == 0 || items[len(items)-1].Ref.SessionID != attachment.SessionID ||
			items[len(items)-1].Ref.MessageID != messageID {
			items = append(items, PendingClaim{Ref: ClaimRef{
				SessionID: attachment.SessionID,
				MessageID: messageID,
			}})
			if claimedAt.Valid {
				items[len(items)-1].ClaimedAt = claimedAt.Time.UTC()
			}
		}
		claim := &items[len(items)-1]
		claim.Ref.AttachmentIDs = append(claim.Ref.AttachmentIDs, attachment.ID)
		claim.Attachments = append(claim.Attachments, attachment)
		if claimedAt.Valid && (claim.ClaimedAt.IsZero() || claimedAt.Time.Before(claim.ClaimedAt)) {
			claim.ClaimedAt = claimedAt.Time.UTC()
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s pending chat attachment claims: %w", s.backend, err)
	}
	return items, nil
}

func (s *SQLStore) ListSessionIDs(ctx context.Context) ([]string, error) {
	rows, err := s.client.DB().QueryContext(ctx, `
SELECT DISTINCT session_id
FROM `+s.table+`
ORDER BY session_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list %s chat attachment session ids: %w", s.backend, err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return nil, fmt.Errorf("scan %s chat attachment session id: %w", s.backend, err)
		}
		ids = append(ids, sessionID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s chat attachment session ids: %w", s.backend, err)
	}
	return ids, nil
}

func (s *SQLStore) beginSessionTx(ctx context.Context, sessionID string) (storage.Tx, error) {
	return s.beginLockedTx(ctx, sessionID, false)
}

func (s *SQLStore) beginCreateTx(ctx context.Context, sessionID string) (storage.Tx, error) {
	return s.beginLockedTx(ctx, sessionID, true)
}

func (s *SQLStore) beginLockedTx(ctx context.Context, sessionID string, globalQuota bool) (storage.Tx, error) {
	tx, err := s.client.DB().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin %s chat attachment transaction: %w", s.backend, err)
	}
	if s.client.Dialect() == storage.DialectPostgres {
		if globalQuota {
			globalLockKey := s.table + "\x00total_quota"
			if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, globalLockKey); err != nil {
				_ = tx.Rollback()
				return nil, fmt.Errorf("lock %s total chat attachment quota: %w", s.backend, err)
			}
		}
		lockKey := s.table + "\x00" + sessionID
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, lockKey); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("lock %s chat attachment session: %w", s.backend, err)
		}
	}
	return tx, nil
}

func (s *SQLStore) getInTx(ctx context.Context, tx storage.Tx, sessionID, id string) (StoredAttachment, lifecycleState, string, error) {
	var attachment StoredAttachment
	var state lifecycleState
	var messageID string
	err := tx.QueryRowContext(ctx, `
SELECT session_id, id, filename, media_type, size_bytes, sha256, created_at, data, lifecycle_state, claimed_message_id
FROM `+s.table+`
WHERE session_id = ? AND id = ?`, sessionID, id).Scan(
		&attachment.SessionID,
		&attachment.ID,
		&attachment.Filename,
		&attachment.MediaType,
		&attachment.SizeBytes,
		&attachment.SHA256,
		&attachment.CreatedAt,
		&attachment.Data,
		&state,
		&messageID,
	)
	if err != nil {
		return StoredAttachment{}, "", "", err
	}
	attachment.CreatedAt = attachment.CreatedAt.UTC()
	return attachment, state, messageID, nil
}

func requireOneRow(result sql.Result, fallback error) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fallback
	}
	return nil
}

func (s *SQLStore) migrate(ctx context.Context) error {
	binaryColumn := storage.BinaryColumn(s.client)
	timestampColumn := storage.TimestampColumn(s.client)
	if _, err := s.client.DB().ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS `+s.table+` (
    session_id TEXT NOT NULL,
    id TEXT NOT NULL,
    filename TEXT NOT NULL,
    media_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL,
    sha256 TEXT NOT NULL,
	    created_at `+timestampColumn+` NOT NULL,
	    data `+binaryColumn+` NOT NULL,
	    lifecycle_state TEXT NOT NULL DEFAULT 'draft',
	    claimed_message_id TEXT NOT NULL DEFAULT '',
	    claimed_at `+timestampColumn+`,
	    PRIMARY KEY (session_id, id)
)`); err != nil {
		return fmt.Errorf("migrate %s chat attachments: %w", s.backend, err)
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{name: "claimed_message_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "claimed_at", definition: timestampColumn},
	} {
		exists, err := storage.ColumnExists(ctx, s.client, s.client.TableName("chat_attachments"), column.name)
		if err != nil {
			return fmt.Errorf("inspect %s chat attachment %s column: %w", s.backend, column.name, err)
		}
		if exists {
			continue
		}
		if _, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(
			`ALTER TABLE %s ADD COLUMN %s %s`, s.table, column.name, column.definition,
		)); err != nil {
			return fmt.Errorf("migrate %s chat attachment %s column: %w", s.backend, column.name, err)
		}
	}
	indexName := s.client.TableName("chat_attachments") + "_created_idx"
	if _, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS "%s" ON %s (session_id, created_at, id)`,
		indexName,
		s.table,
	)); err != nil {
		return fmt.Errorf("migrate %s chat attachments index: %w", s.backend, err)
	}
	reclaimIndexName := s.client.TableName("chat_attachments") + "_lifecycle_created_idx"
	if _, err := s.client.DB().ExecContext(ctx, fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS "%s" ON %s (lifecycle_state, created_at)`,
		reclaimIndexName,
		s.table,
	)); err != nil {
		return fmt.Errorf("migrate %s chat attachment reclaim index: %w", s.backend, err)
	}
	return nil
}
