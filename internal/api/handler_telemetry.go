package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/gateway"
	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/pkg/types"
)

// HandleTracesOrTrace dispatches /v1/traces requests: with a
// request_id query parameter it returns one trace; otherwise the recent
// list. Single-user mode merges the historic tenant-readable mirror
// path into the public /v1/traces surface.
func (h *Handler) HandleTracesOrTrace(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.URL.Query().Get("request_id")) != "" {
		h.HandleTrace(w, r)
		return
	}
	h.writeTraceList(w, r)
}

func (h *Handler) HandleTraces(w http.ResponseWriter, r *http.Request) {
	_, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	h.writeTraceList(w, r)
}

func (h *Handler) writeTraceList(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 && v <= 200 {
			limit = v
		}
	}

	result, err := h.service.ListTraces(r.Context(), limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}

	items := make([]TraceListItem, 0, len(result.Items))
	for _, t := range result.Items {
		item := TraceListItem{
			RequestID: t.RequestID,
			TraceID:   t.TraceID,
			SpanCount: len(t.Spans),
			Route: TraceRouteReportRecord{
				FinalProvider:     t.Route.FinalProvider,
				FinalProviderKind: t.Route.FinalProviderKind,
				FinalModel:        t.Route.FinalModel,
				FinalReason:       t.Route.FinalReason,
				FallbackFrom:      t.Route.FallbackFrom,
				Candidates:        renderTraceRouteCandidates(t.Route.Candidates),
			},
		}
		if !t.StartedAt.IsZero() {
			item.StartedAt = t.StartedAt.UTC().Format(time.RFC3339Nano)
		}
		// Derive duration and status from root span.
		for _, span := range t.Spans {
			if span.Name == "gateway.request" {
				if !span.StartTime.IsZero() && !span.EndTime.IsZero() {
					item.DurationMS = span.EndTime.Sub(span.StartTime).Milliseconds()
				}
				item.StatusCode = span.StatusCode
				item.StatusMessage = span.StatusMessage
				break
			}
		}
		items = append(items, item)
	}

	WriteJSON(w, http.StatusOK, TraceListResponse{Object: "list", Data: items})
}

func (h *Handler) HandleTrace(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAny(w, r)
	if !ok {
		return
	}
	ctx := h.contextWithPrincipal(r.Context(), principal)

	requestID := strings.TrimSpace(r.URL.Query().Get("request_id"))
	if requestID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "request_id query parameter is required")
		return
	}

	result, err := h.service.Trace(ctx, requestID)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.trace.fetch.failed",
			slog.String("event.name", "gateway.trace.fetch.failed"),
			slog.String(telemetry.AttrHecateTraceRequestID, requestID),
			slog.Any("error", err),
		)
		if gateway.IsClientError(err) {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
			return
		}
		WriteError(w, http.StatusNotFound, errCodeNotFound, err.Error())
		return
	}

	spans := make([]TraceSpanRecord, 0, len(result.Spans))
	for _, span := range result.Spans {
		eventItems := make([]TraceEventRecord, 0, len(span.Events))
		for _, event := range span.Events {
			eventItems = append(eventItems, TraceEventRecord{
				Name:       event.Name,
				Timestamp:  event.Timestamp.UTC().Format(time.RFC3339Nano),
				Attributes: event.Attributes,
			})
		}
		item := TraceSpanRecord{
			TraceID:       span.TraceID,
			SpanID:        span.SpanID,
			ParentSpanID:  span.ParentSpanID,
			Name:          span.Name,
			Kind:          span.Kind,
			Attributes:    span.Attributes,
			StatusCode:    span.StatusCode,
			StatusMessage: span.StatusMessage,
			Events:        eventItems,
		}
		if !span.StartTime.IsZero() {
			item.StartTime = span.StartTime.UTC().Format(time.RFC3339Nano)
		}
		if !span.EndTime.IsZero() {
			item.EndTime = span.EndTime.UTC().Format(time.RFC3339Nano)
		}
		spans = append(spans, item)
	}

	payload := TraceResponse{
		Object: "trace",
		Data: TraceResponseItem{
			RequestID: result.RequestID,
			TraceID:   result.TraceID,
			Spans:     spans,
			Route: TraceRouteReportRecord{
				FinalProvider:     result.Route.FinalProvider,
				FinalProviderKind: result.Route.FinalProviderKind,
				FinalModel:        result.Route.FinalModel,
				FinalReason:       result.Route.FinalReason,
				FallbackFrom:      result.Route.FallbackFrom,
				Candidates:        renderTraceRouteCandidates(result.Route.Candidates),
				Failovers:         renderTraceRouteFailovers(result.Route.Failovers),
			},
		},
	}
	if !result.StartedAt.IsZero() {
		payload.Data.StartedAt = result.StartedAt.UTC().Format(time.RFC3339Nano)
	}

	WriteJSON(w, http.StatusOK, payload)
}

func renderTraceRouteCandidates(candidates []types.RouteCandidateReport) []TraceRouteCandidateRecord {
	items := make([]TraceRouteCandidateRecord, 0, len(candidates))
	for _, candidate := range candidates {
		item := TraceRouteCandidateRecord{
			Provider:           candidate.Provider,
			ProviderKind:       candidate.ProviderKind,
			Model:              candidate.Model,
			Reason:             candidate.Reason,
			Outcome:            candidate.Outcome,
			SkipReason:         candidate.SkipReason,
			HealthStatus:       candidate.HealthStatus,
			PolicyRuleID:       candidate.PolicyRuleID,
			PolicyAction:       candidate.PolicyAction,
			PolicyReason:       candidate.PolicyReason,
			EstimatedMicrosUSD: candidate.EstimatedMicrosUSD,
			EstimatedUSD:       formatUSD(candidate.EstimatedMicrosUSD),
			Attempt:            candidate.Attempt,
			RetryCount:         candidate.RetryCount,
			Retryable:          candidate.Retryable,
			Index:              candidate.Index,
			LatencyMS:          candidate.LatencyMS,
			FailoverFrom:       candidate.FailoverFrom,
			FailoverTo:         candidate.FailoverTo,
			Detail:             candidate.Detail,
		}
		if !candidate.Timestamp.IsZero() {
			item.Timestamp = candidate.Timestamp.UTC().Format(time.RFC3339Nano)
		}
		items = append(items, item)
	}
	return items
}

func renderTraceRouteFailovers(failovers []types.RouteFailoverReport) []TraceRouteFailoverRecord {
	items := make([]TraceRouteFailoverRecord, 0, len(failovers))
	for _, failover := range failovers {
		item := TraceRouteFailoverRecord{
			FromProvider: failover.FromProvider,
			FromModel:    failover.FromModel,
			ToProvider:   failover.ToProvider,
			ToModel:      failover.ToModel,
			Reason:       failover.Reason,
		}
		if !failover.Timestamp.IsZero() {
			item.Timestamp = failover.Timestamp.UTC().Format(time.RFC3339Nano)
		}
		items = append(items, item)
	}
	return items
}
