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
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hecate/agent-runtime/internal/acp"
	"github.com/hecate/agent-runtime/internal/profiler"
	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/internal/version"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const (
	defaultGatewayURL    = "http://127.0.0.1:8765"
	defaultHTTPTimeout   = 10 * time.Second
	discoveryHTTPTimeout = 500 * time.Millisecond
	// nativeAppID is the bundle identifier of the Tauri desktop app.
	// MUST match `tauri.conf.json`'s `identifier` field — Tauri's
	// platform-paths API derives the data directory from this string,
	// and hecate-acp reads `hecate.runtime.json` from that same
	// directory to discover the running sidecar. A drift between the
	// two breaks the ACP bridge silently (it falls back to defaultGatewayURL).
	nativeAppID       = "sh.hecate.app"
	hecateRuntimeFile = "hecate.runtime.json"
	maxMessageBytes   = 4 << 20
)

var errAuthSetupFailed = errors.New("auth setup failed")

type bridgeConfig struct {
	GatewayURL    string
	AgentName     string
	AgentVersion  string
	WorkspaceMode string
	ApprovalRoute string
	OTel          bridgeOTelConfig
}

type bridgeOTelConfig struct {
	TracesEnabled         bool
	TracesEndpoint        string
	TracesHeaders         map[string]string
	TracesTimeout         time.Duration
	TracesTransport       string
	TracesSampler         string
	TracesSamplerArg      float64
	ServiceName           string
	ServiceVersion        string
	ServiceInstanceID     string
	DeploymentEnvironment string
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v", "version":
			fmt.Println(version.Version)
			return
		case "auth":
			os.Exit(runAuthCommand(context.Background(), os.Args[2:], os.Stdout, os.Stderr))
		}
	}
	cfg, err := configFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "hecate-acp: %v\n", err)
		os.Exit(1)
	}
	shutdownOTel, err := setupBridgeOTel(context.Background(), cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hecate-acp: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownOTel(ctx)
	}()
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
		WorkspaceMode: firstNonEmpty(os.Getenv("HECATE_WORKSPACE_MODE"), acp.WorkspaceModeAuto),
		ApprovalRoute: firstNonEmpty(os.Getenv("HECATE_APPROVAL_ROUTE"), "editor"),
		OTel:          bridgeOTelFromEnv(),
	}
	if _, err := url.ParseRequestURI(cfg.GatewayURL); err != nil {
		return bridgeConfig{}, fmt.Errorf("invalid HECATE_GATEWAY_URL: %w", err)
	}
	return cfg, nil
}

