package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/hecate/agent-runtime/internal/sandbox"
	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/pkg/types"
)

type Executor interface {
	Execute(ctx context.Context, spec ExecutionSpec) (*ExecutionResult, error)
}

type ExecutionSpec struct {
	Task             types.Task
	Run              types.TaskRun
	RequestID        string
	TraceID          string
	RootSpanID       string
	StartedAt        time.Time
	ResumeCheckpoint *ResumeCheckpoint
	NewID            func(prefix string) string
	UpsertStep       func(step types.TaskStep) error
	UpsertArtifact   func(artifact types.TaskArtifact) error
	// EmitRunEvent appends an event to the run's event stream. Used
	// by executors that want to emit telemetry beyond steps and
	// artifacts — currently the agent loop's MCP dispatcher, which
	// records mcp.tool.dispatched / .failed / .blocked alongside its
	// steps so operators have a per-call audit signal independent of
	// the step kinds. Optional; nil disables emission.
	EmitRunEvent func(eventType string, data map[string]any)
	// SystemPrompt is the composed agent_loop system prompt — global
	// default + tenant prompt + workspace CLAUDE.md/AGENTS.md +
	// per-task prompt, concatenated broadest-first. The runner
	// assembles it via a SystemPromptResolver before dispatching;
	// non-agent_loop executors ignore it.
	SystemPrompt string
	// ShellNetwork* refines the sandbox.Policy egress rules when
	// task.SandboxNetwork is true. Set by the runner from its
	// ShellNetworkPolicy config; ignored when SandboxNetwork is
	// false (the master gate denies all network access first).
	ShellNetworkAllowedHosts    []string
	ShellNetworkAllowPrivateIPs bool
	// ToolCallID/ToolName identify the model tool call that delegated
	// into this executor. Direct shell/git/file tasks leave these
	// empty; the executor falls back to its own step id/name for
	// typed runtime events.
	ToolCallID string
	ToolName   string
}

type ResumeCheckpoint struct {
	SourceRunID         string
	Reason              string
	LastEventSequence   int64
	LastCompletedStepID string
	LastStepIndex       int
	CompletedStepCount  int
	ArtifactCount       int
	// AgentConversation is the JSON-encoded conversation history from
	// the source run, populated when the source produced an
	// `agent_conversation` artifact. Empty on resumes of non-agent_loop
	// runs. The agent loop unmarshals this and continues from the
	// saved state rather than restarting the conversation from
	// scratch — that's what lets the loop survive crashes and
	// approval-gating mid-conversation.
	AgentConversation []byte
	// RetryFromTurn, when > 0, signals that AgentConversation has been
	// truncated to right before the Nth assistant turn — the source
	// run's tool results and reasoning prior to turn N are preserved,
	// but turn N's assistant message and everything after it has been
	// dropped. The agent loop's next LLM call re-issues turn N from
	// the same starting context, letting operators explore alternative
	// paths from a known prior state. The runner zeroes
	// LastStepIndex/LastCompletedStepID for retry-from-turn so the new
	// run's step indices start at 1 rather than continuing the source's.
	RetryFromTurn int
	// PriorCostMicrosUSD is the cumulative LLM spend of every prior
	// run in the resume chain. Fresh runs see zero; resumed runs see
	// source.PriorCostMicrosUSD + source.TotalCostMicrosUSD. The agent
	// loop applies the per-task cost ceiling against
	// (PriorCostMicrosUSD + this run's spend) so the ceiling holds
	// across the full chain — without it, repeatedly resuming a run
	// would silently bypass the ceiling.
	PriorCostMicrosUSD int64
	// ThisRunCostMicrosUSD is the cost already attributed to the
	// current run (run.TotalCostMicrosUSD at claim time). Cross-run
	// resumes see zero — the new run hasn't spent anything yet.
	// Same-run mid-approval resumes see the pre-pause spend, which
	// the agent loop seeds into costSpent so the ceiling check and
	// the persisted run.TotalCostMicrosUSD both account for it
	// rather than losing the pre-pause portion when the runner
	// overwrites Total on finalization.
	ThisRunCostMicrosUSD int64
}

