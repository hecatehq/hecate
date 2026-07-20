package taskapp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/apperrors"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/secrets"
	"github.com/hecatehq/hecate/internal/taskruncoord"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/taskworkflow"
	"github.com/hecatehq/hecate/pkg/types"
)

const codingAgentProfileSystemPrompt = `You are running inside Hecate's coding-agent runtime.

Use read_file and list_dir before editing. Prefer file_edit for targeted changes and file_write only for new files or full rewrites. Keep changes scoped to the user's request. Explain important tradeoffs in the final answer, and mention files changed when useful.`

var (
	ErrStoreNotConfigured           = errors.New("task store is not configured")
	ErrRunnerNotConfigured          = errors.New("task runner is not configured")
	ErrProjectStoreNotConfigured    = errors.New("project store is not configured")
	ErrProjectNotFound              = errors.New("project not found")
	ErrTaskNotFound                 = errors.New("task not found")
	ErrRunNotFound                  = errors.New("task run not found")
	ErrApprovalNotFound             = errors.New("task approval not found")
	ErrTaskIDRequired               = errors.New("task id is required")
	ErrRunIDRequired                = errors.New("run id is required")
	ErrApprovalIDRequired           = errors.New("approval id is required")
	ErrScheduleIDRequired           = errors.New("task schedule id is required")
	ErrScheduleOccurrenceIDRequired = errors.New("task schedule occurrence id is required")
	ErrScheduleClaimOwnerRequired   = errors.New("task schedule claim owner is required")
	ErrScheduledForRequired         = errors.New("scheduled_for is required")
	ErrOriginKindRequired           = errors.New("task origin kind is required")
	ErrOriginIDRequired             = errors.New("task origin id is required")
	ErrOriginRunAdmissionClosed     = taskruncoord.ErrOriginRunAdmissionClosed
	ErrOriginUnavailable            = taskruncoord.ErrOriginUnavailable
	ErrOriginValidationFailed       = taskruncoord.ErrOriginValidationFailed
	ErrModelCallIndexRequired       = errors.New("model_call_index must be >= 1")
	ErrPromptRequired               = errors.New("prompt is required")
	// Keep application-facing error names for API callers while making the
	// runtime and create boundary share one fail-closed workflow contract.
	ErrQAWorkflowRequiresAgentLoop = taskworkflow.ErrQARequiresAgentLoop
	ErrQAWorkflowMCPServers        = taskworkflow.ErrQAMCPServers
	ErrQAWorkflowNetwork           = taskworkflow.ErrQANetwork
	ErrQAWorkflowWorkspaceMode     = taskworkflow.ErrQAWorkspaceMode
	ErrActiveRun                   = orchestrator.ErrActiveRun
	ErrOtherActiveRun              = errors.New("task already has another active run")
	ErrDeleteActiveRun             = errors.New("cannot delete a task with an active run; cancel it first")
	ErrRunNotRetryable             = errors.New("run is not retryable until it reaches a terminal state")
	ErrRunNotResumable             = errors.New("run is not resumable")
	ErrRunNotModelCallRetryable    = errors.New("run is not retryable from a model call (must be terminal)")
	ErrBudgetLower                 = orchestrator.ErrBudgetLower
)

type ValidationError = apperrors.ValidationError

func Validation(err error) error {
	return apperrors.Validation(err)
}

func IsValidationError(err error) bool {
	return apperrors.IsValidationError(err)
}

type Runner interface {
	StartTask(context.Context, types.Task, func(string) string) (*orchestrator.StartTaskResult, error)
	RetryTask(context.Context, types.Task, types.TaskRun, func(string) string) (*orchestrator.StartTaskResult, error)
	ResumeTaskWithBudget(context.Context, types.Task, types.TaskRun, string, int64, func(string) string) (*orchestrator.StartTaskResult, error)
	ContinueAgentTask(context.Context, types.Task, types.TaskRun, string, func(string) string) (*orchestrator.StartTaskResult, error)
	RetryTaskFromModelCall(context.Context, types.Task, types.TaskRun, int, string, func(string) string) (*orchestrator.StartTaskResult, error)
	CancelRun(context.Context, types.Task, string, string) (types.TaskRun, error)
	ResolveTaskApproval(context.Context, orchestrator.ResolveApprovalRequest) (*orchestrator.ResolveApprovalResult, error)
}

