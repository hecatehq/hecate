package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/internal/sandbox"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/internal/workspace"
	"github.com/hecatehq/hecate/internal/workspacefs"
	"github.com/hecatehq/hecate/pkg/types"
)

type Executor interface {
	Execute(ctx context.Context, spec ExecutionSpec) (*ExecutionResult, error)
}

var sensitiveCommandAssignmentRE = regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:API_?KEY|TOKEN|SECRET|PASSWORD|PASS|PRIVATE_?KEY|CREDENTIAL)[A-Z0-9_]*\s*=\s*)(?:"(?:\\.|[^"\\])*"|'[^']*'|[^\s;&|]+)`)

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
	GetArtifact      func(taskID, artifactID string) (types.TaskArtifact, bool, error)
	// EmitRunEvent appends an event to the run's event stream. Used
	// by executors that want to emit telemetry beyond steps and
	// artifacts — currently the agent loop's MCP dispatcher, which
	// records protocol-shaped MCP tool events alongside its
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
	// RTKEnabled is the per-chat/per-task command-output compaction
	// setting. Shell and git executors pass it through to the sandbox
	// instead of consulting process-global environment.
	RTKEnabled bool
	// ToolCallID/ToolName identify the model tool call that delegated
	// into this executor. Direct shell/git/file tasks leave these
	// empty; the executor falls back to its own step id/name for
	// typed runtime events.
	ToolCallID string
	ToolName   string
	// InputMessage is a runtime-hydrated replacement for the fresh or appended
	// user prompt. It can carry rich content such as an image attachment. The
	// resolver owns its storage boundary; executors must not persist inline
	// binary bodies in artifacts.
	InputMessage *types.Message
	// ChatRequirements fences every model turn that retains InputMessage in
	// conversation context (for example, image-capability and provider bounds).
	ChatRequirements types.ChatRequestRequirements
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
	// AppendUserPrompt, when set on a cross-run continuation,
	// appends a new user message after the hydrated conversation.
	// Used by ACP/editor sessions where one durable Hecate task
	// receives multiple prompts over time.
	AppendUserPrompt string
}

type ExecutionResult struct {
	Status            string
	Steps             []types.TaskStep
	Artifacts         []types.TaskArtifact
	LastError         string
	OtelStatusCode    string
	OtelStatusMessage string
	// Provider/ProviderKind/Model capture the route that actually
	// served the agent-loop LLM turn. The run starts with the
	// operator's requested provider hint ("auto" is common), but the
	// UI and resume path need the resolved provider once routing has
	// happened.
	Provider     string
	ProviderKind string
	Model        string
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
	workspace workspace.Workspace
}

func NewShellExecutor(ws workspace.Workspace) *ShellExecutor {
	return &ShellExecutor{workspace: ensureWorkspace(ws)}
}

