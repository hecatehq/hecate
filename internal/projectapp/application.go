package projectapp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/projects"
)

var (
	ErrProjectStoreNotConfigured = errors.New("project store is not configured")
	ErrProjectNotFound           = errors.New("project not found")
	ErrProjectDeleteConflict     = errors.New("project delete conflicts with current state")
	ErrChatDeleteNotConfigured   = errors.New("project chat delete authority is not configured")
)

type ProjectStore interface {
	Get(ctx context.Context, id string) (projects.Project, bool, error)
	Delete(ctx context.Context, id string) error
}

type ChatSessionStore interface {
	List(ctx context.Context) ([]chat.Session, error)
}

type ChatDeleteFunc func(context.Context, chat.Session) (bool, error)

type ProjectWorkStore interface {
	DeleteProject(ctx context.Context, projectID string) (int, error)
}

type ProjectSkillStore interface {
	DeleteProject(ctx context.Context, projectID string) (int, error)
}

type MemoryStore interface {
	DeleteByProjectID(ctx context.Context, projectID string) (int, error)
}

type MemoryCandidateStore interface {
	DeleteCandidatesByProjectID(ctx context.Context, projectID string) (int, error)
}

type Application struct {
	projects         ProjectStore
	chats            ChatSessionStore
	deleteChat       ChatDeleteFunc
	projectWork      ProjectWorkStore
	projectSkills    ProjectSkillStore
	memory           MemoryStore
	memoryCandidates MemoryCandidateStore
}

type Options struct {
	Projects         ProjectStore
	Chats            ChatSessionStore
	DeleteChat       ChatDeleteFunc
	ProjectWork      ProjectWorkStore
	ProjectSkills    ProjectSkillStore
	Memory           MemoryStore
	MemoryCandidates MemoryCandidateStore
}

type DeleteProjectResult struct {
	Project                 projects.Project
	ChatSessionsDeleted     int
	ProjectWorkRowsDeleted  int
	ProjectSkillsDeleted    int
	MemoryEntriesDeleted    int
	MemoryCandidatesDeleted int
}

func New(opts Options) *Application {
	return &Application{
		projects:         opts.Projects,
		chats:            opts.Chats,
		deleteChat:       opts.DeleteChat,
		projectWork:      opts.ProjectWork,
		projectSkills:    opts.ProjectSkills,
		memory:           opts.Memory,
		memoryCandidates: opts.MemoryCandidates,
	}
}

func (app *Application) DeleteProject(ctx context.Context, id string) (DeleteProjectResult, error) {
	if app == nil || app.projects == nil {
		return DeleteProjectResult{}, ErrProjectStoreNotConfigured
	}
	projectID := strings.TrimSpace(id)
	project, ok, err := app.projects.Get(ctx, projectID)
	if err != nil {
		return DeleteProjectResult{}, err
	}
	if !ok {
		return DeleteProjectResult{}, ErrProjectNotFound
	}

	result := DeleteProjectResult{Project: project}
	if err := app.deleteProjectChats(ctx, projectID, &result); err != nil {
		return result, err
	}
	if app.projectWork != nil {
		deleted, err := app.projectWork.DeleteProject(ctx, projectID)
		if err != nil {
			return result, err
		}
		result.ProjectWorkRowsDeleted = deleted
	}
	if app.projectSkills != nil {
		deleted, err := app.projectSkills.DeleteProject(ctx, projectID)
		if err != nil {
			return result, err
		}
		result.ProjectSkillsDeleted = deleted
	}
	if app.memory != nil {
		deleted, err := app.memory.DeleteByProjectID(ctx, projectID)
		if err != nil {
			return result, err
		}
		result.MemoryEntriesDeleted = deleted
	}
	if app.memoryCandidates != nil {
		deleted, err := app.memoryCandidates.DeleteCandidatesByProjectID(ctx, projectID)
		if err != nil {
			return result, err
		}
		result.MemoryCandidatesDeleted = deleted
	}
	// Cross-store cleanup is retry-friendly rather than transactional: the
	// project identity stays durable until every scoped cleanup step succeeds.
	if err := app.projects.Delete(ctx, projectID); err != nil {
		if errors.Is(err, projects.ErrNotFound) {
			return result, ErrProjectNotFound
		}
		return result, err
	}
	return result, nil
}

func (app *Application) deleteProjectChats(ctx context.Context, projectID string, result *DeleteProjectResult) error {
	if app.chats == nil {
		return nil
	}
	sessions, err := app.chats.List(ctx)
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if strings.TrimSpace(session.ProjectID) != projectID {
			continue
		}
		if app.deleteChat == nil {
			return ErrChatDeleteNotConfigured
		}
		stopping, err := app.deleteChat(ctx, session)
		if err != nil {
			return err
		}
		if stopping {
			return fmt.Errorf("%w: chat session %q is still stopping", ErrProjectDeleteConflict, session.ID)
		}
		result.ChatSessionsDeleted++
	}
	return nil
}