type scheduledRunner interface {
	StartScheduledTask(context.Context, types.Task, func(string) string, orchestrator.ScheduledTaskStart) (*orchestrator.StartTaskResult, error)
}

type ProjectStore interface {
	Get(context.Context, string) (projects.Project, bool, error)
}

type Application struct {
	store         taskstate.Store
	runner        Runner
	projects      ProjectStore
	secretCipher  secrets.Cipher
	maxMCPServers int
	idgen         func(string) string
	now           func() time.Time
	originRunGate *taskruncoord.Gate
}

type Options struct {
	Store         taskstate.Store
	Runner        Runner
	Projects      ProjectStore
	SecretCipher  secrets.Cipher
	MaxMCPServers int
	IDGenerator   func(string) string
	Now           func() time.Time
	OriginRunGate *taskruncoord.Gate
}

type CreateCommand struct {
	Title              string
	Prompt             string
	ProjectID          string
	SystemPrompt       string
	WorkflowMode       string
	ExecutionProfile   string
	Repo               string
	BaseBranch         string
	WorkspaceMode      string
	ExecutionKind      string
	ShellCommand       string
	GitCommand         string
	WorkingDirectory   string
	FileOperation      string
	FilePath           string
	FileContent        string
	SandboxAllowedRoot string
	SandboxReadOnly    bool
	SandboxNetwork     bool
	TimeoutMS          int
	Priority           string
	RequestedModel     string
	RequestedProvider  string
	BudgetMicrosUSD    int64
	MCPServers         []MCPServerCommand
}

type MCPServerCommand struct {
	Name           string
	Command        string
	Args           []string
	Env            map[string]string
	URL            string
	Headers        map[string]string
	ApprovalPolicy string
}

type ResumeCommand struct {
	Reason          string
	BudgetMicrosUSD int64
}

type ScheduledStartCommand struct {
	ScheduleID           string
	ScheduleOccurrenceID string
	ScheduledFor         time.Time
	ClaimOwner           string
}

type RetryFromModelCallCommand struct {
	ModelCallIndex int
	Reason         string
}

type ResolveApprovalCommand struct {
	Task       types.Task
	ApprovalID string
	Decision   string
	Note       string
	ResolvedBy string
	RequestID  string
}

type CancelRunsByOriginCommand struct {
	OriginKind string
	OriginID   string
	Reason     string
}

type CancelRunsByOriginResult struct {
	Runs []types.TaskRun
}

// OriginRunSettlement keeps new runs fenced until the caller either commits a
// successful owner deletion or releases the fence after a failed deletion.
type OriginRunSettlement struct {
	closure *taskruncoord.Closure
}

func (settlement *OriginRunSettlement) Release() {
	if settlement != nil && settlement.closure != nil {
		settlement.closure.Release()
	}
}

func (settlement *OriginRunSettlement) Commit() {
	if settlement != nil && settlement.closure != nil {
		settlement.closure.Commit()
	}
}

func New(opts Options) *Application {
	app := &Application{
		store:         opts.Store,
		runner:        opts.Runner,
		projects:      opts.Projects,
		secretCipher:  opts.SecretCipher,
		maxMCPServers: opts.MaxMCPServers,
		idgen:         opts.IDGenerator,
		now:           opts.Now,
		originRunGate: opts.OriginRunGate,
	}
	if app.originRunGate == nil {
		app.originRunGate = taskruncoord.NewOriginGate()
	}
	if app.idgen == nil {
		app.idgen = newOpaqueTaskResourceID
	}
	if app.now == nil {
		app.now = func() time.Time { return time.Now().UTC() }
	}
	return app
}

