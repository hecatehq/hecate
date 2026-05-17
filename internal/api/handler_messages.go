package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/hecate/agent-runtime/internal/providers"
	"github.com/hecate/agent-runtime/internal/requestscope"
	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/pkg/types"
)

// HandleMessages implements POST /v1/messages — the Anthropic-native shape.
// Requests and responses are translated to/from the internal types.ChatRequest
// / ChatResponse so that an Anthropic SDK pointed at Hecate (ANTHROPIC_BASE_URL)
// can route through any configured provider (including OpenAI-compatible ones).
func (h *Handler) HandleMessages(w http.ResponseWriter, r *http.Request) {
	if !h.checkRateLimit(w, "") {
		return
	}
	ctx := r.Context()

	var wireReq AnthropicMessagesRequest
	if !decodeJSON(w, r, &wireReq) {
		return
	}

	internalReq, err := normalizeAnthropicRequest(wireReq, RequestIDFromContext(ctx))
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}

	if internalReq.Stream {
		h.handleMessagesStream(w, r, ctx, internalReq)
		return
	}

	result, err := h.service.HandleChat(ctx, internalReq)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gen_ai.gateway.request.failed",
			slog.String("event.name", "gen_ai.gateway.request.failed"),
			slog.String(telemetry.AttrGenAIRequestModel, internalReq.Model),
			slog.Any("error", err),
		)
		writeMessagesError(w, err, h.gatewayErrorDetails(ctx, internalReq.RequestID))
		return
	}

	wireResp := renderAnthropicMessagesResponse(result.Response)
	applyRuntimeHeaders(w, result.Metadata.Provider, result.Metadata.ProviderKind, result.Metadata.RouteReason,
		result.Metadata.RequestedModel, result.Metadata.CanonicalRequestedModel,
		result.Metadata.Model, result.Metadata.CanonicalResolvedModel,
		result.Metadata.TraceID, result.Metadata.SpanID,
		result.Metadata.AttemptCount, result.Metadata.RetryCount, result.Metadata.FallbackFromProvider,
		result.Metadata.CostMicrosUSD,
	)
	WriteJSON(w, http.StatusOK, wireResp)
}

func (h *Handler) handleMessagesStream(w http.ResponseWriter, r *http.Request, ctx context.Context, req types.ChatRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "streaming not supported by server")
		return
	}

	handle, streamCtx, err := h.service.RouteForStream(ctx, req)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gen_ai.gateway.stream.route_failed",
			slog.String("event.name", "gen_ai.gateway.stream.route_failed"),
			slog.String(telemetry.AttrGenAIRequestModel, req.Model),
			slog.Any("error", err),
		)
		writeMessagesError(w, err, h.gatewayErrorDetails(ctx, req.RequestID))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Runtime-Provider", handle.Metadata.Provider)
	w.Header().Set("X-Runtime-Provider-Kind", handle.Metadata.ProviderKind)
	w.Header().Set("X-Runtime-Route-Reason", handle.Metadata.RouteReason)
	w.Header().Set("X-Runtime-Requested-Model", handle.Metadata.RequestedModel)
	w.Header().Set("X-Runtime-Model", handle.Metadata.Model)
	w.Header().Set("X-Trace-Id", handle.Metadata.TraceID)
	w.Header().Set("X-Span-Id", handle.Metadata.SpanID)
	w.WriteHeader(http.StatusOK)

	// handle.Execute writes OpenAI-format SSE (chat.completion.chunk). Translate
	// each chunk into Anthropic's event-based stream as we read.
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		err := handle.Execute(pw)
		// Close the write end before signalling: this causes
		// translateOpenAIToAnthropicSSE to see EOF (or the upstream error)
		// and return naturally without waiting for more data.
		if err != nil {
			_ = pw.CloseWithError(err)
		} else {
			_ = pw.Close()
		}
		errCh <- err
	}()

	translateErr := translateOpenAIToAnthropicSSE(streamCtx, req.Model, handle.Metadata.Model, pr, flushWriter{w: w, flusher: flusher})
	// Close the read end so the upstream goroutine is unblocked if it is still
	// blocked writing to pw.  This happens when the client disconnected and
	// translateOpenAIToAnthropicSSE exited early due to a write error or a
	// cancelled context.
	_ = pr.CloseWithError(streamCtx.Err())
	runErr := <-errCh
	if runErr != nil {
		telemetry.Error(h.logger, streamCtx, "gen_ai.gateway.stream.failed",
			slog.String("event.name", "gen_ai.gateway.stream.failed"),
			slog.String(telemetry.AttrGenAIRequestModel, req.Model),
			slog.Any("error", runErr),
		)
		errMsg := runErr.Error()
		var upstreamErr *providers.UpstreamError
		if errors.As(runErr, &upstreamErr) {
			errMsg = upstreamErr.Message
		}
		fmt.Fprintf(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":%q}}\n\n", errMsg)
		flusher.Flush()
		return
	}
	if translateErr != nil {
		telemetry.Error(h.logger, streamCtx, "gen_ai.gateway.stream.translate_failed",
			slog.String("event.name", "gen_ai.gateway.stream.translate_failed"),
			slog.Any("error", translateErr),
		)
	}
}

