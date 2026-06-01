package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/api"
	"github.com/hecatehq/hecate/internal/bootstrap"
	"github.com/hecatehq/hecate/internal/catalog"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/gateway"
	"github.com/hecatehq/hecate/internal/governor"
	"github.com/hecatehq/hecate/internal/llamacpp"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/profiler"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/internal/retention"
	"github.com/hecatehq/hecate/internal/router"
	"github.com/hecatehq/hecate/internal/secrets"
	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/internal/version"
)

func runServe() {
	cfg := config.LoadFromEnv()
	if err := cfg.Validate(); err != nil {
		slog.Error("config validation failed", slog.Any("error", err))
		os.Exit(1)
	}
	if err := cfg.Server.ValidateNetworkExposure(); err != nil {
		slog.Error("network exposure guard failed", slog.Any("error", err))
		os.Exit(1)
	}
	if !config.ListenAddressIsLoopback(cfg.Server.Address) {
		slog.Warn("gateway binding beyond loopback; ensure an access-control layer protects Hecate",
			slog.String("listen_addr", cfg.Server.Address),
			slog.String("ack_env", "HECATE_ALLOW_NON_LOOPBACK_BIND"),
		)
	}

	// Resolve the auto-generated control-plane encryption key. Env values
	// win when set; otherwise the value is loaded from the bootstrap file
	// under DataDir, generating a fresh one on first run.
	bootstrapPath := resolveBootstrapPath(cfg.Server.BootstrapFile, cfg.Server.DataDir)
	boot, err := bootstrap.Resolve(bootstrapPath, cfg.Server.ControlPlaneSecretKey)
	if err != nil {
		slog.Error(
			"bootstrap secret init failed",
			slog.String("path", bootstrapPath),
			slog.String("hint", "Hecate requires hecate.bootstrap.json to contain a valid 32-byte base64 key and use private file permissions; fix ownership, ACLs, or POSIX mode bits, or unset an invalid HECATE_CONTROL_PLANE_SECRET_KEY"),
			slog.Any("error", err),
		)
		os.Exit(1)
	}
	cfg.Server.ControlPlaneSecretKey = boot.ControlPlaneSecretKey

	otelResource, err := telemetry.BuildResource(context.Background(), telemetry.ResourceOptions{
		ServiceName:       cfg.OTel.ServiceName,
		ServiceVersion:    cfg.OTel.ServiceVersion,
		ServiceInstanceID: cfg.OTel.ServiceInstanceID,
		DeploymentEnv:     cfg.OTel.DeploymentEnvironment,
	})
	if err != nil {
		slog.Error("otel resource init failed", slog.Any("error", err))
		os.Exit(1)
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	logger, shutdownLogs, err := telemetry.NewLoggerWithOTLP(context.Background(), cfg.LogLevel, telemetry.OTelLogOptions{
		Enabled:   cfg.OTel.Logs.Enabled,
		Endpoint:  firstNonEmpty(cfg.OTel.Logs.Endpoint, cfg.OTel.Traces.Endpoint),
		Headers:   firstNonEmptyMap(cfg.OTel.Logs.Headers, cfg.OTel.Traces.Headers),
		Resource:  otelResource,
		Timeout:   firstNonZeroDuration(cfg.OTel.Logs.Timeout, cfg.OTel.Traces.Timeout),
		Transport: firstNonEmpty(cfg.OTel.Logs.Transport, cfg.OTel.Transport),
	})
	if err != nil {
		slog.Error("otel logger init failed", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := shutdownLogs(shutdownCtx); err != nil {
			logger.Warn("otel logger shutdown failed", slog.Any("error", err))
		}
	}()
	meterProvider, shutdownMetrics, err := telemetry.NewMeterProvider(context.Background(), telemetry.OTelMetricOptions{
		Enabled:        cfg.OTel.Metrics.Enabled,
		Endpoint:       cfg.OTel.Metrics.Endpoint,
		Headers:        cfg.OTel.Metrics.Headers,
		Resource:       otelResource,
		Timeout:        cfg.OTel.Metrics.Timeout,
		Interval:       cfg.OTel.MetricsInterval,
		Transport:      cfg.OTel.Metrics.Transport,
		ExemplarFilter: cfg.OTel.MetricsExemplarFilter,
	})
	if err != nil {
		slog.Error("otel meter provider init failed", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := shutdownMetrics(shutdownCtx); err != nil {
			logger.Warn("otel meter provider shutdown failed", slog.Any("error", err))
		}
	}()
	otel.SetMeterProvider(meterProvider)
	metrics, err := telemetry.NewMetricsWithMeterProvider(meterProvider)
	if err != nil {
		logger.Error("otel metrics init failed", slog.Any("error", err))
		os.Exit(1)
	}
	sqliteClient := buildSQLiteClient(cfg, logger)
	if sqliteClient != nil {
		defer func() {
			if err := sqliteClient.Close(); err != nil {
				logger.Warn("sqlite close failed", slog.Any("error", err))
			}
		}()
	}

	controlPlaneStore := buildControlPlaneStore(cfg, logger, sqliteClient)
	var secretCipher secrets.Cipher
	if strings.TrimSpace(cfg.Server.ControlPlaneSecretKey) != "" {
		cipherImpl, err := secrets.NewAESGCMCipher(cfg.Server.ControlPlaneSecretKey)
		if err != nil {
			logger.Error("control plane secret cipher init failed", slog.Any("error", err))
			os.Exit(1)
		}
		secretCipher = cipherImpl
	}

	providerRuntime := providers.NewControlPlaneRuntimeManager(logger, cfg.Providers.OpenAICompatible, controlPlaneStore, secretCipher)
	providerRuntime.SetGlobalAnthropicCacheDisabled(cfg.Providers.AnthropicCacheDisabled)
	if err := providerRuntime.Reload(context.Background()); err != nil {
		logger.Error("provider runtime reload failed", slog.Any("error", err))
		os.Exit(1)
	}
	if err := controlplane.AutoImportEnvProviders(context.Background(), logger, controlPlaneStore, cfg.Providers.OpenAICompatible); err != nil {
		logger.Warn("auto-import of env-preconfigured providers failed", slog.Any("error", err))
	}
	providerRegistry := providerRuntime.Registry()
	providerHistoryStore := buildProviderHistoryStore(cfg, logger, sqliteClient)
	healthTracker := providers.NewMemoryHealthTrackerWithHistory(
		cfg.Provider.HealthThreshold,
		cfg.Provider.HealthCooldown,
		cfg.Provider.HealthLatencyDegradedThreshold,
		providerHistoryStore,
	)

	otelProvider, err := profiler.NewTracerProvider(context.Background(), profiler.TracerProviderOptions{
		Enabled:   cfg.OTel.Traces.Enabled,
		Endpoint:  cfg.OTel.Traces.Endpoint,
		Headers:   cfg.OTel.Traces.Headers,
		Timeout:   cfg.OTel.Traces.Timeout,
		Resource:  otelResource,
		Transport: cfg.OTel.Traces.Transport,
		Sampler:   telemetry.BuildSampler(cfg.OTel.TracesSampler, cfg.OTel.TracesSamplerArg),
	})
	if err != nil {
		logger.Error("otel tracer provider init failed", slog.Any("error", err))
		os.Exit(1)
	}
	otel.SetTracerProvider(otelProvider)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := otelProvider.Shutdown(shutdownCtx); err != nil {
			logger.Warn("otel tracer provider shutdown failed", slog.Any("error", err))
		}
	}()
	tracer := profiler.NewInMemoryTracer(profiler.NewOTelTracer(otelProvider))
	usageStore := buildUsageStore(cfg, logger, sqliteClient)
	agentChatStore := buildAgentChatStore(cfg, logger, sqliteClient)
	projectStore := buildProjectStore(cfg, logger, sqliteClient)
	// Approval state follows HECATE_BACKEND so chat transcripts, grants,
	// and pending approval rows move together across memory/sqlite modes.
	// Startup reconcile fires before the gateway accepts any request:
	// pending rows from a prior process can't be resurrected
	// (process-local waiters are lost), so they're marked timed_out with
	// path=startup_reconcile up front.
	approvalStore := buildApprovalStore(cfg, logger, sqliteClient)
	if rec, ok := approvalStore.(agentadapters.ApprovalRetentionStore); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		reconciled, err := rec.ReconcilePending(ctx, time.Now().UTC())
		cancel()
		if err != nil {
			logger.Error("approval startup reconcile failed", slog.Any("error", err))
			os.Exit(1)
		}
		if reconciled > 0 {
			logger.Info("approval startup reconcile completed",
				slog.Int64("reconciled", reconciled),
				slog.String("path", "startup_reconcile"),
			)
		}
	}
	retentionHistoryStore := buildRetentionHistoryStore(cfg, logger, sqliteClient)
	// Build the task-state store before the retention manager so the
	// turn-events sweep can target its events table directly.
	taskStore := buildTaskStore(cfg, logger, sqliteClient)
	retentionManager := retention.NewManager(
		logger,
		cfg.Retention,
		tracer,
		tracer,
		usageStore,
		controlPlaneStore,
		pruneableProviderHistory(providerHistoryStore),
		taskStore,
		approvalRetentionPruner(approvalStore),
		retentionHistoryStore,
	)
	providerCatalog := catalog.NewRegistryCatalogWithSelfAddr(providerRegistry, healthTracker, cfg.Server.Address)
	routerEngine := router.NewRuleRouter(
		cfg.Router.DefaultModel,
		providerCatalog,
	)
	governorEngine := governor.NewControlPlaneGovernor(cfg.Governor, usageStore, usageStore, controlPlaneStore)

	service := gateway.NewService(buildGatewayDependencies(
		cfg,
		logger,
		routerEngine,
		providerCatalog,
		governorEngine,
		providerRegistry,
		healthTracker,
		providerHistoryStore,
		tracer,
		metrics,
		retentionManager,
	))

	retentionCtx, retentionCancel := context.WithCancel(context.Background())
	defer retentionCancel()
	go retentionManager.RunLoop(retentionCtx)

	taskQueue := buildTaskQueue(cfg, logger, sqliteClient)

	handler := api.NewHandler(cfg, logger, service, controlPlaneStore, taskStore, taskQueue, providerRuntime)
	handler.SetAgentChatStore(agentChatStore)
	handler.SetProjectStore(projectStore)
	handler.SetAgentApprovalStore(approvalStore)
	handler.SetStateCleaner(sqliteClient)
	// Wire the cipher into the handler and its underlying runner so MCP
	// server env values are encrypted at task-creation time and decrypted
	// at subprocess spawn time. SetSecretCipher is a no-op when cipher
	// is nil (no HECATE_CONTROL_PLANE_SECRET_KEY configured).
	handler.SetSecretCipher(secretCipher)
	// MCP client cache: amortizes subprocess spawn cost across runs by
	// holding one Client per upstream config and handing it back to
	// later runs that configure the same server. The handler owns it
	// and tears it down on Shutdown after the runner has drained, so
	// in-flight runs always see a live client. Zero TTL falls back to
	// the cache's internal default (5 minutes idle eviction).
	// quit is the desktop-app shutdown channel. The Tauri close-window
	// handler calls POST /hecate/v1/system/shutdown which signals here,
	// joining the same drain path SIGINT/SIGTERM take below. Buffered so
	// the handler returns without blocking when the channel is empty;
	// the handler's select-default also tolerates a double-fire.
	quit := make(chan struct{}, 1)
	handler.SetQuitFunc(func() {
		select {
		case quit <- struct{}{}:
		default:
		}
	})
	handler.SetMCPClientCache(orchestrator.NewAgentMCPClientCache(orchestrator.AgentMCPClientCacheOptions{
		// TTL=0 lets the cache use its internal default (5 min).
		// We don't expose a TTL knob today; if operators ask, add
		// HECATE_TASK_MCP_CLIENT_CACHE_TTL alongside the existing
		// max-entries / ping-interval / ping-timeout knobs.
		MaxEntries:   cfg.Server.TaskMCPClientCacheMaxEntries,
		PingInterval: cfg.Server.TaskMCPClientCachePingInterval,
		PingTimeout:  cfg.Server.TaskMCPClientCachePingTimeout,
		Metrics:      handler.OrchestratorMetrics(),
	}))

	// Local-models subsystem (Hecate-managed llama.cpp). v1 was
	// dormant unless HECATE_LLAMA_SERVER_BIN was already set; v2
	// adds an opt-in lazy-download path that resolves the binary
	// from <data_dir>/llamacpp/bin/ or downloads + verifies the
	// pinned upstream release on first call. Tauri sidecar startup
	// keeps setting HECATE_LLAMA_SERVER_BIN directly; headless
	// gateways flip HECATE_LOCAL_MODELS_LAZY_DOWNLOAD=on to opt in.
	if shouldInitLocalModels() {
		resolver, _ := llamacpp.NewBinaryResolver(llamacpp.BinaryResolverOptions{
			DataDir:       cfg.Server.DataDir,
			ExplicitPath:  os.Getenv("HECATE_LLAMA_SERVER_BIN"),
			AllowDownload: os.Getenv("HECATE_LOCAL_MODELS_LAZY_DOWNLOAD") == "on",
		})
		binaryPath := ""
		if resolver != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			resolved, resolveErr := resolver.Resolve(ctx)
			cancel()
			if resolveErr == nil {
				binaryPath = resolved
			} else if !errors.Is(resolveErr, llamacpp.ErrBinaryUnavailable) {
				logger.Warn("local-models binary resolution failed; feature dormant",
					slog.Any("error", resolveErr))
			}
		}
		localModelsSvc, lmErr := llamacpp.NewService(llamacpp.ServiceOptions{
			BinaryPath: binaryPath,
			DataDir:    cfg.Server.DataDir,
			Store:      controlPlaneStore,
			RuntimeOptions: llamacpp.RuntimeOptions{
				MaxResident: parseMaxResident(os.Getenv("HECATE_LOCAL_MODELS_MAX_RESIDENT")),
			},
		})
		if lmErr != nil {
			logger.Warn("local models service init failed; feature disabled",
				slog.Any("error", lmErr))
		} else {
			handler.SetLocalModelsService(localModelsSvc)
			// EnsureAutoRegisteredProvider runs below, after the
			// listener binds — we need the bound address as a
			// fallback when cfg.Server.PublicURL is empty.
		}
	}

	server := &http.Server{
		Addr:              cfg.Server.Address,
		Handler:           api.NewServer(logger, handler),
		ReadHeaderTimeout: 5 * time.Second,
	}
	listener, err := net.Listen("tcp", cfg.Server.Address)
	if err != nil {
		logger.Error("gateway listen failed", slog.String("addr", cfg.Server.Address), slog.Any("error", err))
		os.Exit(1)
	}

	// Auto-register the llamacpp provider AFTER the listener binds so
	// we can fall back to the bound loopback address when
	// HECATE_PUBLIC_URL / cfg.Server.PublicURL is empty. Without that
	// fallback, the stored provider BaseURL would be a relative path
	// (just `/hecate/internal/llamacpp/v1`) and any downstream that
	// resolves the URL fails. Tauri sets PublicURL explicitly; headless
	// / dev gateways frequently leave it empty.
	if svc := handler.LocalModelsService(); svc != nil {
		gatewayURL := strings.TrimSpace(cfg.Server.PublicURL)
		if gatewayURL == "" {
			gatewayURL = "http://" + listener.Addr().String()
		}
		if err := svc.EnsureAutoRegisteredProvider(context.Background(), gatewayURL); err != nil {
			if errors.Is(err, llamacpp.ErrAutoProviderOperatorOwned) {
				logger.Info("llamacpp provider already exists; operator override retained",
					slog.String("preset_id", "llamacpp"))
			} else {
				logger.Warn("local models auto-provider registration failed",
					slog.Any("error", err))
			}
		}
	}
	gatewayStatePath, err := writeGatewayRuntimeState(cfg.Server.DataDir, listener.Addr().String(), cfg.Server.PublicURL)
	if err != nil {
		logger.Warn("hecate runtime state write failed", slog.Any("error", err))
	} else {
		defer removeGatewayRuntimeState(gatewayStatePath)
	}

	printStartupBanner(cfg, listener.Addr().String())
	logFullStartupConfig(logger, cfg)

	go func() {
		// Operator-essential fields only — full config is at Debug level
		// via logFullStartupConfig above. Tighter top-of-log line means
		// the banner above is what an operator scans first.
		logger.Info("gateway starting",
			slog.String("listen_addr", listener.Addr().String()),
			slog.String("data_dir", cfg.Server.DataDir),
			slog.String("default_model", cfg.Router.DefaultModel),
			slog.Int("provider_count", len(cfg.Providers.OpenAICompatible)),
			slog.String("version", version.Version),
		)

		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Error("gateway stopped unexpectedly", slog.Any("error", err))
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	var shutdownTrigger string
	select {
	case sig := <-stop:
		shutdownTrigger = sig.String()
	case <-quit:
		shutdownTrigger = "system_shutdown_endpoint"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logger.Info("gateway shutting down", slog.String("trigger", shutdownTrigger))
	retentionCancel()
	// Stop the task runner before closing the HTTP server. The HTTP
	// layer only enqueues jobs and is quick to drain; the long-poll
	// is the agent loop running in queue workers, which may have
	// spawned MCP subprocesses. Cancelling here propagates through
	// the runner's worker context into running jobs, which run their
	// deferred Pool.Close → Transport.Close chain so subprocesses
	// don't orphan when main returns.
	if err := handler.Shutdown(ctx); err != nil {
		logger.Warn("task runner shutdown did not complete in deadline", slog.Any("error", err))
	}
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("shutdown failed", slog.Any("error", err))
		os.Exit(1)
	}
}

// printStartupBanner writes a short human-readable summary of the
// gateway's startup state to stderr. Goes to stderr (not stdout) so
// log scrapers reading stdout's structured JSON aren't disrupted; an
// operator running `just dev` from a terminal sees both streams
// interleaved and gets the human banner first. Always emitted —
// runs the same in `just dev`, Docker logs, or systemd journal.
func printStartupBanner(cfg config.Config, listenAddr string) {
	url := operatorURL(cfg, listenAddr)
	dataDir := cfg.Server.DataDir
	if dataDir == "" {
		dataDir = "(memory)"
	}
	providers := len(cfg.Providers.OpenAICompatible)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  hecate · %s\n", version.Version)
	fmt.Fprintf(os.Stderr, "  → %s\n", url)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "    data dir       %s\n", dataDir)
	fmt.Fprintf(os.Stderr, "    storage        %s\n", storageSummary(cfg))
	fmt.Fprintf(os.Stderr, "    default model  %s\n", cfg.Router.DefaultModel)
	fmt.Fprintf(os.Stderr, "    providers      %d configured\n", providers)
	if otel := otelSummary(cfg.OTel); otel != "" {
		fmt.Fprintf(os.Stderr, "    otel           %s\n", otel)
	}
	if cfg.Retention.Enabled {
		fmt.Fprintf(os.Stderr, "    retention      every %s\n", cfg.Retention.Interval)
	}
	fmt.Fprintln(os.Stderr)
}

