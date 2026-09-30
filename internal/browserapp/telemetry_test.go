package browserapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/browserrunner"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestServiceTelemetryNeverRecordsPathsOrBrowserDiagnostics(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := browserTracer
	browserTracer = provider.Tracer("browserapp-test")
	t.Cleanup(func() { browserTracer = previous; _ = provider.Shutdown(context.Background()) })
	s, path := fixtureService(t, NewMemoryStore())
	settings := enableFixture(t, s)
	s.newRunner = func(browserrunner.Config) (runner, error) {
		return fakeRunner{inspect: func(context.Context, browserrunner.InspectRequest) (browserrunner.InspectResult, error) {
			return browserrunner.InspectResult{}, errors.New("secret browser diagnostic " + path)
		}}, nil
	}
	_, _ = s.Inspect(context.Background(), browserrunner.InspectRequest{URL: "https://private.example/secret"})
	_, _ = s.Disable(context.Background())
	spans := exporter.GetSpans()
	if len(spans) < 5 {
		t.Fatalf("expected discovery, enable, admission, inspect, disable spans; got %d", len(spans))
	}
	for _, span := range spans {
		rendered := fmt.Sprint(span.Attributes, span.Status, span.Events)
		for _, forbidden := range []string{path, settings.Selected.ID, "secret", "private.example"} {
			if strings.Contains(rendered, forbidden) {
				t.Fatalf("span %q leaked %q: %s", span.Name, forbidden, rendered)
			}
		}
	}
}