type ExecutionResult struct {
	Status            string
	Steps             []types.TaskStep
	Artifacts         []types.TaskArtifact
	LastError         string
	OtelStatusCode    string
	OtelStatusMessage string
	// PendingApprovals are approval records the executor produced
	// during this run that the runner should persist. The agent loop
	// emits these mid-loop when it pauses on a gated tool call —
	// Status will be "awaiting_approval" and the runner persists the
	// approvals as part of the run-finalization path. Other executors
	// (shell/git/file) don't use this; their approvals are created
	// pre-execution by the runner itself.
	PendingApprovals []types.TaskApproval
	// CostMicrosUSD is the total LLM spend for this execution. The
	// agent loop accumulates per-turn ChatResponse.Cost.TotalMicrosUSD
	// and sets this on result; the runner writes it to
	// TaskRun.TotalCostMicrosUSD. Other executors don't make LLM
	// calls and leave this zero.
	CostMicrosUSD int64
	// TurnCosts is the per-turn LLM-spend breakdown the agent loop
	// produced. Each entry pairs a turn number with the LLM cost for
	// that turn, the cumulative spend through it (this run only —
	// PriorCostMicrosUSD is added by the runner before emitting the
	// event), the assistant step ID, and the tool-call count. The
	// runner emits one `turn.completed` event per entry so
	// operators can replay cost evolution from the events feed.
	TurnCosts []TurnCostRecord
}

// TurnCostRecord captures a single agent_loop turn's LLM-cost
// telemetry. Built by the agent loop after each turn's LLM
// round-trip; the runner consumes the slice on result and emits one
// event per entry.
type TurnCostRecord struct {
	Turn          int
	StepID        string
	CostMicrosUSD int64
	// CumulativeMicrosUSD is the running spend through this turn,
	// counting only this run's turns. The runner adds the source
	// chain's PriorCostMicrosUSD before persisting, giving operators
	// a "spend so far including prior resumes" figure in the event.
	CumulativeMicrosUSD int64
	ToolCallCount       int
}

type StubExecutor struct{}

func NewStubExecutor() *StubExecutor {
	return &StubExecutor{}
}

func (e *StubExecutor) Execute(_ context.Context, spec ExecutionSpec) (*ExecutionResult, error) {
	if spec.NewID == nil {
		return nil, fmt.Errorf("resource id generator is required")
	}

	step := types.TaskStep{
		ID:       spec.NewID("step"),
		TaskID:   spec.Task.ID,
		RunID:    spec.Run.ID,
		Index:    1,
		Kind:     "model",
		Title:    "Stub planning step",
		Status:   "completed",
		Phase:    "planning",
		Result:   telemetry.ResultSuccess,
		ToolName: "builtin.stub_planner",
		Input: map[string]any{
			"title":  spec.Task.Title,
			"prompt": spec.Task.Prompt,
		},
		OutputSummary: map[string]any{
			"summary":     "Stub orchestrator generated a first planning step.",
			"next_action": "review generated summary artifact",
		},
		StartedAt:  spec.StartedAt,
		FinishedAt: spec.StartedAt,
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
	}

	summary := fmt.Sprintf("Stub run %d for task %q created a first planning step and is ready for a real executor.", spec.Run.Number, spec.Task.Title)
	artifact := types.TaskArtifact{
		ID:          spec.NewID("artifact"),
		TaskID:      spec.Task.ID,
		RunID:       spec.Run.ID,
		StepID:      step.ID,
		Kind:        "summary",
		Name:        "run-summary.txt",
		Description: "Stub run summary artifact",
		MimeType:    "text/plain",
		StorageKind: "inline",
		ContentText: summary,
		SizeBytes:   int64(len(summary)),
		Status:      "ready",
		CreatedAt:   spec.StartedAt,
		RequestID:   spec.RequestID,
		TraceID:     spec.TraceID,
	}

	return &ExecutionResult{
		Status:         "completed",
		Steps:          []types.TaskStep{step},
		Artifacts:      []types.TaskArtifact{artifact},
		OtelStatusCode: "ok",
	}, nil
}

