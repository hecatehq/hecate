package api

import "github.com/hecatehq/hecate/internal/eventprotocol"

type CreateTaskRequest struct {
	Title  string `json:"title"`
	Prompt string `json:"prompt"`
	// ProjectID links a manually-created task to the selected project.
	// Empty / omitted creates an unprojected task.
	ProjectID string `json:"project_id,omitempty"`
	// SystemPrompt is the per-task system prompt for agent_loop runs.
	// It's the narrowest layer in the four-level composition (global
	// → tenant → workspace CLAUDE.md/AGENTS.md → this).
	SystemPrompt       string `json:"system_prompt,omitempty"`
	ExecutionProfile   string `json:"execution_profile"`
	Repo               string `json:"repo"`
	BaseBranch         string `json:"base_branch"`
	WorkspaceMode      string `json:"workspace_mode"`
	ExecutionKind      string `json:"execution_kind"`
	ShellCommand       string `json:"shell_command"`
	GitCommand         string `json:"git_command"`
	WorkingDirectory   string `json:"working_directory"`
	FileOperation      string `json:"file_operation"`
	FilePath           string `json:"file_path"`
	FileContent        string `json:"file_content"`
	SandboxAllowedRoot string `json:"sandbox_allowed_root"`
	SandboxReadOnly    bool   `json:"sandbox_read_only"`
	SandboxNetwork     bool   `json:"sandbox_network"`
	TimeoutMS          int    `json:"timeout_ms"`
	Priority           string `json:"priority"`
	RequestedModel     string `json:"requested_model"`
	RequestedProvider  string `json:"requested_provider"`
	BudgetMicrosUSD    int64  `json:"budget_micros_usd"`
	// MCPServers, when non-empty on an agent_loop task, configures
	// external MCP servers the run should bring up and expose to the
	// LLM. Each entry is one stdio subprocess; its tools become
	// callable as `mcp__<name>__<tool>` alongside the built-ins.
	MCPServers []MCPServerConfigItem `json:"mcp_servers,omitempty"`
}

// MCPServerConfigItem is the wire shape of an MCP-server entry on a
// task. Mirrors types.MCPServerConfig — duplicated here so the API
// package owns its JSON contract independent of the internal types.
// Exactly one of command or url must be set.
type MCPServerConfigItem struct {
	Name string `json:"name"`
	// Stdio transport (mutually exclusive with url):
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	// HTTP transport (mutually exclusive with command):
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// ApprovalPolicy gates how the agent loop dispatches tool calls
	// from this server. One of "auto" | "require_approval" | "block";
	// empty = auto. See pkg/types task.go for the contract.
	ApprovalPolicy string `json:"approval_policy,omitempty"`
}

type TaskLifecycleRequest struct {
	ID string `json:"id"`
}

type ResolveTaskApprovalRequest struct {
	Decision string `json:"decision"`
	Note     string `json:"note"`
}

type RetryTaskRunRequest struct {
	Reason string `json:"reason"`
}

type ResumeTaskRunRequest struct {
	Reason string `json:"reason"`
	// BudgetMicrosUSD, when > 0, replaces the task's per-task cost
	// ceiling before the resumed run is queued. Used by the
	// "Raise ceiling and resume" affordance on cost_ceiling_exceeded
	// failures so operators don't have to update the task and
	// resume in two separate calls. Zero / unset preserves the
	// existing ceiling.
	BudgetMicrosUSD int64 `json:"budget_micros_usd,omitempty"`
}

type ContinueTaskRunRequest struct {
	Prompt string `json:"prompt"`
}

// RetryFromTurnRequest is the body for
// POST /hecate/v1/tasks/{id}/runs/{run_id}/retry-from-turn — re-run an
// agent_loop run starting at turn N with the prior conversation
// context preserved up to (but not including) that turn's assistant
// message. Turn must be >= 1 and <= the source run's completed
// assistant-turn count.
type RetryFromTurnRequest struct {
	Turn   int    `json:"turn"`
	Reason string `json:"reason"`
}

type AppendTaskRunEventRequest struct {
	Type   string         `json:"type"`
	StepID string         `json:"step_id"`
	Status string         `json:"status"`
	Note   string         `json:"note"`
	Data   map[string]any `json:"data"`
}

type TaskResponse struct {
	Object string   `json:"object"`
	Data   TaskItem `json:"data"`
}

