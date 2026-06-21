package projectworkapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/chat"
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

func TestApplication_UpdateWorkItemDoneRequiresCloseoutReadiness(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := projectwork.NewMemoryStore()
	app := newTestApplication(store)
	if _, err := app.CreateWorkItem(ctx, "proj_1", CreateWorkItemCommand{ID: "work_1", Title: "Build", Status: projectwork.WorkItemStatusReview}); err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}
	if _, err := app.CreateAssignment(ctx, "proj_1", "work_1", CreateAssignmentCommand{
		ID:     "asgn_1",
		RoleID: "software_developer",
		Status: projectwork.AssignmentStatusCompleted,
	}); err != nil {
		t.Fatalf("CreateAssignment() error = %v", err)
	}

	statusDone := projectwork.WorkItemStatusDone
	_, err := app.UpdateWorkItem(ctx, "proj_1", "work_1", UpdateWorkItemCommand{Status: &statusDone})
	if !errors.Is(err, ErrWorkItemCloseoutBlocked) {
		t.Fatalf("UpdateWorkItem(done) error = %v, want ErrWorkItemCloseoutBlocked", err)
	}
	var blocked WorkItemCloseoutBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("UpdateWorkItem(done) error = %T, want WorkItemCloseoutBlockedError", err)
	}
	if blocked.Readiness.Ready || len(blocked.Readiness.MissingEvidenceAssignmentIDs) != 1 || blocked.Readiness.MissingEvidenceAssignmentIDs[0] != "asgn_1" {
		t.Fatalf("blocked readiness = %+v, want missing evidence blocker", blocked.Readiness)
	}
	stored, ok, err := store.GetWorkItem(ctx, "proj_1", "work_1")
	if err != nil || !ok {
		t.Fatalf("GetWorkItem() ok=%v err=%v, want stored work item", ok, err)
	}
	if stored.Status == projectwork.WorkItemStatusDone {
		t.Fatalf("stored work item status = %q, want not done after blocked closeout", stored.Status)
	}

	priority := "high"
	updated, err := app.UpdateWorkItem(ctx, "proj_1", "work_1", UpdateWorkItemCommand{Priority: &priority})
	if err != nil {
		t.Fatalf("UpdateWorkItem(priority) error = %v", err)
	}
	if updated.Priority != priority {
		t.Fatalf("updated priority = %q, want %q", updated.Priority, priority)
	}

	if _, err := store.CreateArtifact(ctx, projectwork.CollaborationArtifact{
		ID:           "artifact_evidence",
		ProjectID:    "proj_1",
		WorkItemID:   "work_1",
		AssignmentID: "asgn_1",
		Kind:         projectwork.ArtifactKindEvidenceLink,
		Title:        "Evidence",
		Body:         "Evidence recorded.",
		EvidenceURL:  "https://example.com/evidence",
	}); err != nil {
		t.Fatalf("CreateArtifact(evidence) error = %v", err)
	}
	updated, err = app.UpdateWorkItem(ctx, "proj_1", "work_1", UpdateWorkItemCommand{Status: &statusDone})
	if err != nil {
		t.Fatalf("UpdateWorkItem(done with evidence) error = %v", err)
	}
	if updated.Status != projectwork.WorkItemStatusDone {
		t.Fatalf("updated status = %q, want done", updated.Status)
	}
}

