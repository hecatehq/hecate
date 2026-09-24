package agentadapters

import (
	"context"
	"sync"
	"time"
)

type executableTrustKey struct {
	runtimeHostID string
	adapterID     string
}

// MemoryExecutableTrustStore keeps executable approvals for the lifetime of
// the current Hecate process. Records are keyed by runtime host as well as
// adapter so approval on one host never authorizes an executable on another.
type MemoryExecutableTrustStore struct {
	mu      sync.RWMutex
	records map[executableTrustKey]ExecutableTrustRecord
}

func NewMemoryExecutableTrustStore() *MemoryExecutableTrustStore {
	return &MemoryExecutableTrustStore{
		records: make(map[executableTrustKey]ExecutableTrustRecord),
	}
}

func (s *MemoryExecutableTrustStore) Backend() string { return "memory" }

func (s *MemoryExecutableTrustStore) Get(ctx context.Context, runtimeHostID, adapterID string) (ExecutableTrustRecord, error) {
	if err := ctx.Err(); err != nil {
		return ExecutableTrustRecord{}, err
	}
	s.mu.RLock()
	record, ok := s.records[executableTrustKey{runtimeHostID: runtimeHostID, adapterID: adapterID}]
	s.mu.RUnlock()
	if !ok {
		return ExecutableTrustRecord{}, ErrExecutableTrustNotFound
	}
	return cloneExecutableTrustRecord(record), nil
}

func (s *MemoryExecutableTrustStore) Approve(ctx context.Context, record ExecutableTrustRecord) (ExecutableTrustRecord, error) {
	if err := ctx.Err(); err != nil {
		return ExecutableTrustRecord{}, err
	}
	record = cloneExecutableTrustRecord(record)
	if record.ApprovedAt.IsZero() {
		record.ApprovedAt = time.Now().UTC()
	} else {
		record.ApprovedAt = record.ApprovedAt.UTC()
	}

	s.mu.Lock()
	s.records[executableTrustKey{
		runtimeHostID: record.RuntimeHostID,
		adapterID:     record.AdapterID,
	}] = record
	s.mu.Unlock()

	return cloneExecutableTrustRecord(record), nil
}

func (s *MemoryExecutableTrustStore) Revoke(ctx context.Context, runtimeHostID, adapterID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.records, executableTrustKey{runtimeHostID: runtimeHostID, adapterID: adapterID})
	s.mu.Unlock()
	return nil
}

var _ ExecutableTrustStore = (*MemoryExecutableTrustStore)(nil)
