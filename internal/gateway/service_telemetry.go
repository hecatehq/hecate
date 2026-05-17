package gateway

import (
	"context"
	"fmt"

	"github.com/hecate/agent-runtime/internal/governor"
	"github.com/hecate/agent-runtime/internal/retention"
)

func (s *Service) UsageSummaryWithFilter(ctx context.Context, filter governor.UsageFilter) (*UsageSummaryResult, error) {
	summary, err := s.governor.UsageSummary(ctx, filter)
	if err != nil {
		return nil, err
	}
	return &UsageSummaryResult{Summary: summary}, nil
}

func (s *Service) UsageEvents(ctx context.Context, limit int) (*UsageEventsResult, error) {
	entries, err := s.governor.RecentUsageEvents(ctx, limit)
	if err != nil {
		return nil, err
	}
	return &UsageEventsResult{Entries: entries}, nil
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
