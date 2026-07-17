package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hecatehq/hecate/internal/catalog"
	"github.com/hecatehq/hecate/internal/governor"
	"github.com/hecatehq/hecate/internal/models"
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/prompttokens"
	"github.com/hecatehq/hecate/internal/providerdispatch"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/retention"
	"github.com/hecatehq/hecate/internal/router"
	"github.com/hecatehq/hecate/internal/sse"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

type Dependencies struct {
	Logger          *slog.Logger
	Finalizer       ResponseFinalizer
	Preflight       RoutePreflight
	Resilience      ResilienceOptions
	Executor        ProviderExecutor
	Router          router.Router
	Catalog         catalog.Catalog
	Governor        governor.Governor
	Providers       providers.Registry
	HealthTracker   providers.HealthTracker
	ProviderHistory providers.HealthHistoryStore
	Tracer          profiler.Tracer
	Metrics         *telemetry.Metrics
	Retention       *retention.Manager
	// TraceBodyCapture enables recording message body diagnostics in traces.
	TraceBodyCapture  bool
	TraceBodyMode     string
	TraceBodyMaxBytes int
}

type ResilienceOptions struct {
	MaxAttempts     int
	RetryBackoff    time.Duration
	FailoverEnabled bool
}

type Service struct {
	finalizer         ResponseFinalizer
	preflight         RoutePreflight
	executor          ProviderExecutor
	router            router.Router
	catalog           catalog.Catalog
	governor          governor.Governor
	tracer            profiler.Tracer
	metrics           *telemetry.Metrics
	retention         *retention.Manager
	providerHistory   providers.HealthHistoryStore
	providers         providers.Registry
	traceBodyCapture  bool
	traceBodyMaxBytes int
	traceBodyMode     string
}

const (
	traceBodyModeMetadata     = "metadata"
	traceBodyModeRedactedText = "redacted_text"
	traceBodyMaxItems         = 128
)

type ChatResult struct {
	Response *types.ChatResponse
	Metadata ResponseMetadata
	Trace    *profiler.Trace
}

type ModelsResult struct {
	Models             []types.ModelInfo
	ProviderIdentities []catalog.ProviderIdentity
}

type ProviderStatusResult struct {
	Providers []types.ProviderStatus
}

type ProviderModelReadinessResult struct {
	Readiness ProviderModelReadiness
}

type ProviderHealthHistoryResult struct {
	Entries []types.ProviderHealthHistoryEntry
}

type UsageSummaryResult struct {
	Summary types.UsageSummary
}

type UsageEventsResult struct {
	Entries []types.UsageEventEntry
}

type TraceResult struct {
	RequestID string
	TraceID   string
	StartedAt time.Time
	Spans     []types.TraceSpan
	Route     types.RouteDecisionReport
}

type RetentionResult struct {
	Run retention.RunResult
}

type RetentionHistoryResult struct {
	Runs []retention.HistoryRecord
}

type ResponseMetadata struct {
	RequestID               string
	Provider                string
	ProviderKind            string
	ProviderInstance        types.ProviderInstanceIdentity
	RouteReason             string
	RequestedModel          string
	CanonicalRequestedModel string
	Model                   string
	CanonicalResolvedModel  string
	PromptTokens            int
	CompletionTokens        int
	TotalTokens             int
	CostMicrosUSD           int64
	AttemptCount            int
	RetryCount              int
	FallbackFromProvider    string
	TraceID                 string
	SpanID                  string
}

type ExecutionPlan struct {
	OriginalRequest types.ChatRequest
	Request         types.ChatRequest
	Route           types.RouteDecision
	ProviderKind    string
}