func TestApplication_UpdateWorkItemDoneAllowsManualEmptyCloseoutAndAlreadyDone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := projectwork.NewMemoryStore()
	app := newTestApplication(store)
	statusDone := projectwork.WorkItemStatusDone
	if _, err := app.CreateWorkItem(ctx, "proj_1", CreateWorkItemCommand{ID: "work_empty", Title: "Manual closeout", Status: projectwork.WorkItemStatusReady}); err != nil {
		t.Fatalf("CreateWorkItem(empty) error = %v", err)
	}
	updated, err := app.UpdateWorkItem(ctx, "proj_1", "work_empty", UpdateWorkItemCommand{Status: &statusDone})
	if err != nil {
		t.Fatalf("UpdateWorkItem(empty done) error = %v", err)
	}
	if updated.Status != projectwork.WorkItemStatusDone {
		t.Fatalf("empty closeout status = %q, want done", updated.Status)
	}

	if _, err := app.CreateWorkItem(ctx, "proj_1", CreateWorkItemCommand{ID: "work_done", Title: "Already done", Status: projectwork.WorkItemStatusDone}); err != nil {
		t.Fatalf("CreateWorkItem(done) error = %v", err)
	}
	updated, err = app.UpdateWorkItem(ctx, "proj_1", "work_done", UpdateWorkItemCommand{Status: &statusDone})
	if err != nil {
		t.Fatalf("UpdateWorkItem(already done) error = %v", err)
	}
	if updated.Status != projectwork.WorkItemStatusDone {
		t.Fatalf("already-done status = %q, want done", updated.Status)
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
	ref := projectwork.AssignmentExecutionRef{Kind: projectwork.AssignmentExecutionKindTaskRun, RunID: "run_1", Status: status}
	startedAt := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)
	updated, err := app.UpdateAssignment(ctx, "proj_1", assignment.ID, UpdateAssignmentCommand{
		Status:       &status,
		ExecutionRef: &ref,
		StartedAt:    &startedAt,
	})
	if err != nil {
		t.Fatalf("UpdateAssignment() error = %v", err)
	}
	if updated.Status != status || updated.ExecutionRef.RunID != ref.RunID || !updated.StartedAt.Equal(startedAt) {
		t.Fatalf("updated assignment = %+v, want optional fields applied", updated)
	}
}

func TestApplication_CreateArtifactGeneratesIDAndPreservesReviewMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := projectwork.NewMemoryStore()
	app := newTestApplication(store)
	if _, err := app.CreateWorkItem(ctx, "proj_1", CreateWorkItemCommand{ID: "work_1", Title: "Build"}); err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}
	if _, err := app.CreateAssignment(ctx, "proj_1", "work_1", CreateAssignmentCommand{ID: "asgn_impl", RoleID: "software_developer"}); err != nil {
		t.Fatalf("CreateAssignment(impl) error = %v", err)
	}
	if _, err := app.CreateAssignment(ctx, "proj_1", "work_1", CreateAssignmentCommand{ID: "asgn_review", RoleID: "reviewer_qa"}); err != nil {
		t.Fatalf("CreateAssignment(review) error = %v", err)
	}

	artifact, err := app.CreateArtifact(ctx, "proj_1", "work_1", CreateArtifactCommand{
		AssignmentID:           "asgn_review",
		Kind:                   projectwork.ArtifactKindReview,
		Title:                  "Review",
		Body:                   "Changes requested.",
		AuthorRoleID:           "reviewer_qa",
		ReviewedAssignmentID:   "asgn_impl",
		ReviewVerdict:          projectwork.ReviewVerdictChangesRequested,
		ReviewRisk:             projectwork.ReviewRiskMedium,
		ReviewFollowUpRequired: true,
	})
	if err != nil {
		t.Fatalf("CreateArtifact() error = %v", err)
	}
	if artifact.ID != "art_fixed" || artifact.ProjectID != "proj_1" || artifact.WorkItemID != "work_1" {
		t.Fatalf("artifact = %+v, want generated id and scope", artifact)
	}
	if artifact.ReviewedAssignmentID != "asgn_impl" || artifact.ReviewVerdict != projectwork.ReviewVerdictChangesRequested || artifact.ReviewRisk != projectwork.ReviewRiskMedium || !artifact.ReviewFollowUpRequired {
		t.Fatalf("artifact review metadata = %+v, want review follow-up metadata", artifact)
	}
}

