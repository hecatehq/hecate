package modelprobe

import (
	"context"
	"strconv"
	"sync"
	"time"
)

type MemoryStore struct {
	mu      sync.Mutex
	records map[string]Record
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: make(map[string]Record)}
}

func (s *MemoryStore) Backend() string { return "memory" }

func (s *MemoryStore) Get(_ context.Context, key Key) (Record, bool, error) {
	key, err := NormalizeKey(key)
	if err != nil {
		return Record{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[memoryKey(key)]
	return record, ok, nil
}

func (s *MemoryStore) GetMany(_ context.Context, keys []Key) (map[Key]Record, error) {
	keys, err := normalizeKeys(keys)
	if err != nil {
		return nil, err
	}
	records := make(map[Key]Record, len(keys))
	if len(keys) == 0 {
		return records, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range keys {
		if record, found := s.records[memoryKey(key)]; found {
			records[key] = record
		}
	}
	return records, nil
}

func (s *MemoryStore) Acquire(_ context.Context, key Key, now time.Time, leaseUntil time.Time, leaseID string) (Record, bool, error) {
	key, err := NormalizeKey(key)
	if err != nil {
		return Record{}, false, err
	}
	if now.IsZero() || leaseUntil.IsZero() || !leaseUntil.After(now) || leaseID == "" {
		return Record{}, false, ErrInvalid
	}
	now = now.UTC()
	leaseUntil = leaseUntil.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.records[memoryKey(key)]; ok && (current.Active(now) || current.LeaseActive(now)) {
		return current, false, nil
	}
	record := Record{
		Key:        key,
		Status:     StatusTesting,
		CheckedAt:  now,
		ExpiresAt:  leaseUntil,
		LeaseUntil: leaseUntil,
		LeaseID:    leaseID,
	}
	s.records[memoryKey(key)] = record
	return record, true, nil
}

func (s *MemoryStore) Complete(_ context.Context, record Record) (Record, error) {
	record, err := NormalizeRecord(record)
	if err != nil {
		return Record{}, err
	}
	if record.Status == StatusTesting || record.LeaseID == "" {
		return Record{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.records[memoryKey(record.Key)]
	if !ok || current.Status != StatusTesting || current.LeaseID != record.LeaseID {
		return Record{}, ErrLeaseLost
	}
	record.LeaseID = ""
	record.LeaseUntil = time.Time{}
	s.records[memoryKey(record.Key)] = record
	return record, nil
}

func memoryKey(key Key) string {
	return key.Provider + "\x00" + key.Model + "\x00" + string(key.Instance.Kind) + "\x00" + key.Instance.ID + "\x00" + strconv.Itoa(key.Version)
}
