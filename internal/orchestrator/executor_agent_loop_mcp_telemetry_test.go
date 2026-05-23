package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

// captureRunEvent is a tiny helper that turns each EmitRunEvent
// callback into a record on a slice. Tests assert on the slice
// after running the loop. Mutex-protected because the agent loop
// writes events from the same goroutine, but a future change might
// run dispatch concurrently.
type captureRunEvent struct {
	mu     sync.Mutex
	events []capturedEvent
}
type capturedEvent struct {
	Type string
	Data map[string]any
}

func (c *captureRunEvent) emit(eventType string, data map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, capturedEvent{Type: eventType, Data: data})
}

func (c *captureRunEvent) byType(t string) []capturedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []capturedEvent
	for _, e := range c.events {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

// newMetricsForTest wires a manual-reader OrchestratorMetrics so
// tests can both record AND inspect produced metrics without going
// through the global meter provider.
func newMetricsForTest(t *testing.T) (*telemetry.OrchestratorMetrics, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	m, err := telemetry.NewOrchestratorMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewOrchestratorMetricsWithMeterProvider: %v", err)
	}
	return m, reader
}

// findMetricSum is the same helper the telemetry package uses; copied
// here so the orchestrator test doesn't need to export it.
func findMetricSum(t *testing.T, reader *sdkmetric.ManualReader, name string) metricdata.Sum[int64] {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != name {
				continue
			}
			data, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("metric %q not a sum: %T", name, m.Data)
			}
			return data
		}
	}
	t.Fatalf("metric %q not found", name)
	return metricdata.Sum[int64]{}
}

// TestAgentLoop_MCPDispatch_EmitsTelemetry pins the success path:
// when the LLM asks for an MCP tool and the host dispatches cleanly,
// the agent loop emits a protocol-shaped tool.completed run event and
// a counter increment with result=dispatched.
func TestAgentLoop_MCPDispatch_EmitsTelemetry(t *testing.T) {
	t.Parallel()
	host := &fakeMCPHost{
		tools: []types.Tool{mcpTool("mcp__filesystem__read_file", "Read a file")},
		handlers: map[string]func(json.RawMessage) (string, bool, error){
			"mcp__filesystem__read_file": func(json.RawMessage) (string, bool, error) {
				return "contents", false, nil
			},
		},
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{
					Name: "mcp__filesystem__read_file", Arguments: `{"path":"x"}`,
				},
			})),
			makeChatResp(makeAssistantMsg("Read it.")),
		},
	}
	executor := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	executor.SetMCPHostFactory(func(_ context.Context, _ []types.MCPServerConfig) (AgentMCPHost, error) {
		return host, nil
	})
	metrics, reader := newMetricsForTest(t)
	executor.SetMetrics(metrics)

	cap := &captureRunEvent{}
	spec := newAgentLoopSpec(t)
	spec.Task.MCPServers = []types.MCPServerConfig{{Name: "filesystem", Command: "fake"}}
	spec.EmitRunEvent = cap.emit

	res, err := executor.Execute(context.Background(), spec)
	if err != nil || res.Status != "completed" {
		t.Fatalf("Execute: status=%q err=%v", res.Status, err)
	}

	completed := cap.byType(telemetry.EventMCPToolCompleted)
	if len(completed) != 1 {
		t.Fatalf("completed events = %d, want 1", len(completed))
	}
	if got := completed[0].Data["kind"]; got != "mcp" {
		t.Errorf("kind attr = %v, want mcp", got)
	}
	if got := completed[0].Data["tool_call_id"]; got != "c1" {
		t.Errorf("tool_call_id attr = %v, want c1", got)
	}
	if got := completed[0].Data["mcp_server"]; got != "filesystem" {
		t.Errorf("mcp_server attr = %v, want filesystem", got)
	}
	if got := completed[0].Data["mcp_tool"]; got != "read_file" {
		t.Errorf("mcp_tool attr = %v, want read_file", got)
	}
	if got := completed[0].Data["result"]; got != telemetry.MCPCallResultDispatched {
		t.Errorf("result attr = %v, want %q", got, telemetry.MCPCallResultDispatched)
	}

	// Counter increment with the same attributes.
	calls := findMetricSum(t, reader, telemetry.MetricOrchestratorMCPToolCallsTotal)
	if len(calls.DataPoints) != 1 {
		t.Fatalf("metric data points = %d, want 1", len(calls.DataPoints))
	}
	if calls.DataPoints[0].Value != 1 {
		t.Errorf("counter = %d, want 1", calls.DataPoints[0].Value)
	}
}

