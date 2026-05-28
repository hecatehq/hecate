package types

import "time"

type Task struct {
	ID     string
	Title  string
	Prompt string
	// SystemPrompt is the per-task agent system prompt. When set, it
	// becomes the narrowest layer in the composition: global default →
	// workspace CLAUDE.md/AGENTS.md → this. Concatenated, broadest
	// first. Empty = no per-task add (still honors the broader layers).
	SystemPrompt       string
	Repo               string
	BaseBranch         string
	WorkspaceMode      string
	ExecutionKind      string
	ExecutionProfile   string
	OriginKind         string
	OriginID           string
	ShellCommand       string
	GitCommand         string
	WorkingDirectory   string
	FileOperation      string
	FilePath           string
	FileContent        string
	SandboxAllowedRoot string
	SandboxReadOnly    bool
	SandboxNetwork     bool
	// RTKEnabled runs shell/git tool subprocesses through RTK for compact
	// command output. It is persisted on the task so Hecate Chat follow-up
	// runs keep the chat's command-output setting.
	RTKEnabled        bool
	TimeoutMS         int
	Status            string
	Priority          string
	RequestedModel    string
	RequestedProvider string
	BudgetMicrosUSD   int64
	LatestRunID       string
	LastError         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	StartedAt         time.Time
	FinishedAt        time.Time
	RootTraceID       string
	LatestTraceID     string
	LatestRequestID   string
	// MCPServers configures external MCP servers that an agent_loop run
	// should bring up and expose to the LLM as additional tools. Each
	// entry produces one stdio subprocess (`Command` + `Args`, env
	// merged from `Env`); its tools are registered alongside the
	// built-ins under names of the form `mcp__<server-name>__<tool>`.
	// Empty for non-agent_loop tasks and for agent_loop tasks that
	// don't need external tools.
	MCPServers []MCPServerConfig
}

// MCPEnvEncPrefix is the storage prefix for env values that were
// encrypted at task-creation time with the control-plane AES-GCM
// cipher. The full stored form is "enc:<base64ciphertext>". The MCP
// host factory decrypts these at subprocess spawn time so the plaintext
// token is never written to the task blob.
const MCPEnvEncPrefix = "enc:"

// MCP approval policy values. These control whether the agent loop
// dispatches an MCP tool call from the configured server immediately,
// pauses for operator approval, or refuses to call it at all.
//
//   - MCPApprovalAuto: dispatch immediately. Equivalent to leaving
//     the field empty.
//   - MCPApprovalRequireApproval: pause the agent loop on every call
//     to a tool from this server, emit an approval record, and resume
//     dispatch only after the operator approves.
//   - MCPApprovalBlock: never dispatch; the agent loop returns a tool
//     error to the LLM ("blocked by policy") so the model can pick a
//     different tool without involving the operator.
//
// Per-server granularity is intentional: real configs almost always
// gate or trust an entire server (the github server stays gated, the
// filesystem server stays auto). Per-tool gating is a follow-up.
const (
	MCPApprovalAuto            = "auto"
	MCPApprovalRequireApproval = "require_approval"
	MCPApprovalBlock           = "block"
)

// IsValidMCPApprovalPolicy reports whether v is a recognized policy
// value. Empty string is accepted as the implicit auto default — the
// API layer does not require operators to spell it out.
func IsValidMCPApprovalPolicy(v string) bool {
	switch v {
	case "", MCPApprovalAuto, MCPApprovalRequireApproval, MCPApprovalBlock:
		return true
	default:
		return false
	}
}

