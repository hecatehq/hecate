package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/secrets"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

var (
	errTaskStoreNotConfigured        = errors.New("task store is not configured")
	errTaskRunnerNotConfigured       = errors.New("task runner is not configured")
	errTaskProjectStoreNotConfigured = errors.New("project store is not configured")
	errTaskProjectNotFound           = errors.New("project not found")
	errTaskNotFound                  = errors.New("task not found")
	errTaskRunNotFound               = errors.New("task run not found")
	errTaskApprovalNotFound          = errors.New("task approval not found")
	errTaskIDRequired                = errors.New("task id is required")
	errTaskRunIDRequired             = errors.New("run id is required")
	errTaskApprovalIDRequired        = errors.New("approval id is required")
	errTaskTurnRequired              = errors.New("turn must be >= 1")
	errTaskPromptRequired            = errors.New("prompt is required")
	errTaskHasActiveRun              = errors.New("task already has an active run")
	errTaskHasOtherActiveRun         = errors.New("task already has another active run")
	errTaskDeleteActiveRun           = errors.New("cannot delete a task with an active run; cancel it first")
	errTaskRunNotRetryable           = errors.New("run is not retryable until it reaches a terminal state")
	errTaskRunNotResumable           = errors.New("run is not resumable")
	errTaskRunNotTurnRetryable       = errors.New("run is not retryable from a turn (must be terminal)")
	errTaskBudgetLower               = errors.New("budget_micros_usd cannot be lower than the current task ceiling")
)

type taskValidationError struct {
	err error
}

func (e taskValidationError) Error() string {
	return e.err.Error()
}

func (e taskValidationError) Unwrap() error {
	return e.err
}

func taskValidation(err error) error {
	if err == nil {
		return nil
	}
	return taskValidationError{err: err}
}

func isTaskValidationError(err error) bool {
	var validation taskValidationError
	return errors.As(err, &validation)
}

type taskApplicationRunner interface {
	StartTask(context.Context, types.Task, func(string) string) (*orchestrator.StartTaskResult, error)
	ResumeTask(context.Context, types.Task, types.TaskRun, string, func(string) string) (*orchestrator.StartTaskResult, error)
	ContinueAgentTask(context.Context, types.Task, types.TaskRun, string, func(string) string) (*orchestrator.StartTaskResult, error)
	RetryTaskFromTurn(context.Context, types.Task, types.TaskRun, int, string, func(string) string) (*orchestrator.StartTaskResult, error)
	CancelRun(context.Context, types.Task, string, string) (types.TaskRun, error)
	ResolveTaskApproval(context.Context, orchestrator.ResolveApprovalRequest) (*orchestrator.ResolveApprovalResult, error)
}

type taskApplication struct {
	store         taskstate.Store
	runner        taskApplicationRunner
	projects      projects.Store
	secretCipher  secrets.Cipher
	maxMCPServers int
	idgen         func(string) string
	now           func() time.Time
}

type taskApplicationOptions struct {
	Store         taskstate.Store
	Runner        taskApplicationRunner
	Projects      projects.Store
	SecretCipher  secrets.Cipher
	MaxMCPServers int
	IDGenerator   func(string) string
	Now           func() time.Time
}

type taskCreateCommand struct {
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
	MCPServers         []taskMCPServerCommand
}

type taskMCPServerCommand struct {
	Name           string
	Command        string
	Args           []string
	Env            map[string]string
	URL            string
	Headers        map[string]string
	ApprovalPolicy string
}

type taskResumeCommand struct {
	Reason          string
	BudgetMicrosUSD int64
}

type taskRetryFromTurnCommand struct {
	Turn   int
	Reason string
}

type taskResolveApprovalCommand struct {
	Task       types.Task
	ApprovalID string
	Decision   string
	Note       string
	ResolvedBy string
	RequestID  string
}