// TestAgentLoop_MCPBlock_EmitsBlockedEvent: block-policy short
// circuit emits policy.tool_blocked (NOT tool.completed) and the counter
// records result=blocked.
func TestAgentLoop_MCPBlock_EmitsBlockedEvent(t *testing.T) {
	t.Parallel()
	host := &fakeMCPHost{
		tools: []types.Tool{mcpTool("mcp__github__delete_repo", "Delete repo")},
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{
					Name: "mcp__github__delete_repo", Arguments: `{}`,
				},
			})),
			makeChatResp(makeAssistantMsg("Won't.")),
		},
	}
	executor := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	executor.SetMCPHostFactory(func(_ context.Context, _ []types.MCPServerConfig) (AgentMCPHost, error) {
		return host, nil
	})
	metrics, reader := newMetricsForTest(t)
	executor.SetMetrics(metrics)

	cap := &captureRunEvent{}
	spec := newAgentLoopSpec(t)
	spec.Task.MCPServers = []types.MCPServerConfig{
		{Name: "github", Command: "fake", ApprovalPolicy: types.MCPApprovalBlock},
	}
	spec.EmitRunEvent = cap.emit

	res, err := executor.Execute(context.Background(), spec)
	if err != nil || res.Status != "completed" {
		t.Fatalf("Execute: status=%q err=%v", res.Status, err)
	}

	blocked := cap.byType(telemetry.EventMCPToolBlocked)
	if len(blocked) != 1 {
		t.Fatalf("blocked events = %d, want 1", len(blocked))
	}
	if got := blocked[0].Data["result"]; got != telemetry.MCPCallResultBlocked {
		t.Errorf("result attr = %v, want %q", got, telemetry.MCPCallResultBlocked)
	}
	// Block path must NOT emit the dispatched event — that would
	// mislead operators who alert on dispatched calls.
	if completed := cap.byType(telemetry.EventMCPToolCompleted); len(completed) != 0 {
		t.Errorf("completed events = %d, want 0 on block", len(completed))
	}

	calls := findMetricSum(t, reader, telemetry.MetricOrchestratorMCPToolCallsTotal)
	if len(calls.DataPoints) != 1 {
		t.Fatalf("data points = %d, want 1", len(calls.DataPoints))
	}
	// Verify the result attribute is "blocked".
	got, ok := calls.DataPoints[0].Attributes.Value("hecate.mcp.call.result")
	if !ok || got.AsString() != telemetry.MCPCallResultBlocked {
		t.Errorf("result attr = %v ok=%v, want %q", got.AsString(), ok, telemetry.MCPCallResultBlocked)
	}
}

// TestAgentLoop_MCPTransportError_EmitsFailedEvent: a protocol-level
// failure from the host (host.Call returned err) emits
// tool.failed and counter result=failed.
func TestAgentLoop_MCPTransportError_EmitsFailedEvent(t *testing.T) {
	t.Parallel()
	host := &fakeMCPHost{
		tools: []types.Tool{mcpTool("mcp__remote__ping", "")},
		handlers: map[string]func(json.RawMessage) (string, bool, error){
			"mcp__remote__ping": func(json.RawMessage) (string, bool, error) {
				return "", false, errors.New("transport closed")
			},
		},
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{Name: "mcp__remote__ping", Arguments: `{}`},
			})),
			makeChatResp(makeAssistantMsg("Failed.")),
		},
	}
	executor := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	executor.SetMCPHostFactory(func(_ context.Context, _ []types.MCPServerConfig) (AgentMCPHost, error) {
		return host, nil
	})
	metrics, reader := newMetricsForTest(t)
	executor.SetMetrics(metrics)

	cap := &captureRunEvent{}
	spec := newAgentLoopSpec(t)
	spec.Task.MCPServers = []types.MCPServerConfig{{Name: "remote", Command: "fake"}}
	spec.EmitRunEvent = cap.emit

	if _, err := executor.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	failed := cap.byType(telemetry.EventMCPToolFailed)
	if len(failed) != 1 {
		t.Fatalf("failed events = %d, want 1", len(failed))
	}
	if got := failed[0].Data["error"]; got != "transport closed" {
		t.Errorf("error attr = %v, want 'transport closed'", got)
	}

	calls := findMetricSum(t, reader, telemetry.MetricOrchestratorMCPToolCallsTotal)
	got, ok := calls.DataPoints[0].Attributes.Value("hecate.mcp.call.result")
	if !ok || got.AsString() != telemetry.MCPCallResultFailed {
		t.Errorf("result attr = %v, want %q", got.AsString(), telemetry.MCPCallResultFailed)
	}
}

