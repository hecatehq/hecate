package chatattachments

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

const (
	// MaxDraftAttachmentsPerSession leaves room for one failed/retried UI
	// submission while keeping abandoned uploads bounded.
	MaxDraftAttachmentsPerSession = 8
	// MaxDraftBytesPerSession is two full four-file messages at the public
	// five MiB per-file upload limit.
	MaxDraftBytesPerSession = int64(40 << 20)
	// MaxStoredBytesPerSession bounds total retained body data for one chat,
	// including linked files. Deleting the chat releases the quota.
	MaxStoredBytesPerSession = int64(512 << 20)
	// MaxMemoryStoredBytesTotal keeps the default in-process backend below a
	// defensible retained-body ceiling. Decode and request buffers are bounded
	// separately because they are transient copies outside this counter.
	MaxMemoryStoredBytesTotal = int64(512 << 20)
	// MaxDurableStoredBytesTotal bounds SQLite/Postgres disk retention across
	// otherwise independent chat sessions.
	MaxDurableStoredBytesTotal = int64(4 << 30)
	// DraftTTL is the age at which a later upload may reclaim an unlinked
	// draft. It is not a wall-clock deletion scheduler. Linked bodies are
	// retained with their session.
	DraftTTL = 24 * time.Hour
)

var (
	ErrAlreadyExists   = errors.New("chat attachment already exists")
	ErrNotFound        = errors.New("chat attachment not found")
	ErrNotDraft        = errors.New("chat attachment is not an unclaimed draft")
	ErrNotClaimed      = errors.New("chat attachment is not claimed")
	ErrClaimLost       = errors.New("chat attachment claim fence no longer matches")
	ErrDraftQuota      = errors.New("chat attachment draft quota exceeded")
	ErrSessionQuota    = errors.New("chat attachment session storage quota exceeded")
	ErrTotalQuota      = errors.New("chat attachment total storage quota exceeded")
	ErrInvalidMetadata = errors.New("chat attachment size metadata does not match body")
)

type lifecycleState string

type TotalQuotaError struct {
	LimitBytes int64
}

func (e TotalQuotaError) Error() string { return ErrTotalQuota.Error() }

func (e TotalQuotaError) Unwrap() error { return ErrTotalQuota }

const (
	lifecycleDraft   lifecycleState = "draft"
	lifecycleClaimed lifecycleState = "claimed"
	lifecycleLinked  lifecycleState = "linked"
)

// Attachment is the persisted descriptor for a file owned by one chat
// session. Validation of names, media types, sizes, and digests belongs to the
// application boundary that accepts the upload.
type Attachment struct {
	ID        string
	SessionID string
	Filename  string
	MediaType string
	SizeBytes int64
	SHA256    string
	CreatedAt time.Time
}

// StoredAttachment keeps opaque bytes out of transcript serialization and
// metadata-only list paths.
type StoredAttachment struct {
	Attachment
	Data []byte
}

type ClaimRef struct {
	SessionID     string
	MessageID     string
	AttachmentIDs []string
}

type ClaimResolution string

const (
	ClaimLinked   ClaimResolution = "linked"
	ClaimReleased ClaimResolution = "released"
)

// PendingClaim is metadata-only recovery state. Attachment bodies are never
// exposed to startup reconciliation or logs.
type PendingClaim struct {
	Ref         ClaimRef
	Attachments []Attachment
	ClaimedAt   time.Time
}

type Store interface {
	Backend() string
	Create(ctx context.Context, attachment StoredAttachment) (StoredAttachment, error)
	Get(ctx context.Context, sessionID, id string) (StoredAttachment, bool, error)
	List(ctx context.Context, sessionID string) ([]Attachment, error)
	Claim(ctx context.Context, ref ClaimRef) ([]StoredAttachment, error)
	ResolveClaim(ctx context.Context, ref ClaimRef, resolution ClaimResolution) error
	ListPendingClaims(ctx context.Context) ([]PendingClaim, error)
	ListSessionIDs(ctx context.Context) ([]string, error)
	DeleteDraft(ctx context.Context, sessionID, id string) error
	DeleteBySessionID(ctx context.Context, sessionID string) error
}

type MemoryStore struct {
	mu                       sync.RWMutex
	attachments              map[string]map[string]memoryRecord
	maxStoredBytesPerSession int64
	maxStoredBytesTotal      int64
	storedBytes              int64
}

