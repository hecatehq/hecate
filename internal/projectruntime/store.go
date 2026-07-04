package projectruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/projectwork"
)

var (
	ErrNotFound = errors.New("project assignment runtime not found")
	ErrInvalid  = errors.New("invalid project assignment runtime")
)

type AssignmentRuntime struct {
	ProjectID     string
	AssignmentID  string
	ExecutionRef  projectwork.AssignmentExecutionRef
	ContextPacket []byte
	StartedAt     time.Time
	CompletedAt   time.Time
	UpdatedAt     time.Time
}

type ProjectDefaults struct {
	ProjectID                string
	DefaultProvider          string
	DefaultModel             string
	DefaultAgentProfile      string
	DefaultToolsEnabled      *bool
	DefaultWorkspaceMode     string
	DefaultSystemPrompt      string
	DefaultCompactToolOutput *bool
	UpdatedAt                time.Time
}

type RoleDefaults struct {
	ProjectID           string
	RoleID              string
	DefaultProvider     string
	DefaultModel        string
	DefaultAgentProfile string
	UpdatedAt           time.Time
}

type Store interface {
	Backend() string
	Get(ctx context.Context, projectID, assignmentID string) (AssignmentRuntime, bool, error)
	Upsert(ctx context.Context, runtime AssignmentRuntime) (AssignmentRuntime, error)
	Delete(ctx context.Context, projectID, assignmentID string) error
	GetProjectDefaults(ctx context.Context, projectID string) (ProjectDefaults, bool, error)
	UpsertProjectDefaults(ctx context.Context, defaults ProjectDefaults) (ProjectDefaults, error)
	DeleteProjectDefaults(ctx context.Context, projectID string) error
	GetRoleDefaults(ctx context.Context, projectID, roleID string) (RoleDefaults, bool, error)
	UpsertRoleDefaults(ctx context.Context, defaults RoleDefaults) (RoleDefaults, error)
	DeleteRoleDefaults(ctx context.Context, projectID, roleID string) error
	DeleteProject(ctx context.Context, projectID string) (int, error)
	Clear(ctx context.Context) (int, error)
}

type MemoryStore struct {
	mu              sync.Mutex
	runtimes        map[string]AssignmentRuntime
	projectDefaults map[string]ProjectDefaults
	roleDefaults    map[string]RoleDefaults
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		runtimes:        make(map[string]AssignmentRuntime),
		projectDefaults: make(map[string]ProjectDefaults),
		roleDefaults:    make(map[string]RoleDefaults),
	}
}

func (s *MemoryStore) Backend() string { return "memory" }

func (s *MemoryStore) Get(_ context.Context, projectID, assignmentID string) (AssignmentRuntime, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, ok := s.runtimes[runtimeKey(projectID, assignmentID)]
	if !ok {
		return AssignmentRuntime{}, false, nil
	}
	return cloneRuntime(runtime), true, nil
}

func (s *MemoryStore) Upsert(_ context.Context, runtime AssignmentRuntime) (AssignmentRuntime, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime = normalizeRuntime(runtime, time.Now().UTC())
	if err := validateRuntime(runtime); err != nil {
		return AssignmentRuntime{}, err
	}
	s.runtimes[runtimeKey(runtime.ProjectID, runtime.AssignmentID)] = cloneRuntime(runtime)
	return cloneRuntime(runtime), nil
}

func (s *MemoryStore) Delete(_ context.Context, projectID, assignmentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := runtimeKey(projectID, assignmentID)
	if _, ok := s.runtimes[key]; !ok {
		return ErrNotFound
	}
	delete(s.runtimes, key)
	return nil
}

func (s *MemoryStore) GetProjectDefaults(_ context.Context, projectID string) (ProjectDefaults, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defaults, ok := s.projectDefaults[projectDefaultsKey(projectID)]
	if !ok {
		return ProjectDefaults{}, false, nil
	}
	return cloneProjectDefaults(defaults), true, nil
}

func (s *MemoryStore) UpsertProjectDefaults(_ context.Context, defaults ProjectDefaults) (ProjectDefaults, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defaults = normalizeProjectDefaults(defaults, time.Now().UTC())
	if err := validateProjectDefaults(defaults); err != nil {
		return ProjectDefaults{}, err
	}
	s.projectDefaults[projectDefaultsKey(defaults.ProjectID)] = cloneProjectDefaults(defaults)
	return cloneProjectDefaults(defaults), nil
}

