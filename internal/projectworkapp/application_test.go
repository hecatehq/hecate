package projectworkapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

func newTestApplication(store projectwork.Store) *Application {
	return New(Options{
		Store:       store,
		IDGenerator: func(prefix string) string { return prefix + "_fixed" },
	})
}

func TestApplication_CreateRoleGeneratesIDAndCopiesSkills(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := projectwork.NewMemoryStore()
	app := newTestApplication(store)
	skills := []string{"backend", "ui"}

	role, err := app.CreateRole(ctx, "proj_1", CreateRoleCommand{
		Name:     "Builder",
		SkillIDs: skills,
	})
	if err != nil {
		t.Fatalf("CreateRole() error = %v", err)
	}
	if role.ID != "role_fixed" || role.ProjectID != "proj_1" || role.Name != "Builder" {
		t.Fatalf("role = %+v, want generated id, project, and name", role)
	}
	skills[0] = "mutated"
	if role.SkillIDs[0] != "backend" {
		t.Fatalf("role skills mutated through command slice: %+v", role.SkillIDs)
	}
}

func TestApplication_CreateAssignmentUsesRoleDefaultDriver(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := projectwork.NewMemoryStore()
	app := newTestApplication(store)
	if _, err := app.CreateWorkItem(ctx, "proj_1", CreateWorkItemCommand{ID: "work_1", Title: "Build"}); err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}
	if _, err := app.CreateRole(ctx, "proj_1", CreateRoleCommand{
		ID:                "role_ext",
		Name:              "External",
		DefaultDriverKind: projectwork.AssignmentDriverExternalAgent,
	}); err != nil {
		t.Fatalf("CreateRole() error = %v", err)
	}

	assignment, err := app.CreateAssignment(ctx, "proj_1", "work_1", CreateAssignmentCommand{
		RoleID: "role_ext",
	})
	if err != nil {
		t.Fatalf("CreateAssignment() error = %v", err)
	}
	if assignment.ID != "asgn_fixed" || assignment.DriverKind != projectwork.AssignmentDriverExternalAgent {
		t.Fatalf("assignment = %+v, want generated id and role default driver", assignment)
	}
}

func TestApplication_UpdateAssignmentAppliesOptionalFields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := projectwork.NewMemoryStore()
	app := newTestApplication(store)
	if _, err := app.CreateWorkItem(ctx, "proj_1", CreateWorkItemCommand{ID: "work_1", Title: "Build"}); err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}
	assignment, err := app.CreateAssignment(ctx, "proj_1", "work_1", CreateAssignmentCommand{
		ID:     "asgn_1",
		RoleID: "software_developer",
	})
	if err != nil {
		t.Fatalf("CreateAssignment() error = %v", err)
	}

	status := projectwork.AssignmentStatusRunning
	runID := "run_1"
	startedAt := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)
	updated, err := app.UpdateAssignment(ctx, "proj_1", assignment.ID, UpdateAssignmentCommand{
		Status:    &status,
		RunID:     &runID,
		StartedAt: &startedAt,
	})
	if err != nil {
		t.Fatalf("UpdateAssignment() error = %v", err)
	}
	if updated.Status != status || updated.RunID != runID || !updated.StartedAt.Equal(startedAt) {
		t.Fatalf("updated assignment = %+v, want optional fields applied", updated)
	}
}

func TestApplication_NilStore(t *testing.T) {
	t.Parallel()

	app := New(Options{})
	if _, err := app.CreateRole(context.Background(), "proj", CreateRoleCommand{Name: "Role"}); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("CreateRole(nil store) error = %v, want ErrStoreNotConfigured", err)
	}
	if err := app.DeleteAssignment(context.Background(), "proj", "work", "asgn"); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("DeleteAssignment(nil store) error = %v, want ErrStoreNotConfigured", err)
	}
}

type recordingTaskRunner struct {
	calls int
	err   error
}

func (r *recordingTaskRunner) StartTaskWithRunInitializer(_ context.Context, task types.Task, _ func(string) string, init func(*types.TaskRun)) (*orchestrator.StartTaskResult, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	run := types.TaskRun{ID: "run_started", TaskID: task.ID, Status: "queued"}
	if init != nil {
		init(&run)
	}
	return &orchestrator.StartTaskResult{
		Task:    task,
		Run:     run,
		TraceID: "trace-start",
		SpanID:  "span-start",
	}, nil
}

func newStartTestApplication(workStore projectwork.Store, taskStore taskstate.Store, runner TaskRunner) *Application {
	return New(Options{
		Store:       workStore,
		TaskStore:   taskStore,
		Runner:      runner,
		IDGenerator: func(prefix string) string { return prefix + "_fixed" },
		Now: func() time.Time {
			return time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
		},
	})
}

func seedStartTestAssignment(t *testing.T, ctx context.Context, app *Application) projectwork.Assignment {
	t.Helper()
	if _, err := app.CreateWorkItem(ctx, "proj_1", CreateWorkItemCommand{ID: "work_1", Title: "Build"}); err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}
	assignment, err := app.CreateAssignment(ctx, "proj_1", "work_1", CreateAssignmentCommand{
		ID:     "asgn_1",
		RoleID: "software_developer",
	})
	if err != nil {
		t.Fatalf("CreateAssignment() error = %v", err)
	}
	return assignment
}