type TasksResponse struct {
	Object string     `json:"object"`
	Data   []TaskItem `json:"data"`
}

type TaskRunResponse struct {
	Object string      `json:"object"`
	Data   TaskRunItem `json:"data"`
}

type TaskRunStreamEventResponse struct {
	Object string                 `json:"object"`
	Data   TaskRunStreamEventData `json:"data"`
}

type TaskRunsResponse struct {
	Object string        `json:"object"`
	Data   []TaskRunItem `json:"data"`
}

type TaskStepResponse struct {
	Object string       `json:"object"`
	Data   TaskStepItem `json:"data"`
}

type TaskStepsResponse struct {
	Object string         `json:"object"`
	Data   []TaskStepItem `json:"data"`
}

type TaskApprovalResponse struct {
	Object string           `json:"object"`
	Data   TaskApprovalItem `json:"data"`
}

type TaskApprovalsResponse struct {
	Object string             `json:"object"`
	Data   []TaskApprovalItem `json:"data"`
}

type TaskArtifactResponse struct {
	Object string           `json:"object"`
	Data   TaskArtifactItem `json:"data"`
}

type TaskArtifactsResponse struct {
	Object string             `json:"object"`
	Data   []TaskArtifactItem `json:"data"`
}

type TaskPatchResponse struct {
	Object string        `json:"object"`
	Data   TaskPatchItem `json:"data"`
}

type TaskPatchesResponse struct {
	Object string          `json:"object"`
	Data   []TaskPatchItem `json:"data"`
}

type TaskRunEventsResponse struct {
	Object string                   `json:"object"`
	Data   []eventprotocol.Envelope `json:"data"`
}

// EventsResponse is the body of GET /hecate/v1/events — a paginated cross-run
// event feed.
type EventsResponse struct {
	Object string                   `json:"object"`
	Data   []eventprotocol.Envelope `json:"data"`
	// NextAfterSequence is the sequence to pass back as
	// `after_sequence` to fetch the next page. Equals the highest
	// sequence in Data; zero when Data is empty.
	NextAfterSequence int64 `json:"next_after_sequence,omitempty"`
}

type TaskItem struct {
	ID                          string `json:"id"`
	Title                       string `json:"title"`
	Prompt                      string `json:"prompt"`
	ProjectID                   string `json:"project_id,omitempty"`
	SystemPrompt                string `json:"system_prompt,omitempty"`
	WorkspaceSystemPromptPolicy string `json:"workspace_system_prompt_policy,omitempty"`
	ExecutionProfile            string `json:"execution_profile,omitempty"`
	OriginKind                  string `json:"origin_kind,omitempty"`
	OriginID                    string `json:"origin_id,omitempty"`
	Repo                        string `json:"repo,omitempty"`
	BaseBranch                  string `json:"base_branch,omitempty"`
	WorkspaceMode               string `json:"workspace_mode,omitempty"`
	ExecutionKind               string `json:"execution_kind,omitempty"`
	ShellCommand                string `json:"shell_command,omitempty"`
	GitCommand                  string `json:"git_command,omitempty"`
	WorkingDirectory            string `json:"working_directory,omitempty"`
	FileOperation               string `json:"file_operation,omitempty"`
	FilePath                    string `json:"file_path,omitempty"`
	FileContent                 string `json:"file_content,omitempty"`
	SandboxAllowedRoot          string `json:"sandbox_allowed_root,omitempty"`
	SandboxReadOnly             bool   `json:"sandbox_read_only,omitempty"`
	SandboxNetwork              bool   `json:"sandbox_network,omitempty"`
	TimeoutMS                   int    `json:"timeout_ms,omitempty"`
	Status                      string `json:"status"`
	Priority                    string `json:"priority,omitempty"`
	RequestedModel              string `json:"requested_model,omitempty"`
	RequestedProvider           string `json:"requested_provider,omitempty"`
	BudgetMicrosUSD             int64  `json:"budget_micros_usd,omitempty"`
	LatestRunID                 string `json:"latest_run_id,omitempty"`
	// LatestModel / LatestProvider are the model + provider the
	// most recent run actually used (after routing). They differ
	// from RequestedModel / RequestedProvider when the operator
	// asked for "auto" or specified a model the router substituted.
	// Surfaced on the task list so operators see at a glance which
	// engine ran without drilling into the run detail.
	LatestModel          string `json:"latest_model,omitempty"`
	LatestProvider       string `json:"latest_provider,omitempty"`
	PendingApprovalCount int    `json:"pending_approval_count,omitempty"`
	StepCount            int    `json:"step_count,omitempty"`
	ArtifactCount        int    `json:"artifact_count,omitempty"`
	LastError            string `json:"last_error,omitempty"`
	CreatedAt            string `json:"created_at,omitempty"`
	UpdatedAt            string `json:"updated_at,omitempty"`
	StartedAt            string `json:"started_at,omitempty"`
	FinishedAt           string `json:"finished_at,omitempty"`
	RootTraceID          string `json:"root_trace_id,omitempty"`
	LatestTraceID        string `json:"latest_trace_id,omitempty"`
	LatestRequestID      string `json:"latest_request_id,omitempty"`
	// MCPServers echoes the configured external MCP servers (if any).
	// Surfaced on the task detail so operators can see at a glance
	// which external tool sources a run will bring up.
	MCPServers []MCPServerConfigItem `json:"mcp_servers,omitempty"`
}

