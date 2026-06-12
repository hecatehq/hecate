package taskapp

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/apperrors"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/secrets"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

const codingAgentProfileSystemPrompt = `You are running inside Hecate's coding-agent runtime.

Use read_file and list_dir before editing. Prefer file_edit for targeted changes and file_write only for new files or full rewrites. Keep changes scoped to the user's request. Explain important tradeoffs in the final answer, and mention files changed when useful.`

var (
	ErrStoreNotConfigured        = errors.New("task store is not configured")
	ErrRunnerNotConfigured       = errors.New("task runner is not configured")
	ErrProjectStoreNotConfigured = errors.New("project store is not configured")
	ErrProjectNotFound           = errors.New("project not found")
	ErrTaskNotFound              = errors.New("task not found")
	ErrRunNotFound               = errors.New("task run not found")
	ErrApprovalNotFound          = errors.New("task approval not found")
	ErrTaskIDRequired            = errors.New("task id is required")
	ErrRunIDRequired             = errors.New("run id is required")
	ErrApprovalIDRequired        = errors.New("approval id is required")
	ErrTurnRequired              = errors.New("turn must be >= 1")
	ErrPromptRequired            = errors.New("prompt is required")
	ErrActiveRun                 = errors.New("task already has an active run")
	ErrOtherActiveRun            = errors.New("task already has another active run")
	ErrDeleteActiveRun           = errors.New("cannot delete a task with an active run; cancel it first")
	ErrRunNotRetryable           = errors.New("run is not retryable until it reaches a terminal state")
	ErrRunNotResumable           = errors.New("run is not resumable")
	ErrRunNotTurnRetryable       = errors.New("run is not retryable from a turn (must be terminal)")
	ErrBudgetLower               = errors.New("budget_micros_usd cannot be lower than the current task ceiling")
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
	ResumeTask(context.Context, types.Task, types.TaskRun, string, func(string) string) (*orchestrator.StartTaskResult, error)
	ContinueAgentTask(context.Context, types.Task, types.TaskRun, string, func(string) string) (*orchestrator.StartTaskResult, error)
	RetryTaskFromTurn(context.Context, types.Task, types.TaskRun, int, string, func(string) string) (*orchestrator.StartTaskResult, error)
	CancelRun(context.Context, types.Task, string, string) (types.TaskRun, error)
	ResolveTaskApproval(context.Context, orchestrator.ResolveApprovalRequest) (*orchestrator.ResolveApprovalResult, error)
}

type Application struct {
	store         taskstate.Store
	runner        Runner
	projects      projects.Store
	secretCipher  secrets.Cipher
	maxMCPServers int
	idgen         func(string) string
	now           func() time.Time
}

type Options struct {
	Store         taskstate.Store
	Runner        Runner
	Projects      projects.Store
	SecretCipher  secrets.Cipher
	MaxMCPServers int
	IDGenerator   func(string) string
	Now           func() time.Time
}

type CreateCommand struct {
	Title              string
	Prompt             string
	ProjectID          string
	SystemPrompt       string
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

type RetryFromTurnCommand struct {
	Turn   int
	Reason string
}

type ResolveApprovalCommand struct {
	Task       types.Task
	ApprovalID string
	Decision   string
	Note       string
	ResolvedBy string
	RequestID  string
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
		ID:                 app.idgen("task"),
		Title:              title,
		Prompt:             prompt,
		ProjectID:          projectID,
		SystemPrompt:       strings.TrimSpace(cmd.SystemPrompt),
		ExecutionProfile:   strings.TrimSpace(cmd.ExecutionProfile),
		Repo:               strings.TrimSpace(cmd.Repo),
		BaseBranch:         strings.TrimSpace(cmd.BaseBranch),
		WorkspaceMode:      workspaceMode,
		ExecutionKind:      strings.TrimSpace(cmd.ExecutionKind),
		ShellCommand:       strings.TrimSpace(cmd.ShellCommand),
		GitCommand:         strings.TrimSpace(cmd.GitCommand),
		WorkingDirectory:   strings.TrimSpace(cmd.WorkingDirectory),
		FileOperation:      strings.TrimSpace(cmd.FileOperation),
		FilePath:           strings.TrimSpace(cmd.FilePath),
		FileContent:        cmd.FileContent,
		SandboxAllowedRoot: strings.TrimSpace(cmd.SandboxAllowedRoot),
		SandboxReadOnly:    cmd.SandboxReadOnly,
		SandboxNetwork:     cmd.SandboxNetwork,
		TimeoutMS:          cmd.TimeoutMS,
		Status:             "queued",
		Priority:           priority,
		RequestedModel:     strings.TrimSpace(cmd.RequestedModel),
		RequestedProvider:  strings.TrimSpace(cmd.RequestedProvider),
		BudgetMicrosUSD:    cmd.BudgetMicrosUSD,
		MCPServers:         mcpServers,
		CreatedAt:          now,
		UpdatedAt:          now,
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
	task, err := app.LoadTask(ctx, id)
	if err != nil {
		return err
	}
	active, err := HasActiveRun(ctx, app.store, task)
	if err != nil {
		return err
	}
	if active {
		return ErrDeleteActiveRun
	}
	return app.store.DeleteTask(ctx, strings.TrimSpace(id))
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
	return app.runner.StartTask(ctx, task, app.idgen)
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
	return app.runner.StartTask(ctx, task, app.idgen)
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
		if app.runner == nil {
			return nil, ErrRunnerNotConfigured
		}
		// Persist the raised ceiling before queueing; the resumed
		// agent loop reads the task ceiling on its first turn.
		task.BudgetMicrosUSD = cmd.BudgetMicrosUSD
		updated, err := app.store.UpdateTask(ctx, task)
		if err != nil {
			return nil, err
		}
		task = updated
	}
	if app.runner == nil {
		return nil, ErrRunnerNotConfigured
	}
	return app.runner.ResumeTask(ctx, task, run, strings.TrimSpace(cmd.Reason), app.idgen)
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
	return app.runner.ContinueAgentTask(ctx, task, run, prompt, app.idgen)
}

func (app *Application) RetryTaskRunFromTurn(ctx context.Context, task types.Task, run types.TaskRun, cmd RetryFromTurnCommand) (*orchestrator.StartTaskResult, error) {
	if app == nil || app.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if !types.IsTerminalTaskRunStatus(run.Status) {
		return nil, ErrRunNotTurnRetryable
	}
	if cmd.Turn < 1 {
		return nil, Validation(ErrTurnRequired)
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
	return app.runner.RetryTaskFromTurn(ctx, task, run, cmd.Turn, strings.TrimSpace(cmd.Reason), app.idgen)
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
