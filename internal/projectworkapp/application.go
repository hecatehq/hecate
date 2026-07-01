package projectworkapp

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/projectruntime"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/pkg/types"
)

var (
	ErrStoreNotConfigured       = errors.New("project work store is not configured")
	ErrTaskStoreNotConfigured   = errors.New("task store is not configured")
	ErrRunnerNotConfigured      = errors.New("task runner is not configured")
	ErrChatStoreNotConfigured   = errors.New("agent chat store is not configured")
	ErrAgentRunnerNotConfigured = errors.New("agent chat runner is not configured")
	ErrAssignmentStartConflict  = errors.New("project assignment start conflicts with current state")
)

type TaskStore interface {
	TaskRunLookupStore
	CreateTask(ctx context.Context, task types.Task) (types.Task, error)
}

type TaskRunLookupStore interface {
	GetRun(ctx context.Context, taskID, runID string) (types.TaskRun, bool, error)
}

type TaskRunner interface {
	StartTaskWithRunInitializer(ctx context.Context, task types.Task, idgen func(string) string, init func(*types.TaskRun)) (*orchestrator.StartTaskResult, error)
}

type AgentRunner interface {
	PrepareSession(context.Context, agentadapters.PrepareSessionRequest) (agentadapters.PrepareSessionResult, error)
	CloseSession(context.Context, string) error
	DeleteSession(context.Context, string) error
}

type ChatSessionStore interface {
	Create(ctx context.Context, session chat.Session) (chat.Session, error)
	UpdateSession(ctx context.Context, id string, update func(*chat.Session)) (chat.Session, error)
	Delete(ctx context.Context, id string) error
}

type Application struct {
	store               projectwork.Store
	taskStore           TaskStore
	runner              TaskRunner
	chatStore           ChatSessionStore
	agentRunner         AgentRunner
	profileStore        AgentProfileStore
	memoryStore         ProjectMemoryStore
	skillStore          ProjectSkillStore
	runtimeStore        projectruntime.Store
	prepareTimeout      time.Duration
	runtimeDefaultModel string
	idgen               func(string) string
	now                 func() time.Time
}

type Options struct {
	Store               projectwork.Store
	TaskStore           TaskStore
	Runner              TaskRunner
	ChatStore           ChatSessionStore
	AgentRunner         AgentRunner
	ProfileStore        AgentProfileStore
	MemoryStore         ProjectMemoryStore
	SkillStore          ProjectSkillStore
	RuntimeStore        projectruntime.Store
	PrepareTimeout      time.Duration
	RuntimeDefaultModel string
	IDGenerator         func(string) string
	Now                 func() time.Time
}

type CreateRoleCommand struct {
	ID                  string
	Name                string
	Description         string
	Instructions        string
	DefaultDriverKind   string
	DefaultProvider     string
	DefaultModel        string
	DefaultAgentProfile string
	SkillIDs            []string
}

type UpdateRoleCommand struct {
	Name                *string
	Description         *string
	Instructions        *string
	DefaultDriverKind   *string
	DefaultProvider     *string
	DefaultModel        *string
	DefaultAgentProfile *string
	SkillIDs            []string
}

type CreateWorkItemCommand struct {
	ID              string
	Title           string
	Brief           string
	Status          string
	Priority        string
	OwnerRoleID     string
	RootID          string
	ReviewerRoleIDs []string
}

type UpdateWorkItemCommand struct {
	Title           *string
	Brief           *string
	Status          *string
	Priority        *string
	OwnerRoleID     *string
	RootID          *string
	ReviewerRoleIDs *[]string
}

type CreateAssignmentCommand struct {
	ID           string
	RoleID       string
	RootID       string
	DriverKind   string
	Status       string
	ExecutionRef projectwork.AssignmentExecutionRef
	StartedAt    time.Time
	CompletedAt  time.Time
}

type UpdateAssignmentCommand struct {
	RoleID       *string
	RootID       *string
	DriverKind   *string
	Status       *string
	ExecutionRef *projectwork.AssignmentExecutionRef
	StartedAt    *time.Time
	CompletedAt  *time.Time
}

type CreateArtifactCommand struct {
	ID                     string
	AssignmentID           string
	Kind                   string
	Title                  string
	Body                   string
	AuthorRoleID           string
	EvidenceSourceKind     string
	EvidenceURL            string
	EvidenceExternalID     string
	EvidenceProvider       string
	EvidenceTrustLabel     string
	ReviewedAssignmentID   string
	ReviewVerdict          string
	ReviewRisk             string
	ReviewFollowUpRequired bool
}

