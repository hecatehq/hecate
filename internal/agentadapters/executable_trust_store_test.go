package agentadapters

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
)

type executableTrustStoreFactory func(t *testing.T) ExecutableTrustStore

func TestMemoryExecutableTrustStoreConformance(t *testing.T) {
	runExecutableTrustStoreConformance(t, "memory", func(t *testing.T) ExecutableTrustStore {
		t.Helper()
		return NewMemoryExecutableTrustStore()
	})
}

func TestSQLiteExecutableTrustStoreConformance(t *testing.T) {
	runExecutableTrustStoreConformance(t, "sqlite", func(t *testing.T) ExecutableTrustStore {
		t.Helper()
		client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
			Path:        filepath.Join(t.TempDir(), "executable-trust.db"),
			TablePrefix: "test",
		})
		if err != nil {
			t.Fatalf("NewSQLiteClient: %v", err)
		}
		t.Cleanup(func() { _ = client.Close() })
		store, err := NewSQLiteExecutableTrustStore(context.Background(), client)
		if err != nil {
			t.Fatalf("NewSQLiteExecutableTrustStore: %v", err)
		}
		return store
	})
}

func runExecutableTrustStoreConformance(t *testing.T, name string, factory executableTrustStoreFactory) {
	t.Helper()
	t.Run(name+"/ApproveGetAndClone", func(t *testing.T) {
		t.Parallel()
		store := factory(t)
		ctx := context.Background()
		approvedAt := time.Date(2026, 9, 24, 11, 12, 13, 456000000, time.FixedZone("test", 2*60*60))
		record := executableTrustTestRecord("host-a", "codex", "sha256:one", approvedAt)

		created, err := store.Approve(ctx, record)
		if err != nil {
			t.Fatalf("Approve: %v", err)
		}
		if created.ApprovedAt.Location() != time.UTC || !created.ApprovedAt.Equal(approvedAt) {
			t.Fatalf("ApprovedAt = %v (%v), want UTC %v", created.ApprovedAt, created.ApprovedAt.Location(), approvedAt)
		}

		// Neither the caller's input nor returned values may alias stored slices.
		record.Identity.LauncherChain[0] = "changed-input"
		created.Identity.LauncherChain[0] = "changed-output"
		got, err := store.Get(ctx, "host-a", "codex")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		want := executableTrustTestRecord("host-a", "codex", "sha256:one", approvedAt.UTC())
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Get = %#v, want %#v", got, want)
		}
		got.Identity.LauncherChain[0] = "changed-get"
		again, err := store.Get(ctx, "host-a", "codex")
		if err != nil {
			t.Fatalf("Get after output mutation: %v", err)
		}
		if again.Identity.LauncherChain[0] != "/usr/bin/env" {
			t.Fatalf("stored launcher chain was aliased: %#v", again.Identity.LauncherChain)
		}
	})

	t.Run(name+"/CompositeKeyAndReplacement", func(t *testing.T) {
		t.Parallel()
		store := factory(t)
		ctx := context.Background()
		now := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
		rows := []ExecutableTrustRecord{
			executableTrustTestRecord("host-a", "codex", "sha256:a-codex", now),
			executableTrustTestRecord("host-b", "codex", "sha256:b-codex", now.Add(time.Minute)),
			executableTrustTestRecord("host-a", "claude_code", "sha256:a-claude", now.Add(2*time.Minute)),
		}
		for _, row := range rows {
			if _, err := store.Approve(ctx, row); err != nil {
				t.Fatalf("Approve(%s, %s): %v", row.RuntimeHostID, row.AdapterID, err)
			}
		}
		for _, want := range rows {
			got, err := store.Get(ctx, want.RuntimeHostID, want.AdapterID)
			if err != nil {
				t.Fatalf("Get(%s, %s): %v", want.RuntimeHostID, want.AdapterID, err)
			}
			if got.Identity.IdentityToken != want.Identity.IdentityToken {
				t.Fatalf("Get(%s, %s) token = %q, want %q", want.RuntimeHostID, want.AdapterID, got.Identity.IdentityToken, want.Identity.IdentityToken)
			}
		}

		replacement := executableTrustTestRecord("host-a", "codex", "sha256:replacement", now.Add(3*time.Minute))
		replacement.ApprovedBy = "second-operator"
		if _, err := store.Approve(ctx, replacement); err != nil {
			t.Fatalf("Approve replacement: %v", err)
		}
		got, err := store.Get(ctx, "host-a", "codex")
		if err != nil {
			t.Fatalf("Get replacement: %v", err)
		}
		if got.Identity.IdentityToken != "sha256:replacement" || got.ApprovedBy != "second-operator" {
			t.Fatalf("replacement not authoritative: %#v", got)
		}
		otherHost, err := store.Get(ctx, "host-b", "codex")
		if err != nil {
			t.Fatalf("Get other host after replacement: %v", err)
		}
		if otherHost.Identity.IdentityToken != "sha256:b-codex" {
			t.Fatalf("replacement crossed host boundary: %#v", otherHost)
		}
	})

	t.Run(name+"/MissingAndIdempotentRevoke", func(t *testing.T) {
		t.Parallel()
		store := factory(t)
		ctx := context.Background()
		if _, err := store.Get(ctx, "host-a", "missing"); !errors.Is(err, ErrExecutableTrustNotFound) {
			t.Fatalf("Get missing error = %v, want ErrExecutableTrustNotFound", err)
		}
		if err := store.Revoke(ctx, "host-a", "codex"); err != nil {
			t.Fatalf("Revoke missing: %v", err)
		}
		if _, err := store.Approve(ctx, executableTrustTestRecord("host-a", "codex", "sha256:one", time.Time{})); err != nil {
			t.Fatalf("Approve: %v", err)
		}
		created, err := store.Get(ctx, "host-a", "codex")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if created.ApprovedAt.IsZero() || created.ApprovedAt.Location() != time.UTC {
			t.Fatalf("generated ApprovedAt = %v, want non-zero UTC", created.ApprovedAt)
		}
		if err := store.Revoke(ctx, "host-a", "codex"); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		if err := store.Revoke(ctx, "host-a", "codex"); err != nil {
			t.Fatalf("second Revoke: %v", err)
		}
		if _, err := store.Get(ctx, "host-a", "codex"); !errors.Is(err, ErrExecutableTrustNotFound) {
			t.Fatalf("Get after revoke error = %v, want ErrExecutableTrustNotFound", err)
		}
	})

	t.Run(name+"/CancelledContextDoesNotMutate", func(t *testing.T) {
		t.Parallel()
		store := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		record := executableTrustTestRecord("host-a", "codex", "sha256:cancelled", time.Now().UTC())
		if _, err := store.Approve(ctx, record); !errors.Is(err, context.Canceled) {
			t.Fatalf("Approve(cancelled) error = %v, want context.Canceled", err)
		}
		if _, err := store.Get(context.Background(), "host-a", "codex"); !errors.Is(err, ErrExecutableTrustNotFound) {
			t.Fatalf("cancelled Approve mutated store; Get error = %v", err)
		}
	})
}