type ShellExecutor struct {
	sandbox sandbox.Executor
}

func NewShellExecutor(exec sandbox.Executor) *ShellExecutor {
	return &ShellExecutor{sandbox: ensureSandboxExecutor(exec)}
}

func (e *ShellExecutor) Execute(ctx context.Context, spec ExecutionSpec) (*ExecutionResult, error) {
	if spec.NewID == nil {
		return nil, fmt.Errorf("resource id generator is required")
	}
	command := spec.Task.ShellCommand
	if command == "" {
		return nil, fmt.Errorf("shell command is required")
	}
	return executeStreamingCommand(ctx, e.sandbox, spec, streamingCommandSpec{
		command:           command,
		kind:              "shell",
		title:             "Shell command",
		toolName:          "shell",
		stdoutName:        "stdout.txt",
		stdoutDescription: "Shell stdout capture",
		stderrName:        "stderr.txt",
		stderrDescription: "Shell stderr capture",
		timeoutErrorKind:  "shell_timeout",
		defaultErrorKind:  "shell_command_failed",
	})
}

func shellErrorKind(err error) string {
	return commandErrorKind(err, "shell_timeout", "shell_command_failed")
}

type FileExecutor struct {
	sandbox sandbox.Executor
}

func NewFileExecutor(exec sandbox.Executor) *FileExecutor {
	return &FileExecutor{sandbox: ensureSandboxExecutor(exec)}
}

func (e *FileExecutor) Execute(ctx context.Context, spec ExecutionSpec) (*ExecutionResult, error) {
	if spec.NewID == nil {
		return nil, fmt.Errorf("resource id generator is required")
	}
	if spec.Task.FilePath == "" {
		return nil, fmt.Errorf("file path is required")
	}

	operation := fileOperation(spec.Task)
	if spec.Task.SandboxReadOnly {
		return fileFailure(spec, operation, spec.Task.FilePath, "sandbox policy denied: write access is disabled", "sandbox_policy_denied"), nil
	}
	request := sandbox.FileRequest{
		Path:             spec.Task.FilePath,
		Content:          spec.Task.FileContent,
		WorkingDirectory: spec.Task.WorkingDirectory,
		Policy:           taskPolicy(spec),
	}
	var (
		fileResult sandbox.FileResult
		err        error
	)
	switch operation {
	case "write":
		fileResult, err = e.sandbox.WriteFile(ctx, request)
	case "append":
		fileResult, err = e.sandbox.AppendFile(ctx, request)
	default:
		return fileFailure(spec, operation, spec.Task.FilePath, fmt.Sprintf("unsupported file operation %q", operation), "file_operation_unsupported"), nil
	}
	if err != nil {
		return fileFailure(spec, operation, spec.Task.FilePath, err.Error(), fileErrorKind(err)), nil
	}

	status, result, lastError, otelStatusCode, otelStatusMessage := executionStatus(nil)
	finishedAt := time.Now().UTC()
	step := newExecutionStep(spec, "file", "File operation", "file", fileOperationInput(spec.Task, operation))
	finalizeExecutionStep(&step, finishedAt, status, result, lastError, "", map[string]any{
		"path":  fileResult.Path,
		"bytes": fileResult.BytesWritten,
	})
	artifact := newInlineArtifact(spec, step.ID, "file", filepath.Base(fileResult.Path), "File executor output", fileResult.Path, spec.Task.FileContent, "ready", finishedAt)

	return newExecutionResult(status, []types.TaskStep{step}, []types.TaskArtifact{artifact}, lastError, otelStatusCode, otelStatusMessage), nil
}

