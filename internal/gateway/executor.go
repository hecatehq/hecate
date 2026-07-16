package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/policy"
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/router"
	"github.com/hecatehq/hecate/internal/safetext"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

type ProviderExecutor interface {
	Execute(ctx context.Context, trace *profiler.Trace, req types.ChatRequest, initial types.RouteDecision) (*providerCallResult, error)
}

type ResilientExecutor struct {
	router        router.Router
	preflight     RoutePreflight
	providers     providers.Registry
	healthTracker providers.HealthTracker
	history       providers.HealthHistoryStore
	metrics       *telemetry.Metrics
	options       ResilienceOptions
	sleep         func(context.Context, time.Duration) error
}

type failoverHistoryEntry struct {
	Provider           string
	Model              string
	Event              string
	Reason             string
	RouteReason        string
	RequestID          string
	TraceID            string
	PeerProvider       string
	PeerModel          string
	PeerRouteReason    string
	HealthStatus       string
	PeerHealthStatus   string
	Error              string
	ErrorClass         string
	AttemptCount       int
	EstimatedMicrosUSD int64
}

func NewResilientExecutor(
	router router.Router,
	preflight RoutePreflight,
	providers providers.Registry,
	healthTracker providers.HealthTracker,
	history providers.HealthHistoryStore,
	metrics *telemetry.Metrics,
	options ResilienceOptions,
) *ResilientExecutor {
	return &ResilientExecutor{
		router:        router,
		preflight:     preflight,
		providers:     providers,
		healthTracker: healthTracker,
		history:       history,
		metrics:       metrics,
		options:       normalizeResilienceOptions(options),
		sleep:         sleepContext,
	}
}

