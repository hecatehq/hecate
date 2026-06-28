package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/internal/websearch"
	"github.com/hecatehq/hecate/pkg/types"
)

type webSearchArgs struct {
	Query      string `json:"query"`
	Count      int    `json:"count,omitempty"`
	Offset     int    `json:"offset,omitempty"`
	Freshness  string `json:"freshness,omitempty"`
	SafeSearch string `json:"safe_search,omitempty"`
	Country    string `json:"country,omitempty"`
	SearchLang string `json:"search_lang,omitempty"`
}

func (d *agentLoopToolDispatcher) webSearchTool(ctx context.Context, spec ExecutionSpec, args webSearchArgs, stepIndex int, startedAt time.Time, toolName string) (agentLoopToolDispatchResult, error) {
	if d == nil || d.webSearch == nil {
		return agentLoopToolDispatchResult{Text: "web_search: no web search provider configured"}, nil
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return agentLoopToolDispatchResult{Text: "web_search: query is required"}, nil
	}
	resp, err := d.webSearch.Search(ctx, websearch.Query{
		Query:      query,
		Count:      args.Count,
		Offset:     args.Offset,
		Freshness:  args.Freshness,
		SafeSearch: args.SafeSearch,
		Country:    args.Country,
		SearchLang: args.SearchLang,
	})
	if err != nil {
		return agentLoopToolDispatchResult{Text: fmt.Sprintf("web_search: %v", err)}, nil
	}
	step := buildWebSearchStep(spec, stepIndex, startedAt, toolName, resp)
	return agentLoopToolDispatchResult{Text: formatWebSearchResponse(resp), Step: &step}, nil
}

func formatWebSearchResponse(resp websearch.Response) string {
	var b strings.Builder
	fmt.Fprintf(&b, "provider=%s query=%q results=%d more_results_available=%v", resp.Provider, resp.Query, len(resp.Results), resp.MoreResultsAvailable)
	for i, item := range resp.Results {
		fmt.Fprintf(&b, "\n\n%d. %s\n%s", i+1, firstNonEmpty(strings.TrimSpace(item.Title), strings.TrimSpace(item.URL)), strings.TrimSpace(item.URL))
		if desc := strings.TrimSpace(item.Description); desc != "" {
			fmt.Fprintf(&b, "\n%s", desc)
		}
		for _, snippet := range item.ExtraSnippets {
			snippet = strings.TrimSpace(snippet)
			if snippet != "" && snippet != item.Description {
				fmt.Fprintf(&b, "\n- %s", snippet)
			}
		}
	}
	if len(resp.Results) == 0 {
		b.WriteString("\nNo results.")
	}
	return b.String()
}

func buildWebSearchStep(spec ExecutionSpec, index int, startedAt time.Time, toolName string, resp websearch.Response) types.TaskStep {
	return types.TaskStep{
		ID:       spec.NewID("step"),
		TaskID:   spec.Task.ID,
		RunID:    spec.Run.ID,
		Index:    index,
		Kind:     "tool",
		Title:    fmt.Sprintf("web_search %q", resp.Query),
		Status:   "completed",
		Phase:    "execution",
		Result:   telemetry.ResultSuccess,
		ToolName: toolName,
		Input: map[string]any{
			"query": resp.Query,
		},
		OutputSummary: map[string]any{
			"provider":               resp.Provider,
			"result_count":           len(resp.Results),
			"more_results_available": resp.MoreResultsAvailable,
		},
		StartedAt:  startedAt,
		FinishedAt: time.Now().UTC(),
		RequestID:  spec.RequestID,
		TraceID:    spec.TraceID,
	}
}
