package agentadapters

import (
	"context"
	"errors"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/hecatehq/hecate/internal/telemetry"
)

// TestAcpChatClientTerminalRPCsReturnTypedSentinel pins both the
// typed-error and counter contract for the five ACP terminal stubs.
// Adapters detect the case via errors.Is(err, ErrTerminalRPCUnsupported);
// dashboards count the case via the terminal_rpc_unsupported counter.
// A single test exercises every method so a regression on any one
// stub fails loudly.
func TestAcpChatClientTerminalRPCsReturnTypedSentinel(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	metrics, err := telemetry.NewAgentAdapterMetricsWithMeterProvider(provider)
	if err != nil {
		t.Fatalf("NewAgentAdapterMetricsWithMeterProvider: %v", err)
	}

	client := &acpChatClient{
		sessionID: "chat_test",
		adapterID: "codex",
		workspace: t.TempDir(),
		metrics:   metrics,
	}

	ctx := context.Background()
	cases := []struct {
		name   string
		method string
		call   func() error
	}{
		{
			name:   "CreateTerminal",
			method: "create",
			call: func() error {
				_, err := client.CreateTerminal(ctx, acp.CreateTerminalRequest{})
				return err
			},
		},
		{
			name:   "KillTerminal",
			method: "kill",
			call: func() error {
				_, err := client.KillTerminal(ctx, acp.KillTerminalRequest{})
				return err
			},
		},
		{
			name:   "TerminalOutput",
			method: "output",
			call: func() error {
				_, err := client.TerminalOutput(ctx, acp.TerminalOutputRequest{})
				return err
			},
		},
		{
			name:   "ReleaseTerminal",
			method: "release",
			call: func() error {
				_, err := client.ReleaseTerminal(ctx, acp.ReleaseTerminalRequest{})
				return err
			},
		},
		{
			name:   "WaitForTerminalExit",
			method: "wait",
			call: func() error {
				_, err := client.WaitForTerminalExit(ctx, acp.WaitForTerminalExitRequest{})
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatalf("%s: want error, got nil", tc.name)
			}
			if !errors.Is(err, ErrTerminalRPCUnsupported) {
				t.Fatalf("%s: errors.Is(err, ErrTerminalRPCUnsupported) = false; err = %v", tc.name, err)
			}
			var rpcErr *acp.RequestError
			if !errors.As(err, &rpcErr) {
				t.Fatalf("%s: errors.As(err, *acp.RequestError) = false; err = %v", tc.name, err)
			}
			if rpcErr.Code != -32601 {
				t.Fatalf("%s: RequestError.Code = %d, want -32601", tc.name, rpcErr.Code)
			}
			// The wrapped JSON-RPC code must surface so adapters that
			// don't know about Hecate's sentinel still classify via the
			// standard method-not-found code (-32601).
			if !strings.Contains(err.Error(), "-32601") {
				t.Errorf("%s: error message %q missing JSON-RPC -32601 method-not-found code", tc.name, err.Error())
			}
		})
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	counts := terminalRPCCountsByMethod(t, rm)
	for _, tc := range cases {
		if counts[tc.method] != 1 {
			t.Errorf("method %q counter = %d, want exactly 1", tc.method, counts[tc.method])
		}
	}
}

// TestAcpChatClientTerminalRPCsTolerateNilMetrics ensures the
// stubs are safe to invoke before metrics have been wired (e.g. in
// tests that build SessionManager directly without
// SetAdapterMetrics). The typed error must still fire — only the
// counter is suppressed.
func TestAcpChatClientTerminalRPCsTolerateNilMetrics(t *testing.T) {
	t.Parallel()

	client := &acpChatClient{
		sessionID: "chat_test",
		adapterID: "codex",
		workspace: t.TempDir(),
	}
	if _, err := client.CreateTerminal(context.Background(), acp.CreateTerminalRequest{}); err == nil {
		t.Fatal("CreateTerminal: want error, got nil")
	} else if !errors.Is(err, ErrTerminalRPCUnsupported) {
		t.Fatalf("CreateTerminal: errors.Is(err, ErrTerminalRPCUnsupported) = false; err = %v", err)
	}
}

func terminalRPCCountsByMethod(t *testing.T, rm metricdata.ResourceMetrics) map[string]int64 {
	t.Helper()
	out := make(map[string]int64)
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != telemetry.MetricAgentAdapterTerminalRPCUnsupportedTotal {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("metric %q has unexpected data type %T", m.Name, m.Data)
			}
			for _, dp := range sum.DataPoints {
				method, _ := dp.Attributes.Value(telemetry.AttrHecateAgentTerminalMethod)
				out[method.AsString()] += dp.Value
			}
		}
	}
	return out
}