func (e *ResilientExecutor) Execute(ctx context.Context, trace *profiler.Trace, req types.ChatRequest, initial types.RouteDecision) (*providerCallResult, error) {
	candidates := []types.RouteDecision{initial}
	if e.options.FailoverEnabled && !req.Requirements.NoProviderFailover {
		candidates = append(candidates, e.router.Fallbacks(ctx, req, initial)...)
	}

	totalAttempts := 0
	totalRetries := 0
	var lastErr error
	var lastAttempt *providerCallResult

	for index, candidate := range candidates {
		recordTrace(trace, "router.candidate.considered", "routing", map[string]any{
			telemetry.AttrGenAIProviderName:          candidate.Provider,
			telemetry.AttrGenAIRequestModel:          candidate.Model,
			telemetry.AttrHecateProviderKind:         candidate.ProviderKind,
			telemetry.AttrHecateRouteReason:          candidate.Reason,
			telemetry.AttrHecateProviderIndex:        index,
			telemetry.AttrHecateRouteOutcome:         "considered",
			telemetry.AttrHecateProviderHealthStatus: healthStatus(e.healthTracker, candidate.Provider),
		})

		instance, ok := e.providers.GetInstance(candidate.Provider)
		if !ok {
			lastErr = fmt.Errorf("provider %q not found", candidate.Provider)
			recordTraceError(trace, "router.candidate.skipped", "routing", errorKindRouterFailed, lastErr, map[string]any{
				telemetry.AttrGenAIProviderName:          candidate.Provider,
				telemetry.AttrGenAIRequestModel:          candidate.Model,
				telemetry.AttrHecateProviderKind:         candidate.ProviderKind,
				telemetry.AttrHecateRouteReason:          candidate.Reason,
				telemetry.AttrHecateProviderIndex:        index,
				telemetry.AttrHecateRouteOutcome:         "skipped",
				telemetry.AttrHecateRouteSkipReason:      string(RoutePreflightProviderNotFound),
				telemetry.AttrHecateProviderHealthStatus: healthStatus(e.healthTracker, candidate.Provider),
			})
			e.appendFailoverTransitionHistory(ctx, candidates, index, failoverHistoryEntry{
				Provider:           candidate.Provider,
				Model:              candidate.Model,
				Event:              "failover_triggered",
				Reason:             string(RoutePreflightProviderNotFound),
				RouteReason:        candidate.Reason,
				HealthStatus:       healthStatus(e.healthTracker, candidate.Provider),
				Error:              safetext.ErrorMessage(lastErr),
				EstimatedMicrosUSD: 0,
			})
			continue
		}
		provider := instance.Provider

		var (
			preflight *RoutePreflightResult
			err       error
		)
		if fenceErr := validateProviderInstanceFence(req, candidate, instance.Identity); fenceErr != nil {
			err = &RoutePreflightError{
				Kind:         RoutePreflightProviderChanged,
				Provider:     candidate.Provider,
				Model:        candidate.Model,
				ProviderKind: string(provider.Kind()),
				Err:          fenceErr,
			}
		} else {
			preflight, err = e.preflight.Evaluate(ctx, req, candidate)
		}
		if err != nil {
			lastErr = err
			if preflightErr, ok := AsRoutePreflightError(err); ok {
				reason := string(preflightErr.Kind)
				if preflightErr.Kind == RoutePreflightRouteDenied {
					reason = classifyRouteDenied(preflightErr.Err)
					lastErr = fmt.Errorf("%w: %v", errDenied, preflightErr.Err)
				}
				eventName := "router.candidate.skipped"
				outcome := "skipped"
				if preflightErr.Kind == RoutePreflightRouteDenied {
					eventName = "router.candidate.denied"
					outcome = "denied"
				}
				recordTraceError(trace, eventName, "routing", reason, preflightErr, map[string]any{
					telemetry.AttrGenAIProviderName:            candidate.Provider,
					telemetry.AttrGenAIRequestModel:            candidate.Model,
					telemetry.AttrHecateProviderKind:           firstNonEmpty(preflightErr.ProviderKind, candidate.ProviderKind),
					telemetry.AttrHecateRouteReason:            candidate.Reason,
					telemetry.AttrHecateProviderIndex:          index,
					telemetry.AttrHecateRouteOutcome:           outcome,
					telemetry.AttrHecateRouteSkipReason:        reason,
					telemetry.AttrHecateProviderHealthStatus:   healthStatus(e.healthTracker, candidate.Provider),
					telemetry.AttrHecateCostEstimatedMicrosUSD: preflightErr.EstimatedCostMicros,
				})
				recordTraceError(trace, "provider.failover.skipped", "provider", reason, preflightErr, map[string]any{
					telemetry.AttrGenAIProviderName:            candidate.Provider,
					telemetry.AttrGenAIRequestModel:            candidate.Model,
					telemetry.AttrHecateFailoverReason:         reason,
					telemetry.AttrHecateCostEstimatedMicrosUSD: preflightErr.EstimatedCostMicros,
				})
				e.appendFailoverTransitionHistory(ctx, candidates, index, failoverHistoryEntry{
					Provider:           candidate.Provider,
					Model:              candidate.Model,
					Event:              "failover_triggered",
					Reason:             reason,
					RouteReason:        candidate.Reason,
					HealthStatus:       healthStatus(e.healthTracker, candidate.Provider),
					Error:              safetext.ErrorMessage(preflightErr),
					ErrorClass:         providers.HealthErrorClass(preflightErr.Err),
					EstimatedMicrosUSD: preflightErr.EstimatedCostMicros,
				})
				continue
			}
			if index == 0 {
				return nil, err
			}
			continue
		}

		if index > 0 {
			previous := candidates[index-1]
			recordTrace(trace, "provider.failover.selected", "provider", map[string]any{
				telemetry.AttrGenAIProviderName:            candidate.Provider,
				telemetry.AttrGenAIRequestModel:            candidate.Model,
				telemetry.AttrHecateProviderKind:           preflight.ProviderKind,
				telemetry.AttrHecateFailoverFromProvider:   previous.Provider,
				telemetry.AttrHecateFailoverFromModel:      previous.Model,
				telemetry.AttrHecateFailoverToProvider:     candidate.Provider,
				telemetry.AttrHecateFailoverToModel:        candidate.Model,
				telemetry.AttrHecateFailoverReason:         candidate.Reason,
				telemetry.AttrHecateProviderIndex:          index,
				telemetry.AttrHecateCostEstimatedMicrosUSD: preflight.EstimatedCost.TotalMicrosUSD,
			})
			e.appendFailoverHistory(ctx, failoverHistoryEntry{
				Provider:           candidate.Provider,
				Model:              candidate.Model,
				Event:              "failover_selected",
				Reason:             "candidate_selected",
				RouteReason:        candidate.Reason,
				PeerProvider:       previous.Provider,
				PeerModel:          previous.Model,
				PeerRouteReason:    previous.Reason,
				HealthStatus:       healthStatus(e.healthTracker, candidate.Provider),
				PeerHealthStatus:   healthStatus(e.healthTracker, previous.Provider),
				EstimatedMicrosUSD: preflight.EstimatedCost.TotalMicrosUSD,
			})
		}

		recordTrace(trace, "router.candidate.selected", "routing", map[string]any{
			telemetry.AttrGenAIProviderName:            candidate.Provider,
			telemetry.AttrGenAIRequestModel:            candidate.Model,
			telemetry.AttrHecateProviderKind:           preflight.ProviderKind,
			telemetry.AttrHecateRouteReason:            candidate.Reason,
			telemetry.AttrHecateProviderIndex:          index,
			telemetry.AttrHecateRouteOutcome:           "selected",
			telemetry.AttrHecateProviderHealthStatus:   healthStatus(e.healthTracker, candidate.Provider),
			telemetry.AttrHecateCostEstimatedMicrosUSD: preflight.EstimatedCost.TotalMicrosUSD,
		})

		attemptReq := withResolvedModel(req, candidate.Model)
		for attempt := 1; attempt <= e.options.MaxAttempts; attempt++ {
			dispatchProvider := provider
			if requiresProviderInstanceFence(req) {
				dispatchInstance, dispatchErr := providerInstanceForDispatch(e.providers, req, candidate)
				if dispatchErr != nil {
					lastErr = dispatchErr
					recordProviderCallBlocked(trace, candidate, index, dispatchErr)
					if lastAttempt != nil {
						lastAttempt.AttemptCount = totalAttempts
						lastAttempt.RetryCount = totalRetries
					}
					return lastAttempt, dispatchErr
				}
				dispatchProvider = dispatchInstance.Provider
			}
			totalAttempts++
			lastAttempt = &providerCallResult{
				Decision:             candidate,
				ProviderKind:         preflight.ProviderKind,
				AttemptCount:         totalAttempts,
				RetryCount:           totalRetries,
				FallbackFromProvider: fallbackFrom(initial.Provider, candidate.Provider),
			}
			recordTrace(trace, "provider.call.started", "provider", map[string]any{
				telemetry.AttrGenAIProviderName:      candidate.Provider,
				telemetry.AttrGenAIRequestModel:      candidate.Model,
				telemetry.AttrHecateRetryAttempt:     attempt,
				telemetry.AttrHecateProviderIndex:    index,
				telemetry.AttrHecateRetryMaxAttempts: e.options.MaxAttempts,
				telemetry.AttrHecateFailoverActive:   index > 0,
			})

			start := time.Now()
			resp, err := dispatchProvider.Chat(ctx, attemptReq)
			latency := time.Since(start)
			if err == nil {
				recordTrace(trace, "provider.call.finished", "provider", map[string]any{
					telemetry.AttrGenAIProviderName:       candidate.Provider,
					telemetry.AttrGenAIRequestModel:       candidate.Model,
					telemetry.AttrHecateRetryAttempt:      attempt,
					telemetry.AttrHecateProviderIndex:     index,
					telemetry.AttrHecateProviderLatencyMS: latency.Milliseconds(),
				})
				if e.healthTracker != nil {
					if contextual, ok := e.healthTracker.(providers.ContextualHealthTracker); ok {
						contextual.ObserveWithContext(ctx, candidate.Provider, providers.HealthObservation{Duration: latency})
					} else {
						e.healthTracker.Observe(candidate.Provider, providers.HealthObservation{Duration: latency})
					}
				}
				e.recordProviderCallMetric(ctx, candidate, preflight.ProviderKind, telemetry.ResultSuccess, attempt, latency)
				return &providerCallResult{
					Response:             resp,
					Decision:             candidate,
					ProviderKind:         preflight.ProviderKind,
					AttemptCount:         totalAttempts,
					RetryCount:           totalRetries,
					FallbackFromProvider: fallbackFrom(initial.Provider, candidate.Provider),
				}, nil
			}

			lastErr = fmt.Errorf("provider %s call failed: %w", candidate.Provider, err)
			recordTraceError(trace, "provider.call.failed", "provider", errorKindProviderCallFailed, err, map[string]any{
				telemetry.AttrGenAIProviderName:       candidate.Provider,
				telemetry.AttrGenAIRequestModel:       candidate.Model,
				telemetry.AttrHecateRetryAttempt:      attempt,
				telemetry.AttrHecateProviderIndex:     index,
				telemetry.AttrHecateRetryRetryable:    providers.IsRetryableError(err),
				telemetry.AttrHecateProviderLatencyMS: latency.Milliseconds(),
			})
			if e.healthTracker != nil {
				// Only count retryable errors (timeouts, 5xx) against provider health.
				// Non-retryable errors (auth failures, bad requests) mean the provider
				// is reachable — they must not trip the circuit breaker.
				var healthErr error
				if providers.IsRetryableError(err) {
					healthErr = err
				}
				observation := providers.HealthObservation{
					Duration: latency,
					Error:    healthErr,
				}
				if contextual, ok := e.healthTracker.(providers.ContextualHealthTracker); ok {
					contextual.ObserveWithContext(ctx, candidate.Provider, observation)
				} else {
					e.healthTracker.Observe(candidate.Provider, observation)
				}
			}
			e.recordProviderCallMetric(ctx, candidate, preflight.ProviderKind, telemetry.ResultError, attempt, latency)

			if !providers.IsRetryableError(err) {
				break
			}
			if attempt >= e.options.MaxAttempts {
				break
			}

			totalRetries++
			backoff := e.retryDelay(attempt)
			recordTrace(trace, "provider.retry.scheduled", "provider", map[string]any{
				telemetry.AttrGenAIProviderName:      candidate.Provider,
				telemetry.AttrGenAIRequestModel:      candidate.Model,
				telemetry.AttrHecateProviderIndex:    index,
				telemetry.AttrHecateRetryAttempt:     attempt,
				telemetry.AttrHecateRetryNextAttempt: attempt + 1,
				telemetry.AttrHecateRetryMaxAttempts: e.options.MaxAttempts,
				telemetry.AttrHecateRetryBackoffMS:   backoff.Milliseconds(),
				telemetry.AttrHecateFailoverActive:   index > 0,
			})
			if err := e.sleep(ctx, backoff); err != nil {
				lastAttempt.AttemptCount = totalAttempts
				lastAttempt.RetryCount = totalRetries
				recordTraceError(trace, "provider.retry.backoff_failed", "provider", errorKindRetryBackoffFailed, err, map[string]any{
					telemetry.AttrGenAIProviderName:    candidate.Provider,
					telemetry.AttrGenAIRequestModel:    candidate.Model,
					telemetry.AttrHecateRetryAttempt:   attempt,
					telemetry.AttrHecateRetryBackoffMS: backoff.Milliseconds(),
				})
				return lastAttempt, fmt.Errorf("wait for retry backoff: %w", err)
			}
		}

		if e.healthTracker != nil && providers.IsRetryableError(lastErr) {
			recordTraceError(trace, "provider.health.degraded", "provider", errorKindProviderHealth, lastErr, map[string]any{
				telemetry.AttrGenAIProviderName:          candidate.Provider,
				telemetry.AttrHecateProviderHealthStatus: string(e.healthTracker.State(candidate.Provider).Status),
			})
		}

		if index < len(candidates)-1 && providers.IsRetryableError(lastErr) {
			nextCandidate := candidates[index+1]
			recordTraceError(trace, telemetry.EventProviderFailoverTriggered, "provider", errorKindProviderCallFailed, lastErr, map[string]any{
				telemetry.AttrGenAIProviderName:          candidate.Provider,
				telemetry.AttrGenAIRequestModel:          candidate.Model,
				telemetry.AttrHecateFailoverFromProvider: candidate.Provider,
				telemetry.AttrHecateFailoverFromModel:    candidate.Model,
				telemetry.AttrHecateFailoverToProvider:   nextCandidate.Provider,
				telemetry.AttrHecateFailoverToModel:      nextCandidate.Model,
				telemetry.AttrHecateFailoverReason:       "provider_retry_exhausted",
				telemetry.AttrHecateProviderIndex:        index,
			})
			e.appendFailoverHistory(ctx, failoverHistoryEntry{
				Provider:         candidate.Provider,
				Model:            candidate.Model,
				Event:            "failover_triggered",
				Reason:           "provider_retry_exhausted",
				RouteReason:      candidate.Reason,
				PeerProvider:     nextCandidate.Provider,
				PeerModel:        nextCandidate.Model,
				PeerRouteReason:  nextCandidate.Reason,
				HealthStatus:     healthStatus(e.healthTracker, candidate.Provider),
				PeerHealthStatus: healthStatus(e.healthTracker, nextCandidate.Provider),
				Error:            safetext.ErrorMessage(lastErr),
				ErrorClass:       providers.HealthErrorClass(lastErr),
				AttemptCount:     e.options.MaxAttempts,
			})
			continue
		}
		break
	}

	if lastErr == nil {
		lastErr = errors.New("provider call failed")
	}
	if lastAttempt != nil {
		lastAttempt.AttemptCount = totalAttempts
		lastAttempt.RetryCount = totalRetries
	}
	return lastAttempt, lastErr
}

