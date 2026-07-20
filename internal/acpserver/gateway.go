package acpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	runtimeAPIPrefix = "/hecate/v1"
	maxErrorBody     = 64 * 1024
)

// HTTPRuntime is the local stdio bridge's authenticated client for a running
// Hecate runtime. It deliberately accepts only loopback URLs: an ACP client's
// CWD names files on the editor's machine, so silently dispatching that work
// to a remote Hecate runtime would be unsafe and misleading.
type HTTPRuntime struct {
	baseURL      string
	runtimeToken string
	httpClient   *http.Client
}

// NewHTTPRuntime validates and constructs a local Hecate runtime client.
func NewHTTPRuntime(baseURL, runtimeToken string) (*HTTPRuntime, error) {
	normalized, err := loopbackBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	return &HTTPRuntime{
		baseURL:      normalized,
		runtimeToken: strings.TrimSpace(runtimeToken),
		httpClient:   newHTTPClient(),
	}, nil
}

// setHTTPClient replaces the HTTP client in focused package tests. Production
// callers always use the redirect-blocking local-runtime client above.
func (r *HTTPRuntime) setHTTPClient(client *http.Client) {
	if client == nil {
		r.httpClient = newHTTPClient()
		return
	}
	r.httpClient = client
}

func (r *HTTPRuntime) EnsureReady(ctx context.Context) error {
	var response providerStatusResponse
	if err := r.do(ctx, http.MethodGet, runtimeAPIPrefix+"/providers/status", nil, &response); err != nil {
		return err
	}
	for _, provider := range response.Data {
		if provider.AutoRouteReady {
			return nil
		}
	}
	return errors.New("no Hecate auto-route is ready for ACP; configure a routable provider default or gateway default model in Hecate first")
}

func (r *HTTPRuntime) CreateTask(ctx context.Context, request CreateTaskRequest) (Task, error) {
	payload := map[string]any{
		"title":             request.Title,
		"prompt":            request.Prompt,
		"execution_kind":    "agent_loop",
		"workspace_mode":    "in_place",
		"working_directory": request.WorkingDirectory,
	}
	var response taskResponse
	if err := r.do(ctx, http.MethodPost, runtimeAPIPrefix+"/tasks", payload, &response); err != nil {
		return Task{}, err
	}
	if strings.TrimSpace(response.Data.ID) == "" {
		return Task{}, errors.New("Hecate returned a task without an id")
	}
	return Task{ID: response.Data.ID}, nil
}

func (r *HTTPRuntime) StartTask(ctx context.Context, taskID string) (Run, error) {
	var response taskRunResponse
	if err := r.do(ctx, http.MethodPost, taskPath(taskID, "start"), map[string]any{}, &response); err != nil {
		return Run{}, err
	}
	return runtimeRun(response.Data)
}

func (r *HTTPRuntime) ContinueTask(ctx context.Context, taskID, runID, prompt string) (Run, error) {
	var response taskRunResponse
	if err := r.do(ctx, http.MethodPost, runPath(taskID, runID, "continue"), map[string]any{"prompt": prompt}, &response); err != nil {
		return Run{}, err
	}
	return runtimeRun(response.Data)
}

func (r *HTTPRuntime) CancelRun(ctx context.Context, taskID, runID, reason string) error {
	return r.do(ctx, http.MethodPost, runPath(taskID, runID, "cancel"), map[string]any{"reason": reason}, nil)
}

func (r *HTTPRuntime) ListRunEvents(ctx context.Context, taskID, runID string, afterSequence int64) ([]RunEvent, error) {
	endpoint := runPath(taskID, runID, "events")
	query := url.Values{}
	if afterSequence > 0 {
		query.Set("after_sequence", fmt.Sprintf("%d", afterSequence))
		endpoint += "?" + query.Encode()
	}
	var response taskRunEventsResponse
	if err := r.do(ctx, http.MethodGet, endpoint, nil, &response); err != nil {
		return nil, err
	}
	events := make([]RunEvent, 0, len(response.Data))
	for _, event := range response.Data {
		events = append(events, RunEvent{
			Sequence: event.Sequence,
			Type:     event.Type,
			Data:     event.Data,
		})
	}
	return events, nil
}

func (r *HTTPRuntime) do(ctx context.Context, method, endpoint string, body any, out any) error {
	if r == nil {
		return errors.New("Hecate runtime client is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode Hecate request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.baseURL+endpoint, reader)
	if err != nil {
		return fmt.Errorf("build Hecate request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.runtimeToken != "" {
		req.Header.Set("X-Hecate-Runtime-Token", r.runtimeToken)
	}

	client := r.httpClient
	if client == nil {
		client = newHTTPClient()
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("call Hecate %s %s: %w", method, endpoint, err)
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusMultipleChoices {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
		return fmt.Errorf("Hecate %s %s returned %d: %s", method, endpoint, response.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(out); err != nil {
		return fmt.Errorf("decode Hecate response: %w", err)
	}
	return nil
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		// Never follow a runtime redirect. A loopback base URL is a security
		// boundary: forwarding its runtime token and editor workspace intent to
		// a redirect target would defeat the local-only ACP contract.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func loopbackBaseURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("HECATE_BASE_URL is required")
	}
	u, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parse HECATE_BASE_URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("HECATE_BASE_URL must use http or https")
	}
	if u.User != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("HECATE_BASE_URL must be a plain loopback origin")
	}
	if !isLoopbackHost(u.Hostname()) {
		return "", fmt.Errorf("HECATE_BASE_URL host %q is not loopback; ACP editor workspaces require a local Hecate runtime", u.Hostname())
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func isLoopbackHost(host string) bool {
	// Do not accept a hostname such as localhost here. A default HTTP
	// transport resolves hostnames at dial time, so a hostile DNS answer could
	// receive the runtime token and editor prompt after this preflight check.
	// Literal loopback addresses have no resolver race.
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}

func taskPath(taskID string, suffix string) string {
	return path.Join(runtimeAPIPrefix, "tasks", url.PathEscape(taskID), suffix)
}

func runPath(taskID, runID, suffix string) string {
	return path.Join(runtimeAPIPrefix, "tasks", url.PathEscape(taskID), "runs", url.PathEscape(runID), suffix)
}

func runtimeRun(item taskRunItem) (Run, error) {
	if strings.TrimSpace(item.ID) == "" {
		return Run{}, errors.New("Hecate returned a run without an id")
	}
	return Run{ID: item.ID, Status: item.Status}, nil
}

type providerStatusResponse struct {
	Data []providerStatusItem `json:"data"`
}

type providerStatusItem struct {
	AutoRouteReady bool `json:"auto_route_ready"`
}

type taskResponse struct {
	Data taskItem `json:"data"`
}

type taskRunResponse struct {
	Data taskRunItem `json:"data"`
}

type taskItem struct {
	ID string `json:"id"`
}

type taskRunItem struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type taskRunEventsResponse struct {
	Data []taskRunEventItem `json:"data"`
}

type taskRunEventItem struct {
	Sequence int64          `json:"sequence"`
	Type     string         `json:"type"`
	Data     map[string]any `json:"data"`
}
