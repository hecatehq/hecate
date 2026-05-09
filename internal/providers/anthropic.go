package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/pkg/types"
)

const defaultAnthropicVersion = "2023-06-01"

type AnthropicProvider struct {
	config     config.OpenAICompatibleProviderConfig
	logger     *slog.Logger
	httpClient *http.Client
	mu         sync.Mutex
	cachedCaps Capabilities
	capsExpiry time.Time
}

type anthropicMessagesRequest struct {
	Model         string                     `json:"model"`
	System        json.RawMessage            `json:"system,omitempty"` // string or [{type,text,cache_control}]
	Messages      []anthropicMessage         `json:"messages"`
	MaxTokens     int                        `json:"max_tokens"`
	Temperature   float64                    `json:"temperature,omitempty"`
	TopP          float64                    `json:"top_p,omitempty"`
	TopK          int                        `json:"top_k,omitempty"`
	StopSequences []string                   `json:"stop_sequences,omitempty"`
	Metadata      *anthropicMessagesMetadata `json:"metadata,omitempty"`
	Tools         []anthropicTool            `json:"tools,omitempty"`
	ToolChoice    json.RawMessage            `json:"tool_choice,omitempty"`
	Stream        bool                       `json:"stream,omitempty"`
	// Extended thinking: {"type":"enabled","budget_tokens":N}
	Thinking json.RawMessage `json:"thinking,omitempty"`
	// ServiceTier passes through the operator's tier preference
	// (`auto`, `standard_only`, etc.). Empty omits the field —
	// Anthropic then picks per its default policy.
	ServiceTier string `json:"service_tier,omitempty"`
}

type anthropicMessagesMetadata struct {
	UserID string `json:"user_id,omitempty"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

// anthropicContentBlock covers all block variants (text, tool_use, tool_result, thinking).
type anthropicContentBlock struct {
	Type string `json:"type"`
	// text
	Text string `json:"text,omitempty"`
	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	// ResultContent is the tool_result content payload. Anthropic
	// accepts either a JSON string OR a JSON array of nested
	// content blocks (text/image/etc.) — we use json.RawMessage so
	// either shape can be sent on the wire. The toolResultBlock
	// builder picks the shape based on whether the source message
	// has structured ContentBlocks.
	ResultContent json.RawMessage `json:"content,omitempty"`
	// IsError flags a tool_result as a failed tool call. Anthropic
	// uses it to feed clearer failure context to the model.
	IsError bool `json:"is_error,omitempty"`
	// Source carries the image payload for type=="image" blocks.
	// Anthropic accepts {type:"url", url:"..."} or
	// {type:"base64", media_type:"image/png", data:"..."}.
	// We use json.RawMessage so the builder can pick either shape
	// without a typed union.
	Source json.RawMessage `json:"source,omitempty"`
	// prompt caching
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
	// extended thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"` // redacted_thinking opaque data
}

type anthropicTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"input_schema"`
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
}

type anthropicMessagesResponse struct {
	ID         string                  `json:"id"`
	Model      string                  `json:"model"`
	Role       string                  `json:"role"`
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      anthropicUsage          `json:"usage"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	// CacheReadInputTokens are tokens served from a prior turn's
	// prompt cache. Anthropic bills these at a steeply discounted
	// rate (typically 0.1× the base input rate). The API returns
	// them disjoint from input_tokens, so we map them to
	// types.Usage.CachedPromptTokens — which the pricebook scales
	// at CachedInputMicrosUSDPerMillionTokens.
	CacheReadInputTokens int `json:"cache_read_input_tokens,omitempty"`
	// CacheCreationInputTokens are tokens written to the cache on
	// this turn (charged at ~1.25× base rate at Anthropic).
	// Hecate's pricebook has no separate cache-write rate yet, so
	// we fold these into Usage.PromptTokens at the fresh rate
	// (under-charges by ~20% per cache-write token vs. Anthropic's
	// listed rate, but at least counts them — the prior adapter
	// dropped them entirely). When the pricebook gains a
	// CacheCreationMicrosUSDPerMillionTokens rate, split this back
	// out into a dedicated Usage field.
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
}

type anthropicModelsResponse struct {
	Data []anthropicModel `json:"data"`
}

type anthropicModel struct {
	ID string `json:"id"`
}

type anthropicErrorEnvelope struct {
	Error anthropicErrorDetail `json:"error"`
}

type anthropicErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func NewAnthropicProvider(cfg config.OpenAICompatibleProviderConfig, logger *slog.Logger) *AnthropicProvider {
	return &AnthropicProvider{
		config: cfg,
		logger: logger,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

func (p *AnthropicProvider) Name() string {
	return p.config.Name
}

func (p *AnthropicProvider) Enabled() bool {
	return p.config.Enabled
}

func (p *AnthropicProvider) BaseURL() string {
	return p.config.BaseURL
}

func (p *AnthropicProvider) CredentialState() CredentialState {
	if p.Kind() == KindLocal || p.config.StubMode {
		return CredentialStateNotRequired
	}
	if strings.TrimSpace(p.config.APIKey) == "" {
		return CredentialStateMissing
	}
	return CredentialStateConfigured
}

func (p *AnthropicProvider) Kind() Kind {
	if p.config.Kind == string(KindLocal) {
		return KindLocal
	}
	return KindCloud
}

func (p *AnthropicProvider) DefaultModel() string {
	return p.config.DefaultModel
}

func (p *AnthropicProvider) Supports(model string) bool {
	if model == "" {
		return p.config.DefaultModel != ""
	}
	return p.config.DefaultModel != "" && p.config.DefaultModel == model
}

func (p *AnthropicProvider) supportsResolvedModel(ctx context.Context, model string) bool {
	if model == "" {
		return p.config.DefaultModel != ""
	}
	caps, err := p.Capabilities(ctx)
	if err == nil {
		for _, candidate := range caps.Models {
			if candidate == model {
				return true
			}
		}
		if caps.DefaultModel == model {
			return true
		}
	}
	return p.Supports(model)
}

func (p *AnthropicProvider) Capabilities(ctx context.Context) (Capabilities, error) {
	if p.config.StubMode {
		return p.staticCapabilities("config"), nil
	}
	return resolveCapabilities(
		ctx,
		p.logger,
		p.Name(),
		p.Kind(),
		p.config.APIKey,
		&p.mu,
		&p.cachedCaps,
		&p.capsExpiry,
		p.discoverCapabilities,
		p.staticCapabilities,
	)
}

func (p *AnthropicProvider) Chat(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	if !p.supportsResolvedModel(ctx, req.Model) {
		return nil, fmt.Errorf("model %q is not supported by provider %s", req.Model, p.Name())
	}
	if p.config.StubMode {
		content := p.config.StubResponse
		if content == "" {
			content = "Stubbed Anthropic response."
		}
		promptTokens := estimatePromptTokens(req.Messages)
		completionTokens := max(16, len(content)/4)
		now := time.Now().UTC()
		return &types.ChatResponse{
			ID:        "msg-stub",
			Model:     req.Model,
			CreatedAt: now,
			Choices: []types.ChatChoice{{
				Index: 0,
				Message: types.Message{
					Role:    "assistant",
					Content: content,
				},
				FinishReason: "stop",
			}},
			Usage: types.Usage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			},
		}, nil
	}
	return p.chatUpstream(ctx, req)
}

func (p *AnthropicProvider) staticCapabilities(source string) Capabilities {
	models := append([]string(nil), p.config.KnownModels...)
	if p.config.DefaultModel != "" && !contains(models, p.config.DefaultModel) {
		models = append(models, p.config.DefaultModel)
	}
	return Capabilities{
		Name:            p.Name(),
		Kind:            p.Kind(),
		DefaultModel:    p.config.DefaultModel,
		Models:          models,
		Discoverable:    !p.config.StubMode,
		DiscoverySource: source,
		RefreshedAt:     time.Now().UTC(),
	}
}

func (p *AnthropicProvider) discoverCapabilities(ctx context.Context) (Capabilities, error) {
	endpoint := strings.TrimRight(p.config.BaseURL, "/") + "/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Capabilities{}, fmt.Errorf("build models request: %w", err)
	}
	p.applyHeaders(req)
	injectTraceContext(req)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return Capabilities{}, fmt.Errorf("send models request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return Capabilities{}, decodeAnthropicError(resp)
	}

	var payload anthropicModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Capabilities{}, fmt.Errorf("decode models response: %w", err)
	}

	models := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		if item.ID != "" && !contains(models, item.ID) {
			models = append(models, item.ID)
		}
	}
	defaultModel := p.config.DefaultModel
	if defaultModel == "" && len(models) > 0 {
		defaultModel = models[0]
	}
	return Capabilities{
		Name:            p.Name(),
		Kind:            p.Kind(),
		DefaultModel:    defaultModel,
		Models:          models,
		Discoverable:    true,
		DiscoverySource: "upstream_v1_models",
		RefreshedAt:     time.Now().UTC(),
	}, nil
}

func (p *AnthropicProvider) Validate() error {
	if p.config.APIKey == "" && p.Kind() != KindLocal && !p.config.StubMode {
		return fmt.Errorf("api key is required for cloud provider %s when stub mode is disabled", p.Name())
	}
	return nil
}

func (p *AnthropicProvider) chatUpstream(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}

	systemRaw, messages := anthropicMessagesFromTypes(req.Messages)
	if len(messages) == 0 {
		return nil, fmt.Errorf("anthropic messages request requires at least one non-system message")
	}
	wireReq := anthropicMessagesRequest{
		Model:         req.Model,
		System:        systemRaw,
		Messages:      messages,
		MaxTokens:     req.MaxTokens,
		TopP:          req.TopP,
		TopK:          req.TopK,
		StopSequences: append([]string(nil), req.StopSequences...),
	}
	if wireReq.MaxTokens <= 0 {
		wireReq.MaxTokens = 1024
	}
	if req.Temperature > 0 {
		wireReq.Temperature = req.Temperature
	}
	if len(req.Thinking) > 0 {
		wireReq.Thinking = req.Thinking
	}
	if strings.TrimSpace(req.ServiceTier) != "" {
		wireReq.ServiceTier = strings.TrimSpace(req.ServiceTier)
	}
	p.warnResponseFormatDropped(req)
	if len(req.Tools) > 0 {
		wireReq.Tools = make([]anthropicTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			schema := t.Function.Parameters
			if len(schema) == 0 {
				schema = json.RawMessage(`{}`)
			}
			wireReq.Tools = append(wireReq.Tools, anthropicTool{
				Name:         t.Function.Name,
				Description:  t.Function.Description,
				InputSchema:  schema,
				CacheControl: t.CacheControl,
			})
		}
		wireReq.ToolChoice = anthropicToolChoice(req.ToolChoice)
	}
	if !p.config.AnthropicCacheDisabled {
		wireReq.System = applyAnthropicSystemCacheMarker(wireReq.System)
		applyAnthropicToolsCacheMarker(wireReq.Tools)
	}

	payload, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("marshal upstream request: %w", err)
	}
	endpoint := strings.TrimRight(p.config.BaseURL, "/") + "/v1/messages"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	p.applyHeaders(httpReq)
	if len(req.Betas) > 0 {
		httpReq.Header.Set("anthropic-beta", strings.Join(req.Betas, ","))
	}
	injectTraceContext(httpReq)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send upstream request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, decodeAnthropicError(resp)
	}

	var wireResp anthropicMessagesResponse
	if err := json.NewDecoder(resp.Body).Decode(&wireResp); err != nil {
		return nil, fmt.Errorf("decode upstream response: %w", err)
	}
	model := wireResp.Model
	if model == "" {
		model = req.Model
	}
	msg := anthropicResponseToMessage(wireResp.Content)
	finishReason := wireResp.StopReason
	if finishReason == "tool_use" {
		finishReason = "tool_calls"
	}
	return &types.ChatResponse{
		ID:        wireResp.ID,
		Model:     model,
		CreatedAt: time.Now().UTC(),
		Choices: []types.ChatChoice{{
			Index:        0,
			Message:      msg,
			FinishReason: finishReason,
		}},
		Usage: anthropicUsageToTypes(wireResp.Usage),
	}, nil
}

