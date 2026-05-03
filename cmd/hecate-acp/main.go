package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/acp"
	"github.com/hecate/agent-runtime/internal/version"
)

const (
	defaultGatewayURL  = "http://127.0.0.1:8765"
	defaultHTTPTimeout = 10 * time.Second
	maxMessageBytes    = 4 << 20
)

type bridgeConfig struct {
	GatewayURL    string
	APIKey        string
	AuthToken     string
	AgentName     string
	AgentVersion  string
	WorkspaceMode string
	ApprovalRoute string
}

func main() {
	cfg, err := configFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "hecate-acp: %v\n", err)
		os.Exit(1)
	}
	if err := run(context.Background(), os.Stdin, os.Stdout, os.Stderr, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "hecate-acp: %v\n", err)
		os.Exit(1)
	}
}

func configFromEnv() (bridgeConfig, error) {
	cfg := bridgeConfig{
		GatewayURL:    firstNonEmpty(os.Getenv("HECATE_GATEWAY_URL"), defaultGatewayURL),
		APIKey:        os.Getenv("HECATE_API_KEY"),
		AuthToken:     os.Getenv("HECATE_AUTH_TOKEN"),
		AgentName:     firstNonEmpty(os.Getenv("HECATE_AGENT_NAME"), "Hecate"),
		AgentVersion:  version.Version,
		WorkspaceMode: firstNonEmpty(os.Getenv("HECATE_WORKSPACE_MODE"), "hecate-owned"),
		ApprovalRoute: firstNonEmpty(os.Getenv("HECATE_APPROVAL_ROUTE"), "editor"),
	}
	if _, err := url.ParseRequestURI(cfg.GatewayURL); err != nil {
		return bridgeConfig{}, fmt.Errorf("invalid HECATE_GATEWAY_URL: %w", err)
	}
	return cfg, nil
}

func run(ctx context.Context, stdin io.Reader, stdout io.Writer, stderr io.Writer, cfg bridgeConfig) error {
	client, err := newGatewayHTTPClient(cfg)
	if err != nil {
		return err
	}
	dispatcher := acp.NewDispatcher(client, acp.NewSessionStore(), acp.Config{
		AgentName:     cfg.AgentName,
		AgentVersion:  cfg.AgentVersion,
		WorkspaceMode: cfg.WorkspaceMode,
		ApprovalRoute: cfg.ApprovalRoute,
	})

	fmt.Fprintf(stderr, "hecate-acp: started gateway=%s workspace_mode=%s approval_route=%s\n", cfg.GatewayURL, cfg.WorkspaceMode, cfg.ApprovalRoute)

	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)
	encoder := json.NewEncoder(stdout)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var req acp.Request
		if err := json.Unmarshal(line, &req); err != nil {
			if err := encoder.Encode(parseErrorResponse(err)); err != nil {
				return err
			}
			continue
		}
		if req.JSONRPC != acp.JSONRPCVersion || req.Method == "" {
			if err := encoder.Encode(invalidRequestResponse(req.ID)); err != nil {
				return err
			}
			continue
		}
		if resp := dispatcher.Handle(ctx, &req); resp != nil {
			if err := encoder.Encode(resp); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func parseErrorResponse(err error) acp.Response {
	return acp.Response{
		JSONRPC: acp.JSONRPCVersion,
		ID:      nil,
		Error: &acp.RPCError{
			Code:    acp.ErrorParse,
			Message: "invalid JSON-RPC request: " + err.Error(),
		},
	}
}

func invalidRequestResponse(id *json.RawMessage) acp.Response {
	return acp.Response{
		JSONRPC: acp.JSONRPCVersion,
		ID:      id,
		Error: &acp.RPCError{
			Code:    acp.ErrorInvalidRequest,
			Message: "request must be a JSON-RPC 2.0 object with a method",
		},
	}
}

type gatewayHTTPClient struct {
	baseURL    string
	apiKey     string
	authToken  string
	httpClient *http.Client
}

func newGatewayHTTPClient(cfg bridgeConfig) (*gatewayHTTPClient, error) {
	base, err := url.Parse(cfg.GatewayURL)
	if err != nil {
		return nil, fmt.Errorf("invalid gateway URL: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("gateway URL must use http or https")
	}
	return &gatewayHTTPClient{
		baseURL:    strings.TrimRight(base.String(), "/"),
		apiKey:     cfg.APIKey,
		authToken:  cfg.AuthToken,
		httpClient: &http.Client{Timeout: defaultHTTPTimeout},
	}, nil
}

func (c *gatewayHTTPClient) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	c.authorize(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("gateway returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		if strings.TrimSpace(item.ID) != "" {
			models = append(models, item.ID)
		}
	}
	return models, nil
}

func (c *gatewayHTTPClient) CreateAgentLoopTask(context.Context, acp.CreateTaskRequest) (acp.CreateTaskResult, error) {
	return acp.CreateTaskResult{}, errors.New("agent_loop task creation is not implemented in hecate-acp yet")
}

func (c *gatewayHTTPClient) ResumeTask(context.Context, string, string) (string, error) {
	return "", errors.New("task resume is not implemented in hecate-acp yet")
}

func (c *gatewayHTTPClient) CancelRun(context.Context, string, string) error {
	return errors.New("run cancellation is not implemented in hecate-acp yet")
}

func (c *gatewayHTTPClient) ResolveApproval(context.Context, string, string, string, acp.ApprovalDecision) error {
	return errors.New("approval resolution is not implemented in hecate-acp yet")
}

func (c *gatewayHTTPClient) StreamRunEvents(context.Context, string, string) (<-chan acp.RunEvent, error) {
	return nil, errors.New("run event streaming is not implemented in hecate-acp yet")
}

func (c *gatewayHTTPClient) authorize(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
	}
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