func (e *ShellExecutor) Execute(ctx context.Context, spec ExecutionSpec) (*ExecutionResult, error) {
	if spec.NewID == nil {
		return nil, fmt.Errorf("resource id generator is required")
	}
	command := spec.Task.ShellCommand
	if command == "" {
		return nil, fmt.Errorf("shell command is required")
	}
	return executeStreamingCommand(ctx, e.workspace, spec, streamingCommandSpec{
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
	workspace workspace.Workspace
}

func NewFileExecutor(ws workspace.Workspace) *FileExecutor {
	return &FileExecutor{workspace: ensureWorkspace(ws)}
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
	beforeContent, beforeExists, _, err := fileContentBeforeWrite(request)
	if err != nil {
		return fileFailure(spec, operation, spec.Task.FilePath, err.Error(), fileErrorKind(err)), nil
	}
	var (
		fileResult sandbox.FileResult
	)
	switch operation {
	case "write":
		fileResult, err = e.workspace.WriteFile(ctx, request)
	case "append":
		fileResult, err = e.workspace.AppendFile(ctx, request)
	default:
		return fileFailure(spec, operation, spec.Task.FilePath, fmt.Sprintf("unsupported file operation %q", operation), "file_operation_unsupported"), nil
	}
	if err != nil {
		return fileFailure(spec, operation, spec.Task.FilePath, err.Error(), fileErrorKind(err)), nil
	}
	afterContent, err := readFileWithWorkspacePolicy(request, fileResult.Path)
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
	patchArtifact := newPatchArtifact(spec, step.ID, operation, spec.Task.FilePath, fileResult.Path, beforeContent, string(afterContent), beforeExists, finishedAt)
	emitFilePatchEvent(spec, step.ID, operation, patchArtifact, fileResult, beforeExists)

	return newExecutionResult(status, []types.TaskStep{step}, []types.TaskArtifact{artifact, patchArtifact}, lastError, otelStatusCode, otelStatusMessage), nil
}

type GitExecutor struct {
	workspace workspace.Workspace
}

func NewGitExecutor(ws workspace.Workspace) *GitExecutor {
	return &GitExecutor{workspace: ensureWorkspace(ws)}
}

func (e *GitExecutor) Execute(ctx context.Context, spec ExecutionSpec) (*ExecutionResult, error) {
	if spec.NewID == nil {
		return nil, fmt.Errorf("resource id generator is required")
	}
	command := spec.Task.GitCommand
	if command == "" {
		return nil, fmt.Errorf("git command is required")
	}
	return executeStreamingCommand(ctx, e.workspace, spec, streamingCommandSpec{
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

func executeStreamingCommand(ctx context.Context, ws workspace.Workspace, spec ExecutionSpec, commandSpec streamingCommandSpec) (*ExecutionResult, error) {
	timeout := commandTimeout(spec.Task)
	workingDirectory := commandWorkingDirectory(spec.Task)
	policy := taskPolicy(spec)
	wrapperKind := string(sandbox.HealthInfo().Kind)
	outputLimit := sandbox.DefaultResourceLimits().MaxOutputBytes
	displayCommand := commandSpec.displayCommand
	if displayCommand == "" {
		displayCommand = commandSpec.command
	}

	stepInput := map[string]any{
		"command":                                displayCommand,
		"working_directory":                      workingDirectory,
		"timeout_ms":                             timeout,
		telemetry.AttrHecateToolWorkingDirectory: workingDirectory,
		telemetry.AttrHecateToolTimeoutMS:        timeout,
	}
	mergeMap(stepInput, sandboxTelemetryAttrs(policy, wrapperKind, outputLimit, spec.RTKEnabled))
	mergeMap(stepInput, rtkCommandTelemetryAttrs(commandSpec.command, spec.RTKEnabled))
	step := newExecutionStep(spec, commandSpec.kind, commandSpec.title, commandSpec.toolName, stepInput)
	toolCallID := firstNonEmpty(spec.ToolCallID, step.ID)
	eventToolName := firstNonEmpty(spec.ToolName, commandSpec.toolName)
	if commandSpec.kind == "shell" {
		baseEvent := shellToolTelemetryAttrs(toolCallID, eventToolName, policy, wrapperKind, outputLimit, spec.RTKEnabled)
		baseEvent["turn_index"] = 0
		emitTypedShellRunEvent(spec, runtimeevents.EventToolInvoked.String(), cloneMap(baseEvent))
		emitTypedShellRunEvent(spec, runtimeevents.EventToolStarted.String(), cloneMap(baseEvent))
		commandEvent := shellToolTelemetryAttrs(toolCallID, eventToolName, policy, wrapperKind, outputLimit, spec.RTKEnabled)
		commandEvent["argv"] = sandbox.ShellArgv(sandbox.Command{Command: commandSpec.command, RTKEnabled: spec.RTKEnabled})
		commandEvent["cwd"] = workingDirectory
		commandEvent["env_keys"] = []string{"PATH", "HOME"}
		commandEvent["sandbox_layer"] = wrapperKind
		commandEvent["timeout_ms"] = timeout
		commandEvent[telemetry.AttrHecateToolWorkingDirectory] = workingDirectory
		commandEvent[telemetry.AttrHecateToolTimeoutMS] = timeout
		commandEvent["command_string"] = displayCommand
		mergeMap(commandEvent, rtkCommandTelemetryAttrs(commandSpec.command, spec.RTKEnabled))
		emitTypedShellRunEvent(spec, runtimeevents.EventToolShellCommand.String(), commandEvent)
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
	resultData, err := ws.RunStreaming(ctx, sandbox.Command{
		Command:          commandSpec.command,
		WorkingDirectory: workingDirectory,
		Timeout:          time.Duration(timeout) * time.Millisecond,
		Policy:           policy,
		Limits:           sandbox.ResourceLimits{MaxOutputBytes: outputLimit},
		RTKEnabled:       spec.RTKEnabled,
	}, func(chunk sandbox.OutputChunk) {
		switch chunk.Stream {
		case "stdout":
			offset := stdoutOffset
			stdoutOffset += len(chunk.Text)
			stdoutArtifact.ContentText += chunk.Text
			stdoutArtifact.SizeBytes = int64(len(stdoutArtifact.ContentText))
			_ = upsertTaskArtifact(spec, stdoutArtifact)
			if commandSpec.kind == "shell" {
				emitTypedShellRunEvent(spec, runtimeevents.EventToolShellOutputChunk.String(), map[string]any{
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
				emitTypedShellRunEvent(spec, runtimeevents.EventToolShellOutputChunk.String(), map[string]any{
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

	stepOutput := commandResultTelemetryAttrs(resultData, err)
	stepOutput["stdout_bytes"] = len(resultData.Stdout)
	stepOutput["stderr_bytes"] = len(resultData.Stderr)
	stepOutput["exit_code"] = resultData.ExitCode
	mergeMap(stepOutput, sandboxTelemetryAttrs(policy, wrapperKind, outputLimit, spec.RTKEnabled))
	mergeMap(stepOutput, rtkCommandTelemetryAttrs(commandSpec.command, spec.RTKEnabled))
	finalizeExecutionStep(&step, finishedAt, status, result, lastError, commandErrorKind(err, commandSpec.timeoutErrorKind, commandSpec.defaultErrorKind), stepOutput)
	step.ExitCode = resultData.ExitCode
	if commandSpec.kind == "shell" && !sandbox.IsPolicyDenied(err) {
		exitEvent := map[string]any{
			"tool_call_id": toolCallID,
			"exit_code":    resultData.ExitCode,
			"signal":       nil,
			"stdout_bytes": len(resultData.Stdout),
			"stderr_bytes": len(resultData.Stderr),
			"truncated":    sandbox.IsOutputLimitExceeded(err),
		}
		mergeMap(exitEvent, commandResultTelemetryAttrs(resultData, err))
		emitTypedShellRunEvent(spec, runtimeevents.EventToolShellExited.String(), exitEvent)
	}
	if commandSpec.kind == "shell" {
		eventType := runtimeevents.EventToolCompleted.String()
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			eventType = runtimeevents.EventToolTimedOut.String()
		case errors.Is(err, context.Canceled):
			eventType = runtimeevents.EventToolCancelled.String()
		case err != nil:
			eventType = runtimeevents.EventToolFailed.String()
		}
		data := map[string]any{
			"tool_call_id": toolCallID,
			"tool_name":    eventToolName,
			"kind":         "shell",
			"duration_ms":  finishedAt.Sub(step.StartedAt).Milliseconds(),
			"summary":      shellEventSummary(status, resultData.ExitCode, lastError),
		}
		mergeMap(data, commandResultTelemetryAttrs(resultData, err))
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

func rtkCommandTelemetryAttrs(command string, enabled bool) map[string]any {
	if !enabled {
		return nil
	}
	return map[string]any{
		telemetry.AttrHecateSandboxRTKCommandBefore: formatCommandArgvForTelemetry(sandbox.ShellArgv(sandbox.Command{Command: command})),
		telemetry.AttrHecateSandboxRTKCommandAfter:  formatCommandArgvForTelemetry(sandbox.ShellArgv(sandbox.Command{Command: command, RTKEnabled: true})),
	}
}

func formatCommandArgvForTelemetry(argv []string) string {
	return redactCommandTelemetryValue(formatCommandArgv(argv))
}

func formatCommandArgv(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		if strings.ContainsAny(arg, " \t\r\n\"'\\$`") {
			parts = append(parts, strconv.Quote(arg))
			continue
		}
		parts = append(parts, arg)
	}
	return strings.Join(parts, " ")
}

func redactCommandTelemetryValue(value string) string {
	return sensitiveCommandAssignmentRE.ReplaceAllString(value, `${1}<redacted>`)
}

func sandboxTelemetryAttrs(policy sandbox.Policy, wrapperKind string, outputLimit int64, rtkEnabled bool) map[string]any {
	attrs := map[string]any{
		telemetry.AttrHecateSandboxWrapperKind:    wrapperKind,
		telemetry.AttrHecateSandboxNetworkEnabled: policy.Network,
		telemetry.AttrHecateSandboxReadOnly:       policy.ReadOnly,
		telemetry.AttrHecateSandboxOutputLimit:    outputLimit,
	}
	if rtkEnabled {
		attrs[telemetry.AttrHecateSandboxRTKEnabled] = true
	}
	return attrs
}

func shellToolTelemetryAttrs(toolCallID, toolName string, policy sandbox.Policy, wrapperKind string, outputLimit int64, rtkEnabled bool) map[string]any {
	attrs := map[string]any{
		"tool_call_id": toolCallID,
		"tool_name":    toolName,
		"kind":         "shell",
	}
	mergeMap(attrs, sandboxTelemetryAttrs(policy, wrapperKind, outputLimit, rtkEnabled))
	return attrs
}

func commandResultTelemetryAttrs(result sandbox.Result, err error) map[string]any {
	return map[string]any{
		telemetry.AttrHecateToolStdoutBytes:     len(result.Stdout),
		telemetry.AttrHecateToolStderrBytes:     len(result.Stderr),
		telemetry.AttrHecateToolExitCode:        result.ExitCode,
		telemetry.AttrHecateToolTimedOut:        errors.Is(err, context.DeadlineExceeded),
		telemetry.AttrHecateToolCancelled:       errors.Is(err, context.Canceled),
		telemetry.AttrHecateToolOutputTruncated: sandbox.IsOutputLimitExceeded(err),
	}
}

func mergeMap(dst map[string]any, src map[string]any) {
	for key, value := range src {
		dst[key] = value
	}
}

func cloneMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	mergeMap(dst, src)
	return dst
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

// ensureWorkspace returns a usable Workspace, defaulting to a fresh
// LocalWorkspace when the caller didn't supply one. The orchestrator
// historically tolerated a nil sandbox.Executor by falling back to
// the local impl; preserve that for callers wiring the orchestrator
// from tests and small one-shot scripts.
func ensureWorkspace(ws workspace.Workspace) workspace.Workspace {
	if ws == nil {
		return workspace.NewLocalWorkspace()
	}
	return ws
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

func fileContentBeforeWrite(request sandbox.FileRequest) (content string, exists bool, resolvedPath string, err error) {
	resolvedPath, err = sandbox.ResolvePath(request.WorkingDirectory, request.Path, request.Policy)
	if err != nil {
		return "", false, "", err
	}
	raw, err := readFileWithWorkspacePolicy(request, resolvedPath)
	if err == nil {
		return string(raw), true, resolvedPath, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return "", false, resolvedPath, nil
	}
	return "", false, resolvedPath, err
}

func readFileWithWorkspacePolicy(request sandbox.FileRequest, path string) ([]byte, error) {
	allowedRoot := strings.TrimSpace(request.Policy.AllowedRoot)
	if allowedRoot == "" {
		return os.ReadFile(path)
	}
	root, err := filepath.Abs(allowedRoot)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}
	fsys, err := workspacefs.New(root)
	if err != nil {
		return nil, err
	}
	raw, _, err := fsys.ReadFile(rel)
	return raw, err
}

func newPatchArtifact(spec ExecutionSpec, stepID, operation, displayPath, artifactPath, before, after string, beforeExists bool, createdAt time.Time) types.TaskArtifact {
	patch := unifiedPatch(displayPath, before, after, beforeExists)
	artifact := newInlineArtifact(spec, stepID, "patch", filepath.Base(displayPath)+".patch", "Unified diff produced by a file-writing tool", artifactPath, patch, "applied", createdAt)
	artifact.MimeType = "text/x-diff"
	sum := sha256.Sum256([]byte(patch))
	artifact.SHA256 = hex.EncodeToString(sum[:])
	return artifact
}

func emitFilePatchEvent(spec ExecutionSpec, stepID, operation string, artifact types.TaskArtifact, result sandbox.FileResult, beforeExists bool) {
	if spec.EmitRunEvent == nil {
		return
	}
	toolCallID := firstNonEmpty(spec.ToolCallID, stepID)
	toolName := firstNonEmpty(spec.ToolName, "file")
	data := map[string]any{
		"tool_call_id":                           toolCallID,
		"tool_name":                              toolName,
		"kind":                                   "file",
		"operation":                              operation,
		"path":                                   artifact.Path,
		"artifact_id":                            artifact.ID,
		"bytes_written":                          result.BytesWritten,
		"diff_bytes":                             artifact.SizeBytes,
		"before_existed":                         beforeExists,
		"artifact_status":                        artifact.Status,
		telemetry.AttrHecateToolFileOperation:    operation,
		telemetry.AttrHecateToolFileBytesWritten: result.BytesWritten,
		telemetry.AttrHecateToolFileDiffBytes:    artifact.SizeBytes,
		telemetry.AttrHecateToolFileBeforeExisted:  beforeExists,
		telemetry.AttrHecateToolFileArtifactStatus: artifact.Status,
	}
	spec.EmitRunEvent(runtimeevents.EventFilePatch.String(), data)
}

func unifiedPatch(path, before, after string, beforeExists bool) string {
	oldPath := "a/" + filepath.ToSlash(path)
	if !beforeExists {
		oldPath = "/dev/null"
	}
	newPath := "b/" + filepath.ToSlash(path)
	beforeLines := patchLines(before)
	afterLines := patchLines(after)

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n", oldPath)
	fmt.Fprintf(&b, "+++ %s\n", newPath)
	fmt.Fprintf(&b, "@@ -1,%d +1,%d @@\n", len(beforeLines), len(afterLines))
	for _, line := range beforeLines {
		b.WriteByte('-')
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, line := range afterLines {
		b.WriteByte('+')
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func patchLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
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