func NewService(deps Dependencies) *Service {
	cat := deps.Catalog
	if cat == nil {
		cat = catalog.NewRegistryCatalog(deps.Providers, deps.HealthTracker)
	}

	preflight := deps.Preflight
	if preflight == nil {
		preflight = NewDefaultRoutePreflight(deps.Governor, deps.Providers)
	}

	executor := deps.Executor
	if executor == nil {
		executor = NewResilientExecutor(
			deps.Router,
			preflight,
			deps.Providers,
			deps.HealthTracker,
			deps.ProviderHistory,
			deps.Metrics,
			deps.Resilience,
		)
	}

	finalizer := deps.Finalizer
	if finalizer == nil {
		finalizer = NewDefaultResponseFinalizer(
			deps.Logger,
			deps.Governor,
			deps.Metrics,
		)
	}

	traceBodyMaxBytes := deps.TraceBodyMaxBytes
	if traceBodyMaxBytes <= 0 {
		traceBodyMaxBytes = 4096
	}
	traceBodyMode := normalizeTraceBodyMode(deps.TraceBodyMode)

	return &Service{
		finalizer:         finalizer,
		preflight:         preflight,
		executor:          executor,
		router:            deps.Router,
		catalog:           cat,
		governor:          deps.Governor,
		tracer:            deps.Tracer,
		metrics:           deps.Metrics,
		retention:         deps.Retention,
		providerHistory:   deps.ProviderHistory,
		providers:         deps.Providers,
		traceBodyCapture:  deps.TraceBodyCapture,
		traceBodyMaxBytes: traceBodyMaxBytes,
		traceBodyMode:     traceBodyMode,
	}
}

func (s *Service) Tracer() profiler.Tracer {
	if s == nil {
		return nil
	}
	return s.tracer
}

func (s *Service) HandleChat(ctx context.Context, req types.ChatRequest) (result *ChatResult, err error) {
	startedAt := time.Now()
	defer func() { s.recordRequestOutcome(ctx, err, time.Since(startedAt)) }()

	trace := s.tracer.Start(req.RequestID)
	defer trace.Finalize()
	ctx = telemetry.WithTraceIDs(ctx, trace.TraceID, trace.RootSpanID())

	plan, err := s.buildExecutionPlan(ctx, trace, req)
	if err != nil {
		return nil, err
	}

	if s.traceBodyCapture {
		s.captureRequestBody(trace, req)
	}

	result, err = s.executePlan(ctx, trace, plan)
	if err == nil && s.traceBodyCapture && result != nil && result.Response != nil {
		s.captureResponseBody(trace, result.Response)
	}
	return result, err
}

func (s *Service) recordRequestOutcome(ctx context.Context, err error, duration time.Duration) {
	if s.metrics == nil {
		return
	}
	result := telemetry.ResultSuccess
	if err != nil {
		result = telemetry.ResultError
		if IsDeniedError(err) {
			result = telemetry.ResultDenied
		}
	}
	s.metrics.RecordRequestOutcome(ctx, result, duration)
}

