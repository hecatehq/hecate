package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hecate/agent-runtime/internal/agentcontrols"
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
	Model       string              `json:"model"`
	Provider    string              `json:"provider,omitempty"`
	Messages    []OpenAIChatMessage `json:"messages"`
	MaxTokens   int                 `json:"max_tokens,omitempty"`
	Temperature float64             `json:"temperature,omitempty"`
	User        string              `json:"user,omitempty"`
	Tools       []OpenAITool        `json:"tools,omitempty"`
	ToolChoice  json.RawMessage     `json:"tool_choice,omitempty"`
	Stream      bool                `json:"stream,omitempty"`
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

// SessionResponseItem reports who is calling. In single-user mode this
// always describes the anonymous local operator — auth was removed and
// the gateway treats every caller as fully privileged.
type SessionResponseItem struct {
	Role string `json:"role"`
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

type ChatSessionsResponse struct {
	Object string                   `json:"object"`
	Data   []ChatSessionSummaryItem `json:"data"`
}

type ChatSessionResponse struct {
	Object string          `json:"object"`
	Data   ChatSessionItem `json:"data"`
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
	Readiness           ReadinessSummaryResponseItem         `json:"readiness,omitempty"`
	ReadinessChecks     []ProviderReadinessCheckResponseItem `json:"readiness_checks,omitempty"`
}

type ReadinessSummaryResponseItem struct {
	Status         string `json:"status,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Message        string `json:"message,omitempty"`
	OperatorAction string `json:"operator_action,omitempty"`
}

type ModelReadinessResponseItem struct {
	Provider              string   `json:"provider,omitempty"`
	MatchedProvider       string   `json:"matched_provider,omitempty"`
	Model                 string   `json:"model,omitempty"`
	Ready                 bool     `json:"ready"`
	Status                string   `json:"status,omitempty"`
	Reason                string   `json:"reason,omitempty"`
	Message               string   `json:"message,omitempty"`
	OperatorAction        string   `json:"operator_action,omitempty"`
	RoutingReady          bool     `json:"routing_ready"`
	ProviderStatus        string   `json:"provider_status,omitempty"`
	ProviderBlockedReason string   `json:"provider_blocked_reason,omitempty"`
	SuggestedModels       []string `json:"suggested_models,omitempty"`
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
	ID                  string                              `json:"id"`
	Name                string                              `json:"name"`
	Kind                string                              `json:"kind"`
	Command             string                              `json:"command"`
	Args                []string                            `json:"args,omitempty"`
	Managed             bool                                `json:"managed,omitempty"`
	ManagedPackage      string                              `json:"managed_package,omitempty"`
	Available           bool                                `json:"available"`
	Status              string                              `json:"status"`
	Path                string                              `json:"path,omitempty"`
	Error               string                              `json:"error,omitempty"`
	Description         string                              `json:"description,omitempty"`
	CostMode            string                              `json:"cost_mode,omitempty"`
	DocsURL             string                              `json:"docs_url,omitempty"`
	Version             string                              `json:"version,omitempty"`
	SupportedRange      string                              `json:"supported_range,omitempty"`
	VersionOutsideRange bool                                `json:"version_outside_range,omitempty"`
	AuthStatus          string                              `json:"auth_status,omitempty"`
	AuthError           string                              `json:"auth_error,omitempty"`
	ClaudeCodeCLI       *AgentAdapterSetupCommandStatusItem `json:"claude_code_cli,omitempty"`
}

type AgentAdapterSetupCommandStatusItem struct {
	Available      bool   `json:"available"`
	Command        string `json:"command,omitempty"`
	ExecutablePath string `json:"executable_path,omitempty"`
}

type CreateChatSessionRequest struct {
	Title      string `json:"title,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
	Workspace  string `json:"workspace"`
	RTKEnabled bool   `json:"rtk_enabled,omitempty"`
}

type CreateChatMessageRequest struct {
	Content       string `json:"content"`
	ExecutionMode string `json:"execution_mode,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	SystemPrompt  string `json:"system_prompt,omitempty"`
	Workspace     string `json:"workspace,omitempty"`
}

type UpdateChatSessionRequest struct {
	Title *string `json:"title,omitempty"`
}

type SetAgentChatConfigOptionRequest struct {
	Value any `json:"value"`
}

type SetAgentChatSettingsRequest struct {
	RTKEnabled *bool `json:"rtk_enabled,omitempty"`
}

type ChatSessionSummaryItem struct {
	ID              string                  `json:"id"`
	Title           string                  `json:"title"`
	AgentID         string                  `json:"agent_id"`
	DriverKind      string                  `json:"driver_kind,omitempty"`
	NativeSessionID string                  `json:"native_session_id,omitempty"`
	TaskID          string                  `json:"task_id,omitempty"`
	LatestRunID     string                  `json:"latest_run_id,omitempty"`
	Provider        string                  `json:"provider,omitempty"`
	Model           string                  `json:"model,omitempty"`
	Capabilities    types.ModelCapabilities `json:"capabilities,omitempty"`
	RTKEnabled      bool                    `json:"rtk_enabled,omitempty"`
	Workspace       string                  `json:"workspace"`
	WorkspaceBranch string                  `json:"workspace_branch,omitempty"`
	Status          string                  `json:"status"`
	MessageCount    int                     `json:"message_count"`
	CreatedAt       string                  `json:"created_at,omitempty"`
	UpdatedAt       string                  `json:"updated_at,omitempty"`
}

type ChatSessionItem struct {
	ID                   string                       `json:"id"`
	Title                string                       `json:"title"`
	AgentID              string                       `json:"agent_id"`
	DriverKind           string                       `json:"driver_kind,omitempty"`
	NativeSessionID      string                       `json:"native_session_id,omitempty"`
	TaskID               string                       `json:"task_id,omitempty"`
	LatestRunID          string                       `json:"latest_run_id,omitempty"`
	Provider             string                       `json:"provider,omitempty"`
	Model                string                       `json:"model,omitempty"`
	Capabilities         types.ModelCapabilities      `json:"capabilities,omitempty"`
	RTKEnabled           bool                         `json:"rtk_enabled,omitempty"`
	Workspace            string                       `json:"workspace"`
	WorkspaceBranch      string                       `json:"workspace_branch,omitempty"`
	Status               string                       `json:"status"`
	TurnsUsed            int                          `json:"turns_used"`
	MaxTurnsPerSession   int                          `json:"max_turns_per_session,omitempty"`
	SessionStartedAt     string                       `json:"session_started_at,omitempty"`
	MaxSessionDurationMS int64                        `json:"max_session_duration_ms,omitempty"`
	IdleTimeoutMS        int64                        `json:"idle_timeout_ms,omitempty"`
	ConfigOptions        []agentcontrols.ConfigOption `json:"config_options,omitempty"`
	CreatedAt            string                       `json:"created_at,omitempty"`
	UpdatedAt            string                       `json:"updated_at,omitempty"`
	Segments             []ChatSegmentItem            `json:"segments,omitempty"`
	Messages             []ChatMessageItem            `json:"messages"`
}

type ChatSegmentItem struct {
	ID            string `json:"id"`
	ExecutionMode string `json:"execution_mode"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	TaskID        string `json:"task_id,omitempty"`
	LatestRunID   string `json:"latest_run_id,omitempty"`
	Workspace     string `json:"workspace,omitempty"`
	Status        string `json:"status,omitempty"`
	MessageCount  int    `json:"message_count"`
	StartedAt     string `json:"started_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

type ChatMessageItem struct {
	ID              string                  `json:"id"`
	ExecutionMode   string                  `json:"execution_mode,omitempty"`
	SegmentID       string                  `json:"segment_id,omitempty"`
	TaskID          string                  `json:"task_id,omitempty"`
	RunID           string                  `json:"run_id,omitempty"`
	RequestID       string                  `json:"request_id,omitempty"`
	TraceID         string                  `json:"trace_id,omitempty"`
	SpanID          string                  `json:"span_id,omitempty"`
	Role            string                  `json:"role"`
	Content         string                  `json:"content"`
	RawOutput       string                  `json:"raw_output,omitempty"`
	AgentID         string                  `json:"agent_id,omitempty"`
	AgentName       string                  `json:"agent_name,omitempty"`
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
	Activities      []ChatActivityItem      `json:"activities,omitempty"`
	Usage           *ChatUsageItem          `json:"usage,omitempty"`
	Timing          *ChatTimingItem         `json:"timing,omitempty"`
}

type ChatChangedFileItem struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Status    string `json:"status"`
}

type ChatChangedFilesResponse struct {
	Object string                `json:"object"`
	Data   []ChatChangedFileItem `json:"data"`
}

type ChatChangedFileDiffItem struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Status    string `json:"status"`
	Diff      string `json:"diff"`
}

type ChatChangedFileDiffResponse struct {
	Object string                  `json:"object"`
	Data   ChatChangedFileDiffItem `json:"data"`
}

type RevertChatMessageFilesRequest struct {
	Paths []string `json:"paths,omitempty"`
}

type ChatRevertItem struct {
	Reverted bool                  `json:"reverted"`
	Paths    []string              `json:"paths"`
	DiffStat string                `json:"diff_stat,omitempty"`
	Files    []ChatChangedFileItem `json:"files"`
}

type ChatRevertResponse struct {
	Object string         `json:"object"`
	Data   ChatRevertItem `json:"data"`
}

type ChatActivityItem struct {
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

type ChatUsageItem struct {
	ContextSize          int    `json:"context_size,omitempty"`
	ContextUsed          int    `json:"context_used,omitempty"`
	ReportedCostAmount   string `json:"reported_cost_amount,omitempty"`
	ReportedCostCurrency string `json:"reported_cost_currency,omitempty"`
}

type ChatTimingItem struct {
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

type UsageSummaryResponse struct {
	Object string                   `json:"object"`
	Data   UsageSummaryResponseItem `json:"data"`
}

type UsageEventsResponse struct {
	Object string             `json:"object"`
	Data   []UsageEventRecord `json:"data"`
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
	// RTKAvailable reports whether the optional RTK command-output
	// wrapper is installed in the gateway process PATH. The UI uses this
	// to offer compact command-output setup without enabling it by default.
	RTKAvailable bool   `json:"rtk_available"`
	RTKPath      string `json:"rtk_path,omitempty"`
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

type UsageSummaryResponseItem struct {
	Key           string `json:"key"`
	Scope         string `json:"scope"`
	Provider      string `json:"provider,omitempty"`
	Backend       string `json:"backend"`
	UsedMicrosUSD int64  `json:"used_micros_usd"`
	UsedUSD       string `json:"used_usd"`
}

type UsageEventRecord struct {
	Type             string `json:"type"`
	Scope            string `json:"scope,omitempty"`
	Provider         string `json:"provider,omitempty"`
	Model            string `json:"model,omitempty"`
	RequestID        string `json:"request_id,omitempty"`
	Actor            string `json:"actor,omitempty"`
	Detail           string `json:"detail,omitempty"`
	AmountMicrosUSD  int64  `json:"amount_micros_usd"`
	AmountUSD        string `json:"amount_usd"`
	PromptTokens     int    `json:"prompt_tokens,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"`
	TotalTokens      int    `json:"total_tokens,omitempty"`
	Timestamp        string `json:"timestamp,omitempty"`
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

type SettingsResponse struct {
	Object string               `json:"object"`
	Data   SettingsResponseItem `json:"data"`
}

type SettingsResponseItem struct {
	Backend     string                     `json:"backend"`
	Providers   []SettingsProviderRecord   `json:"providers"`
	PolicyRules []SettingsPolicyRuleRecord `json:"policy_rules"`
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

type SettingsTenantLifecycleRequest struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

type SettingsAPIKeyLifecycleRequest struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Key     string `json:"key"`
}