func (e *ResilientExecutor) recordProviderCallMetric(ctx context.Context, candidate types.RouteDecision, providerKind, result string, attempt int, latency time.Duration) {
	if e == nil || e.metrics == nil {
		return
	}
	e.metrics.RecordProviderCall(ctx, telemetry.ProviderCallMetricsRecord{
		Provider:     candidate.Provider,
		ProviderKind: providerKind,
		Model:        candidate.Model,
		Result:       result,
		Attempt:      attempt,
		HealthStatus: healthStatus(e.healthTracker, candidate.Provider),
		DurationMS:   latency.Milliseconds(),
	})
}

func healthStatus(tracker providers.HealthTracker, provider string) string {
	if tracker == nil {
		return ""
	}
	return string(tracker.State(provider).Status)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func classifyRouteDenied(err error) string {
	var policyErr *policy.Error
	if errors.As(err, &policyErr) {
		return "policy_denied"
	}
	message := lowerError(err)
	switch {
	case strings.Contains(message, "policy") || strings.Contains(message, "not allowed") || strings.Contains(message, "denied") || strings.Contains(message, "route mode"):
		return "policy_denied"
	default:
		return string(RoutePreflightRouteDenied)
	}
}

func lowerError(err error) string {
	return strings.ToLower(strings.TrimSpace(fmt.Sprint(err)))
}

func (e *ResilientExecutor) appendFailoverTransitionHistory(ctx context.Context, candidates []types.RouteDecision, index int, entry failoverHistoryEntry) {
	if index >= len(candidates)-1 {
		return
	}
	nextCandidate := candidates[index+1]
	entry.PeerProvider = nextCandidate.Provider
	entry.PeerModel = nextCandidate.Model
	entry.PeerRouteReason = nextCandidate.Reason
	entry.PeerHealthStatus = healthStatus(e.healthTracker, nextCandidate.Provider)
	e.appendFailoverHistory(ctx, entry)
}

func (e *ResilientExecutor) appendFailoverHistory(ctx context.Context, entry failoverHistoryEntry) {
	if e == nil || e.history == nil || entry.Provider == "" || entry.Event == "" {
		return
	}
	traceIDs := telemetry.TraceIDsFromContext(ctx)
	entry.Error = safetext.SanitizeErrorMessage(entry.Error)
	_ = e.history.Append(context.Background(), providers.HealthHistoryRecord{
		Provider:           entry.Provider,
		Model:              entry.Model,
		Event:              entry.Event,
		Status:             "failover",
		Error:              entry.Error,
		ErrorClass:         entry.ErrorClass,
		Reason:             entry.Reason,
		RouteReason:        entry.RouteReason,
		RequestID:          telemetry.RequestIDFromContext(ctx),
		TraceID:            traceIDs.TraceID,
		PeerProvider:       entry.PeerProvider,
		PeerModel:          entry.PeerModel,
		PeerRouteReason:    entry.PeerRouteReason,
		HealthStatus:       entry.HealthStatus,
		PeerHealthStatus:   entry.PeerHealthStatus,
		AttemptCount:       entry.AttemptCount,
		EstimatedMicrosUSD: entry.EstimatedMicrosUSD,
		Timestamp:          time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (e *ResilientExecutor) retryDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return e.options.RetryBackoff
	}
	return time.Duration(attempt) * e.options.RetryBackoff
}

func sleepContext(ctx context.Context, wait time.Duration) error {
	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