type CreateHandoffCommand struct {
	ID                    string
	SourceAssignmentID    string
	SourceRunID           string
	SourceChatSessionID   string
	SourceMessageID       string
	TargetRoleID          string
	TargetAssignmentID    string
	TargetWorkItemID      string
	Title                 string
	Summary               string
	RecommendedNextAction string
	LinkedArtifactIDs     []string
	LinkedMemoryIDs       []string
	ContextRefs           []string
	Status                string
	ProvenanceKind        string
	TrustLabel            string
	CreatedByRoleID       string
}

type UpdateHandoffCommand struct {
	SourceAssignmentID    *string
	SourceRunID           *string
	SourceChatSessionID   *string
	SourceMessageID       *string
	TargetRoleID          *string
	TargetAssignmentID    *string
	TargetWorkItemID      *string
	Title                 *string
	Summary               *string
	RecommendedNextAction *string
	LinkedArtifactIDs     *[]string
	LinkedMemoryIDs       *[]string
	ContextRefs           *[]string
	Status                *string
	ProvenanceKind        *string
	TrustLabel            *string
	CreatedByRoleID       *string
}

type StartTaskAssignmentCommand struct {
	ProjectID         string
	WorkItemID        string
	Assignment        projectwork.Assignment
	ContextSnapshotID string
	BuildTask         func(taskID string) (types.Task, error)
	OnTaskCreated     func(types.Task)
	InitializeRun     func(types.Task, *types.TaskRun)
}

type StartTaskAssignmentResult struct {
	Assignment projectwork.Assignment
	Task       types.Task
	Run        types.TaskRun
	TraceID    string
	SpanID     string
}

type StartExternalAgentAssignmentCommand struct {
	ProjectID         string
	Assignment        projectwork.Assignment
	Session           chat.Session
	ContextSnapshotID string
	ContextPacket     []byte
}

type StartExternalAgentAssignmentResult struct {
	Assignment projectwork.Assignment
	Session    chat.Session
}

type ExternalAgentPrepareError struct {
	Err error
}

func (e ExternalAgentPrepareError) Error() string {
	if e.Err == nil {
		return "external agent prepare failed"
	}
	return e.Err.Error()
}

func (e ExternalAgentPrepareError) Unwrap() error {
	return e.Err
}

func New(opts Options) *Application {
	app := &Application{
		store:               opts.Store,
		taskStore:           opts.TaskStore,
		runner:              opts.Runner,
		chatStore:           opts.ChatStore,
		agentRunner:         opts.AgentRunner,
		profileStore:        opts.ProfileStore,
		memoryStore:         opts.MemoryStore,
		skillStore:          opts.SkillStore,
		runtimeStore:        opts.RuntimeStore,
		prepareTimeout:      opts.PrepareTimeout,
		runtimeDefaultModel: strings.TrimSpace(opts.RuntimeDefaultModel),
		idgen:               opts.IDGenerator,
		now:                 opts.Now,
	}
	if app.idgen == nil {
		app.idgen = func(prefix string) string { return strings.TrimSpace(prefix) }
	}
	if app.now == nil {
		app.now = func() time.Time { return time.Now().UTC() }
	}
	return app
}

func (app *Application) CreateRole(ctx context.Context, projectID string, cmd CreateRoleCommand) (projectwork.AgentRoleProfile, error) {
	if app == nil || app.store == nil {
		return projectwork.AgentRoleProfile{}, ErrStoreNotConfigured
	}
	id := strings.TrimSpace(cmd.ID)
	if id == "" {
		id = app.idgen("role")
	}
	return app.store.CreateRole(ctx, projectwork.AgentRoleProfile{
		ID:                  id,
		ProjectID:           projectID,
		Name:                cmd.Name,
		Description:         cmd.Description,
		Instructions:        cmd.Instructions,
		DefaultDriverKind:   cmd.DefaultDriverKind,
		DefaultProvider:     cmd.DefaultProvider,
		DefaultModel:        cmd.DefaultModel,
		DefaultAgentProfile: cmd.DefaultAgentProfile,
		SkillIDs:            append([]string(nil), cmd.SkillIDs...),
	})
}

