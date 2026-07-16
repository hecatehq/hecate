package api

import (
	"context"
	"errors"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/internal/projectworkapp"
)

// cairnlineProjectAssignments lists the portable assignment rows and, for the
// strict embedded authority path, first reconciles linked Hecate task runs
// into Cairnline. This keeps aggregate reads such as Activity, Operations, and
// Health from computing against a stale running row after the host execution
// has reached an approval gate or terminal outcome.
func (h *Handler) cairnlineProjectAssignments(
	ctx context.Context,
	service *cairnline.Service,
	snapshot cairnlinebridge.Snapshot,
) ([]projectwork.Assignment, error) {
	if h != nil && h.requiresEmbeddedCairnlineProjectReads() && h.projectAssignmentWritesUseCairnlineAuthority() {
		mutationCtx, release, err := h.projectMutationGate.begin(ctx, snapshot.Project.ID)
		if err != nil {
			return nil, err
		}
		defer release()
		ctx = mutationCtx
	}
	items, err := service.ListAssignments(ctx, snapshot.Project.ID)
	if err != nil {
		return nil, err
	}
	assignments := projectWorkAssignmentsFromCairnline(items, snapshot.Assignments)
	if h == nil || !h.requiresEmbeddedCairnlineProjectReads() || !h.projectAssignmentWritesUseCairnlineAuthority() {
		return assignments, nil
	}

	for index := range assignments {
		portable := items[index]
		assignment := assignments[index]
		if assignment.DriverKind != projectwork.AssignmentDriverHecateTask ||
			strings.TrimSpace(portable.ExecutionRef.TaskID) == "" ||
			assignmentTerminalStatusForAuthority(portable.Status) {
			continue
		}

		assignment, err = h.projectWorkApplication().ApplyAssignmentRuntime(ctx, assignment)
		if err != nil {
			return nil, err
		}
		assignment, linked, linkErr := h.currentTaskRunForCairnlineAssignmentReconciliation(ctx, portable, assignment)
		if linkErr != nil {
			return nil, linkErr
		}
		if !linked {
			// A missing or mismatched task/run is stale host evidence. It may be
			// rendered as unavailable, but it cannot mutate portable progress.
			continue
		}
		projection, err := projectworkapp.ProjectAssignmentExecution(ctx, h.taskStore, assignment)
		if err != nil {
			return nil, err
		}
		if projection == nil || projection.Execution.Missing {
			continue
		}
		projectedStatus := strings.TrimSpace(projection.Status)
		if projectedStatus == "" || projectedStatus == projectwork.AssignmentStatusQueued {
			continue
		}

		assignment.Status = projectedStatus
		assignment.StartedAt = projectworkapp.FirstNonZeroTime(assignment.StartedAt, projection.StartedAt)
		if projectworkapp.AssignmentIsTerminal(projectedStatus) {
			assignment.CompletedAt = projectworkapp.FirstNonZeroTime(assignment.CompletedAt, projection.CompletedAt)
		}
		if ref := projectworkapp.AssignmentExecutionRefFor(assignment, &projection.Execution, projectedStatus); ref != nil {
			assignment.ExecutionRef = projectwork.NormalizeAssignmentExecutionRef(*ref)
		}
		// The portable launch packet remains authoritative even if an old
		// compatibility runtime shadow carries a different snapshot id.
		assignment.ExecutionRef.ContextSnapshotID = portable.ContextSnapshotID

		desiredStatus := cairnlinebridge.AssignmentStatus(projectedStatus)
		desiredRef := cairnlinebridge.ExecutionRef(assignment.ExecutionRef)
		if portable.Status == desiredStatus && portable.ExecutionRef == desiredRef {
			assignments[index] = projectWorkAssignmentFromCairnlineAuthority(portable, assignment)
			continue
		}

		written, writeErr := h.writeStrictEmbeddedCairnlineAssignmentRuntime(ctx, service, assignment)
		if errors.Is(writeErr, cairnline.ErrConflict) {
			// Another transition won after the list read. Cairnline's first
			// terminal outcome is final; render that row and let a later read
			// reconsider any still-progressing state.
			latest, getErr := service.GetAssignment(ctx, assignment.ProjectID, assignment.ID)
			if getErr != nil {
				return nil, getErr
			}
			assignments[index] = projectWorkAssignmentFromCairnlineAuthority(latest, assignment)
			continue
		}
		if writeErr != nil {
			return nil, writeErr
		}
		assignments[index] = written
		h.shadowProjectAssignmentRuntimeToHecate(ctx, "project_assignment_task_reconcile_cairnline_runtime", written)
	}
	return assignments, nil
}

func (h *Handler) currentTaskRunForCairnlineAssignmentReconciliation(
	ctx context.Context,
	portable cairnline.Assignment,
	assignment projectwork.Assignment,
) (projectwork.Assignment, bool, error) {
	if h == nil || h.taskStore == nil {
		return assignment, false, nil
	}
	taskID := strings.TrimSpace(portable.ExecutionRef.TaskID)
	if taskID == "" {
		return assignment, false, nil
	}
	task, found, err := h.taskStore.GetTask(ctx, taskID)
	if err != nil {
		return assignment, false, err
	}
	if !found ||
		strings.TrimSpace(task.ID) != taskID ||
		strings.TrimSpace(task.ProjectID) != strings.TrimSpace(portable.ProjectID) ||
		strings.TrimSpace(task.WorkItemID) != strings.TrimSpace(portable.WorkItemID) ||
		strings.TrimSpace(task.AssignmentID) != strings.TrimSpace(portable.ID) {
		return assignment, false, nil
	}

	// A resumed task supersedes the run captured when the assignment first
	// started. Following LatestRunID prevents an obsolete terminal run from
	// irreversibly closing the portable assignment after work resumed.
	runID := strings.TrimSpace(task.LatestRunID)
	if runID == "" {
		runID = strings.TrimSpace(portable.ExecutionRef.RunID)
	}
	if runID == "" {
		return assignment, false, nil
	}
	run, found, err := h.taskStore.GetRun(ctx, taskID, runID)
	if err != nil {
		return assignment, false, err
	}
	if !found ||
		strings.TrimSpace(run.ID) != runID ||
		strings.TrimSpace(run.TaskID) != taskID ||
		strings.TrimSpace(run.ProjectID) != strings.TrimSpace(portable.ProjectID) ||
		strings.TrimSpace(run.WorkItemID) != strings.TrimSpace(portable.WorkItemID) ||
		strings.TrimSpace(run.AssignmentID) != strings.TrimSpace(portable.ID) {
		return assignment, false, nil
	}

	ref := projectwork.NormalizeAssignmentExecutionRef(assignment.ExecutionRef)
	ref.Kind = projectwork.AssignmentExecutionKindTaskRun
	ref.TaskID = taskID
	ref.RunID = runID
	ref.ContextSnapshotID = strings.TrimSpace(portable.ContextSnapshotID)
	assignment.ExecutionRef = ref
	return assignment, true, nil
}
