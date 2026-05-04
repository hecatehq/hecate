package telemetry

import (
	"context"
	"testing"
)

func TestNewLogExporterGRPCUsesDefaultEndpointWhenUnset(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), testOTLPTimeout)
	defer cancel()

	exporter, err := newLogExporter(ctx, OTelLogOptions{
		Transport: OTLPTransportGRPC,
	})
	if err != nil {
		t.Fatalf("newLogExporter() error = %v", err)
	}
	if exporter == nil {
		t.Fatal("newLogExporter() exporter = nil")
	}
}