// applyRuntimeHeaders sets X-Runtime-* headers consistent with the chat completion handler.
func applyRuntimeHeaders(w http.ResponseWriter,
	provider, providerKind, routeReason,
	requestedModel, canonicalRequestedModel,
	model, canonicalModel string,
	traceID, spanID string,
	attempts, retries int, fallbackFrom string,
	costMicrosUSD int64,
) {
	w.Header().Set("X-Runtime-Provider", provider)
	w.Header().Set("X-Runtime-Provider-Kind", providerKind)
	w.Header().Set("X-Runtime-Route-Reason", routeReason)
	w.Header().Set("X-Runtime-Requested-Model", requestedModel)
	w.Header().Set("X-Runtime-Requested-Model-Canonical", canonicalRequestedModel)
	w.Header().Set("X-Runtime-Model", model)
	w.Header().Set("X-Runtime-Model-Canonical", canonicalModel)
	w.Header().Set("X-Trace-Id", traceID)
	w.Header().Set("X-Span-Id", spanID)
	w.Header().Set("X-Runtime-Attempts", strconv.Itoa(attempts))
	w.Header().Set("X-Runtime-Retries", strconv.Itoa(retries))
	if fallbackFrom != "" {
		w.Header().Set("X-Runtime-Fallback-From", fallbackFrom)
	}
	w.Header().Set("X-Runtime-Cost-USD", formatUSD(costMicrosUSD))
}

func writeMessagesError(w http.ResponseWriter, err error, details ErrorDetails) {
	writeAnthropicGatewayError(w, classifyGatewayError(err), details)
}