type GitExecutor struct {
	sandbox sandbox.Executor
}

func NewGitExecutor(exec sandbox.Executor) *GitExecutor {
	return &GitExecutor{sandbox: ensureSandboxExecutor(exec)}
}

func (e *GitExecutor) Execute(ctx context.Context, spec ExecutionSpec) (*ExecutionResult, error) {
	if spec.NewID == nil {
		return nil, fmt.Errorf("resource id generator is required")
	}
	command := spec.Task.GitCommand
	if command == "" {
		return nil, fmt.Errorf("git command is required")
	}
	return executeStreamingCommand(ctx, e.sandbox, spec, streamingCommandSpec{
		command:           "git " + command,
		displayCommand:    command,
		kind:              "git",
		title:             "Git command",
		toolName:          "git",
		stdoutName:        "git-stdout.txt",
		stdoutDescription: "Git stdout capture",
		stderrName:        "git-stderr.txt",
		stderrDescription: "Git stderr capture",
		timeoutErrorKind:  "git_timeout",
		defaultErrorKind:  "git_command_failed",
	})
}

func gitErrorKind(err error) string {
	return commandErrorKind(err, "git_timeout", "git_command_failed")
}

type streamingCommandSpec struct {
	command           string
	displayCommand    string
	kind              string
	title             string
	toolName          string
	stdoutName        string
	stdoutDescription string
	stderrName        string
	stderrDescription string
	timeoutErrorKind  string
	defaultErrorKind  string
}

