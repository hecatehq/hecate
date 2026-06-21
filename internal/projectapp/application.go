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
	ErrProjectStoreNotConfigured    = errors.New("project store is not configured")
	ErrProjectNotFound              = errors.New("project not found")
	ErrProjectDeleteConflict        = errors.New("project delete conflicts with current state")
	ErrProjectContextSourceNotFound = errors.New("project context source not found")
	ErrProjectContextSourceConflict = errors.New("project context source conflict")
	ErrChatDeleteNotConfigured      = errors.New("project chat delete authority is not configured")
)

type ProjectStore interface {
	Get(ctx context.Context, id string) (projects.Project, bool, error)
	Update(ctx context.Context, id string, update func(*projects.Project)) (projects.Project, error)
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

func (app *Application) CreateContextSource(ctx context.Context, projectID string, source projects.ContextSource) (projects.Project, projects.ContextSource, error) {
	if app == nil || app.projects == nil {
		return projects.Project{}, projects.ContextSource{}, ErrProjectStoreNotConfigured
	}
	projectID = strings.TrimSpace(projectID)
	source.ID = strings.TrimSpace(source.ID)
	if source.ID == "" {
		return projects.Project{}, projects.ContextSource{}, fmt.Errorf("%w: context source id is required", projects.ErrInvalid)
	}
	project, ok, err := app.projects.Get(ctx, projectID)
	if err != nil {
		return projects.Project{}, projects.ContextSource{}, err
	}
	if !ok {
		return projects.Project{}, projects.ContextSource{}, ErrProjectNotFound
	}
	if projectContextSourceExists(project.ContextSources, source.ID) {
		return projects.Project{}, projects.ContextSource{}, fmt.Errorf("%w: context source %q already exists", ErrProjectContextSourceConflict, source.ID)
	}
	updated, err := app.projects.Update(ctx, projectID, func(item *projects.Project) {
		item.ContextSources = append(append([]projects.ContextSource(nil), item.ContextSources...), source)
	})
	if err != nil {
		return projects.Project{}, projects.ContextSource{}, mapProjectStoreError(err)
	}
	created, ok := findProjectContextSource(updated.ContextSources, source.ID)
	if !ok {
		return projects.Project{}, projects.ContextSource{}, ErrProjectContextSourceNotFound
	}
	return updated, created, nil
}

func (app *Application) UpdateContextSource(ctx context.Context, projectID, sourceID string, source projects.ContextSource) (projects.Project, projects.ContextSource, error) {
	if app == nil || app.projects == nil {
		return projects.Project{}, projects.ContextSource{}, ErrProjectStoreNotConfigured
	}
	projectID = strings.TrimSpace(projectID)
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return projects.Project{}, projects.ContextSource{}, ErrProjectContextSourceNotFound
	}
	project, ok, err := app.projects.Get(ctx, projectID)
	if err != nil {
		return projects.Project{}, projects.ContextSource{}, err
	}
	if !ok {
		return projects.Project{}, projects.ContextSource{}, ErrProjectNotFound
	}
	if _, ok := findProjectContextSource(project.ContextSources, sourceID); !ok {
		return projects.Project{}, projects.ContextSource{}, ErrProjectContextSourceNotFound
	}
	source.ID = sourceID
	updated, err := app.projects.Update(ctx, projectID, func(item *projects.Project) {
		for idx := range item.ContextSources {
			if strings.TrimSpace(item.ContextSources[idx].ID) == sourceID {
				item.ContextSources[idx] = source
				return
			}
		}
	})
	if err != nil {
		return projects.Project{}, projects.ContextSource{}, mapProjectStoreError(err)
	}
	next, ok := findProjectContextSource(updated.ContextSources, sourceID)
	if !ok {
		return projects.Project{}, projects.ContextSource{}, ErrProjectContextSourceNotFound
	}
	return updated, next, nil
}

func (app *Application) DeleteContextSource(ctx context.Context, projectID, sourceID string) (projects.Project, projects.ContextSource, error) {
	if app == nil || app.projects == nil {
		return projects.Project{}, projects.ContextSource{}, ErrProjectStoreNotConfigured
	}
	projectID = strings.TrimSpace(projectID)
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return projects.Project{}, projects.ContextSource{}, ErrProjectContextSourceNotFound
	}
	project, ok, err := app.projects.Get(ctx, projectID)
	if err != nil {
		return projects.Project{}, projects.ContextSource{}, err
	}
	if !ok {
		return projects.Project{}, projects.ContextSource{}, ErrProjectNotFound
	}
	deleted, ok := findProjectContextSource(project.ContextSources, sourceID)
	if !ok {
		return projects.Project{}, projects.ContextSource{}, ErrProjectContextSourceNotFound
	}
	updated, err := app.projects.Update(ctx, projectID, func(item *projects.Project) {
		next := item.ContextSources[:0]
		for _, source := range item.ContextSources {
			if strings.TrimSpace(source.ID) == sourceID {
				continue
			}
			next = append(next, source)
		}
		item.ContextSources = append([]projects.ContextSource(nil), next...)
	})
	if err != nil {
		return projects.Project{}, projects.ContextSource{}, mapProjectStoreError(err)
	}
	return updated, deleted, nil
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

func findProjectContextSource(sources []projects.ContextSource, id string) (projects.ContextSource, bool) {
	id = strings.TrimSpace(id)
	for _, source := range sources {
		if strings.TrimSpace(source.ID) == id {
			return source, true
		}
	}
	return projects.ContextSource{}, false
}

func projectContextSourceExists(sources []projects.ContextSource, id string) bool {
	_, ok := findProjectContextSource(sources, id)
	return ok
}

func mapProjectStoreError(err error) error {
	switch {
	case errors.Is(err, projects.ErrNotFound):
		return ErrProjectNotFound
	default:
		return err
	}
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