// logFullStartupConfig captures the full startup configuration at
// Debug level for diagnostics. The user-visible startup log line
// stays small; this is what an operator running with -log-level=debug
// sees when something's misbehaving and they need the whole config
// snapshot.
func logFullStartupConfig(logger *slog.Logger, cfg config.Config) {
	logger.Debug("gateway config",
		slog.String("addr", cfg.Server.Address),
		slog.Int("provider_max_attempts", cfg.Provider.MaxAttempts),
		slog.Bool("provider_failover_enabled", cfg.Provider.FailoverEnabled),
		slog.Int("provider_health_failure_threshold", cfg.Provider.HealthThreshold),
		slog.String("provider_health_cooldown", cfg.Provider.HealthCooldown.String()),
		slog.String("provider_health_latency_degraded_threshold", cfg.Provider.HealthLatencyDegradedThreshold.String()),
		slog.String("provider_history_backend", cfg.Provider.HistoryBackend),
		slog.Int("provider_history_limit", cfg.Provider.HistoryLimit),
		slog.Bool("retention_enabled", cfg.Retention.Enabled),
		slog.String("retention_interval", cfg.Retention.Interval.String()),
		slog.Bool("otel_traces_enabled", cfg.OTel.Traces.Enabled),
		slog.String("otel_traces_endpoint", cfg.OTel.Traces.Endpoint),
		slog.String("otel_traces_transport", cfg.OTel.Traces.Transport),
		slog.Bool("otel_metrics_enabled", cfg.OTel.Metrics.Enabled),
		slog.String("otel_metrics_endpoint", cfg.OTel.Metrics.Endpoint),
		slog.String("otel_metrics_transport", cfg.OTel.Metrics.Transport),
		slog.String("otel_metrics_exemplar_filter", cfg.OTel.MetricsExemplarFilter),
		slog.Bool("otel_logs_enabled", cfg.OTel.Logs.Enabled),
		slog.String("otel_logs_endpoint", firstNonEmpty(cfg.OTel.Logs.Endpoint, cfg.OTel.Traces.Endpoint)),
		slog.String("otel_logs_transport", firstNonEmpty(cfg.OTel.Logs.Transport, cfg.OTel.Transport)),
	)
}