func executeStreamingCommand(ctx context.Context, exec sandbox.Executor, spec ExecutionSpec, commandSpec streamingCommandSpec) (*ExecutionResult, error) {
	timeout := commandTimeout(spec.Task)
	workingDirectory := commandWorkingDirectory(spec.Task)
	displayCommand := commandSpec.displayCommand
	if displayCommand == "" {
		displayCommand = commandSpec.command
	}

	step := newExecutionStep(spec, commandSpec.kind, commandSpec.title, commandSpec.toolName, map[string]any{
		"command":           displayCommand,
		"working_directory": workingDirectory,
		"timeout_ms":        timeout,
	})
	toolCallID := firstNonEmpty(spec.ToolCallID, step.ID)
	eventToolName := firstNonEmpty(spec.ToolName, commandSpec.toolName)
	if commandSpec.kind == "shell" {
		emitTypedShellRunEvent(spec, "tool.invoked", map[string]any{
			"tool_call_id": toolCallID,
			"tool_name":    eventToolName,
			"kind":         "shell",
			"turn_index":   0,
		})
		emitTypedShellRunEvent(spec, "tool.started", map[string]any{
			"tool_call_id": toolCallID,
			"tool_name":    eventToolName,
			"kind":         "shell",
			"turn_index":   0,
		})
		emitTypedShellRunEvent(spec, "tool.shell.command", map[string]any{
			"tool_call_id":   toolCallID,
			"argv":           []string{"sh", "-lc", displayCommand},
			"cwd":            workingDirectory,
			"env_keys":       []string{"PATH", "HOME"},
			"sandbox_layer":  string(sandbox.HealthInfo().Kind),
			"timeout_ms":     timeout,
			"command_string": displayCommand,
		})
	}
	step.OutputSummary = map[string]any{
		"stdout_bytes": 0,
		"stderr_bytes": 0,
		"exit_code":    0,
	}
	if err := upsertTaskStep(spec, step); err != nil {
		return nil, err
	}

	stdoutArtifact := newStreamingCommandArtifact(spec, step.ID, "stdout", commandSpec.stdoutName, commandSpec.stdoutDescription)
	if err := upsertTaskArtifact(spec, stdoutArtifact); err != nil {
		return nil, err
	}
	stderrArtifact := newStreamingCommandArtifact(spec, step.ID, "stderr", commandSpec.stderrName, commandSpec.stderrDescription)
	if err := upsertTaskArtifact(spec, stderrArtifact); err != nil {
		return nil, err
	}

	var stdoutOffset int
	var stderrOffset int
	resultData, err := exec.RunStreaming(ctx, sandbox.Command{
		Command:          commandSpec.command,
		WorkingDirectory: workingDirectory,
		Timeout:          time.Duration(timeout) * time.Millisecond,
		Policy:           taskPolicy(spec),
	}, func(chunk sandbox.OutputChunk) {
		switch chunk.Stream {
		case "stdout":
			offset := stdoutOffset
			stdoutOffset += len(chunk.Text)
			stdoutArtifact.ContentText += chunk.Text
			stdoutArtifact.SizeBytes = int64(len(stdoutArtifact.ContentText))
			_ = upsertTaskArtifact(spec, stdoutArtifact)
			if commandSpec.kind == "shell" {
				emitTypedShellRunEvent(spec, "tool.shell.output_chunk", map[string]any{
					"tool_call_id": toolCallID,
					"stream":       "stdout",
					"data":         chunk.Text,
					"byte_offset":  offset,
				})
			}
		case "stderr":
			offset := stderrOffset
			stderrOffset += len(chunk.Text)
			stderrArtifact.ContentText += chunk.Text
			stderrArtifact.SizeBytes = int64(len(stderrArtifact.ContentText))
			_ = upsertTaskArtifact(spec, stderrArtifact)
			if commandSpec.kind == "shell" {
				emitTypedShellRunEvent(spec, "tool.shell.output_chunk", map[string]any{
					"tool_call_id": toolCallID,
					"stream":       "stderr",
					"data":         chunk.Text,
					"byte_offset":  offset,
				})
			}
		}
	})

	status, result, lastError, otelStatusCode, otelStatusMessage := executionStatus(err)
	finishedAt := time.Now().UTC()

	finalizeExecutionStep(&step, finishedAt, status, result, lastError, commandErrorKind(err, commandSpec.timeoutErrorKind, commandSpec.defaultErrorKind), map[string]any{
		"stdout_bytes": len(resultData.Stdout),
		"stderr_bytes": len(resultData.Stderr),
		"exit_code":    resultData.ExitCode,
	})
	step.ExitCode = resultData.ExitCode
	if commandSpec.kind == "shell" && !sandbox.IsPolicyDenied(err) {
		emitTypedShellRunEvent(spec, "tool.shell.exited", map[string]any{
			"tool_call_id": toolCallID,
			"exit_code":    resultData.ExitCode,
			"signal":       nil,
			"stdout_bytes": len(resultData.Stdout),
			"stderr_bytes": len(resultData.Stderr),
			"truncated":    sandbox.IsOutputLimitExceeded(err),
		})
	}
	if commandSpec.kind == "shell" {
		eventType := "tool.completed"
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			eventType = "tool.timed_out"
		case errors.Is(err, context.Canceled):
			eventType = "tool.cancelled"
		case err != nil:
			eventType = "tool.failed"
		}
		data := map[string]any{
			"tool_call_id": toolCallID,
			"tool_name":    eventToolName,
			"kind":         "shell",
			"duration_ms":  finishedAt.Sub(step.StartedAt).Milliseconds(),
			"summary":      shellEventSummary(status, resultData.ExitCode, lastError),
		}
		if lastError != "" {
			data["error"] = lastError
		}
		if errors.Is(err, context.DeadlineExceeded) {
			data["after_ms"] = timeout
		}
		emitTypedShellRunEvent(spec, eventType, data)
	}
	if err := upsertTaskStep(spec, step); err != nil {
		return nil, err
	}

	finalArtifactStatus := "ready"
	if status == "cancelled" {
		finalArtifactStatus = "cancelled"
	}
	stdoutArtifact.Status = finalArtifactStatus
	stderrArtifact.Status = finalArtifactStatus
	if err := upsertTaskArtifact(spec, stdoutArtifact); err != nil {
		return nil, err
	}
	if err := upsertTaskArtifact(spec, stderrArtifact); err != nil {
		return nil, err
	}

	return newExecutionResult(status, []types.TaskStep{step}, []types.TaskArtifact{stdoutArtifact, stderrArtifact}, lastError, otelStatusCode, otelStatusMessage), nil
}