func normalizeAnthropicRequest(req AnthropicMessagesRequest, requestID string) (types.ChatRequest, error) {
	if strings.TrimSpace(req.Model) == "" {
		return types.ChatRequest{}, fmt.Errorf("field \"model\" is required")
	}
	if len(req.Messages) == 0 {
		return types.ChatRequest{}, fmt.Errorf("field \"messages\" must not be empty")
	}
	if req.MaxTokens <= 0 {
		return types.ChatRequest{}, fmt.Errorf("field \"max_tokens\" is required")
	}

	messages := make([]types.Message, 0, len(req.Messages)+1)

	if sysBlocks, err := decodeAnthropicSystemBlocks(req.System); err != nil {
		return types.ChatRequest{}, err
	} else if len(sysBlocks) > 0 {
		text := contentBlocksText(sysBlocks)
		messages = append(messages, types.Message{
			Role:          "system",
			Content:       text,
			ContentBlocks: sysBlocks,
		})
	}

	for i, m := range req.Messages {
		converted, err := convertAnthropicInboundMessage(m)
		if err != nil {
			return types.ChatRequest{}, fmt.Errorf("messages[%d]: %w", i, err)
		}
		messages = append(messages, converted...)
	}

	tools := make([]types.Tool, 0, len(req.Tools))
	for _, t := range req.Tools {
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{}`)
		}
		tools = append(tools, types.Tool{
			Type: "function",
			Function: types.ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  schema,
			},
			CacheControl: t.CacheControl,
		})
	}

	toolChoice := anthropicInboundToolChoice(req.ToolChoice)

	scope := requestscope.Build(req.Provider)

	return types.ChatRequest{
		RequestID:     requestID,
		Model:         req.Model,
		Messages:      messages,
		MaxTokens:     req.MaxTokens,
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		TopK:          req.TopK,
		StopSequences: req.StopSequences,
		Scope:         scope,
		Tools:         tools,
		ToolChoice:    toolChoice,
		Stream:        req.Stream,
		Thinking:      req.Thinking,
		Betas:         req.Betas,
	}, nil
}

// decodeAnthropicSystemBlocks accepts either a plain string or an array of content blocks
// and returns them as []types.ContentBlock, preserving cache_control annotations.
func decodeAnthropicSystemBlocks(raw json.RawMessage) ([]types.ContentBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if strings.TrimSpace(s) == "" {
			return nil, nil
		}
		return []types.ContentBlock{{Type: "text", Text: s}}, nil
	}
	var blocks []AnthropicInboundContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("field \"system\" must be a string or an array of content blocks")
	}
	out := make([]types.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == "" || b.Type == "text" {
			out = append(out, types.ContentBlock{
				Type:         "text",
				Text:         b.Text,
				CacheControl: b.CacheControl,
			})
		}
	}
	return out, nil
}

// contentBlocksText concatenates text blocks into a single string.
func contentBlocksText(blocks []types.ContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if (b.Type == "" || b.Type == "text") && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// convertAnthropicInboundMessage converts one Anthropic message into one or more
// internal messages. ContentBlocks is always populated from block arrays so that
// cache_control annotations survive the gateway roundtrip to Anthropic upstreams.
// OpenAI-bound providers use Message.Content / Message.ToolCalls and ignore ContentBlocks.
func convertAnthropicInboundMessage(m AnthropicInboundMessage) ([]types.Message, error) {
	role := strings.TrimSpace(m.Role)
	if role != "user" && role != "assistant" {
		return nil, fmt.Errorf("role %q is not supported", role)
	}

	// Content is a plain string — wrap it in a single ContentBlock for consistency.
	var asString string
	if err := json.Unmarshal(m.Content, &asString); err == nil {
		return []types.Message{{
			Role:          role,
			Content:       asString,
			ContentBlocks: []types.ContentBlock{{Type: "text", Text: asString}},
		}}, nil
	}

	var blocks []AnthropicInboundContentBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return nil, fmt.Errorf("content must be a string or an array of content blocks")
	}

	out := make([]types.Message, 0, 1)
	// accumulators for the current user/assistant message segment
	var contentBlocks []types.ContentBlock // for Anthropic pass-through
	var textParts []string
	var toolCalls []types.ToolCall

	flush := func() {
		if len(contentBlocks) == 0 && len(toolCalls) == 0 {
			return
		}
		msg := types.Message{
			Role:          role,
			Content:       strings.Join(textParts, "\n"),
			ContentBlocks: contentBlocks,
		}
		if len(toolCalls) > 0 {
			msg.ToolCalls = toolCalls
		}
		out = append(out, msg)
		contentBlocks = nil
		textParts = nil
		toolCalls = nil
	}

	for _, b := range blocks {
		switch b.Type {
		case "text":
			cb := types.ContentBlock{
				Type:         "text",
				Text:         b.Text,
				CacheControl: b.CacheControl,
			}
			contentBlocks = append(contentBlocks, cb)
			if t := b.Text; t != "" {
				textParts = append(textParts, t)
			}
		case "tool_use":
			args := string(b.Input)
			if !json.Valid(json.RawMessage(args)) || args == "" {
				args = "{}"
			}
			cb := types.ContentBlock{
				Type:         "tool_use",
				ID:           b.ID,
				Name:         b.Name,
				Input:        json.RawMessage(args),
				CacheControl: b.CacheControl,
			}
			contentBlocks = append(contentBlocks, cb)
			toolCalls = append(toolCalls, types.ToolCall{
				ID:       b.ID,
				Type:     "function",
				Function: types.ToolCallFunction{Name: b.Name, Arguments: args},
			})
		case "tool_result":
			// Emit the accumulated user/assistant segment before the tool-result message.
			flush()
			resultText, err := decodeToolResultContent(b.Content)
			if err != nil {
				return nil, err
			}
			resultBlocks, err := decodeToolResultBlocks(b.Content)
			if err != nil {
				// non-fatal: fall back to text only
				resultBlocks = []types.ContentBlock{{Type: "text", Text: resultText}}
			}
			out = append(out, types.Message{
				Role:          "tool",
				Content:       resultText,
				ContentBlocks: resultBlocks,
				ToolCallID:    b.ToolUseID,
				// Preserve the is_error flag so the round-trip to
				// upstream Anthropic carries the failure signal —
				// the inbound side previously dropped it, which
				// meant the model would only see error context as
				// free-form text.
				ToolError: b.IsError,
			})
		case "thinking":
			contentBlocks = append(contentBlocks, types.ContentBlock{
				Type:      "thinking",
				Thinking:  b.Thinking,
				Signature: b.Signature,
			})
		case "redacted_thinking":
			contentBlocks = append(contentBlocks, types.ContentBlock{
				Type: "redacted_thinking",
				Data: b.Data,
			})
		default:
			// Unknown block types (image, document, ...): carry them
			// as ContentBlocks for Anthropic pass-through but skip for text/toolCalls.
			if b.Type != "" {
				contentBlocks = append(contentBlocks, types.ContentBlock{
					Type:         b.Type,
					CacheControl: b.CacheControl,
				})
			}
		}
	}

	flush()
	if len(out) == 0 {
		// Edge case: empty block array.
		out = append(out, types.Message{Role: role})
	}
	return out, nil
}

func decodeToolResultContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var blocks []AnthropicInboundContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", fmt.Errorf("tool_result content must be a string or an array of content blocks")
	}
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == "" || b.Type == "text" {
			if t := b.Text; t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, "\n"), nil
}

func decodeToolResultBlocks(raw json.RawMessage) ([]types.ContentBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil, nil
		}
		return []types.ContentBlock{{Type: "text", Text: s}}, nil
	}
	var blocks []AnthropicInboundContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("tool_result content must be a string or an array of content blocks")
	}
	out := make([]types.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == "" || b.Type == "text" {
			out = append(out, types.ContentBlock{
				Type:         "text",
				Text:         b.Text,
				CacheControl: b.CacheControl,
			})
		}
	}
	return out, nil
}

// anthropicInboundToolChoice converts Anthropic tool_choice ({"type":"auto"|"any"|"tool","name":...})
// to the OpenAI tool_choice shape used internally.
func anthropicInboundToolChoice(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var obj struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	switch obj.Type {
	case "auto":
		return json.RawMessage(`"auto"`)
	case "any":
		return json.RawMessage(`"required"`)
	case "tool":
		if obj.Name == "" {
			return nil
		}
		b, _ := json.Marshal(map[string]any{
			"type":     "function",
			"function": map[string]any{"name": obj.Name},
		})
		return b
	}
	return nil
}

// renderAnthropicMessagesResponse converts the internal ChatResponse back to the
// Anthropic /v1/messages shape.
func renderAnthropicMessagesResponse(resp *types.ChatResponse) AnthropicMessagesResponse {
	out := AnthropicMessagesResponse{
		ID:    resp.ID,
		Type:  "message",
		Role:  "assistant",
		Model: resp.Model,
		Usage: AnthropicOutboundUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}
	if out.ID == "" {
		out.ID = "msg_" + strings.TrimSpace(resp.Model)
	}
	if len(resp.Choices) == 0 {
		out.StopReason = "end_turn"
		return out
	}
	choice := resp.Choices[0]

	// If the message carries structured ContentBlocks (Anthropic pass-through path),
	// render them directly so thinking/redacted_thinking blocks survive the round-trip.
	if len(choice.Message.ContentBlocks) > 0 {
		blocks := make([]AnthropicOutboundContentBlock, 0, len(choice.Message.ContentBlocks))
		for _, cb := range choice.Message.ContentBlocks {
			switch cb.Type {
			case "thinking":
				blocks = append(blocks, AnthropicOutboundContentBlock{
					Type:      "thinking",
					Thinking:  cb.Thinking,
					Signature: cb.Signature,
				})
			case "redacted_thinking":
				blocks = append(blocks, AnthropicOutboundContentBlock{
					Type: "redacted_thinking",
					Data: cb.Data,
				})
			case "tool_use":
				input := cb.Input
				if len(input) == 0 || !json.Valid(input) {
					input = json.RawMessage(`{}`)
				}
				blocks = append(blocks, AnthropicOutboundContentBlock{
					Type:  "tool_use",
					ID:    cb.ID,
					Name:  cb.Name,
					Input: input,
				})
			default:
				if cb.Text != "" || cb.Type == "text" || cb.Type == "" {
					blocks = append(blocks, AnthropicOutboundContentBlock{Type: "text", Text: cb.Text})
				}
			}
		}
		if len(blocks) == 0 {
			blocks = append(blocks, AnthropicOutboundContentBlock{Type: "text", Text: ""})
		}
		out.Content = blocks
		out.StopReason = openAIFinishToAnthropicStopReason(choice.FinishReason)
		return out
	}

	// Standard path: build blocks from flat Content + ToolCalls.
	blocks := make([]AnthropicOutboundContentBlock, 0, 1+len(choice.Message.ToolCalls))
	if text := strings.TrimSpace(choice.Message.Content); text != "" {
		blocks = append(blocks, AnthropicOutboundContentBlock{Type: "text", Text: choice.Message.Content})
	}
	for _, tc := range choice.Message.ToolCalls {
		input := json.RawMessage(tc.Function.Arguments)
		if len(input) == 0 || !json.Valid(input) {
			input = json.RawMessage(`{}`)
		}
		blocks = append(blocks, AnthropicOutboundContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, AnthropicOutboundContentBlock{Type: "text", Text: ""})
	}
	out.Content = blocks
	out.StopReason = openAIFinishToAnthropicStopReason(choice.FinishReason)
	return out
}

func openAIFinishToAnthropicStopReason(finish string) string {
	switch finish {
	case "stop", "":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls", "tool_use":
		return "tool_use"
	case "content_filter":
		return "end_turn"
	default:
		return finish
	}
}

// translateOpenAIToAnthropicSSE reads OpenAI chat.completion.chunk SSE lines
// from src and writes Anthropic event-stream events to dst.
func translateOpenAIToAnthropicSSE(ctx context.Context, requestedModel, resolvedModel string, src io.Reader, dst io.Writer) error {
	type openAIDelta struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		ToolCalls []struct {
			Index    int    `json:"index"`
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
		// Extension fields used when routing through an Anthropic upstream:
		// translateAnthropicSSE encodes thinking deltas here so they survive
		// the Anthropic→OpenAI→Anthropic double translation.
		XThinking          string `json:"x_thinking,omitempty"`
		XThinkingSignature string `json:"x_thinking_signature,omitempty"`
	}
	type openAIChoice struct {
		Index        int         `json:"index"`
		Delta        openAIDelta `json:"delta"`
		FinishReason *string     `json:"finish_reason"`
	}
	type openAIUsageChunk struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}
	type openAIChunk struct {
		ID      string            `json:"id"`
		Model   string            `json:"model"`
		Choices []openAIChoice    `json:"choices"`
		Usage   *openAIUsageChunk `json:"usage,omitempty"`
	}

	messageID := ""
	model := resolvedModel
	if model == "" {
		model = requestedModel
	}

	// Block bookkeeping: index 0 is text block (lazily opened on first text).
	textOpen := false
	textIndex := 0
	nextBlockIndex := 1
	// toolBlocks tracks OpenAI tool_calls-index -> anthropic block index (and name/id)
	type toolState struct {
		blockIndex int
		id         string
		name       string
		started    bool
	}
	toolBlocks := make(map[int]*toolState)
	// thinkingBlockIndex is the Anthropic block index for an open thinking block
	// (-1 = none open yet).
	thinkingBlockIndex := -1

	promptTokens := 0
	completionTokens := 0
	var stopReason string
	started := false

	writeEvent := func(event string, payload any) error {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(dst, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return err
		}
		if f, ok := dst.(interface{ Flush() }); ok {
			f.Flush()
		}
		return nil
	}

	ensureMessageStart := func() error {
		if started {
			return nil
		}
		started = true
		if messageID == "" {
			messageID = "msg_stream"
		}
		return writeEvent("message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            messageID,
				"type":          "message",
				"role":          "assistant",
				"model":         model,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  0,
					"output_tokens": 0,
				},
			},
		})
	}

	ensureTextBlockOpen := func() error {
		if textOpen {
			return nil
		}
		textOpen = true
		return writeEvent("content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         textIndex,
			"content_block": map[string]any{"type": "text", "text": ""},
		})
	}

	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk openAIChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.ID != "" && messageID == "" {
			messageID = chunk.ID
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if err := ensureMessageStart(); err != nil {
			return err
		}

		if chunk.Usage != nil {
			if chunk.Usage.PromptTokens > 0 {
				promptTokens = chunk.Usage.PromptTokens
			}
			if chunk.Usage.CompletionTokens > 0 {
				completionTokens = chunk.Usage.CompletionTokens
			}
		}

		for _, choice := range chunk.Choices {
			// Text delta
			if choice.Delta.Content != "" {
				if err := ensureTextBlockOpen(); err != nil {
					return err
				}
				if err := writeEvent("content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": textIndex,
					"delta": map[string]any{"type": "text_delta", "text": choice.Delta.Content},
				}); err != nil {
					return err
				}
			}
			// Tool call deltas
			for _, tc := range choice.Delta.ToolCalls {
				state, ok := toolBlocks[tc.Index]
				if !ok {
					state = &toolState{blockIndex: nextBlockIndex}
					nextBlockIndex++
					toolBlocks[tc.Index] = state
				}
				if tc.ID != "" {
					state.id = tc.ID
				}
				if tc.Function.Name != "" {
					state.name = tc.Function.Name
				}
				if !state.started && state.id != "" && state.name != "" {
					state.started = true
					if err := writeEvent("content_block_start", map[string]any{
						"type":  "content_block_start",
						"index": state.blockIndex,
						"content_block": map[string]any{
							"type":  "tool_use",
							"id":    state.id,
							"name":  state.name,
							"input": map[string]any{},
						},
					}); err != nil {
						return err
					}
				}
				if tc.Function.Arguments != "" && state.started {
					if err := writeEvent("content_block_delta", map[string]any{
						"type":  "content_block_delta",
						"index": state.blockIndex,
						"delta": map[string]any{"type": "input_json_delta", "partial_json": tc.Function.Arguments},
					}); err != nil {
						return err
					}
				}
			}
			// Thinking pass-through (Anthropic→OpenAI→Anthropic double translation).
			if choice.Delta.XThinking != "" {
				if thinkingBlockIndex < 0 {
					// Open the thinking block on first delta.
					thinkingBlockIndex = nextBlockIndex
					nextBlockIndex++
					if err := writeEvent("content_block_start", map[string]any{
						"type":          "content_block_start",
						"index":         thinkingBlockIndex,
						"content_block": map[string]any{"type": "thinking", "thinking": ""},
					}); err != nil {
						return err
					}
				}
				if err := writeEvent("content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": thinkingBlockIndex,
					"delta": map[string]any{"type": "thinking_delta", "thinking": choice.Delta.XThinking},
				}); err != nil {
					return err
				}
			}
			if choice.Delta.XThinkingSignature != "" {
				if thinkingBlockIndex >= 0 {
					if err := writeEvent("content_block_delta", map[string]any{
						"type":  "content_block_delta",
						"index": thinkingBlockIndex,
						"delta": map[string]any{"type": "signature_delta", "signature": choice.Delta.XThinkingSignature},
					}); err != nil {
						return err
					}
				}
			}
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				stopReason = openAIFinishToAnthropicStopReason(*choice.FinishReason)
			}
		}
	}
	// Prefer the context error when the scanner stopped due to an I/O error
	// caused by context cancellation (the pipe read end was closed, or the
	// upstream HTTP body was aborted).
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	// Ensure message_start even for empty streams.
	if err := ensureMessageStart(); err != nil {
		return err
	}

	// Close blocks.
	if thinkingBlockIndex >= 0 {
		if err := writeEvent("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": thinkingBlockIndex,
		}); err != nil {
			return err
		}
	}
	if textOpen {
		if err := writeEvent("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": textIndex,
		}); err != nil {
			return err
		}
	}
	for _, state := range toolBlocks {
		if !state.started {
			continue
		}
		if err := writeEvent("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": state.blockIndex,
		}); err != nil {
			return err
		}
	}

	if stopReason == "" {
		stopReason = "end_turn"
	}
	if err := writeEvent("message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"input_tokens":  promptTokens,
			"output_tokens": completionTokens,
		},
	}); err != nil {
		return err
	}
	if err := writeEvent("message_stop", map[string]any{"type": "message_stop"}); err != nil {
		return err
	}
	return nil
}
