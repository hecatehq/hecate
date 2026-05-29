package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GatewayClient is the thin layer the MCP server uses to talk to a running
// Hecate gateway over its public REST API. Every tool dispatches
// through here — there's no shortcut into the gateway's in-process
// services because the MCP server is meant to run as a separate
// subprocess of the operator's MCP-aware editor.
type GatewayClient struct {
	BaseURL      string
	RuntimeToken string
	HTTPClient   *http.Client
}

// NewGatewayClient constructs a GatewayClient with a default 30-second
// timeout. The timeout is deliberately generous: queue-stat queries
// can be slow on a busy durable-store deploy, and the MCP client
// (Claude Desktop / Cursor) has its own user-facing wait UI.
func NewGatewayClient(baseURL string) *GatewayClient {
	return &GatewayClient{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *GatewayClient) SetRuntimeToken(token string) {
	c.RuntimeToken = strings.TrimSpace(token)
}

// Get issues a GET against `path` (joined onto BaseURL) and decodes
// the JSON body into out. `query` parameters are appended verbatim;
// empty values are dropped so callers can pass "" for optional fields.
func (c *GatewayClient) Get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

// Post issues a POST with a JSON body.
func (c *GatewayClient) Post(ctx context.Context, path string, body any, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, body, out)
}

func (c *GatewayClient) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	u := c.BaseURL + path
	if len(query) > 0 {
		u = u + "?" + cleanQuery(query).Encode()
	}

	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.RuntimeToken != "" {
		req.Header.Set("X-Hecate-Runtime-Token", c.RuntimeToken)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("call %s %s: %w", method, u, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		// Surface the gateway's error envelope verbatim so the MCP
		// caller sees the same diagnostic an HTTP-direct client would.
		// We cap the read at 64 KiB to bound memory if the gateway
		// returns a runaway response.
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return fmt.Errorf("hecate %s %s returned %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// cleanQuery drops empty values — callers pass "" for optional
// fields to keep call sites compact, and we don't want stray empty
// query keys hitting the gateway.
func cleanQuery(q url.Values) url.Values {
	out := url.Values{}
	for k, vs := range q {
		for _, v := range vs {
			if v == "" {
				continue
			}
			out.Add(k, v)
		}
	}
	return out
}