func (s *Service) buildExecutionPlan(ctx context.Context, trace *profiler.Trace, req types.ChatRequest) (*ExecutionPlan, error) {
	requestedIdentity := models.BuildIdentity(req.Model, "")
	trace.Record("request.received", map[string]any{
		telemetry.AttrHecateRequestMessageCount: len(req.Messages),
		telemetry.AttrGenAIRequestModel:         req.Model,
		telemetry.AttrHecateModelCanonical:      requestedIdentity.CanonicalRequested,
	})

	if err := validate(req); err != nil {
		recordTraceError(trace, "request.invalid", "request", errorKindInvalidRequest, err, nil)
		return nil, fmt.Errorf("%w: %v", errClient, err)
	}

	if err := s.governor.Check(ctx, req); err != nil {
		recordTraceError(trace, "governor.denied", "governor", errorKindRequestDenied, err, map[string]any{
			telemetry.AttrHecateGovernorResult: telemetry.ResultDenied,
		})
		return nil, fmt.Errorf("%w: %v", errDenied, err)
	}
	recordTrace(trace, "governor.allowed", "governor", map[string]any{
		telemetry.AttrHecateGovernorResult: telemetry.ResultSuccess,
	})

	rewrite := s.governor.RewriteResult(req)
	rewrittenReq := rewrite.Request
	if rewrite.Applied {
		attrs := map[string]any{
			telemetry.AttrGenAIRequestModel + ".original":  rewrite.OriginalModel,
			telemetry.AttrGenAIRequestModel + ".rewritten": rewrittenReq.Model,
		}
		if rewrite.PolicyRuleID != "" {
			attrs[telemetry.AttrHecatePolicyRuleID] = rewrite.PolicyRuleID
		}
		if rewrite.PolicyAction != "" {
			attrs[telemetry.AttrHecatePolicyAction] = rewrite.PolicyAction
		}
		if rewrite.PolicyReason != "" {
			attrs[telemetry.AttrHecatePolicyReason] = rewrite.PolicyReason
		}
		trace.Record("governor.model_rewrite", attrs)
	}

	decision, err := s.router.Route(ctx, rewrittenReq)
	if err != nil {
		recordTraceError(trace, "router.failed", "routing", errorKindRouterFailed, err, nil)
		return nil, fmt.Errorf("route request: %w", err)
	}
	recordTrace(trace, "router.selected", "routing", map[string]any{
		telemetry.AttrGenAIProviderName:  decision.Provider,
		telemetry.AttrGenAIRequestModel:  decision.Model,
		telemetry.AttrHecateRouteReason:  decision.Reason,
		telemetry.AttrHecateProviderKind: decision.ProviderKind,
	})

	preflight, err := s.preflight.Evaluate(ctx, rewrittenReq, decision)
	if err != nil {
		if preflightErr, ok := AsRoutePreflightError(err); ok {
			switch preflightErr.Kind {
			case RoutePreflightRouteDenied:
				recordRouteDeniedCandidate(trace, decision, preflightErr, 0)
				recordTraceError(trace, "governor.route_denied", "governor", errorKindRouteDenied, preflightErr, map[string]any{
					telemetry.AttrGenAIProviderName:            decision.Provider,
					telemetry.AttrHecateProviderKind:           preflightErr.ProviderKind,
					telemetry.AttrHecateCostEstimatedMicrosUSD: preflightErr.EstimatedCostMicros,
					telemetry.AttrHecateGovernorRouteResult:    telemetry.ResultDenied,
				})
				return nil, fmt.Errorf("%w: %v", errDenied, preflightErr.Err)
			}
		}
		if preflight == nil {
			return nil, err
		}
	}
	recordTrace(trace, "governor.route_allowed", "governor", map[string]any{
		telemetry.AttrGenAIProviderName:            decision.Provider,
		telemetry.AttrHecateProviderKind:           preflight.ProviderKind,
		telemetry.AttrHecateCostEstimatedMicrosUSD: preflight.EstimatedCost.TotalMicrosUSD,
		telemetry.AttrHecateGovernorRouteResult:    telemetry.ResultSuccess,
	})

	plan := &ExecutionPlan{
		OriginalRequest: req,
		Request:         rewrittenReq,
		Route:           decision,
		ProviderKind:    preflight.ProviderKind,
	}

	return plan, nil
}

func (s *Service) executePlan(ctx context.Context, trace *profiler.Trace, plan *ExecutionPlan) (*ChatResult, error) {
	callResult, err := s.executor.Execute(ctx, trace, plan.Request, plan.Route)
	if err != nil {
		if callResult == nil {
			return nil, err
		}
		// A provider may have received the request before returning an error.
		// Preserve that attempted execution identity even without a response so
		// Hecate can durably attribute image disclosure and correlate the trace.
		identity := models.BuildIdentity(plan.OriginalRequest.Model, callResult.Decision.Model)
		return &ChatResult{
			Metadata: buildResponseMetadata(plan, callResult, trace, identity, types.Usage{}, 0),
			Trace:    trace,
		}, err
	}
	result, err := s.finalizer.FinalizeExecution(ctx, trace, plan, callResult)
	if err != nil {
		return nil, err
	}
	return result, nil
}

type providerCallResult struct {
	Response             *types.ChatResponse
	Decision             types.RouteDecision
	ProviderKind         string
	AttemptCount         int
	RetryCount           int
	FallbackFromProvider string
}

func estimateUsage(req types.ChatRequest) types.Usage {
	promptTokens := prompttokens.EstimateMessages(req.Messages)
	completionTokens := req.MaxTokens
	if completionTokens < 0 {
		completionTokens = 0
	}
	return types.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}
}

func withResolvedModel(req types.ChatRequest, model string) types.ChatRequest {
	req.Model = model
	return req
}

// StreamHandle holds everything needed to execute a stream after routing succeeds.
type StreamHandle struct {
	Metadata  ResponseMetadata
	stream    func(w io.Writer) error
	attempted *atomic.Bool
}