func (s *MemoryStore) DeleteProjectDefaults(_ context.Context, projectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := projectDefaultsKey(projectID)
	if _, ok := s.projectDefaults[key]; !ok {
		return ErrNotFound
	}
	delete(s.projectDefaults, key)
	return nil
}

func (s *MemoryStore) GetRoleDefaults(_ context.Context, projectID, roleID string) (RoleDefaults, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defaults, ok := s.roleDefaults[roleDefaultsKey(projectID, roleID)]
	if !ok {
		return RoleDefaults{}, false, nil
	}
	return cloneRoleDefaults(defaults), true, nil
}

func (s *MemoryStore) UpsertRoleDefaults(_ context.Context, defaults RoleDefaults) (RoleDefaults, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defaults = normalizeRoleDefaults(defaults, time.Now().UTC())
	if err := validateRoleDefaults(defaults); err != nil {
		return RoleDefaults{}, err
	}
	s.roleDefaults[roleDefaultsKey(defaults.ProjectID, defaults.RoleID)] = cloneRoleDefaults(defaults)
	return cloneRoleDefaults(defaults), nil
}

func (s *MemoryStore) DeleteRoleDefaults(_ context.Context, projectID, roleID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := roleDefaultsKey(projectID, roleID)
	if _, ok := s.roleDefaults[key]; !ok {
		return ErrNotFound
	}
	delete(s.roleDefaults, key)
	return nil
}

func (s *MemoryStore) DeleteProject(_ context.Context, projectID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projectID = strings.TrimSpace(projectID)
	deleted := 0
	for key, runtime := range s.runtimes {
		if runtime.ProjectID != projectID {
			continue
		}
		delete(s.runtimes, key)
		deleted++
	}
	if _, ok := s.projectDefaults[projectDefaultsKey(projectID)]; ok {
		delete(s.projectDefaults, projectDefaultsKey(projectID))
		deleted++
	}
	for key, defaults := range s.roleDefaults {
		if defaults.ProjectID != projectID {
			continue
		}
		delete(s.roleDefaults, key)
		deleted++
	}
	return deleted, nil
}

func (s *MemoryStore) Clear(context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := len(s.runtimes) + len(s.projectDefaults) + len(s.roleDefaults)
	s.runtimes = make(map[string]AssignmentRuntime)
	s.projectDefaults = make(map[string]ProjectDefaults)
	s.roleDefaults = make(map[string]RoleDefaults)
	return deleted, nil
}

func FromAssignment(assignment projectwork.Assignment) AssignmentRuntime {
	return AssignmentRuntime{
		ProjectID:     assignment.ProjectID,
		AssignmentID:  assignment.ID,
		ExecutionRef:  projectwork.NormalizeAssignmentExecutionRef(assignment.ExecutionRef),
		ContextPacket: append([]byte(nil), assignment.ContextPacket...),
		StartedAt:     assignment.StartedAt,
		CompletedAt:   assignment.CompletedAt,
		UpdatedAt:     assignment.UpdatedAt,
	}
}

func Apply(assignment projectwork.Assignment, runtime AssignmentRuntime) projectwork.Assignment {
	if strings.TrimSpace(runtime.ProjectID) == "" || strings.TrimSpace(runtime.AssignmentID) == "" {
		return assignment
	}
	if strings.TrimSpace(runtime.ProjectID) != strings.TrimSpace(assignment.ProjectID) ||
		strings.TrimSpace(runtime.AssignmentID) != strings.TrimSpace(assignment.ID) {
		return assignment
	}
	assignment.ExecutionRef = projectwork.NormalizeAssignmentExecutionRef(runtime.ExecutionRef)
	assignment.ContextPacket = append([]byte(nil), runtime.ContextPacket...)
	assignment.StartedAt = runtime.StartedAt
	assignment.CompletedAt = runtime.CompletedAt
	return assignment
}

func normalizeRuntime(runtime AssignmentRuntime, now time.Time) AssignmentRuntime {
	runtime.ProjectID = strings.TrimSpace(runtime.ProjectID)
	runtime.AssignmentID = strings.TrimSpace(runtime.AssignmentID)
	runtime.ExecutionRef = projectwork.NormalizeAssignmentExecutionRef(runtime.ExecutionRef)
	runtime.ContextPacket = append([]byte(nil), runtime.ContextPacket...)
	runtime.StartedAt = normalizeTime(runtime.StartedAt)
	runtime.CompletedAt = normalizeTime(runtime.CompletedAt)
	runtime.UpdatedAt = normalizeTime(runtime.UpdatedAt)
	if runtime.UpdatedAt.IsZero() {
		runtime.UpdatedAt = normalizeTime(now)
	}
	return runtime
}