// TestAgentLoop_MCPToolError_RecordedAsToolError: an upstream-returned
// is_error=true is functionally a tool failure but a protocol success;
// it lands on tool.completed with result=tool_error so dashboards can
// chart "model errors" separately from "transport failures."
func TestAgentLoop_MCPToolError_RecordedAsToolError(t *testing.T) {
	t.Parallel()
	host := &fakeMCPHost{
		tools: []types.Tool{mcpTool("mcp__fs__read", "")},
		handlers: map[string]func(json.RawMessage) (string, bool, error){
			"mcp__fs__read": func(json.RawMessage) (string, bool, error) {
				return "permission denied", true, nil // upstream tool error
			},
		},
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{Name: "mcp__fs__read", Arguments: `{}`},
			})),
			makeChatResp(makeAssistantMsg("Recovered.")),
		},
	}
	executor := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	executor.SetMCPHostFactory(func(_ context.Context, _ []types.MCPServerConfig) (AgentMCPHost, error) {
		return host, nil
	})
	metrics, reader := newMetricsForTest(t)
	executor.SetMetrics(metrics)

	cap := &captureRunEvent{}
	spec := newAgentLoopSpec(t)
	spec.Task.MCPServers = []types.MCPServerConfig{{Name: "fs", Command: "fake"}}
	spec.EmitRunEvent = cap.emit

	if _, err := executor.Execute(context.Background(), spec); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	completed := cap.byType(telemetry.EventMCPToolCompleted)
	if len(completed) != 1 {
		t.Fatalf("completed events = %d, want 1", len(completed))
	}
	if got := completed[0].Data["result"]; got != telemetry.MCPCallResultToolError {
		t.Errorf("result attr = %v, want %q", got, telemetry.MCPCallResultToolError)
	}

	calls := findMetricSum(t, reader, telemetry.MetricOrchestratorMCPToolCallsTotal)
	got, ok := calls.DataPoints[0].Attributes.Value("hecate.mcp.call.result")
	if !ok || got.AsString() != telemetry.MCPCallResultToolError {
		t.Errorf("metric result = %v, want tool_error", got.AsString())
	}
}

// TestAgentLoop_MCPDispatch_NoMetrics_NoEmitRunEvent: nil-safe paths
// — neither metrics nor EmitRunEvent set is fine; the dispatch
// completes silently. Pinning so a future change doesn't accidentally
// require either.
func TestAgentLoop_MCPDispatch_NoMetrics_NoEmitRunEvent(t *testing.T) {
	t.Parallel()
	host := &fakeMCPHost{
		tools: []types.Tool{mcpTool("mcp__fs__ping", "")},
		handlers: map[string]func(json.RawMessage) (string, bool, error){
			"mcp__fs__ping": func(json.RawMessage) (string, bool, error) {
				return "pong", false, nil
			},
		},
	}
	llm := &scriptedLLM{
		responses: []*types.ChatResponse{
			makeChatResp(makeAssistantMsg("", types.ToolCall{
				ID: "c1", Type: "function",
				Function: types.ToolCallFunction{Name: "mcp__fs__ping", Arguments: `{}`},
			})),
			makeChatResp(makeAssistantMsg("ok")),
		},
	}
	executor := NewAgentLoopExecutor(llm, &stubExecutor{}, &stubExecutor{}, &stubExecutor{}, 4, nil, HTTPRequestPolicy{})
	executor.SetMCPHostFactory(func(_ context.Context, _ []types.MCPServerConfig) (AgentMCPHost, error) {
		return host, nil
	})
	// Neither SetMetrics nor spec.EmitRunEvent — must not panic.
	spec := newAgentLoopSpec(t)
	spec.Task.MCPServers = []types.MCPServerConfig{{Name: "fs", Command: "fake"}}

	res, err := executor.Execute(context.Background(), spec)
	if err != nil || res.Status != "completed" {
		t.Fatalf("Execute: status=%q err=%v", res.Status, err)
	}
}
