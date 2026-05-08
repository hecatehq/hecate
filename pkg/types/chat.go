package types

import (
	"encoding/json"
	"time"
)

type ChatRequest struct {
	RequestID     string
	SessionID     string
	SessionTitle  string
	Model         string
	Messages      []Message
	MaxTokens     int
	Temperature   float64
	TopP          float64
	TopK          int
	StopSequences []string
	Scope         RequestScope
	Tools         []Tool
	ToolChoice    json.RawMessage
	Stream        bool
	// Extended thinking (Anthropic): {"type":"enabled","budget_tokens":N}
	Thinking json.RawMessage
	// Anthropic beta features (e.g. ["interleaved-thinking-2025-02-19"])
	Betas []string
	// ServiceTier requests an Anthropic service tier — `auto`,
	// `standard_only`, etc. Empty means the upstream picks. Passed
	// through verbatim on the wire so newer tier names work without
	// a code change. OpenAI-compat providers ignore the field.
	ServiceTier string
	// ResponseFormat is the OpenAI `response_format` field:
	//   {"type":"text"} | {"type":"json_object"} |
	//   {"type":"json_schema","json_schema":{...}}
	// We carry it as raw JSON to stay forward-compatible with new
	// shapes (e.g. structured-output extensions). OpenAI-compat
	// providers pass it through verbatim. Anthropic providers log
	// and drop it (Anthropic has no direct equivalent — operators
	// should use `tools` + `tool_choice` for structured output, or
	// the dedicated `output_format` field on newer Claude APIs).
	ResponseFormat json.RawMessage

	// Tier-2 OpenAI passthroughs. Each is captured from the
	// inbound /v1/chat/completions request and re-emitted verbatim
	// by the OpenAI provider. Anthropic providers log-and-drop
	// these (no direct equivalents). The set is intentionally
	// narrow — adding more fields is a one-line change here +
	// one-line at the inbound parser + one-line at the outbound
	// provider.

	// Seed is OpenAI's deterministic-sampling knob. Pointer
	// because 0 is a valid seed and we need to distinguish it
	// from "not set."
	Seed *int
	// PresencePenalty / FrequencyPenalty are OpenAI's [-2, 2]
	// repetition controls. 0.0 is the default — semantically
	// equivalent to omitting the field — so plain float64 with
	// omitempty on the wire is sufficient.
	PresencePenalty  float64
	FrequencyPenalty float64
	// Logprobs requests per-token log-probability data on the
	// response. TopLogprobs (0..20) caps how many alternatives
	// to include per position; only meaningful when Logprobs is
	// true.
	Logprobs    bool
	TopLogprobs int
	// LogitBias is a `{token_id: bias}` map (-100..100) that
	// nudges sampling. We keep it as raw JSON so callers can
	// pass either string or int keys (the API has accepted both
	// over time) without a typed-map conversion.
	LogitBias json.RawMessage
	// StreamOptions carries OpenAI's stream tuning — currently
	// {include_usage: bool} but the field is open-ended on the
	// upstream side. RawMessage stays forward-compatible.
	StreamOptions json.RawMessage
	// ParallelToolCalls toggles concurrent tool dispatch on the
	// OpenAI side (default true). Pointer because the user's
	// explicit "false" intent must survive — omitempty would drop
	// it. Anthropic's analog (`tool_choice.disable_parallel_tool_use`)
	// is captured separately via the existing ToolChoice
	// passthrough; cross-translation is not yet wired.
	ParallelToolCalls *bool
}

// RequestScope carries routing hints derived from the inbound request.
// In single-user mode it's just the provider hint — multi-tenant
// scoping (Tenant, AllowedProviders/Models, Principal) was removed.
type RequestScope struct {
	ProviderHint string
}

type Tool struct {
	Type         string          `json:"type"`
	Function     ToolFunction    `json:"function"`
	CacheControl json.RawMessage `json:"cache_control,omitempty"` // Anthropic prompt caching
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

// ContentBlock represents a single content block within a message, preserving
// provider-specific metadata such as cache_control for Anthropic prompt caching.
type ContentBlock struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	ID           string          `json:"id,omitempty"`            // tool_use
	Name         string          `json:"name,omitempty"`          // tool_use
	Input        json.RawMessage `json:"input,omitempty"`         // tool_use
	ToolUseID    string          `json:"tool_use_id,omitempty"`   // tool_result
	CacheControl json.RawMessage `json:"cache_control,omitempty"` // Anthropic prompt caching
	// Extended thinking fields (Anthropic)
	Thinking  string `json:"thinking,omitempty"`  // thinking block content
	Signature string `json:"signature,omitempty"` // thinking block signature (verified by Anthropic)
	Data      string `json:"data,omitempty"`      // redacted_thinking block opaque data
	// Image carries image-content data for multi-modal messages.
	// Set when Type == "image_url" (OpenAI-shaped) or Type ==
	// "image" (Anthropic-shaped). The Image struct unifies the
	// two upstreams' shapes: URL for url-based images, Data +
	// MediaType for inlined base64. Detail is an OpenAI hint
	// (low/high/auto) and is ignored by Anthropic.
	Image *ContentImage `json:"image,omitempty"`
}