// Execute writes the SSE stream to w.
func (h *StreamHandle) Execute(w io.Writer) error {
	return h.stream(w)
}

func (h *StreamHandle) providerCallAttempted() bool {
	return h != nil && h.attempted != nil && h.attempted.Load()
}

// StreamedContent holds the text accumulated during a streaming response.
type StreamedContent struct {
	ID           string
	Content      string
	FinishReason string
	Model        string
	ToolCalls    []types.ToolCall
}

// ExecuteAndCapture writes the SSE stream to w and simultaneously captures
// content deltas so the caller can record the turn after streaming completes.
func (h *StreamHandle) ExecuteAndCapture(w io.Writer) (StreamedContent, error) {
	return h.ExecuteAndCaptureDeltas(w, nil)
}

func (h *StreamHandle) ExecuteAndCaptureDeltas(w io.Writer, onContentDelta func(string)) (StreamedContent, error) {
	cap := &sseCapture{dst: w, onContentDelta: onContentDelta}
	err := h.stream(cap)
	return StreamedContent{
		ID:           cap.id,
		Content:      cap.content.String(),
		FinishReason: cap.finishReason,
		Model:        cap.model,
		ToolCalls:    cap.toolCalls(),
	}, err
}

// sseCapture wraps a writer and parses OpenAI-format SSE content deltas as
// they flow through, so the accumulated text is available after streaming ends.
type sseCapture struct {
	dst            io.Writer
	pending        []byte
	content        strings.Builder
	model          string
	id             string
	finishReason   string
	onContentDelta func(string)
	toolCallStates map[int]*streamedToolCall
}

type streamedToolCall struct {
	id        string
	callType  string
	name      string
	arguments strings.Builder
}

func (c *sseCapture) Write(p []byte) (int, error) {
	n, err := c.dst.Write(p)
	c.pending = append(c.pending, p[:n]...)
	c.drain()
	return n, err
}

func (c *sseCapture) drain() {
	for {
		idx := bytes.IndexByte(c.pending, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimRight(string(c.pending[:idx]), "\r")
		c.pending = c.pending[idx+1:]

		data, ok := sse.DataValue(line)
		if !ok {
			continue
		}
		if strings.TrimSpace(data) == "[DONE]" {
			break
		}
		var chunk struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
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
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.ID != "" && c.id == "" {
			c.id = chunk.ID
		}
		if chunk.Model != "" && c.model == "" {
			c.model = chunk.Model
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				c.content.WriteString(choice.Delta.Content)
				if c.onContentDelta != nil {
					c.onContentDelta(choice.Delta.Content)
				}
			}
			c.captureToolCallDeltas(choice.Delta.ToolCalls)
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				c.finishReason = *choice.FinishReason
			}
		}
	}
}

func (c *sseCapture) captureToolCallDeltas(deltas []struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}) {
	for _, delta := range deltas {
		if c.toolCallStates == nil {
			c.toolCallStates = make(map[int]*streamedToolCall)
		}
		state := c.toolCallStates[delta.Index]
		if state == nil {
			state = &streamedToolCall{}
			c.toolCallStates[delta.Index] = state
		}
		if delta.ID != "" {
			state.id = delta.ID
		}
		if delta.Type != "" {
			state.callType = delta.Type
		}
		if delta.Function.Name != "" {
			state.name = delta.Function.Name
		}
		if delta.Function.Arguments != "" {
			state.arguments.WriteString(delta.Function.Arguments)
		}
	}
}

func (c *sseCapture) toolCalls() []types.ToolCall {
	if len(c.toolCallStates) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(c.toolCallStates))
	for index := range c.toolCallStates {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	calls := make([]types.ToolCall, 0, len(indexes))
	for _, index := range indexes {
		state := c.toolCallStates[index]
		if state == nil || (state.id == "" && state.name == "" && state.arguments.Len() == 0) {
			continue
		}
		callType := state.callType
		if callType == "" {
			callType = "function"
		}
		calls = append(calls, types.ToolCall{
			ID:   state.id,
			Type: callType,
			Function: types.ToolCallFunction{
				Name:      state.name,
				Arguments: state.arguments.String(),
			},
		})
	}
	return calls
}