func bridgeOTelFromEnv() bridgeOTelConfig {
	sharedEndpoint := firstNonEmpty(os.Getenv("HECATE_ACP_OTEL_ENDPOINT"), os.Getenv("HECATE_OTEL_ENDPOINT"))
	sharedTransport := normalizeBridgeOTelTransport(firstNonEmpty(os.Getenv("HECATE_ACP_OTEL_TRANSPORT"), os.Getenv("HECATE_OTEL_TRANSPORT"), "http"))
	traceTransport := normalizeBridgeOTelTransport(firstNonEmpty(os.Getenv("HECATE_ACP_OTEL_TRACES_TRANSPORT"), os.Getenv("HECATE_OTEL_TRACES_TRANSPORT"), sharedTransport))
	traceEndpoint := firstNonEmpty(
		os.Getenv("HECATE_ACP_OTEL_TRACES_ENDPOINT"),
		os.Getenv("HECATE_ACP_OTEL_ENDPOINT"),
		os.Getenv("HECATE_OTEL_TRACES_ENDPOINT"),
	)
	if traceEndpoint == "" && sharedEndpoint != "" {
		traceEndpoint = deriveBridgeOTelEndpoint(sharedEndpoint, traceTransport, "traces")
	}
	return bridgeOTelConfig{
		TracesEnabled:         getBridgeEnvBool("HECATE_ACP_OTEL_TRACES_ENABLED", getBridgeEnvBool("HECATE_OTEL_TRACES_ENABLED", false)),
		TracesEndpoint:        traceEndpoint,
		TracesHeaders:         parseBridgeEnvMap(firstNonEmpty(os.Getenv("HECATE_ACP_OTEL_TRACES_HEADERS"), os.Getenv("HECATE_ACP_OTEL_HEADERS"), os.Getenv("HECATE_OTEL_TRACES_HEADERS"), os.Getenv("HECATE_OTEL_HEADERS"))),
		TracesTimeout:         getBridgeEnvDuration("HECATE_ACP_OTEL_TRACES_TIMEOUT", getBridgeEnvDuration("HECATE_ACP_OTEL_TIMEOUT", getBridgeEnvDuration("HECATE_OTEL_TRACES_TIMEOUT", getBridgeEnvDuration("HECATE_OTEL_TIMEOUT", 5*time.Second)))),
		TracesTransport:       traceTransport,
		TracesSampler:         firstNonEmpty(os.Getenv("HECATE_ACP_OTEL_TRACES_SAMPLER"), os.Getenv("HECATE_OTEL_TRACES_SAMPLER")),
		TracesSamplerArg:      getBridgeEnvFloat("HECATE_ACP_OTEL_TRACES_SAMPLER_ARG", getBridgeEnvFloat("HECATE_OTEL_TRACES_SAMPLER_ARG", 1.0)),
		ServiceName:           firstNonEmpty(os.Getenv("HECATE_ACP_OTEL_SERVICE_NAME"), "hecate-acp"),
		ServiceVersion:        firstNonEmpty(os.Getenv("HECATE_ACP_OTEL_SERVICE_VERSION"), version.Version),
		ServiceInstanceID:     os.Getenv("HECATE_ACP_OTEL_SERVICE_INSTANCE_ID"),
		DeploymentEnvironment: firstNonEmpty(os.Getenv("HECATE_ACP_OTEL_DEPLOYMENT_ENVIRONMENT"), os.Getenv("HECATE_OTEL_DEPLOYMENT_ENVIRONMENT")),
	}
}

func setupBridgeOTel(ctx context.Context, cfg bridgeConfig) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	resource, err := telemetry.BuildResource(ctx, telemetry.ResourceOptions{
		ServiceName:       cfg.OTel.ServiceName,
		ServiceVersion:    cfg.OTel.ServiceVersion,
		ServiceInstanceID: cfg.OTel.ServiceInstanceID,
		DeploymentEnv:     cfg.OTel.DeploymentEnvironment,
	})
	if err != nil {
		return nil, fmt.Errorf("otel resource init failed: %w", err)
	}
	provider, err := profiler.NewTracerProvider(ctx, profiler.TracerProviderOptions{
		Enabled:   cfg.OTel.TracesEnabled,
		Endpoint:  cfg.OTel.TracesEndpoint,
		Headers:   cfg.OTel.TracesHeaders,
		Timeout:   cfg.OTel.TracesTimeout,
		Transport: cfg.OTel.TracesTransport,
		Resource:  resource,
		Sampler:   telemetry.BuildSampler(cfg.OTel.TracesSampler, cfg.OTel.TracesSamplerArg),
	})
	if err != nil {
		return nil, fmt.Errorf("otel tracer provider init failed: %w", err)
	}
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

func discoverGatewayURL() string {
	return discoverGatewayURLFromRuntimePaths(hecateRuntimeCandidatePaths(), gatewayHealthy)
}

func discoverGatewayURLFromRuntimePaths(paths []string, healthy func(string) bool) string {
	for _, runtimePath := range paths {
		baseURL, err := gatewayURLFromRuntimeFile(runtimePath)
		if err != nil {
			continue
		}
		if healthy(baseURL) {
			return baseURL
		}
	}
	return defaultGatewayURL
}

