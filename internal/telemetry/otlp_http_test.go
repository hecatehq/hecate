package telemetry_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	collectorlog "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"
)

func TestOTLPHTTPExporterPaths(t *testing.T) {
	for _, signal := range []string{"traces", "metrics", "logs"} {
		for _, endpointPath := range []string{"", "/", "/custom/ingest"} {
			t.Run(signal+"/"+endpointPath, func(t *testing.T) {
				t.Parallel()
				paths := make(chan string, 8)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					paths <- r.URL.Path
					w.Header().Set("Content-Type", "application/x-protobuf")
				}))
				defer server.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				endpoint := server.URL + endpointPath
				switch signal {
				case "traces":
					provider, err := profiler.NewTracerProvider(ctx, profiler.TracerProviderOptions{Enabled: true, Endpoint: endpoint})
					if err != nil {
						t.Fatal(err)
					}
					_, span := provider.Tracer("test").Start(ctx, "export")
					span.End()
					if err := provider.Shutdown(ctx); err != nil {
						t.Fatal(err)
					}
				case "metrics":
					provider, shutdown, err := telemetry.NewMeterProvider(ctx, telemetry.OTelMetricOptions{Enabled: true, Endpoint: endpoint})
					if err != nil {
						t.Fatal(err)
					}
					counter, err := provider.Meter("test").Int64Counter("exports")
					if err != nil {
						t.Fatal(err)
					}
					counter.Add(ctx, 1)
					if err := shutdown(ctx); err != nil {
						t.Fatal(err)
					}
				case "logs":
					logger, shutdown, err := telemetry.NewLoggerWithOTLP(ctx, "info", telemetry.OTelLogOptions{Enabled: true, Endpoint: endpoint})
					if err != nil {
						t.Fatal(err)
					}
					logger.InfoContext(ctx, "export")
					if err := shutdown(ctx); err != nil {
						t.Fatal(err)
					}
				}
				want := endpointPath
				if want == "" {
					want = "/v1/" + signal
					// The log exporter already used the exact URL before this upgrade.
					if signal == "logs" {
						want = "/"
					}
				}
				select {
				case got := <-paths:
					if got != want {
						t.Fatalf("export path = %q, want %q", got, want)
					}
				default:
					t.Fatal("no export received")
				}
			})
		}
	}
}

func TestOTLPStructuredLogValues(t *testing.T) {
	t.Parallel()
	bodies := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		bodies <- body
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	logger, shutdown, err := telemetry.NewLoggerWithOTLP(ctx, "info", telemetry.OTelLogOptions{Enabled: true, Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	logger.WithGroup("request").InfoContext(ctx, "structured event",
		slog.Int("count", 2), slog.Bool("allowed", true), slog.Float64("cost", 0.5),
		slog.Any("tags", []string{"one", "two"}),
		slog.Any("details", map[string]any{"answer": int64(42)}),
		slog.Any("mixed", []any{true, int64(3)}),
		slog.Any("attribute", attribute.StringSlice("ignored", []string{"a", "b"})),
		slog.Group("nested", slog.String("name", "value")),
	)
	if err := shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	var request collectorlog.ExportLogsServiceRequest
	select {
	case body := <-bodies:
		if err := proto.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("no log export received")
	}
	if len(request.ResourceLogs) != 1 || len(request.ResourceLogs[0].ScopeLogs) != 1 || len(request.ResourceLogs[0].ScopeLogs[0].LogRecords) != 1 {
		t.Fatalf("unexpected export: %v", &request)
	}
	record := request.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
	if record.Body.GetStringValue() != "structured event" {
		t.Fatalf("body = %v", record.Body)
	}
	seen := map[string]bool{}
	for _, kv := range record.Attributes {
		seen[kv.Key] = true
		switch kv.Key {
		case "request.count":
			if kv.Value.GetIntValue() != 2 {
				t.Errorf("count = %v", kv.Value)
			}
		case "request.allowed":
			if !kv.Value.GetBoolValue() {
				t.Errorf("allowed = %v", kv.Value)
			}
		case "request.cost":
			if kv.Value.GetDoubleValue() != 0.5 {
				t.Errorf("cost = %v", kv.Value)
			}
		case "request.tags", "request.attribute":
			values := kv.Value.GetArrayValue().GetValues()
			want := []string{"one", "two"}
			if kv.Key == "request.attribute" {
				want = []string{"a", "b"}
			}
			if len(values) != 2 || values[0].GetStringValue() != want[0] || values[1].GetStringValue() != want[1] {
				t.Errorf("%s = %v", kv.Key, kv.Value)
			}
		case "request.mixed":
			values := kv.Value.GetArrayValue().GetValues()
			if len(values) != 2 || !values[0].GetBoolValue() || values[1].GetIntValue() != 3 {
				t.Errorf("mixed = %v", kv.Value)
			}
		case "request.details":
			values := kv.Value.GetKvlistValue().GetValues()
			if len(values) != 1 || values[0].Key != "answer" || values[0].Value.GetIntValue() != 42 {
				t.Errorf("details = %v", kv.Value)
			}
		case "request.nested.name":
			if kv.Value.GetStringValue() != "value" {
				t.Errorf("nested = %v", kv.Value)
			}
		}
	}
	for _, key := range []string{"count", "allowed", "cost", "tags", "attribute", "mixed", "details", "nested.name"} {
		if !seen["request."+key] {
			t.Errorf("missing attribute %q", key)
		}
	}
}