func emitTypedShellRunEvent(spec ExecutionSpec, eventType string, data map[string]any) {
	if spec.EmitRunEvent == nil {
		return
	}
	spec.EmitRunEvent(eventType, data)
}

func shellEventSummary(status string, exitCode int, lastError string) string {
	switch {
	case status == "completed":
		return fmt.Sprintf("exited with status %d", exitCode)
	case lastError != "":
		return lastError
	default:
		return status
	}
}

func ensureSandboxExecutor(exec sandbox.Executor) sandbox.Executor {
	if exec == nil {
		return sandbox.NewLocalExecutor()
	}
	return exec
}

func commandTimeout(task types.Task) int {
	timeout := task.TimeoutMS
	if timeout <= 0 {
		return 5000
	}
	return timeout
}

func commandWorkingDirectory(task types.Task) string {
	if task.WorkingDirectory == "" {
		return "."
	}
	return task.WorkingDirectory
}

func executionStatus(err error) (status string, result string, lastError string, otelStatusCode string, otelStatusMessage string) {
	if err == nil {
		return "completed", telemetry.ResultSuccess, "", "ok", ""
	}

	status = "failed"
	result = telemetry.ResultError
	lastError = err.Error()
	otelStatusCode = "error"
	otelStatusMessage = err.Error()
	if errors.Is(err, context.Canceled) {
		status = "cancelled"
	}
	return status, result, lastError, otelStatusCode, otelStatusMessage
}

func commandErrorKind(err error, timeoutErrorKind, defaultErrorKind string) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "run_cancelled"
	}
	if sandbox.IsPolicyDenied(err) {
		return "sandbox_policy_denied"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return timeoutErrorKind
	}
	return defaultErrorKind
}

func newStreamingCommandArtifact(spec ExecutionSpec, stepID, kind, name, description string) types.TaskArtifact {
	return newInlineArtifact(spec, stepID, kind, name, description, "", "", "streaming", spec.StartedAt)
}

func upsertTaskStep(spec ExecutionSpec, step types.TaskStep) error {
	if spec.UpsertStep == nil {
		return nil
	}
	return spec.UpsertStep(step)
}

func upsertTaskArtifact(spec ExecutionSpec, artifact types.TaskArtifact) error {
	if spec.UpsertArtifact == nil {
		return nil
	}
	return spec.UpsertArtifact(artifact)
}

func fileOperation(task types.Task) string {
	if task.FileOperation == "" {
		return "write"
	}
	return task.FileOperation
}

func fileErrorKind(err error) string {
	if err == nil {
		return ""
	}
	if sandbox.IsPolicyDenied(err) {
		return "sandbox_policy_denied"
	}
	return "file_operation_failed"
}

// taskPolicy translates a task's sandbox fields into a sandbox.Policy.
// The shell-network refinement (per-host allowlist, private-IP block)
// is plumbed via ExecutionSpec.ShellNetworkAllowedHosts /
// ShellNetworkAllowPrivateIPs — the runner sets those from its
// runtime config before dispatch. ExecutionSpec is the single
// per-run carrier the runner already populates, so propagating two
// more fields keeps the executor stateless and avoids passing the
// runtime config into every executor.
func taskPolicy(spec ExecutionSpec) sandbox.Policy {
	task := spec.Task
	return sandbox.Policy{
		AllowedRoot:     task.SandboxAllowedRoot,
		ReadOnly:        task.SandboxReadOnly,
		Network:         task.SandboxNetwork,
		AllowedHosts:    spec.ShellNetworkAllowedHosts,
		AllowPrivateIPs: spec.ShellNetworkAllowPrivateIPs,
	}
}