func hecateRuntimeCandidatePaths() []string {
	var candidates []string
	if dataDir := strings.TrimSpace(os.Getenv("HECATE_DATA_DIR")); dataDir != "" {
		candidates = append(candidates, filepath.Join(dataDir, hecateRuntimeFile))
	}
	if nativePath, err := nativeHecateRuntimePath(); err == nil {
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

func nativeHecateRuntimePath() (string, error) {
	dir, err := nativeAppDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, hecateRuntimeFile), nil
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

func gatewayURLFromRuntimeFile(runtimePath string) (string, error) {
	raw, err := os.ReadFile(runtimePath)
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

func runAuthCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: hecate-acp auth setup")
		return 1
	}
	switch args[0] {
	case "help", "--help", "-h":
		fmt.Fprintln(stdout, "usage: hecate-acp auth setup")
		return 0
	case "setup":
		if len(args) > 1 {
			if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
				fmt.Fprintln(stdout, "usage: hecate-acp auth setup")
				return 0
			}
			fmt.Fprintf(stderr, "hecate-acp: unexpected auth setup argument %q\n", args[1])
			fmt.Fprintln(stderr, "usage: hecate-acp auth setup")
			return 1
		}
	default:
		fmt.Fprintf(stderr, "hecate-acp: unknown auth subcommand %q\n", args[0])
		fmt.Fprintln(stderr, "usage: hecate-acp auth setup")
		return 1
	}

	cfg, err := configFromEnv()
	if err != nil {
		fmt.Fprintf(stderr, "hecate-acp: %v\n", err)
		return 1
	}
	if err := runAuthSetup(ctx, stdout, cfg); err != nil {
		if errors.Is(err, errAuthSetupFailed) {
			return 1
		}
		fmt.Fprintf(stderr, "hecate-acp: %v\n", err)
		return 1
	}
	return 0
}

func runAuthSetup(ctx context.Context, stdout io.Writer, cfg bridgeConfig) error {
	client, err := newGatewayHTTPClient(cfg)
	if err != nil {
		return err
	}
	if err := client.Health(ctx); err != nil {
		fmt.Fprintf(stdout, "Hecate gateway: not reachable at %s\n", cfg.GatewayURL)
		fmt.Fprintln(stdout, "Start Hecate first, or set HECATE_GATEWAY_URL to the running gateway URL.")
		return errAuthSetupFailed
	}
	fmt.Fprintf(stdout, "Hecate gateway: reachable at %s\n", cfg.GatewayURL)

	models, err := client.ListModelDescriptions(ctx)
	if err != nil {
		fmt.Fprintln(stdout, "Models: could not list models from the gateway.")
		fmt.Fprintln(stdout, "Open the Hecate operator console or check HECATE_GATEWAY_URL, then retry setup.")
		return errAuthSetupFailed
	}
	if ready := readyModelCount(models); ready > 0 {
		fmt.Fprintf(stdout, "Models: %d ready\n", ready)
		fmt.Fprintln(stdout, "ACP setup is ready.")
		return nil
	}

	if len(models) == 0 {
		fmt.Fprintln(stdout, "Models: none available")
	} else {
		fmt.Fprintf(stdout, "Models: %d listed, none ready for routing\n", len(models))
	}
	statuses, statusErr := client.ProviderStatuses(ctx)
	if statusErr == nil && len(statuses) > 0 {
		fmt.Fprintln(stdout, "Provider readiness:")
		for _, status := range statuses {
			message := firstNonEmpty(status.Readiness.Message, status.RoutingBlockedReason, status.Status)
			fmt.Fprintf(stdout, "- %s: %s\n", status.Name, message)
		}
	}
	fmt.Fprintln(stdout, "Open the Hecate operator console and configure at least one routable provider/model.")
	return errAuthSetupFailed
}

func readyModelCount(models []modelDescription) int {
	count := 0
	for _, model := range models {
		if model.Metadata.Readiness.Ready && model.Metadata.Readiness.RoutingReady {
			count++
		}
	}
	return count
}