func (s *Service) HandleChatStreamCapture(ctx context.Context, req types.ChatRequest, onContentDelta func(string)) (*types.ChatResponse, error) {
	handle, _, err := s.RouteForStream(ctx, req)
	if err != nil {
		return nil, err
	}
	captured, err := handle.ExecuteAndCaptureDeltas(io.Discard, onContentDelta)
	if err != nil {
		if !handle.providerCallAttempted() {
			return nil, err
		}
		return streamRouteResponse(handle.Metadata, req.Model), err
	}
	model := captured.Model
	if model == "" {
		model = req.Model
	}
	finishReason := captured.FinishReason
	if finishReason == "" {
		finishReason = "stop"
	}
	id := captured.ID
	if id == "" {
		id = "chatcmpl-stream"
	}
	return &types.ChatResponse{
		ID:        id,
		Model:     model,
		CreatedAt: time.Now().UTC(),
		Route: types.RouteDecision{
			Provider:         handle.Metadata.Provider,
			ProviderKind:     handle.Metadata.ProviderKind,
			ProviderInstance: handle.Metadata.ProviderInstance,
			Model:            handle.Metadata.Model,
			Reason:           handle.Metadata.RouteReason,
		},
		Choices: []types.ChatChoice{{
			Index: 0,
			Message: types.Message{
				Role:      "assistant",
				Content:   captured.Content,
				ToolCalls: captured.ToolCalls,
			},
			FinishReason: finishReason,
		}},
	}, nil
}

func streamRouteResponse(metadata ResponseMetadata, fallbackModel string) *types.ChatResponse {
	model := metadata.Model
	if model == "" {
		model = fallbackModel
	}
	return &types.ChatResponse{
		Model: model,
		Route: types.RouteDecision{
			Provider:         metadata.Provider,
			ProviderKind:     metadata.ProviderKind,
			ProviderInstance: metadata.ProviderInstance,
			Model:            model,
			Reason:           metadata.RouteReason,
		},
	}
}

// RouteForStream runs governor/routing checks and returns a StreamHandle ready to
// write to any io.Writer. This lets the HTTP handler set response headers and status
// between routing and streaming, so errors during routing still produce JSON responses.
func (s *Service) RouteForStream(ctx context.Context, req types.ChatRequest) (*StreamHandle, context.Context, error) {
	trace := s.tracer.Start(req.RequestID)
	ctx = telemetry.WithTraceIDs(ctx, trace.TraceID, trace.RootSpanID())

	plan, err := s.buildExecutionPlan(ctx, trace, req)
	if err != nil {
		trace.Finalize()
		return nil, ctx, err
	}

	var instance providers.ProviderInstance
	var found bool
	if s.providers != nil {
		instance, found = s.providers.GetInstance(plan.Route.Provider)
	}
	if !found || instance.Provider == nil {
		trace.Finalize()
		return nil, ctx, fmt.Errorf("provider %q not found", plan.Route.Provider)
	}
	if err := validateProviderInstanceFence(plan.Request, plan.Route, instance.Identity); err != nil {
		trace.Finalize()
		return nil, ctx, err
	}
	p := instance.Provider

	streamer, ok := p.(providers.Streamer)
	if !ok {
		trace.Finalize()
		return nil, ctx, fmt.Errorf("provider %q does not support streaming", plan.Route.Provider)
	}

	if v, ok := p.(providers.Validator); ok {
		if err := v.Validate(); err != nil {
			trace.Finalize()
			return nil, ctx, fmt.Errorf("%w: %v", errClient, err)
		}
	}

	streamReq := plan.Request
	streamReq.Model = plan.Route.Model

	meta := ResponseMetadata{
		RequestID:        req.RequestID,
		Provider:         plan.Route.Provider,
		ProviderKind:     plan.ProviderKind,
		ProviderInstance: plan.Route.ProviderInstance,
		RouteReason:      plan.Route.Reason,
		RequestedModel:   req.Model,
		Model:            plan.Route.Model,
		TraceID:          trace.TraceID,
		SpanID:           trace.RootSpanID(),
	}

	attempted := &atomic.Bool{}
	handle := &StreamHandle{
		Metadata:  meta,
		attempted: attempted,
		stream: func(w io.Writer) error {
			defer trace.Finalize()

			dispatchInstance := instance
			dispatchStreamer := streamer
			if requiresProviderInstanceFence(streamReq) {
				var err error
				dispatchInstance, err = providerInstanceForDispatch(s.providers, streamReq, plan.Route)
				if err != nil {
					recordProviderCallBlocked(trace, plan.Route, 0, err)
					return err
				}
				currentStreamer, ok := dispatchInstance.Provider.(providers.Streamer)
				if !ok {
					return fmt.Errorf("provider %q does not support streaming", plan.Route.Provider)
				}
				if validator, ok := dispatchInstance.Provider.(providers.Validator); ok {
					if err := validator.Validate(); err != nil {
						return fmt.Errorf("%w: %v", errClient, err)
					}
				}
				dispatchStreamer = currentStreamer
			}
			dispatchRoute := plan.Route
			dispatchRoute.ProviderKind = plan.ProviderKind
			dispatchRoute.ProviderInstance = dispatchInstance.Identity
			if err := providerdispatch.RecordAttempt(ctx, dispatchRoute); err != nil {
				err = fmt.Errorf("record provider dispatch: %w", err)
				recordRichInputRouteFenceBlocked(trace, dispatchRoute, 0, err)
				return err
			}

			attempted.Store(true)
			return dispatchStreamer.ChatStream(ctx, streamReq, w)
		},
	}
	return handle, ctx, nil
}