func (app *Application) UpdateRole(ctx context.Context, projectID, roleID string, cmd UpdateRoleCommand) (projectwork.AgentRoleProfile, error) {
	if app == nil || app.store == nil {
		return projectwork.AgentRoleProfile{}, ErrStoreNotConfigured
	}
	return app.store.UpdateRole(ctx, projectID, roleID, func(item *projectwork.AgentRoleProfile) {
		if cmd.Name != nil {
			item.Name = *cmd.Name
		}
		if cmd.Description != nil {
			item.Description = *cmd.Description
		}
		if cmd.Instructions != nil {
			item.Instructions = *cmd.Instructions
		}
		if cmd.DefaultDriverKind != nil {
			item.DefaultDriverKind = *cmd.DefaultDriverKind
		}
		if cmd.DefaultProvider != nil {
			item.DefaultProvider = *cmd.DefaultProvider
		}
		if cmd.DefaultModel != nil {
			item.DefaultModel = *cmd.DefaultModel
		}
		if cmd.DefaultAgentProfile != nil {
			item.DefaultAgentProfile = *cmd.DefaultAgentProfile
		}
		if cmd.SkillIDs != nil {
			item.SkillIDs = append([]string(nil), cmd.SkillIDs...)
		}
	})
}

func (app *Application) DeleteRole(ctx context.Context, projectID, roleID string) error {
	if app == nil || app.store == nil {
		return ErrStoreNotConfigured
	}
	return app.store.DeleteRole(ctx, projectID, roleID)
}

func (app *Application) CreateWorkItem(ctx context.Context, projectID string, cmd CreateWorkItemCommand) (projectwork.WorkItem, error) {
	if app == nil || app.store == nil {
		return projectwork.WorkItem{}, ErrStoreNotConfigured
	}
	id := strings.TrimSpace(cmd.ID)
	if id == "" {
		id = app.idgen("work")
	}
	return app.store.CreateWorkItem(ctx, projectwork.WorkItem{
		ID:              id,
		ProjectID:       projectID,
		Title:           cmd.Title,
		Brief:           cmd.Brief,
		Status:          cmd.Status,
		Priority:        cmd.Priority,
		OwnerRoleID:     cmd.OwnerRoleID,
		RootID:          cmd.RootID,
		ReviewerRoleIDs: append([]string(nil), cmd.ReviewerRoleIDs...),
	})
}

func (app *Application) UpdateWorkItem(ctx context.Context, projectID, workItemID string, cmd UpdateWorkItemCommand) (projectwork.WorkItem, error) {
	if app == nil || app.store == nil {
		return projectwork.WorkItem{}, ErrStoreNotConfigured
	}
	if cmd.Status != nil && strings.TrimSpace(*cmd.Status) == projectwork.WorkItemStatusDone {
		readiness, err := app.WorkItemReadiness(ctx, projectID, workItemID)
		if err != nil {
			return projectwork.WorkItem{}, err
		}
		if readiness.Status != "done" && !readiness.Ready {
			return projectwork.WorkItem{}, WorkItemCloseoutBlockedError{Readiness: readiness}
		}
	}
	return app.store.UpdateWorkItem(ctx, projectID, workItemID, func(item *projectwork.WorkItem) {
		if cmd.Title != nil {
			item.Title = *cmd.Title
		}
		if cmd.Brief != nil {
			item.Brief = *cmd.Brief
		}
		if cmd.Status != nil {
			item.Status = *cmd.Status
		}
		if cmd.Priority != nil {
			item.Priority = *cmd.Priority
		}
		if cmd.OwnerRoleID != nil {
			item.OwnerRoleID = *cmd.OwnerRoleID
		}
		if cmd.RootID != nil {
			item.RootID = *cmd.RootID
		}
		if cmd.ReviewerRoleIDs != nil {
			item.ReviewerRoleIDs = append([]string(nil), *cmd.ReviewerRoleIDs...)
		}
	})
}

func (app *Application) DeleteWorkItem(ctx context.Context, projectID, workItemID string) error {
	if app == nil || app.store == nil {
		return ErrStoreNotConfigured
	}
	return app.store.DeleteWorkItem(ctx, projectID, workItemID)
}

func (app *Application) CreateAssignment(ctx context.Context, projectID, workItemID string, cmd CreateAssignmentCommand) (projectwork.Assignment, error) {
	if app == nil || app.store == nil {
		return projectwork.Assignment{}, ErrStoreNotConfigured
	}
	id := strings.TrimSpace(cmd.ID)
	if id == "" {
		id = app.idgen("asgn")
	}
	driverKind := strings.TrimSpace(cmd.DriverKind)
	if driverKind == "" {
		if role, ok, err := app.loadRole(ctx, projectID, cmd.RoleID); err != nil {
			return projectwork.Assignment{}, err
		} else if ok {
			driverKind = role.DefaultDriverKind
		}
	}
	assignment, err := app.store.CreateAssignment(ctx, projectwork.Assignment{
		ID:           id,
		ProjectID:    projectID,
		WorkItemID:   workItemID,
		RoleID:       cmd.RoleID,
		RootID:       cmd.RootID,
		DriverKind:   driverKind,
		Status:       cmd.Status,
		ExecutionRef: cmd.ExecutionRef,
		StartedAt:    cmd.StartedAt,
		CompletedAt:  cmd.CompletedAt,
	})
	if err != nil {
		return projectwork.Assignment{}, err
	}
	return app.persistAssignmentRuntime(ctx, assignment)
}