// MCPServerConfig describes one external MCP server an agent_loop run
// should connect to. Persisted as part of the task payload (no schema
// migration — the task is stored as a JSON blob in both the sqlite and
// postgres backends).
//
// Env values are stored in one of three forms:
//   - "$VAR_NAME" — resolved from the Hecate process environment at
//     subprocess spawn time; the token itself is never written to the DB.
//   - "enc:<base64>" — encrypted by the API layer with the control-plane
//     AES-GCM key; decrypted at spawn time.
//   - bare literal — stored as-is (acceptable for non-secret values
//     or when no cipher key is configured).
type MCPServerConfig struct {
	// Name is the operator-chosen label used to namespace this server's
	// tools (e.g. "filesystem" → tools become `mcp__filesystem__*`).
	// Must be non-empty and unique within a task; the gateway rejects
	// duplicates at create time.
	Name string
	// Stdio transport (mutually exclusive with URL):
	// Command is the executable that speaks MCP over stdio (e.g. "npx",
	// "uvx", or a path to a binary).
	Command string
	// Args are passed verbatim to the command.
	Args []string
	// Env is merged onto the spawned process's environment. Values may
	// be $VAR_NAME references, enc: ciphertext, or bare literals.
	Env map[string]string
	// HTTP transport (mutually exclusive with Command):
	// URL is the MCP endpoint (e.g. "https://api.example.com/mcp").
	URL string
	// Headers are sent on every HTTP request. Values follow the same
	// $VAR_NAME / enc: / literal rules as Env values.
	Headers map[string]string
	// ApprovalPolicy gates how the agent loop dispatches tool calls
	// from this server. One of "auto" | "require_approval" | "block",
	// or empty (interpreted as auto). See the MCPApproval* constants
	// above for the contract. Per-server, not per-tool.
	ApprovalPolicy string
}

type TaskRun struct {
	ID                 string
	TaskID             string
	Number             int
	Status             string
	Orchestrator       string
	Model              string
	Provider           string
	ProviderKind       string
	WorkspaceID        string
	WorkspacePath      string
	StepCount          int
	ApprovalCount      int
	ArtifactCount      int
	TotalCostMicrosUSD int64
	// PriorCostMicrosUSD is the cumulative LLM spend of every prior
	// run in this run's resume chain (excluding this run itself).
	// Fresh runs are zero; resumed/retry-from-turn runs inherit the
	// source's PriorCost + Total. The per-task cost ceiling check
	// uses (PriorCost + this run's running spend) so a chain of
	// resumes can't escape the ceiling — without this a $5 ceiling
	// could be exceeded by the operator simply re-resuming N times.
	PriorCostMicrosUSD int64
	LastError          string
	StartedAt          time.Time
	FinishedAt         time.Time
	RequestID          string
	TraceID            string
	RootSpanID         string
	OtelStatusCode     string
	OtelStatusMessage  string
}

type TaskStep struct {
	ID            string
	TaskID        string
	RunID         string
	ParentStepID  string
	Index         int
	Kind          string
	Title         string
	Status        string
	Phase         string
	Result        string
	ToolName      string
	Input         map[string]any
	OutputSummary map[string]any
	ExitCode      int
	Error         string
	ErrorKind     string
	ApprovalID    string
	StartedAt     time.Time
	FinishedAt    time.Time
	RequestID     string
	TraceID       string
	SpanID        string
	ParentSpanID  string
}

type TaskApproval struct {
	ID             string
	TaskID         string
	RunID          string
	StepID         string
	Kind           string
	Status         string
	Reason         string
	RequestedBy    string
	ResolvedBy     string
	ResolutionNote string
	CreatedAt      time.Time
	ResolvedAt     time.Time
	RequestID      string
	TraceID        string
	SpanID         string
}

type TaskArtifact struct {
	ID          string
	TaskID      string
	RunID       string
	StepID      string
	Kind        string
	Name        string
	Description string
	MimeType    string
	StorageKind string
	Path        string
	ContentText string
	ObjectRef   string
	SizeBytes   int64
	SHA256      string
	Status      string
	CreatedAt   time.Time
	RequestID   string
	TraceID     string
	SpanID      string
}

type TaskRunEvent struct {
	ID        string
	TaskID    string
	RunID     string
	Sequence  int64
	EventType string
	Data      map[string]any
	CreatedAt time.Time
	RequestID string
	TraceID   string
}

func ApprovalResolvedEventData(approval TaskApproval) map[string]any {
	return map[string]any{
		"approval_id": approval.ID,
		"decision":    approval.Status,
		"by":          approval.ResolvedBy,
		"comment":     approval.ResolutionNote,
		"scope":       "once",
		"kind":        approval.Kind,
		"status":      approval.Status,
	}
}

func IsTerminalTaskRunStatus(status string) bool {
	switch status {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}
