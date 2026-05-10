package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hecate/agent-runtime/pkg/types"
)

// OpenAIMessageContent is the polymorphic OpenAI message-content
// value. The wire shape is one of:
//   - JSON string ("Hello")
//   - JSON array of blocks ([{type:"text",text:"..."},{type:"image_url",image_url:{url:"..."}}])
//   - JSON null (assistant message paired with tool_calls)
//
// We unmarshal both shapes into this struct and re-marshal to the
// more specific form: blocks → array, otherwise string. Null is
// preserved (used for assistant messages with tool_calls — OpenAI's
// API requires a literal null there, not an empty string).
type OpenAIMessageContent struct {
	Text   string
	Blocks []OpenAIContentBlock
	// Null records whether the wire value was an explicit null.
	// Distinguished from an empty Text so the response renderer
	// emits null (not "") on assistant + tool_calls turns.
	Null bool
}

func (c *OpenAIMessageContent) UnmarshalJSON(data []byte) error {
	*c = OpenAIMessageContent{}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		c.Null = true
		return nil
	}
	switch trimmed[0] {
	case '"':
		return json.Unmarshal(data, &c.Text)
	case '[':
		return json.Unmarshal(data, &c.Blocks)
	}
	return fmt.Errorf("content must be string, array, or null")
}

func (c OpenAIMessageContent) MarshalJSON() ([]byte, error) {
	if c.Null {
		return []byte("null"), nil
	}
	if len(c.Blocks) > 0 {
		return json.Marshal(c.Blocks)
	}
	return json.Marshal(c.Text)
}

