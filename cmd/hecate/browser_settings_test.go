package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/hecatehq/hecate/internal/browserapp"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/storage"
)

func TestBrowserSettingsStoreUsesControlPlaneBackend(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			var client *storage.SQLiteClient
			if backend == "sqlite" {
				var err error
				client, err = storage.NewSQLiteClient(ctx, storage.SQLiteConfig{Path: filepath.Join(t.TempDir(), "runtime.sqlite")})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = client.Close() })
			}
			cfg := config.Config{Server: config.ServerConfig{ControlPlaneBackend: backend}}
			store := buildBrowserSettingsStore(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), client, nil)
			if store.Backend() != backend {
				t.Fatalf("backend = %s want %s", store.Backend(), backend)
			}
			want := browserapp.Selection{RuntimeHostID: "host", Name: "Browser", Path: "/browser", CanonicalPath: "/browser"}
			if err := store.Put(ctx, want); err != nil {
				t.Fatal(err)
			}
			if got, err := store.Get(ctx, "host"); err != nil || got != want {
				t.Fatalf("Get = %+v, %v", got, err)
			}
		})
	}
}
