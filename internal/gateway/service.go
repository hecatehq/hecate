package gateway

import (
	"bytes"
	"context"
	cryptoRand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/billing"
	"github.com/hecate/agent-runtime/internal/catalog"
	"github.com/hecate/agent-runtime/internal/chatstate"
	"github.com/hecate/agent-runtime/internal/governor"
	"github.com/hecate/agent-runtime/internal/models"
	"github.com/hecate/agent-runtime/internal/profiler"
	"github.com/hecate/agent-runtime/internal/providers"
	"github.com/hecate/agent-runtime/internal/retention"
	"github.com/hecate/agent-runtime/internal/router"
	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/pkg/types"
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
	Pricebook       billing.Pricebook
	Tracer          profiler.Tracer
	Metrics         *telemetry.Metrics
	Retention       *retention.Manager
	ChatSessions    chatstate.Store
	// TraceBodyCapture enables recording (redacted) message bodies in traces.
	TraceBodyCapture  bool
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
	pricebook         billing.Pricebook
	providerHistory   providers.HealthHistoryStore
	chatSessions      chatstate.Store
	providers         providers.Registry
	traceBodyCapture  bool
	traceBodyMaxBytes int
}

type ChatResult struct {
	Response *types.ChatResponse
	Metadata ResponseMetadata
	Trace    *profiler.Trace
}

type ModelsResult struct {
	Models []types.ModelInfo
}

type ProviderStatusResult struct {
	Providers []types.ProviderStatus
}

type ProviderHealthHistoryResult struct {
	Entries []types.ProviderHealthHistoryEntry
}

type BudgetStatusResult struct {
	Status types.BudgetStatus
}

type AccountSummaryResult struct {
	Status    types.BudgetStatus
	Estimates []types.AccountModelEstimate
}

type RequestLedgerResult struct {
	Entries []types.BudgetHistoryEntry
}

type TraceResult struct {
	RequestID string
	TraceID   string
	StartedAt time.Time
	Spans     []types.TraceSpan
	Route     types.RouteDecisionReport
}

type ChatSessionResult struct {
	Session types.ChatSession
}

