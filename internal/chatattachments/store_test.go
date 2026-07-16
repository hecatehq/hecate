package chatattachments

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryStore_Conformance(t *testing.T) {
	RunConformanceTests(t, "MemoryStore", func(*testing.T) Store {
		return NewMemoryStore()
	})
}

func TestMemoryStore_TotalQuotaRejectionDoesNotRetainEmptySession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	store.setMaxStoredBytesTotal(1)
	if _, err := store.Create(ctx, StoredAttachment{
		Attachment: Attachment{ID: "kept", SessionID: "session_kept", SizeBytes: 1},
		Data:       []byte("x"),
	}); err != nil {
		t.Fatalf("Create(kept): %v", err)
	}
	if _, err := store.Create(ctx, StoredAttachment{
		Attachment: Attachment{ID: "rejected", SessionID: "session_rejected", SizeBytes: 1},
		Data:       []byte("y"),
	}); !errors.Is(err, ErrTotalQuota) {
		t.Fatalf("Create(rejected) error = %v, want ErrTotalQuota", err)
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	if _, ok := store.attachments["session_rejected"]; ok {
		t.Fatal("total-quota rejection retained an empty session map")
	}
}