func TestSQLiteExecutableTrustStorePersistsAndMigratesIdempotently(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "executable-trust.db")
	config := storage.SQLiteConfig{Path: path, TablePrefix: "test"}
	client, err := storage.NewSQLiteClient(ctx, config)
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	store, err := NewSQLiteExecutableTrustStore(ctx, client)
	if err != nil {
		_ = client.Close()
		t.Fatalf("NewSQLiteExecutableTrustStore: %v", err)
	}
	if _, err := NewSQLiteExecutableTrustStore(ctx, client); err != nil {
		_ = client.Close()
		t.Fatalf("second migration on same client: %v", err)
	}
	want := executableTrustTestRecord(
		"runtime-host-1",
		"codex",
		"sha256:persisted",
		time.Date(2026, 9, 24, 10, 30, 0, 0, time.UTC),
	)
	if _, err := store.Approve(ctx, want); err != nil {
		_ = client.Close()
		t.Fatalf("Approve: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close first client: %v", err)
	}

	client, err = storage.NewSQLiteClient(ctx, config)
	if err != nil {
		t.Fatalf("reopen NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	reopened, err := NewSQLiteExecutableTrustStore(ctx, client)
	if err != nil {
		t.Fatalf("reopen NewSQLiteExecutableTrustStore: %v", err)
	}
	got, err := reopened.Get(ctx, want.RuntimeHostID, want.AdapterID)
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted record = %#v, want %#v", got, want)
	}
}

func executableTrustTestRecord(runtimeHostID, adapterID, token string, approvedAt time.Time) ExecutableTrustRecord {
	return ExecutableTrustRecord{
		RuntimeHostID: runtimeHostID,
		AdapterID:     adapterID,
		Identity: ExecutableIdentity{
			SchemaVersion:  ExecutableIdentitySchemaVersion,
			IdentityToken:  token,
			InvocationPath: "/opt/bin/" + adapterID,
			CanonicalPath:  "/opt/libexec/" + adapterID,
			SHA256:         "abcdef0123456789",
			Coverage:       ExecutableCoverageLauncherOnly,
			LauncherChain:  []string{"/usr/bin/env", "/opt/libexec/" + adapterID},
			FileID:         "device:inode",
			SizeBytes:      12345,
			Publisher: ExecutablePublisherEvidence{
				Status:   ExecutablePublisherUnavailable,
				Platform: "test",
			},
		},
		ApprovedBy: "operator",
		ApprovedAt: approvedAt,
	}
}

var (
	_ ExecutableTrustStore = (*MemoryExecutableTrustStore)(nil)
	_ ExecutableTrustStore = (*SQLExecutableTrustStore)(nil)
)
