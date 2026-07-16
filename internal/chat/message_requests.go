package chat

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

const (
	// MessageRequestLeaseStaleAfter bounds how long another runtime must wait
	// before reclaiming a pre-commit reservation that is no longer renewed.
	MessageRequestLeaseStaleAfter = 2 * time.Minute
	messageRequestPollInterval    = 15 * time.Millisecond
)

var (
	ErrMessageRequestPayloadConflict = errors.New("client_request_id was already used with a different chat message payload")
	ErrMessageRequestLeaseInvalid    = errors.New("chat message request lease is no longer valid")
)

// MessageRequestFingerprint is a one-way digest of the API payload associated
// with a client_request_id. Stores persist only this digest, never prompt,
// system-prompt, MCP configuration, or attachment bodies.
type MessageRequestFingerprint [sha256.Size]byte

// MessageRequestLease is an internal ownership token for the request that won
// the right to append a user message. It is never rendered through the API.
type MessageRequestLease struct {
	SessionID       string
	ClientRequestID string
	Fingerprint     MessageRequestFingerprint
	OwnerToken      string
}

// RenewMessageRequestRequest carries the internal ownership proof required to
// refresh one pending reservation.
type RenewMessageRequestRequest struct {
	Lease MessageRequestLease
}

func (lease MessageRequestLease) Empty() bool {
	return lease.SessionID == "" || lease.ClientRequestID == "" || lease.OwnerToken == ""
}

// MessageRequestClaim returns either a lease for a new request or Replay=true
// with the latest authoritative session for an already committed request.
type MessageRequestClaim struct {
	Lease              MessageRequestLease
	Session            Session
	Replay             bool
	CommittedMessageID string
}

type messageRequestKey struct {
	SessionID       string
	ClientRequestID string
}

type memoryMessageRequest struct {
	fingerprint MessageRequestFingerprint
	ownerToken  string
	messageID   string
	committed   bool
	updatedAt   time.Time
	done        chan struct{}
}

func newMessageRequestToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate chat message request token: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func (s *MemoryStore) ClaimMessageRequest(ctx context.Context, sessionID, clientRequestID string, fingerprint MessageRequestFingerprint) (MessageRequestClaim, error) {
	ownerToken, err := newMessageRequestToken()
	if err != nil {
		return MessageRequestClaim{}, err
	}
	key := messageRequestKey{SessionID: sessionID, ClientRequestID: clientRequestID}
	for {
		s.mu.Lock()
		now := s.messageRequestNowUTC()
		session, ok := s.sessions[sessionID]
		if !ok {
			s.mu.Unlock()
			return MessageRequestClaim{}, fmt.Errorf("agent chat session %q not found", sessionID)
		}
		record, found := s.messageRequests[key]
		if !found {
			s.messageRequests[key] = &memoryMessageRequest{
				fingerprint: fingerprint,
				ownerToken:  ownerToken,
				updatedAt:   now,
				done:        make(chan struct{}),
			}
			s.mu.Unlock()
			return MessageRequestClaim{
				Lease: MessageRequestLease{
					SessionID:       sessionID,
					ClientRequestID: clientRequestID,
					Fingerprint:     fingerprint,
					OwnerToken:      ownerToken,
				},
				Session: cloneSession(session),
			}, nil
		}
		if record.fingerprint != fingerprint {
			s.mu.Unlock()
			return MessageRequestClaim{}, ErrMessageRequestPayloadConflict
		}
		if record.committed {
			s.mu.Unlock()
			return MessageRequestClaim{
				Session:            cloneSession(session),
				Replay:             true,
				CommittedMessageID: record.messageID,
			}, nil
		}
		if !record.updatedAt.After(now.Add(-s.MessageRequestLeaseTTL())) {
			previousDone := record.done
			s.messageRequests[key] = &memoryMessageRequest{
				fingerprint: fingerprint,
				ownerToken:  ownerToken,
				updatedAt:   now,
				done:        make(chan struct{}),
			}
			close(previousDone)
			s.mu.Unlock()
			return MessageRequestClaim{
				Lease: MessageRequestLease{
					SessionID:       sessionID,
					ClientRequestID: clientRequestID,
					Fingerprint:     fingerprint,
					OwnerToken:      ownerToken,
				},
				Session: cloneSession(session),
			}, nil
		}
		done := record.done
		s.mu.Unlock()

		timer := time.NewTimer(messageRequestPollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return MessageRequestClaim{}, ctx.Err()
		case <-done:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
}

func (s *MemoryStore) RenewMessageRequest(_ context.Context, req RenewMessageRequestRequest) error {
	lease := req.Lease
	if lease.Empty() {
		return ErrMessageRequestLeaseInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.messageRequests[messageRequestKey{SessionID: lease.SessionID, ClientRequestID: lease.ClientRequestID}]
	if !ok || record.committed || record.ownerToken != lease.OwnerToken || record.fingerprint != lease.Fingerprint {
		return ErrMessageRequestLeaseInvalid
	}
	record.updatedAt = s.messageRequestNowUTC()
	return nil
}

func (s *MemoryStore) CommitMessageRequest(_ context.Context, lease MessageRequestLease, message Message) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := messageRequestKey{SessionID: lease.SessionID, ClientRequestID: lease.ClientRequestID}
	record, ok := s.messageRequests[key]
	if !ok || record.ownerToken != lease.OwnerToken || record.fingerprint != lease.Fingerprint {
		return Session{}, ErrMessageRequestLeaseInvalid
	}
	session, ok := s.sessions[lease.SessionID]
	if !ok {
		return Session{}, fmt.Errorf("agent chat session %q not found", lease.SessionID)
	}
	if record.committed {
		return cloneSession(session), nil
	}
	if err := appendMemoryMessage(&session, message); err != nil {
		return Session{}, err
	}
	s.sessions[lease.SessionID] = session
	record.committed = true
	record.messageID = message.ID
	close(record.done)
	record.done = nil
	return cloneSession(session), nil
}

func (s *MemoryStore) ReleaseMessageRequest(_ context.Context, lease MessageRequestLease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := messageRequestKey{SessionID: lease.SessionID, ClientRequestID: lease.ClientRequestID}
	record, ok := s.messageRequests[key]
	if !ok || record.committed || record.ownerToken != lease.OwnerToken || record.fingerprint != lease.Fingerprint {
		return nil
	}
	delete(s.messageRequests, key)
	close(record.done)
	return nil
}