// operatorURL derives a clickable URL from the gateway's
// configuration. Prefers PublicURL when set (operator-supplied) and
// falls back to a 127.0.0.1-rewritten form of the listen address.
// `:8765` and `[::]:8765` are both ugly in a terminal; `127.0.0.1:8765`
// renders as a clickable link in most modern terminals.
func operatorURL(cfg config.Config, listenAddr string) string {
	if u := strings.TrimSpace(cfg.Server.PublicURL); u != "" {
		return u
	}
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "http://" + listenAddr
	}
	// net.SplitHostPort strips the IPv6 brackets, so we match the
	// unbracketed form "::" (the bracketed "[::]" branch never fires).
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// otelSummary returns a short human-readable description of which
// OTel signals are enabled, or "" when none are. Operators on alpha
// usually have all three off; the banner omits the row entirely in
// that case rather than printing "otel off".
func otelSummary(o config.OTelConfig) string {
	var parts []string
	if o.Traces.Enabled {
		parts = append(parts, "traces")
	}
	if o.Metrics.Enabled {
		parts = append(parts, "metrics")
	}
	if o.Logs.Enabled {
		parts = append(parts, "logs")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

// storageSummary collapses Hecate's per-subsystem backend choices into
// one short hint. Hecate has ~12 independent storage subsystems (control
// plane, tasks, sessions, etc.) — most deployments are uniformly memory
// (local dev) or sqlite (Docker, native binary). The hint reflects the
// control-plane backend by default — that's the "primary" backend an
// operator identifies with — and appends "(mixed)" when a peer subsystem
// disagrees, signaling to look at `docs/deployment.md`.
func storageSummary(cfg config.Config) string {
	primary := strings.TrimSpace(cfg.Server.ControlPlaneBackend)
	if primary == "" {
		primary = "memory"
	}
	peers := []string{
		cfg.Server.TasksBackend,
		cfg.Server.TaskQueueBackend,
		cfg.Provider.HistoryBackend,
	}
	for _, peer := range peers {
		if peer = strings.TrimSpace(peer); peer != "" && peer != primary {
			return primary + " (mixed)"
		}
	}
	return primary
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmptyMap(values ...map[string]string) map[string]string {
	for _, value := range values {
		if len(value) == 0 {
			continue
		}
		cloned := make(map[string]string, len(value))
		for key, item := range value {
			cloned[key] = item
		}
		return cloned
	}
	return nil
}

func firstNonZeroDuration(values ...time.Duration) time.Duration {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func buildGatewayDependencies(
	cfg config.Config,
	logger *slog.Logger,
	routerEngine router.Router,
	providerCatalog catalog.Catalog,
	governorEngine governor.Governor,
	providerRegistry providers.Registry,
	healthTracker providers.HealthTracker,
	providerHistoryStore providers.HealthHistoryStore,
	tracer profiler.Tracer,
	metrics *telemetry.Metrics,
	retentionManager *retention.Manager,
) gateway.Dependencies {
	return gateway.Dependencies{
		Logger: logger,
		Resilience: gateway.ResilienceOptions{
			MaxAttempts:     cfg.Provider.MaxAttempts,
			RetryBackoff:    cfg.Provider.RetryBackoff,
			FailoverEnabled: cfg.Provider.FailoverEnabled,
		},
		Router:            routerEngine,
		Catalog:           providerCatalog,
		Governor:          governorEngine,
		Providers:         providerRegistry,
		HealthTracker:     healthTracker,
		ProviderHistory:   providerHistoryStore,
		Tracer:            tracer,
		Metrics:           metrics,
		Retention:         retentionManager,
		TraceBodyCapture:  cfg.Server.TraceBodyCapture,
		TraceBodyMode:     cfg.Server.TraceBodyMode,
		TraceBodyMaxBytes: cfg.Server.TraceBodyMaxBytes,
	}
}

func pruneableProviderHistory(store providers.HealthHistoryStore) retention.Pruner {
	pruner, _ := store.(retention.Pruner)
	return pruner
}

// approvalRetentionPruner exposes the Pruner surface when the
// configured approval store implements it. Memory and SQLite both do;
// tests that swap in a stub may not — returning nil is harmless
// because the retention worker skips subsystems with a nil pruner.
func approvalRetentionPruner(store agentadapters.ApprovalStore) retention.Pruner {
	pruner, _ := store.(retention.Pruner)
	return pruner
}

func buildControlPlaneStore(cfg config.Config, logger *slog.Logger, sqliteClient *storage.SQLiteClient) controlplane.Store {
	// "memory" (the documented default) and any unrecognized value fall
	// through to the default branch and produce a MemoryStore — same
	// lenient shape every other backend selector uses today.
	switch cfg.Server.ControlPlaneBackend {
	case "sqlite":
		store, err := controlplane.NewSQLiteStore(context.Background(), sqliteClient, cfg.Server.ControlPlaneKey)
		if err != nil {
			logger.Error("control plane store init failed", slog.Any("error", err))
			os.Exit(1)
		}
		return store
	default:
		return controlplane.NewMemoryStore()
	}
}

func buildRetentionHistoryStore(cfg config.Config, logger *slog.Logger, sqliteClient *storage.SQLiteClient) retention.HistoryStore {
	switch strings.ToLower(strings.TrimSpace(cfg.Retention.HistoryBackend)) {
	case "sqlite":
		store, err := retention.NewSQLiteHistoryStore(context.Background(), sqliteClient, "retention_runs")
		if err != nil {
			logger.Error("retention history store init failed", slog.Any("error", err))
			os.Exit(1)
		}
		return store
	default:
		return retention.NewMemoryHistoryStore()
	}
}

func buildProviderHistoryStore(cfg config.Config, logger *slog.Logger, sqliteClient *storage.SQLiteClient) providers.HealthHistoryStore {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider.HistoryBackend)) {
	case "sqlite":
		store, err := providers.NewSQLiteHealthHistoryStore(context.Background(), sqliteClient, "provider_health_history")
		if err != nil {
			logger.Error("provider health history store init failed", slog.Any("error", err))
			os.Exit(1)
		}
		return store
	default:
		return providers.NewMemoryHealthHistoryStore()
	}
}

func findProviderConfig(cfg config.ProvidersConfig, name string) (config.OpenAICompatibleProviderConfig, bool) {
	for _, providerCfg := range cfg.OpenAICompatible {
		if providerCfg.Name == name {
			return providerCfg, true
		}
	}
	return config.OpenAICompatibleProviderConfig{}, false
}

func buildTaskStore(cfg config.Config, logger *slog.Logger, sqliteClient *storage.SQLiteClient) taskstate.Store {
	switch strings.ToLower(strings.TrimSpace(cfg.Server.TasksBackend)) {
	case "sqlite":
		store, err := taskstate.NewSQLiteStore(context.Background(), sqliteClient)
		if err != nil {
			// Hard-fail: a silent fallback to memory on a configured
			// durable backend would drop every task on the next restart.
			logger.Error("task store init failed", slog.Any("error", err))
			os.Exit(1)
		}
		return store
	default:
		return taskstate.NewMemoryStore()
	}
}

func buildTaskQueue(cfg config.Config, logger *slog.Logger, sqliteClient *storage.SQLiteClient) orchestrator.RunQueue {
	lease := time.Duration(cfg.Server.TaskQueueLeaseSeconds) * time.Second
	if lease <= 0 {
		lease = 30 * time.Second
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Server.TaskQueueBackend)) {
	case "sqlite":
		queue, err := orchestrator.NewSQLiteRunQueue(context.Background(), sqliteClient, lease)
		if err != nil {
			logger.Error("task queue init failed", slog.Any("error", err))
			os.Exit(1)
		}
		return queue
	default:
		return orchestrator.NewMemoryRunQueue(cfg.Server.TaskQueueBuffer, lease)
	}
}

func buildUsageStore(cfg config.Config, logger *slog.Logger, sqliteClient *storage.SQLiteClient) governor.UsageRepository {
	if cfg.Governor.UsageBackend == "sqlite" {
		store, err := governor.NewSQLiteUsageStore(context.Background(), sqliteClient)
		if err != nil {
			logger.Error("usage store init failed", slog.Any("error", err))
			os.Exit(1)
		}
		return store
	}
	return governor.NewMemoryUsageStore()
}

func buildSQLiteClient(cfg config.Config, logger *slog.Logger) *storage.SQLiteClient {
	if !sqliteRequired(cfg) {
		return nil
	}

	client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        cfg.SQLite.Path,
		TablePrefix: cfg.SQLite.TablePrefix,
		BusyTimeout: cfg.SQLite.BusyTimeout,
	})
	if err != nil {
		logger.Error("sqlite init failed", slog.Any("error", err))
		os.Exit(1)
	}
	return client
}

