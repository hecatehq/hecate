package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/hecate/agent-runtime/internal/acp"
	"github.com/hecate/agent-runtime/internal/version"
)

const (
	defaultGatewayURL    = "http://127.0.0.1:8765"
	defaultHTTPTimeout   = 10 * time.Second
	discoveryHTTPTimeout = 500 * time.Millisecond
	nativeAppID          = "io.github.chicoxyzzy.hecate"
	gatewayStateFileName = "gateway-state.json"
	maxMessageBytes      = 4 << 20
)

type bridgeConfig struct {
	GatewayURL    string
	AgentName     string
	AgentVersion  string
	WorkspaceMode string
	ApprovalRoute string
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v", "version":
			fmt.Println(version.Version)
			return
		}
	}
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
	gatewayURL := strings.TrimSpace(os.Getenv("HECATE_GATEWAY_URL"))
	if gatewayURL == "" {
		gatewayURL = discoverGatewayURL()
	}
	cfg := bridgeConfig{
		GatewayURL:    firstNonEmpty(gatewayURL, defaultGatewayURL),
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

func discoverGatewayURL() string {
	return discoverGatewayURLFromStatePaths(gatewayStateCandidatePaths(), gatewayHealthy)
}

func discoverGatewayURLFromStatePaths(paths []string, healthy func(string) bool) string {
	for _, statePath := range paths {
		baseURL, err := gatewayURLFromStateFile(statePath)
		if err != nil {
			continue
		}
		if healthy(baseURL) {
			return baseURL
		}
	}
	return defaultGatewayURL
}

func gatewayStateCandidatePaths() []string {
	var candidates []string
	if dataDir := strings.TrimSpace(os.Getenv("GATEWAY_DATA_DIR")); dataDir != "" {
		candidates = append(candidates, filepath.Join(dataDir, gatewayStateFileName))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, ".data", gatewayStateFileName))
	}
	if nativePath, err := nativeGatewayStatePath(); err == nil {
		candidates = append(candidates, nativePath)
	}
	return uniqueNonEmptyStrings(candidates)
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func nativeGatewayStatePath() (string, error) {
	dir, err := nativeAppDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, gatewayStateFileName), nil
}

func nativeAppDataDir() (string, error) {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		if home == "" {
			return "", fmt.Errorf("home directory not found")
		}
		return filepath.Join(home, "Library", "Application Support", nativeAppID), nil
	case "windows":
		if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
			return filepath.Join(appData, nativeAppID), nil
		}
		if home == "" {
			return "", fmt.Errorf("home directory not found")
		}
		return filepath.Join(home, "AppData", "Roaming", nativeAppID), nil
	default:
		if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
			return filepath.Join(xdg, nativeAppID), nil
		}
		if home == "" {
			return "", fmt.Errorf("home directory not found")
		}
		return filepath.Join(home, ".local", "share", nativeAppID), nil
	}
}

func gatewayURLFromStateFile(statePath string) (string, error) {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return "", err
	}
	var state struct {
		BaseURL string `json:"base_url"`
	}
	baseURL := ""
	if err := json.Unmarshal(raw, &state); err == nil {
		baseURL = state.BaseURL
	} else {
		return "", err
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	u, err := url.ParseRequestURI(baseURL)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("gateway URL must use http or https")
	}
	if u.Host == "" {
		return "", fmt.Errorf("gateway URL is missing host")
	}
	return baseURL, nil
}