type TaskRunItem struct {
	ID                 string `json:"id"`
	TaskID             string `json:"task_id"`
	Number             int    `json:"number"`
	Status             string `json:"status"`
	Orchestrator       string `json:"orchestrator,omitempty"`
	Model              string `json:"model,omitempty"`
	Provider           string `json:"provider,omitempty"`
	ProviderKind       string `json:"provider_kind,omitempty"`
	WorkspaceID        string `json:"workspace_id,omitempty"`
	WorkspacePath      string `json:"workspace_path,omitempty"`
	StepCount          int    `json:"step_count,omitempty"`
	ApprovalCount      int    `json:"approval_count,omitempty"`
	ArtifactCount      int    `json:"artifact_count,omitempty"`
	TotalCostMicrosUSD int64  `json:"total_cost_micros_usd,omitempty"`
	// PriorCostMicrosUSD is the cumulative LLM spend of every prior
	// run in this run's resume chain (zero for fresh runs). Add it
	// to TotalCostMicrosUSD to get the task-level cumulative spend.
	PriorCostMicrosUSD int64  `json:"prior_cost_micros_usd,omitempty"`
	LastError          string `json:"last_error,omitempty"`
	StartedAt          string `json:"started_at,omitempty"`
	FinishedAt         string `json:"finished_at,omitempty"`
	RequestID          string `json:"request_id,omitempty"`
	TraceID            string `json:"trace_id,omitempty"`
	RootSpanID         string `json:"root_span_id,omitempty"`
	OtelStatusCode     string `json:"otel_status_code,omitempty"`
	OtelStatusMessage  string `json:"otel_status_message,omitempty"`
}

type TaskRunStreamEventData struct {
	Sequence  int                `json:"sequence"`
	Terminal  bool               `json:"terminal,omitempty"`
	Run       TaskRunItem        `json:"run"`
	Steps     []TaskStepItem     `json:"steps,omitempty"`
	Artifacts []TaskArtifactItem `json:"artifacts,omitempty"`
	Activity  []TaskActivityItem `json:"activity,omitempty"`
	// Approvals are this task's approvals scoped to the run being
	// streamed. Carried in every snapshot so the UI's approval banner
	// stays in lock-step with run.status — without this the banner
	// could drift (e.g. a mid-loop approval gets created mid-stream
	// but the UI wouldn't see it until the next manual refresh, and
	// conversely a server-resolved approval might still render in the
	// banner because the UI cached the old state).
	Approvals []TaskApprovalItem `json:"approvals,omitempty"`
	// Turn carries the per-turn cost breakdown when the snapshot was
	// driven by a `turn.completed` event. It's populated only
	// for that event type — every other snapshot leaves Turn nil.
	// Lets the UI render a live per-turn cost/tokens summary without having
	// to subscribe to the public events stream separately.
	Turn      *TaskRunStreamTurnCost `json:"turn,omitempty"`
	EventType string                 `json:"event_type,omitempty"`
}

