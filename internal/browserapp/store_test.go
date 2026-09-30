package browserapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
)

func TestStoreSelectionLifecycleAndHostIsolation(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store Store = NewMemoryStore()
			if backend == "sqlite" {
				client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{Path: filepath.Join(t.TempDir(), "browser.sqlite")})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = client.Close() })
				store, err = NewSQLiteStore(context.Background(), client)
				if err != nil {
					t.Fatal(err)
				}
			}
			if backend == "postgres" {
				databaseURL := strings.TrimSpace(os.Getenv("HECATE_POSTGRES_TEST_URL"))
				if databaseURL == "" {
					t.Skip("set HECATE_POSTGRES_TEST_URL to run Postgres browser-settings conformance")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				client, err := storage.NewPostgresClient(ctx, storage.PostgresConfig{DatabaseURL: databaseURL, TablePrefix: fmt.Sprintf("browser_test_%d", time.Now().UnixNano())})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					_, _ = client.DB().ExecContext(context.Background(), "DROP TABLE IF EXISTS "+client.QualifiedTable("browser_runtime_settings"))
					_ = client.Close()
				})
				store, err = NewPostgresStore(ctx, client)
				if err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			if _, err := store.Get(ctx, "host-a"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("initial Get = %v", err)
			}
			want := Selection{RuntimeHostID: "host-a", Name: "Chromium", Path: "/selected/browser", CanonicalPath: "/installed/browser"}
			if err := store.Put(ctx, want); err != nil {
				t.Fatal(err)
			}
			if got, err := store.Get(ctx, "host-a"); err != nil || got != want {
				t.Fatalf("Get = %#v, %v", got, err)
			}
			if _, err := store.Get(ctx, "host-b"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("cross-host Get = %v", err)
			}
			want.Name = "Updated selection"
			if err := store.Put(ctx, want); err != nil {
				t.Fatal(err)
			}
			if got, err := store.Get(ctx, "host-a"); err != nil || got != want {
				t.Fatalf("updated Get = %#v, %v", got, err)
			}
			if err := store.Delete(ctx, "host-b"); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Get(ctx, "host-a"); err != nil {
				t.Fatal("other-host delete affected selection")
			}
			if err := store.Delete(ctx, "host-a"); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Get(ctx, "host-a"); !errors.Is(err, ErrNotFound) {
				t.Fatal("delete retained selection")
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if err := store.Put(cancelled, want); err == nil {
				t.Fatal("cancelled Put succeeded")
			}
		})
	}
}

func TestSQLiteSelectionSurvivesClientRestart(t *testing.T) {
	ctx := context.Background()
	config := storage.SQLiteConfig{Path: filepath.Join(t.TempDir(), "browser.sqlite")}
	client, err := storage.NewSQLiteClient(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := fixtureService(t, store)
	enableFixture(t, s)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	client, err = storage.NewSQLiteClient(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	store, err = NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(Options{RuntimeHostID: "runtime-a", Store: store})
	if ready := restarted.Readiness(ctx); !ready.Available || ready.Status != "configured" {
		t.Fatalf("restarted readiness = %#v", ready)
	}
}
