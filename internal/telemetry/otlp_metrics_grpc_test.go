package telemetry

import (
	"context"
	"net"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type metricExportReceipt struct {
	request *collectormetrics.ExportMetricsServiceRequest
	headers metadata.MD
}

type testMetricReceiver struct {
	collectormetrics.UnimplementedMetricsServiceServer
	received chan metricExportReceipt
}

func (r *testMetricReceiver) Export(ctx context.Context, request *collectormetrics.ExportMetricsServiceRequest) (*collectormetrics.ExportMetricsServiceResponse, error) {
	headers, _ := metadata.FromIncomingContext(ctx)
	r.received <- metricExportReceipt{request: request, headers: headers.Copy()}
	return &collectormetrics.ExportMetricsServiceResponse{}, nil
}

func TestMetricGRPCExporterDeliversValuesAndHeaders(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	receiver := &testMetricReceiver{received: make(chan metricExportReceipt, 1)}
	server := grpc.NewServer()
	collectormetrics.RegisterMetricsServiceServer(server, receiver)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); <-done })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exporter, err := newMetricExporter(ctx, OTelMetricOptions{
		Transport: OTLPTransportGRPC,
		Endpoint:  "http://" + listener.Addr().String(),
		Headers:   map[string]string{"x-hecate-test": "maintenance"},
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := exporter.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	now := time.Now()
	err = exporter.Export(ctx, &metricdata.ResourceMetrics{ScopeMetrics: []metricdata.ScopeMetrics{{Metrics: []metricdata.Metrics{{
		Name: "maintenance.counter",
		Data: metricdata.Sum[int64]{Temporality: metricdata.CumulativeTemporality, IsMonotonic: true, DataPoints: []metricdata.DataPoint[int64]{{StartTime: now, Time: now, Value: 42}}},
	}}}}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case receipt := <-receiver.received:
		if got := receipt.headers.Get("x-hecate-test"); len(got) != 1 || got[0] != "maintenance" {
			t.Fatalf("headers = %v", got)
		}
		resources := receipt.request.GetResourceMetrics()
		if len(resources) != 1 || len(resources[0].GetScopeMetrics()) != 1 || len(resources[0].GetScopeMetrics()[0].GetMetrics()) != 1 {
			t.Fatalf("metrics = %v", receipt.request)
		}
		metric := resources[0].GetScopeMetrics()[0].GetMetrics()[0]
		points := metric.GetSum().GetDataPoints()
		if metric.GetName() != "maintenance.counter" || len(points) != 1 || points[0].GetAsInt() != 42 {
			t.Fatalf("metric = %v", metric)
		}
	case <-ctx.Done():
		t.Fatal("receiver did not receive exported metrics")
	}
}
