package taskstate

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/pkg/types"
)

type MemoryStore struct {
	runEventBus
	mu        sync.Mutex
	tasks     map[string]types.Task
	runs      map[string]types.TaskRun
	steps     map[string]types.TaskStep
	approvals map[string]types.TaskApproval
	artifacts map[string]types.TaskArtifact
	events    map[string][]types.TaskRunEvent
	nextSeq   int64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tasks:     make(map[string]types.Task),
		runs:      make(map[string]types.TaskRun),
		steps:     make(map[string]types.TaskStep),
		approvals: make(map[string]types.TaskApproval),
		artifacts: make(map[string]types.TaskArtifact),
		events:    make(map[string][]types.TaskRunEvent),
		nextSeq:   1,
	}
}

func (s *MemoryStore) Backend() string {
	return "memory"
}

func (s *MemoryStore) CreateTask(_ context.Context, task types.Task) (types.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if task.ID == "" {
		return types.Task{}, fmt.Errorf("task id is required")
	}
	now := time.Now().UTC()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	s.tasks[task.ID] = cloneTask(task)
	return cloneTask(task), nil
}

func (s *MemoryStore) GetTask(_ context.Context, id string) (types.Task, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok {
		return types.Task{}, false, nil
	}
	return cloneTask(task), true, nil
}

func (s *MemoryStore) ListTasks(_ context.Context, filter TaskFilter) ([]types.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]types.Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		if filter.Status != "" && task.Status != filter.Status {
			continue
		}
		if filter.ProjectID != nil && task.ProjectID != *filter.ProjectID {
			continue
		}
		items = append(items, cloneTask(task))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	if filter.Limit > 0 && len(items) > filter.Limit {
		items = items[:filter.Limit]
	}
	return items, nil
}

func (s *MemoryStore) UpdateTask(_ context.Context, task types.Task) (types.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[task.ID]; !ok {
		return types.Task{}, fmt.Errorf("task %q not found", task.ID)
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = time.Now().UTC()
	}
	s.tasks[task.ID] = cloneTask(task)
	return cloneTask(task), nil
}

func (s *MemoryStore) DeleteTask(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; !ok {
		return fmt.Errorf("task %q not found", id)
	}
	delete(s.tasks, id)
	for runID, run := range s.runs {
		if run.TaskID != id {
			continue
		}
		delete(s.runs, runID)
		delete(s.steps, runID)
		delete(s.events, run.TaskID+"/"+runID)
	}
	for k, step := range s.steps {
		if step.TaskID == id {
			delete(s.steps, k)
		}
	}
	for k, approval := range s.approvals {
		if approval.TaskID == id {
			delete(s.approvals, k)
		}
	}
	for k, artifact := range s.artifacts {
		if artifact.TaskID == id {
			delete(s.artifacts, k)
		}
	}
	return nil
}

func (s *MemoryStore) CreateRun(_ context.Context, run types.TaskRun) (types.TaskRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run.ID == "" {
		return types.TaskRun{}, fmt.Errorf("run id is required")
	}
	s.runs[run.ID] = run
	s.signalRun(run.ID)
	return run, nil
}

func (s *MemoryStore) GetRun(_ context.Context, taskID, runID string) (types.TaskRun, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runID]
	if !ok || (taskID != "" && run.TaskID != taskID) {
		return types.TaskRun{}, false, nil
	}
	return run, true, nil
}

func (s *MemoryStore) ListRuns(_ context.Context, taskID string) ([]types.TaskRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]types.TaskRun, 0)
	for _, run := range s.runs {
		if taskID != "" && run.TaskID != taskID {
			continue
		}
		items = append(items, run)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Number == items[j].Number {
			return items[i].ID < items[j].ID
		}
		return items[i].Number > items[j].Number
	})
	return items, nil
}