func sqliteRequired(cfg config.Config) bool {
	return cfg.Governor.UsageBackend == "sqlite" ||
		cfg.Server.ControlPlaneBackend == "sqlite" ||
		cfg.Chat.SessionsBackend == "sqlite" ||
		cfg.Projects.Backend == "sqlite" ||
		cfg.Server.TasksBackend == "sqlite" ||
		cfg.Server.TaskQueueBackend == "sqlite" ||
		cfg.Retention.HistoryBackend == "sqlite"
}

func buildProjectStore(cfg config.Config, logger *slog.Logger, sqliteClient *storage.SQLiteClient) projects.Store {
	switch cfg.Projects.Backend {
	case "sqlite":
		store, err := projects.NewSQLiteStore(context.Background(), sqliteClient)
		if err != nil {
			logger.Error("project store init failed", slog.Any("error", err))
			os.Exit(1)
		}
		return store
	default:
		return projects.NewMemoryStore()
	}
}

func buildAgentChatStore(cfg config.Config, logger *slog.Logger, sqliteClient *storage.SQLiteClient) chat.Store {
	switch cfg.Chat.SessionsBackend {
	case "sqlite":
		store, err := chat.NewSQLiteStore(context.Background(), sqliteClient)
		if err != nil {
			logger.Error("agent chat store init failed", slog.Any("error", err))
			os.Exit(1)
		}
		return store
	default:
		return chat.NewMemoryStore()
	}
}

