package chat

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

const (
	messageRequestStatePending   = "pending"
	messageRequestStateCommitted = "committed"
	messageRequestStaleAfter     = MessageRequestLeaseStaleAfter
)

func (s *SQLiteStore) ClaimMessageRequest(ctx context.Context, sessionID, clientRequestID string, fingerprint MessageRequestFingerprint) (MessageRequestClaim, error) {
	if _, err := s.loadSession(ctx, sessionID); err != nil {
		return MessageRequestClaim{}, err
	}
	ownerToken, err := newMessageRequestToken()
	if err != nil {
		return MessageRequestClaim{}, err
	}
	fingerprintText := hex.EncodeToString(fingerprint[:])

	for {
		now := s.messageRequestNowUTC()
		result, err := s.client.DB().ExecContext(
			ctx,
			fmt.Sprintf(
				`INSERT INTO %s (
					session_id, client_request_id, payload_fingerprint, state,
					owner_instance, owner_token, message_id, created_at, updated_at
				 ) VALUES (?, ?, ?, ?, ?, ?, '', ?, ?)
				 ON CONFLICT (session_id, client_request_id) DO NOTHING`,
				s.messageRequestsTable,
			),
			sessionID,
			clientRequestID,
			fingerprintText,
			messageRequestStatePending,
			s.messageRequestInstance,
			ownerToken,
			now,
			now,
		)
		if err != nil {
			return MessageRequestClaim{}, fmt.Errorf("claim agent chat message request: %w", err)
		}
		if inserted, rowsErr := result.RowsAffected(); rowsErr == nil && inserted == 1 {
			latest, loadErr := s.loadSession(ctx, sessionID)
			if loadErr != nil {
				return MessageRequestClaim{}, loadErr
			}
			return MessageRequestClaim{
				Lease: MessageRequestLease{
					SessionID:       sessionID,
					ClientRequestID: clientRequestID,
					Fingerprint:     fingerprint,
					OwnerToken:      ownerToken,
				},
				Session: latest,
			}, nil
		}

		var storedFingerprint string
		var state string
		var ownerInstance string
		var storedOwnerToken string
		var messageID string
		var updatedAt time.Time
		err = s.client.DB().QueryRowContext(
			ctx,
			fmt.Sprintf(
				`SELECT payload_fingerprint, state, owner_instance, owner_token, message_id, updated_at
				 FROM %s
				 WHERE session_id = ? AND client_request_id = ?`,
				s.messageRequestsTable,
			),
			sessionID,
			clientRequestID,
		).Scan(&storedFingerprint, &state, &ownerInstance, &storedOwnerToken, &messageID, &updatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return MessageRequestClaim{}, fmt.Errorf("read agent chat message request: %w", err)
		}
		if storedFingerprint != fingerprintText {
			return MessageRequestClaim{}, ErrMessageRequestPayloadConflict
		}
		switch state {
		case messageRequestStateCommitted:
			if messageID == "" {
				return MessageRequestClaim{}, fmt.Errorf("committed agent chat message request has no message id")
			}
			latest, loadErr := s.loadSession(ctx, sessionID)
			if loadErr != nil {
				return MessageRequestClaim{}, loadErr
			}
			return MessageRequestClaim{
				Session:            latest,
				Replay:             true,
				CommittedMessageID: messageID,
			}, nil
		case messageRequestStatePending:
			if ownerInstance == s.messageRequestInstance && storedOwnerToken == ownerToken {
				latest, loadErr := s.loadSession(ctx, sessionID)
				if loadErr != nil {
					return MessageRequestClaim{}, loadErr
				}
				return MessageRequestClaim{
					Lease: MessageRequestLease{
						SessionID:       sessionID,
						ClientRequestID: clientRequestID,
						Fingerprint:     fingerprint,
						OwnerToken:      ownerToken,
					},
					Session: latest,
				}, nil
			}
			// A different SQL store may be another live runtime. Only reclaim a
			// pending pre-dispatch owner after its bounded lease is stale, using a
			// conditional delete so a concurrent commit/refresh wins safely.
			staleBefore := now.Add(-s.MessageRequestLeaseTTL())
			if !updatedAt.After(staleBefore) {
				_, deleteErr := s.client.DB().ExecContext(
					ctx,
					fmt.Sprintf(
						`DELETE FROM %s
						 WHERE session_id = ? AND client_request_id = ? AND state = ?
						   AND owner_instance = ? AND owner_token = ? AND payload_fingerprint = ?
						   AND updated_at <= ?`,
						s.messageRequestsTable,
					),
					sessionID,
					clientRequestID,
					messageRequestStatePending,
					ownerInstance,
					storedOwnerToken,
					fingerprintText,
					staleBefore,
				)
				if deleteErr != nil {
					return MessageRequestClaim{}, fmt.Errorf("reclaim interrupted agent chat message request: %w", deleteErr)
				}
				continue
			}
		default:
			return MessageRequestClaim{}, fmt.Errorf("agent chat message request has invalid state")
		}

		timer := time.NewTimer(messageRequestPollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return MessageRequestClaim{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *SQLiteStore) RenewMessageRequest(ctx context.Context, req RenewMessageRequestRequest) error {
	lease := req.Lease
	if lease.Empty() {
		return ErrMessageRequestLeaseInvalid
	}
	result, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s
			 SET updated_at = ?
			 WHERE session_id = ? AND client_request_id = ? AND state = ?
			   AND owner_instance = ? AND owner_token = ? AND payload_fingerprint = ?`,
			s.messageRequestsTable,
		),
		s.messageRequestNowUTC(),
		lease.SessionID,
		lease.ClientRequestID,
		messageRequestStatePending,
		s.messageRequestInstance,
		lease.OwnerToken,
		hex.EncodeToString(lease.Fingerprint[:]),
	)
	if err != nil {
		return fmt.Errorf("renew agent chat message request: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read renewed agent chat message request rows: %w", err)
	}
	if updated != 1 {
		return ErrMessageRequestLeaseInvalid
	}
	return nil
}

func (s *SQLiteStore) CommitMessageRequest(ctx context.Context, lease MessageRequestLease, message Message) (Session, error) {
	if message.ID == "" {
		return Session{}, fmt.Errorf("message id is required")
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	session, err := s.loadSession(ctx, lease.SessionID)
	if err != nil {
		return Session{}, err
	}
	hydrateMessageRuntimeFromSession(&message, session)
	fingerprintText := hex.EncodeToString(lease.Fingerprint[:])

	tx, err := s.client.DB().BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("begin agent chat message request tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var storedFingerprint string
	var state string
	var ownerToken string
	err = tx.QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT payload_fingerprint, state, owner_token
			 FROM %s
			 WHERE session_id = ? AND client_request_id = ?`,
			s.messageRequestsTable,
		),
		lease.SessionID,
		lease.ClientRequestID,
	).Scan(&storedFingerprint, &state, &ownerToken)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrMessageRequestLeaseInvalid
	}
	if err != nil {
		return Session{}, fmt.Errorf("read agent chat message request for commit: %w", err)
	}
	if storedFingerprint != fingerprintText {
		return Session{}, ErrMessageRequestPayloadConflict
	}
	if ownerToken != lease.OwnerToken {
		return Session{}, ErrMessageRequestLeaseInvalid
	}
	if state == messageRequestStateCommitted {
		committed, loadErr := s.loadSessionFrom(ctx, tx, lease.SessionID)
		if loadErr != nil {
			return Session{}, loadErr
		}
		_ = tx.Rollback()
		return committed, nil
	}
	if state != messageRequestStatePending {
		return Session{}, ErrMessageRequestLeaseInvalid
	}
	if err := s.appendMessageTx(ctx, tx, lease.SessionID, message); err != nil {
		return Session{}, err
	}
	result, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(
			`UPDATE %s
			 SET state = ?, message_id = ?, updated_at = ?
			 WHERE session_id = ? AND client_request_id = ? AND state = ?
			   AND owner_token = ? AND payload_fingerprint = ?`,
			s.messageRequestsTable,
		),
		messageRequestStateCommitted,
		message.ID,
		time.Now().UTC(),
		lease.SessionID,
		lease.ClientRequestID,
		messageRequestStatePending,
		lease.OwnerToken,
		fingerprintText,
	)
	if err != nil {
		return Session{}, fmt.Errorf("commit agent chat message request: %w", err)
	}
	if updated, rowsErr := result.RowsAffected(); rowsErr != nil || updated != 1 {
		return Session{}, ErrMessageRequestLeaseInvalid
	}
	// Materialize the authoritative response inside the atomic transaction.
	// Once Commit succeeds, this method must not perform another request-bound
	// read that can report cancellation after the user row and idempotency key
	// are already durable.
	committed, err := s.loadSessionFrom(ctx, tx, lease.SessionID)
	if err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit agent chat message request tx: %w", err)
	}
	return committed, nil
}

func (s *SQLiteStore) ReleaseMessageRequest(ctx context.Context, lease MessageRequestLease) error {
	_, err := s.client.DB().ExecContext(
		ctx,
		fmt.Sprintf(
			`DELETE FROM %s
			 WHERE session_id = ? AND client_request_id = ? AND state = ?
			   AND owner_token = ? AND payload_fingerprint = ?`,
			s.messageRequestsTable,
		),
		lease.SessionID,
		lease.ClientRequestID,
		messageRequestStatePending,
		lease.OwnerToken,
		hex.EncodeToString(lease.Fingerprint[:]),
	)
	if err != nil {
		return fmt.Errorf("release agent chat message request: %w", err)
	}
	return nil
}