func fallbackFrom(initialProvider, finalProvider string) string {
	if initialProvider == "" || initialProvider == finalProvider {
		return ""
	}
	return initialProvider
}

func normalizeResilienceOptions(options ResilienceOptions) ResilienceOptions {
	if options.MaxAttempts <= 0 {
		options.MaxAttempts = 1
	}
	if options.RetryBackoff <= 0 {
		options.RetryBackoff = 200 * time.Millisecond
	}
	return options
}

func validate(req types.ChatRequest) error {
	if len(req.Messages) == 0 {
		return errors.New("at least one message is required")
	}
	for _, msg := range req.Messages {
		if msg.Role == "" {
			return errors.New("message role is required")
		}
	}
	return nil
}

// captureRequestBody records a safe-by-default diagnostic snapshot of request
// messages into the distributed trace when HECATE_TRACE_BODIES=true.
func (s *Service) captureRequestBody(trace *profiler.Trace, req types.ChatRequest) {
	type capturedMessage struct {
		Role         string `json:"role"`
		Content      string `json:"content,omitempty"`
		ContentBytes int    `json:"content_bytes,omitempty"`
		Blocks       int    `json:"blocks,omitempty"`
		ToolCalls    int    `json:"tool_calls,omitempty"`
	}
	messageCount := len(req.Messages)
	captureCount := min(messageCount, traceBodyMaxItems)
	msgs := make([]capturedMessage, 0, captureCount)
	remaining := s.traceBodyMaxBytes
	for _, m := range req.Messages[:captureCount] {
		item := capturedMessage{
			Role:         m.Role,
			ContentBytes: len(m.Content),
			Blocks:       len(m.ContentBlocks),
			ToolCalls:    len(m.ToolCalls),
		}
		if s.traceBodyMode == traceBodyModeRedactedText {
			item.Content, remaining = redactedTraceContent(m.Content, remaining)
		}
		msgs = append(msgs, item)
		if s.traceBodyMode == traceBodyModeRedactedText && remaining <= 0 {
			break
		}
	}
	b, _ := json.Marshal(msgs)
	trace.Record("request.body.captured", map[string]any{
		"messages":          string(b),
		"message_count":     messageCount,
		"messages_captured": len(msgs),
		"truncated":         len(msgs) < messageCount || (s.traceBodyMode == traceBodyModeRedactedText && remaining <= 0),
		"model":             req.Model,
		"mode":              s.traceBodyMode,
	})
}