func (app *Application) UpdateAssignment(ctx context.Context, projectID, assignmentID string, cmd UpdateAssignmentCommand) (projectwork.Assignment, error) {
	if app == nil || app.store == nil {
		return projectwork.Assignment{}, ErrStoreNotConfigured
	}
	assignment, err := app.store.UpdateAssignment(ctx, projectID, assignmentID, func(item *projectwork.Assignment) {
		if cmd.RoleID != nil {
			item.RoleID = *cmd.RoleID
		}
		if cmd.RootID != nil {
			item.RootID = *cmd.RootID
		}
		if cmd.DriverKind != nil {
			item.DriverKind = *cmd.DriverKind
		}
		if cmd.Status != nil {
			item.Status = *cmd.Status
		}
		if cmd.ExecutionRef != nil {
			item.ExecutionRef = *cmd.ExecutionRef
		}
		if cmd.StartedAt != nil {
			item.StartedAt = *cmd.StartedAt
		}
		if cmd.CompletedAt != nil {
			item.CompletedAt = *cmd.CompletedAt
		}
	})
	if err != nil {
		return projectwork.Assignment{}, err
	}
	if cmd.ExecutionRef != nil || cmd.StartedAt != nil || cmd.CompletedAt != nil {
		return app.persistAssignmentRuntime(ctx, assignment)
	}
	return app.ApplyAssignmentRuntime(ctx, assignment)
}

func (app *Application) DeleteAssignment(ctx context.Context, projectID, workItemID, assignmentID string) error {
	if app == nil || app.store == nil {
		return ErrStoreNotConfigured
	}
	if err := app.store.DeleteAssignment(ctx, projectID, workItemID, assignmentID); err != nil {
		return err
	}
	return app.deleteAssignmentRuntime(ctx, projectID, assignmentID)
}

func (app *Application) CreateArtifact(ctx context.Context, projectID, workItemID string, cmd CreateArtifactCommand) (projectwork.CollaborationArtifact, error) {
	if app == nil || app.store == nil {
		return projectwork.CollaborationArtifact{}, ErrStoreNotConfigured
	}
	id := strings.TrimSpace(cmd.ID)
	if id == "" {
		id = app.idgen("art")
	}
	return app.store.CreateArtifact(ctx, projectwork.CollaborationArtifact{
		ID:                     id,
		ProjectID:              projectID,
		WorkItemID:             workItemID,
		AssignmentID:           cmd.AssignmentID,
		Kind:                   cmd.Kind,
		Title:                  cmd.Title,
		Body:                   cmd.Body,
		AuthorRoleID:           cmd.AuthorRoleID,
		EvidenceSourceKind:     cmd.EvidenceSourceKind,
		EvidenceURL:            cmd.EvidenceURL,
		EvidenceExternalID:     cmd.EvidenceExternalID,
		EvidenceProvider:       cmd.EvidenceProvider,
		EvidenceTrustLabel:     cmd.EvidenceTrustLabel,
		ReviewedAssignmentID:   cmd.ReviewedAssignmentID,
		ReviewVerdict:          cmd.ReviewVerdict,
		ReviewRisk:             cmd.ReviewRisk,
		ReviewFollowUpRequired: cmd.ReviewFollowUpRequired,
	})
}

func (app *Application) CreateHandoff(ctx context.Context, projectID, workItemID string, cmd CreateHandoffCommand) (projectwork.Handoff, error) {
	if app == nil || app.store == nil {
		return projectwork.Handoff{}, ErrStoreNotConfigured
	}
	id := strings.TrimSpace(cmd.ID)
	if id == "" {
		id = app.idgen("handoff")
	}
	return app.store.CreateHandoff(ctx, projectwork.Handoff{
		ID:                    id,
		ProjectID:             projectID,
		WorkItemID:            workItemID,
		SourceAssignmentID:    cmd.SourceAssignmentID,
		SourceRunID:           cmd.SourceRunID,
		SourceChatSessionID:   cmd.SourceChatSessionID,
		SourceMessageID:       cmd.SourceMessageID,
		TargetRoleID:          cmd.TargetRoleID,
		TargetAssignmentID:    cmd.TargetAssignmentID,
		TargetWorkItemID:      cmd.TargetWorkItemID,
		Title:                 cmd.Title,
		Summary:               cmd.Summary,
		RecommendedNextAction: cmd.RecommendedNextAction,
		LinkedArtifactIDs:     append([]string(nil), cmd.LinkedArtifactIDs...),
		LinkedMemoryIDs:       append([]string(nil), cmd.LinkedMemoryIDs...),
		ContextRefs:           append([]string(nil), cmd.ContextRefs...),
		Status:                cmd.Status,
		ProvenanceKind:        cmd.ProvenanceKind,
		TrustLabel:            cmd.TrustLabel,
		CreatedByRoleID:       cmd.CreatedByRoleID,
	})
}

