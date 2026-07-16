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
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/prompttokens"
	"github.com/hecatehq/hecate/internal/safetext"
	"github.com/hecatehq/hecate/internal/sse"
	"github.com/hecatehq/hecate/pkg/types"
)

type OpenAICompatibleProvider struct {
	config     config.OpenAICompatibleProviderConfig
	logger     *slog.Logger
	httpClient *http.Client
	mu         sync.Mutex
	cachedCaps Capabilities
	capsExpiry time.Time
	capsFlight *capabilityDiscoveryCall
}

const (
	ollamaCapabilityDiscoveryConcurrency = 4
	ollamaCapabilityDiscoveryMaxModels   = 32
	ollamaCapabilityDiscoveryMinTimeout  = 500 * time.Millisecond
	ollamaCapabilityDiscoveryMaxTimeout  = 3 * time.Second

	fireworksModelDiscoveryPageSize = 200
	fireworksModelDiscoveryMaxPages = 20
)

type openAIChatCompletionRequest struct {
	Model       string              `json:"model"`
	Messages    []openAIChatMessage `json:"messages"`
	MaxTokens   int                 `json:"max_tokens,omitempty"`
	Temperature float64             `json:"temperature,omitempty"`
	TopP        float64             `json:"top_p,omitempty"`
	Stop        []string            `json:"stop,omitempty"`
	User        string              `json:"user,omitempty"`
	Tools       []openAITool        `json:"tools,omitempty"`
	ToolChoice  json.RawMessage     `json:"tool_choice,omitempty"`
	Stream      bool                `json:"stream,omitempty"`
	// ResponseFormat is the OpenAI structured-output knob, passed
	// through verbatim. Most OpenAI-compat upstreams (real OpenAI,
	// Together, Groq, vLLM with hermes/grammar) honor it; Ollama
	// ignores unknown fields silently.
	ResponseFormat json.RawMessage `json:"response_format,omitempty"`
	// Tier-2 passthroughs. Each rides the wire verbatim when the
	// caller set it; default-zero fields are dropped via omitempty
	// so we never accidentally enable a knob the caller didn't
	// ask for.
	Seed              *int            `json:"seed,omitempty"`
	PresencePenalty   float64         `json:"presence_penalty,omitempty"`
	FrequencyPenalty  float64         `json:"frequency_penalty,omitempty"`
	Logprobs          bool            `json:"logprobs,omitempty"`
	TopLogprobs       int             `json:"top_logprobs,omitempty"`
	LogitBias         json.RawMessage `json:"logit_bias,omitempty"`
	StreamOptions     json.RawMessage `json:"stream_options,omitempty"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls,omitempty"`
}

type openAITool struct {
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

type openAIToolCall struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function openAIToolCallFunction `json:"function"`
}

type openAIToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIChatMessage struct {
	Role string `json:"role"`
	// Content accepts string, array of blocks, or null. Mirrors
	// the inbound OpenAIMessageContent shape; defined here in the
	// provider package so the outbound wire encoding stays
	// dependency-free of the API package.
	Content    openAIMessageContent `json:"content"`
	Name       string               `json:"name,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
	ToolCalls  []openAIToolCall     `json:"tool_calls,omitempty"`
}

// openAIMessageContent is the provider-side polymorphic content
// value. Same shape as api.OpenAIMessageContent; duplicated here
// to keep the providers package free of api-package imports.
type openAIMessageContent struct {
	Text   string
	Blocks []openAIContentBlock
	Null   bool
}

func (c *openAIMessageContent) UnmarshalJSON(data []byte) error {
	*c = openAIMessageContent{}
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

func (c openAIMessageContent) MarshalJSON() ([]byte, error) {
	if c.Null {
		return []byte("null"), nil
	}
	if len(c.Blocks) > 0 {
		return json.Marshal(c.Blocks)
	}
	return json.Marshal(c.Text)
}

// AsString flattens block content for code paths (token estimation,
// response decoding) that want a single string.
func (c openAIMessageContent) AsString() string {
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

// openAIContentBlock is one element of the array form of message
// content. Today: text + image_url; the struct's open shape lets
// future block types pass through.
type openAIContentBlock struct {
	Type     string                 `json:"type"`
	Text     string                 `json:"text,omitempty"`
	ImageURL *openAIContentImageURL `json:"image_url,omitempty"`
}

type openAIContentImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type openAIChatCompletionResponse struct {
	ID      string                       `json:"id"`
	Created int64                        `json:"created"`
	Model   string                       `json:"model"`
	Choices []openAIChatCompletionChoice `json:"choices"`
	Usage   openAIUsage                  `json:"usage"`
}

type openAIChatCompletionChoice struct {
	Index        int               `json:"index"`
	Message      openAIChatMessage `json:"message"`
	FinishReason string            `json:"finish_reason"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// PromptTokensDetails carries the cache-token breakdown OpenAI
	// (and OpenAI-compat providers like Together / Groq) return
	// when prompt caching is in play. We pull `cached_tokens` into
	// internal Usage.CachedPromptTokens so Hecate can report cached
	// input separately from fresh prompt tokens.
	PromptTokensDetails *openAIPromptTokensDetails `json:"prompt_tokens_details,omitempty"`
}

type openAIPromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

type openAIErrorEnvelope struct {
	Error openAIErrorDetail `json:"error"`
}

type openAIErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

type openAIModelsResponse struct {
	Data []openAIModel `json:"data"`
}

type openAIModel struct {
	ID string `json:"id"`
}

type fireworksModelsResponse struct {
	Models           []fireworksModel `json:"models"`
	Data             []fireworksModel `json:"data"`
	NextPageToken    string           `json:"nextPageToken"`
	NextPageTokenAlt string           `json:"next_page_token"`
}

func (r fireworksModelsResponse) items() []fireworksModel {
	if len(r.Models) > 0 {
		return r.Models
	}
	return r.Data
}

func (r fireworksModelsResponse) nextPageToken() string {
	if token := strings.TrimSpace(r.NextPageToken); token != "" {
		return token
	}
	return strings.TrimSpace(r.NextPageTokenAlt)
}

type fireworksModel struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	DisplayName        string `json:"displayName"`
	SupportsTools      bool   `json:"supportsTools"`
	SupportsImageInput bool   `json:"supportsImageInput"`
	ContextLength      int    `json:"contextLength"`
}

type lmStudioModelsResponse struct {
	Models []lmStudioModel `json:"models"`
}

type lmStudioModel struct {
	ID               string `json:"id"`
	Key              string `json:"key"`
	Type             string `json:"type"`
	MaxContextLength int    `json:"max_context_length"`
	Capabilities     struct {
		TrainedForToolUse bool `json:"trained_for_tool_use"`
	} `json:"capabilities"`
}

type ollamaShowRequest struct {
	Model string `json:"model"`
}

type ollamaShowResponse struct {
	Capabilities []string `json:"capabilities"`
}

type UpstreamError struct {
	StatusCode int
	Message    string
	Type       string
}

func (e *UpstreamError) Error() string {
	if e == nil {
		return ""
	}
	message := safetext.SanitizeErrorMessage(e.Message)
	errorType := safetext.SanitizeErrorType(e.Type, "")
	if errorType == "" {
		return fmt.Sprintf("upstream error (%d): %s", e.StatusCode, message)
	}
	return fmt.Sprintf("upstream error (%d/%s): %s", e.StatusCode, errorType, message)
}

