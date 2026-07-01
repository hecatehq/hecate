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

type Store interface {
	Backend() string
	Get(ctx context.Context, projectID, assignmentID string) (AssignmentRuntime, bool, error)
	Upsert(ctx context.Context, runtime AssignmentRuntime) (AssignmentRuntime, error)
	Delete(ctx context.Context, projectID, assignmentID string) error
	DeleteProject(ctx context.Context, projectID string) (int, error)
	Clear(ctx context.Context) (int, error)
}

type MemoryStore struct {
	mu       sync.Mutex
	runtimes map[string]AssignmentRuntime
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{runtimes: make(map[string]AssignmentRuntime)}
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
	return deleted, nil
}

func (s *MemoryStore) Clear(context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := len(s.runtimes)
	s.runtimes = make(map[string]AssignmentRuntime)
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

func cloneRuntime(runtime AssignmentRuntime) AssignmentRuntime {
	runtime.ContextPacket = append([]byte(nil), runtime.ContextPacket...)
	return runtime
}

func runtimeKey(projectID, assignmentID string) string {
	return strings.TrimSpace(projectID) + "\x00" + strings.TrimSpace(assignmentID)
}

func normalizeTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.UTC()
}