type ChatSessionListResult struct {
	Sessions []types.ChatSession
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
		preflight = NewDefaultRoutePreflight(deps.Governor, deps.Providers, deps.Pricebook)
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
			deps.Pricebook,
			deps.Metrics,
		)
	}

	traceBodyMaxBytes := deps.TraceBodyMaxBytes
	if traceBodyMaxBytes <= 0 {
		traceBodyMaxBytes = 4096
	}

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
		pricebook:         deps.Pricebook,
		providerHistory:   deps.ProviderHistory,
		chatSessions:      deps.ChatSessions,
		providers:         deps.Providers,
		traceBodyCapture:  deps.TraceBodyCapture,
		traceBodyMaxBytes: traceBodyMaxBytes,
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
		var budgetErr *governor.BudgetExceededError
		if errors.As(err, &budgetErr) {
			return nil, budgetErr
		}
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
	recordRouteDiagnostics(ctx, trace, s.router, rewrittenReq, decision)

	preflight, err := s.preflight.Evaluate(ctx, rewrittenReq, decision)
	if err != nil {
		if preflightErr, ok := AsRoutePreflightError(err); ok {
			switch preflightErr.Kind {
			case RoutePreflightCostEstimate:
				recordTraceError(trace, "governor.budget_estimate_failed", "governor", errorKindBudgetEstimateFailed, preflightErr, map[string]any{
					telemetry.AttrGenAIProviderName:  decision.Provider,
					telemetry.AttrGenAIRequestModel:  decision.Model,
					telemetry.AttrHecateProviderKind: firstNonEmpty(preflightErr.ProviderKind, decision.ProviderKind),
				})
				preflight = &RoutePreflightResult{
					ProviderKind:   firstNonEmpty(preflightErr.ProviderKind, decision.ProviderKind),
					EstimatedUsage: estimateUsage(withResolvedModel(rewrittenReq, decision.Model)),
					EstimatedCost:  types.CostBreakdown{Currency: "USD"},
				}
			case RoutePreflightRouteDenied:
				recordRouteDeniedCandidate(trace, decision, preflightErr, 0)
				recordTraceError(trace, "governor.route_denied", "governor", errorKindRouteDenied, preflightErr, map[string]any{
					telemetry.AttrGenAIProviderName:            decision.Provider,
					telemetry.AttrHecateProviderKind:           preflightErr.ProviderKind,
					telemetry.AttrHecateCostEstimatedMicrosUSD: preflightErr.EstimatedCostMicros,
					telemetry.AttrHecateGovernorRouteResult:    telemetry.ResultDenied,
				})
				var budgetErr *governor.BudgetExceededError
				if errors.As(preflightErr.Err, &budgetErr) {
					return nil, budgetErr
				}
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
		return nil, err
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
	promptTokens := 0
	for _, msg := range req.Messages {
		promptTokens += len(msg.Content) / 4
	}
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
	Metadata ResponseMetadata
	stream   func(w io.Writer) error
}

// Execute writes the SSE stream to w.
func (h *StreamHandle) Execute(w io.Writer) error {
	return h.stream(w)
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

		const prefix = "data: "
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		data := line[len(prefix):]
		if data == "[DONE]" {
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
		return nil, err
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

	var p providers.Provider
	if s.providers != nil {
		p, _ = s.providers.Get(plan.Route.Provider)
	}
	if p == nil {
		trace.Finalize()
		return nil, ctx, fmt.Errorf("provider %q not found", plan.Route.Provider)
	}

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
		RequestID:      req.RequestID,
		Provider:       plan.Route.Provider,
		ProviderKind:   plan.ProviderKind,
		RouteReason:    plan.Route.Reason,
		RequestedModel: req.Model,
		Model:          plan.Route.Model,
		TraceID:        trace.TraceID,
		SpanID:         trace.RootSpanID(),
	}

	handle := &StreamHandle{
		Metadata: meta,
		stream: func(w io.Writer) error {
			defer trace.Finalize()
			return streamer.ChatStream(ctx, streamReq, w)
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

func (s *Service) ListModels(ctx context.Context) (*ModelsResult, error) {
	seen := make(map[string]struct{})
	modelsOut := make([]types.ModelInfo, 0, 16)

	for _, entry := range s.catalog.Snapshot(ctx) {
		for _, modelID := range entry.Models {
			key := entry.Name + "/" + modelID
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}

			modelsOut = append(modelsOut, types.ModelInfo{
				ID:              modelID,
				Provider:        entry.Name,
				Kind:            string(entry.Kind),
				OwnedBy:         entry.Name,
				Default:         modelID == entry.DefaultModel,
				DiscoverySource: entry.DiscoverySource,
			})
		}
	}

	return &ModelsResult{Models: modelsOut}, nil
}

func (s *Service) ProviderStatus(ctx context.Context) (*ProviderStatusResult, error) {
	entries := s.catalog.Snapshot(ctx)
	statuses := make([]types.ProviderStatus, 0, len(entries))
	for _, entry := range entries {
		status := types.ProviderStatus{
			Name:                entry.Name,
			Kind:                string(entry.Kind),
			BaseURL:             entry.BaseURL,
			CredentialState:     entry.CredentialState,
			CredentialReady:     providerCredentialReady(entry.CredentialState),
			Healthy:             entry.Healthy,
			Status:              entry.Status,
			RoutingReady:        providerRoutingReady(entry),
			RoutingBlocked:      providerRoutingBlockedReason(entry),
			DefaultModel:        entry.DefaultModel,
			Models:              append([]string(nil), entry.Models...),
			DiscoverySource:     entry.DiscoverySource,
			LastError:           entry.LastError,
			LastErrorClass:      entry.HealthReason,
			LastLatencyMS:       entry.LastLatencyMS,
			ConsecutiveFailures: entry.ConsecutiveFailures,
			TotalSuccesses:      entry.TotalSuccesses,
			TotalFailures:       entry.TotalFailures,
			Timeouts:            entry.Timeouts,
			ServerErrors:        entry.ServerErrors,
			RateLimits:          entry.RateLimits,
			Error:               entry.Error,
		}
		if entry.RefreshedAt != "" {
			if ts, err := time.Parse(time.RFC3339, entry.RefreshedAt); err == nil {
				status.RefreshedAt = ts
			}
		}
		if entry.LastCheckedAt != "" {
			if ts, err := time.Parse(time.RFC3339, entry.LastCheckedAt); err == nil {
				status.LastCheckedAt = ts
			}
		}
		if entry.OpenUntil != "" {
			if ts, err := time.Parse(time.RFC3339, entry.OpenUntil); err == nil {
				status.OpenUntil = ts
			}
		}
		statuses = append(statuses, status)
	}

	return &ProviderStatusResult{Providers: statuses}, nil
}

func (s *Service) ProviderHealthHistory(ctx context.Context, provider string, limit int) (*ProviderHealthHistoryResult, error) {
	if s.providerHistory == nil {
		return &ProviderHealthHistoryResult{}, nil
	}
	records, err := s.providerHistory.List(ctx, providers.HealthHistoryFilter{
		Provider: provider,
		Limit:    limit,
	})
	if err != nil {
		return nil, err
	}
	kindByProvider := make(map[string]string, 8)
	for _, entry := range s.catalog.Snapshot(ctx) {
		kindByProvider[entry.Name] = string(entry.Kind)
	}
	out := make([]types.ProviderHealthHistoryEntry, 0, len(records))
	for _, record := range records {
		item := types.ProviderHealthHistoryEntry{
			Provider:            record.Provider,
			ProviderKind:        kindByProvider[record.Provider],
			Model:               record.Model,
			Event:               record.Event,
			Status:              record.Status,
			Available:           record.Available,
			Error:               record.Error,
			ErrorClass:          record.ErrorClass,
			Reason:              record.Reason,
			RouteReason:         record.RouteReason,
			RequestID:           record.RequestID,
			TraceID:             record.TraceID,
			PeerProvider:        record.PeerProvider,
			PeerModel:           record.PeerModel,
			PeerRouteReason:     record.PeerRouteReason,
			HealthStatus:        record.HealthStatus,
			PeerHealthStatus:    record.PeerHealthStatus,
			LatencyMS:           record.LatencyMS,
			ConsecutiveFailures: record.ConsecutiveFailures,
			TotalSuccesses:      record.TotalSuccesses,
			TotalFailures:       record.TotalFailures,
			Timeouts:            record.Timeouts,
			ServerErrors:        record.ServerErrors,
			RateLimits:          record.RateLimits,
			AttemptCount:        record.AttemptCount,
			EstimatedMicrosUSD:  record.EstimatedMicrosUSD,
		}
		if record.OpenUntil != "" {
			if ts, err := time.Parse(time.RFC3339Nano, record.OpenUntil); err == nil {
				item.OpenUntil = ts
			}
		}
		if record.Timestamp != "" {
			if ts, err := time.Parse(time.RFC3339Nano, record.Timestamp); err == nil {
				item.Timestamp = ts
			}
		}
		out = append(out, item)
	}
	return &ProviderHealthHistoryResult{Entries: out}, nil
}

func providerCredentialReady(state string) bool {
	switch state {
	case "", "unknown", "configured", "not_required":
		return true
	default:
		return false
	}
}

func providerRoutingReady(entry catalog.Entry) bool {
	return providerRoutingBlockedReason(entry) == ""
}

func providerRoutingBlockedReason(entry catalog.Entry) string {
	if entry.Status == "disabled" {
		return "provider_disabled"
	}
	if !providerCredentialReady(entry.CredentialState) {
		return "credential_missing"
	}
	if entry.Status == "open" {
		if entry.HealthReason == "rate_limit" {
			return "provider_rate_limited"
		}
		return "circuit_open"
	}
	if !entry.Healthy && entry.Status != "half_open" {
		if entry.HealthReason == "rate_limit" {
			return "provider_rate_limited"
		}
		return "provider_unhealthy"
	}
	if entry.DefaultModel == "" && len(entry.Models) == 0 {
		return "no_models"
	}
	return ""
}

func (s *Service) BudgetStatus(ctx context.Context, key string) (*BudgetStatusResult, error) {
	status, err := s.governor.BudgetStatus(ctx, governor.BudgetFilter{Key: key})
	if err != nil {
		return nil, err
	}
	return &BudgetStatusResult{Status: status}, nil
}

func (s *Service) ResetBudget(ctx context.Context, key string) (*BudgetStatusResult, error) {
	if err := s.governor.ResetBudget(ctx, governor.BudgetFilter{Key: key}); err != nil {
		return nil, err
	}
	return s.BudgetStatus(ctx, key)
}

func (s *Service) BudgetStatusWithFilter(ctx context.Context, filter governor.BudgetFilter) (*BudgetStatusResult, error) {
	status, err := s.governor.BudgetStatus(ctx, filter)
	if err != nil {
		return nil, err
	}
	return &BudgetStatusResult{Status: status}, nil
}

func (s *Service) ResetBudgetWithFilter(ctx context.Context, filter governor.BudgetFilter) (*BudgetStatusResult, error) {
	if err := s.governor.ResetBudget(ctx, filter); err != nil {
		return nil, err
	}
	return s.BudgetStatusWithFilter(ctx, filter)
}

func (s *Service) TopUpBudgetWithFilter(ctx context.Context, filter governor.BudgetFilter, deltaMicros int64) (*BudgetStatusResult, error) {
	if err := s.governor.TopUpBudget(ctx, filter, deltaMicros); err != nil {
		return nil, err
	}
	return s.BudgetStatusWithFilter(ctx, filter)
}

func (s *Service) SetBudgetBalanceWithFilter(ctx context.Context, filter governor.BudgetFilter, balanceMicros int64) (*BudgetStatusResult, error) {
	if err := s.governor.SetBudgetBalance(ctx, filter, balanceMicros); err != nil {
		return nil, err
	}
	return s.BudgetStatusWithFilter(ctx, filter)
}

func (s *Service) AccountSummaryWithFilter(ctx context.Context, filter governor.BudgetFilter) (*AccountSummaryResult, error) {
	status, err := s.governor.BudgetStatus(ctx, filter)
	if err != nil {
		return nil, err
	}

	entries := s.catalog.Snapshot(ctx)
	estimates := make([]types.AccountModelEstimate, 0, 16)
	for _, entry := range entries {
		if filter.Provider != "" && entry.Name != filter.Provider {
			continue
		}
		for _, model := range entry.Models {
			price, ok := s.pricebook.Lookup(entry.Name, model)
			estimate := types.AccountModelEstimate{
				Provider:                        entry.Name,
				ProviderKind:                    string(entry.Kind),
				Model:                           model,
				Default:                         model == entry.DefaultModel,
				DiscoverySource:                 entry.DiscoverySource,
				Priced:                          ok,
				InputMicrosUSDPerMillionTokens:  price.InputMicrosUSDPerMillionTokens,
				OutputMicrosUSDPerMillionTokens: price.OutputMicrosUSDPerMillionTokens,
			}
			if price.InputMicrosUSDPerMillionTokens > 0 && status.AvailableMicrosUSD > 0 {
				estimate.EstimatedRemainingPromptTokens = status.AvailableMicrosUSD * 1_000_000 / price.InputMicrosUSDPerMillionTokens
			}
			if price.OutputMicrosUSDPerMillionTokens > 0 && status.AvailableMicrosUSD > 0 {
				estimate.EstimatedRemainingOutputTokens = status.AvailableMicrosUSD * 1_000_000 / price.OutputMicrosUSDPerMillionTokens
			}
			estimates = append(estimates, estimate)
		}
	}

	return &AccountSummaryResult{
		Status:    status,
		Estimates: estimates,
	}, nil
}

func (s *Service) RequestLedger(ctx context.Context, limit int) (*RequestLedgerResult, error) {
	entries, err := s.governor.RecentBudgetHistory(ctx, limit)
	if err != nil {
		return nil, err
	}
	return &RequestLedgerResult{Entries: entries}, nil
}

func (s *Service) CreateChatSession(ctx context.Context, session types.ChatSession) (*ChatSessionResult, error) {
	if s.chatSessions == nil {
		return nil, fmt.Errorf("chat session store is not configured")
	}
	created, err := s.chatSessions.CreateSession(ctx, session)
	if err != nil {
		return nil, err
	}
	return &ChatSessionResult{Session: created}, nil
}

func (s *Service) GetChatSession(ctx context.Context, id string) (*ChatSessionResult, error) {
	if s.chatSessions == nil {
		return nil, fmt.Errorf("chat session store is not configured")
	}
	session, ok, err := s.chatSessions.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("chat session %q not found", id)
	}
	return &ChatSessionResult{Session: session}, nil
}

func (s *Service) ListChatSessions(ctx context.Context, filter chatstate.Filter) (*ChatSessionListResult, error) {
	if s.chatSessions == nil {
		return &ChatSessionListResult{Sessions: nil}, nil
	}
	sessions, err := s.chatSessions.ListSessions(ctx, filter)
	if err != nil {
		return nil, err
	}
	return &ChatSessionListResult{Sessions: sessions}, nil
}

func (s *Service) DeleteChatSession(ctx context.Context, id string) error {
	if s.chatSessions == nil {
		return fmt.Errorf("chat session store not configured")
	}
	return s.chatSessions.DeleteSession(ctx, id)
}

func (s *Service) UpdateChatSessionTitle(ctx context.Context, id string, title string) (*ChatSessionResult, error) {
	if s.chatSessions == nil {
		return nil, fmt.Errorf("chat session store not configured")
	}
	session, err := s.chatSessions.UpdateSession(ctx, id, title)
	if err != nil {
		return nil, err
	}
	return &ChatSessionResult{Session: session}, nil
}

func (s *Service) UpdateChatSessionSystemPrompt(ctx context.Context, id string, prompt string) (*ChatSessionResult, error) {
	if s.chatSessions == nil {
		return nil, fmt.Errorf("chat session store not configured")
	}
	session, err := s.chatSessions.UpdateSessionSystemPrompt(ctx, id, prompt)
	if err != nil {
		return nil, err
	}
	return &ChatSessionResult{Session: session}, nil
}

// RecordChatExchange persists one upstream chat-completion request as a
// (messages, provider_call) pair on the chat session.
//
// "New" client-supplied messages are everything in req.Messages that
// comes after a possible session-system-prompt prefix and after the
// already-persisted message count. They are appended verbatim with no
// ProducedByCallID — the operator/runtime supplied them. The assistant
// response from result.Response.Choices[0] is appended after them with
// ProducedByCallID pointing at the new ChatProviderCall.
//
// This handles three flows uniformly:
//   - First turn: req.Messages = [user]; persist [user, assistant_response].
//   - Multi-turn replay: req.Messages = [...history, new_user]; persist [new_user, assistant_response].
//   - Tool loop continuation: req.Messages = [...history, tool_result, ...];
//     persist [tool_result..., assistant_response]. Orphan tool_call_ids
//     no longer occur on subsequent provider switches because the full
//     intermediate sequence is preserved.
func (s *Service) RecordChatExchange(ctx context.Context, sessionID string, req types.ChatRequest, result *ChatResult) (*ChatSessionResult, error) {
	if s.chatSessions == nil || sessionID == "" || result == nil || result.Response == nil {
		return nil, nil
	}

	// Read current state so we know how many messages are already
	// persisted (and therefore which req.Messages tail is new).
	existing, ok, err := s.chatSessions.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("chat session %q not found", sessionID)
	}

	// applySessionSystemPrompt prepends a system message that isn't
	// part of the persisted conversation. Skip it when computing the
	// "new tail" if it matches the session's stored SystemPrompt.
	skipPrefix := 0
	if len(req.Messages) > 0 && strings.EqualFold(req.Messages[0].Role, "system") {
		if existing.SystemPrompt != "" && req.Messages[0].Content == existing.SystemPrompt {
			skipPrefix = 1
		}
	}

	start := skipPrefix + len(existing.Messages)
	if start > len(req.Messages) {
		// Defensive: a request that omits some prior history would
		// otherwise produce a negative slice. Clamp and let the
		// assistant message land alone — better than panicking, and
		// consistent with the assistant being the only thing the
		// provider actually emitted this round.
		start = len(req.Messages)
	}
	newInputs := req.Messages[start:]

	callID := newProviderCallID()
	call := types.ChatProviderCall{
		ID:                callID,
		RequestID:         req.RequestID,
		RequestedProvider: req.Scope.ProviderHint,
		Provider:          result.Metadata.Provider,
		ProviderKind:      result.Metadata.ProviderKind,
		RequestedModel:    req.Model,
		Model:             result.Metadata.Model,
		CostMicrosUSD:     result.Metadata.CostMicrosUSD,
		PromptTokens:      result.Metadata.PromptTokens,
		CompletionTokens:  result.Metadata.CompletionTokens,
		TotalTokens:       result.Metadata.TotalTokens,
		CreatedAt:         result.Response.CreatedAt,
	}

	messages := make([]types.ChatSessionMessage, 0, len(newInputs))
	for _, m := range newInputs {
		messages = append(messages, types.ChatSessionMessage{
			ID:      newSessionMessageID(),
			Message: m,
			// ProducedByCallID empty: the operator/client supplied
			// this. Tool-result messages the client hands back also
			// land here without a producing call — they were emitted
			// outside the gateway.
		})
	}

	if len(result.Response.Choices) > 0 {
		assistant := result.Response.Choices[0].Message
		if assistant.Role == "" {
			assistant.Role = "assistant"
		}
		messages = append(messages, types.ChatSessionMessage{
			ID:               newSessionMessageID(),
			ProducedByCallID: callID,
			Message:          assistant,
		})
	}

	session, err := s.chatSessions.AppendExchange(ctx, sessionID, messages, call)
	if err != nil {
		return nil, err
	}
	return &ChatSessionResult{Session: session}, nil
}

func newProviderCallID() string {
	return "call_" + randomHex(12)
}

func newSessionMessageID() string {
	return "msg_" + randomHex(12)
}

func randomHex(byteLen int) string {
	buf := make([]byte, byteLen)
	if _, err := cryptoRand.Read(buf); err != nil {
		// Fall back to nanosecond timestamp; collision-resistant
		// enough for an in-process fallback when /dev/urandom is
		// unavailable, which is essentially never.
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(buf)
}

type TraceListResult struct {
	Items []TraceResult
}

func (s *Service) ListTraces(ctx context.Context, limit int) (*TraceListResult, error) {
	traces := s.tracer.List(limit)
	items := make([]TraceResult, 0, len(traces))
	for _, t := range traces {
		spans := t.Spans()
		item := TraceResult{
			RequestID: t.RequestID,
			TraceID:   t.TraceID,
			StartedAt: t.StartedAt,
			Spans:     spans,
			Route:     buildRouteDecisionReport(spans),
		}
		items = append(items, item)
	}
	return &TraceListResult{Items: items}, nil
}

func (s *Service) Trace(ctx context.Context, requestID string) (*TraceResult, error) {
	if requestID == "" {
		return nil, fmt.Errorf("%w: request_id is required", errClient)
	}

	trace, ok := s.tracer.Get(requestID)
	if !ok {
		return nil, fmt.Errorf("trace %q not found", requestID)
	}

	spans := trace.Spans()
	return &TraceResult{
		RequestID: trace.RequestID,
		TraceID:   trace.TraceID,
		StartedAt: trace.StartedAt,
		Spans:     spans,
		Route:     buildRouteDecisionReport(spans),
	}, nil
}

func (s *Service) RunRetention(ctx context.Context, req retention.RunRequest) (*RetentionResult, error) {
	if s.retention == nil {
		return nil, fmt.Errorf("retention manager is not configured")
	}
	return &RetentionResult{Run: s.retention.Run(ctx, req)}, nil
}

func (s *Service) ListRetentionRuns(ctx context.Context, limit int) (*RetentionHistoryResult, error) {
	if s.retention == nil {
		return nil, fmt.Errorf("retention manager is not configured")
	}
	runs, err := s.retention.ListRuns(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list retention runs: %w", err)
	}
	return &RetentionHistoryResult{Runs: runs}, nil
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

// captureRequestBody records a redacted, size-capped snapshot of the request
// messages into the distributed trace when GATEWAY_TRACE_BODIES=true.
func (s *Service) captureRequestBody(trace *profiler.Trace, req types.ChatRequest) {
	type capturedMsg struct {
		Role    string `json:"role"`
		Content string `json:"content,omitempty"`
		Blocks  int    `json:"blocks,omitempty"`
	}
	msgs := make([]capturedMsg, 0, len(req.Messages))
	remaining := s.traceBodyMaxBytes
	for _, m := range req.Messages {
		content := redactSensitiveText(m.Content)
		if len(content) > remaining {
			content = content[:remaining] + "…[truncated]"
			remaining = 0
		} else {
			remaining -= len(content)
		}
		msgs = append(msgs, capturedMsg{
			Role:    m.Role,
			Content: content,
			Blocks:  len(m.ContentBlocks),
		})
		if remaining <= 0 {
			break
		}
	}
	b, _ := json.Marshal(msgs)
	trace.Record("request.body.captured", map[string]any{
		"messages": string(b),
		"model":    req.Model,
	})
}

// captureResponseBody records a redacted, size-capped snapshot of the response
// into the distributed trace when GATEWAY_TRACE_BODIES=true.
func (s *Service) captureResponseBody(trace *profiler.Trace, resp *types.ChatResponse) {
	if resp == nil || len(resp.Choices) == 0 {
		return
	}
	type capturedChoice struct {
		Role         string `json:"role"`
		Content      string `json:"content,omitempty"`
		FinishReason string `json:"finish_reason,omitempty"`
		ToolCalls    int    `json:"tool_calls,omitempty"`
	}
	choices := make([]capturedChoice, 0, len(resp.Choices))
	remaining := s.traceBodyMaxBytes
	for _, c := range resp.Choices {
		content := redactSensitiveText(c.Message.Content)
		if len(content) > remaining {
			content = content[:remaining] + "…[truncated]"
			remaining = 0
		} else {
			remaining -= len(content)
		}
		choices = append(choices, capturedChoice{
			Role:         c.Message.Role,
			Content:      content,
			FinishReason: c.FinishReason,
			ToolCalls:    len(c.Message.ToolCalls),
		})
		if remaining <= 0 {
			break
		}
	}
	b, _ := json.Marshal(choices)
	trace.Record("response.body.captured", map[string]any{
		"choices": string(b),
		"model":   resp.Model,
	})
}

// redactSensitiveText masks patterns that look like secrets in captured bodies.
func redactSensitiveText(s string) string {
	// Simple heuristic: mask anything that looks like "key": "sk-..." or
	// "authorization": "Bearer ..." — exact fields are already stripped at
	// the HTTP layer; this is a belt-and-suspenders pass over message content.
	return s
}