func newTaskApplication(opts taskApplicationOptions) *taskApplication {
	app := &taskApplication{
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

func (app *taskApplication) CreateTask(ctx context.Context, cmd taskCreateCommand) (types.Task, error) {
	if app == nil || app.store == nil {
		return types.Task{}, errTaskStoreNotConfigured
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
		return types.Task{}, errTaskPromptRequired
	}

	mcpServers, err := normalizeMCPServerConfigs(cmd.MCPServers, app.secretCipher, app.maxMCPServers)
	if err != nil {
		return types.Task{}, taskValidation(err)
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
			return types.Task{}, errTaskProjectStoreNotConfigured
		}
		if _, ok, err := app.projects.Get(ctx, projectID); err != nil {
			return types.Task{}, err
		} else if !ok {
			return types.Task{}, errTaskProjectNotFound
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

func applyExecutionProfileDefaults(cmd *taskCreateCommand) {
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

func (app *taskApplication) ListTasks(ctx context.Context, filter taskstate.TaskFilter) ([]types.Task, error) {
	if app == nil || app.store == nil {
		return nil, errTaskStoreNotConfigured
	}
	return app.store.ListTasks(ctx, filter)
}

func (app *taskApplication) LoadTask(ctx context.Context, id string) (types.Task, error) {
	if app == nil || app.store == nil {
		return types.Task{}, errTaskStoreNotConfigured
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return types.Task{}, taskValidation(errTaskIDRequired)
	}
	task, found, err := app.store.GetTask(ctx, id)
	if err != nil {
		return types.Task{}, err
	}
	if !found {
		return types.Task{}, errTaskNotFound
	}
	return task, nil
}

func (app *taskApplication) RequireRunner() error {
	if app == nil || app.store == nil {
		return errTaskStoreNotConfigured
	}
	if app.runner == nil {
		return errTaskRunnerNotConfigured
	}
	return nil
}

func (app *taskApplication) DeleteTask(ctx context.Context, id string) error {
	if app == nil || app.store == nil {
		return errTaskStoreNotConfigured
	}
	task, err := app.LoadTask(ctx, id)
	if err != nil {
		return err
	}
	active, err := taskHasActiveRun(ctx, app.store, task)
	if err != nil {
		return err
	}
	if active {
		return errTaskDeleteActiveRun
	}
	return app.store.DeleteTask(ctx, strings.TrimSpace(id))
}

func (app *taskApplication) LoadTaskRun(ctx context.Context, task types.Task, runID string) (types.TaskRun, error) {
	if app == nil || app.store == nil {
		return types.TaskRun{}, errTaskStoreNotConfigured
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return types.TaskRun{}, taskValidation(errTaskRunIDRequired)
	}
	run, found, err := app.store.GetRun(ctx, task.ID, runID)
	if err != nil {
		return types.TaskRun{}, err
	}
	if !found {
		return types.TaskRun{}, errTaskRunNotFound
	}
	return run, nil
}

func (app *taskApplication) StartTask(ctx context.Context, task types.Task) (*orchestrator.StartTaskResult, error) {
	if app == nil || app.store == nil {
		return nil, errTaskStoreNotConfigured
	}
	if app.runner == nil {
		return nil, errTaskRunnerNotConfigured
	}
	active, err := taskHasActiveRun(ctx, app.store, task)
	if err != nil {
		return nil, err
	}
	if active {
		return nil, errTaskHasActiveRun
	}
	return app.runner.StartTask(ctx, task, app.idgen)
}

func (app *taskApplication) CancelTaskRun(ctx context.Context, task types.Task, run types.TaskRun, reason string) (types.TaskRun, error) {
	if app == nil || app.store == nil {
		return types.TaskRun{}, errTaskStoreNotConfigured
	}
	if app.runner == nil {
		return types.TaskRun{}, errTaskRunnerNotConfigured
	}
	return app.runner.CancelRun(ctx, task, run.ID, reason)
}

func (app *taskApplication) RetryTaskRun(ctx context.Context, task types.Task, run types.TaskRun) (*orchestrator.StartTaskResult, error) {
	if app == nil || app.store == nil {
		return nil, errTaskStoreNotConfigured
	}
	if !types.IsTerminalTaskRunStatus(run.Status) {
		return nil, errTaskRunNotRetryable
	}
	active, err := taskHasOtherActiveRun(ctx, app.store, task, run.ID)
	if err != nil {
		return nil, err
	}
	if active {
		return nil, errTaskHasOtherActiveRun
	}
	if app.runner == nil {
		return nil, errTaskRunnerNotConfigured
	}
	return app.runner.StartTask(ctx, task, app.idgen)
}

func (app *taskApplication) ResumeTaskRun(ctx context.Context, task types.Task, run types.TaskRun, cmd taskResumeCommand) (*orchestrator.StartTaskResult, error) {
	if app == nil || app.store == nil {
		return nil, errTaskStoreNotConfigured
	}
	if run.Status != "failed" && run.Status != "cancelled" {
		return nil, errTaskRunNotResumable
	}
	active, err := taskHasOtherActiveRun(ctx, app.store, task, run.ID)
	if err != nil {
		return nil, err
	}
	if active {
		return nil, errTaskHasOtherActiveRun
	}
	if cmd.BudgetMicrosUSD > 0 {
		if cmd.BudgetMicrosUSD < task.BudgetMicrosUSD {
			return nil, errTaskBudgetLower
		}
		if app.runner == nil {
			return nil, errTaskRunnerNotConfigured
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
		return nil, errTaskRunnerNotConfigured
	}
	return app.runner.ResumeTask(ctx, task, run, strings.TrimSpace(cmd.Reason), app.idgen)
}

func (app *taskApplication) ContinueTaskRun(ctx context.Context, task types.Task, run types.TaskRun, prompt string) (*orchestrator.StartTaskResult, error) {
	if app == nil || app.store == nil {
		return nil, errTaskStoreNotConfigured
	}
	active, err := taskHasOtherActiveRun(ctx, app.store, task, run.ID)
	if err != nil {
		return nil, err
	}
	if active {
		return nil, errTaskHasOtherActiveRun
	}
	if app.runner == nil {
		return nil, errTaskRunnerNotConfigured
	}
	return app.runner.ContinueAgentTask(ctx, task, run, prompt, app.idgen)
}

func (app *taskApplication) RetryTaskRunFromTurn(ctx context.Context, task types.Task, run types.TaskRun, cmd taskRetryFromTurnCommand) (*orchestrator.StartTaskResult, error) {
	if app == nil || app.store == nil {
		return nil, errTaskStoreNotConfigured
	}
	if !types.IsTerminalTaskRunStatus(run.Status) {
		return nil, errTaskRunNotTurnRetryable
	}
	if cmd.Turn < 1 {
		return nil, taskValidation(errTaskTurnRequired)
	}
	active, err := taskHasOtherActiveRun(ctx, app.store, task, run.ID)
	if err != nil {
		return nil, err
	}
	if active {
		return nil, errTaskHasOtherActiveRun
	}
	if app.runner == nil {
		return nil, errTaskRunnerNotConfigured
	}
	return app.runner.RetryTaskFromTurn(ctx, task, run, cmd.Turn, strings.TrimSpace(cmd.Reason), app.idgen)
}

func (app *taskApplication) ListTaskApprovals(ctx context.Context, task types.Task) ([]types.TaskApproval, error) {
	if app == nil || app.store == nil {
		return nil, errTaskStoreNotConfigured
	}
	return app.store.ListApprovals(ctx, task.ID)
}

func (app *taskApplication) GetTaskApproval(ctx context.Context, task types.Task, approvalID string) (types.TaskApproval, error) {
	if app == nil || app.store == nil {
		return types.TaskApproval{}, errTaskStoreNotConfigured
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return types.TaskApproval{}, taskValidation(errTaskApprovalIDRequired)
	}
	approval, found, err := app.store.GetApproval(ctx, task.ID, approvalID)
	if err != nil {
		return types.TaskApproval{}, err
	}
	if !found {
		return types.TaskApproval{}, errTaskApprovalNotFound
	}
	return approval, nil
}

func (app *taskApplication) ResolveTaskApproval(ctx context.Context, cmd taskResolveApprovalCommand) (*orchestrator.ResolveApprovalResult, error) {
	if app == nil || app.store == nil {
		return nil, errTaskStoreNotConfigured
	}
	if app.runner == nil {
		return nil, errTaskRunnerNotConfigured
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

func taskHasActiveRun(ctx context.Context, store taskstate.Store, task types.Task) (bool, error) {
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
	return taskHasActiveRun(ctx, store, task)
}