func (app *Application) CreateTask(ctx context.Context, cmd CreateCommand) (types.Task, error) {
	if app == nil || app.store == nil {
		return types.Task{}, ErrStoreNotConfigured
	}
	requestedWorkspaceMode := strings.TrimSpace(cmd.WorkspaceMode)
	applyExecutionProfileDefaults(&cmd)

	title := strings.TrimSpace(cmd.Title)
	prompt := strings.TrimSpace(cmd.Prompt)
	if title == "" {
		if prompt == "" {
			title = "New task"
		} else {
			title = prompt
			if len(title) > 80 {
				title = strings.TrimSpace(title[:80]) + "..."
			}
		}
	}
	effectiveKind := strings.TrimSpace(cmd.ExecutionKind)
	isAgentLoop := effectiveKind == "" || effectiveKind == "agent_loop"
	if prompt == "" && isAgentLoop {
		return types.Task{}, ErrPromptRequired
	}
	workflowMode, err := taskworkflow.ParseMode(cmd.WorkflowMode)
	if err != nil {
		return types.Task{}, Validation(err)
	}
	if taskworkflow.IsQA(workflowMode) {
		if !isAgentLoop {
			return types.Task{}, Validation(ErrQAWorkflowRequiresAgentLoop)
		}
		// The historical empty execution_kind shorthand is treated as an agent
		// loop for validation, but a QA task needs the explicit persisted value:
		// the runner selects executors from this field and must not fall back to
		// a non-agent execution path after the report-only contract was accepted.
		if effectiveKind == "" {
			cmd.ExecutionKind = "agent_loop"
		}
		if len(cmd.MCPServers) > 0 {
			return types.Task{}, Validation(ErrQAWorkflowMCPServers)
		}
		if cmd.SandboxNetwork {
			return types.Task{}, Validation(ErrQAWorkflowNetwork)
		}
		if requestedWorkspaceMode != "" && requestedWorkspaceMode != "ephemeral" {
			return types.Task{}, Validation(ErrQAWorkflowWorkspaceMode)
		}
		// A caller cannot relax QA posture through task fields. We set the
		// values here, before persistence, and the agent loop independently
		// enforces the same tool boundary in case an older record is loaded.
		cmd.SandboxReadOnly = true
		cmd.SandboxNetwork = false
		cmd.WorkspaceMode = "ephemeral"
		cmd.SystemPrompt = taskworkflow.AppendQASystemPrompt(cmd.SystemPrompt)
	}
	workspaceSystemPromptPolicy := ""
	if taskworkflow.IsQA(workflowMode) {
		// Repository guidance is evidence for QA, not privileged instruction.
		// Keep CLAUDE.md/AGENTS.md out of the system-prompt compatibility layer;
		// the QA contract itself is appended above and runtime validation repeats
		// this constraint for persisted rows.
		workspaceSystemPromptPolicy = types.WorkspaceSystemPromptExclude
	}

	mcpServers, err := NormalizeMCPServerConfigs(cmd.MCPServers, app.secretCipher, app.maxMCPServers)
	if err != nil {
		return types.Task{}, Validation(err)
	}

	workspaceMode := strings.TrimSpace(cmd.WorkspaceMode)
	if workspaceMode == "" {
		workspaceMode = "ephemeral"
	}
	priority := strings.TrimSpace(cmd.Priority)
	if priority == "" {
		priority = "normal"
	}
	projectID := strings.TrimSpace(cmd.ProjectID)
	if projectID != "" {
		if app.projects == nil {
			return types.Task{}, ErrProjectStoreNotConfigured
		}
		if _, ok, err := app.projects.Get(ctx, projectID); err != nil {
			return types.Task{}, err
		} else if !ok {
			return types.Task{}, ErrProjectNotFound
		}
	}

	now := app.now().UTC()
	task := types.Task{
		ID:                          app.idgen("task"),
		Title:                       title,
		Prompt:                      prompt,
		ProjectID:                   projectID,
		SystemPrompt:                strings.TrimSpace(cmd.SystemPrompt),
		WorkspaceSystemPromptPolicy: workspaceSystemPromptPolicy,
		WorkflowMode:                workflowMode,
		WorkflowVersion:             taskworkflow.VersionForMode(workflowMode),
		ExecutionProfile:            strings.TrimSpace(cmd.ExecutionProfile),
		Repo:                        strings.TrimSpace(cmd.Repo),
		BaseBranch:                  strings.TrimSpace(cmd.BaseBranch),
		WorkspaceMode:               workspaceMode,
		ExecutionKind:               strings.TrimSpace(cmd.ExecutionKind),
		ShellCommand:                strings.TrimSpace(cmd.ShellCommand),
		GitCommand:                  strings.TrimSpace(cmd.GitCommand),
		WorkingDirectory:            strings.TrimSpace(cmd.WorkingDirectory),
		FileOperation:               strings.TrimSpace(cmd.FileOperation),
		FilePath:                    strings.TrimSpace(cmd.FilePath),
		FileContent:                 cmd.FileContent,
		SandboxAllowedRoot:          strings.TrimSpace(cmd.SandboxAllowedRoot),
		SandboxReadOnly:             cmd.SandboxReadOnly,
		SandboxNetwork:              cmd.SandboxNetwork,
		TimeoutMS:                   cmd.TimeoutMS,
		Status:                      types.TaskStatusNotStarted,
		Priority:                    priority,
		RequestedModel:              strings.TrimSpace(cmd.RequestedModel),
		RequestedProvider:           strings.TrimSpace(cmd.RequestedProvider),
		BudgetMicrosUSD:             cmd.BudgetMicrosUSD,
		MCPServers:                  mcpServers,
		CreatedAt:                   now,
		UpdatedAt:                   now,
	}
	return app.store.CreateTask(ctx, task)
}

