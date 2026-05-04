//go:build e2e && docker

package e2e

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const otelCollectorImage = "otel/opentelemetry-collector-contrib:0.119.0"

// TestDockerOTelCollectorReceivesGRPCExport puts a real OpenTelemetry Collector
// between Hecate and the collector debug exporter. Hecate exports OTLP/gRPC to
// the collector; the collector renders received signals to logs. This catches
// exporter transport regressions and collector interoperability drift without
// relying on Docker-to-host callbacks that vary between Linux CI and Docker
// Desktop.
func TestDockerOTelCollectorReceivesGRPCExport(t *testing.T) {
	requireDocker(t)

	collectorGRPCPort := freePort(t)
	containerName := fmt.Sprintf("hecate-otel-collector-%d", time.Now().UnixNano())
	configPath := writeCollectorConfig(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	runArgs := []string{
		"run", "-d",
		"--name", containerName,
		"-p", fmt.Sprintf("127.0.0.1:%d:4317", collectorGRPCPort),
		"-v", configPath + ":/etc/otelcol/config.yaml:ro",
		otelCollectorImage,
		"--config=/etc/otelcol/config.yaml",
	}
	out, err := exec.CommandContext(ctx, "docker", runArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("start otel collector container: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		stopCtx, cancelStop := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelStop()
		_ = exec.CommandContext(stopCtx, "docker", "rm", "-f", containerName).Run()
	})
	waitTCP(t, fmt.Sprintf("127.0.0.1:%d", collectorGRPCPort), 30*time.Second)

	fakeResp := `{"id":"chatcmpl-otel-collector","object":"chat.completion","created":1700000000,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"collector ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":8,"total_tokens":13}}`
	upstream := fakeOpenAIServer(t, "/v1/chat/completions", fakeResp, false)
	base := gatewayServer(t,
		"PROVIDER_FAKE_API_KEY=dummy",
		"PROVIDER_FAKE_BASE_URL="+upstream,
		"PROVIDER_FAKE_DEFAULT_MODEL=gpt-4o-mini",
		"PROVIDER_FAKE_KIND=local",
		"GATEWAY_DEFAULT_MODEL=gpt-4o-mini",
		"GATEWAY_OTEL_TRANSPORT=grpc",
		"GATEWAY_OTEL_ENDPOINT=127.0.0.1:"+fmt.Sprint(collectorGRPCPort),
		"GATEWAY_OTEL_TRACES_ENABLED=true",
		"GATEWAY_OTEL_METRICS_ENABLED=true",
		"GATEWAY_OTEL_METRICS_INTERVAL=200ms",
	)

	resp := postJSON(t, base+"/v1/chat/completions", `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}`, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", resp.StatusCode, readBody(t, resp))
	}
	_, _ = io.Copy(io.Discard, resp.Body)

	if !waitForDockerLog(ctx, containerName, "gateway.provider", 15*time.Second) {
		t.Logf("otel collector logs:\n%s", dockerLogs(t, ctx, containerName))
		t.Fatal("collector did not log gateway.provider span")
	}
	if !waitForDockerLog(ctx, containerName, "hecate.provider.calls", 15*time.Second) {
		t.Logf("otel collector logs:\n%s", dockerLogs(t, ctx, containerName))
		t.Fatal("collector did not log hecate.provider.calls metric")
	}
}

func waitForDockerLog(ctx context.Context, containerName, substr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return false
		}
		out, err := exec.CommandContext(ctx, "docker", "logs", containerName).CombinedOutput()
		if err == nil && strings.Contains(string(out), substr) {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

func dockerLogs(t *testing.T, ctx context.Context, containerName string) string {
	t.Helper()
	out, err := exec.CommandContext(ctx, "docker", "logs", containerName).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("docker logs failed: %v\n%s", err, out)
	}
	return string(out)
}

func writeCollectorConfig(t *testing.T) string {
	t.Helper()
	config := `
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
      http:
        endpoint: 0.0.0.0:4318

exporters:
  debug:
    verbosity: detailed

service:
  pipelines:
    traces:
      receivers: [otlp]
      exporters: [debug]
    metrics:
      receivers: [otlp]
      exporters: [debug]
`

	path := filepath.Join(t.TempDir(), "otelcol.yaml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(config)+"\n"), 0o600); err != nil {
		t.Fatalf("write collector config: %v", err)
	}
	return path
}

func waitTCP(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("%s did not accept TCP connections within %s", addr, timeout)
}
