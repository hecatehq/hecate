package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
)

func (h *Handler) openCairnlineEmbeddedService(ctx context.Context) (string, *cairnline.Service, *cairnline.SQLiteStore, error) {
	dbPath := h.cairnlineEmbeddedDatabasePath()
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil, nil, errors.Join(cairnline.ErrNotFound, errors.New("embedded Cairnline database not found"))
		}
		return "", nil, nil, err
	}
	service, store, err := cairnline.NewSQLiteService(ctx, dbPath)
	if err != nil {
		return "", nil, nil, err
	}
	return dbPath, service, store, nil
}

func (h *Handler) cairnlineSnapshotSources() cairnlinebridge.SnapshotSources {
	return cairnlinebridge.SnapshotSources{
		Projects:         h.projects,
		Skills:           h.projectSkills,
		Work:             h.projectWork,
		Memory:           h.memory,
		MemoryCandidates: h.memoryCandidates,
		Proposals:        h.projectAssistantProposals,
	}
}

func (h *Handler) cairnlineEmbeddedDatabasePath() string {
	dataDir := strings.TrimSpace(h.config.Server.DataDir)
	if dataDir == "" {
		dataDir = ".data"
	}
	path := filepath.Join(dataDir, "cairnline", "embedded", "projects.db")
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return path
}