func applyExecutionProfileDefaults(cmd *CreateCommand) {
	if cmd == nil {
		return
	}
	profile := strings.TrimSpace(cmd.ExecutionProfile)
	if profile != "repo_local" && profile != "coding_agent" {
		return
	}
	if strings.TrimSpace(cmd.ExecutionKind) == "" {
		cmd.ExecutionKind = "agent_loop"
	}
	if strings.TrimSpace(cmd.WorkspaceMode) == "" {
		cmd.WorkspaceMode = "persistent"
	}
	if strings.TrimSpace(cmd.WorkingDirectory) == "" {
		cmd.WorkingDirectory = "."
	}
	if strings.TrimSpace(cmd.SandboxAllowedRoot) == "" {
		workingDir := strings.TrimSpace(cmd.WorkingDirectory)
		repo := strings.TrimSpace(cmd.Repo)
		switch {
		case filepath.IsAbs(workingDir):
			cmd.SandboxAllowedRoot = workingDir
		case filepath.IsAbs(repo):
			cmd.SandboxAllowedRoot = repo
		}
	}
	if cmd.TimeoutMS <= 0 {
		cmd.TimeoutMS = 120000
	}
	if profile == "coding_agent" {
		if cmd.TimeoutMS <= 120000 {
			cmd.TimeoutMS = 300000
		}
		if strings.TrimSpace(cmd.SystemPrompt) == "" {
			cmd.SystemPrompt = codingAgentProfileSystemPrompt
		}
	}
}

func (app *Application) ListTasks(ctx context.Context, filter taskstate.TaskFilter) ([]types.Task, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	return app.store.ListTasks(ctx, filter)
}

func (app *Application) LoadTask(ctx context.Context, id string) (types.Task, error) {
	if app == nil || app.store == nil {
		return types.Task{}, ErrStoreNotConfigured
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return types.Task{}, Validation(ErrTaskIDRequired)
	}
	task, found, err := app.store.GetTask(ctx, id)
	if err != nil {
		return types.Task{}, err
	}
	if !found {
		return types.Task{}, ErrTaskNotFound
	}
	return task, nil
}

func (app *Application) RequireRunner() error {
	if app == nil || app.store == nil {
		return ErrStoreNotConfigured
	}
	if app.runner == nil {
		return ErrRunnerNotConfigured
	}
	return nil
}

func (app *Application) DeleteTask(ctx context.Context, id string) error {
	if app == nil || app.store == nil {
		return ErrStoreNotConfigured
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Validation(ErrTaskIDRequired)
	}
	err := app.store.DeleteTask(ctx, id)
	if errors.Is(err, taskstate.ErrActiveRun) {
		return ErrDeleteActiveRun
	}
	if errors.Is(err, taskstate.ErrTaskNotFound) {
		return ErrTaskNotFound
	}
	return err
}

