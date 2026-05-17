package orchestrator

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/pkg/types"
)

// The HTTP tool is the agent's only outbound-network surface. It runs
// through e.httpClient (constructed once at executor init with the
// configured timeout) and applies three layers of safety:
//
//   1. Scheme allowlist — only http/https. file://, ftp://, gopher://
//      etc. are rejected outright.
//   2. SSRF guard — by default any host that resolves to a loopback,
//      private, or link-local IP is blocked (cf. RFC 1918 / 4193 /
//      6890). Operators flip GATEWAY_TASK_HTTP_ALLOW_PRIVATE_IPS=true
//      to permit this; useful for agents that hit the gateway's own
//      admin API or a sidecar service.
//   3. Hostname allowlist — when GATEWAY_TASK_HTTP_ALLOWED_HOSTS is
//      set, only those exact host names are reachable. Subdomains are
//      NOT inferred (api.openai.com vs openai.com) — operators write
//      what they mean.
//
// Response body is capped to MaxResponseBytes to keep prompts cheap.
// Truncation is reported in the tool result so the agent can ask for
// more if needed (e.g. via a follow-up call with a Range header).

type httpRequestArgs struct {
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

func (e *AgentLoopExecutor) httpRequestTool(ctx context.Context, spec ExecutionSpec, args httpRequestArgs, stepIndex int, startedAt time.Time, toolName string) (string, *types.TaskStep, []types.TaskArtifact, error) {
	method := strings.ToUpper(strings.TrimSpace(args.Method))
	if method == "" {
		method = "GET"
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD":
	default:
		return fmt.Sprintf("http_request: unsupported method %q", method), nil, nil, nil
	}

	parsed, err := url.Parse(strings.TrimSpace(args.URL))
	if err != nil {
		return fmt.Sprintf("http_request: invalid URL: %v", err), nil, nil, nil
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Sprintf("http_request: scheme %q is not allowed; use http or https", parsed.Scheme), nil, nil, nil
	}
	host := parsed.Hostname()
	if host == "" {
		return "http_request: URL has no host", nil, nil, nil
	}

	// Hostname allowlist — exact match only.
	if len(e.httpPolicy.AllowedHosts) > 0 {
		ok := false
		for _, h := range e.httpPolicy.AllowedHosts {
			if strings.EqualFold(strings.TrimSpace(h), host) {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Sprintf("http_request: host %q is not in the configured allowlist", host), nil, nil, nil
		}
	}

	// SSRF guard. Block loopback / private / link-local unless the
	// operator opted in. We resolve the host and check every address
	// — a hostname like `internal.example.com` could legitimately
	// resolve to 10.0.0.5, and we want to catch that, not just
	// literal IPs in the URL.
	if !e.httpPolicy.AllowPrivateIPs {
		if msg := checkPublicHost(ctx, host); msg != "" {
			return msg, nil, nil, nil
		}
	}

	var body io.Reader
	if args.Body != "" {
		body = strings.NewReader(args.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, parsed.String(), body)
	if err != nil {
		return fmt.Sprintf("http_request: build request: %v", err), nil, nil, nil
	}
	for k, v := range args.Headers {
		req.Header.Set(k, v)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return fmt.Sprintf("http_request: %v", err), nil, nil, nil
	}
	defer resp.Body.Close()

	max := e.httpPolicy.MaxResponseBytes
	limited := io.LimitReader(resp.Body, int64(max)+1) // +1 to detect overflow
	raw, _ := io.ReadAll(limited)
	truncated := false
	if len(raw) > max {
		raw = raw[:max]
		truncated = true
	}

	step := buildHTTPRequestStep(spec, stepIndex, startedAt, toolName, method, parsed.String(), resp.StatusCode, len(raw), truncated)

	var b strings.Builder
	fmt.Fprintf(&b, "status=%d url=%s bytes=%d", resp.StatusCode, parsed.String(), len(raw))
	if truncated {
		fmt.Fprintf(&b, " truncated=true")
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		fmt.Fprintf(&b, " content_type=%s", ct)
	}
	b.WriteString("\n--- body ---\n")
	b.Write(raw)
	if truncated {
		fmt.Fprintf(&b, "\n…(truncated at %d bytes; configure GATEWAY_TASK_HTTP_MAX_RESPONSE_BYTES to widen)", max)
	}
	return b.String(), &step, nil, nil
}