func (s *MemoryStore) ListRunsByFilter(ctx context.Context, filter RunFilter) ([]types.TaskRun, error) {
	runs, err := s.ListRuns(ctx, filter.TaskID)
	if err != nil {
		return nil, err
	}
	if len(filter.Statuses) == 0 {
		if filter.OrderByID {
			runs = filterRunsByIDCursor(runs, filter.AfterID)
		}
		if filter.Limit > 0 && len(runs) > filter.Limit {
			return runs[:filter.Limit], nil
		}
		return runs, nil
	}
	allowed := make(map[string]struct{}, len(filter.Statuses))
	for _, status := range filter.Statuses {
		allowed[status] = struct{}{}
	}
	filtered := make([]types.TaskRun, 0, len(runs))
	for _, run := range runs {
		if _, ok := allowed[run.Status]; !ok {
			continue
		}
		filtered = append(filtered, run)
	}
	if filter.OrderByID {
		filtered = filterRunsByIDCursor(filtered, filter.AfterID)
	}
	if filter.Limit > 0 && len(filtered) > filter.Limit {
		filtered = filtered[:filter.Limit]
	}
	return filtered, nil
}

func filterRunsByIDCursor(runs []types.TaskRun, afterID string) []types.TaskRun {
	sort.Slice(runs, func(i, j int) bool { return runs[i].ID < runs[j].ID })
	if afterID == "" {
		return runs
	}
	first := sort.Search(len(runs), func(i int) bool { return runs[i].ID > afterID })
	return runs[first:]
}

func (s *MemoryStore) UpdateRun(_ context.Context, run types.TaskRun) (types.TaskRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runs[run.ID]; !ok {
		return types.TaskRun{}, fmt.Errorf("run %q not found", run.ID)
	}
	s.runs[run.ID] = run
	s.signalRun(run.ID)
	return run, nil
}

func (s *MemoryStore) AppendStep(_ context.Context, step types.TaskStep) (types.TaskStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if step.ID == "" {
		return types.TaskStep{}, fmt.Errorf("step id is required")
	}
	s.steps[step.ID] = step
	s.signalRun(step.RunID)
	return step, nil
}

func (s *MemoryStore) GetStep(_ context.Context, runID, stepID string) (types.TaskStep, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	step, ok := s.steps[stepID]
	if !ok || (runID != "" && step.RunID != runID) {
		return types.TaskStep{}, false, nil
	}
	return step, true, nil
}

func (s *MemoryStore) ListSteps(_ context.Context, runID string) ([]types.TaskStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]types.TaskStep, 0)
	for _, step := range s.steps {
		if runID != "" && step.RunID != runID {
			continue
		}
		items = append(items, step)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Index == items[j].Index {
			return items[i].ID < items[j].ID
		}
		return items[i].Index < items[j].Index
	})
	return items, nil
}

func (s *MemoryStore) UpdateStep(_ context.Context, step types.TaskStep) (types.TaskStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.steps[step.ID]; !ok {
		return types.TaskStep{}, fmt.Errorf("step %q not found", step.ID)
	}
	s.steps[step.ID] = step
	s.signalRun(step.RunID)
	return step, nil
}

func (s *MemoryStore) CreateApproval(_ context.Context, approval types.TaskApproval) (types.TaskApproval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if approval.ID == "" {
		return types.TaskApproval{}, fmt.Errorf("approval id is required")
	}
	if approval.CreatedAt.IsZero() {
		approval.CreatedAt = time.Now().UTC()
	}
	s.approvals[approval.ID] = approval
	s.signalRun(approval.RunID)
	return approval, nil
}

func (s *MemoryStore) GetApproval(_ context.Context, taskID, approvalID string) (types.TaskApproval, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	approval, ok := s.approvals[approvalID]
	if !ok || (taskID != "" && approval.TaskID != taskID) {
		return types.TaskApproval{}, false, nil
	}
	return approval, true, nil
}