func fileFailure(spec ExecutionSpec, operation, path, message, errorKind string) *ExecutionResult {
	finishedAt := time.Now().UTC()
	step := newExecutionStep(spec, "file", "File operation", "file", fileOperationInput(spec.Task, operation))
	finalizeExecutionStep(&step, finishedAt, "failed", telemetry.ResultError, message, errorKind, nil)
	artifact := newInlineArtifact(spec, step.ID, "stderr", "file-error.txt", "File executor error output", "", message, "ready", finishedAt)
	return newExecutionResult("failed", []types.TaskStep{step}, []types.TaskArtifact{artifact}, message, "error", message)
}

func fileOperationInput(task types.Task, operation string) map[string]any {
	return map[string]any{
		"operation":         operation,
		"path":              task.FilePath,
		"working_directory": task.WorkingDirectory,
	}
}

func newExecutionStep(spec ExecutionSpec, kind, title, toolName string, input map[string]any) types.TaskStep {
	stepIndex := 1
	if spec.ResumeCheckpoint != nil && spec.ResumeCheckpoint.LastStepIndex > 0 {
		stepIndex = spec.ResumeCheckpoint.LastStepIndex + 1
	}
	if spec.ResumeCheckpoint != nil {
		if input == nil {
			input = map[string]any{}
		}
		input["resume_from_run_id"] = spec.ResumeCheckpoint.SourceRunID
		input["resume_from_step_id"] = spec.ResumeCheckpoint.LastCompletedStepID
		input["resume_from_event_sequence"] = spec.ResumeCheckpoint.LastEventSequence
		input["reason"] = spec.ResumeCheckpoint.Reason
	}
	return types.TaskStep{
		ID:        spec.NewID("step"),
		TaskID:    spec.Task.ID,
		RunID:     spec.Run.ID,
		Index:     stepIndex,
		Kind:      kind,
		Title:     title,
		Status:    "running",
		Phase:     "execution",
		Result:    telemetry.ResultSuccess,
		ToolName:  toolName,
		Input:     input,
		StartedAt: spec.StartedAt,
		RequestID: spec.RequestID,
		TraceID:   spec.TraceID,
	}
}

func finalizeExecutionStep(step *types.TaskStep, finishedAt time.Time, status, result, errMessage, errKind string, outputSummary map[string]any) {
	step.Status = status
	step.Result = result
	step.Error = errMessage
	step.ErrorKind = errKind
	step.OutputSummary = outputSummary
	step.FinishedAt = finishedAt
}

func newInlineArtifact(spec ExecutionSpec, stepID, kind, name, description, path, content, status string, createdAt time.Time) types.TaskArtifact {
	return types.TaskArtifact{
		ID:          spec.NewID("artifact"),
		TaskID:      spec.Task.ID,
		RunID:       spec.Run.ID,
		StepID:      stepID,
		Kind:        kind,
		Name:        name,
		Description: description,
		MimeType:    "text/plain",
		StorageKind: "inline",
		Path:        path,
		ContentText: content,
		SizeBytes:   int64(len(content)),
		Status:      status,
		CreatedAt:   createdAt,
		RequestID:   spec.RequestID,
		TraceID:     spec.TraceID,
	}
}

func newExecutionResult(status string, steps []types.TaskStep, artifacts []types.TaskArtifact, lastError, otelStatusCode, otelStatusMessage string) *ExecutionResult {
	return &ExecutionResult{
		Status:            status,
		Steps:             steps,
		Artifacts:         artifacts,
		LastError:         lastError,
		OtelStatusCode:    otelStatusCode,
		OtelStatusMessage: otelStatusMessage,
	}
}
