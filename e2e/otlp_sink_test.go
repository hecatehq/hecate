//go:build e2e

package e2e

import (
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

// otlpSink is an in-process OTLP/HTTP receiver used by e2e tests. It records
// only signal names, which keeps the smoke focused on "did the exporter ship
// useful telemetry?" without depending on collector internals.
type otlpSink struct {
	ln      net.Listener
	srv     *http.Server
	mu      sync.Mutex
	spans   []string
	metrics []string
}

func newOTLPSink() *otlpSink {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("otlpSink listen: %v", err)
	}
	s := &otlpSink{ln: ln}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/traces", s.handleTraces)
	mux.HandleFunc("/v1/metrics", s.handleMetrics)
	s.srv = &http.Server{Handler: mux}
	go s.srv.Serve(ln) //nolint:errcheck
	return s
}

func (s *otlpSink) close() {
	if s == nil || s.srv == nil {
		return
	}
	_ = s.srv.Close()
}

func (s *otlpSink) addr() string { return s.ln.Addr().String() }

func (s *otlpSink) handleTraces(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req coltrace.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	for _, rs := range req.GetResourceSpans() {
		for _, ss := range rs.GetScopeSpans() {
			for _, span := range ss.GetSpans() {
				s.spans = append(s.spans, span.GetName())
			}
		}
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (s *otlpSink) handleMetrics(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req colmetrics.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	for _, rm := range req.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				s.metrics = append(s.metrics, m.GetName())
			}
		}
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (s *otlpSink) spanNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.spans))
	copy(out, s.spans)
	return out
}

func (s *otlpSink) metricNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.metrics))
	copy(out, s.metrics)
	return out
}

func (s *otlpSink) waitForSpan(substr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, name := range s.spanNames() {
			if strings.Contains(name, substr) {
				return true
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

func (s *otlpSink) waitForMetric(substr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, name := range s.metricNames() {
			if strings.Contains(name, substr) {
				return true
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}