// anthropicUsageToTypes maps Anthropic's three-bucket usage
// (input / cache_read / cache_creation) onto Hecate's two-bucket
// pricebook model (PromptTokens / CachedPromptTokens). Cache reads
// land in their own bucket so the pricebook applies the cache rate;
// cache creations fold into PromptTokens at the fresh rate (see
// anthropicUsage docs for the trade-off). TotalTokens is the sum
// of all three input variants plus output, matching what the
// operator is actually billed for.
func anthropicUsageToTypes(u anthropicUsage) types.Usage {
	prompt := u.InputTokens + u.CacheCreationInputTokens
	return types.Usage{
		PromptTokens:       prompt,
		CompletionTokens:   u.OutputTokens,
		CachedPromptTokens: u.CacheReadInputTokens,
		TotalTokens:        prompt + u.OutputTokens + u.CacheReadInputTokens,
	}
}

// warnUnsupportedFieldsDropped logs once per request when the
// caller set OpenAI-specific knobs that have no Anthropic
// equivalent. We don't fail the request — the model still produces
// something usable — but the operator deserves to know the
// constraint was silently dropped. Each entry below names the
// field, includes its value in the log for diagnosability, and
// hints at the right Anthropic-side equivalent (or notes there
// is none).
//
// Centralized here rather than scattered across per-field calls
// so adding a new passthrough is a one-line append: declare the
// field on ChatRequest + add a case below.
func (p *AnthropicProvider) warnUnsupportedFieldsDropped(req types.ChatRequest) {
	if p.logger == nil {
		return
	}
	emit := func(field string, value any, hint string) {
		p.logger.Warn("OpenAI-only field dropped on Anthropic route",
			slog.String("provider", p.Name()),
			slog.String("model", req.Model),
			slog.String("field", field),
			slog.Any("value", value),
			slog.String("hint", hint),
		)
	}
	if len(req.ResponseFormat) > 0 {
		emit("response_format", string(req.ResponseFormat),
			"Anthropic has no direct equivalent; use tools + tool_choice for structured output")
	}
	if req.Seed != nil {
		emit("seed", *req.Seed,
			"Anthropic has no deterministic-sampling knob")
	}
	if req.PresencePenalty != 0 {
		emit("presence_penalty", req.PresencePenalty,
			"Anthropic does not expose presence/frequency penalties")
	}
	if req.FrequencyPenalty != 0 {
		emit("frequency_penalty", req.FrequencyPenalty,
			"Anthropic does not expose presence/frequency penalties")
	}
	if req.Logprobs {
		emit("logprobs", true,
			"Anthropic does not return per-token log probabilities")
	}
	if req.TopLogprobs > 0 {
		emit("top_logprobs", req.TopLogprobs,
			"Anthropic does not return per-token log probabilities")
	}
	if len(req.LogitBias) > 0 {
		emit("logit_bias", string(req.LogitBias),
			"Anthropic does not accept logit_bias")
	}
	if len(req.StreamOptions) > 0 {
		emit("stream_options", string(req.StreamOptions),
			"Anthropic streaming has its own usage shape; per-chunk usage is already forwarded by translateAnthropicSSE")
	}
	if req.ParallelToolCalls != nil && !*req.ParallelToolCalls {
		emit("parallel_tool_calls", false,
			"to disable parallelism on Anthropic, embed disable_parallel_tool_use:true inside tool_choice")
	}
}

// warnResponseFormatDropped is retained for compatibility with the
// existing call sites. The general path now lives in
// warnUnsupportedFieldsDropped.
func (p *AnthropicProvider) warnResponseFormatDropped(req types.ChatRequest) {
	p.warnUnsupportedFieldsDropped(req)
}

func (p *AnthropicProvider) applyHeaders(req *http.Request) {
	if p.config.APIKey != "" {
		req.Header.Set("x-api-key", p.config.APIKey)
	}
	req.Header.Set("anthropic-version", p.apiVersion())
}

func (p *AnthropicProvider) apiVersion() string {
	if strings.TrimSpace(p.config.APIVersion) != "" {
		return strings.TrimSpace(p.config.APIVersion)
	}
	return defaultAnthropicVersion
}