func validateRuntime(runtime AssignmentRuntime) error {
	if strings.TrimSpace(runtime.ProjectID) == "" {
		return fmt.Errorf("%w: project_id is required", ErrInvalid)
	}
	if strings.TrimSpace(runtime.AssignmentID) == "" {
		return fmt.Errorf("%w: assignment_id is required", ErrInvalid)
	}
	return nil
}

func normalizeProjectDefaults(defaults ProjectDefaults, now time.Time) ProjectDefaults {
	defaults.ProjectID = strings.TrimSpace(defaults.ProjectID)
	defaults.DefaultProvider = strings.TrimSpace(defaults.DefaultProvider)
	defaults.DefaultModel = strings.TrimSpace(defaults.DefaultModel)
	defaults.DefaultAgentProfile = strings.TrimSpace(defaults.DefaultAgentProfile)
	defaults.DefaultToolsEnabled = cloneBoolPtr(defaults.DefaultToolsEnabled)
	defaults.DefaultWorkspaceMode = strings.TrimSpace(defaults.DefaultWorkspaceMode)
	defaults.DefaultSystemPrompt = strings.TrimSpace(defaults.DefaultSystemPrompt)
	defaults.DefaultCompactToolOutput = cloneBoolPtr(defaults.DefaultCompactToolOutput)
	defaults.UpdatedAt = normalizeTime(defaults.UpdatedAt)
	if defaults.UpdatedAt.IsZero() {
		defaults.UpdatedAt = normalizeTime(now)
	}
	return defaults
}

func validateProjectDefaults(defaults ProjectDefaults) error {
	if strings.TrimSpace(defaults.ProjectID) == "" {
		return fmt.Errorf("%w: project_id is required", ErrInvalid)
	}
	return nil
}

func normalizeRoleDefaults(defaults RoleDefaults, now time.Time) RoleDefaults {
	defaults.ProjectID = strings.TrimSpace(defaults.ProjectID)
	defaults.RoleID = strings.TrimSpace(defaults.RoleID)
	defaults.DefaultProvider = strings.TrimSpace(defaults.DefaultProvider)
	defaults.DefaultModel = strings.TrimSpace(defaults.DefaultModel)
	defaults.DefaultAgentProfile = strings.TrimSpace(defaults.DefaultAgentProfile)
	defaults.UpdatedAt = normalizeTime(defaults.UpdatedAt)
	if defaults.UpdatedAt.IsZero() {
		defaults.UpdatedAt = normalizeTime(now)
	}
	return defaults
}

func validateRoleDefaults(defaults RoleDefaults) error {
	if strings.TrimSpace(defaults.ProjectID) == "" {
		return fmt.Errorf("%w: project_id is required", ErrInvalid)
	}
	if strings.TrimSpace(defaults.RoleID) == "" {
		return fmt.Errorf("%w: role_id is required", ErrInvalid)
	}
	return nil
}

func cloneRuntime(runtime AssignmentRuntime) AssignmentRuntime {
	runtime.ContextPacket = append([]byte(nil), runtime.ContextPacket...)
	return runtime
}

func cloneProjectDefaults(defaults ProjectDefaults) ProjectDefaults {
	defaults.DefaultToolsEnabled = cloneBoolPtr(defaults.DefaultToolsEnabled)
	defaults.DefaultCompactToolOutput = cloneBoolPtr(defaults.DefaultCompactToolOutput)
	return defaults
}

func cloneRoleDefaults(defaults RoleDefaults) RoleDefaults {
	return defaults
}

func runtimeKey(projectID, assignmentID string) string {
	return strings.TrimSpace(projectID) + "\x00" + strings.TrimSpace(assignmentID)
}

func projectDefaultsKey(projectID string) string {
	return strings.TrimSpace(projectID)
}

func roleDefaultsKey(projectID, roleID string) string {
	return strings.TrimSpace(projectID) + "\x00" + strings.TrimSpace(roleID)
}

func normalizeTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.UTC()
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
