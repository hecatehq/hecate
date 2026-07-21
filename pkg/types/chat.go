package types

import (
	"encoding/json"
	"time"
)

type ChatRequest struct {
	RequestID string
	Model     string
	Messages  []Message
	// Requirements are Hecate-internal route constraints. Provider-compatible
	// /v1 requests leave this zero-valued so Hecate preserves ordinary upstream
	// passthrough semantics even when provider discovery cannot prove a model
	// capability. Hecate-owned runtimes set only the capabilities they require.
	Requirements  ChatRequestRequirements
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

// ChatRequestRequirements describes capabilities every route candidate must
// explicitly support, plus request-scoped routing boundaries. It is
// intentionally separate from message-content inspection: public
// compatibility requests may contain provider-native rich content without
// opting into Hecate's stricter runtime admission policy.
type ChatRequestRequirements struct {
	ImageInput bool
	// ToolCalling requires each route candidate to declare tool-call support.
	// Rich-input agent turns set this alongside ImageInput so Auto routing cannot
	// combine image support from one provider with tool support from another.
	// A manually verified tools-on task also sets it with a private exact-route
	// fence. Ordinary tool turns may otherwise leave it false to retain their
	// established optimistic behavior for providers with unknown capability
	// discovery.
	ToolCalling bool
	// ToolCallingVerified carries a Hecate-owned, generation/model/expiry-bound
	// manual proof for an otherwise-unknown tool capability. It is accepted by
	// routing only with an exact, no-failover provider fence; it is never
	// accepted from or emitted to HTTP clients.
	ToolCallingVerified bool `json:"-"`
	// ToolCallingVerifiedModel and ToolCallingVerifiedUntil make the internal
	// verification proof reject a governor model rewrite and expire while a
	// queued or long-running Hecate task is still alive.
	ToolCallingVerifiedModel string    `json:"-"`
	ToolCallingVerifiedUntil time.Time `json:"-"`
	// NoProviderFailover keeps retries on the selected provider but prevents a
	// request from crossing into another provider. It also requires the gateway
	// to revalidate the selected provider instance immediately before every
	// dispatch, so a same-name configuration replacement cannot silently retarget
	// bound content. Hecate-owned and compatibility image turns set this because
	// image bytes must not be disclosed to another upstream implicitly.
	NoProviderFailover bool
	// ExactProvider requires ProviderHint to match the configured runtime name
	// exactly. Hecate-owned turns set this after hydrating provider-bound image
	// history so a concurrent registry reload cannot reinterpret the canonical
	// name as another provider's normalized name or alias.
	ExactProvider bool
	// ProviderInstance pins provider-bound dispatch to the opaque provider
	// instance that admission inspected. It is never accepted from or returned
	// to public API clients. Auto routing may leave this empty until the router
	// selects an instance; the resulting RouteDecision still carries the fence.
	ProviderInstance ProviderInstanceIdentity `json:"-"`
}

// ProviderInstanceIdentity is an opaque, internal execution fence. The ID is
// either a stable fingerprint of a managed provider's effective configuration
// generation or a process-scoped token assigned to an otherwise unknown
// provider object. It contains no raw endpoint, credential, or operator-visible
// configuration and must never be exposed through API responses or telemetry.
type ProviderInstanceIdentity struct {
	ID   string                       `json:"id,omitempty"`
	Kind ProviderInstanceIdentityKind `json:"kind,omitempty"`
}

type ProviderInstanceIdentityKind string

const (
	ProviderInstanceIdentityConfiguration ProviderInstanceIdentityKind = "configuration"
	ProviderInstanceIdentityRuntime       ProviderInstanceIdentityKind = "runtime"
)

func (identity ProviderInstanceIdentity) Valid() bool {
	if identity.ID == "" {
		return false
	}
	return identity.Kind == ProviderInstanceIdentityConfiguration || identity.Kind == ProviderInstanceIdentityRuntime
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
	// Width and Height are internal estimation hints. Provider adapters do not
	// serialize them; Hecate populates them from validated local attachments so
	// policy and cost preflight can account for visual tokens without decoding
	// the base64 payload again.
	Width  int `json:"-"`
	Height int `json:"-"`
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
	Provider         string
	ProviderKind     string
	ProviderInstance ProviderInstanceIdentity `json:"-"`
	Model            string
	Reason           string
}

type ModelInfo struct {
	ID       string
	Provider string
	// ProviderAliases are internal stable route keys for the configured
	// provider, such as a control-plane id or preset id.
	ProviderAliases []string `json:"-"`
	// ProviderFamily is internal capability-resolution metadata. Provider is
	// still the configured routing identity exposed to operators and clients.
	ProviderFamily string `json:"-"`
	// ProviderInstance is an internal execution fence and is deliberately absent
	// from model-list API responses.
	ProviderInstance ProviderInstanceIdentity `json:"-"`
	Kind             string
	OwnedBy          string
	Default          bool
	DiscoverySource  string
	Capabilities     ModelCapabilities
	Readiness        ModelReadiness
}

// ModelCapabilities is the operator-facing capability snapshot Hecate uses
// to decide whether a model can back Hecate Agent sessions. The source tells
// callers where the currently effective value came from.
type ModelCapabilities struct {
	ToolCalling string `json:"tool_calling,omitempty"`
	// ImageInput is a tri-state capability (unknown, none, supported).
	// A string keeps unknown distinct from an explicit provider-reported false,
	// which lets multimodal routing fail closed instead of guessing.
	ImageInput string `json:"image_input,omitempty"`
	Streaming  bool   `json:"streaming"`
	// StreamingKnown is internal merge metadata: provider discovery can
	// intentionally override catalog defaults to streaming=false without
	// exposing another API field.
	StreamingKnown   bool `json:"-"`
	MaxContextTokens int  `json:"max_context_tokens,omitempty"`
	// Source is catalog, provider, mixed, or unknown. Mixed means the effective
	// snapshot combines dimensions from provider-native metadata and Hecate's
	// catalog inference; it must not be presented as wholly provider-reported.
	Source string `json:"source,omitempty"`
	// ToolVerification is an operator-triggered, provider-generation-bound
	// observation for an otherwise unknown tool-calling capability. It never
	// contains request content, tool arguments, provider credentials, or an
	// endpoint. Known provider/catalog capabilities remain authoritative.
	ToolVerification *ToolCapabilityVerification `json:"tool_verification,omitempty"`
	// ToolCallingVerificationApplied is internal merge metadata. It identifies
	// a `basic`/`none` value projected from one route-bound verification so an
	// Auto aggregate can safely restore the otherwise-unknown value.
	ToolCallingVerificationApplied bool `json:"-"`
}

// ToolCapabilityVerification is the safe, operator-facing summary of one
// manual tool-calling capability probe. The internal provider-instance fence
// used to bind this record is deliberately omitted from the API contract.
type ToolCapabilityVerification struct {
	Status    string    `json:"status,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Reason    string    `json:"reason,omitempty"`
}

// ReadinessSummary is the compact operator-facing answer to "can Hecate use
// this thing right now?" Detailed checks remain available for drill-down.
type ReadinessSummary struct {
	Status         string
	Reason         string
	Message        string
	OperatorAction string
}

// ModelReadiness explains whether a discovered provider/model pair can be used
// for routing. It mirrors the gateway readiness contract without exposing the
// gateway package to API/UI projection code.
type ModelReadiness struct {
	Provider              string
	MatchedProvider       string
	Model                 string
	Ready                 bool
	Status                string
	Reason                string
	Message               string
	OperatorAction        string
	RoutingReady          bool
	ProviderStatus        string
	ProviderBlockedReason string
	SuggestedModels       []string
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
	Readiness           ReadinessSummary
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

type UsageSummary struct {
	Key           string
	Scope         string
	Provider      string
	Backend       string
	UsedMicrosUSD int64
}

type UsageEventEntry struct {
	Type             string
	Scope            string
	Provider         string
	Model            string
	RequestID        string
	Actor            string
	Detail           string
	AmountMicrosUSD  int64
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Timestamp        time.Time
}

type UsageModelEstimate struct {
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