// anthropicMessagesFromTypes converts internal messages to Anthropic wire format.
// Returns (system, messages) where system is a json.RawMessage (either a JSON string or
// a JSON array of text blocks, preserving cache_control) and messages is the conversation.
func anthropicMessagesFromTypes(messages []types.Message) (json.RawMessage, []anthropicMessage) {
	var systemRaw json.RawMessage
	wire := make([]anthropicMessage, 0, len(messages))

	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		role := strings.TrimSpace(msg.Role)

		switch role {
		case "system":
			systemRaw = buildAnthropicSystemRaw(msg)

		case "assistant":
			if len(msg.ContentBlocks) > 0 {
				wire = append(wire, anthropicMessage{
					Role:    "assistant",
					Content: contentBlocksToAnthropicBlocks(msg.ContentBlocks),
				})
			} else if len(msg.ToolCalls) > 0 {
				// Cap is len(ToolCalls) + (1 if Content is non-empty);
				// pre-allocate with the tool-calls count and let append
				// grow the slice once if Content adds a leading text
				// block. Computing `len(...) + 1` directly inside `make`
				// is flagged by static analysis (CodeQL CWE-190) as a
				// theoretical overflow risk; sidestepping the math here
				// keeps the allocation deterministically bounded and
				// the analyzer happy without a runtime guard.
				blocks := make([]anthropicContentBlock, 0, len(msg.ToolCalls))
				if msg.Content != "" {
					blocks = append(blocks, anthropicContentBlock{Type: "text", Text: msg.Content})
				}
				for _, tc := range msg.ToolCalls {
					input := json.RawMessage(tc.Function.Arguments)
					if !json.Valid(input) {
						input = json.RawMessage(`{}`)
					}
					blocks = append(blocks, anthropicContentBlock{
						Type:  "tool_use",
						ID:    tc.ID,
						Name:  tc.Function.Name,
						Input: input,
					})
				}
				wire = append(wire, anthropicMessage{Role: "assistant", Content: blocks})
			} else {
				wire = append(wire, anthropicMessage{
					Role:    "assistant",
					Content: []anthropicContentBlock{{Type: "text", Text: msg.Content}},
				})
			}

		case "tool":
			// Batch consecutive tool-result messages into a single user message.
			blocks := []anthropicContentBlock{toolResultBlock(msg)}
			for i+1 < len(messages) && strings.TrimSpace(messages[i+1].Role) == "tool" {
				i++
				blocks = append(blocks, toolResultBlock(messages[i]))
			}
			wire = append(wire, anthropicMessage{Role: "user", Content: blocks})

		case "user":
			if len(msg.ContentBlocks) > 0 {
				wire = append(wire, anthropicMessage{
					Role:    "user",
					Content: contentBlocksToAnthropicBlocks(msg.ContentBlocks),
				})
			} else {
				wire = append(wire, anthropicMessage{
					Role:    "user",
					Content: []anthropicContentBlock{{Type: "text", Text: msg.Content}},
				})
			}
		}
	}
	return systemRaw, wire
}

// buildAnthropicSystemRaw marshals the system message into the Anthropic wire form:
// - a plain JSON string when there is a single un-cached text block
// - a JSON array of text blocks (with optional cache_control) otherwise
func buildAnthropicSystemRaw(msg types.Message) json.RawMessage {
	if len(msg.ContentBlocks) == 0 {
		if text := strings.TrimSpace(msg.Content); text != "" {
			b, _ := json.Marshal(text)
			return b
		}
		return nil
	}
	// Check whether any block has cache_control — if not and there is only one
	// text block, send a plain string (avoids unnecessary array wrapping).
	hasCacheControl := false
	for _, cb := range msg.ContentBlocks {
		if len(cb.CacheControl) > 0 {
			hasCacheControl = true
			break
		}
	}
	if !hasCacheControl && len(msg.ContentBlocks) == 1 && msg.ContentBlocks[0].Type == "text" {
		b, _ := json.Marshal(msg.ContentBlocks[0].Text)
		return b
	}
	type sysBlock struct {
		Type         string          `json:"type"`
		Text         string          `json:"text"`
		CacheControl json.RawMessage `json:"cache_control,omitempty"`
	}
	blocks := make([]sysBlock, 0, len(msg.ContentBlocks))
	for _, cb := range msg.ContentBlocks {
		if cb.Type == "" || cb.Type == "text" {
			blocks = append(blocks, sysBlock{
				Type:         "text",
				Text:         cb.Text,
				CacheControl: cb.CacheControl,
			})
		}
	}
	if len(blocks) == 0 {
		return nil
	}
	b, _ := json.Marshal(blocks)
	return b
}

// ephemeralCacheControl is the canonical Anthropic prompt-cache
// marker. Anthropic's Messages API treats every block bearing this
// tag as a cacheable boundary: subsequent requests that share the
// same prefix up to (and including) the marker are served from the
// cache for ~10% of the fresh-input rate. The "ephemeral" type is
// the only one Anthropic currently exposes — `1h` durations and
// other variants would change this constant if they ship.
//
// See: https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching
var ephemeralCacheControl = json.RawMessage(`{"type":"ephemeral"}`)

// applyAnthropicSystemCacheMarker attaches the ephemeral cache_control
// marker to the LAST entry of the system section so Anthropic caches
// the static system prefix. It handles both wire forms:
//
//   - a JSON string: converted into a single-element block list with
//     the marker attached. Cache_control requires the block-list
//     shape; Anthropic accepts either form for system, but only
//     blocks can carry the marker.
//   - a JSON array of blocks: the marker is attached to the LAST
//     entry. If the caller already supplied a cache_control on that
//     entry we leave it alone — caller intent wins (e.g. an
//     orchestrator that wants the marker earlier in the array to
//     cache a different boundary).
//
// Returns the original input unchanged when systemRaw is empty or
// when it already has a marker on its tail block. Errors during
// re-marshal fall through to returning the original — the goal is
// "best-effort cache hint", never to fail the request.
//
// TODO(memory): once the agent-memory RFC (docs/rfcs/agent-memory.md)
// lands and memory blocks are injected into the system prompt as a
// dedicated content block, the ideal cache boundary becomes the LAST
// memory block rather than the LAST system block — the static
// instructions sit before memory, and memory churns per-session. At
// that point this helper should locate the memory boundary and
// attach the marker there instead of (or in addition to) the system
// tail.
func applyAnthropicSystemCacheMarker(systemRaw json.RawMessage) json.RawMessage {
	if len(systemRaw) == 0 {
		return systemRaw
	}
	type sysBlock struct {
		Type         string          `json:"type"`
		Text         string          `json:"text"`
		CacheControl json.RawMessage `json:"cache_control,omitempty"`
	}
	// String form: wrap into a single-block list with the marker.
	if len(systemRaw) > 0 && systemRaw[0] == '"' {
		var text string
		if err := json.Unmarshal(systemRaw, &text); err != nil {
			return systemRaw
		}
		if strings.TrimSpace(text) == "" {
			return systemRaw
		}
		b, err := json.Marshal([]sysBlock{{
			Type:         "text",
			Text:         text,
			CacheControl: ephemeralCacheControl,
		}})
		if err != nil {
			return systemRaw
		}
		return b
	}
	// Array form: attach to the last entry only if it doesn't already
	// have a cache_control. We round-trip via []sysBlock — buildAnthropicSystemRaw
	// only emits text blocks today, so this is shape-preserving.
	var blocks []sysBlock
	if err := json.Unmarshal(systemRaw, &blocks); err != nil {
		return systemRaw
	}
	if len(blocks) == 0 {
		return systemRaw
	}
	if len(blocks[len(blocks)-1].CacheControl) > 0 {
		return systemRaw
	}
	blocks[len(blocks)-1].CacheControl = ephemeralCacheControl
	b, err := json.Marshal(blocks)
	if err != nil {
		return systemRaw
	}
	return b
}