func (app *Application) LoadTaskRun(ctx context.Context, task types.Task, runID string) (types.TaskRun, error) {
	if app == nil || app.store == nil {
		return types.TaskRun{}, ErrStoreNotConfigured
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return types.TaskRun{}, Validation(ErrRunIDRequired)
	}
	run, found, err := app.store.GetRun(ctx, task.ID, runID)
	if err != nil {
		return types.TaskRun{}, err
	}
	if !found {
		return types.TaskRun{}, ErrRunNotFound
	}
	return run, nil
}

func (app *Application) StartTask(ctx context.Context, task types.Task) (*orchestrator.StartTaskResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if app.runner == nil {
		return nil, ErrRunnerNotConfigured
	}
	active, err := HasActiveRun(ctx, app.store, task)
	if err != nil {
		return nil, err
	}
	if active {
		return nil, ErrActiveRun
	}
	result, err := app.runner.StartTask(ctx, task, app.idgen)
	return result, mapTaskNotFoundError(err)
}

// StartScheduledTask creates an ordinary task run while recording the durable
// schedule occurrence that triggered it. The runner admits the claimed
// occurrence, Run, and parent task projection through one storage transition.
func (app *Application) StartScheduledTask(ctx context.Context, task types.Task, cmd ScheduledStartCommand) (*orchestrator.StartTaskResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if app.runner == nil {
		return nil, ErrRunnerNotConfigured
	}
	scheduleID := strings.TrimSpace(cmd.ScheduleID)
	if scheduleID == "" {
		return nil, Validation(ErrScheduleIDRequired)
	}
	occurrenceID := strings.TrimSpace(cmd.ScheduleOccurrenceID)
	if occurrenceID == "" {
		return nil, Validation(ErrScheduleOccurrenceIDRequired)
	}
	if cmd.ScheduledFor.IsZero() {
		return nil, Validation(ErrScheduledForRequired)
	}
	claimOwner := strings.TrimSpace(cmd.ClaimOwner)
	if claimOwner == "" {
		return nil, Validation(ErrScheduleClaimOwnerRequired)
	}
	runner, ok := app.runner.(scheduledRunner)
	if !ok {
		return nil, ErrRunnerNotConfigured
	}
	scheduledFor := cmd.ScheduledFor.UTC()
	result, err := runner.StartScheduledTask(ctx, task, app.idgen, orchestrator.ScheduledTaskStart{
		ScheduleID: scheduleID, ScheduleOccurrenceID: occurrenceID,
		ScheduledFor: scheduledFor, ClaimOwner: claimOwner,
	})
	return result, mapTaskNotFoundError(err)
}

func (app *Application) CancelTaskRun(ctx context.Context, task types.Task, run types.TaskRun, reason string) (types.TaskRun, error) {
	if app == nil || app.store == nil {
		return types.TaskRun{}, ErrStoreNotConfigured
	}
	if app.runner == nil {
		return types.TaskRun{}, ErrRunnerNotConfigured
	}
	return app.runner.CancelRun(ctx, task, run.ID, reason)
}

