package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/requestscope"
	"github.com/hecatehq/hecate/internal/safetext"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

func (h *Handler) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
	bodyRead := h.beginInferenceBodyRead(w, r)
	if !h.checkInferenceRateLimit(w, "", bodyRead, inferenceErrorOpenAI) {
		return
	}
	ctx := r.Context()

	var wireReq OpenAIChatCompletionRequest
	if !h.decodeInferenceJSON(w, r, &wireReq, bodyRead, inferenceErrorOpenAI) {
		return
	}

	internalReq, err := normalizeChatRequest(wireReq, RequestIDFromContext(ctx))
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}

	if internalReq.Stream {
		h.handleChatCompletionsStream(w, r, ctx, internalReq)
		return
	}

	result, err := h.service.HandleChat(ctx, internalReq)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gen_ai.gateway.request.failed",
			slog.String("event.name", "gen_ai.gateway.request.failed"),
			slog.String(telemetry.AttrGenAIRequestModel, internalReq.Model),
			slog.Any("error", err),
		)

		writeOpenAIGatewayError(w, classifyGatewayError(err), h.gatewayErrorDetails(ctx, internalReq.RequestID))
		return
	}

	wireResp := renderChatCompletionResponse(result.Response)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Runtime-Provider", result.Metadata.Provider)
	w.Header().Set("X-Runtime-Provider-Kind", result.Metadata.ProviderKind)
	w.Header().Set("X-Runtime-Route-Reason", result.Metadata.RouteReason)
	w.Header().Set("X-Runtime-Requested-Model", result.Metadata.RequestedModel)
	w.Header().Set("X-Runtime-Requested-Model-Canonical", result.Metadata.CanonicalRequestedModel)
	w.Header().Set("X-Runtime-Model", result.Metadata.Model)
	w.Header().Set("X-Runtime-Model-Canonical", result.Metadata.CanonicalResolvedModel)
	w.Header().Set("X-Trace-Id", result.Metadata.TraceID)
	w.Header().Set("X-Span-Id", result.Metadata.SpanID)
	w.Header().Set("X-Runtime-Attempts", strconv.Itoa(result.Metadata.AttemptCount))
	w.Header().Set("X-Runtime-Retries", strconv.Itoa(result.Metadata.RetryCount))
	if result.Metadata.FallbackFromProvider != "" {
		w.Header().Set("X-Runtime-Fallback-From", result.Metadata.FallbackFromProvider)
	}
	w.Header().Set("X-Runtime-Cost-USD", formatUSD(result.Metadata.CostMicrosUSD))
	WriteJSON(w, http.StatusOK, wireResp)
}

func (h *Handler) handleChatCompletionsStream(w http.ResponseWriter, r *http.Request, ctx context.Context, req types.ChatRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "streaming not supported by server")
		return
	}

	// Route first — no bytes written yet, so errors can still be JSON.
	handle, streamCtx, err := h.service.RouteForStream(ctx, req)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gen_ai.gateway.stream.route_failed",
			slog.String("event.name", "gen_ai.gateway.stream.route_failed"),
			slog.String(telemetry.AttrGenAIRequestModel, req.Model),
			slog.Any("error", err),
		)
		writeOpenAIGatewayError(w, classifyGatewayError(err), h.gatewayErrorDetails(ctx, req.RequestID))
		return
	}

	// Routing succeeded — now commit to SSE.
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

	if _, err := handle.ExecuteAndCapture(flushWriter{w, flusher}); err != nil {
		terminalErr := classifyStreamingTerminalError(err)
		telemetry.Error(h.logger, streamCtx, "gen_ai.gateway.stream.failed",
			slog.String("event.name", "gen_ai.gateway.stream.failed"),
			slog.String(telemetry.AttrGenAIRequestModel, req.Model),
			slog.String(telemetry.AttrErrorType, terminalErr.OpenAIType),
			slog.String(telemetry.AttrErrorMessage, terminalErr.Message),
		)
		// Headers already sent; write a terminal SSE error event.
		payload, marshalErr := json.Marshal(map[string]any{
			"error": map[string]string{
				"message": terminalErr.Message,
				"type":    terminalErr.OpenAIType,
			},
		})
		if marshalErr != nil {
			payload = []byte(`{"error":{"message":"gateway stream error","type":"gateway_error"}}`)
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", payload)
		flusher.Flush()
		return
	}
}