// applyAnthropicToolsCacheMarker attaches the ephemeral cache_control
// marker to the LAST tool entry so Anthropic caches the (typically
// large and stable) tool catalog alongside the system prefix. The
// marker is set in place; if the caller already supplied a
// CacheControl on the tail tool we leave it untouched — same
// "caller intent wins" rule as the system helper.
//
// Tools mutates in place. Callers that need to keep the original
// slice should pass a copy.
func applyAnthropicToolsCacheMarker(tools []anthropicTool) {
	if len(tools) == 0 {
		return
	}
	last := &tools[len(tools)-1]
	if len(last.CacheControl) > 0 {
		return
	}
	last.CacheControl = ephemeralCacheControl
}

// contentBlocksToAnthropicBlocks converts types.ContentBlock slice to the provider wire type.
func contentBlocksToAnthropicBlocks(cbs []types.ContentBlock) []anthropicContentBlock {
	out := make([]anthropicContentBlock, 0, len(cbs))
	for _, cb := range cbs {
		switch cb.Type {
		case "text", "":
			out = append(out, anthropicContentBlock{
				Type:         "text",
				Text:         cb.Text,
				CacheControl: cb.CacheControl,
			})
		case "tool_use":
			input := cb.Input
			if !json.Valid(input) || len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			out = append(out, anthropicContentBlock{
				Type:         "tool_use",
				ID:           cb.ID,
				Name:         cb.Name,
				Input:        input,
				CacheControl: cb.CacheControl,
			})
		case "thinking":
			out = append(out, anthropicContentBlock{
				Type:      "thinking",
				Thinking:  cb.Thinking,
				Signature: cb.Signature,
			})
		case "redacted_thinking":
			out = append(out, anthropicContentBlock{
				Type: "redacted_thinking",
				Data: cb.Data,
			})
		case "image_url", "image":
			// Translate OpenAI's image_url shape (and the
			// internal `image` shape used by the Anthropic-
			// inbound path) into Anthropic's image block on the
			// wire. The internal ContentImage struct unifies
			// both upstreams' source formats — we just pick
			// between url and base64 based on which field is
			// populated.
			if source := buildAnthropicImageSource(cb.Image); source != nil {
				out = append(out, anthropicContentBlock{
					Type:         "image",
					Source:       source,
					CacheControl: cb.CacheControl,
				})
			}
			// If Image is nil or unrenderable, drop the block
			// silently — sending an image type with no source
			// would 400 the upstream.
		// tool_result is handled via the "tool" role path, not content blocks
		default:
			// pass unknown block types through verbatim so they reach the upstream
			out = append(out, anthropicContentBlock{
				Type:         cb.Type,
				CacheControl: cb.CacheControl,
			})
		}
	}
	return out
}

// buildAnthropicImageSource maps the unified ContentImage shape to
// Anthropic's image source object. Returns nil when the source has
// neither a URL nor base64 data — caller drops the block in that
// case (sending an image with no source would 400 the upstream).
//
// Two output shapes:
//   - {type: "url", url: "https://..."} — for url-referenced images.
//     If the URL is a data: URI, we parse it into the base64 form
//     instead so older Anthropic API versions (which only accepted
//     base64) keep working.
//   - {type: "base64", media_type: "image/png", data: "..."}
//     — for inline base64 content.
func buildAnthropicImageSource(img *types.ContentImage) json.RawMessage {
	if img == nil {
		return nil
	}
	// Inline base64 takes precedence when both fields are set —
	// it's the more specific representation.
	if img.Data != "" {
		mediaType := img.MediaType
		if mediaType == "" {
			// Anthropic requires a media_type. Default to
			// image/png when the caller didn't set one — the
			// upstream rejects with a clear error if the
			// payload doesn't actually match, so a wrong
			// default is at worst an actionable error rather
			// than silent corruption.
			mediaType = "image/png"
		}
		raw, err := json.Marshal(map[string]any{
			"type":       "base64",
			"media_type": mediaType,
			"data":       img.Data,
		})
		if err != nil {
			return nil
		}
		return raw
	}
	if img.URL != "" {
		// Data URIs (data:image/png;base64,...) are common in
		// OpenAI flows. Convert to Anthropic's base64 form
		// inline so the upstream doesn't have to fetch a
		// pseudo-URL.
		if strings.HasPrefix(img.URL, "data:") {
			mediaType, data := parseDataURI(img.URL)
			if data != "" {
				if mediaType == "" {
					mediaType = "image/png"
				}
				raw, err := json.Marshal(map[string]any{
					"type":       "base64",
					"media_type": mediaType,
					"data":       data,
				})
				if err == nil {
					return raw
				}
			}
		}
		raw, err := json.Marshal(map[string]any{
			"type": "url",
			"url":  img.URL,
		})
		if err != nil {
			return nil
		}
		return raw
	}
	return nil
}