// CancelNonTerminalRunsByOrigin settles every active run whose parent task is
// owned by one logical source. The tasks and their run history remain durable;
// the returned settlement keeps that source closed to new runs until the
// caller commits a successful source deletion or releases a failed attempt.
func (app *Application) CancelNonTerminalRunsByOrigin(ctx context.Context, cmd CancelRunsByOriginCommand) (CancelRunsByOriginResult, *OriginRunSettlement, error) {
	var result CancelRunsByOriginResult
	if app == nil || app.store == nil {
		return result, nil, ErrStoreNotConfigured
	}
	originKind := strings.TrimSpace(cmd.OriginKind)
	if originKind == "" {
		return result, nil, Validation(ErrOriginKindRequired)
	}
	originID := strings.TrimSpace(cmd.OriginID)
	if originID == "" {
		return result, nil, Validation(ErrOriginIDRequired)
	}
	closure, err := app.originRunGate.Close(ctx, taskruncoord.Origin{Kind: originKind, ID: originID})
	if err != nil {
		return result, nil, err
	}
	settlement := &OriginRunSettlement{closure: closure}

	tasks, err := app.store.ListTasks(ctx, taskstate.TaskFilter{})
	if err != nil {
		return result, settlement, err
	}
	var cancelErrors []error
	for _, task := range tasks {
		if strings.TrimSpace(task.OriginKind) != originKind || strings.TrimSpace(task.OriginID) != originID {
			continue
		}
		runs, err := app.store.ListRuns(ctx, task.ID)
		if err != nil {
			cancelErrors = append(cancelErrors, fmt.Errorf("list runs for task %q: %w", task.ID, err))
			continue
		}
		for _, run := range runs {
			if types.IsTerminalTaskRunStatus(run.Status) {
				if app.runner != nil {
					if _, err := app.runner.CancelRun(ctx, task, run.ID, strings.TrimSpace(cmd.Reason)); err != nil {
						cancelErrors = append(cancelErrors, fmt.Errorf("wait for terminal run %q for task %q: %w", run.ID, task.ID, err))
					}
				}
				continue
			}
			if app.runner == nil {
				cancelErrors = append(cancelErrors, fmt.Errorf("cancel run %q for task %q: %w", run.ID, task.ID, ErrRunnerNotConfigured))
				continue
			}
			cancelled, err := app.runner.CancelRun(ctx, task, run.ID, strings.TrimSpace(cmd.Reason))
			if err != nil {
				cancelErrors = append(cancelErrors, fmt.Errorf("cancel run %q for task %q: %w", run.ID, task.ID, err))
				continue
			}
			result.Runs = append(result.Runs, cancelled)
		}
	}
	return result, settlement, errors.Join(cancelErrors...)
}

func (app *Application) RetryTaskRun(ctx context.Context, task types.Task, run types.TaskRun) (*orchestrator.StartTaskResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if !types.IsTerminalTaskRunStatus(run.Status) {
		return nil, ErrRunNotRetryable
	}
	active, err := taskHasOtherActiveRun(ctx, app.store, task, run.ID)
	if err != nil {
		return nil, err
	}
	if active {
		return nil, ErrOtherActiveRun
	}
	if app.runner == nil {
		return nil, ErrRunnerNotConfigured
	}
	result, err := app.runner.RetryTask(ctx, task, run, app.idgen)
	return result, mapOtherActiveRunError(err)
}

func (app *Application) ResumeTaskRun(ctx context.Context, task types.Task, run types.TaskRun, cmd ResumeCommand) (*orchestrator.StartTaskResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if run.Status != "failed" && run.Status != "cancelled" {
		return nil, ErrRunNotResumable
	}
	active, err := taskHasOtherActiveRun(ctx, app.store, task, run.ID)
	if err != nil {
		return nil, err
	}
	if active {
		return nil, ErrOtherActiveRun
	}
	if cmd.BudgetMicrosUSD > 0 {
		if cmd.BudgetMicrosUSD < task.BudgetMicrosUSD {
			return nil, ErrBudgetLower
		}
	}
	if app.runner == nil {
		return nil, ErrRunnerNotConfigured
	}
	result, err := app.runner.ResumeTaskWithBudget(ctx, task, run, strings.TrimSpace(cmd.Reason), cmd.BudgetMicrosUSD, app.idgen)
	return result, mapOtherActiveRunError(err)
}

func (app *Application) ContinueTaskRun(ctx context.Context, task types.Task, run types.TaskRun, prompt string) (*orchestrator.StartTaskResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	active, err := taskHasOtherActiveRun(ctx, app.store, task, run.ID)
	if err != nil {
		return nil, err
	}
	if active {
		return nil, ErrOtherActiveRun
	}
	if app.runner == nil {
		return nil, ErrRunnerNotConfigured
	}
	result, err := app.runner.ContinueAgentTask(ctx, task, run, prompt, app.idgen)
	return result, mapOtherActiveRunError(err)
}

