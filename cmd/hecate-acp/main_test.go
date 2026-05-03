package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayHTTPClientListModels(t *testing.T) {
	t.Parallel()

	var gotAPIKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		gotAPIKey = r.Header.Get("x-api-key")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o-mini"},{"id":"claude-sonnet-4-20250514"},{"id":""}]}`))
	}))
	defer srv.Close()

	client, err := newGatewayHTTPClient(bridgeConfig{
		GatewayURL: srv.URL,
		APIKey:     "tenant-key",
		AuthToken:  "admin-token",
	})
	if err != nil {
		t.Fatalf("newGatewayHTTPClient() error = %v", err)
	}
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if strings.Join(models, ",") != "gpt-4o-mini,claude-sonnet-4-20250514" {
		t.Fatalf("models = %#v", models)
	}
	if gotAPIKey != "tenant-key" {
		t.Fatalf("x-api-key = %q", gotAPIKey)
	}
	if gotAuth != "Bearer admin-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}

func TestRunInitializeOverStdio(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"llama3.1:8b"}]}`))
	}))
	defer srv.Close()

	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"0.1","clientCapabilities":{"permissions":{}}}}` + "\n")
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), input, &stdout, &stderr, bridgeConfig{
		GatewayURL:    srv.URL,
		AgentName:     "Hecate",
		AgentVersion:  "test",
		WorkspaceMode: "hecate-owned",
		ApprovalRoute: "editor",
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(stderr.String(), "hecate-acp: started") {
		t.Fatalf("stderr missing startup line: %q", stderr.String())
	}
	var resp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code int `json:"code"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, stdout.String())
	}
	if resp.Error != nil {
		t.Fatalf("response error = %+v", resp.Error)
	}
	var result struct {
		AvailableModels []struct {
			ID string `json:"id"`
		} `json:"availableModels"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(result.AvailableModels) != 1 || result.AvailableModels[0].ID != "llama3.1:8b" {
		t.Fatalf("availableModels = %#v", result.AvailableModels)
	}
}

func TestRunWritesParseError(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), strings.NewReader("{not-json}\n"), &stdout, &stderr, bridgeConfig{
		GatewayURL: defaultGatewayURL,
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	var resp struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, stdout.String())
	}
	if resp.Error == nil || resp.Error.Code != -32700 {
		t.Fatalf("error = %+v, want parse error", resp.Error)
	}
}