func (app *Application) UpdateHandoff(ctx context.Context, projectID, workItemID, handoffID string, cmd UpdateHandoffCommand) (projectwork.Handoff, error) {
	if app == nil || app.store == nil {
		return projectwork.Handoff{}, ErrStoreNotConfigured
	}
	return app.store.UpdateHandoff(ctx, projectID, workItemID, handoffID, func(item *projectwork.Handoff) {
		if cmd.SourceAssignmentID != nil {
			item.SourceAssignmentID = *cmd.SourceAssignmentID
		}
		if cmd.SourceRunID != nil {
			item.SourceRunID = *cmd.SourceRunID
		}
		if cmd.SourceChatSessionID != nil {
			item.SourceChatSessionID = *cmd.SourceChatSessionID
		}
		if cmd.SourceMessageID != nil {
			item.SourceMessageID = *cmd.SourceMessageID
		}
		if cmd.TargetRoleID != nil {
			item.TargetRoleID = *cmd.TargetRoleID
		}
		if cmd.TargetAssignmentID != nil {
			item.TargetAssignmentID = *cmd.TargetAssignmentID
		}
		if cmd.TargetWorkItemID != nil {
			item.TargetWorkItemID = *cmd.TargetWorkItemID
		}
		if cmd.Title != nil {
			item.Title = *cmd.Title
		}
		if cmd.Summary != nil {
			item.Summary = *cmd.Summary
		}
		if cmd.RecommendedNextAction != nil {
			item.RecommendedNextAction = *cmd.RecommendedNextAction
		}
		if cmd.LinkedArtifactIDs != nil {
			item.LinkedArtifactIDs = append([]string(nil), *cmd.LinkedArtifactIDs...)
		}
		if cmd.LinkedMemoryIDs != nil {
			item.LinkedMemoryIDs = append([]string(nil), *cmd.LinkedMemoryIDs...)
		}
		if cmd.ContextRefs != nil {
			item.ContextRefs = append([]string(nil), *cmd.ContextRefs...)
		}
		if cmd.Status != nil {
			item.Status = *cmd.Status
		}
		if cmd.ProvenanceKind != nil {
			item.ProvenanceKind = *cmd.ProvenanceKind
		}
		if cmd.TrustLabel != nil {
			item.TrustLabel = *cmd.TrustLabel
		}
		if cmd.CreatedByRoleID != nil {
			item.CreatedByRoleID = *cmd.CreatedByRoleID
		}
	})
}

func (app *Application) DeleteHandoff(ctx context.Context, projectID, workItemID, handoffID string) error {
	if app == nil || app.store == nil {
		return ErrStoreNotConfigured
	}
	return app.store.DeleteHandoff(ctx, projectID, workItemID, handoffID)
}

