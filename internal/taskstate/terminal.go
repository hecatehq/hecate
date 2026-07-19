package taskstate

import (
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/pkg/types"
)

func validateTerminalTransition(tr TerminalRunTransition) error {
	if tr.Task.ID == "" {
		return fmt.Errorf("task id is required")
	}
	if tr.Run.ID == "" {
		return fmt.Errorf("run id is required")
	}
	if tr.Run.TaskID != "" && tr.Run.TaskID != tr.Task.ID {
		return fmt.Errorf("run %q does not belong to task %q", tr.Run.ID, tr.Task.ID)
	}
	if err := validatePendingApprovalResolution(tr.ApprovalResolution, "rejected"); err != nil {
		return err
	}
	if tr.ApprovalResolution != nil && (tr.Run.Status != "cancelled" || tr.Task.Status != "cancelled") {
		return fmt.Errorf("rejected approval resolution requires cancelled task and run candidates")
	}
	if tr.ApprovalResolution != nil {
		if err := validateApprovalResolutionProjection(*tr.ApprovalResolution, tr.Task, tr.Run); err != nil {
			return err
		}
	}
	if tr.ApprovalResolution != nil && tr.PreserveTaskProjection {
		return fmt.Errorf("rejected approval resolution cannot preserve the prior task projection")
	}
	if tr.ApprovalResolution != nil && tr.TrustedSupplementalRunMetadata != nil {
		return fmt.Errorf("rejected approval resolution cannot carry terminal supplemental metadata")
	}
	if tr.ApprovalResolution != nil && (tr.TerminalEvent != nil || tr.TaskUpdatedEvent != nil) {
		return fmt.Errorf("rejected approval resolution events are store-derived")
	}
	return nil
}

func validatePendingApprovalResolution(resolution *PendingApprovalResolution, requiredStatus string) error {
	if resolution == nil {
		return nil
	}
	if strings.TrimSpace(resolution.ApprovalID) == "" {
		return fmt.Errorf("approval id is required")
	}
	if resolution.Status != requiredStatus {
		return fmt.Errorf("approval resolution status must be %q", requiredStatus)
	}
	if resolution.ResolvedAt.IsZero() {
		return fmt.Errorf("approval resolution time is required")
	}
	if strings.TrimSpace(resolution.RequestID) == "" {
		return fmt.Errorf("approval resolution request id is required")
	}
	if strings.TrimSpace(resolution.TraceID) == "" {
		return fmt.Errorf("approval resolution trace id is required")
	}
	return nil
}

func validateApprovalResolutionProjection(resolution PendingApprovalResolution, task types.Task, run types.TaskRun) error {
	if run.RequestID != resolution.RequestID || run.TraceID != resolution.TraceID {
		return fmt.Errorf("approval resolution correlation must match the run candidate")
	}
	if task.LatestRequestID != resolution.RequestID || task.LatestTraceID != resolution.TraceID {
		return fmt.Errorf("approval resolution correlation must match the task candidate")
	}
	return nil
}

func terminalTransitionFinishedAt(tr TerminalRunTransition) time.Time {
	if !tr.FinishedAt.IsZero() {
		return tr.FinishedAt
	}
	if !tr.Run.FinishedAt.IsZero() {
		return tr.Run.FinishedAt
	}
	if !tr.Task.FinishedAt.IsZero() {
		return tr.Task.FinishedAt
	}
	return time.Now().UTC()
}

func mergeTrustedTerminalRunMetadata(winner types.TaskRun, supplemental *TerminalRunSupplementalMetadata) types.TaskRun {
	merged := winner
	if supplemental == nil {
		return mergeStoredRichInputRoute(merged, winner)
	}
	if strings.TrimSpace(supplemental.Provider) != "" {
		merged.Provider = supplemental.Provider
	}
	if strings.TrimSpace(supplemental.ProviderKind) != "" {
		merged.ProviderKind = supplemental.ProviderKind
	}
	if supplemental.InputProviderInstance.Valid() {
		merged.InputProviderInstance = supplemental.InputProviderInstance
	}
	if supplemental.InputProviderDispatchRecorded {
		merged.InputProviderDispatchRecorded = true
	}
	if supplemental.InputProviderDisclosedInstance.Valid() {
		merged.InputProviderDisclosedInstance = supplemental.InputProviderDisclosedInstance
	}
	if strings.TrimSpace(supplemental.Model) != "" {
		merged.Model = supplemental.Model
	}
	if supplemental.StepCount > merged.StepCount {
		merged.StepCount = supplemental.StepCount
	}
	if supplemental.ModelCallCount > merged.ModelCallCount {
		merged.ModelCallCount = supplemental.ModelCallCount
	}
	if supplemental.ArtifactCount > merged.ArtifactCount {
		merged.ArtifactCount = supplemental.ArtifactCount
	}
	if supplemental.TotalCostMicrosUSD > merged.TotalCostMicrosUSD {
		merged.TotalCostMicrosUSD = supplemental.TotalCostMicrosUSD
	}
	return mergeStoredRichInputRoute(merged, winner)
}