type TaskActivityItem struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	Status      string         `json:"status,omitempty"`
	Title       string         `json:"title,omitempty"`
	StepID      string         `json:"step_id,omitempty"`
	ArtifactID  string         `json:"artifact_id,omitempty"`
	ApprovalID  string         `json:"approval_id,omitempty"`
	ToolName    string         `json:"tool_name,omitempty"`
	Kind        string         `json:"kind,omitempty"`
	Path        string         `json:"path,omitempty"`
	Summary     map[string]any `json:"summary,omitempty"`
	OccurredAt  string         `json:"occurred_at,omitempty"`
	Terminal    bool           `json:"terminal,omitempty"`
	NeedsAction bool           `json:"needs_action,omitempty"`
}

// TaskRunStreamTurnCost mirrors the turn.completed event
// payload one-for-one. The field names match the event keys (we read
// them straight from the event data map) so a future generalization
// to other turn-shaped events stays trivial.
type TaskRunStreamTurnCost struct {
	Turn                    int    `json:"turn_index"`
	StepID                  string `json:"step_id,omitempty"`
	CostMicrosUSD           int64  `json:"cost_micros_usd"`
	RunCumulativeMicrosUSD  int64  `json:"run_cumulative_cost_micros_usd"`
	TaskCumulativeMicrosUSD int64  `json:"task_cumulative_cost_micros_usd"`
	ToolCallCount           int    `json:"tool_calls,omitempty"`
}

type TaskStepItem struct {
	ID            string         `json:"id"`
	TaskID        string         `json:"task_id"`
	RunID         string         `json:"run_id"`
	ParentStepID  string         `json:"parent_step_id,omitempty"`
	Index         int            `json:"index"`
	Kind          string         `json:"kind"`
	Title         string         `json:"title,omitempty"`
	Status        string         `json:"status"`
	Phase         string         `json:"phase,omitempty"`
	Result        string         `json:"result,omitempty"`
	ToolName      string         `json:"tool_name,omitempty"`
	Input         map[string]any `json:"input,omitempty"`
	OutputSummary map[string]any `json:"output_summary,omitempty"`
	ExitCode      int            `json:"exit_code,omitempty"`
	Error         string         `json:"error,omitempty"`
	ErrorKind     string         `json:"error_kind,omitempty"`
	ApprovalID    string         `json:"approval_id,omitempty"`
	StartedAt     string         `json:"started_at,omitempty"`
	FinishedAt    string         `json:"finished_at,omitempty"`
	RequestID     string         `json:"request_id,omitempty"`
	TraceID       string         `json:"trace_id,omitempty"`
	SpanID        string         `json:"span_id,omitempty"`
	ParentSpanID  string         `json:"parent_span_id,omitempty"`
}

type TaskApprovalItem struct {
	ID             string `json:"id"`
	TaskID         string `json:"task_id"`
	RunID          string `json:"run_id"`
	StepID         string `json:"step_id,omitempty"`
	Kind           string `json:"kind"`
	Status         string `json:"status"`
	Reason         string `json:"reason,omitempty"`
	RequestedBy    string `json:"requested_by,omitempty"`
	ResolvedBy     string `json:"resolved_by,omitempty"`
	ResolutionNote string `json:"resolution_note,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	ResolvedAt     string `json:"resolved_at,omitempty"`
	RequestID      string `json:"request_id,omitempty"`
	TraceID        string `json:"trace_id,omitempty"`
	SpanID         string `json:"span_id,omitempty"`
}

type TaskArtifactItem struct {
	ID          string `json:"id"`
	TaskID      string `json:"task_id"`
	RunID       string `json:"run_id"`
	StepID      string `json:"step_id,omitempty"`
	Kind        string `json:"kind"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
	StorageKind string `json:"storage_kind,omitempty"`
	Path        string `json:"path,omitempty"`
	ContentText string `json:"content_text,omitempty"`
	ObjectRef   string `json:"object_ref,omitempty"`
	SizeBytes   int64  `json:"size_bytes,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Status      string `json:"status,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	RequestID   string `json:"request_id,omitempty"`
	TraceID     string `json:"trace_id,omitempty"`
	SpanID      string `json:"span_id,omitempty"`
}

type TaskPatchItem struct {
	Artifact      TaskArtifactItem `json:"artifact"`
	Diff          string           `json:"diff"`
	Status        string           `json:"status"`
	Path          string           `json:"path,omitempty"`
	BeforeExisted bool             `json:"before_existed"`
}