func TestApplication_CreateAndUpdateHandoffAppliesOptionalFields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := projectwork.NewMemoryStore()
	app := newTestApplication(store)
	if _, err := app.CreateWorkItem(ctx, "proj_1", CreateWorkItemCommand{ID: "work_1", Title: "Build"}); err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}
	if _, err := app.CreateAssignment(ctx, "proj_1", "work_1", CreateAssignmentCommand{ID: "asgn_source", RoleID: "software_developer"}); err != nil {
		t.Fatalf("CreateAssignment(source) error = %v", err)
	}
	if _, err := app.CreateAssignment(ctx, "proj_1", "work_1", CreateAssignmentCommand{ID: "asgn_target", RoleID: "reviewer_qa"}); err != nil {
		t.Fatalf("CreateAssignment(target) error = %v", err)
	}
	linkedArtifacts := []string{"artifact_1", "artifact_1"}
	contextRefs := []string{"ctx_1"}

	handoff, err := app.CreateHandoff(ctx, "proj_1", "work_1", CreateHandoffCommand{
		SourceAssignmentID:    "asgn_source",
		TargetRoleID:          "reviewer_qa",
		Title:                 "Review follow-up",
		Summary:               "Review needs follow-up.",
		RecommendedNextAction: "Queue the reviewer.",
		LinkedArtifactIDs:     linkedArtifacts,
		ContextRefs:           contextRefs,
	})
	if err != nil {
		t.Fatalf("CreateHandoff() error = %v", err)
	}
	if handoff.ID != "handoff_fixed" || handoff.ProjectID != "proj_1" || handoff.WorkItemID != "work_1" || handoff.Status != projectwork.HandoffStatusPending {
		t.Fatalf("handoff = %+v, want generated id, scope, and pending default", handoff)
	}
	linkedArtifacts[0] = "mutated"
	contextRefs[0] = "mutated"
	if handoff.LinkedArtifactIDs[0] != "artifact_1" || handoff.ContextRefs[0] != "ctx_1" {
		t.Fatalf("handoff slices mutated through command: %+v/%+v", handoff.LinkedArtifactIDs, handoff.ContextRefs)
	}

	status := projectwork.HandoffStatusAccepted
	action := "Start the target assignment."
	targetAssignmentID := "asgn_target"
	updateArtifacts := []string{"artifact_2"}
	updated, err := app.UpdateHandoff(ctx, "proj_1", "work_1", handoff.ID, UpdateHandoffCommand{
		TargetAssignmentID:    &targetAssignmentID,
		RecommendedNextAction: &action,
		LinkedArtifactIDs:     &updateArtifacts,
		Status:                &status,
	})
	if err != nil {
		t.Fatalf("UpdateHandoff() error = %v", err)
	}
	if updated.TargetAssignmentID != "asgn_target" || updated.RecommendedNextAction != action || updated.Status != projectwork.HandoffStatusAccepted {
		t.Fatalf("updated handoff = %+v, want target assignment, action, accepted status", updated)
	}
	updateArtifacts[0] = "mutated"
	if updated.LinkedArtifactIDs[0] != "artifact_2" {
		t.Fatalf("updated handoff linked artifacts mutated through command: %+v", updated.LinkedArtifactIDs)
	}
	if err := app.DeleteHandoff(ctx, "proj_1", "work_1", handoff.ID); err != nil {
		t.Fatalf("DeleteHandoff() error = %v", err)
	}
	handoffs, err := store.ListHandoffs(ctx, projectwork.HandoffFilter{ProjectID: "proj_1", WorkItemID: "work_1"})
	if err != nil {
		t.Fatalf("ListHandoffs() error = %v", err)
	}
	if len(handoffs) != 0 {
		t.Fatalf("handoffs after delete = %+v, want none", handoffs)
	}
}