func (app *Application) StartTaskAssignment(ctx context.Context, cmd StartTaskAssignmentCommand) (*StartTaskAssignmentResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if app.taskStore == nil {
		return nil, ErrTaskStoreNotConfigured
	}
	if app.runner == nil {
		return nil, ErrRunnerNotConfigured
	}
	assignmentWithRuntime, err := app.ApplyAssignmentRuntime(ctx, cmd.Assignment)
	if err != nil {
		return nil, err
	}
	cmd.Assignment = assignmentWithRuntime
	if AssignmentIsTerminal(cmd.Assignment.Status) || cmd.Assignment.DriverKind != projectwork.AssignmentDriverHecateTask {
		return &StartTaskAssignmentResult{Assignment: cmd.Assignment}, ErrAssignmentStartConflict
	}
	active, err := AssignmentHasActiveExecution(ctx, app.taskStore, cmd.Assignment)
	if err != nil {
		return nil, err
	}
	if active {
		return &StartTaskAssignmentResult{Assignment: cmd.Assignment}, ErrAssignmentStartConflict
	}

	taskID := app.idgen("task")
	claimRejected := false
	assignment, err := app.store.UpdateAssignment(ctx, cmd.ProjectID, cmd.Assignment.ID, func(item *projectwork.Assignment) {
		ref := projectwork.NormalizeAssignmentExecutionRef(item.ExecutionRef)
		if ref.TaskID != "" || ref.RunID != "" || AssignmentIsTerminal(item.Status) || item.DriverKind != projectwork.AssignmentDriverHecateTask {
			claimRejected = true
			return
		}
		item.ExecutionRef = projectwork.AssignmentExecutionRef{
			Kind:   projectwork.AssignmentExecutionKindTaskRun,
			TaskID: taskID,
			Status: projectwork.AssignmentStatusQueued,
		}
		item.Status = projectwork.AssignmentStatusQueued
		if item.StartedAt.IsZero() {
			item.StartedAt = app.now().UTC()
		}
	})
	if err != nil {
		return nil, err
	}
	if assignment, err = app.persistAssignmentRuntime(ctx, assignment); err != nil {
		return nil, err
	}
	if claimRejected {
		return &StartTaskAssignmentResult{Assignment: assignment}, ErrAssignmentStartConflict
	}

	task, err := cmd.BuildTask(taskID)
	if err != nil {
		assignment, updateErr := app.clearTaskClaim(ctx, cmd.ProjectID, cmd.Assignment.ID, taskID)
		if updateErr != nil {
			return &StartTaskAssignmentResult{Assignment: assignment}, errors.Join(err, updateErr)
		}
		return &StartTaskAssignmentResult{Assignment: assignment}, err
	}
	task, err = app.taskStore.CreateTask(ctx, task)
	if err != nil {
		assignment, updateErr := app.clearTaskClaim(ctx, cmd.ProjectID, cmd.Assignment.ID, taskID)
		if updateErr != nil {
			return &StartTaskAssignmentResult{Assignment: assignment}, errors.Join(err, updateErr)
		}
		return &StartTaskAssignmentResult{Assignment: assignment}, err
	}
	if cmd.OnTaskCreated != nil {
		cmd.OnTaskCreated(task)
	}

	result, err := app.runner.StartTaskWithRunInitializer(ctx, task, app.idgen, func(run *types.TaskRun) {
		if cmd.InitializeRun != nil {
			cmd.InitializeRun(task, run)
		}
	})
	if err != nil {
		assignment, updateErr := app.store.UpdateAssignment(ctx, cmd.ProjectID, cmd.Assignment.ID, func(item *projectwork.Assignment) {
			item.ExecutionRef = projectwork.AssignmentExecutionRef{
				Kind:   projectwork.AssignmentExecutionKindTaskRun,
				TaskID: task.ID,
				Status: projectwork.AssignmentStatusFailed,
			}
			item.Status = projectwork.AssignmentStatusFailed
			item.CompletedAt = app.now().UTC()
		})
		if updateErr == nil {
			assignment, updateErr = app.persistAssignmentRuntime(ctx, assignment)
		}
		if updateErr != nil {
			return &StartTaskAssignmentResult{Assignment: assignment, Task: task}, errors.Join(err, updateErr)
		}
		return &StartTaskAssignmentResult{Assignment: assignment, Task: task}, err
	}

	assignment, err = app.store.UpdateAssignment(ctx, cmd.ProjectID, cmd.Assignment.ID, func(item *projectwork.Assignment) {
		status := AssignmentStatusFromRun(result.Run.Status)
		item.ExecutionRef = projectwork.AssignmentExecutionRef{
			Kind:              projectwork.AssignmentExecutionKindTaskRun,
			TaskID:            result.Task.ID,
			RunID:             result.Run.ID,
			ContextSnapshotID: cmd.ContextSnapshotID,
			Status:            status,
			TraceID:           result.Run.TraceID,
		}
		item.Status = status
		if item.StartedAt.IsZero() {
			item.StartedAt = app.now().UTC()
		}
	})
	if err == nil {
		assignment, err = app.persistAssignmentRuntime(ctx, assignment)
	}
	if err != nil {
		return &StartTaskAssignmentResult{Task: result.Task, Run: result.Run, TraceID: result.TraceID, SpanID: result.SpanID}, err
	}
	return &StartTaskAssignmentResult{
		Assignment: assignment,
		Task:       result.Task,
		Run:        result.Run,
		TraceID:    result.TraceID,
		SpanID:     result.SpanID,
	}, nil
}