// parseDataURI extracts media-type and base64 payload from a
// `data:image/png;base64,iVBOR...` URI. Returns empty strings on
// any malformed input — the caller falls back to passing the URL
// through verbatim, which lets newer Anthropic API versions that
// accept data URIs handle it themselves.
func parseDataURI(uri string) (mediaType, data string) {
	if !strings.HasPrefix(uri, "data:") {
		return "", ""
	}
	rest := uri[len("data:"):]
	commaIdx := strings.IndexByte(rest, ',')
	if commaIdx < 0 {
		return "", ""
	}
	meta := rest[:commaIdx]
	payload := rest[commaIdx+1:]
	// meta is "image/png;base64" or "text/plain;charset=utf-8".
	// We only support base64 for image data.
	parts := strings.Split(meta, ";")
	if len(parts) == 0 {
		return "", ""
	}
	mediaType = parts[0]
	hasBase64 := false
	for _, p := range parts[1:] {
		if strings.EqualFold(p, "base64") {
			hasBase64 = true
		}
	}
	if !hasBase64 {
		return "", ""
	}
	return mediaType, payload
}

// toolResultBlock converts a tool-role message into a tool_result content block.
//
// Picks the content shape based on the source:
//   - If the message has ContentBlocks (e.g. inbound /v1/messages
//     callers passed a structured array, possibly with images or
//     cache_control), emit those as a JSON array — Anthropic accepts
//     a heterogeneous content array on tool_results.
//   - Otherwise emit msg.Content as a JSON string. The historical
//     wire shape; agent-loop tool dispatches go through this branch.
//
// is_error is forwarded from msg.ToolError so the model gets a
// proper failure signal instead of having to infer it from text.
func toolResultBlock(msg types.Message) anthropicContentBlock {
	block := anthropicContentBlock{
		Type:      "tool_result",
		ToolUseID: msg.ToolCallID,
		IsError:   msg.ToolError,
	}
	if len(msg.ContentBlocks) > 0 {
		// Re-serialize the source blocks into the shape Anthropic
		// expects on tool_result content. We pass through text and
		// image blocks (the two the API accepts here); other types
		// are dropped to avoid confusing the upstream parser.
		type nested struct {
			Type         string          `json:"type"`
			Text         string          `json:"text,omitempty"`
			Source       json.RawMessage `json:"source,omitempty"`
			CacheControl json.RawMessage `json:"cache_control,omitempty"`
		}
		nestedBlocks := make([]nested, 0, len(msg.ContentBlocks))
		for _, cb := range msg.ContentBlocks {
			switch cb.Type {
			case "", "text":
				nestedBlocks = append(nestedBlocks, nested{
					Type:         "text",
					Text:         cb.Text,
					CacheControl: cb.CacheControl,
				})
			case "image":
				// Image blocks carry their `source` as a json.RawMessage
				// in the gateway's pass-through model. Preserve as-is.
				nestedBlocks = append(nestedBlocks, nested{
					Type:         "image",
					Source:       cb.Input, // image source is reused under the Input field in the gateway model
					CacheControl: cb.CacheControl,
				})
			}
		}
		if len(nestedBlocks) > 0 {
			if raw, err := json.Marshal(nestedBlocks); err == nil {
				block.ResultContent = raw
				return block
			}
		}
	}
	if msg.Content != "" {
		raw, _ := json.Marshal(msg.Content)
		block.ResultContent = raw
	}
	return block
}

func anthropicResponseToMessage(blocks []anthropicContentBlock) types.Message {
	msg := types.Message{Role: "assistant"}
	textParts := make([]string, 0)
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				textParts = append(textParts, t)
			}
			msg.ContentBlocks = append(msg.ContentBlocks, types.ContentBlock{
				Type: "text",
				Text: b.Text,
			})
		case "thinking":
			msg.ContentBlocks = append(msg.ContentBlocks, types.ContentBlock{
				Type:      "thinking",
				Thinking:  b.Thinking,
				Signature: b.Signature,
			})
		case "redacted_thinking":
			msg.ContentBlocks = append(msg.ContentBlocks, types.ContentBlock{
				Type: "redacted_thinking",
				Data: b.Data,
			})
		case "tool_use":
			args := string(b.Input)
			if args == "" {
				args = "{}"
			}
			msg.ToolCalls = append(msg.ToolCalls, types.ToolCall{
				ID:   b.ID,
				Type: "function",
				Function: types.ToolCallFunction{
					Name:      b.Name,
					Arguments: args,
				},
			})
			msg.ContentBlocks = append(msg.ContentBlocks, types.ContentBlock{
				Type:  "tool_use",
				ID:    b.ID,
				Name:  b.Name,
				Input: b.Input,
			})
		}
	}
	msg.Content = strings.Join(textParts, "\n")
	return msg
}

func anthropicToolChoice(choice json.RawMessage) json.RawMessage {
	if len(choice) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(choice, &s) == nil {
		switch s {
		case "auto":
			return json.RawMessage(`{"type":"auto"}`)
		case "none":
			return nil
		case "required":
			return json.RawMessage(`{"type":"any"}`)
		}
		return nil
	}
	var obj struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(choice, &obj) == nil && obj.Type == "function" && obj.Function.Name != "" {
		b, _ := json.Marshal(map[string]string{"type": "tool", "name": obj.Function.Name})
		return b
	}
	return nil
}