// AsString flattens content into a single text string. Block-form
// content concatenates text-typed blocks with double newlines;
// non-text blocks (images) are skipped — callers that need the
// structured form should walk Blocks directly.
func (c OpenAIMessageContent) AsString() string {
	if c.Text != "" {
		return c.Text
	}
	parts := make([]string, 0, len(c.Blocks))
	for _, b := range c.Blocks {
		if (b.Type == "text" || b.Type == "") && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// OpenAIContentBlock is one element of the array form of message
// content. OpenAI today defines two block types in this position:
//   - {type:"text", text:"..."}
//   - {type:"image_url", image_url:{url:"...", detail:"low|high|auto"}}
//
// Audio / file / video blocks land here too as the API grows; the
// struct accepts unknown variants by leaving non-recognized fields
// untouched (the JSON layer drops them but the Type is preserved
// so the inbound parser can still warn-and-skip cleanly).
type OpenAIContentBlock struct {
	Type     string                 `json:"type"`
	Text     string                 `json:"text,omitempty"`
	ImageURL *OpenAIContentImageURL `json:"image_url,omitempty"`
}

// OpenAIContentImageURL mirrors OpenAI's image_url object. URL is
// either a public https:// URL or a `data:image/...;base64,...`
// data URI. Detail is a sampling hint ("low" | "high" | "auto");
// upstream defaults to "auto" when absent.
type OpenAIContentImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type OpenAIChatCompletionRequest struct {
	Model        string              `json:"model"`
	Provider     string              `json:"provider,omitempty"`
	SessionID    string              `json:"session_id,omitempty"`
	SessionTitle string              `json:"session_title,omitempty"`
	Messages     []OpenAIChatMessage `json:"messages"`
	MaxTokens    int                 `json:"max_tokens,omitempty"`
	Temperature  float64             `json:"temperature,omitempty"`
	User         string              `json:"user,omitempty"`
	Tools        []OpenAITool        `json:"tools,omitempty"`
	ToolChoice   json.RawMessage     `json:"tool_choice,omitempty"`
	Stream       bool                `json:"stream,omitempty"`
	// ResponseFormat carries the OpenAI structured-output knob:
	// {"type":"text"|"json_object"|"json_schema",...}. Passed
	// through verbatim to OpenAI-compat upstreams; Anthropic
	// upstreams log-and-drop it (no direct equivalent).
	ResponseFormat json.RawMessage `json:"response_format,omitempty"`
	// Tier-2 OpenAI passthroughs (mirrors types.ChatRequest).
	Seed              *int            `json:"seed,omitempty"`
	PresencePenalty   float64         `json:"presence_penalty,omitempty"`
	FrequencyPenalty  float64         `json:"frequency_penalty,omitempty"`
	Logprobs          bool            `json:"logprobs,omitempty"`
	TopLogprobs       int             `json:"top_logprobs,omitempty"`
	LogitBias         json.RawMessage `json:"logit_bias,omitempty"`
	StreamOptions     json.RawMessage `json:"stream_options,omitempty"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls,omitempty"`
}

type OpenAITool struct {
	Type     string             `json:"type"`
	Function OpenAIToolFunction `json:"function"`
}

type OpenAIToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

type OpenAIToolCall struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function OpenAIToolCallFunction `json:"function"`
}

type OpenAIToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type OpenAIChatMessage struct {
	Role string `json:"role"`
	// Content accepts string, array of blocks, or null. See
	// OpenAIMessageContent for the unmarshal contract.
	Content    OpenAIMessageContent `json:"content"`
	Name       string               `json:"name,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
	ToolCalls  []OpenAIToolCall     `json:"tool_calls,omitempty"`
	// ContentBlocks carries provider-native content (Anthropic
	// thinking / redacted_thinking / tool_use blocks, image blocks
	// with cache_control hints) so cross-provider replay preserves
	// fidelity. The OpenAI public spec doesn't define this field on
	// the request side; we use it as a Hecate-specific extension.
	// Unknown clients (real OpenAI SDK against the Hecate proxy)
	// continue to work — they don't emit it. Hecate-aware clients
	// (the operator UI replaying stored history) round-trip it
	// through.
	ContentBlocks []OpenAIPersistedContentBlock `json:"content_blocks,omitempty"`
	// ToolError flags a tool-role message as the result of a failed
	// tool call so the Anthropic adapter can set is_error on the
	// downstream tool_result block. Without it, the model has to
	// guess from the content text.
	ToolError bool `json:"tool_error,omitempty"`
}

// OpenAIPersistedContentBlock mirrors types.ContentBlock on the
// inbound/outbound wire. Used only by Hecate's session-fetch and
// history-replay paths — the public chat-completion spec stays
// OpenAI-shaped via the Content/Blocks polymorphic field. Fields are
// the union of OpenAI image-block shape and Anthropic content-block
// shape; the gateway translates between this and the canonical
// types.ContentBlock.
type OpenAIPersistedContentBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	ID           string                 `json:"id,omitempty"`            // tool_use
	Name         string                 `json:"name,omitempty"`          // tool_use
	Input        json.RawMessage        `json:"input,omitempty"`         // tool_use
	ToolUseID    string                 `json:"tool_use_id,omitempty"`   // tool_result
	CacheControl json.RawMessage        `json:"cache_control,omitempty"` // Anthropic prompt caching
	Thinking     string                 `json:"thinking,omitempty"`      // extended thinking
	Signature    string                 `json:"signature,omitempty"`
	Data         string                 `json:"data,omitempty"` // redacted_thinking
	ImageURL     *OpenAIContentImageURL `json:"image_url,omitempty"`
}

type OpenAIChatCompletionResponse struct {
	ID      string                       `json:"id"`
	Object  string                       `json:"object"`
	Created int64                        `json:"created"`
	Model   string                       `json:"model"`
	Choices []OpenAIChatCompletionChoice `json:"choices"`
	Usage   OpenAIUsage                  `json:"usage"`
}

type OpenAIChatCompletionChoice struct {
	Index        int               `json:"index"`
	Message      OpenAIChatMessage `json:"message"`
	FinishReason string            `json:"finish_reason"`
}

type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// PromptTokensDetails surfaces the breakdown of prompt-side
	// tokens, mirroring OpenAI's own response shape. We currently
	// populate `cached_tokens` from internal Usage.CachedPromptTokens
	// (Anthropic upstreams set this; OpenAI upstreams report it
	// natively). Pointer so callers that don't care don't see the
	// nested object at all — keeps backwards compat for clients that
	// were sniffing for `usage.prompt_tokens_details === undefined`.
	PromptTokensDetails *OpenAIPromptTokensDetails `json:"prompt_tokens_details,omitempty"`
}

// OpenAIPromptTokensDetails matches the shape OpenAI returns. Only
// `cached_tokens` is populated today; `audio_tokens` would be added
// alongside multi-modal support.
type OpenAIPromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

type OpenAIModelsResponse struct {
	Object string            `json:"object"`
	Data   []OpenAIModelData `json:"data"`
}

type ModelCapabilityUpsertRequest struct {
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	ToolCalling      string `json:"tool_calling,omitempty"`
	Streaming        *bool  `json:"streaming,omitempty"`
	MaxContextTokens int    `json:"max_context_tokens,omitempty"`
	Note             string `json:"note,omitempty"`
}

type ModelCapabilityResponse struct {
	Object string              `json:"object"`
	Data   ModelCapabilityItem `json:"data"`
}

type ModelCapabilityItem struct {
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	ToolCalling      string `json:"tool_calling"`
	Streaming        bool   `json:"streaming"`
	MaxContextTokens int    `json:"max_context_tokens,omitempty"`
	Source           string `json:"source"`
	Note             string `json:"note,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

type SessionResponse struct {
	Object string              `json:"object"`
	Data   SessionResponseItem `json:"data"`
}

type ChatSessionsResponse struct {
	Object  string                   `json:"object"`
	Data    []ChatSessionSummaryItem `json:"data"`
	HasMore bool                     `json:"has_more"`
}

type ChatSessionResponse struct {
	Object string          `json:"object"`
	Data   ChatSessionItem `json:"data"`
}

type CreateChatSessionRequest struct {
	Title string `json:"title"`
}

// UpdateChatSessionRequest patches an existing chat session. Both fields
// are pointers so a request can leave a field unchanged by omitting it
// entirely (`null` and "absent" are treated the same — leave alone). To
// clear a field, send the empty string explicitly.
type UpdateChatSessionRequest struct {
	Title        *string `json:"title,omitempty"`
	SystemPrompt *string `json:"system_prompt,omitempty"`
}

// SessionResponseItem reports who is calling. In single-user mode this
// always describes the anonymous local operator — auth was removed and
// the gateway treats every caller as fully privileged.
type SessionResponseItem struct {
	Role string `json:"role"`
}

type ChatSessionSummaryItem struct {
	ID                string `json:"id"`
	Title             string `json:"title"`
	MessageCount      int    `json:"message_count"`
	ProviderCallCount int    `json:"provider_call_count"`
	CreatedAt         string `json:"created_at,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
	LastModel         string `json:"last_model,omitempty"`
	LastProvider      string `json:"last_provider,omitempty"`
	LastCostUSD       string `json:"last_cost_usd,omitempty"`
	LastRequestID     string `json:"last_request_id,omitempty"`
}

// ChatSessionItem is the full session payload returned by the
// session-fetch endpoint. Messages and ProviderCalls are flat,
// independently-iterable arrays — the relationship between them is
// expressed through ChatSessionMessageItem.ProducedByCallID, which
// references ChatProviderCallItem.ID. The UI builds whatever
// projection it wants (exchange-grouped view, raw transcript, etc.).
type ChatSessionItem struct {
	ID            string                   `json:"id"`
	Title         string                   `json:"title"`
	SystemPrompt  string                   `json:"system_prompt,omitempty"`
	CreatedAt     string                   `json:"created_at,omitempty"`
	UpdatedAt     string                   `json:"updated_at,omitempty"`
	Messages      []ChatSessionMessageItem `json:"messages"`
	ProviderCalls []ChatProviderCallItem   `json:"provider_calls"`
}

// ChatSessionMessageItem is one row from the session's flat message
// stream. Sequence is monotonic per session and is the authoritative
// ordering. ProducedByCallID, when set, points at the
// ChatProviderCallItem.ID that emitted this message (assistant or
// runtime-emitted tool messages); empty for client-supplied messages
// (user, system, client-injected tool results). The OpenAIChatMessage
// embed flattens role / content / tool_calls / content_blocks /
// tool_error onto the same JSON object.
type ChatSessionMessageItem struct {
	ID               string `json:"id"`
	Sequence         int    `json:"sequence"`
	ProducedByCallID string `json:"produced_by_call_id,omitempty"`
	CreatedAt        string `json:"created_at,omitempty"`
	OpenAIChatMessage
}

// ChatProviderCallItem is one row from the session's provider-call
// observability stream. Each row corresponds to one upstream
// chat-completion request (its routing decision, model, tokens, cost).
type ChatProviderCallItem struct {
	ID                string `json:"id"`
	RequestID         string `json:"request_id"`
	RequestedProvider string `json:"requested_provider,omitempty"`
	Provider          string `json:"provider"`
	ProviderKind      string `json:"provider_kind,omitempty"`
	RequestedModel    string `json:"requested_model,omitempty"`
	Model             string `json:"model"`
	CostMicrosUSD     int64  `json:"cost_micros_usd"`
	CostUSD           string `json:"cost_usd"`
	PromptTokens      int    `json:"prompt_tokens"`
	CompletionTokens  int    `json:"completion_tokens"`
	TotalTokens       int    `json:"total_tokens"`
	CreatedAt         string `json:"created_at,omitempty"`
}

type OpenAIModelData struct {
	ID       string         `json:"id"`
	Object   string         `json:"object"`
	OwnedBy  string         `json:"owned_by"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type ProviderStatusResponse struct {
	Object string                       `json:"object"`
	Data   []ProviderStatusResponseItem `json:"data"`
}

type ProviderHealthHistoryResponse struct {
	Object string                              `json:"object"`
	Data   []ProviderHealthHistoryResponseItem `json:"data"`
}

type ProviderPresetResponse struct {
	Object string                       `json:"object"`
	Data   []ProviderPresetResponseItem `json:"data"`
}

type AgentAdapterResponse struct {
	Object string                     `json:"object"`
	Data   []AgentAdapterResponseItem `json:"data"`
}

type AgentChatSessionsResponse struct {
	Object string                        `json:"object"`
	Data   []AgentChatSessionSummaryItem `json:"data"`
}

type AgentChatSessionResponse struct {
	Object string               `json:"object"`
	Data   AgentChatSessionItem `json:"data"`
}

type WorkspaceDialogResponse struct {
	Object string                      `json:"object"`
	Data   WorkspaceDialogResponseItem `json:"data"`
}

type TraceListResponse struct {
	Object string          `json:"object"`
	Data   []TraceListItem `json:"data"`
}

type TraceListItem struct {
	RequestID     string                 `json:"request_id"`
	TraceID       string                 `json:"trace_id,omitempty"`
	StartedAt     string                 `json:"started_at,omitempty"`
	SpanCount     int                    `json:"span_count"`
	DurationMS    int64                  `json:"duration_ms,omitempty"`
	StatusCode    string                 `json:"status_code,omitempty"`
	StatusMessage string                 `json:"status_message,omitempty"`
	Route         TraceRouteReportRecord `json:"route,omitempty"`
}

type TraceResponse struct {
	Object string            `json:"object"`
	Data   TraceResponseItem `json:"data"`
}

type TraceResponseItem struct {
	RequestID string                 `json:"request_id"`
	TraceID   string                 `json:"trace_id,omitempty"`
	StartedAt string                 `json:"started_at,omitempty"`
	Spans     []TraceSpanRecord      `json:"spans,omitempty"`
	Route     TraceRouteReportRecord `json:"route,omitempty"`
}

type TraceRouteReportRecord struct {
	FinalProvider     string                      `json:"final_provider,omitempty"`
	FinalProviderKind string                      `json:"final_provider_kind,omitempty"`
	FinalModel        string                      `json:"final_model,omitempty"`
	FinalReason       string                      `json:"final_reason,omitempty"`
	FallbackFrom      string                      `json:"fallback_from,omitempty"`
	Candidates        []TraceRouteCandidateRecord `json:"candidates,omitempty"`
	Failovers         []TraceRouteFailoverRecord  `json:"failovers,omitempty"`
}

type TraceRouteCandidateRecord struct {
	Provider           string `json:"provider,omitempty"`
	ProviderKind       string `json:"provider_kind,omitempty"`
	Model              string `json:"model,omitempty"`
	Reason             string `json:"reason,omitempty"`
	Outcome            string `json:"outcome,omitempty"`
	SkipReason         string `json:"skip_reason,omitempty"`
	HealthStatus       string `json:"health_status,omitempty"`
	PolicyRuleID       string `json:"policy_rule_id,omitempty"`
	PolicyAction       string `json:"policy_action,omitempty"`
	PolicyReason       string `json:"policy_reason,omitempty"`
	EstimatedMicrosUSD int64  `json:"estimated_micros_usd,omitempty"`
	EstimatedUSD       string `json:"estimated_usd,omitempty"`
	Attempt            int    `json:"attempt,omitempty"`
	RetryCount         int    `json:"retry_count,omitempty"`
	Retryable          bool   `json:"retryable,omitempty"`
	Index              int    `json:"index,omitempty"`
	LatencyMS          int64  `json:"latency_ms,omitempty"`
	FailoverFrom       string `json:"failover_from,omitempty"`
	FailoverTo         string `json:"failover_to,omitempty"`
	Detail             string `json:"detail,omitempty"`
	Timestamp          string `json:"timestamp,omitempty"`
}

type TraceRouteFailoverRecord struct {
	FromProvider string `json:"from_provider,omitempty"`
	FromModel    string `json:"from_model,omitempty"`
	ToProvider   string `json:"to_provider,omitempty"`
	ToModel      string `json:"to_model,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Timestamp    string `json:"timestamp,omitempty"`
}

type TraceSpanRecord struct {
	TraceID       string             `json:"trace_id"`
	SpanID        string             `json:"span_id"`
	ParentSpanID  string             `json:"parent_span_id,omitempty"`
	Name          string             `json:"name"`
	Kind          string             `json:"kind,omitempty"`
	StartTime     string             `json:"start_time,omitempty"`
	EndTime       string             `json:"end_time,omitempty"`
	Attributes    map[string]any     `json:"attributes,omitempty"`
	StatusCode    string             `json:"status_code,omitempty"`
	StatusMessage string             `json:"status_message,omitempty"`
	Events        []TraceEventRecord `json:"events,omitempty"`
}

type TraceEventRecord struct {
	Name       string         `json:"name"`
	Timestamp  string         `json:"timestamp"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type ProviderStatusResponseItem struct {
	Name                string                               `json:"name"`
	Kind                string                               `json:"kind"`
	BaseURL             string                               `json:"base_url,omitempty"`
	CredentialState     string                               `json:"credential_state,omitempty"`
	CredentialReady     bool                                 `json:"credential_ready"`
	Healthy             bool                                 `json:"healthy"`
	Status              string                               `json:"status"`
	RoutingReady        bool                                 `json:"routing_ready"`
	RoutingBlocked      string                               `json:"routing_blocked_reason,omitempty"`
	DefaultModel        string                               `json:"default_model,omitempty"`
	Models              []string                             `json:"models,omitempty"`
	ModelCount          int                                  `json:"model_count"`
	DiscoverySource     string                               `json:"discovery_source,omitempty"`
	RefreshedAt         string                               `json:"refreshed_at,omitempty"`
	LastCheckedAt       string                               `json:"last_checked_at,omitempty"`
	LastError           string                               `json:"last_error,omitempty"`
	LastErrorClass      string                               `json:"last_error_class,omitempty"`
	OpenUntil           string                               `json:"open_until,omitempty"`
	LastLatencyMS       int64                                `json:"last_latency_ms,omitempty"`
	ConsecutiveFailures int                                  `json:"consecutive_failures,omitempty"`
	TotalSuccesses      int64                                `json:"total_successes,omitempty"`
	TotalFailures       int64                                `json:"total_failures,omitempty"`
	Timeouts            int64                                `json:"timeouts,omitempty"`
	ServerErrors        int64                                `json:"server_errors,omitempty"`
	RateLimits          int64                                `json:"rate_limits,omitempty"`
	ReadinessChecks     []ProviderReadinessCheckResponseItem `json:"readiness_checks,omitempty"`
}

type ProviderReadinessCheckResponseItem struct {
	Name           string `json:"name"`
	Status         string `json:"status"`
	Reason         string `json:"reason,omitempty"`
	Message        string `json:"message,omitempty"`
	OperatorAction string `json:"operator_action,omitempty"`
}

type ProviderHealthHistoryResponseItem struct {
	Provider            string `json:"provider"`
	ProviderKind        string `json:"provider_kind,omitempty"`
	Model               string `json:"model,omitempty"`
	Event               string `json:"event"`
	Status              string `json:"status"`
	Available           bool   `json:"available"`
	Error               string `json:"error,omitempty"`
	ErrorClass          string `json:"error_class,omitempty"`
	Reason              string `json:"reason,omitempty"`
	RouteReason         string `json:"route_reason,omitempty"`
	RequestID           string `json:"request_id,omitempty"`
	TraceID             string `json:"trace_id,omitempty"`
	PeerProvider        string `json:"peer_provider,omitempty"`
	PeerModel           string `json:"peer_model,omitempty"`
	PeerRouteReason     string `json:"peer_route_reason,omitempty"`
	HealthStatus        string `json:"health_status,omitempty"`
	PeerHealthStatus    string `json:"peer_health_status,omitempty"`
	LatencyMS           int64  `json:"latency_ms,omitempty"`
	ConsecutiveFailures int    `json:"consecutive_failures,omitempty"`
	TotalSuccesses      int64  `json:"total_successes,omitempty"`
	TotalFailures       int64  `json:"total_failures,omitempty"`
	Timeouts            int64  `json:"timeouts,omitempty"`
	ServerErrors        int64  `json:"server_errors,omitempty"`
	RateLimits          int64  `json:"rate_limits,omitempty"`
	AttemptCount        int    `json:"attempt_count,omitempty"`
	EstimatedMicrosUSD  int64  `json:"estimated_micros_usd,omitempty"`
	OpenUntil           string `json:"open_until,omitempty"`
	Timestamp           string `json:"timestamp,omitempty"`
}

type ProviderPresetResponseItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Protocol     string `json:"protocol"`
	BaseURL      string `json:"base_url"`
	APIKeyEnv    string `json:"api_key_env,omitempty"`
	APIVersion   string `json:"api_version,omitempty"`
	DefaultModel string `json:"default_model,omitempty"`
	DocsURL      string `json:"docs_url,omitempty"`
	Description  string `json:"description,omitempty"`
	EnvSnippet   string `json:"env_snippet,omitempty"`
}

type LocalProviderDiscoveryResponse struct {
	Object string                               `json:"object"`
	Data   []LocalProviderDiscoveryResponseItem `json:"data"`
}

type LocalProviderDiscoveryResponseItem struct {
	PresetID         string   `json:"preset_id"`
	Name             string   `json:"name"`
	BaseURL          string   `json:"base_url"`
	ProbeURL         string   `json:"probe_url"`
	Status           string   `json:"status"`
	Command          string   `json:"command,omitempty"`
	CommandAvailable bool     `json:"command_available"`
	CommandPath      string   `json:"command_path,omitempty"`
	HTTPAvailable    bool     `json:"http_available"`
	ModelCount       int      `json:"model_count,omitempty"`
	Models           []string `json:"models,omitempty"`
	Error            string   `json:"error,omitempty"`
}

type AgentAdapterResponseItem struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Kind                 string   `json:"kind"`
	Command              string   `json:"command"`
	Args                 []string `json:"args,omitempty"`
	Managed              bool     `json:"managed,omitempty"`
	ManagedPackage       string   `json:"managed_package,omitempty"`
	Available            bool     `json:"available"`
	Status               string   `json:"status"`
	Path                 string   `json:"path,omitempty"`
	Error                string   `json:"error,omitempty"`
	Description          string   `json:"description,omitempty"`
	CostMode             string   `json:"cost_mode,omitempty"`
	DocsURL              string   `json:"docs_url,omitempty"`
	Version              string   `json:"version,omitempty"`
	SupportedRange       string   `json:"supported_range,omitempty"`
	VersionOutsideRange  bool     `json:"version_outside_range,omitempty"`
	AuthStatus           string   `json:"auth_status,omitempty"`
	AuthError            string   `json:"auth_error,omitempty"`
	CredentialConfigured bool     `json:"credential_configured,omitempty"`
	CredentialPreview    string   `json:"credential_preview,omitempty"`
}

type AgentAdapterCredentialSetRequest struct {
	Name  string `json:"name,omitempty"`
	Value string `json:"value"`
}

type AgentAdapterCredentialResponse struct {
	Object string                             `json:"object"`
	Data   AgentAdapterCredentialResponseItem `json:"data"`
}

type AgentAdapterCredentialResponseItem struct {
	AdapterID  string `json:"adapter_id"`
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	Preview    string `json:"preview,omitempty"`
}

type CreateAgentChatSessionRequest struct {
	Title       string `json:"title,omitempty"`
	RuntimeKind string `json:"runtime_kind,omitempty"`
	AdapterID   string `json:"adapter_id,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Model       string `json:"model,omitempty"`
	Workspace   string `json:"workspace"`
}

type CreateAgentChatMessageRequest struct {
	Content      string `json:"content"`
	RuntimeKind  string `json:"runtime_kind,omitempty"`
	Provider     string `json:"provider,omitempty"`
	Model        string `json:"model,omitempty"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	Workspace    string `json:"workspace,omitempty"`
}

type AgentChatSessionSummaryItem struct {
	ID              string                  `json:"id"`
	Title           string                  `json:"title"`
	RuntimeKind     string                  `json:"runtime_kind"`
	AdapterID       string                  `json:"adapter_id,omitempty"`
	DriverKind      string                  `json:"driver_kind,omitempty"`
	NativeSessionID string                  `json:"native_session_id,omitempty"`
	TaskID          string                  `json:"task_id,omitempty"`
	LatestRunID     string                  `json:"latest_run_id,omitempty"`
	Provider        string                  `json:"provider,omitempty"`
	Model           string                  `json:"model,omitempty"`
	Capabilities    types.ModelCapabilities `json:"capabilities,omitempty"`
	Workspace       string                  `json:"workspace"`
	WorkspaceBranch string                  `json:"workspace_branch,omitempty"`
	Status          string                  `json:"status"`
	MessageCount    int                     `json:"message_count"`
	CreatedAt       string                  `json:"created_at,omitempty"`
	UpdatedAt       string                  `json:"updated_at,omitempty"`
}

type AgentChatSessionItem struct {
	ID                   string                  `json:"id"`
	Title                string                  `json:"title"`
	RuntimeKind          string                  `json:"runtime_kind"`
	AdapterID            string                  `json:"adapter_id,omitempty"`
	DriverKind           string                  `json:"driver_kind,omitempty"`
	NativeSessionID      string                  `json:"native_session_id,omitempty"`
	TaskID               string                  `json:"task_id,omitempty"`
	LatestRunID          string                  `json:"latest_run_id,omitempty"`
	Provider             string                  `json:"provider,omitempty"`
	Model                string                  `json:"model,omitempty"`
	Capabilities         types.ModelCapabilities `json:"capabilities,omitempty"`
	Workspace            string                  `json:"workspace"`
	WorkspaceBranch      string                  `json:"workspace_branch,omitempty"`
	Status               string                  `json:"status"`
	TurnsUsed            int                     `json:"turns_used"`
	MaxTurnsPerSession   int                     `json:"max_turns_per_session,omitempty"`
	SessionStartedAt     string                  `json:"session_started_at,omitempty"`
	MaxSessionDurationMS int64                   `json:"max_session_duration_ms,omitempty"`
	IdleTimeoutMS        int64                   `json:"idle_timeout_ms,omitempty"`
	CreatedAt            string                  `json:"created_at,omitempty"`
	UpdatedAt            string                  `json:"updated_at,omitempty"`
	Segments             []AgentChatSegmentItem  `json:"segments,omitempty"`
	Messages             []AgentChatMessageItem  `json:"messages"`
}

type AgentChatSegmentItem struct {
	ID           string `json:"id"`
	RuntimeKind  string `json:"runtime_kind"`
	Provider     string `json:"provider,omitempty"`
	Model        string `json:"model,omitempty"`
	TaskID       string `json:"task_id,omitempty"`
	LatestRunID  string `json:"latest_run_id,omitempty"`
	Workspace    string `json:"workspace,omitempty"`
	Status       string `json:"status,omitempty"`
	MessageCount int    `json:"message_count"`
	StartedAt    string `json:"started_at,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

type AgentChatMessageItem struct {
	ID              string                  `json:"id"`
	RuntimeKind     string                  `json:"runtime_kind,omitempty"`
	SegmentID       string                  `json:"segment_id,omitempty"`
	TaskID          string                  `json:"task_id,omitempty"`
	RunID           string                  `json:"run_id,omitempty"`
	RequestID       string                  `json:"request_id,omitempty"`
	TraceID         string                  `json:"trace_id,omitempty"`
	SpanID          string                  `json:"span_id,omitempty"`
	Role            string                  `json:"role"`
	Content         string                  `json:"content"`
	RawOutput       string                  `json:"raw_output,omitempty"`
	AdapterID       string                  `json:"adapter_id,omitempty"`
	AdapterName     string                  `json:"adapter_name,omitempty"`
	DriverKind      string                  `json:"driver_kind,omitempty"`
	NativeSessionID string                  `json:"native_session_id,omitempty"`
	Status          string                  `json:"status,omitempty"`
	ExitCode        int                     `json:"exit_code,omitempty"`
	CostMode        string                  `json:"cost_mode,omitempty"`
	Provider        string                  `json:"provider,omitempty"`
	Model           string                  `json:"model,omitempty"`
	Capabilities    types.ModelCapabilities `json:"capabilities,omitempty"`
	Workspace       string                  `json:"workspace,omitempty"`
	DiffStat        string                  `json:"diff_stat,omitempty"`
	Diff            string                  `json:"diff,omitempty"`
	CreatedAt       string                  `json:"created_at,omitempty"`
	StartedAt       string                  `json:"started_at,omitempty"`
	CompletedAt     string                  `json:"completed_at,omitempty"`
	DurationMS      int64                   `json:"duration_ms,omitempty"`
	Error           string                  `json:"error,omitempty"`
	Activities      []AgentChatActivityItem `json:"activities,omitempty"`
	Usage           *AgentChatUsageItem     `json:"usage,omitempty"`
	Timing          *AgentChatTimingItem    `json:"timing,omitempty"`
}

type AgentChatChangedFileItem struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Status    string `json:"status"`
}

type AgentChatChangedFilesResponse struct {
	Object string                     `json:"object"`
	Data   []AgentChatChangedFileItem `json:"data"`
}

type AgentChatChangedFileDiffItem struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Status    string `json:"status"`
	Diff      string `json:"diff"`
}

type AgentChatChangedFileDiffResponse struct {
	Object string                       `json:"object"`
	Data   AgentChatChangedFileDiffItem `json:"data"`
}

type RevertAgentChatMessageFilesRequest struct {
	Paths []string `json:"paths,omitempty"`
}

type AgentChatRevertItem struct {
	Reverted bool                       `json:"reverted"`
	Paths    []string                   `json:"paths"`
	DiffStat string                     `json:"diff_stat,omitempty"`
	Files    []AgentChatChangedFileItem `json:"files"`
}

type AgentChatRevertResponse struct {
	Object string              `json:"object"`
	Data   AgentChatRevertItem `json:"data"`
}

type AgentChatActivityItem struct {
	ID                string `json:"id,omitempty"`
	Type              string `json:"type"`
	Status            string `json:"status,omitempty"`
	Kind              string `json:"kind,omitempty"`
	Title             string `json:"title"`
	Detail            string `json:"detail,omitempty"`
	CreatedAt         string `json:"created_at,omitempty"`
	ArtifactID        string `json:"artifact_id,omitempty"`
	ArtifactSizeBytes int64  `json:"artifact_size_bytes,omitempty"`
	ArtifactPreview   string `json:"artifact_preview,omitempty"`
	ApprovalID        string `json:"approval_id,omitempty"`
	NeedsAction       bool   `json:"needs_action,omitempty"`
}

type AgentChatUsageItem struct {
	ContextSize          int    `json:"context_size,omitempty"`
	ContextUsed          int    `json:"context_used,omitempty"`
	ReportedCostAmount   string `json:"reported_cost_amount,omitempty"`
	ReportedCostCurrency string `json:"reported_cost_currency,omitempty"`
}

type AgentChatTimingItem struct {
	TotalMS        int64  `json:"total_ms,omitempty"`
	QueueMS        int64  `json:"queue_ms,omitempty"`
	ModelMS        int64  `json:"model_ms,omitempty"`
	ToolMS         int64  `json:"tool_ms,omitempty"`
	ApprovalWaitMS int64  `json:"approval_wait_ms,omitempty"`
	OverheadMS     int64  `json:"overhead_ms,omitempty"`
	TurnCount      int    `json:"turn_count,omitempty"`
	ToolCount      int    `json:"tool_count,omitempty"`
	Bottleneck     string `json:"bottleneck,omitempty"`
	BottleneckMS   int64  `json:"bottleneck_ms,omitempty"`
}

type WorkspaceDialogResponseItem struct {
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
}

type BudgetStatusResponse struct {
	Object string                   `json:"object"`
	Data   BudgetStatusResponseItem `json:"data"`
}

type AccountSummaryResponse struct {
	Object string                     `json:"object"`
	Data   AccountSummaryResponseItem `json:"data"`
}

type RequestLedgerResponse struct {
	Object string                `json:"object"`
	Data   []BudgetHistoryRecord `json:"data"`
}

type RuntimeStatsResponse struct {
	Object string                   `json:"object"`
	Data   RuntimeStatsResponseItem `json:"data"`
}

type RuntimeStatsResponseItem struct {
	CheckedAt               string `json:"checked_at"`
	QueueDepth              int    `json:"queue_depth"`
	QueueCapacity           int    `json:"queue_capacity"`
	QueueBackend            string `json:"queue_backend,omitempty"`
	WorkerCount             int    `json:"worker_count"`
	InFlightJobs            int    `json:"in_flight_jobs"`
	QueuedRuns              int    `json:"queued_runs"`
	RunningRuns             int    `json:"running_runs"`
	AwaitingApprovalRuns    int    `json:"awaiting_approval_runs"`
	OldestQueuedAgeSeconds  int64  `json:"oldest_queued_age_seconds"`
	OldestRunningAgeSeconds int64  `json:"oldest_running_age_seconds"`
	StoreBackend            string `json:"store_backend,omitempty"`
	// AgentAdapterApprovalMode reports the configured mode for the
	// External Agent adapter approval coordinator: "auto", "prompt",
	// or "deny". Operators surface a danger banner in the UI when this
	// is "auto" since every adapter call is permitted without review.
	// Empty when the gateway was built without an approval coordinator
	// (test fixtures, legacy configs).
	AgentAdapterApprovalMode string `json:"agent_adapter_approval_mode,omitempty"`
}

// MCPProbeRequest is the wire shape for POST /hecate/v1/mcp/probe — a
// dry-run that brings an MCP server up exactly the way an
// agent_loop run would (same secret resolution, same uncached
// spawn path), calls tools/list, and tears it down. Lets operators
// discover what tools a config vends without creating a task and
// reading the conversation. Body shape mirrors a single
// MCPServerConfigItem entry from the task-create payload (minus
// approval_policy, which is a runtime gating decision that doesn't
// affect what the server vends).
type MCPProbeRequest struct {
	Name    string            `json:"name,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// MCPProbeResponse carries the upstream's tools/list result. Each
// tool keeps its un-namespaced name (the operator probably wants to
// see what the server itself calls them — namespacing happens at
// task-spawn time based on the operator-chosen alias).
type MCPProbeResponse struct {
	Object string               `json:"object"`
	Data   MCPProbeResponseItem `json:"data"`
}

type MCPProbeResponseItem struct {
	// Server identity reported by the upstream during initialize.
	// Useful for confirming the operator pointed at the right thing
	// before they wire it into a task.
	ServerName    string                   `json:"server_name,omitempty"`
	ServerVersion string                   `json:"server_version,omitempty"`
	Tools         []MCPProbeToolDescriptor `json:"tools"`
}

type MCPProbeToolDescriptor struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// InputSchema is the upstream-declared JSON Schema for the tool's
	// arguments, returned verbatim so operators can paste it into
	// docs / build a test fixture without re-fetching.
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// MCPCacheStatsResponse is the wire shape for GET /hecate/v1/system/mcp/cache.
// Surfaces the SharedClientCache snapshot — entries / in-use / idle —
// so operators can answer "is the cache doing useful work?" without
// scraping OTLP. Configured indicates whether a cache is wired at all
// (false on deploys that explicitly disabled it via a setter), which
// is operationally distinct from "wired but empty."
type MCPCacheStatsResponse struct {
	Object string                    `json:"object"`
	Data   MCPCacheStatsResponseItem `json:"data"`
}

type MCPCacheStatsResponseItem struct {
	CheckedAt  string `json:"checked_at"`
	Configured bool   `json:"configured"`
	Entries    int    `json:"entries"`
	// InUse is the SUM of refcounts across all entries — total live
	// Acquire→Release pairs in flight, NOT the count of entries with
	// at least one acquirer. See SharedClientCache.Stats for the
	// contract.
	InUse int `json:"in_use"`
	// Idle is the count of entries with refcount == 0 (the ones the
	// reaper will evict once their lastUsed crosses the TTL boundary).
	Idle int `json:"idle"`
}

type AccountSummaryResponseItem struct {
	Account   BudgetStatusResponseItem     `json:"account"`
	Estimates []AccountModelEstimateRecord `json:"estimates"`
}

type AccountModelEstimateRecord struct {
	Provider                        string `json:"provider"`
	ProviderKind                    string `json:"provider_kind"`
	Model                           string `json:"model"`
	Default                         bool   `json:"default,omitempty"`
	DiscoverySource                 string `json:"discovery_source,omitempty"`
	Priced                          bool   `json:"priced"`
	InputMicrosUSDPerMillionTokens  int64  `json:"input_micros_usd_per_million_tokens"`
	OutputMicrosUSDPerMillionTokens int64  `json:"output_micros_usd_per_million_tokens"`
	EstimatedRemainingPromptTokens  int64  `json:"estimated_remaining_prompt_tokens"`
	EstimatedRemainingOutputTokens  int64  `json:"estimated_remaining_output_tokens"`
}

type BudgetStatusResponseItem struct {
	Key                string                `json:"key"`
	Scope              string                `json:"scope"`
	Provider           string                `json:"provider,omitempty"`
	Tenant             string                `json:"tenant,omitempty"`
	Backend            string                `json:"backend"`
	BalanceSource      string                `json:"balance_source"`
	DebitedMicrosUSD   int64                 `json:"debited_micros_usd"`
	DebitedUSD         string                `json:"debited_usd"`
	CreditedMicrosUSD  int64                 `json:"credited_micros_usd"`
	CreditedUSD        string                `json:"credited_usd"`
	BalanceMicrosUSD   int64                 `json:"balance_micros_usd"`
	BalanceUSD         string                `json:"balance_usd"`
	AvailableMicrosUSD int64                 `json:"available_micros_usd"`
	AvailableUSD       string                `json:"available_usd"`
	Enforced           bool                  `json:"enforced"`
	Warnings           []BudgetWarningRecord `json:"warnings,omitempty"`
	History            []BudgetHistoryRecord `json:"history,omitempty"`
}

type BudgetWarningRecord struct {
	ThresholdPercent   int   `json:"threshold_percent"`
	ThresholdMicrosUSD int64 `json:"threshold_micros_usd"`
	BalanceMicrosUSD   int64 `json:"balance_micros_usd"`
	AvailableMicrosUSD int64 `json:"available_micros_usd"`
	Triggered          bool  `json:"triggered"`
}

type BudgetHistoryRecord struct {
	Type              string `json:"type"`
	Scope             string `json:"scope,omitempty"`
	Provider          string `json:"provider,omitempty"`
	Tenant            string `json:"tenant,omitempty"`
	Model             string `json:"model,omitempty"`
	RequestID         string `json:"request_id,omitempty"`
	Actor             string `json:"actor,omitempty"`
	Detail            string `json:"detail,omitempty"`
	AmountMicrosUSD   int64  `json:"amount_micros_usd"`
	AmountUSD         string `json:"amount_usd"`
	BalanceMicrosUSD  int64  `json:"balance_micros_usd"`
	BalanceUSD        string `json:"balance_usd"`
	CreditedMicrosUSD int64  `json:"credited_micros_usd"`
	CreditedUSD       string `json:"credited_usd"`
	DebitedMicrosUSD  int64  `json:"debited_micros_usd"`
	DebitedUSD        string `json:"debited_usd"`
	PromptTokens      int    `json:"prompt_tokens,omitempty"`
	CompletionTokens  int    `json:"completion_tokens,omitempty"`
	TotalTokens       int    `json:"total_tokens,omitempty"`
	Timestamp         string `json:"timestamp,omitempty"`
}

type RetentionRunData struct {
	StartedAt  string                     `json:"started_at"`
	FinishedAt string                     `json:"finished_at"`
	Trigger    string                     `json:"trigger"`
	Actor      string                     `json:"actor,omitempty"`
	RequestID  string                     `json:"request_id,omitempty"`
	Results    []RetentionRunResultRecord `json:"results"`
}

type RetentionRunResultRecord struct {
	Name     string `json:"name"`
	Deleted  int    `json:"deleted"`
	MaxAge   string `json:"max_age,omitempty"`
	MaxCount int    `json:"max_count"`
	Error    string `json:"error,omitempty"`
	Skipped  bool   `json:"skipped,omitempty"`
}

type RetentionRunResponse struct {
	Object string           `json:"object"`
	Data   RetentionRunData `json:"data"`
}

type RetentionRunsResponse struct {
	Object string             `json:"object"`
	Data   []RetentionRunData `json:"data"`
}

type BudgetResetRequest struct {
	Key      string `json:"key"`
	Scope    string `json:"scope"`
	Provider string `json:"provider"`
	Tenant   string `json:"tenant"`
}

type BudgetTopUpRequest struct {
	Key             string `json:"key"`
	Scope           string `json:"scope"`
	Provider        string `json:"provider"`
	Tenant          string `json:"tenant"`
	AmountMicrosUSD int64  `json:"amount_micros_usd"`
}

type BudgetBalanceRequest struct {
	Key              string `json:"key"`
	Scope            string `json:"scope"`
	Provider         string `json:"provider"`
	Tenant           string `json:"tenant"`
	BalanceMicrosUSD int64  `json:"balance_micros_usd"`
}

type SettingsResponse struct {
	Object string               `json:"object"`
	Data   SettingsResponseItem `json:"data"`
}

type SettingsResponseItem struct {
	Backend     string                     `json:"backend"`
	Providers   []SettingsProviderRecord   `json:"providers"`
	PolicyRules []SettingsPolicyRuleRecord `json:"policy_rules"`
	Pricebook   []SettingsPricebookRecord  `json:"pricebook"`
	Events      []SettingsAuditEventRecord `json:"events"`
}

type SettingsProviderRecord struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	PresetID             string   `json:"preset_id,omitempty"`
	CustomName           string   `json:"custom_name,omitempty"`
	Kind                 string   `json:"kind"`
	Protocol             string   `json:"protocol"`
	BaseURL              string   `json:"base_url"`
	APIVersion           string   `json:"api_version,omitempty"`
	DefaultModel         string   `json:"default_model,omitempty"`
	ExplicitFields       []string `json:"explicit_fields,omitempty"`
	InheritedFields      []string `json:"inherited_fields,omitempty"`
	CredentialConfigured bool     `json:"credential_configured"`
	CredentialSource     string   `json:"credential_source,omitempty"`
}

type SettingsPolicyRuleRecord struct {
	ID                     string   `json:"id"`
	Action                 string   `json:"action"`
	Reason                 string   `json:"reason,omitempty"`
	Providers              []string `json:"providers,omitempty"`
	ProviderKinds          []string `json:"provider_kinds,omitempty"`
	Models                 []string `json:"models,omitempty"`
	RouteReasons           []string `json:"route_reasons,omitempty"`
	MinPromptTokens        int      `json:"min_prompt_tokens,omitempty"`
	MinEstimatedCostMicros int64    `json:"min_estimated_cost_micros_usd,omitempty"`
	RewriteModelTo         string   `json:"rewrite_model_to,omitempty"`
}

type SettingsPricebookRecord struct {
	Provider                             string `json:"provider"`
	Model                                string `json:"model"`
	InputMicrosUSDPerMillionTokens       int64  `json:"input_micros_usd_per_million_tokens"`
	OutputMicrosUSDPerMillionTokens      int64  `json:"output_micros_usd_per_million_tokens"`
	CachedInputMicrosUSDPerMillionTokens int64  `json:"cached_input_micros_usd_per_million_tokens"`
	// Source is "manual" (operator-edited) or "imported" (LiteLLM bulk
	// import). Empty on legacy responses; the UI treats empty as manual.
	Source string `json:"source,omitempty"`
}

// PricebookImportUpdateRecord pairs an incoming imported entry with the
// current row it would overwrite, so the UI can show a side-by-side diff
// before the operator confirms apply.
type PricebookImportUpdateRecord struct {
	Entry    SettingsPricebookRecord `json:"entry"`
	Previous SettingsPricebookRecord `json:"previous"`
}

// PricebookImportFailureRecord pairs an entry the apply endpoint tried
// to persist with the storage error it hit. The apply loop is best-
// effort: a failure on one row doesn't stop the others, so a single
// 4xx with no per-row reporting would leave the operator unable to
// tell what landed and what didn't. Each failure carries the
// SettingsPricebookRecord we attempted to write plus the raw error
// message — the UI can show them as a follow-up in the consent dialog.
type PricebookImportFailureRecord struct {
	Entry SettingsPricebookRecord `json:"entry"`
	Error string                  `json:"error"`
}

// PricebookImportDiff is the response payload for both the preview and apply
// endpoints. On preview, `Added`, `Updated`, and `Skipped` are populated; on
// apply, the rows that were persisted move into `Applied` and rows that hit
// a storage error during the loop move into `Failed`.
//
// `Skipped` lists currently-manual rows where LiteLLM has a *different* price.
// We never touch them in a blanket apply (manual is operator-protected), but
// the UI can offer an explicit "replace this manual row with LiteLLM's price"
// affordance — when the operator opts in by passing the row's key in the
// apply request, the backend honors it. Each entry pairs LiteLLM's proposal
// (`Entry`) with the current manual row (`Previous`), the same shape as
// `Updated`, so the UI can render a price diff identically.
type PricebookImportDiff struct {
	FetchedAt string                         `json:"fetched_at"`
	Added     []SettingsPricebookRecord      `json:"added,omitempty"`
	Updated   []PricebookImportUpdateRecord  `json:"updated,omitempty"`
	Applied   []SettingsPricebookRecord      `json:"applied,omitempty"`
	Failed    []PricebookImportFailureRecord `json:"failed,omitempty"`
	Unchanged int                            `json:"unchanged"`
	Skipped   []PricebookImportUpdateRecord  `json:"skipped,omitempty"`
}

// PricebookImportApplyRequest narrows the apply call to a subset of rows.
// Empty `keys` (or omitted) means "apply everything in Added+Updated".
// Each key is "<provider>/<model>", matching the format the UI displays
// in the import-modal checklist.
type PricebookImportApplyRequest struct {
	Keys []string `json:"keys,omitempty"`
}

type SettingsAuditEventRecord struct {
	Timestamp  string `json:"timestamp"`
	Actor      string `json:"actor"`
	Action     string `json:"action"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Detail     string `json:"detail,omitempty"`
}

type SettingsProviderUpsertRequest struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	PresetID     string  `json:"preset_id"`
	Kind         *string `json:"kind,omitempty"`
	Protocol     *string `json:"protocol,omitempty"`
	BaseURL      *string `json:"base_url,omitempty"`
	APIVersion   *string `json:"api_version,omitempty"`
	DefaultModel *string `json:"default_model,omitempty"`
	Enabled      bool    `json:"enabled"`
	Key          string  `json:"key"`
}

type SettingsPolicyRuleUpsertRequest = SettingsPolicyRuleRecord

type SettingsPricebookUpsertRequest = SettingsPricebookRecord

type SettingsTenantLifecycleRequest struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

type SettingsAPIKeyLifecycleRequest struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Key     string `json:"key"`
}