// mergeStoredRichInputRoute keeps the authoritative rich-input fence when a
// terminal transition was built from an older run snapshot. The dispatch
// boundary writes this metadata independently of finalization, so cancellation
// and cleanup paths must never erase it while settling the winning status.
func mergeStoredRichInputRoute(candidate, stored types.TaskRun) types.TaskRun {
	if strings.TrimSpace(stored.InputRef) == "" {
		return candidate
	}
	merged := candidate
	merged.InputRef = stored.InputRef
	if stored.InputProviderInstance.Valid() {
		merged.InputProviderInstance = stored.InputProviderInstance
	}
	if stored.InputProviderDispatchRecorded {
		merged.InputProviderDispatchRecorded = true
		merged.Provider = firstNonEmptyString(stored.Provider, merged.Provider)
		merged.ProviderKind = firstNonEmptyString(stored.ProviderKind, merged.ProviderKind)
		merged.Model = firstNonEmptyString(stored.Model, merged.Model)
	}
	// A stale terminal or state-transition candidate may have captured an
	// unrelated disclosure marker before the dispatch fence committed. Keep
	// the marker empty unless it belongs to the authoritative admitted route;
	// actual provider-call metadata may fill it below.
	if stored.InputProviderInstance.Valid() && merged.InputProviderDisclosedInstance.Valid() && merged.InputProviderDisclosedInstance != stored.InputProviderInstance {
		merged.InputProviderDisclosedInstance = types.ProviderInstanceIdentity{}
	}
	if stored.InputProviderDisclosedInstance.Valid() {
		merged.InputProviderDisclosedInstance = stored.InputProviderDisclosedInstance
	}
	return merged
}

func terminalChildSettlementTime(settlement, earliest time.Time) time.Time {
	if !earliest.IsZero() && settlement.Before(earliest) {
		return earliest
	}
	return settlement
}

func mergeApprovalResolution(stored types.TaskApproval, resolution PendingApprovalResolution) (types.TaskApproval, error) {
	if !stored.CreatedAt.IsZero() && resolution.ResolvedAt.Before(stored.CreatedAt) {
		return types.TaskApproval{}, fmt.Errorf("approval resolution time cannot precede creation time")
	}
	resolved := stored
	resolved.Status = resolution.Status
	resolved.ResolvedBy = firstNonEmptyString(resolution.ResolvedBy, "operator")
	resolved.ResolutionNote = resolution.ResolutionNote
	resolved.ResolvedAt = resolution.ResolvedAt
	return resolved, nil
}

func terminalEventFromSpec(spec RunEventSpec, taskID, runID string, createdAt time.Time) types.TaskRunEvent {
	if spec.CreatedAt.IsZero() {
		spec.CreatedAt = createdAt
	}
	return types.TaskRunEvent{
		TaskID:    taskID,
		RunID:     runID,
		EventType: spec.EventType,
		Data:      copyEventData(spec.Data),
		RequestID: spec.RequestID,
		TraceID:   spec.TraceID,
		CreatedAt: spec.CreatedAt,
	}
}

func runStateEventFromSpec(spec RunEventSpec, taskID string, run types.TaskRun, steps []types.TaskStep, artifacts []types.TaskArtifact, createdAt time.Time) types.TaskRunEvent {
	if spec.IncludeRunSnapshot {
		data := make(map[string]any, len(spec.Data)+3)
		for key, value := range spec.Data {
			data[key] = value
		}
		data["run"] = run
		data["steps"] = steps
		data["artifacts"] = artifacts
		spec.Data = data
	}
	return terminalEventFromSpec(spec, taskID, run.ID, createdAt)
}

func approvalResolutionEvent(taskID string, resolution PendingApprovalResolution, approval types.TaskApproval, run types.TaskRun, steps []types.TaskStep, artifacts []types.TaskArtifact) types.TaskRunEvent {
	return runStateEventFromSpec(RunEventSpec{
		EventType:          runtimeevents.EventApprovalResolved.String(),
		Data:               runtimeevents.ApprovalResolved(approval),
		RequestID:          resolution.RequestID,
		TraceID:            resolution.TraceID,
		CreatedAt:          approval.ResolvedAt,
		IncludeRunSnapshot: true,
	}, taskID, run, steps, artifacts, approval.ResolvedAt)
}

func approvalRunQueuedEvent(taskID string, resolution PendingApprovalResolution, run types.TaskRun, steps []types.TaskStep, artifacts []types.TaskArtifact) types.TaskRunEvent {
	return runStateEventFromSpec(RunEventSpec{
		EventType:          runtimeevents.EventRunQueued.String(),
		Data:               map[string]any{"resume": true},
		RequestID:          resolution.RequestID,
		TraceID:            resolution.TraceID,
		CreatedAt:          resolution.ResolvedAt,
		IncludeRunSnapshot: true,
	}, taskID, run, steps, artifacts, resolution.ResolvedAt)
}

func rejectedApprovalTerminalEvent(taskID string, resolution PendingApprovalResolution, run types.TaskRun, finishedAt time.Time) types.TaskRunEvent {
	return terminalEventFromSpec(RunEventSpec{
		EventType: runtimeevents.EventRunCancelled.String(),
		Data:      map[string]any{"reason": run.LastError},
		RequestID: resolution.RequestID,
		TraceID:   resolution.TraceID,
		CreatedAt: finishedAt,
	}, taskID, run.ID, finishedAt)
}

func rejectedApprovalTaskUpdatedEvent(taskID string, resolution PendingApprovalResolution, runID string, finishedAt time.Time) types.TaskRunEvent {
	return terminalEventFromSpec(RunEventSpec{
		EventType: runtimeevents.EventTaskUpdated.String(),
		RequestID: resolution.RequestID,
		TraceID:   resolution.TraceID,
		CreatedAt: finishedAt,
	}, taskID, runID, finishedAt)
}

func runEventSpecsNeedSnapshot(specs []RunEventSpec) bool {
	for _, spec := range specs {
		if spec.IncludeRunSnapshot {
			return true
		}
	}
	return false
}

func copyEventData(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	copied := make(map[string]any, len(data))
	for key, value := range data {
		copied[key] = value
	}
	return copied
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