func NewOpenAICompatibleProvider(cfg config.OpenAICompatibleProviderConfig, logger *slog.Logger) *OpenAICompatibleProvider {
	return &OpenAICompatibleProvider{
		config: cfg,
		logger: logger,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

func NewOpenAIProvider(cfg config.OpenAICompatibleProviderConfig, logger *slog.Logger) *OpenAICompatibleProvider {
	return NewOpenAICompatibleProvider(cfg, logger)
}

func (p *OpenAICompatibleProvider) Name() string {
	return p.config.Name
}

func (p *OpenAICompatibleProvider) ProviderInstanceIdentity() types.ProviderInstanceIdentity {
	return configurationProviderInstanceIdentity(p.config)
}

func (p *OpenAICompatibleProvider) Aliases() []string {
	return append([]string(nil), p.config.Aliases...)
}

func (p *OpenAICompatibleProvider) CapabilityFamily() string {
	return configuredCapabilityFamily(p.config)
}

func (p *OpenAICompatibleProvider) Enabled() bool {
	return p.config.Enabled
}

func (p *OpenAICompatibleProvider) BaseURL() string {
	return p.config.BaseURL
}

func (p *OpenAICompatibleProvider) CredentialState() CredentialState {
	if p.Kind() == KindLocal || p.config.StubMode {
		return CredentialStateNotRequired
	}
	if strings.TrimSpace(p.config.APIKey) == "" {
		return CredentialStateMissing
	}
	return CredentialStateConfigured
}

func (p *OpenAICompatibleProvider) Kind() Kind {
	if p.config.Kind == string(KindLocal) {
		return KindLocal
	}
	return KindCloud
}

func (p *OpenAICompatibleProvider) DefaultModel() string {
	return p.config.DefaultModel
}

func (p *OpenAICompatibleProvider) Capabilities(ctx context.Context) (Capabilities, error) {
	return p.capabilities(ctx, false)
}

func (p *OpenAICompatibleProvider) RefreshCapabilities(ctx context.Context) (Capabilities, error) {
	return p.capabilities(ctx, true)
}

func (p *OpenAICompatibleProvider) capabilities(ctx context.Context, refresh bool) (Capabilities, error) {
	if p.config.StubMode {
		return p.staticCapabilities("config"), nil
	}
	return resolveCapabilities(
		ctx,
		p.logger,
		p.Name(),
		p.Kind(),
		p.config.APIKey,
		refresh,
		&p.mu,
		&p.cachedCaps,
		&p.capsExpiry,
		&p.capsFlight,
		p.discoverCapabilities,
		p.staticCapabilities,
	)
}

func (p *OpenAICompatibleProvider) Supports(model string) bool {
	if model == "" {
		return p.config.DefaultModel != ""
	}
	return p.config.DefaultModel != "" && p.config.DefaultModel == model
}

func (p *OpenAICompatibleProvider) supportsResolvedModel(ctx context.Context, model string) bool {
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

func (p *OpenAICompatibleProvider) Chat(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	if !p.supportsResolvedModel(ctx, req.Model) {
		return nil, fmt.Errorf("model %q is not supported by provider %s", req.Model, p.Name())
	}

	if !p.config.StubMode {
		return p.chatUpstream(ctx, req)
	}

	content := p.config.StubResponse
	if last := lastUserMessage(req.Messages); last != "" {
		content = fmt.Sprintf("%s Echo: %s", p.config.StubResponse, last)
	}

	promptTokens := estimatePromptTokens(req.Messages)
	completionTokens := max(16, len(content)/4)
	now := time.Now().UTC()

	return &types.ChatResponse{
		ID:        "chatcmpl-stub",
		Model:     req.Model,
		CreatedAt: now,
		Choices: []types.ChatChoice{
			{
				Index: 0,
				Message: types.Message{
					Role:    "assistant",
					Content: content,
				},
				FinishReason: "stop",
			},
		},
		Usage: types.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
	}, nil
}

func (p *OpenAICompatibleProvider) staticCapabilities(source string) Capabilities {
	// KnownModels is an operator-supplied static override (only set via
	// PROVIDER_<NAME>_MODELS env). When empty — the common case — the picker
	// stays empty until /v1/models discovery succeeds; the runtime no longer
	// ships hard-coded model lists per built-in preset because they bit-rot
	// as upstream catalogs churn.
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

func (p *OpenAICompatibleProvider) discoverCapabilities(ctx context.Context) (Capabilities, error) {
	endpoint := buildModelsURL(p.config.BaseURL, p.config.ModelsPath)
	if p.isFireworksProvider() {
		return p.discoverFireworksCapabilities(ctx, endpoint)
	}
	if p.isLMStudioProvider() {
		caps, err := p.discoverLMStudioCapabilities(ctx)
		if err == nil && len(caps.Models) > 0 {
			return caps, nil
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Capabilities{}, fmt.Errorf("build models request: %w", err)
	}
	if p.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	}
	injectTraceContext(req)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return Capabilities{}, fmt.Errorf("send models request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return Capabilities{}, decodeUpstreamError(resp)
	}

	var payload openAIModelsResponse
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
		Name:              p.Name(),
		Kind:              p.Kind(),
		DefaultModel:      defaultModel,
		Models:            models,
		ModelCapabilities: p.discoverProviderModelCapabilities(ctx, models),
		Discoverable:      true,
		DiscoverySource:   "upstream_v1_models",
		RefreshedAt:       time.Now().UTC(),
	}, nil
}

func (p *OpenAICompatibleProvider) discoverLMStudioCapabilities(ctx context.Context) (Capabilities, error) {
	endpoint, err := lmStudioNativeModelsURL(p.config.BaseURL)
	if err != nil {
		return Capabilities{}, fmt.Errorf("build LM Studio models URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Capabilities{}, fmt.Errorf("build LM Studio models request: %w", err)
	}
	if p.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	}
	injectTraceContext(req)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return Capabilities{}, fmt.Errorf("send LM Studio models request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return Capabilities{}, decodeUpstreamError(resp)
	}

	var payload lmStudioModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Capabilities{}, fmt.Errorf("decode LM Studio models response: %w", err)
	}

	models := make([]string, 0, len(payload.Models))
	modelCapabilities := make(map[string]types.ModelCapabilities, len(payload.Models))
	for _, item := range payload.Models {
		if modelType := strings.TrimSpace(item.Type); modelType != "" && !strings.EqualFold(modelType, "llm") {
			continue
		}
		id := strings.TrimSpace(item.Key)
		if id == "" {
			id = strings.TrimSpace(item.ID)
		}
		if id == "" || contains(models, id) {
			continue
		}
		models = append(models, id)

		cap := types.ModelCapabilities{
			Streaming:      true,
			StreamingKnown: true,
			Source:         "provider",
		}
		if item.Capabilities.TrainedForToolUse {
			cap.ToolCalling = "basic"
		}
		if item.MaxContextLength > 0 {
			cap.MaxContextTokens = item.MaxContextLength
		}
		modelCapabilities[id] = cap
	}
	if len(modelCapabilities) == 0 {
		modelCapabilities = nil
	}

	defaultModel := p.config.DefaultModel
	if defaultModel == "" && len(models) > 0 {
		defaultModel = models[0]
	}

	return Capabilities{
		Name:              p.Name(),
		Kind:              p.Kind(),
		DefaultModel:      defaultModel,
		Models:            models,
		ModelCapabilities: modelCapabilities,
		Discoverable:      true,
		DiscoverySource:   "lmstudio_api_models",
		RefreshedAt:       time.Now().UTC(),
	}, nil
}

func (p *OpenAICompatibleProvider) discoverFireworksCapabilities(ctx context.Context, endpoint string) (Capabilities, error) {
	items := make([]fireworksModel, 0)
	pageToken := ""
	seenPageTokens := make(map[string]struct{})
	for page := 0; page < fireworksModelDiscoveryMaxPages; page++ {
		pageEndpoint, err := fireworksModelsPageURL(endpoint, pageToken)
		if err != nil {
			return Capabilities{}, fmt.Errorf("build Fireworks models page URL: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageEndpoint, nil)
		if err != nil {
			return Capabilities{}, fmt.Errorf("build Fireworks models request: %w", err)
		}
		if p.config.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
		}
		injectTraceContext(req)

		resp, err := p.httpClient.Do(req)
		if err != nil {
			return Capabilities{}, fmt.Errorf("send Fireworks models request: %w", err)
		}

		if resp.StatusCode >= http.StatusBadRequest {
			defer resp.Body.Close()
			return Capabilities{}, decodeUpstreamError(resp)
		}

		var payload fireworksModelsResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			resp.Body.Close()
			return Capabilities{}, fmt.Errorf("decode Fireworks models response: %w", err)
		}
		resp.Body.Close()

		items = append(items, payload.items()...)
		pageToken = payload.nextPageToken()
		if pageToken == "" {
			break
		}
		if _, seen := seenPageTokens[pageToken]; seen {
			break
		}
		seenPageTokens[pageToken] = struct{}{}
	}

	models := make([]string, 0, len(items))
	modelCapabilities := make(map[string]types.ModelCapabilities, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.Name)
		if id == "" {
			id = strings.TrimSpace(item.ID)
		}
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, id)

		cap := types.ModelCapabilities{
			Streaming:      true,
			StreamingKnown: true,
			Source:         "provider",
		}
		if item.SupportsTools {
			cap.ToolCalling = "basic"
		} else {
			cap.ToolCalling = "none"
		}
		if item.SupportsImageInput {
			cap.ImageInput = "supported"
		} else {
			cap.ImageInput = "none"
		}
		if item.ContextLength > 0 {
			cap.MaxContextTokens = item.ContextLength
		}
		if cap.ToolCalling != "" || cap.ImageInput != "" || cap.MaxContextTokens > 0 {
			modelCapabilities[id] = cap
		}
	}
	if len(modelCapabilities) == 0 {
		modelCapabilities = nil
	}

	defaultModel := p.config.DefaultModel
	if defaultModel == "" && len(models) > 0 {
		defaultModel = models[0]
	}

	return Capabilities{
		Name:              p.Name(),
		Kind:              p.Kind(),
		DefaultModel:      defaultModel,
		Models:            models,
		ModelCapabilities: modelCapabilities,
		Discoverable:      true,
		DiscoverySource:   "fireworks_models",
		RefreshedAt:       time.Now().UTC(),
	}, nil
}