// buildApprovalStore picks memory or sqlite for the approval coordinator.
// It follows HECATE_BACKEND, like the chat session/message stores, so the
// whole chat state bundle moves together.
func buildApprovalStore(cfg config.Config, logger *slog.Logger, sqliteClient *storage.SQLiteClient) agentadapters.ApprovalStore {
	switch cfg.Chat.SessionsBackend {
	case "sqlite":
		store, err := agentadapters.NewSQLiteApprovalStore(context.Background(), sqliteClient)
		if err != nil {
			logger.Error("approval store init failed", slog.Any("error", err))
			os.Exit(1)
		}
		return store
	default:
		return agentadapters.NewMemoryApprovalStore()
	}
}

// parseMaxResident reads the LRU keep-warm cap from env. Defaults to
// 0 (which the Runtime collapses to 1 — v1 behavior). Negative or
// non-numeric values fall back to default with no warning; the
// gateway boot is the wrong place to be noisy about minor env typos
// when the safe default is the v1 behavior.
func parseMaxResident(raw string) int {
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return 0
	}
	return n
}

// shouldInitLocalModels gates the local-models subsystem on operator
// opt-in. Any of these flags triggers init:
//
//   - HECATE_LLAMA_SERVER_BIN is set (Tauri sidecar path, or an
//     operator pointing at their own binary).
//   - HECATE_LOCAL_MODELS=on (force-on; pairs with cache hit or
//     lazy download).
//   - HECATE_LOCAL_MODELS_LAZY_DOWNLOAD=on (headless lazy-download
//     opt-in; downloads + verifies upstream on first request).
//
// When none are set the gateway boots without mounting the
// local-models endpoints — same dormant shape as v1.
func shouldInitLocalModels() bool {
	if strings.TrimSpace(os.Getenv("HECATE_LLAMA_SERVER_BIN")) != "" {
		return true
	}
	if os.Getenv("HECATE_LOCAL_MODELS") == "on" {
		return true
	}
	if os.Getenv("HECATE_LOCAL_MODELS_LAZY_DOWNLOAD") == "on" {
		return true
	}
	return false
}

func retentionHistoryKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		key = "control-plane"
	}
	return key + ":retention-history"
}

// resolveBootstrapPath returns the location the gateway should read/write
// the bootstrap secret file from. An explicit HECATE_BOOTSTRAP_FILE
// (carried in `bootstrapFile`) wins; otherwise the file lives at
// `<dataDir>/hecate.bootstrap.json`, which keeps it under the same
// volume mount in docker and the same `.data/` directory in local dev.
func resolveBootstrapPath(bootstrapFile, dataDir string) string {
	if bootstrapFile != "" {
		return bootstrapFile
	}
	return filepath.Join(dataDir, "hecate.bootstrap.json")
}