type memoryRecord struct {
	attachment StoredAttachment
	state      lifecycleState
	messageID  string
	claimedAt  time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		attachments:              make(map[string]map[string]memoryRecord),
		maxStoredBytesPerSession: MaxStoredBytesPerSession,
		maxStoredBytesTotal:      MaxMemoryStoredBytesTotal,
	}
}

func (s *MemoryStore) Backend() string { return "memory" }

func (s *MemoryStore) Create(_ context.Context, attachment StoredAttachment) (StoredAttachment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if attachment.SizeBytes != int64(len(attachment.Data)) {
		return StoredAttachment{}, ErrInvalidMetadata
	}
	now := time.Now().UTC()
	if attachment.CreatedAt.IsZero() {
		attachment.CreatedAt = now
	} else {
		attachment.CreatedAt = attachment.CreatedAt.UTC()
	}
	s.storedBytes -= reclaimStaleMemoryDrafts(s.attachments, now.Add(-DraftTTL))
	byID := s.attachments[attachment.SessionID]
	if byID == nil {
		byID = make(map[string]memoryRecord)
	}
	if _, exists := byID[attachment.ID]; exists {
		return StoredAttachment{}, ErrAlreadyExists
	}
	count, size := memoryDraftUsage(byID)
	if count >= MaxDraftAttachmentsPerSession || attachment.SizeBytes > MaxDraftBytesPerSession-size {
		return StoredAttachment{}, ErrDraftQuota
	}
	if attachment.SizeBytes > s.maxStoredBytesPerSession-memoryStoredUsage(byID) {
		return StoredAttachment{}, ErrSessionQuota
	}
	if attachment.SizeBytes > s.maxStoredBytesTotal-s.storedBytes {
		return StoredAttachment{}, TotalQuotaError{LimitBytes: s.maxStoredBytesTotal}
	}
	attachment = cloneStoredAttachment(attachment)
	if s.attachments[attachment.SessionID] == nil {
		s.attachments[attachment.SessionID] = byID
	}
	byID[attachment.ID] = memoryRecord{attachment: attachment, state: lifecycleDraft}
	s.storedBytes += attachment.SizeBytes
	return cloneStoredAttachment(attachment), nil
}

func (s *MemoryStore) Get(_ context.Context, sessionID, id string) (StoredAttachment, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	record, ok := s.attachments[sessionID][id]
	if !ok {
		return StoredAttachment{}, false, nil
	}
	return cloneStoredAttachment(record.attachment), true, nil
}

func (s *MemoryStore) List(_ context.Context, sessionID string) ([]Attachment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	byID := s.attachments[sessionID]
	items := make([]Attachment, 0, len(byID))
	for _, record := range byID {
		items = append(items, record.attachment.Attachment)
	}
	sortAttachments(items)
	return items, nil
}

func (s *MemoryStore) Claim(_ context.Context, ref ClaimRef) ([]StoredAttachment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	byID := s.attachments[ref.SessionID]
	claimed := make([]StoredAttachment, 0, len(ref.AttachmentIDs))
	for _, id := range ref.AttachmentIDs {
		record, ok := byID[id]
		if !ok {
			return nil, ErrNotFound
		}
		if record.state != lifecycleDraft && !(record.state == lifecycleClaimed && record.messageID == ref.MessageID) {
			return nil, ErrNotDraft
		}
		claimed = append(claimed, cloneStoredAttachment(record.attachment))
	}
	now := time.Now().UTC()
	for _, id := range ref.AttachmentIDs {
		record := byID[id]
		if record.state == lifecycleDraft {
			record.state = lifecycleClaimed
			record.messageID = ref.MessageID
			record.claimedAt = now
		}
		byID[id] = record
	}
	return claimed, nil
}