// captureResponseBody records a safe-by-default diagnostic snapshot of the
// response into the distributed trace when HECATE_TRACE_BODIES=true.
func (s *Service) captureResponseBody(trace *profiler.Trace, resp *types.ChatResponse) {
	if resp == nil || len(resp.Choices) == 0 {
		return
	}
	type capturedChoice struct {
		Role         string `json:"role"`
		Content      string `json:"content,omitempty"`
		ContentBytes int    `json:"content_bytes,omitempty"`
		FinishReason string `json:"finish_reason,omitempty"`
		ToolCalls    int    `json:"tool_calls,omitempty"`
	}
	choiceCount := len(resp.Choices)
	captureCount := min(choiceCount, traceBodyMaxItems)
	choices := make([]capturedChoice, 0, captureCount)
	remaining := s.traceBodyMaxBytes
	for _, c := range resp.Choices[:captureCount] {
		item := capturedChoice{
			Role:         c.Message.Role,
			ContentBytes: len(c.Message.Content),
			FinishReason: c.FinishReason,
			ToolCalls:    len(c.Message.ToolCalls),
		}
		if s.traceBodyMode == traceBodyModeRedactedText {
			item.Content, remaining = redactedTraceContent(c.Message.Content, remaining)
		}
		choices = append(choices, item)
		if s.traceBodyMode == traceBodyModeRedactedText && remaining <= 0 {
			break
		}
	}
	b, _ := json.Marshal(choices)
	trace.Record("response.body.captured", map[string]any{
		"choices":          string(b),
		"choice_count":     choiceCount,
		"choices_captured": len(choices),
		"truncated":        len(choices) < choiceCount || (s.traceBodyMode == traceBodyModeRedactedText && remaining <= 0),
		"model":            resp.Model,
		"mode":             s.traceBodyMode,
	})
}

func normalizeTraceBodyMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case traceBodyModeRedactedText:
		return traceBodyModeRedactedText
	default:
		return traceBodyModeMetadata
	}
}

func redactedTraceContent(content string, remaining int) (string, int) {
	content = redactSensitiveText(content)
	if remaining <= 0 {
		return "…[truncated]", 0
	}
	if len(content) > remaining {
		return content[:remaining] + "…[truncated]", 0
	}
	return content, remaining - len(content)
}

// redactSensitiveText masks patterns that look like secrets in captured bodies.
func redactSensitiveText(s string) string {
	// Simple heuristic: mask anything that looks like "key": "sk-..." or
	// "authorization": "Bearer ..." — exact fields are already stripped at
	// the HTTP layer; this is a belt-and-suspenders pass over message content.
	out := authorizationHeaderPattern.ReplaceAllString(s, "${1}[redacted]")
	out = bearerTokenPattern.ReplaceAllString(out, "${1}[redacted]")
	out = secretAssignmentPattern.ReplaceAllString(out, "${1}[redacted]")
	out = secretJSONFieldPattern.ReplaceAllString(out, "${1}[redacted]${3}")
	out = providerSecretPattern.ReplaceAllString(out, "[redacted]")
	return out
}

var (
	authorizationHeaderPattern = regexp.MustCompile(`(?i)(\bAuthorization\s*:\s*)(?:Bearer\s+)?[A-Za-z0-9._~+/=-]{12,}`)
	bearerTokenPattern         = regexp.MustCompile(`(?i)(\bBearer\s+)[A-Za-z0-9._~+/=-]{12,}`)
	// Environment and dotenv-style assignments commonly show up in prompts
	// when an operator pastes setup snippets. Keep the variable name visible
	// so traces remain useful without retaining the secret value.
	secretAssignmentPattern = regexp.MustCompile(`(?i)\b((?:OPENAI|ANTHROPIC|CLAUDE|CODEX|CURSOR|GITHUB|GITLAB|NPM|AWS|GOOGLE|AZURE|HECATE)?_?(?:API[_-]?KEY|AUTH[_-]?TOKEN|ACCESS[_-]?TOKEN|SECRET(?:_KEY)?|TOKEN|PASSWORD)\s*=\s*)[^\s"']+`)
	secretJSONFieldPattern  = regexp.MustCompile(`(?i)(["']?(?:api[_-]?key|auth[_-]?token|access[_-]?token|secret(?:[_-]?key)?|token|password|authorization)["']?\s*:\s*["']?)([^"',}\s]+)(["']?)`)
	// Provider setup tokens are often pasted as prose, not just as field
	// values. Match long sk-* values while leaving short words untouched.
	providerSecretPattern = regexp.MustCompile(`\bsk-[A-Za-z0-9][A-Za-z0-9._-]{16,}\b`)
)