func (s *MemoryStore) ListApprovals(_ context.Context, taskID string) ([]types.TaskApproval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]types.TaskApproval, 0)
	for _, approval := range s.approvals {
		if taskID != "" && approval.TaskID != taskID {
			continue
		}
		items = append(items, approval)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

func (s *MemoryStore) UpdateApproval(_ context.Context, approval types.TaskApproval) (types.TaskApproval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.approvals[approval.ID]; !ok {
		return types.TaskApproval{}, fmt.Errorf("approval %q not found", approval.ID)
	}
	s.approvals[approval.ID] = approval
	s.signalRun(approval.RunID)
	return approval, nil
}

func (s *MemoryStore) UpdatePendingApproval(_ context.Context, approval types.TaskApproval) (types.TaskApproval, bool, error) {
	if strings.TrimSpace(approval.ID) == "" {
		return types.TaskApproval{}, false, fmt.Errorf("approval id is required")
	}
	if strings.TrimSpace(approval.TaskID) == "" {
		return types.TaskApproval{}, false, fmt.Errorf("approval task id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.approvals[approval.ID]
	if !ok || current.Status != "pending" || (approval.TaskID != "" && current.TaskID != approval.TaskID) {
		return types.TaskApproval{}, false, nil
	}
	s.approvals[approval.ID] = approval
	s.signalRun(approval.RunID)
	return approval, true, nil
}

func (s *MemoryStore) CreateArtifact(_ context.Context, artifact types.TaskArtifact) (types.TaskArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if artifact.ID == "" {
		return types.TaskArtifact{}, fmt.Errorf("artifact id is required")
	}
	s.artifacts[artifact.ID] = artifact
	s.signalRun(artifact.RunID)
	return artifact, nil
}

func (s *MemoryStore) GetArtifact(_ context.Context, taskID, artifactID string) (types.TaskArtifact, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	artifact, ok := s.artifacts[artifactID]
	if !ok || (taskID != "" && artifact.TaskID != taskID) {
		return types.TaskArtifact{}, false, nil
	}
	return artifact, true, nil
}

func (s *MemoryStore) ListArtifacts(_ context.Context, filter ArtifactFilter) ([]types.TaskArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]types.TaskArtifact, 0)
	for _, artifact := range s.artifacts {
		if filter.TaskID != "" && artifact.TaskID != filter.TaskID {
			continue
		}
		if filter.RunID != "" && artifact.RunID != filter.RunID {
			continue
		}
		if filter.StepID != "" && artifact.StepID != filter.StepID {
			continue
		}
		if filter.Kind != "" && artifact.Kind != filter.Kind {
			continue
		}
		items = append(items, artifact)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if filter.Limit > 0 && len(items) > filter.Limit {
		items = items[:filter.Limit]
	}
	return items, nil
}

func (s *MemoryStore) UpdateArtifact(_ context.Context, artifact types.TaskArtifact) (types.TaskArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.artifacts[artifact.ID]; !ok {
		return types.TaskArtifact{}, fmt.Errorf("artifact %q not found", artifact.ID)
	}
	s.artifacts[artifact.ID] = artifact
	s.signalRun(artifact.RunID)
	return artifact, nil
}

func (s *MemoryStore) AppendRunEvent(_ context.Context, event types.TaskRunEvent) (types.TaskRunEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if event.RunID == "" {
		return types.TaskRunEvent{}, fmt.Errorf("run id is required")
	}
	if event.Sequence <= 0 {
		event.Sequence = s.nextSeq
		s.nextSeq++
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	s.events[event.RunID] = append(s.events[event.RunID], event)
	s.signalRun(event.RunID)
	return event, nil
}

func (s *MemoryStore) ApplyRunTerminalTransition(_ context.Context, tr TerminalRunTransition) (TerminalRunTransitionResult, error) {
	if err := validateTerminalTransition(tr); err != nil {
		return TerminalRunTransitionResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	storedTask, ok := s.tasks[tr.Task.ID]
	if !ok {
		return TerminalRunTransitionResult{}, fmt.Errorf("task %q not found", tr.Task.ID)
	}
	storedRun, ok := s.runs[tr.Run.ID]
	if !ok {
		return TerminalRunTransitionResult{}, fmt.Errorf("run %q not found", tr.Run.ID)
	}
	if storedRun.TaskID != tr.Task.ID {
		return TerminalRunTransitionResult{}, fmt.Errorf("run %q does not belong to task %q", tr.Run.ID, tr.Task.ID)
	}
	storedResolutionApproval := types.TaskApproval{}
	if tr.ApprovalResolution != nil {
		var found bool
		storedResolutionApproval, found = s.approvals[tr.ApprovalResolution.ApprovalID]
		if !found || storedResolutionApproval.TaskID != tr.Task.ID || storedResolutionApproval.RunID != tr.Run.ID {
			return TerminalRunTransitionResult{}, fmt.Errorf("approval %q not found", tr.ApprovalResolution.ApprovalID)
		}
		if storedRun.Status != "awaiting_approval" || storedResolutionApproval.Status != "pending" {
			return TerminalRunTransitionResult{
				Task:      cloneTask(storedTask),
				Run:       storedRun,
				Approval:  storedResolutionApproval,
				Steps:     s.listStepsLocked(storedRun.ID),
				Artifacts: s.listArtifactsLocked(ArtifactFilter{TaskID: tr.Task.ID, RunID: storedRun.ID}),
			}, nil
		}
	}
	storedRunTerminal := types.IsTerminalTaskRunStatus(storedRun.Status)
	trustedDifferentTerminalReplay := storedRunTerminal && storedRun.Status != tr.Run.Status && tr.TrustedSupplementalRunMetadata != nil
	if storedRunTerminal && storedRun.Status != tr.Run.Status && !trustedDifferentTerminalReplay {
		return TerminalRunTransitionResult{
			Task:      cloneTask(s.tasks[tr.Task.ID]),
			Run:       storedRun,
			Steps:     s.listStepsLocked(storedRun.ID),
			Artifacts: s.listArtifactsLocked(ArtifactFilter{TaskID: tr.Task.ID, RunID: storedRun.ID}),
		}, nil
	}

	finishedAt := terminalTransitionFinishedAt(tr)
	task := tr.Task
	run := mergeStoredRichInputRoute(tr.Run, storedRun)
	terminalEvent := tr.TerminalEvent
	taskUpdatedEvent := tr.TaskUpdatedEvent
	activeStepError := tr.ActiveStepError
	activeStepResult := tr.ActiveStepResult
	activeStepErrorKind := tr.ActiveStepErrorKind
	pendingApprovalResolutionNote := tr.PendingApprovalResolutionNote
	cancelActiveSteps := tr.CancelActiveSteps
	cancelStreamingArtifacts := tr.CancelStreamingArtifacts
	cancelPendingApprovals := tr.CancelPendingApprovals
	pendingApprovalStatus := tr.PendingApprovalStatus
	pendingApprovalResolvedBy := tr.PendingApprovalResolvedBy
	if tr.ApprovalResolution != nil {
		terminalEvent = nil
		taskUpdatedEvent = nil
		cancelActiveSteps = true
		cancelStreamingArtifacts = true
		cancelPendingApprovals = true
		activeStepError = run.LastError
		activeStepResult = "error"
		activeStepErrorKind = "run_cancelled"
		pendingApprovalStatus = "cancelled"
		pendingApprovalResolvedBy = "system"
		pendingApprovalResolutionNote = run.LastError
	}
	sameTerminalReplay := storedRunTerminal && storedRun.Status == tr.Run.Status
	terminalReplay := sameTerminalReplay || trustedDifferentTerminalReplay
	if trustedDifferentTerminalReplay {
		// Execution-result finalization can arrive after another terminal
		// status won. Preserve that winner completely and merge only the
		// explicitly trusted route/accounting fields; loser cleanup and events
		// must not run under the winner's identity.
		task = cloneTask(storedTask)
		run = mergeTrustedTerminalRunMetadata(storedRun, tr.TrustedSupplementalRunMetadata)
		terminalEvent = nil
		taskUpdatedEvent = nil
	} else if sameTerminalReplay {
		// Same-status terminal calls are cleanup replays. The first terminal
		// transition owns the run, task projection, and terminal events; a
		// replay may only settle children that arrived while the executor was
		// draining.
		task = cloneTask(storedTask)
		run = mergeTrustedTerminalRunMetadata(storedRun, tr.TrustedSupplementalRunMetadata)
		terminalEvent = nil
		taskUpdatedEvent = nil
		activeStepError = run.LastError
		pendingApprovalResolutionNote = run.LastError
	} else if tr.PreserveTaskProjection {
		task = cloneTask(storedTask)
	} else {
		if task.UpdatedAt.IsZero() {
			task.UpdatedAt = finishedAt
		}
		if task.FinishedAt.IsZero() {
			task.FinishedAt = finishedAt
		}
	}
	if !terminalReplay && run.FinishedAt.IsZero() {
		run.FinishedAt = finishedAt
	}
	resolvedApproval := storedResolutionApproval
	if tr.ApprovalResolution != nil {
		var err error
		resolvedApproval, err = mergeApprovalResolution(storedResolutionApproval, *tr.ApprovalResolution)
		if err != nil {
			return TerminalRunTransitionResult{}, err
		}
	}
	if terminalReplay {
		s.runs[run.ID] = run
	} else {
		s.tasks[task.ID] = cloneTask(task)
		s.runs[run.ID] = run
	}
	if tr.ApprovalResolution != nil {
		s.approvals[resolvedApproval.ID] = resolvedApproval
	}

	cancelledApprovals := make([]types.TaskApproval, 0)
	if cancelPendingApprovals && !trustedDifferentTerminalReplay {
		status := firstNonEmptyString(pendingApprovalStatus, "cancelled")
		resolvedBy := firstNonEmptyString(pendingApprovalResolvedBy, "system")
		note := firstNonEmptyString(pendingApprovalResolutionNote, run.LastError)
		for id, approval := range s.approvals {
			if approval.TaskID != task.ID || approval.RunID != run.ID || approval.Status != "pending" {
				continue
			}
			approval.Status = status
			approval.ResolvedBy = resolvedBy
			approval.ResolutionNote = note
			approval.ResolvedAt = terminalChildSettlementTime(finishedAt, approval.CreatedAt)
			s.approvals[id] = approval
			cancelledApprovals = append(cancelledApprovals, approval)
		}
		sort.Slice(cancelledApprovals, func(i, j int) bool {
			return cancelledApprovals[i].CreatedAt.Before(cancelledApprovals[j].CreatedAt)
		})
	}

	if cancelActiveSteps && !trustedDifferentTerminalReplay {
		result := firstNonEmptyString(activeStepResult, "error")
		errorKind := firstNonEmptyString(activeStepErrorKind, "run_cancelled")
		stepError := firstNonEmptyString(activeStepError, run.LastError)
		for id, step := range s.steps {
			if step.RunID != run.ID || (step.Status != "running" && step.Status != "awaiting_approval") {
				continue
			}
			step.Status = "cancelled"
			step.Result = result
			step.Error = stepError
			step.ErrorKind = errorKind
			step.FinishedAt = terminalChildSettlementTime(finishedAt, step.StartedAt)
			s.steps[id] = step
		}
	}

	if cancelStreamingArtifacts && !trustedDifferentTerminalReplay {
		for id, artifact := range s.artifacts {
			if artifact.TaskID != task.ID || artifact.RunID != run.ID || artifact.Status != "streaming" {
				continue
			}
			artifact.Status = "cancelled"
			s.artifacts[id] = artifact
		}
	}

	steps := s.listStepsLocked(run.ID)
	artifacts := s.listArtifactsLocked(ArtifactFilter{TaskID: task.ID, RunID: run.ID})
	events := make([]types.TaskRunEvent, 0, len(cancelledApprovals)+3)
	approvalEventType := firstNonEmptyString(tr.ApprovalResolvedEventType, runtimeevents.EventApprovalResolved.String())
	if tr.ApprovalResolution != nil {
		approvalEventType = runtimeevents.EventApprovalResolved.String()
	}
	for _, approval := range cancelledApprovals {
		event := types.TaskRunEvent{
			TaskID:    task.ID,
			RunID:     run.ID,
			EventType: approvalEventType,
			Data:      runtimeevents.ApprovalResolved(approval),
			RequestID: run.RequestID,
			TraceID:   run.TraceID,
			CreatedAt: approval.ResolvedAt,
		}
		events = append(events, s.appendRunEventLocked(event))
	}
	if tr.ApprovalResolution != nil {
		events = append(events, s.appendRunEventLocked(rejectedApprovalTerminalEvent(task.ID, *tr.ApprovalResolution, run, finishedAt)))
		events = append(events, s.appendRunEventLocked(rejectedApprovalTaskUpdatedEvent(task.ID, *tr.ApprovalResolution, run.ID, finishedAt)))
	} else if terminalEvent != nil {
		event := runStateEventFromSpec(*terminalEvent, task.ID, run, steps, artifacts, finishedAt)
		events = append(events, s.appendRunEventLocked(event))
	}
	if tr.ApprovalResolution == nil && taskUpdatedEvent != nil {
		event := runStateEventFromSpec(*taskUpdatedEvent, task.ID, run, steps, artifacts, finishedAt)
		events = append(events, s.appendRunEventLocked(event))
	}
	if tr.ApprovalResolution != nil {
		event := approvalResolutionEvent(task.ID, *tr.ApprovalResolution, resolvedApproval, run, steps, artifacts)
		events = append(events, s.appendRunEventLocked(event))
	}

	// The transition mutates the run, its children, and approvals through
	// locked helpers that bypass the per-write signal, so wake stream
	// subscribers explicitly.
	s.signalRun(run.ID)
	return TerminalRunTransitionResult{
		Task:               cloneTask(task),
		Run:                run,
		Approval:           resolvedApproval,
		Steps:              steps,
		Artifacts:          artifacts,
		CancelledApprovals: cancelledApprovals,
		Events:             events,
		Applied:            true,
	}, nil
}

func cloneTask(task types.Task) types.Task {
	if task.AgentPresetToolsEnabled != nil {
		toolsEnabled := *task.AgentPresetToolsEnabled
		task.AgentPresetToolsEnabled = &toolsEnabled
	}
	if task.MCPServers != nil {
		task.MCPServers = append([]types.MCPServerConfig(nil), task.MCPServers...)
		for index := range task.MCPServers {
			task.MCPServers[index].Args = append([]string(nil), task.MCPServers[index].Args...)
			task.MCPServers[index].Env = cloneStringMap(task.MCPServers[index].Env)
			task.MCPServers[index].Headers = cloneStringMap(task.MCPServers[index].Headers)
		}
	}
	return task
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func (s *MemoryStore) appendRunEventLocked(event types.TaskRunEvent) types.TaskRunEvent {
	if event.Sequence <= 0 {
		event.Sequence = s.nextSeq
		s.nextSeq++
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	event.ID = fmt.Sprintf("%d", event.Sequence)
	s.events[event.RunID] = append(s.events[event.RunID], event)
	return event
}

func (s *MemoryStore) listStepsLocked(runID string) []types.TaskStep {
	items := make([]types.TaskStep, 0)
	for _, step := range s.steps {
		if runID != "" && step.RunID != runID {
			continue
		}
		items = append(items, step)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Index == items[j].Index {
			return items[i].ID < items[j].ID
		}
		return items[i].Index < items[j].Index
	})
	return items
}

func (s *MemoryStore) listArtifactsLocked(filter ArtifactFilter) []types.TaskArtifact {
	items := make([]types.TaskArtifact, 0)
	for _, artifact := range s.artifacts {
		if filter.TaskID != "" && artifact.TaskID != filter.TaskID {
			continue
		}
		if filter.RunID != "" && artifact.RunID != filter.RunID {
			continue
		}
		if filter.StepID != "" && artifact.StepID != filter.StepID {
			continue
		}
		if filter.Kind != "" && artifact.Kind != filter.Kind {
			continue
		}
		items = append(items, artifact)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if filter.Limit > 0 && len(items) > filter.Limit {
		items = items[:filter.Limit]
	}
	return items
}

func (s *MemoryStore) ListRunEvents(_ context.Context, taskID, runID string, afterSequence int64, limit int) ([]types.TaskRunEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.events[runID]
	if len(items) == 0 {
		return nil, nil
	}
	result := make([]types.TaskRunEvent, 0, len(items))
	for _, event := range items {
		if taskID != "" && event.TaskID != taskID {
			continue
		}
		if event.Sequence <= afterSequence {
			continue
		}
		result = append(result, event)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Sequence < result[j].Sequence
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (s *MemoryStore) ListEvents(_ context.Context, filter EventFilter) ([]types.TaskRunEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var typeSet map[string]struct{}
	if len(filter.EventTypes) > 0 {
		typeSet = make(map[string]struct{}, len(filter.EventTypes))
		for _, t := range filter.EventTypes {
			typeSet[t] = struct{}{}
		}
	}
	var taskSet map[string]struct{}
	if len(filter.TaskIDs) > 0 {
		taskSet = make(map[string]struct{}, len(filter.TaskIDs))
		for _, id := range filter.TaskIDs {
			taskSet[id] = struct{}{}
		}
	}

	// MemoryStore keys events by run; flatten into a single slice and
	// filter+sort. Cheap relative to typical run counts; if it ever
	// becomes a bottleneck we'd switch to a per-store global event
	// log. Until then this is the simplest correct implementation.
	result := make([]types.TaskRunEvent, 0)
	for _, list := range s.events {
		for _, event := range list {
			if filter.AfterSequence > 0 && event.Sequence <= filter.AfterSequence {
				continue
			}
			if typeSet != nil {
				if _, ok := typeSet[event.EventType]; !ok {
					continue
				}
			}
			if taskSet != nil {
				if _, ok := taskSet[event.TaskID]; !ok {
					continue
				}
			}
			result = append(result, event)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Sequence < result[j].Sequence
	})
	limit := filter.Limit
	if limit <= 0 {
		limit = 200
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

// Prune removes `turn.completed` rows older than
// maxAge or, if maxCount > 0, beyond the most recent maxCount rows
// (counted globally across all runs). Returns the number of rows
// removed. Other event types are preserved.
//
// Both bounds are evaluated additively — i.e. a row is dropped if it
// fails *either* the age check (when maxAge > 0) or the count check
// (when maxCount > 0). With both zero, this is a no-op.
func (s *MemoryStore) Prune(_ context.Context, maxAge time.Duration, maxCount int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Time{}
	if maxAge > 0 {
		cutoff = time.Now().UTC().Add(-maxAge)
	}

	// Pass 1: drop rows older than cutoff. Track surviving turn rows so
	// pass 2 can apply a global most-recent-N cap by sequence.
	type turnRef struct {
		runID    string
		idx      int
		sequence int64
	}
	survivingTurns := make([]turnRef, 0)
	deleted := 0
	for runID, list := range s.events {
		kept := list[:0]
		for _, evt := range list {
			if evt.EventType == runtimeevents.EventTurnCompleted.String() && maxAge > 0 && evt.CreatedAt.Before(cutoff) {
				deleted++
				continue
			}
			kept = append(kept, evt)
			if evt.EventType == runtimeevents.EventTurnCompleted.String() {
				survivingTurns = append(survivingTurns, turnRef{
					runID:    runID,
					idx:      len(kept) - 1,
					sequence: evt.Sequence,
				})
			}
		}
		// Zero out slack so the GC can reclaim the dropped events.
		for i := len(kept); i < len(list); i++ {
			list[i] = types.TaskRunEvent{}
		}
		s.events[runID] = kept
	}

	if maxCount > 0 && len(survivingTurns) > maxCount {
		sort.Slice(survivingTurns, func(i, j int) bool {
			// Newest first so we can drop the tail.
			return survivingTurns[i].sequence > survivingTurns[j].sequence
		})
		// Mark old ones for deletion.
		toDrop := survivingTurns[maxCount:]
		dropSet := make(map[string]map[int64]struct{}, len(toDrop))
		for _, ref := range toDrop {
			if _, ok := dropSet[ref.runID]; !ok {
				dropSet[ref.runID] = make(map[int64]struct{})
			}
			dropSet[ref.runID][ref.sequence] = struct{}{}
		}
		for runID, seqs := range dropSet {
			list := s.events[runID]
			kept := list[:0]
			for _, evt := range list {
				if evt.EventType == runtimeevents.EventTurnCompleted.String() {
					if _, ok := seqs[evt.Sequence]; ok {
						deleted++
						continue
					}
				}
				kept = append(kept, evt)
			}
			for i := len(kept); i < len(list); i++ {
				list[i] = types.TaskRunEvent{}
			}
			s.events[runID] = kept
		}
	}

	return deleted, nil
}
