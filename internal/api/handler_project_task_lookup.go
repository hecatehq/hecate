package api

import (
	"context"
	"errors"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/taskapp"
)

func (h *Handler) taskProjectStore() taskapp.ProjectStore {
	if h != nil && h.config.ProjectsUseCairnlineOnly() {
		return cairnlineTaskProjectStore{handler: h}
	}
	if h == nil {
		return nil
	}
	return h.projects
}

type cairnlineTaskProjectStore struct {
	handler *Handler
}

func (store cairnlineTaskProjectStore) Get(ctx context.Context, projectID string) (projects.Project, bool, error) {
	if store.handler == nil {
		return projects.Project{}, false, nil
	}
	project, err := store.handler.projectFromEmbeddedCairnlineWriteAuthority(ctx, projectID)
	if errors.Is(err, cairnline.ErrNotFound) {
		return projects.Project{}, false, nil
	}
	if err != nil {
		return projects.Project{}, false, err
	}
	return project, true, nil
}