func (app *Application) StartExternalAgentAssignment(ctx context.Context, cmd StartExternalAgentAssignmentCommand) (*StartExternalAgentAssignmentResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if app.chatStore == nil {
		return nil, ErrChatStoreNotConfigured
	}
	if app.agentRunner == nil {
		return nil, ErrAgentRunnerNotConfigured
	}
	assignmentWithRuntime, err := app.ApplyAssignmentRuntime(ctx, cmd.Assignment)
	if err != nil {
		return nil, err
	}
	cmd.Assignment = assignmentWithRuntime
	if strings.TrimSpace(cmd.Assignment.ExecutionRef.ChatSessionID) != "" ||
		AssignmentIsTerminal(cmd.Assignment.Status) ||
		cmd.Assignment.DriverKind != projectwork.AssignmentDriverExternalAgent {
		return &StartExternalAgentAssignmentResult{Assignment: cmd.Assignment}, ErrAssignmentStartConflict
	}

	session, err := app.chatStore.Create(ctx, cmd.Session)
	if err != nil {
		return nil, err
	}
	prepareCtx := ctx
	cancel := func() {}
	if app.prepareTimeout > 0 {
		prepareCtx, cancel = context.WithTimeout(ctx, app.prepareTimeout)
	}
	prepared, prepareErr := app.agentRunner.PrepareSession(prepareCtx, agentadapters.PrepareSessionRequest{
		SessionID:     session.ID,
		AdapterID:     session.AgentID,
		Workspace:     session.Workspace,
		ConfigOptions: session.ConfigOptions,
		MCPServers:    session.MCPServers,
	})
	cancel()
	if prepareErr != nil {
		_ = app.chatStore.Delete(context.Background(), session.ID)
		return &StartExternalAgentAssignmentResult{Session: session}, ExternalAgentPrepareError{Err: prepareErr}
	}

	session, err = app.chatStore.UpdateSession(ctx, session.ID, func(item *chat.Session) {
		item.DriverKind = prepared.DriverKind
		item.NativeSessionID = prepared.NativeSessionID
		item.ConfigOptions = prepared.ConfigOptions
	})
	if err != nil {
		app.cleanupExternalSession(session.ID)
		return &StartExternalAgentAssignmentResult{Session: session}, err
	}

	assignment, err := app.store.UpdateAssignment(ctx, cmd.ProjectID, cmd.Assignment.ID, func(item *projectwork.Assignment) {
		ref := projectwork.NormalizeAssignmentExecutionRef(item.ExecutionRef)
		if ref.ChatSessionID != "" || AssignmentIsTerminal(item.Status) || item.DriverKind != projectwork.AssignmentDriverExternalAgent {
			return
		}
		item.ExecutionRef = projectwork.AssignmentExecutionRef{
			Kind:              projectwork.AssignmentExecutionKindChatSession,
			ChatSessionID:     session.ID,
			ContextSnapshotID: cmd.ContextSnapshotID,
			Status:            projectwork.AssignmentStatusRunning,
		}
		item.ContextPacket = append([]byte(nil), cmd.ContextPacket...)
		item.Status = projectwork.AssignmentStatusRunning
		if item.StartedAt.IsZero() {
			item.StartedAt = app.now().UTC()
		}
	})
	if err == nil {
		assignment, err = app.persistAssignmentRuntime(ctx, assignment)
	}
	if err != nil {
		app.cleanupExternalSession(session.ID)
		return &StartExternalAgentAssignmentResult{Session: session}, err
	}
	if assignment.ExecutionRef.ChatSessionID != session.ID {
		app.cleanupExternalSession(session.ID)
		return &StartExternalAgentAssignmentResult{Assignment: assignment, Session: session}, ErrAssignmentStartConflict
	}
	return &StartExternalAgentAssignmentResult{Assignment: assignment, Session: session}, nil
}

func (app *Application) ApplyAssignmentRuntime(ctx context.Context, assignment projectwork.Assignment) (projectwork.Assignment, error) {
	if app == nil || app.runtimeStore == nil || strings.TrimSpace(assignment.ProjectID) == "" || strings.TrimSpace(assignment.ID) == "" {
		return assignment, nil
	}
	runtime, ok, err := app.runtimeStore.Get(ctx, assignment.ProjectID, assignment.ID)
	if err != nil {
		return projectwork.Assignment{}, err
	}
	if !ok {
		return assignment, nil
	}
	return projectruntime.Apply(assignment, runtime), nil
}

func (app *Application) ApplyAssignmentsRuntime(ctx context.Context, assignments []projectwork.Assignment) ([]projectwork.Assignment, error) {
	if app == nil || app.runtimeStore == nil || len(assignments) == 0 {
		return assignments, nil
	}
	out := make([]projectwork.Assignment, 0, len(assignments))
	for _, assignment := range assignments {
		overlaid, err := app.ApplyAssignmentRuntime(ctx, assignment)
		if err != nil {
			return nil, err
		}
		out = append(out, overlaid)
	}
	return out, nil
}