func TestApplication_StartTaskAssignmentCreatesTaskAndLinksRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workStore := projectwork.NewMemoryStore()
	taskStore := taskstate.NewMemoryStore()
	runner := &recordingTaskRunner{}
	app := newStartTestApplication(workStore, taskStore, runner)
	assignment := seedStartTestAssignment(t, ctx, app)
	var initializedRunID string

	result, err := app.StartTaskAssignment(ctx, StartTaskAssignmentCommand{
		ProjectID:         "proj_1",
		WorkItemID:        "work_1",
		Assignment:        assignment,
		ContextSnapshotID: "ctx_1",
		BuildTask: func(taskID string) (types.Task, error) {
			return types.Task{ID: taskID, Title: "Build", Status: "queued"}, nil
		},
		InitializeRun: func(_ types.Task, run *types.TaskRun) {
			initializedRunID = run.ID
		},
	})
	if err != nil {
		t.Fatalf("StartTaskAssignment() error = %v", err)
	}
	if runner.calls != 1 || initializedRunID != "run_started" {
		t.Fatalf("runner calls=%d initializedRunID=%q, want one initialized run", runner.calls, initializedRunID)
	}
	if result.Task.ID != "task_fixed" || result.Run.ID != "run_started" || result.TraceID != "trace-start" {
		t.Fatalf("result = %+v, want task/run/trace from runner", result)
	}
	if result.Assignment.TaskID != result.Task.ID || result.Assignment.RunID != result.Run.ID || result.Assignment.ContextSnapshotID != "ctx_1" {
		t.Fatalf("linked assignment = %+v, want task/run/context links", result.Assignment)
	}
	if _, ok, err := taskStore.GetTask(ctx, result.Task.ID); err != nil || !ok {
		t.Fatalf("created task lookup ok=%v err=%v, want persisted task", ok, err)
	}
}

func TestApplication_StartTaskAssignmentRejectsActiveAssignmentBeforeRunner(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workStore := projectwork.NewMemoryStore()
	taskStore := taskstate.NewMemoryStore()
	runner := &recordingTaskRunner{}
	app := newStartTestApplication(workStore, taskStore, runner)
	assignment := seedStartTestAssignment(t, ctx, app)
	assignment, err := workStore.UpdateAssignment(ctx, "proj_1", assignment.ID, func(item *projectwork.Assignment) {
		item.TaskID = "task_existing"
		item.Status = projectwork.AssignmentStatusQueued
	})
	if err != nil {
		t.Fatalf("UpdateAssignment() error = %v", err)
	}

	_, err = app.StartTaskAssignment(ctx, StartTaskAssignmentCommand{
		ProjectID:  "proj_1",
		WorkItemID: "work_1",
		Assignment: assignment,
		BuildTask: func(taskID string) (types.Task, error) {
			return types.Task{ID: taskID}, nil
		},
	})
	if !errors.Is(err, ErrAssignmentStartConflict) {
		t.Fatalf("StartTaskAssignment(active) error = %v, want ErrAssignmentStartConflict", err)
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls = %d, want 0 before conflict", runner.calls)
	}
}

func TestApplication_StartTaskAssignmentBuildFailureClearsClaim(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workStore := projectwork.NewMemoryStore()
	taskStore := taskstate.NewMemoryStore()
	runner := &recordingTaskRunner{}
	app := newStartTestApplication(workStore, taskStore, runner)
	assignment := seedStartTestAssignment(t, ctx, app)

	_, err := app.StartTaskAssignment(ctx, StartTaskAssignmentCommand{
		ProjectID:  "proj_1",
		WorkItemID: "work_1",
		Assignment: assignment,
		BuildTask: func(string) (types.Task, error) {
			return types.Task{}, fmt.Errorf("build task failed")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "build task failed") {
		t.Fatalf("StartTaskAssignment(build failure) error = %v, want build task failed", err)
	}
	items, err := workStore.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: "proj_1", WorkItemID: "work_1"})
	if err != nil {
		t.Fatalf("ListAssignments() error = %v", err)
	}
	got := items[0]
	if got.TaskID != "" || got.RunID != "" || got.Status != projectwork.AssignmentStatusQueued || !got.StartedAt.IsZero() {
		t.Fatalf("assignment after build failure = %+v, want cleared queued claim", got)
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls = %d, want 0 on build failure", runner.calls)
	}
}

func TestApplication_StartTaskAssignmentRunnerFailureMarksFailed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workStore := projectwork.NewMemoryStore()
	taskStore := taskstate.NewMemoryStore()
	runner := &recordingTaskRunner{err: fmt.Errorf("runner boom")}
	app := newStartTestApplication(workStore, taskStore, runner)
	assignment := seedStartTestAssignment(t, ctx, app)

	result, err := app.StartTaskAssignment(ctx, StartTaskAssignmentCommand{
		ProjectID:  "proj_1",
		WorkItemID: "work_1",
		Assignment: assignment,
		BuildTask: func(taskID string) (types.Task, error) {
			return types.Task{ID: taskID, Title: "Build", Status: "queued"}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "runner boom") {
		t.Fatalf("StartTaskAssignment(runner failure) error = %v, want runner boom", err)
	}
	if result == nil || result.Task.ID != "task_fixed" {
		t.Fatalf("result = %+v, want created task on runner failure", result)
	}
	if result.Assignment.Status != projectwork.AssignmentStatusFailed || result.Assignment.TaskID != "task_fixed" || result.Assignment.CompletedAt.IsZero() {
		t.Fatalf("assignment after runner failure = %+v, want failed linked task", result.Assignment)
	}
}