func bridgeTracer() oteltrace.Tracer {
	return otel.Tracer("hecate.acp")
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
			responseCtx, span := bridgeTracer().Start(ctx, "acp.response",
				oteltrace.WithAttributes(attribute.String("rpc.system", "jsonrpc"), attribute.String("rpc.service", "acp")))
			dispatcher.HandleResponse(responseCtx, &acp.Response{
				JSONRPC: envelope.JSONRPC,
				ID:      envelope.ID,
				Result:  envelope.Result,
				Error:   envelope.Error,
			})
			span.End()
			continue
		}
		var req acp.Request
		if err := json.Unmarshal(line, &req); err != nil {
			writeCh <- parseErrorResponse(err)
			continue
		}
		reqCtx, span := bridgeTracer().Start(ctx, "acp.rpc",
			oteltrace.WithAttributes(
				attribute.String("rpc.system", "jsonrpc"),
				attribute.String("rpc.service", "acp"),
				attribute.String("rpc.method", req.Method),
			))
		resp := dispatcher.Handle(reqCtx, &req)
		if resp != nil && resp.Error != nil {
			span.SetStatus(codes.Error, resp.Error.Message)
			span.SetAttributes(attribute.Int("rpc.jsonrpc.error_code", resp.Error.Code))
		}
		span.End()
		if resp != nil {
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
	descriptions, err := c.ListModelDescriptions(ctx)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(descriptions))
	for _, item := range descriptions {
		if strings.TrimSpace(item.ID) != "" {
			models = append(models, item.ID)
		}
	}
	return models, nil
}

type modelDescription struct {
	ID       string `json:"id"`
	Metadata struct {
		Readiness struct {
			Ready        bool `json:"ready"`
			RoutingReady bool `json:"routing_ready"`
		} `json:"readiness"`
	} `json:"metadata"`
}