func (p *AnthropicProvider) ChatStream(ctx context.Context, req types.ChatRequest, w io.Writer) error {
	if err := p.Validate(); err != nil {
		return err
	}

	systemRaw, messages := anthropicMessagesFromTypes(req.Messages)
	if len(messages) == 0 {
		return fmt.Errorf("anthropic messages request requires at least one non-system message")
	}
	wireReq := anthropicMessagesRequest{
		Model:         req.Model,
		System:        systemRaw,
		Messages:      messages,
		MaxTokens:     req.MaxTokens,
		TopP:          req.TopP,
		TopK:          req.TopK,
		StopSequences: append([]string(nil), req.StopSequences...),
		Stream:        true,
	}
	if wireReq.MaxTokens <= 0 {
		wireReq.MaxTokens = 1024
	}
	if req.Temperature > 0 {
		wireReq.Temperature = req.Temperature
	}
	if len(req.Thinking) > 0 {
		wireReq.Thinking = req.Thinking
	}
	if strings.TrimSpace(req.ServiceTier) != "" {
		wireReq.ServiceTier = strings.TrimSpace(req.ServiceTier)
	}
	p.warnResponseFormatDropped(req)
	if len(req.Tools) > 0 {
		wireReq.Tools = make([]anthropicTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			schema := t.Function.Parameters
			if len(schema) == 0 {
				schema = json.RawMessage(`{}`)
			}
			wireReq.Tools = append(wireReq.Tools, anthropicTool{
				Name:         t.Function.Name,
				Description:  t.Function.Description,
				InputSchema:  schema,
				CacheControl: t.CacheControl,
			})
		}
		wireReq.ToolChoice = anthropicToolChoice(req.ToolChoice)
	}
	if !p.config.AnthropicCacheDisabled {
		wireReq.System = applyAnthropicSystemCacheMarker(wireReq.System)
		applyAnthropicToolsCacheMarker(wireReq.Tools)
	}

	payload, err := json.Marshal(wireReq)
	if err != nil {
		return fmt.Errorf("marshal upstream request: %w", err)
	}
	endpoint := strings.TrimRight(p.config.BaseURL, "/") + "/v1/messages"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build upstream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	p.applyHeaders(httpReq)
	if len(req.Betas) > 0 {
		httpReq.Header.Set("anthropic-beta", strings.Join(req.Betas, ","))
	}
	injectTraceContext(httpReq)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("send upstream request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return decodeAnthropicError(resp)
	}

	return translateAnthropicSSE(ctx, req.Model, resp.Body, w)
}

