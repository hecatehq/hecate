package modelprobe

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestStoreLeaseAndGenerationConformance(t *testing.T) {
	t.Parallel()
	for _, factory := range []struct {
		name string
		new  func(*testing.T) Store
	}{
		{name: "memory", new: func(*testing.T) Store { return NewMemoryStore() }},
		{name: "sqlite", new: newSQLiteStore},
	} {
		factory := factory
		t.Run(factory.name, func(t *testing.T) {
			t.Parallel()
			store := factory.new(t)
			now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
			key := testKey("generation-a")
			lease, acquired, err := store.Acquire(t.Context(), key, now, now.Add(time.Minute), "lease-a")
			if err != nil || !acquired || lease.Status != StatusTesting {
				t.Fatalf("Acquire() = %+v, %t, %v", lease, acquired, err)
			}
			joined, acquired, err := store.Acquire(t.Context(), key, now.Add(time.Second), now.Add(time.Minute), "lease-b")
			if err != nil || acquired || joined.LeaseID != "lease-a" {
				t.Fatalf("joined Acquire() = %+v, %t, %v", joined, acquired, err)
			}

			lease.Status = StatusSupported
			lease.Reason = ReasonNone
			lease.CheckedAt = now.Add(2 * time.Second)
			lease.ExpiresAt = now.Add(24 * time.Hour)
			completed, err := store.Complete(t.Context(), lease)
			if err != nil || completed.Status != StatusSupported || completed.LeaseID != "" {
				t.Fatalf("Complete() = %+v, %v", completed, err)
			}
			cached, acquired, err := store.Acquire(t.Context(), key, now.Add(time.Minute), now.Add(2*time.Minute), "lease-c")
			if err != nil || acquired || cached.Status != StatusSupported {
				t.Fatalf("cached Acquire() = %+v, %t, %v", cached, acquired, err)
			}

			stale := completed
			stale.LeaseID = "wrong-lease"
			if _, err := store.Complete(t.Context(), stale); !errors.Is(err, ErrLeaseLost) && !errors.Is(err, ErrInvalid) {
				t.Fatalf("stale Complete() error = %v, want lease failure", err)
			}
			if _, found, err := store.Get(t.Context(), testKey("generation-b")); err != nil || found {
				t.Fatalf("Get(new generation) = found %t, err %v", found, err)
			}

			expired, acquired, err := store.Acquire(t.Context(), key, now.Add(25*time.Hour), now.Add(25*time.Hour+time.Minute), "lease-d")
			if err != nil || !acquired || expired.LeaseID != "lease-d" {
				t.Fatalf("Acquire(expired) = %+v, %t, %v", expired, acquired, err)
			}
		})
	}
}

func TestStoreGetManyReturnsOnlyExactGenerationRecords(t *testing.T) {
	t.Parallel()
	for _, factory := range []struct {
		name string
		new  func(*testing.T) Store
	}{
		{name: "memory", new: func(*testing.T) Store { return NewMemoryStore() }},
		{name: "sqlite", new: newSQLiteStore},
	} {
		factory := factory
		t.Run(factory.name, func(t *testing.T) {
			t.Parallel()
			store := factory.new(t)
			batch, ok := store.(BatchStore)
			if !ok {
				t.Fatalf("%T does not implement BatchStore", store)
			}
			now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
			key := testKey("generation-a")
			lease, acquired, err := store.Acquire(t.Context(), key, now, now.Add(time.Minute), "batch-lease")
			if err != nil || !acquired {
				t.Fatalf("Acquire() = %+v, %t, %v", lease, acquired, err)
			}
			lease.Status = StatusSupported
			lease.CheckedAt = now
			lease.ExpiresAt = now.Add(time.Hour)
			if _, err := store.Complete(t.Context(), lease); err != nil {
				t.Fatalf("Complete() error = %v", err)
			}

			records, err := batch.GetMany(t.Context(), []Key{key, key, testKey("generation-b")})
			if err != nil {
				t.Fatalf("GetMany() error = %v", err)
			}
			if len(records) != 1 || records[key].Status != StatusSupported {
				t.Fatalf("GetMany() = %+v, want only supported generation-a record", records)
			}
		})
	}
}

func TestCoordinatorCoalescesProviderCalls(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	coordinator := NewCoordinator(store)
	coordinator.now = func() time.Time { return time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC) }
	started := make(chan struct{})
	release := make(chan struct{})
	results := make(chan Record, 2)
	errs := make(chan error, 2)
	var calls atomic.Int32
	for range 2 {
		go func() {
			record, _, err := coordinator.Verify(context.Background(), testKey("generation-a"), func(context.Context) Outcome {
				calls.Add(1)
				select {
				case started <- struct{}{}:
				default:
				}
				<-release
				return Outcome{Status: StatusSupported}
			})
			results <- record
			errs <- err
		}()
	}
	<-started
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("Verify() error = %v", err)
		}
		if record := <-results; record.Status != StatusSupported {
			t.Fatalf("Verify() record = %+v", record)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("probe calls = %d, want 1", got)
	}
}

func TestCoordinatorSkipsProbeWhenCallerIsAlreadyCanceled(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	coordinator := NewCoordinator(store)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false

	record, performed, err := coordinator.Verify(ctx, testKey("generation-a"), func(context.Context) Outcome {
		called = true
		return Outcome{Status: StatusSupported}
	})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !performed || called || record.Status != StatusInconclusive || record.Reason != ReasonUnexpectedResult {
		t.Fatalf("Verify() = record %+v performed=%t called=%t, want completed inconclusive without provider call", record, performed, called)
	}
}

func testKey(instance string) Key {
	return Key{
		Provider: "local-provider",
		Model:    "custom-model",
		Instance: types.ProviderInstanceIdentity{ID: instance, Kind: types.ProviderInstanceIdentityConfiguration},
		Version:  ProbeVersion,
	}
}

func newSQLiteStore(t *testing.T) Store {
	t.Helper()
	client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path: filepath.Join(t.TempDir(), "modelprobe.db"),
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	store, err := NewSQLiteStore(context.Background(), client)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	return store
}