func fireworksModelsPageURL(endpoint string, pageToken string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	query := u.Query()
	if query.Get("pageSize") == "" {
		query.Set("pageSize", strconv.Itoa(fireworksModelDiscoveryPageSize))
	}
	if pageToken != "" {
		query.Set("pageToken", pageToken)
	} else {
		query.Del("pageToken")
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func (p *OpenAICompatibleProvider) discoverProviderModelCapabilities(ctx context.Context, models []string) map[string]types.ModelCapabilities {
	if !p.isOllamaProvider() || len(models) == 0 {
		return nil
	}
	baseURL, err := ollamaNativeBaseURL(p.config.BaseURL)
	if err != nil {
		return nil
	}

	probeModels := models
	if len(probeModels) > ollamaCapabilityDiscoveryMaxModels {
		probeModels = probeModels[:ollamaCapabilityDiscoveryMaxModels]
	}
	probeCtx, cancel := context.WithTimeout(ctx, p.ollamaCapabilityDiscoveryTimeout())
	defer cancel()

	out := make(map[string]types.ModelCapabilities, len(probeModels))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, ollamaCapabilityDiscoveryConcurrency)

loop:
	for _, model := range probeModels {
		select {
		case <-probeCtx.Done():
			break loop
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(model string) {
			defer wg.Done()
			defer func() { <-sem }()

			cap, ok := p.discoverOllamaModelCapability(probeCtx, baseURL, model)
			if !ok {
				return
			}
			mu.Lock()
			out[model] = cap
			mu.Unlock()
		}(model)
	}
	wg.Wait()
	if len(out) == 0 {
		return nil
	}
	return out
}

func (p *OpenAICompatibleProvider) ollamaCapabilityDiscoveryTimeout() time.Duration {
	timeout := p.config.Timeout / 20
	if timeout < ollamaCapabilityDiscoveryMinTimeout {
		timeout = ollamaCapabilityDiscoveryMinTimeout
	}
	if timeout > ollamaCapabilityDiscoveryMaxTimeout {
		timeout = ollamaCapabilityDiscoveryMaxTimeout
	}
	return timeout
}

func (p *OpenAICompatibleProvider) isOllamaProvider() bool {
	return p.CapabilityFamily() == "ollama"
}

func (p *OpenAICompatibleProvider) isFireworksProvider() bool {
	if p.CapabilityFamily() == "fireworks" {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(p.config.Name), "fireworks") {
		return true
	}
	endpoint := buildModelsURL(p.config.BaseURL, p.config.ModelsPath)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, "api.fireworks.ai") &&
		strings.Contains(parsed.EscapedPath(), "/models")
}

func (p *OpenAICompatibleProvider) isLMStudioProvider() bool {
	if p.CapabilityFamily() == "lmstudio" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(p.config.Name)) {
	case "lmstudio", "lm-studio", "lm studio", "local-lmstudio":
		return true
	default:
		return false
	}
}