// classifyStreamingTerminalError preserves the protocol-specific mapping for
// typed provider failures while ensuring every generic error crosses the
// shared privacy boundary before it reaches logs or a terminal SSE event.
func classifyStreamingTerminalError(err error) gatewayHTTPError {
	var upstreamErr *providers.UpstreamError
	if errors.As(err, &upstreamErr) {
		return classifyUpstreamError(upstreamErr)
	}

	classified := classifyGatewayError(err)
	classified.Message = safetext.ErrorMessage(err)
	if strings.TrimSpace(classified.Message) == "" {
		classified.Message = "gateway stream error"
	}
	return classified
}

type flushWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func (fw flushWriter) Write(p []byte) (int, error) { return fw.w.Write(p) }
func (fw flushWriter) Flush()                      { fw.flusher.Flush() }

func normalizeChatRequest(req OpenAIChatCompletionRequest, requestID string) (types.ChatRequest, error) {
	messages := make([]types.Message, 0, len(req.Messages))
	for messageIndex, msg := range req.Messages {
		// Content can be a plain string OR an array of content
		// blocks (multi-modal: text + image_url). The string form
		// stays in Message.Content for legacy code paths; the array
		// form additionally populates ContentBlocks so the outbound
		// adapter can reconstruct the structured wire shape.
		m := types.Message{
			Role:       msg.Role,
			Content:    msg.Content.AsString(),
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
			ToolError:  msg.ToolError,
		}
		if len(msg.Content.Blocks) > 0 {
			var err error
			m.ContentBlocks, err = openAIInboundBlocksToContentBlocks(messageIndex, msg.Content.Blocks)
			if err != nil {
				return types.ChatRequest{}, err
			}
		}
		// content_blocks (the Hecate-extension persisted-block shape)
		// supersedes the OpenAI-spec inline blocks when both are set —
		// it carries the richer Anthropic-aware data (thinking,
		// redacted_thinking, tool_use, cache_control). Replay paths
		// always emit content_blocks; SDK clients hitting the OpenAI
		// proxy never set it.
		if len(msg.ContentBlocks) > 0 {
			var err error
			m.ContentBlocks, err = persistedBlocksToContentBlocks(messageIndex, msg.ContentBlocks)
			if err != nil {
				return types.ChatRequest{}, err
			}
		}
		if len(msg.ToolCalls) > 0 {
			m.ToolCalls = make([]types.ToolCall, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
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
		messages = append(messages, m)
	}

	tools := make([]types.Tool, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, types.Tool{
			Type: t.Type,
			Function: types.ToolFunction{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
				Strict:      t.Function.Strict,
			},
		})
	}

	scope := requestscope.Build(req.Provider)
	requirements := types.ChatRequestRequirements{
		NoProviderFailover: chatMessagesContainImages(messages),
	}

	return types.ChatRequest{
		RequestID:         requestID,
		Model:             req.Model,
		Messages:          messages,
		Temperature:       req.Temperature,
		MaxTokens:         req.MaxTokens,
		Scope:             scope,
		Requirements:      requirements,
		Tools:             tools,
		ToolChoice:        req.ToolChoice,
		Stream:            req.Stream,
		ResponseFormat:    req.ResponseFormat,
		Seed:              req.Seed,
		PresencePenalty:   req.PresencePenalty,
		FrequencyPenalty:  req.FrequencyPenalty,
		Logprobs:          req.Logprobs,
		TopLogprobs:       req.TopLogprobs,
		LogitBias:         req.LogitBias,
		StreamOptions:     req.StreamOptions,
		ParallelToolCalls: req.ParallelToolCalls,
	}, nil
}

func renderChatCompletionResponse(resp *types.ChatResponse) OpenAIChatCompletionResponse {
	choices := make([]OpenAIChatCompletionChoice, 0, len(resp.Choices))
	for _, choice := range resp.Choices {
		msg := OpenAIChatMessage{
			Role: choice.Message.Role,
			Name: choice.Message.Name,
		}
		if len(choice.Message.ToolCalls) > 0 {
			// OpenAI requires content: null when tool_calls is set.
			msg.Content = OpenAIMessageContent{Null: true}
			msg.ToolCalls = make([]OpenAIToolCall, 0, len(choice.Message.ToolCalls))
			for _, tc := range choice.Message.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, OpenAIToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: OpenAIToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		} else {
			msg.Content = OpenAIMessageContent{Text: choice.Message.Content}
		}
		choices = append(choices, OpenAIChatCompletionChoice{
			Index:        choice.Index,
			Message:      msg,
			FinishReason: choice.FinishReason,
		})
	}

	usage := OpenAIUsage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}
	// Surface cache-read tokens in the OpenAI prompt_tokens_details
	// shape so /v1/chat/completions clients see the same usage buckets
	// Hecate records internally. Without this, an Anthropic upstream's
	// cache hits are invisible on the wire.
	if resp.Usage.CachedPromptTokens > 0 {
		usage.PromptTokensDetails = &OpenAIPromptTokensDetails{
			CachedTokens: resp.Usage.CachedPromptTokens,
		}
	}
	return OpenAIChatCompletionResponse{
		ID:      resp.ID,
		Object:  "chat.completion",
		Created: resp.CreatedAt.Unix(),
		Model:   resp.Model,
		Choices: choices,
		Usage:   usage,
	}
}