func (app *Application) persistAssignmentRuntime(ctx context.Context, assignment projectwork.Assignment) (projectwork.Assignment, error) {
	if app == nil || app.runtimeStore == nil {
		return assignment, nil
	}
	runtime, err := app.runtimeStore.Upsert(ctx, projectruntime.FromAssignment(assignment))
	if err != nil {
		return projectwork.Assignment{}, err
	}
	return projectruntime.Apply(assignment, runtime), nil
}

func (app *Application) deleteAssignmentRuntime(ctx context.Context, projectID, assignmentID string) error {
	if app == nil || app.runtimeStore == nil {
		return nil
	}
	err := app.runtimeStore.Delete(ctx, projectID, assignmentID)
	if errors.Is(err, projectruntime.ErrNotFound) {
		return nil
	}
	return err
}

func (app *Application) loadRole(ctx context.Context, projectID, roleID string) (projectwork.AgentRoleProfile, bool, error) {
	roles, err := app.store.ListRoles(ctx, projectID)
	if err != nil {
		return projectwork.AgentRoleProfile{}, false, err
	}
	roleID = strings.TrimSpace(roleID)
	for _, role := range roles {
		if role.ID == roleID {
			return role, true, nil
		}
	}
	return projectwork.AgentRoleProfile{}, false, nil
}

func (app *Application) clearTaskClaim(ctx context.Context, projectID, assignmentID, taskID string) (projectwork.Assignment, error) {
	assignment, err := app.store.UpdateAssignment(ctx, projectID, assignmentID, func(item *projectwork.Assignment) {
		ref := projectwork.NormalizeAssignmentExecutionRef(item.ExecutionRef)
		if ref.TaskID == taskID && ref.RunID == "" {
			item.ExecutionRef = projectwork.AssignmentExecutionRef{}
			item.Status = projectwork.AssignmentStatusQueued
			item.StartedAt = time.Time{}
			item.CompletedAt = time.Time{}
		}
	})
	if err != nil {
		return projectwork.Assignment{}, err
	}
	if err := app.deleteAssignmentRuntime(ctx, projectID, assignmentID); err != nil {
		return assignment, err
	}
	return assignment, nil
}

func (app *Application) cleanupExternalSession(sessionID string) {
	cleanupCtx := context.Background()
	cancel := func() {}
	if app.prepareTimeout > 0 {
		cleanupCtx, cancel = context.WithTimeout(cleanupCtx, app.prepareTimeout)
	}
	_ = app.agentRunner.DeleteSession(cleanupCtx, sessionID)
	cancel()
	_ = app.chatStore.Delete(context.Background(), sessionID)
}

func AssignmentIsTerminal(status string) bool {
	switch strings.TrimSpace(status) {
	case projectwork.AssignmentStatusCompleted, projectwork.AssignmentStatusFailed, projectwork.AssignmentStatusCancelled:
		return true
	default:
		return false
	}
}

func AssignmentStatusFromRun(status string) string {
	switch strings.TrimSpace(status) {
	case "awaiting_approval":
		return projectwork.AssignmentStatusAwaitingApproval
	case "running":
		return projectwork.AssignmentStatusRunning
	case "completed":
		return projectwork.AssignmentStatusCompleted
	case "failed":
		return projectwork.AssignmentStatusFailed
	case "cancelled":
		return projectwork.AssignmentStatusCancelled
	default:
		return projectwork.AssignmentStatusQueued
	}
}

func AssignmentHasActiveExecution(ctx context.Context, store TaskRunLookupStore, assignment projectwork.Assignment) (bool, error) {
	ref := projectwork.NormalizeAssignmentExecutionRef(assignment.ExecutionRef)
	if strings.TrimSpace(ref.RunID) != "" && strings.TrimSpace(ref.TaskID) != "" && store == nil {
		return false, ErrTaskStoreNotConfigured
	}
	if strings.TrimSpace(ref.RunID) != "" && strings.TrimSpace(ref.TaskID) != "" {
		run, ok, err := store.GetRun(ctx, ref.TaskID, ref.RunID)
		if err != nil {
			return false, err
		}
		if ok {
			return !types.IsTerminalTaskRunStatus(run.Status), nil
		}
	}
	switch strings.TrimSpace(assignment.Status) {
	case projectwork.AssignmentStatusRunning, projectwork.AssignmentStatusAwaitingApproval:
		return true, nil
	case projectwork.AssignmentStatusQueued:
		return strings.TrimSpace(ref.TaskID) != "" || strings.TrimSpace(ref.RunID) != "", nil
	default:
		return false, nil
	}
}