func lmStudioNativeModelsURL(base string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return "", err
	}
	u.RawQuery = ""
	u.Fragment = ""
	path := strings.TrimRight(u.Path, "/")
	if strings.EqualFold(path, "/v1") {
		path = ""
	}
	u.Path = strings.TrimRight(path, "/") + "/api/v1/models"
	return u.String(), nil
}

func ollamaNativeBaseURL(base string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return "", err
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/")
	if strings.EqualFold(u.Path, "/v1") {
		u.Path = ""
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func (p *OpenAICompatibleProvider) discoverOllamaModelCapability(ctx context.Context, baseURL, model string) (types.ModelCapabilities, bool) {
	body, err := json.Marshal(ollamaShowRequest{Model: model})
	if err != nil {
		return types.ModelCapabilities{}, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/show", bytes.NewReader(body))
	if err != nil {
		return types.ModelCapabilities{}, false
	}
	req.Header.Set("Content-Type", "application/json")
	injectTraceContext(req)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return types.ModelCapabilities{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return types.ModelCapabilities{}, false
	}

	var payload ollamaShowResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil || len(payload.Capabilities) == 0 {
		return types.ModelCapabilities{}, false
	}
	toolCalling := "none"
	imageInput := "none"
	for _, capability := range payload.Capabilities {
		switch strings.ToLower(strings.TrimSpace(capability)) {
		case "tools":
			toolCalling = "basic"
		case "vision":
			imageInput = "supported"
		}
	}
	return types.ModelCapabilities{
		ToolCalling:    toolCalling,
		ImageInput:     imageInput,
		Streaming:      true,
		StreamingKnown: true,
		Source:         "provider",
	}, true
}

func (p *OpenAICompatibleProvider) Validate() error {
	if p.config.APIKey == "" && p.Kind() != KindLocal && !p.config.StubMode {
		return fmt.Errorf("api key is required for cloud provider %s when stub mode is disabled", p.Name())
	}
	return nil
}

func (p *OpenAICompatibleProvider) chatUpstream(ctx context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}

	wireReq := openAIChatCompletionRequest{
		Model:             req.Model,
		Messages:          make([]openAIChatMessage, 0, len(req.Messages)),
		MaxTokens:         req.MaxTokens,
		Temperature:       req.Temperature,
		TopP:              req.TopP,
		Stop:              append([]string(nil), req.StopSequences...),
		ToolChoice:        req.ToolChoice,
		ResponseFormat:    req.ResponseFormat,
		Seed:              req.Seed,
		PresencePenalty:   req.PresencePenalty,
		FrequencyPenalty:  req.FrequencyPenalty,
		Logprobs:          req.Logprobs,
		TopLogprobs:       req.TopLogprobs,
		LogitBias:         req.LogitBias,
		StreamOptions:     req.StreamOptions,
		ParallelToolCalls: req.ParallelToolCalls,
	}
	for _, msg := range req.Messages {
		wireMsg := openAIChatMessage{
			Role:       msg.Role,
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
		}
		if len(msg.ToolCalls) > 0 {
			// content: null is required by OpenAI when tool_calls
			// is present.
			wireMsg.Content = openAIMessageContent{Null: true}
			wireMsg.ToolCalls = make([]openAIToolCall, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				wireMsg.ToolCalls = append(wireMsg.ToolCalls, openAIToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: openAIToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		} else {
			wireMsg.Content = buildOpenAIWireContent(msg)
		}
		wireReq.Messages = append(wireReq.Messages, wireMsg)
	}
	if len(req.Tools) > 0 {
		wireReq.Tools = make([]openAITool, 0, len(req.Tools))
		for _, t := range req.Tools {
			wireReq.Tools = append(wireReq.Tools, openAITool{
				Type: t.Type,
				Function: openAIToolFunction{
					Name:        t.Function.Name,
					Description: t.Function.Description,
					Parameters:  t.Function.Parameters,
					Strict:      t.Function.Strict,
				},
			})
		}
	}

	payload, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("marshal upstream request: %w", err)
	}

	endpoint := buildChatCompletionsURL(p.config.BaseURL, p.config.ChatPath)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.config.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	}
	injectTraceContext(httpReq)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send upstream request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, decodeUpstreamError(resp)
	}

	var wireResp openAIChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&wireResp); err != nil {
		return nil, fmt.Errorf("decode upstream response: %w", err)
	}

	choices := make([]types.ChatChoice, 0, len(wireResp.Choices))
	for _, choice := range wireResp.Choices {
		// Response-side content from OpenAI today is always a
		// string (or null with tool_calls). AsString flattens
		// either form, so this stays correct if a future API
		// version returns image content blocks in responses.
		m := types.Message{
			Role:       choice.Message.Role,
			Content:    choice.Message.Content.AsString(),
			Name:       choice.Message.Name,
			ToolCallID: choice.Message.ToolCallID,
		}
		if len(choice.Message.ToolCalls) > 0 {
			m.ToolCalls = make([]types.ToolCall, 0, len(choice.Message.ToolCalls))
			for _, tc := range choice.Message.ToolCalls {
				m.ToolCalls = append(m.ToolCalls, types.ToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: types.ToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		}
		choices = append(choices, types.ChatChoice{
			Index:        choice.Index,
			Message:      m,
			FinishReason: choice.FinishReason,
		})
	}

	createdAt := time.Now().UTC()
	if wireResp.Created > 0 {
		createdAt = time.Unix(wireResp.Created, 0).UTC()
	}

	model := wireResp.Model
	if model == "" {
		model = req.Model
	}

	usage := types.Usage{
		PromptTokens:     wireResp.Usage.PromptTokens,
		CompletionTokens: wireResp.Usage.CompletionTokens,
		TotalTokens:      wireResp.Usage.TotalTokens,
	}
	if d := wireResp.Usage.PromptTokensDetails; d != nil {
		usage.CachedPromptTokens = d.CachedTokens
	}
	return &types.ChatResponse{
		ID:        wireResp.ID,
		Model:     model,
		CreatedAt: createdAt,
		Choices:   choices,
		Usage:     usage,
	}, nil
}

func (p *OpenAICompatibleProvider) ChatStream(ctx context.Context, req types.ChatRequest, w io.Writer) error {
	if err := p.Validate(); err != nil {
		return err
	}

	wireReq := openAIChatCompletionRequest{
		Model:             req.Model,
		Messages:          make([]openAIChatMessage, 0, len(req.Messages)),
		MaxTokens:         req.MaxTokens,
		Temperature:       req.Temperature,
		TopP:              req.TopP,
		Stop:              append([]string(nil), req.StopSequences...),
		ToolChoice:        req.ToolChoice,
		ResponseFormat:    req.ResponseFormat,
		Seed:              req.Seed,
		PresencePenalty:   req.PresencePenalty,
		FrequencyPenalty:  req.FrequencyPenalty,
		Logprobs:          req.Logprobs,
		TopLogprobs:       req.TopLogprobs,
		LogitBias:         req.LogitBias,
		StreamOptions:     req.StreamOptions,
		ParallelToolCalls: req.ParallelToolCalls,
		Stream:            true,
	}
	for _, msg := range req.Messages {
		wireMsg := openAIChatMessage{
			Role:       msg.Role,
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
		}
		if len(msg.ToolCalls) > 0 {
			// content: null is required by OpenAI when tool_calls
			// is present.
			wireMsg.Content = openAIMessageContent{Null: true}
			wireMsg.ToolCalls = make([]openAIToolCall, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				wireMsg.ToolCalls = append(wireMsg.ToolCalls, openAIToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: openAIToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		} else {
			wireMsg.Content = buildOpenAIWireContent(msg)
		}
		wireReq.Messages = append(wireReq.Messages, wireMsg)
	}
	if len(req.Tools) > 0 {
		wireReq.Tools = make([]openAITool, 0, len(req.Tools))
		for _, t := range req.Tools {
			wireReq.Tools = append(wireReq.Tools, openAITool{
				Type: t.Type,
				Function: openAIToolFunction{
					Name:        t.Function.Name,
					Description: t.Function.Description,
					Parameters:  t.Function.Parameters,
					Strict:      t.Function.Strict,
				},
			})
		}
	}

	payload, err := json.Marshal(wireReq)
	if err != nil {
		return fmt.Errorf("marshal upstream request: %w", err)
	}

	endpoint := buildChatCompletionsURL(p.config.BaseURL, p.config.ChatPath)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build upstream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.config.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	}
	injectTraceContext(httpReq)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("send upstream request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return decodeUpstreamError(resp)
	}

	return proxySSE(ctx, resp.Body, w)
}

// buildOpenAIWireContent picks between the string form and the
// array-of-blocks form for outbound message content.
//
//   - When the source message has no ContentBlocks (the common
//     case — single-turn text chat, tool results, etc.), we emit
//     the string form. This matches what every OpenAI-compat
//     upstream has accepted forever; multi-modal isn't needed.
//   - When ContentBlocks contains image/image_url blocks, we emit the
//     array form so the upstream sees the structured payload.
//   - Text-only block content collapses back to the string form
//     to keep payloads compact and avoid surprising unsupported
//     downstreams (some OpenAI-compat endpoints — older Ollama,
//     llama.cpp — only accept the string form).
func buildOpenAIWireContent(msg types.Message) openAIMessageContent {
	if !messageHasNonTextBlocks(msg) {
		return openAIMessageContent{Text: msg.Content}
	}
	blocks := make([]openAIContentBlock, 0, len(msg.ContentBlocks))
	for _, cb := range msg.ContentBlocks {
		switch cb.Type {
		case "text", "":
			if cb.Text == "" {
				continue
			}
			blocks = append(blocks, openAIContentBlock{Type: "text", Text: cb.Text})
		case "image_url", "image":
			if imageURL := buildOpenAIImageURL(cb.Image); imageURL != nil {
				blocks = append(blocks, openAIContentBlock{Type: "image_url", ImageURL: imageURL})
			}
		}
		// Unknown block types are dropped here — OpenAI rejects
		// strict-mode requests with unrecognized block types, and
		// silently sending them risks an upstream 400 the operator
		// can't easily debug.
	}
	if len(blocks) == 0 {
		// All blocks were unrecognized / empty; fall back to the
		// flattened string form so the upstream still gets *some*
		// content.
		return openAIMessageContent{Text: msg.Content}
	}
	return openAIMessageContent{Blocks: blocks}
}

func buildOpenAIImageURL(image *types.ContentImage) *openAIContentImageURL {
	if image == nil {
		return nil
	}
	url := strings.TrimSpace(image.URL)
	if data := strings.TrimSpace(image.Data); data != "" {
		mediaType := strings.TrimSpace(image.MediaType)
		if mediaType == "" {
			return nil
		}
		url = "data:" + mediaType + ";base64," + data
	}
	if url == "" {
		return nil
	}
	return &openAIContentImageURL{URL: url, Detail: image.Detail}
}

// messageHasNonTextBlocks reports whether the message's
// ContentBlocks include anything that needs the array form on the
// wire. Pure-text block content can ride as a flat string.
func messageHasNonTextBlocks(msg types.Message) bool {
	for _, cb := range msg.ContentBlocks {
		switch cb.Type {
		case "image_url", "image":
			return true
		}
	}
	return false
}

func buildChatCompletionsURL(baseURL, path string) string {
	if strings.TrimSpace(path) != "" {
		return buildProviderURL(baseURL, path)
	}
	trimmed := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(trimmed, "/v1") {
		return trimmed + "/chat/completions"
	}
	return trimmed + "/v1/chat/completions"
}

func buildModelsURL(baseURL, path string) string {
	if strings.TrimSpace(path) != "" {
		return buildProviderURL(baseURL, path)
	}
	trimmed := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(trimmed, "/v1") {
		return trimmed + "/models"
	}
	return trimmed + "/v1/models"
}

func buildProviderURL(baseURL, path string) string {
	trimmedPath := strings.TrimSpace(path)
	if u, err := url.Parse(trimmedPath); err == nil && u.IsAbs() {
		return trimmedPath
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(trimmedPath, "/")
}

func decodeUpstreamError(resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("read upstream error body: %w", err)
	}

	var envelope openAIErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error.Message != "" {
		return &UpstreamError{
			StatusCode: resp.StatusCode,
			Message:    safetext.SanitizeErrorMessage(envelope.Error.Message),
			Type:       safetext.SanitizeErrorType(envelope.Error.Type, "upstream_error"),
		}
	}

	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}

	return &UpstreamError{
		StatusCode: resp.StatusCode,
		Message:    safetext.SanitizeErrorMessage(message),
	}
}

func lastUserMessage(messages []types.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

func estimatePromptTokens(messages []types.Message) int {
	return max(1, prompttokens.EstimateMessages(messages))
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

// proxySSE reads OpenAI-format SSE from src and writes ordinary events to dst,
// flushing after each blank-line boundary. Provider error frames are converted
// into typed, sanitized failures instead of being copied across the privacy
// boundary or mistaken for a successful stream.
func proxySSE(ctx context.Context, src io.Reader, dst io.Writer) error {
	scanner := bufio.NewScanner(src)
	for scanner.Scan() {
		// Check context cancellation before each write so we stop producing
		// output as soon as the client disconnects or the deadline expires.
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()
		if upstreamErr := decodeOpenAIStreamError(line); upstreamErr != nil {
			return upstreamErr
		}
		if _, err := fmt.Fprintf(dst, "%s\n", line); err != nil {
			return err
		}
		// SSE events are separated by blank lines; flush after each blank line.
		if line == "" {
			if f, ok := dst.(interface{ Flush() }); ok {
				f.Flush()
			}
		}
		if value, ok := sse.DataValue(line); ok && strings.TrimSpace(value) == "[DONE]" {
			// Write the trailing blank line and flush.
			fmt.Fprintf(dst, "\n") //nolint:errcheck
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
	return &UpstreamError{
		StatusCode: http.StatusBadGateway,
		Message:    "OpenAI-compatible stream ended before [DONE]",
		Type:       "upstream_error",
	}
}

func decodeOpenAIStreamError(line string) *UpstreamError {
	raw, ok := sse.DataValue(line)
	if !ok {
		return nil
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[DONE]" {
		return nil
	}
	var event struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &event); err != nil || len(event.Error) == 0 || bytes.Equal(bytes.TrimSpace(event.Error), []byte("null")) {
		return nil
	}
	message := "upstream provider stream error"
	errorType := "upstream_error"
	var detail openAIErrorDetail
	if err := json.Unmarshal(event.Error, &detail); err == nil {
		if sanitized := safetext.SanitizeErrorMessage(detail.Message); strings.TrimSpace(sanitized) != "" {
			message = sanitized
		}
		errorType = safetext.SanitizeErrorType(detail.Type, errorType)
	} else {
		var stringMessage string
		if err := json.Unmarshal(event.Error, &stringMessage); err == nil {
			if sanitized := safetext.SanitizeErrorMessage(stringMessage); strings.TrimSpace(sanitized) != "" {
				message = sanitized
			}
		}
	}
	return &UpstreamError{
		StatusCode: http.StatusBadGateway,
		Message:    message,
		Type:       errorType,
	}
}