func (app *Application) RetryTaskRunFromModelCall(ctx context.Context, task types.Task, run types.TaskRun, cmd RetryFromModelCallCommand) (*orchestrator.StartTaskResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if !types.IsTerminalTaskRunStatus(run.Status) {
		return nil, ErrRunNotModelCallRetryable
	}
	if cmd.ModelCallIndex < 1 {
		return nil, Validation(ErrModelCallIndexRequired)
	}
	if run.ModelCallCount < 1 {
		return nil, Validation(errors.New("source Run has no completed model calls"))
	}
	if cmd.ModelCallIndex > run.ModelCallCount {
		return nil, Validation(fmt.Errorf("model call %d not found: source Run has %d completed model call(s)", cmd.ModelCallIndex, run.ModelCallCount))
	}
	active, err := taskHasOtherActiveRun(ctx, app.store, task, run.ID)
	if err != nil {
		return nil, err
	}
	if active {
		return nil, ErrOtherActiveRun
	}
	if app.runner == nil {
		return nil, ErrRunnerNotConfigured
	}
	result, err := app.runner.RetryTaskFromModelCall(ctx, task, run, cmd.ModelCallIndex, strings.TrimSpace(cmd.Reason), app.idgen)
	return result, mapOtherActiveRunError(err)
}

func mapOtherActiveRunError(err error) error {
	if errors.Is(err, orchestrator.ErrActiveRun) {
		return ErrOtherActiveRun
	}
	return mapTaskNotFoundError(err)
}

func mapTaskNotFoundError(err error) error {
	if errors.Is(err, taskstate.ErrTaskNotFound) {
		return ErrTaskNotFound
	}
	return err
}

func (app *Application) ListTaskApprovals(ctx context.Context, task types.Task) ([]types.TaskApproval, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	return app.store.ListApprovals(ctx, task.ID)
}

func (app *Application) GetTaskApproval(ctx context.Context, task types.Task, approvalID string) (types.TaskApproval, error) {
	if app == nil || app.store == nil {
		return types.TaskApproval{}, ErrStoreNotConfigured
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return types.TaskApproval{}, Validation(ErrApprovalIDRequired)
	}
	approval, found, err := app.store.GetApproval(ctx, task.ID, approvalID)
	if err != nil {
		return types.TaskApproval{}, err
	}
	if !found {
		return types.TaskApproval{}, ErrApprovalNotFound
	}
	return approval, nil
}

func (app *Application) ResolveTaskApproval(ctx context.Context, cmd ResolveApprovalCommand) (*orchestrator.ResolveApprovalResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if app.runner == nil {
		return nil, ErrRunnerNotConfigured
	}
	req := orchestrator.ResolveApprovalRequest{
		Task:       cmd.Task,
		ApprovalID: strings.TrimSpace(cmd.ApprovalID),
		Decision:   cmd.Decision,
		Note:       cmd.Note,
		ResolvedBy: strings.TrimSpace(cmd.ResolvedBy),
		RequestID:  cmd.RequestID,
	}
	if req.ResolvedBy == "" {
		req.ResolvedBy = "operator"
	}
	req.IDGenerator = app.idgen
	return app.runner.ResolveTaskApproval(ctx, req)
}

func HasActiveRun(ctx context.Context, store taskstate.Store, task types.Task) (bool, error) {
	latestRunID := strings.TrimSpace(task.LatestRunID)
	if latestRunID != "" && store != nil {
		run, found, err := store.GetRun(ctx, task.ID, latestRunID)
		if err != nil {
			return false, err
		}
		if found {
			return !types.IsTerminalTaskRunStatus(run.Status), nil
		}
	}
	return latestRunID != "" && !types.IsTerminalTaskRunStatus(task.Status), nil
}

func taskHasOtherActiveRun(ctx context.Context, store taskstate.Store, task types.Task, currentRunID string) (bool, error) {
	latestRunID := strings.TrimSpace(task.LatestRunID)
	if latestRunID == "" || latestRunID == strings.TrimSpace(currentRunID) {
		return false, nil
	}
	return HasActiveRun(ctx, store, task)
}