// ContentImage is the unified image-content payload carried on
// ContentBlock.Image. Adapters convert between this and their
// provider's wire shape: OpenAI uses {image_url:{url, detail}};
// Anthropic uses {source:{type, media_type, data|url}}. Exactly
// one of URL or Data should be set on a given block — URL for
// url-referenced images, Data for inline base64 (with MediaType).
type ContentImage struct {
	URL       string `json:"url,omitempty"`
	Data      string `json:"data,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Message struct {
	Role          string         `json:"role"`
	Content       string         `json:"content"`
	ContentBlocks []ContentBlock `json:"content_blocks,omitempty"` // set when rich block content is needed (e.g. cache_control)
	Name          string         `json:"name,omitempty"`
	ToolCallID    string         `json:"tool_call_id,omitempty"`
	ToolCalls     []ToolCall     `json:"tool_calls,omitempty"`
	// ToolError marks a tool-role message as a failed tool call so
	// providers that distinguish error results (Anthropic's
	// is_error on tool_result blocks) can pass that signal upstream.
	// The model uses it to decide whether to retry, fall back, or
	// report failure; without it, the model has to guess from the
	// content text.
	ToolError bool `json:"tool_error,omitempty"`
}

type ChatResponse struct {
	ID        string
	Model     string
	CreatedAt time.Time
	Choices   []ChatChoice
	Usage     Usage
	Cost      CostBreakdown
	Route     RouteDecision
}

type ChatChoice struct {
	Index        int
	Message      Message
	FinishReason string
}

type Usage struct {
	PromptTokens       int
	CompletionTokens   int
	TotalTokens        int
	CachedPromptTokens int
}

type CostBreakdown struct {
	Currency                  string
	InputMicrosUSD            int64
	OutputMicrosUSD           int64
	CachedInputMicrosUSD      int64
	TotalMicrosUSD            int64
	InputMicrosUSDPerMillion  int64
	OutputMicrosUSDPerMillion int64
}

type RouteDecision struct {
	Provider     string
	ProviderKind string
	Model        string
	Reason       string
}

type ModelInfo struct {
	ID              string
	Provider        string
	Kind            string
	OwnedBy         string
	Default         bool
	DiscoverySource string
	Capabilities    ModelCapabilities
}

// ModelCapabilities is the operator-facing capability snapshot Hecate uses
// to decide whether a model can back Hecate Agent sessions. The source tells
// callers where the currently effective value came from.
type ModelCapabilities struct {
	ToolCalling      string `json:"tool_calling,omitempty"`
	Streaming        bool   `json:"streaming,omitempty"`
	MaxContextTokens int    `json:"max_context_tokens,omitempty"`
	Source           string `json:"source,omitempty"`
}

type ProviderStatus struct {
	Name                string
	Kind                string
	BaseURL             string
	CredentialState     string
	CredentialReady     bool
	Healthy             bool
	Status              string
	RoutingReady        bool
	RoutingBlocked      string
	DefaultModel        string
	Models              []string
	DiscoverySource     string
	RefreshedAt         time.Time
	LastCheckedAt       time.Time
	LastError           string
	LastErrorClass      string
	OpenUntil           time.Time
	LastLatencyMS       int64
	ConsecutiveFailures int
	TotalSuccesses      int64
	TotalFailures       int64
	Timeouts            int64
	ServerErrors        int64
	RateLimits          int64
	Error               string
	ReadinessChecks     []ProviderReadinessCheck
}

type ProviderReadinessCheck struct {
	Name           string
	Status         string
	Reason         string
	Message        string
	OperatorAction string
}

type ProviderHealthHistoryEntry struct {
	Provider            string
	ProviderKind        string
	Model               string
	Event               string
	Status              string
	Available           bool
	Error               string
	ErrorClass          string
	Reason              string
	RouteReason         string
	RequestID           string
	TraceID             string
	PeerProvider        string
	PeerModel           string
	PeerRouteReason     string
	HealthStatus        string
	PeerHealthStatus    string
	LatencyMS           int64
	ConsecutiveFailures int
	TotalSuccesses      int64
	TotalFailures       int64
	Timeouts            int64
	ServerErrors        int64
	RateLimits          int64
	AttemptCount        int
	EstimatedMicrosUSD  int64
	OpenUntil           time.Time
	Timestamp           time.Time
}

type BudgetStatus struct {
	Key                string
	Scope              string
	Provider           string
	Backend            string
	BalanceSource      string
	DebitedMicrosUSD   int64
	CreditedMicrosUSD  int64
	BalanceMicrosUSD   int64
	AvailableMicrosUSD int64
	Enforced           bool
	Warnings           []BudgetWarning
	History            []BudgetHistoryEntry
}

type BudgetWarning struct {
	ThresholdPercent   int
	ThresholdMicrosUSD int64
	BalanceMicrosUSD   int64
	AvailableMicrosUSD int64
	Triggered          bool
}

type BudgetHistoryEntry struct {
	Type              string
	Scope             string
	Provider          string
	Model             string
	RequestID         string
	Actor             string
	Detail            string
	AmountMicrosUSD   int64
	BalanceMicrosUSD  int64
	CreditedMicrosUSD int64
	DebitedMicrosUSD  int64
	PromptTokens      int
	CompletionTokens  int
	TotalTokens       int
	Timestamp         time.Time
}

// ChatSession is a stored conversation. The conversation itself lives
// in Messages — a flat, append-only sequence ordered by Sequence.
// ProviderCalls is parallel observability: one record per upstream
// chat-completion request, with its routing decision and cost. A
// message produced by the assistant or a tool result message points
// back at the call that produced it via Message.ProducedByCallID;
// user and system messages have an empty ProducedByCallID.
//
// This separation lets the same conversation span multiple providers
// and models (each switch records a new ProviderCall) and lets a
// single user prompt fan out into many calls when the agent runtime
// drives a server-side tool loop. Replay = walk Messages in Sequence
// order. Cost / routing analytics = walk ProviderCalls.
type ChatSession struct {
	ID    string
	Title string
	// SystemPrompt is prepended as a system-role message to chat
	// completions made against this session, unless the incoming request
	// already starts with a system message. Empty means no per-session
	// system prompt — clients fall back to whatever they send inline.
	SystemPrompt  string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Messages      []ChatSessionMessage
	ProviderCalls []ChatProviderCall
}

// ChatSessionMessage is one persisted entry in a chat session's
// conversation. The Message field carries the canonical content
// (role, content, content_blocks, tool_calls, tool_call_id, tool_error);
// the surrounding fields are storage metadata.
//
// Sequence is monotonic per session and is the authoritative ordering
// (CreatedAt is informational — sub-second ties are possible).
//
// ProducedByCallID points at the ChatProviderCall.ID that emitted this
// message. Empty for messages the operator/client supplied directly
// (user, system) and for tool results inserted by the client between
// calls. Set for assistant messages (the model produced them) and for
// tool messages the runtime emitted as part of a server-driven loop.
type ChatSessionMessage struct {
	ID               string
	Sequence         int
	ProducedByCallID string
	Message          Message
	CreatedAt        time.Time
}

// ChatProviderCall captures one upstream chat-completion request: the
// routing decision, the model/provider that ran, token usage, and the
// resolved cost. It is parallel to the message stream — multiple calls
// can share a session, and a single call can produce multiple messages
// (an assistant tool_call message + the tool results it triggered, when
// the runtime drives the loop). RequestID lets operators correlate
// against gateway request logs and OTel spans.
type ChatProviderCall struct {
	ID                string
	RequestID         string
	RequestedProvider string
	Provider          string
	ProviderKind      string
	RequestedModel    string
	Model             string
	CostMicrosUSD     int64
	PromptTokens      int
	CompletionTokens  int
	TotalTokens       int
	CreatedAt         time.Time
}

type AccountModelEstimate struct {
	Provider                        string
	ProviderKind                    string
	Model                           string
	Default                         bool
	DiscoverySource                 string
	Priced                          bool
	InputMicrosUSDPerMillionTokens  int64
	OutputMicrosUSDPerMillionTokens int64
	EstimatedRemainingPromptTokens  int64
	EstimatedRemainingOutputTokens  int64
}

type RouteDecisionReport struct {
	FinalProvider     string
	FinalProviderKind string
	FinalModel        string
	FinalReason       string
	FallbackFrom      string
	Candidates        []RouteCandidateReport
	Failovers         []RouteFailoverReport
}

type RouteCandidateReport struct {
	Provider           string
	ProviderKind       string
	Model              string
	Reason             string
	Outcome            string
	SkipReason         string
	HealthStatus       string
	PolicyRuleID       string
	PolicyAction       string
	PolicyReason       string
	EstimatedMicrosUSD int64
	Attempt            int
	RetryCount         int
	Retryable          bool
	Index              int
	LatencyMS          int64
	FailoverFrom       string
	FailoverTo         string
	Detail             string
	Timestamp          time.Time
}

type RouteFailoverReport struct {
	FromProvider string
	FromModel    string
	ToProvider   string
	ToModel      string
	Reason       string
	Timestamp    time.Time
}

type TraceEvent struct {
	Name       string
	Timestamp  time.Time
	Attributes map[string]any
}

type TraceSpan struct {
	TraceID       string
	SpanID        string
	ParentSpanID  string
	Name          string
	Kind          string
	StartTime     time.Time
	EndTime       time.Time
	Attributes    map[string]any
	Events        []TraceEvent
	StatusCode    string
	StatusMessage string
}
