package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/hecatehq/hecate/internal/browserapp"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/storage"
)

func buildBrowserSettingsStore(cfg config.Config, logger *slog.Logger, sqliteClient *storage.SQLiteClient, postgresClient *storage.PostgresClient) browserapp.Store {
	var store browserapp.Store
	var err error
	switch cfg.Server.ControlPlaneBackend {
	case "sqlite":
		store, err = browserapp.NewSQLiteStore(context.Background(), sqliteClient)
	case "postgres":
		store, err = browserapp.NewPostgresStore(context.Background(), postgresClient)
	default:
		return browserapp.NewMemoryStore()
	}
	if err != nil {
		logger.Error("browser settings store init failed")
		os.Exit(1)
	}
	return store
}