func (c *gatewayHTTPClient) ListModelDescriptions(ctx context.Context) ([]modelDescription, error) {
	ctx, span := startGatewaySpan(ctx, http.MethodGet, "/v1/models")
	defer span.End()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/models", nil)
	if err != nil {
		recordSpanError(span, err)
		return nil, err
	}
	injectBridgeTraceContext(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		recordSpanError(span, err)
		return nil, err
	}
	defer resp.Body.Close()
	recordSpanStatus(span, resp.StatusCode)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		err := fmt.Errorf("gateway returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		recordSpanError(span, err)
		return nil, err
	}
	var payload struct {
		Data []modelDescription `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Data, nil
}

func (c *gatewayHTTPClient) Health(ctx context.Context) error {
	ctx, span := startGatewaySpan(ctx, http.MethodGet, "/healthz")
	defer span.End()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		recordSpanError(span, err)
		return err
	}
	injectBridgeTraceContext(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		recordSpanError(span, err)
		return err
	}
	defer resp.Body.Close()
	recordSpanStatus(span, resp.StatusCode)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		err := fmt.Errorf("gateway returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		recordSpanError(span, err)
		return err
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

type providerStatus struct {
	Name                 string `json:"name"`
	Status               string `json:"status"`
	RoutingBlockedReason string `json:"routing_blocked_reason"`
	Readiness            struct {
		Message string `json:"message"`
	} `json:"readiness"`
}

func (c *gatewayHTTPClient) ProviderStatuses(ctx context.Context) ([]providerStatus, error) {
	var payload struct {
		Data []providerStatus `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/hecate/v1/providers/status", nil, &payload); err != nil {
		return nil, err
	}
	return payload.Data, nil
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
	if err := c.doJSON(ctx, http.MethodPost, "/hecate/v1/tasks", body, &created); err != nil {
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
	if err := c.doJSON(ctx, http.MethodPost, "/hecate/v1/tasks/"+url.PathEscape(created.Data.ID)+"/start", map[string]any{}, &started); err != nil {
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
	path := "/hecate/v1/tasks/" + url.PathEscape(taskID) + "/runs/" + url.PathEscape(runID) + "/continue"
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
	return c.doJSON(ctx, http.MethodPost, "/hecate/v1/tasks/"+url.PathEscape(taskID)+"/runs/"+url.PathEscape(runID)+"/cancel", map[string]any{"reason": reason}, &ignored)
}

func (c *gatewayHTTPClient) ResolveApproval(ctx context.Context, taskID, _ string, approvalID string, decision acp.ApprovalDecision) error {
	wireDecision := "reject"
	if decision == acp.ApprovalAllow {
		wireDecision = "approve"
	}
	var ignored map[string]any
	path := "/hecate/v1/tasks/" + url.PathEscape(taskID) + "/approvals/" + url.PathEscape(approvalID) + "/resolve"
	return c.doJSON(ctx, http.MethodPost, path, map[string]any{"decision": wireDecision}, &ignored)
}

func (c *gatewayHTTPClient) StreamRunEvents(ctx context.Context, taskID, runID string) (<-chan acp.RunEvent, error) {
	requestPath := "/hecate/v1/tasks/" + url.PathEscape(taskID) + "/runs/" + url.PathEscape(runID) + "/stream"
	ctx, span := startGatewaySpan(ctx, http.MethodGet, requestPath)
	defer span.End()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+requestPath, nil)
	if err != nil {
		recordSpanError(span, err)
		return nil, err
	}
	injectBridgeTraceContext(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		recordSpanError(span, err)
		return nil, err
	}
	recordSpanStatus(span, resp.StatusCode)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		err := fmt.Errorf("gateway returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		recordSpanError(span, err)
		return nil, err
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
	ctx, span := startGatewaySpan(ctx, method, cleanAPIPath(requestPath))
	defer span.End()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			recordSpanError(span, err)
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+cleanAPIPath(requestPath), reader)
	if err != nil {
		recordSpanError(span, err)
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	injectBridgeTraceContext(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		recordSpanError(span, err)
		return err
	}
	defer resp.Body.Close()
	recordSpanStatus(span, resp.StatusCode)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		err := fmt.Errorf("gateway returned %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		recordSpanError(span, err)
		return err
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

func injectBridgeTraceContext(req *http.Request) {
	if req == nil {
		return
	}
	otel.GetTextMapPropagator().Inject(req.Context(), propagation.HeaderCarrier(req.Header))
}

func startGatewaySpan(ctx context.Context, method, requestPath string) (context.Context, oteltrace.Span) {
	return bridgeTracer().Start(ctx, "acp.gateway.request",
		oteltrace.WithSpanKind(oteltrace.SpanKindClient),
		oteltrace.WithAttributes(
			attribute.String("server.address", "hecate"),
			attribute.String("http.request.method", method),
			attribute.String("url.path", cleanAPIPath(requestPath)),
		))
}

func recordSpanStatus(span oteltrace.Span, statusCode int) {
	span.SetAttributes(attribute.Int("http.response.status_code", statusCode))
	if statusCode >= 500 {
		span.SetStatus(codes.Error, http.StatusText(statusCode))
	}
}

func recordSpanError(span oteltrace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
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
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeBridgeOTelTransport(value string) string {
	return telemetry.NormalizeOTLPTransport(value)
}

func deriveBridgeOTelEndpoint(endpoint, transport, httpPath string) string {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return ""
	}
	if normalizeBridgeOTelTransport(transport) == telemetry.OTLPTransportGRPC {
		return endpoint
	}
	if strings.HasSuffix(endpoint, "/v1/"+httpPath) {
		return endpoint
	}
	return endpoint + "/v1/" + httpPath
}

func parseBridgeEnvMap(value string) map[string]string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	out := map[string]string{}
	for _, part := range strings.Split(value, ",") {
		key, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(val)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func getBridgeEnvBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getBridgeEnvDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getBridgeEnvFloat(key string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}