// translateAnthropicSSE reads Anthropic SSE events and writes OpenAI-format SSE chunks.
func translateAnthropicSSE(ctx context.Context, model string, src io.Reader, dst io.Writer) error {
	type anthropicStreamEvent struct {
		Type  string          `json:"type"`
		Index int             `json:"index"`
		Delta json.RawMessage `json:"delta"`
		// message_start carries the initial usage, including
		// input_tokens AND the cache buckets when prompt caching
		// is in use. The prior adapter only captured ID/model and
		// dropped the usage entirely, so streamed responses
		// reported zero prompt tokens and never billed cache
		// reads/writes. Capture the full shape now so the final
		// usage chunk we emit downstream matches the non-stream
		// Chat() path.
		Message *struct {
			ID    string         `json:"id"`
			Model string         `json:"model"`
			Usage anthropicUsage `json:"usage"`
		} `json:"message"`
		// content_block_start
		ContentBlock *struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
		// message_delta usage — Anthropic re-sends the running
		// totals here as output progresses; the final value (at
		// the message_delta with stop_reason set) is the
		// authoritative count.
		Usage *anthropicUsage `json:"usage"`
	}

	type deltaPayload struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	}

	var (
		completionID string
		// track open tool_use blocks by index
		toolBlocks = make(map[int]struct{ id, name string })
		// track open thinking blocks by index (value = true once opened)
		thinkingBlocks = make(map[int]bool)
		// usageSnapshot accumulates token counts seen across
		// message_start (initial input + cache buckets) and the
		// running message_delta usage frames (output tokens). We
		// emit it on the final usage chunk so downstream cost
		// accounting sees the same shape as the non-stream path.
		usageSnapshot anthropicUsage
	)

	writeChunk := func(data any) error {
		b, err := json.Marshal(data)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(dst, "data: %s\n\n", b); err != nil {
			return err
		}
		if f, ok := dst.(interface{ Flush() }); ok {
			f.Flush()
		}
		return nil
	}

	scanner := bufio.NewScanner(src)
	var eventType string
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		rawData := strings.TrimPrefix(line, "data: ")

		var ev anthropicStreamEvent
		if err := json.Unmarshal([]byte(rawData), &ev); err != nil {
			continue
		}

		switch eventType {
		case "message_start":
			if ev.Message != nil {
				completionID = ev.Message.ID
				if ev.Message.Model != "" {
					model = ev.Message.Model
				}
				// Capture the initial usage snapshot. Anthropic's
				// message_start has the final input_tokens + cache
				// counts already populated; output_tokens is zero
				// at this point and will grow via message_delta.
				usageSnapshot.InputTokens = ev.Message.Usage.InputTokens
				usageSnapshot.CacheReadInputTokens = ev.Message.Usage.CacheReadInputTokens
				usageSnapshot.CacheCreationInputTokens = ev.Message.Usage.CacheCreationInputTokens
			}
			// Send role delta
			if err := writeChunk(map[string]any{
				"id":      completionID,
				"object":  "chat.completion.chunk",
				"created": 0,
				"model":   model,
				"choices": []map[string]any{{
					"index":         0,
					"delta":         map[string]any{"role": "assistant", "content": ""},
					"finish_reason": nil,
				}},
			}); err != nil {
				return err
			}

		case "content_block_start":
			if ev.ContentBlock != nil && ev.ContentBlock.Type == "thinking" {
				thinkingBlocks[ev.Index] = true
				// No OpenAI equivalent for thinking_start — the thinking content
				// is forwarded as x_thinking extension deltas below.
			}
			if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
				toolBlocks[ev.Index] = struct{ id, name string }{ev.ContentBlock.ID, ev.ContentBlock.Name}
				if err := writeChunk(map[string]any{
					"id":      completionID,
					"object":  "chat.completion.chunk",
					"created": 0,
					"model":   model,
					"choices": []map[string]any{{
						"index": 0,
						"delta": map[string]any{
							"tool_calls": []map[string]any{{
								"index": ev.Index,
								"id":    ev.ContentBlock.ID,
								"type":  "function",
								"function": map[string]any{
									"name":      ev.ContentBlock.Name,
									"arguments": "",
								},
							}},
						},
						"finish_reason": nil,
					}},
				}); err != nil {
					return err
				}
			}

		case "content_block_delta":
			var delta deltaPayload
			if err := json.Unmarshal(ev.Delta, &delta); err != nil {
				continue
			}
			switch delta.Type {
			case "thinking_delta":
				if thinkingBlocks[ev.Index] {
					if err := writeChunk(map[string]any{
						"id":      completionID,
						"object":  "chat.completion.chunk",
						"created": 0,
						"model":   model,
						"choices": []map[string]any{{
							"index":         0,
							"delta":         map[string]any{"x_thinking": delta.Thinking},
							"finish_reason": nil,
						}},
					}); err != nil {
						return err
					}
				}
			case "signature_delta":
				if thinkingBlocks[ev.Index] {
					if err := writeChunk(map[string]any{
						"id":      completionID,
						"object":  "chat.completion.chunk",
						"created": 0,
						"model":   model,
						"choices": []map[string]any{{
							"index":         0,
							"delta":         map[string]any{"x_thinking_signature": delta.Signature},
							"finish_reason": nil,
						}},
					}); err != nil {
						return err
					}
				}
			case "text_delta":
				if err := writeChunk(map[string]any{
					"id":      completionID,
					"object":  "chat.completion.chunk",
					"created": 0,
					"model":   model,
					"choices": []map[string]any{{
						"index":         0,
						"delta":         map[string]any{"content": delta.Text},
						"finish_reason": nil,
					}},
				}); err != nil {
					return err
				}
			case "input_json_delta":
				if _, ok := toolBlocks[ev.Index]; ok {
					if err := writeChunk(map[string]any{
						"id":      completionID,
						"object":  "chat.completion.chunk",
						"created": 0,
						"model":   model,
						"choices": []map[string]any{{
							"index": 0,
							"delta": map[string]any{
								"tool_calls": []map[string]any{{
									"index":    ev.Index,
									"function": map[string]any{"arguments": delta.PartialJSON},
								}},
							},
							"finish_reason": nil,
						}},
					}); err != nil {
						return err
					}
				}
			}

		case "message_delta":
			var delta deltaPayload
			if err := json.Unmarshal(ev.Delta, &delta); err != nil {
				continue
			}
			finishReason := delta.StopReason
			if finishReason == "tool_use" {
				finishReason = "tool_calls"
			}
			if finishReason == "" {
				finishReason = "stop"
			}
			// Anthropic re-sends the latest output_tokens count on
			// every message_delta. The cache buckets are stable
			// across the run (they're determined at message_start),
			// so we keep what we captured earlier and only update
			// output_tokens here.
			if ev.Usage != nil {
				usageSnapshot.OutputTokens = ev.Usage.OutputTokens
			}
			// Translate to OpenAI's flat usage shape. PromptTokens
			// folds in cache writes (see anthropicUsageToTypes
			// docs); cache reads ride alongside as
			// prompt_tokens_details.cached_tokens, the same key
			// OpenAI uses, so a downstream that already knows
			// the OpenAI prompt-cache shape sees a familiar
			// payload.
			normalized := anthropicUsageToTypes(usageSnapshot)
			usage := map[string]any{
				"prompt_tokens":     normalized.PromptTokens,
				"completion_tokens": normalized.CompletionTokens,
				"total_tokens":      normalized.TotalTokens,
			}
			if normalized.CachedPromptTokens > 0 {
				usage["prompt_tokens_details"] = map[string]any{
					"cached_tokens": normalized.CachedPromptTokens,
				}
			}
			if err := writeChunk(map[string]any{
				"id":      completionID,
				"object":  "chat.completion.chunk",
				"created": 0,
				"model":   model,
				"choices": []map[string]any{{
					"index":         0,
					"delta":         map[string]any{},
					"finish_reason": finishReason,
				}},
				"usage": usage,
			}); err != nil {
				return err
			}

		case "message_stop":
			fmt.Fprintf(dst, "data: [DONE]\n\n") //nolint:errcheck
			if f, ok := dst.(interface{ Flush() }); ok {
				f.Flush()
			}
			return nil
		}
	}

	// Prefer the context error when the scanner stopped due to an I/O error
	// caused by context cancellation (Go HTTP transport closes the response
	// body when the request context is done).
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	// Send DONE if message_stop was never seen
	fmt.Fprintf(dst, "data: [DONE]\n\n") //nolint:errcheck
	if f, ok := dst.(interface{ Flush() }); ok {
		f.Flush()
	}
	return nil
}

func decodeAnthropicError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var envelope anthropicErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err == nil && strings.TrimSpace(envelope.Error.Message) != "" {
		return &UpstreamError{
			StatusCode: resp.StatusCode,
			Message:    envelope.Error.Message,
			Type:       envelope.Error.Type,
		}
	}
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = resp.Status
	}
	return &UpstreamError{
		StatusCode: resp.StatusCode,
		Message:    message,
		Type:       "anthropic_error",
	}
}