func TestApplication_ReconcileChatSessionAssignmentsUpdatesLinkedExternalAssignment(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := projectwork.NewMemoryStore()
	app := newTestApplication(store)
	base := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	if _, err := app.CreateWorkItem(ctx, "proj_1", CreateWorkItemCommand{ID: "work_1", Title: "Build"}); err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}
	if _, err := app.CreateAssignment(ctx, "proj_1", "work_1", CreateAssignmentCommand{
		ID:         "asgn_1",
		RoleID:     "software_developer",
		DriverKind: projectwork.AssignmentDriverExternalAgent,
		Status:     projectwork.AssignmentStatusRunning,
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:          projectwork.AssignmentExecutionKindChatSession,
			ChatSessionID: "chat_1",
			Status:        projectwork.AssignmentStatusRunning,
		},
		StartedAt: base,
	}); err != nil {
		t.Fatalf("CreateAssignment() error = %v", err)
	}

	result, err := app.ReconcileChatSessionAssignments(ctx, chat.Session{
		ID:        "chat_1",
		ProjectID: "proj_1",
		Status:    "running",
		UpdatedAt: base.Add(2 * time.Minute),
		Messages: []chat.Message{
			{ID: "msg_done", Role: "assistant", Status: "completed", TraceID: "trace_chat", StartedAt: base.Add(time.Minute), CompletedAt: base.Add(2 * time.Minute)},
		},
	})
	if err != nil {
		t.Fatalf("ReconcileChatSessionAssignments() error = %v", err)
	}
	if len(result.Updated) != 1 {
		t.Fatalf("updated assignments = %d, want 1", len(result.Updated))
	}
	updated := result.Updated[0]
	if updated.Status != projectwork.AssignmentStatusCompleted || updated.ExecutionRef.MessageID != "msg_done" || updated.ExecutionRef.Status != projectwork.AssignmentStatusCompleted {
		t.Fatalf("updated assignment = %+v, want completed chat linkage", updated)
	}
	if updated.CompletedAt.IsZero() {
		t.Fatalf("updated assignment completed_at is zero, want chat completion timestamp")
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
	if result.Assignment.ExecutionRef.TaskID != result.Task.ID || result.Assignment.ExecutionRef.RunID != result.Run.ID || result.Assignment.ExecutionRef.ContextSnapshotID != "ctx_1" {
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
		item.ExecutionRef = projectwork.AssignmentExecutionRef{Kind: projectwork.AssignmentExecutionKindTaskRun, TaskID: "task_existing"}
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
	if got.ExecutionRef.TaskID != "" || got.ExecutionRef.RunID != "" || got.Status != projectwork.AssignmentStatusQueued || !got.StartedAt.IsZero() {
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
	if result.Assignment.Status != projectwork.AssignmentStatusFailed || result.Assignment.ExecutionRef.TaskID != "task_fixed" || result.Assignment.CompletedAt.IsZero() {
		t.Fatalf("assignment after runner failure = %+v, want failed linked task", result.Assignment)
	}
}

type recordingAgentRunner struct {
	prepareCalls int
	closeCalls   int
	deleteCalls  int
	prepareErr   error
	prepareReq   agentadapters.PrepareSessionRequest
}

func (r *recordingAgentRunner) PrepareSession(_ context.Context, req agentadapters.PrepareSessionRequest) (agentadapters.PrepareSessionResult, error) {
	r.prepareCalls++
	r.prepareReq = req
	if r.prepareErr != nil {
		return agentadapters.PrepareSessionResult{}, r.prepareErr
	}
	return agentadapters.PrepareSessionResult{
		DriverKind:      agentadapters.DriverKindACP,
		NativeSessionID: "native_" + req.SessionID,
		ConfigOptions:   req.ConfigOptions,
	}, nil
}

func (r *recordingAgentRunner) CloseSession(_ context.Context, _ string) error {
	r.closeCalls++
	return nil
}

func (r *recordingAgentRunner) DeleteSession(_ context.Context, _ string) error {
	r.deleteCalls++
	return nil
}

func newExternalStartTestApplication(workStore projectwork.Store, chatStore ChatSessionStore, runner AgentRunner) *Application {
	return New(Options{
		Store:          workStore,
		ChatStore:      chatStore,
		AgentRunner:    runner,
		PrepareTimeout: time.Second,
		IDGenerator:    func(prefix string) string { return prefix + "_fixed" },
		Now: func() time.Time {
			return time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
		},
	})
}

func seedExternalStartTestAssignment(t *testing.T, ctx context.Context, app *Application) projectwork.Assignment {
	t.Helper()
	if _, err := app.CreateWorkItem(ctx, "proj_1", CreateWorkItemCommand{ID: "work_1", Title: "Build"}); err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}
	assignment, err := app.CreateAssignment(ctx, "proj_1", "work_1", CreateAssignmentCommand{
		ID:         "asgn_ext",
		RoleID:     "software_developer",
		DriverKind: projectwork.AssignmentDriverExternalAgent,
	})
	if err != nil {
		t.Fatalf("CreateAssignment() error = %v", err)
	}
	return assignment
}

func TestApplication_StartExternalAgentAssignmentPreparesAndLinksSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workStore := projectwork.NewMemoryStore()
	chatStore := chat.NewMemoryStore()
	runner := &recordingAgentRunner{}
	app := newExternalStartTestApplication(workStore, chatStore, runner)
	assignment := seedExternalStartTestAssignment(t, ctx, app)

	result, err := app.StartExternalAgentAssignment(ctx, StartExternalAgentAssignmentCommand{
		ProjectID:         "proj_1",
		Assignment:        assignment,
		ContextSnapshotID: "ctx_ext",
		ContextPacket:     []byte(`{"refs":{"assignment_id":"asgn_ext"}}`),
		Session: chat.Session{
			ID:        "chat_ext",
			ProjectID: "proj_1",
			AgentID:   "codex",
			Workspace: "/tmp/hecate",
			MCPServers: []types.MCPServerConfig{{
				Name:    "fs",
				Command: "node",
				Args:    []string{"server.js"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("StartExternalAgentAssignment() error = %v", err)
	}
	if runner.prepareCalls != 1 || runner.closeCalls != 0 || runner.deleteCalls != 0 {
		t.Fatalf("runner prepare/close/delete = %d/%d/%d, want 1/0/0", runner.prepareCalls, runner.closeCalls, runner.deleteCalls)
	}
	if got := runner.prepareReq.MCPServers; len(got) != 1 || got[0].Name != "fs" || got[0].Args[0] != "server.js" {
		t.Fatalf("prepare MCP servers = %+v, want fs server", got)
	}
	if result.Assignment.ExecutionRef.ChatSessionID != "chat_ext" || result.Assignment.ExecutionRef.ContextSnapshotID != "ctx_ext" || result.Assignment.Status != projectwork.AssignmentStatusRunning {
		t.Fatalf("assignment = %+v, want linked running session", result.Assignment)
	}
	session, ok, err := chatStore.Get(ctx, "chat_ext")
	if err != nil || !ok {
		t.Fatalf("Get(chat_ext) ok=%v err=%v, want session", ok, err)
	}
	if session.NativeSessionID != "native_chat_ext" || session.DriverKind != agentadapters.DriverKindACP {
		t.Fatalf("session = %+v, want prepared native metadata", session)
	}
}

func TestApplication_StartExternalAgentAssignmentPrepareFailureDeletesSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workStore := projectwork.NewMemoryStore()
	chatStore := chat.NewMemoryStore()
	runner := &recordingAgentRunner{prepareErr: fmt.Errorf("prepare boom")}
	app := newExternalStartTestApplication(workStore, chatStore, runner)
	assignment := seedExternalStartTestAssignment(t, ctx, app)

	_, err := app.StartExternalAgentAssignment(ctx, StartExternalAgentAssignmentCommand{
		ProjectID:  "proj_1",
		Assignment: assignment,
		Session: chat.Session{
			ID:        "chat_ext",
			ProjectID: "proj_1",
			AgentID:   "codex",
			Workspace: "/tmp/hecate",
		},
	})
	var prepareErr ExternalAgentPrepareError
	if !errors.As(err, &prepareErr) || !strings.Contains(err.Error(), "prepare boom") {
		t.Fatalf("StartExternalAgentAssignment(prepare failure) error = %v, want ExternalAgentPrepareError", err)
	}
	if _, ok, err := chatStore.Get(ctx, "chat_ext"); err != nil || ok {
		t.Fatalf("Get(chat_ext) ok=%v err=%v, want deleted session", ok, err)
	}
	items, err := workStore.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: "proj_1", WorkItemID: "work_1"})
	if err != nil {
		t.Fatalf("ListAssignments() error = %v", err)
	}
	if items[0].ExecutionRef.ChatSessionID != "" || items[0].Status != projectwork.AssignmentStatusQueued {
		t.Fatalf("assignment after prepare failure = %+v, want unlinked queued", items[0])
	}
}

func TestApplication_StartExternalAgentAssignmentNilDependencies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workStore := projectwork.NewMemoryStore()
	app := newExternalStartTestApplication(workStore, nil, &recordingAgentRunner{})
	assignment := seedExternalStartTestAssignment(t, ctx, app)

	_, err := app.StartExternalAgentAssignment(ctx, StartExternalAgentAssignmentCommand{
		ProjectID:  "proj_1",
		Assignment: assignment,
		Session:    chat.Session{ID: "chat_ext", ProjectID: "proj_1", AgentID: "codex", Workspace: "/tmp/hecate"},
	})
	if !errors.Is(err, ErrChatStoreNotConfigured) {
		t.Fatalf("StartExternalAgentAssignment(nil chat store) error = %v, want ErrChatStoreNotConfigured", err)
	}

	app = newExternalStartTestApplication(workStore, chat.NewMemoryStore(), nil)
	_, err = app.StartExternalAgentAssignment(ctx, StartExternalAgentAssignmentCommand{
		ProjectID:  "proj_1",
		Assignment: assignment,
		Session:    chat.Session{ID: "chat_ext", ProjectID: "proj_1", AgentID: "codex", Workspace: "/tmp/hecate"},
	})
	if !errors.Is(err, ErrAgentRunnerNotConfigured) {
		t.Fatalf("StartExternalAgentAssignment(nil agent runner) error = %v, want ErrAgentRunnerNotConfigured", err)
	}
}

type racingAssignmentStore struct {
	projectwork.Store
	raced bool
}

func (s *racingAssignmentStore) UpdateAssignment(ctx context.Context, projectID, assignmentID string, update func(*projectwork.Assignment)) (projectwork.Assignment, error) {
	if !s.raced && assignmentID == "asgn_ext" {
		s.raced = true
		if _, err := s.Store.UpdateAssignment(ctx, projectID, assignmentID, func(item *projectwork.Assignment) {
			item.ExecutionRef = projectwork.AssignmentExecutionRef{Kind: projectwork.AssignmentExecutionKindChatSession, ChatSessionID: "chat_winner"}
			item.Status = projectwork.AssignmentStatusRunning
		}); err != nil {
			return projectwork.Assignment{}, err
		}
	}
	return s.Store.UpdateAssignment(ctx, projectID, assignmentID, update)
}

func TestApplication_StartExternalAgentAssignmentCleansPreparedSessionWhenClaimLost(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseStore := projectwork.NewMemoryStore()
	workStore := &racingAssignmentStore{Store: baseStore}
	chatStore := chat.NewMemoryStore()
	runner := &recordingAgentRunner{}
	app := newExternalStartTestApplication(workStore, chatStore, runner)
	assignment := seedExternalStartTestAssignment(t, ctx, app)

	result, err := app.StartExternalAgentAssignment(ctx, StartExternalAgentAssignmentCommand{
		ProjectID:  "proj_1",
		Assignment: assignment,
		Session:    chat.Session{ID: "chat_ext", ProjectID: "proj_1", AgentID: "codex", Workspace: "/tmp/hecate"},
	})
	if !errors.Is(err, ErrAssignmentStartConflict) {
		t.Fatalf("StartExternalAgentAssignment(raced claim) error = %v, want ErrAssignmentStartConflict", err)
	}
	if result == nil || result.Assignment.ExecutionRef.ChatSessionID != "chat_winner" {
		t.Fatalf("result = %+v, want winning assignment", result)
	}
	if runner.prepareCalls != 1 || runner.deleteCalls != 1 {
		t.Fatalf("runner prepare/delete = %d/%d, want 1/1 cleanup", runner.prepareCalls, runner.deleteCalls)
	}
	if runner.closeCalls != 0 {
		t.Fatalf("closeCalls = %d, want destructive cleanup to use delete", runner.closeCalls)
	}
	if _, ok, err := chatStore.Get(ctx, "chat_ext"); err != nil || ok {
		t.Fatalf("Get(chat_ext) ok=%v err=%v, want cleaned session", ok, err)
	}
}

func TestApplication_StartExternalAgentAssignmentRejectsExistingChatBeforePrepare(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workStore := projectwork.NewMemoryStore()
	chatStore := chat.NewMemoryStore()
	runner := &recordingAgentRunner{}
	app := newExternalStartTestApplication(workStore, chatStore, runner)
	assignment := seedExternalStartTestAssignment(t, ctx, app)
	assignment, err := workStore.UpdateAssignment(ctx, "proj_1", assignment.ID, func(item *projectwork.Assignment) {
		item.ExecutionRef = projectwork.AssignmentExecutionRef{Kind: projectwork.AssignmentExecutionKindChatSession, ChatSessionID: "chat_existing"}
	})
	if err != nil {
		t.Fatalf("UpdateAssignment() error = %v", err)
	}

	_, err = app.StartExternalAgentAssignment(ctx, StartExternalAgentAssignmentCommand{
		ProjectID:  "proj_1",
		Assignment: assignment,
		Session:    chat.Session{ID: "chat_ext", ProjectID: "proj_1", AgentID: "codex", Workspace: "/tmp/hecate"},
	})
	if !errors.Is(err, ErrAssignmentStartConflict) {
		t.Fatalf("StartExternalAgentAssignment(existing chat) error = %v, want ErrAssignmentStartConflict", err)
	}
	if runner.prepareCalls != 0 {
		t.Fatalf("prepareCalls = %d, want 0 before conflict", runner.prepareCalls)
	}
}