func gatewayHealthy(baseURL string) bool {
	client := http.Client{Timeout: discoveryHTTPTimeout}
	resp, err := client.Get(strings.TrimRight(baseURL, "/") + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
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

	writeCh := make(chan any, 32)
	var writerWG sync.WaitGroup
	var writerErr error
	writerWG.Add(1)
	go func() {
		defer writerWG.Done()
		encoder := json.NewEncoder(stdout)
		for msg := range writeCh {
			if err := encoder.Encode(msg); err != nil {
				writerErr = err
				return
			}
		}
	}()
	dispatcher.SetEmitter(func(req *acp.Request) {
		defer func() { _ = recover() }()
		select {
		case writeCh <- req:
		case <-ctx.Done():
		}
	})

	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var envelope struct {
			JSONRPC string           `json:"jsonrpc"`
			ID      *json.RawMessage `json:"id,omitempty"`
			Method  string           `json:"method,omitempty"`
			Result  json.RawMessage  `json:"result,omitempty"`
			Error   *acp.RPCError    `json:"error,omitempty"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			writeCh <- parseErrorResponse(err)
			continue
		}
		if envelope.JSONRPC != acp.JSONRPCVersion {
			writeCh <- invalidRequestResponse(envelope.ID)
			continue
		}
		if envelope.Method == "" {
			if envelope.ID == nil {
				writeCh <- invalidRequestResponse(envelope.ID)
				continue
			}
			dispatcher.HandleResponse(ctx, &acp.Response{
				JSONRPC: envelope.JSONRPC,
				ID:      envelope.ID,
				Result:  envelope.Result,
				Error:   envelope.Error,
			})
			continue
		}
		var req acp.Request
		if err := json.Unmarshal(line, &req); err != nil {
			writeCh <- parseErrorResponse(err)
			continue
		}
		if resp := dispatcher.Handle(ctx, &req); resp != nil {
			writeCh <- resp
		}
	}
	if err := scanner.Err(); err != nil {
		close(writeCh)
		writerWG.Wait()
		return err
	}
	close(writeCh)
	writerWG.Wait()
	return writerErr
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
		httpClient: &http.Client{Timeout: defaultHTTPTimeout},
	}, nil
}

func (c *gatewayHTTPClient) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
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

func (c *gatewayHTTPClient) CreateAgentLoopTask(ctx context.Context, request acp.CreateTaskRequest) (acp.CreateTaskResult, error) {
	body := map[string]any{
		"title":             "ACP session",
		"prompt":            request.Prompt,
		"execution_kind":    "agent_loop",
		"execution_profile": "coding_agent",
		"workspace_mode":    "persistent",
	}
	if request.Model != "" {
		body["requested_model"] = request.Model
	}
	if request.WorkingDirectory != "" {
		body["working_directory"] = request.WorkingDirectory
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/tasks", body, &created); err != nil {
		return acp.CreateTaskResult{}, err
	}
	if created.Data.ID == "" {
		return acp.CreateTaskResult{}, fmt.Errorf("gateway task create response missing task id")
	}
	var started struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/tasks/"+url.PathEscape(created.Data.ID)+"/start", map[string]any{}, &started); err != nil {
		return acp.CreateTaskResult{}, err
	}
	if started.Data.ID == "" {
		return acp.CreateTaskResult{}, fmt.Errorf("gateway task start response missing run id")
	}
	return acp.CreateTaskResult{TaskID: created.Data.ID, RunID: started.Data.ID}, nil
}

func (c *gatewayHTTPClient) ContinueAgentLoopTask(ctx context.Context, taskID, runID, prompt string) (string, error) {
	var continued struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	path := "/v1/tasks/" + url.PathEscape(taskID) + "/runs/" + url.PathEscape(runID) + "/continue"
	if err := c.doJSON(ctx, http.MethodPost, path, map[string]any{"prompt": prompt}, &continued); err != nil {
		return "", err
	}
	if continued.Data.ID == "" {
		return "", fmt.Errorf("gateway task continue response missing run id")
	}
	return continued.Data.ID, nil
}

func (c *gatewayHTTPClient) CancelRun(ctx context.Context, taskID, runID, reason string) error {
	var ignored map[string]any
	return c.doJSON(ctx, http.MethodPost, "/v1/tasks/"+url.PathEscape(taskID)+"/runs/"+url.PathEscape(runID)+"/cancel", map[string]any{"reason": reason}, &ignored)
}

func (c *gatewayHTTPClient) ResolveApproval(ctx context.Context, taskID, _ string, approvalID string, decision acp.ApprovalDecision) error {
	wireDecision := "reject"
	if decision == acp.ApprovalAllow {
		wireDecision = "approve"
	}
	var ignored map[string]any
	path := "/v1/tasks/" + url.PathEscape(taskID) + "/approvals/" + url.PathEscape(approvalID) + "/resolve"
	return c.doJSON(ctx, http.MethodPost, path, map[string]any{"decision": wireDecision}, &ignored)
}

func (c *gatewayHTTPClient) StreamRunEvents(ctx context.Context, taskID, runID string) (<-chan acp.RunEvent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/tasks/"+url.PathEscape(taskID)+"/runs/"+url.PathEscape(runID)+"/stream", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("gateway returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	events := make(chan acp.RunEvent, 16)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		readSSE(resp.Body, events)
	}()
	return events, nil
}

func (c *gatewayHTTPClient) doJSON(ctx context.Context, method, requestPath string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+cleanAPIPath(requestPath), reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("gateway returned %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func cleanAPIPath(value string) string {
	if strings.HasPrefix(value, "/") {
		return path.Clean(value)
	}
	return "/" + path.Clean(value)
}

func readSSE(r io.Reader, out chan<- acp.RunEvent) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)
	currentEvent := "message"
	var currentData strings.Builder
	flush := func() bool {
		data := strings.TrimSpace(currentData.String())
		if data == "" {
			currentEvent = "message"
			currentData.Reset()
			return false
		}
		raw := []byte(data)
		eventType := eventTypeFromStreamPayload(raw, currentEvent)
		out <- acp.RunEvent{Type: eventType, Data: streamPayloadData(raw)}
		terminal := streamPayloadTerminal(raw)
		currentEvent = "message"
		currentData.Reset()
		return terminal
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if strings.HasPrefix(line, ":") {
			continue
		}
		if line == "" {
			if flush() {
				return
			}
			continue
		}
		if value, ok := strings.CutPrefix(line, "event:"); ok {
			currentEvent = strings.TrimSpace(value)
			continue
		}
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			if currentData.Len() > 0 {
				currentData.WriteByte('\n')
			}
			currentData.WriteString(strings.TrimSpace(value))
		}
	}
	flush()
}

func eventTypeFromStreamPayload(raw []byte, fallback string) string {
	var payload struct {
		Data struct {
			EventType string `json:"event_type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil && payload.Data.EventType != "" {
		return payload.Data.EventType
	}
	return fallback
}

func streamPayloadTerminal(raw []byte) bool {
	var payload struct {
		Data struct {
			Terminal bool `json:"terminal"`
		} `json:"data"`
	}
	return json.Unmarshal(raw, &payload) == nil && payload.Data.Terminal
}

func streamPayloadData(raw []byte) []byte {
	var payload struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil && len(payload.Data) > 0 {
		return payload.Data
	}
	return raw
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