func messageToWire(msg types.Message) OpenAIChatMessage {
	wire := OpenAIChatMessage{
		Role:       msg.Role,
		Name:       msg.Name,
		ToolCallID: msg.ToolCallID,
		ToolError:  msg.ToolError,
	}
	if len(msg.ToolCalls) > 0 {
		wire.ToolCalls = make([]OpenAIToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			wire.ToolCalls = append(wire.ToolCalls, OpenAIToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: OpenAIToolCallFunction{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
		// OpenAI requires assistant + tool_calls messages to carry
		// content: null on the wire. The marshaller honors the
		// Null flag.
		wire.Content = OpenAIMessageContent{Null: true}
	} else {
		wire.Content = OpenAIMessageContent{Text: msg.Content}
	}
	if len(msg.ContentBlocks) > 0 {
		wire.ContentBlocks = contentBlocksToPersistedBlocks(msg.ContentBlocks)
	}
	return wire
}

// contentBlocksToPersistedBlocks maps the canonical types.ContentBlock
// to the persisted Hecate-extension wire shape. Used by session-fetch
// rendering so Anthropic thinking / redacted_thinking / tool_use blocks
// survive the round-trip back to a UI client. The OpenAI image-block
// shape is also emitted here for completeness.
func contentBlocksToPersistedBlocks(blocks []types.ContentBlock) []OpenAIPersistedContentBlock {
	out := make([]OpenAIPersistedContentBlock, 0, len(blocks))
	for _, cb := range blocks {
		wire := OpenAIPersistedContentBlock{
			Type:         cb.Type,
			Text:         cb.Text,
			ID:           cb.ID,
			Name:         cb.Name,
			Input:        cb.Input,
			ToolUseID:    cb.ToolUseID,
			CacheControl: cb.CacheControl,
			Thinking:     cb.Thinking,
			Signature:    cb.Signature,
			Data:         cb.Data,
		}
		if cb.Image != nil && (cb.Type == "image_url" || cb.Type == "image") {
			wire.ImageURL = &OpenAIContentImageURL{
				URL:    cb.Image.URL,
				Detail: cb.Image.Detail,
			}
		}
		out = append(out, wire)
	}
	return out
}

// persistedBlocksToContentBlocks is the inverse: maps the wire
// extension shape back to the canonical types.ContentBlock. Used on
// the inbound side of normalizeChatRequest when the UI replays history
// containing rich blocks.
func persistedBlocksToContentBlocks(messageIndex int, blocks []OpenAIPersistedContentBlock) ([]types.ContentBlock, error) {
	out := make([]types.ContentBlock, 0, len(blocks))
	for blockIndex, b := range blocks {
		cb := types.ContentBlock{
			Type:         b.Type,
			Text:         b.Text,
			ID:           b.ID,
			Name:         b.Name,
			Input:        b.Input,
			ToolUseID:    b.ToolUseID,
			CacheControl: b.CacheControl,
			Thinking:     b.Thinking,
			Signature:    b.Signature,
			Data:         b.Data,
		}
		if b.Type == "image" || b.Type == "image_url" {
			field := fmt.Sprintf("messages[%d].content_blocks[%d].image_url", messageIndex, blockIndex)
			image, err := validateOpenAIContentImage(field, b.ImageURL)
			if err != nil {
				return nil, err
			}
			cb.Image = image
		} else if b.ImageURL != nil {
			cb.Image = &types.ContentImage{URL: b.ImageURL.URL, Detail: b.ImageURL.Detail}
		}
		out = append(out, cb)
	}
	return out, nil
}

// openAIInboundBlocksToContentBlocks converts the inbound OpenAI
// content-block array into the internal types.ContentBlock shape.
// Text blocks land as text; image_url blocks land as Type="image_url"
// with the URL/Detail packed into ContentImage. Unknown block
// types pass through with Type set so the outbound adapter can
// either re-emit or warn-and-drop.
func openAIInboundBlocksToContentBlocks(messageIndex int, blocks []OpenAIContentBlock) ([]types.ContentBlock, error) {
	out := make([]types.ContentBlock, 0, len(blocks))
	for blockIndex, b := range blocks {
		switch b.Type {
		case "text", "":
			out = append(out, types.ContentBlock{
				Type: "text",
				Text: b.Text,
			})
		case "image_url":
			field := fmt.Sprintf("messages[%d].content[%d].image_url", messageIndex, blockIndex)
			image, err := validateOpenAIContentImage(field, b.ImageURL)
			if err != nil {
				return nil, err
			}
			out = append(out, types.ContentBlock{
				Type:  "image_url",
				Image: image,
			})
		default:
			// Forward unknown variants so future block types
			// (audio, file, video) survive the round-trip; the
			// outbound adapter decides whether to ship them.
			out = append(out, types.ContentBlock{Type: b.Type})
		}
	}
	return out, nil
}

func validateOpenAIContentImage(field string, imageURL *OpenAIContentImageURL) (*types.ContentImage, error) {
	if imageURL == nil {
		return nil, fmt.Errorf("%s is required", field)
	}
	rawURL := strings.TrimSpace(imageURL.URL)
	if rawURL == "" {
		return nil, fmt.Errorf("%s.url is required", field)
	}
	if !isSupportedOpenAIImageURL(rawURL) {
		return nil, fmt.Errorf("%s.url must use http, https, or a valid base64 image data URI", field)
	}
	detail := strings.TrimSpace(imageURL.Detail)
	switch detail {
	case "", "auto", "low", "high":
	default:
		return nil, fmt.Errorf("%s.detail must be one of auto, low, or high", field)
	}
	return &types.ContentImage{URL: rawURL, Detail: detail}, nil
}

func isSupportedOpenAIImageURL(rawURL string) bool {
	lower := strings.ToLower(rawURL)
	if strings.HasPrefix(lower, "data:image/") {
		comma := strings.IndexByte(rawURL, ',')
		if comma < 0 || !strings.HasSuffix(strings.ToLower(rawURL[:comma]), ";base64") || comma == len(rawURL)-1 {
			return false
		}
		decodedBytes, err := io.Copy(io.Discard, base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(rawURL[comma+1:])))
		return err == nil && decodedBytes > 0
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func mapUpstreamStatus(statusCode int) int {
	switch statusCode {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusTooManyRequests:
		return statusCode
	default:
		return http.StatusBadGateway
	}
}