func (s *MemoryStore) ResolveClaim(_ context.Context, ref ClaimRef, resolution ClaimResolution) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	byID := s.attachments[ref.SessionID]
	for _, id := range ref.AttachmentIDs {
		record, ok := byID[id]
		if !ok {
			return ErrNotFound
		}
		if record.messageID != ref.MessageID {
			return ErrClaimLost
		}
		switch resolution {
		case ClaimLinked:
			if record.state != lifecycleClaimed && record.state != lifecycleLinked {
				return ErrClaimLost
			}
		case ClaimReleased:
			if record.state != lifecycleClaimed && record.state != lifecycleDraft {
				return ErrClaimLost
			}
		default:
			return ErrClaimLost
		}
	}
	for _, id := range ref.AttachmentIDs {
		record := byID[id]
		if record.state == lifecycleClaimed {
			if resolution == ClaimLinked {
				record.state = lifecycleLinked
			} else {
				record.state = lifecycleDraft
			}
		}
		byID[id] = record
	}
	return nil
}

func (s *MemoryStore) ListPendingClaims(_ context.Context) ([]PendingClaim, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	claims := make(map[string]*PendingClaim)
	for sessionID, byID := range s.attachments {
		for _, record := range byID {
			if record.state != lifecycleClaimed {
				continue
			}
			key := sessionID + "\x00" + record.messageID
			claim := claims[key]
			if claim == nil {
				claim = &PendingClaim{
					Ref:       ClaimRef{SessionID: sessionID, MessageID: record.messageID},
					ClaimedAt: record.claimedAt,
				}
				claims[key] = claim
			}
			claim.Ref.AttachmentIDs = append(claim.Ref.AttachmentIDs, record.attachment.ID)
			claim.Attachments = append(claim.Attachments, record.attachment.Attachment)
			if claim.ClaimedAt.IsZero() || record.claimedAt.Before(claim.ClaimedAt) {
				claim.ClaimedAt = record.claimedAt
			}
		}
	}
	items := make([]PendingClaim, 0, len(claims))
	for _, claim := range claims {
		sort.Strings(claim.Ref.AttachmentIDs)
		sortAttachments(claim.Attachments)
		items = append(items, *claim)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Ref.SessionID == items[j].Ref.SessionID {
			return items[i].Ref.MessageID < items[j].Ref.MessageID
		}
		return items[i].Ref.SessionID < items[j].Ref.SessionID
	})
	return items, nil
}

func (s *MemoryStore) ListSessionIDs(_ context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]string, 0, len(s.attachments))
	for sessionID, byID := range s.attachments {
		if len(byID) > 0 {
			ids = append(ids, sessionID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *MemoryStore) DeleteDraft(_ context.Context, sessionID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	byID := s.attachments[sessionID]
	record, ok := byID[id]
	if !ok {
		return ErrNotFound
	}
	if record.state != lifecycleDraft {
		return ErrNotDraft
	}
	delete(byID, id)
	s.storedBytes -= record.attachment.SizeBytes
	if len(byID) == 0 {
		delete(s.attachments, sessionID)
	}
	return nil
}

func (s *MemoryStore) DeleteBySessionID(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, record := range s.attachments[sessionID] {
		s.storedBytes -= record.attachment.SizeBytes
	}
	delete(s.attachments, sessionID)
	return nil
}

func cloneStoredAttachment(attachment StoredAttachment) StoredAttachment {
	attachment.Data = append([]byte(nil), attachment.Data...)
	return attachment
}

func reclaimStaleMemoryDrafts(attachments map[string]map[string]memoryRecord, cutoff time.Time) int64 {
	var reclaimedBytes int64
	for sessionID, byID := range attachments {
		for id, record := range byID {
			if record.state == lifecycleDraft && record.attachment.CreatedAt.Before(cutoff) {
				reclaimedBytes += record.attachment.SizeBytes
				delete(byID, id)
			}
		}
		if len(byID) == 0 {
			delete(attachments, sessionID)
		}
	}
	return reclaimedBytes
}

func memoryDraftUsage(byID map[string]memoryRecord) (int, int64) {
	var count int
	var size int64
	for _, record := range byID {
		if record.state == lifecycleLinked {
			continue
		}
		count++
		size += record.attachment.SizeBytes
	}
	return count, size
}

func memoryStoredUsage(byID map[string]memoryRecord) int64 {
	var size int64
	for _, record := range byID {
		size += record.attachment.SizeBytes
	}
	return size
}

func (s *MemoryStore) setMaxStoredBytesPerSession(limit int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxStoredBytesPerSession = limit
}

func (s *MemoryStore) setMaxStoredBytesTotal(limit int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxStoredBytesTotal = limit
}

func sortAttachments(items []Attachment) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
}
