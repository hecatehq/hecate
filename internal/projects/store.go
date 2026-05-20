package projects

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound = errors.New("project not found")
	ErrInvalid  = errors.New("invalid project")
)

type Project struct {
	ID                       string
	Name                     string
	Description              string
	Roots                    []Root
	DefaultRootID            string
	DefaultProvider          string
	DefaultModel             string
	DefaultAgentProfile      string
	DefaultToolsEnabled      *bool
	DefaultWorkspaceMode     string
	DefaultSystemPrompt      string
	DefaultCompactToolOutput *bool
	CreatedAt                time.Time
	UpdatedAt                time.Time
	LastOpenedAt             time.Time
}

type Root struct {
	ID        string
	Path      string
	Kind      string
	GitRemote string
	GitBranch string
	Active    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Store interface {
	Backend() string
	Create(ctx context.Context, project Project) (Project, error)
	Get(ctx context.Context, id string) (Project, bool, error)
	List(ctx context.Context) ([]Project, error)
	Update(ctx context.Context, id string, update func(*Project)) (Project, error)
	Delete(ctx context.Context, id string) error
}

type MemoryStore struct {
	mu       sync.Mutex
	projects map[string]Project
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{projects: make(map[string]Project)}
}

func (s *MemoryStore) Backend() string {
	return "memory"
}

func (s *MemoryStore) Create(_ context.Context, project Project) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	project = normalizeProject(project, time.Now().UTC())
	if err := validateProject(project); err != nil {
		return Project{}, err
	}
	s.projects[project.ID] = cloneProject(project)
	return cloneProject(project), nil
}

func (s *MemoryStore) Get(_ context.Context, id string) (Project, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	project, ok := s.projects[strings.TrimSpace(id)]
	if !ok {
		return Project{}, false, nil
	}
	return cloneProject(project), true, nil
}

func (s *MemoryStore) List(_ context.Context) ([]Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Project, 0, len(s.projects))
	for _, item := range s.projects {
		items = append(items, cloneProject(item))
	}
	sortProjects(items)
	return items, nil
}

func (s *MemoryStore) Update(_ context.Context, id string, update func(*Project)) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = strings.TrimSpace(id)
	project, ok := s.projects[id]
	if !ok {
		return Project{}, ErrNotFound
	}
	project = cloneProject(project)
	if update != nil {
		update(&project)
	}
	project.UpdatedAt = time.Now().UTC()
	project = normalizeProject(project, project.UpdatedAt)
	if err := validateProject(project); err != nil {
		return Project{}, err
	}
	s.projects[id] = cloneProject(project)
	return cloneProject(project), nil
}

func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = strings.TrimSpace(id)
	if _, ok := s.projects[id]; !ok {
		return ErrNotFound
	}
	delete(s.projects, id)
	return nil
}

func normalizeProject(project Project, now time.Time) Project {
	project.ID = strings.TrimSpace(project.ID)
	project.Name = strings.TrimSpace(project.Name)
	project.Description = strings.TrimSpace(project.Description)
	project.DefaultRootID = strings.TrimSpace(project.DefaultRootID)
	project.DefaultProvider = strings.TrimSpace(project.DefaultProvider)
	project.DefaultModel = strings.TrimSpace(project.DefaultModel)
	project.DefaultAgentProfile = strings.TrimSpace(project.DefaultAgentProfile)
	project.DefaultWorkspaceMode = strings.TrimSpace(project.DefaultWorkspaceMode)
	project.DefaultSystemPrompt = strings.TrimSpace(project.DefaultSystemPrompt)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if project.CreatedAt.IsZero() {
		project.CreatedAt = now
	}
	if project.UpdatedAt.IsZero() {
		project.UpdatedAt = project.CreatedAt
	}
	if project.LastOpenedAt.IsZero() {
		project.LastOpenedAt = project.UpdatedAt
	}
	for idx := range project.Roots {
		project.Roots[idx] = normalizeRoot(project.Roots[idx], now)
	}
	if len(project.Roots) == 0 {
		project.DefaultRootID = ""
	} else if project.DefaultRootID == "" {
		project.DefaultRootID = project.Roots[0].ID
	}
	return project
}

func normalizeRoot(root Root, now time.Time) Root {
	root.ID = strings.TrimSpace(root.ID)
	root.Path = strings.TrimSpace(root.Path)
	root.Kind = strings.TrimSpace(root.Kind)
	root.GitRemote = strings.TrimSpace(root.GitRemote)
	root.GitBranch = strings.TrimSpace(root.GitBranch)
	if root.Kind == "" {
		root.Kind = "local"
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if root.CreatedAt.IsZero() {
		root.CreatedAt = now
	}
	if root.UpdatedAt.IsZero() {
		root.UpdatedAt = root.CreatedAt
	}
	return root
}

func validateProject(project Project) error {
	if project.ID == "" {
		return fmt.Errorf("%w: project id is required", ErrInvalid)
	}
	if project.Name == "" {
		return fmt.Errorf("%w: project name is required", ErrInvalid)
	}
	rootIDs := make(map[string]struct{}, len(project.Roots))
	for _, root := range project.Roots {
		if root.ID == "" {
			return fmt.Errorf("%w: project root id is required", ErrInvalid)
		}
		if root.Path == "" {
			return fmt.Errorf("%w: project root path is required", ErrInvalid)
		}
		if _, exists := rootIDs[root.ID]; exists {
			return fmt.Errorf("%w: duplicate project root id %q", ErrInvalid, root.ID)
		}
		rootIDs[root.ID] = struct{}{}
	}
	if project.DefaultRootID != "" {
		if _, ok := rootIDs[project.DefaultRootID]; !ok {
			return fmt.Errorf("%w: default_root_id %q does not match a project root", ErrInvalid, project.DefaultRootID)
		}
	}
	return nil
}

func hasRootID(roots []Root, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	for _, root := range roots {
		if root.ID == id {
			return true
		}
	}
	return false
}

func cloneProject(project Project) Project {
	project.Roots = append([]Root(nil), project.Roots...)
	project.DefaultToolsEnabled = cloneBoolPtr(project.DefaultToolsEnabled)
	project.DefaultCompactToolOutput = cloneBoolPtr(project.DefaultCompactToolOutput)
	return project
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func sortProjects(items []Project) {
	sort.SliceStable(items, func(i, j int) bool {
		left := projectSortTime(items[i])
		right := projectSortTime(items[j])
		if !left.Equal(right) {
			return left.After(right)
		}
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].ID < items[j].ID
	})
}

func projectSortTime(project Project) time.Time {
	if !project.LastOpenedAt.IsZero() {
		return project.LastOpenedAt
	}
	if !project.UpdatedAt.IsZero() {
		return project.UpdatedAt
	}
	return project.CreatedAt
}
