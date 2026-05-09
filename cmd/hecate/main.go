package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/hecate/agent-runtime/internal/agentadapters"
	"github.com/hecate/agent-runtime/internal/agentchat"
	"github.com/hecate/agent-runtime/internal/api"
	"github.com/hecate/agent-runtime/internal/billing"
	"github.com/hecate/agent-runtime/internal/bootstrap"
	"github.com/hecate/agent-runtime/internal/catalog"
	"github.com/hecate/agent-runtime/internal/chatstate"
	"github.com/hecate/agent-runtime/internal/config"
	"github.com/hecate/agent-runtime/internal/controlplane"
	"github.com/hecate/agent-runtime/internal/gateway"
	"github.com/hecate/agent-runtime/internal/governor"
	"github.com/hecate/agent-runtime/internal/orchestrator"
	"github.com/hecate/agent-runtime/internal/profiler"
	"github.com/hecate/agent-runtime/internal/providers"
	"github.com/hecate/agent-runtime/internal/retention"
	"github.com/hecate/agent-runtime/internal/router"
	"github.com/hecate/agent-runtime/internal/secrets"
	"github.com/hecate/agent-runtime/internal/storage"
	"github.com/hecate/agent-runtime/internal/taskstate"
	"github.com/hecate/agent-runtime/internal/telemetry"
	"github.com/hecate/agent-runtime/internal/version"
)

func main() {
	// Tiny manual flag parse: a single `--version` / `-v` short-circuit.
	// We don't want to pull in the full flag package here because the rest
	// of configuration is env-driven; mixing the two would muddle the
	// surface. Anything other than `--version`/`-v` falls through to the
	// regular env-driven startup.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v", "version":
			fmt.Println(version.Version)
			return
		case "mcp-server":
			runMCPServer()
			return
		}
	}

	cfg := config.LoadFromEnv()
	if err := cfg.Validate(); err != nil {
		slog.Error("config validation failed", slog.Any("error", err))
		os.Exit(1)
	}

	// Resolve the auto-generated control-plane encryption key. Env values
	// win when set; otherwise the value is loaded from the bootstrap file
	// under DataDir, generating a fresh one on first run.
	bootstrapPath := resolveBootstrapPath(cfg.Server.BootstrapFile, cfg.Server.DataDir)
	boot, err := bootstrap.Resolve(bootstrapPath, cfg.Server.ControlPlaneSecretKey)
	if err != nil {
		slog.Error("bootstrap secret init failed", slog.String("path", bootstrapPath), slog.Any("error", err))
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

	staticPricebook := billing.NewStaticPricebook(cfg.Providers, cfg.Pricebook)
	pricebook := billing.NewRegistryAwarePricebook(billing.NewControlPlanePricebook(staticPricebook, controlPlaneStore), providerRegistry)
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
	budgetStore := buildBudgetStore(cfg, logger, sqliteClient)
	chatSessionStore := buildChatSessionStore(cfg, logger, sqliteClient)
	agentChatStore := buildAgentChatStore(cfg, logger, sqliteClient)
	// Approval store shares the agent-chat backend selector
	// (GATEWAY_CHAT_SESSIONS_BACKEND) so all agent-chat state lives
	// together. Startup reconcile fires before the gateway accepts
	// any request: pending rows from a prior process can't be
	// resurrected (process-local waiters are lost), so they're
	// marked timed_out with path=startup_reconcile up front.
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
		budgetStore,
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
	governorEngine := governor.NewControlPlaneGovernor(cfg.Governor, budgetStore, budgetStore, controlPlaneStore)

	service := gateway.NewService(buildGatewayDependencies(
		cfg,
		logger,
		routerEngine,
		providerCatalog,
		governorEngine,
		providerRegistry,
		healthTracker,
		providerHistoryStore,
		pricebook,
		tracer,
		metrics,
		retentionManager,
		chatSessionStore,
	))

	retentionCtx, retentionCancel := context.WithCancel(context.Background())
	defer retentionCancel()
	go retentionManager.RunLoop(retentionCtx)

	taskQueue := buildTaskQueue(cfg, logger, sqliteClient)

	// Pricebook auto-import scheduler. Enabled when the operator sets
	// GATEWAY_PRICEBOOK_AUTO_IMPORT_INTERVAL to a positive duration —
	// otherwise this returns immediately and the goroutine exits clean.
	// Runs once on start (so a fresh deploy isn't stuck on stale prices)
	// and then on every interval. Manual rows are always preserved per
	// the operator-protection contract; only Added/Updated land.
	pricebookImportCtx, pricebookImportCancel := context.WithCancel(context.Background())
	defer pricebookImportCancel()
	go billing.RunPricebookAutoImport(
		pricebookImportCtx,
		billing.NewPricebookImporter(controlPlaneStore, http.DefaultClient),
		billing.PricebookAutoImportConfig{Interval: cfg.Pricebook.AutoImportInterval},
		logger,
	)

	handler := api.NewHandler(cfg, logger, service, controlPlaneStore, taskStore, taskQueue, providerRuntime)
	handler.SetAgentChatStore(agentChatStore)
	handler.SetAgentApprovalStore(approvalStore)
	// Wire the cipher into the handler and its underlying runner so MCP
	// server env values are encrypted at task-creation time and decrypted
	// at subprocess spawn time. SetSecretCipher is a no-op when cipher
	// is nil (no GATEWAY_CONTROL_PLANE_SECRET_KEY configured).
	handler.SetSecretCipher(secretCipher)
	// MCP client cache: amortizes subprocess spawn cost across runs by
	// holding one Client per upstream config and handing it back to
	// later runs that configure the same server. The handler owns it
	// and tears it down on Shutdown after the runner has drained, so
	// in-flight runs always see a live client. Zero TTL falls back to
	// the cache's internal default (5 minutes idle eviction).
	handler.SetMCPClientCache(orchestrator.NewAgentMCPClientCache(orchestrator.AgentMCPClientCacheOptions{
		// TTL=0 lets the cache use its internal default (5 min).
		// We don't expose a TTL knob today; if operators ask, add
		// GATEWAY_TASK_MCP_CLIENT_CACHE_TTL alongside the existing
		// max-entries / ping-interval / ping-timeout knobs.
		MaxEntries:   cfg.Server.TaskMCPClientCacheMaxEntries,
		PingInterval: cfg.Server.TaskMCPClientCachePingInterval,
		PingTimeout:  cfg.Server.TaskMCPClientCachePingTimeout,
		Metrics:      handler.OrchestratorMetrics(),
	}))

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
	gatewayStatePath, err := writeGatewayRuntimeState(cfg.Server.DataDir, listener.Addr().String(), cfg.Server.PublicURL)
	if err != nil {
		logger.Warn("hecate runtime state write failed", slog.Any("error", err))
	} else {
		defer removeGatewayRuntimeState(gatewayStatePath)
	}

	go func() {
		logger.Info("gateway starting",
			slog.String("addr", cfg.Server.Address),
			slog.String("listen_addr", listener.Addr().String()),
			slog.String("hecate.runtime.path", gatewayStatePath),
			slog.String("default_model", cfg.Router.DefaultModel),
			slog.Int("provider_max_attempts", cfg.Provider.MaxAttempts),
			slog.Bool("provider_failover_enabled", cfg.Provider.FailoverEnabled),
			slog.Int("provider_health_failure_threshold", cfg.Provider.HealthThreshold),
			slog.Duration("provider_health_cooldown", cfg.Provider.HealthCooldown),
			slog.Duration("provider_health_latency_degraded_threshold", cfg.Provider.HealthLatencyDegradedThreshold),
			slog.String("provider_history_backend", cfg.Provider.HistoryBackend),
			slog.Int("provider_history_limit", cfg.Provider.HistoryLimit),
			slog.Bool("retention_enabled", cfg.Retention.Enabled),
			slog.Duration("retention_interval", cfg.Retention.Interval),
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
			slog.Int("provider_count", len(cfg.Providers.OpenAICompatible)),
		)

		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Error("gateway stopped unexpectedly", slog.Any("error", err))
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logger.Info("gateway shutting down")
	retentionCancel()
	pricebookImportCancel()
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
	pricebook billing.Pricebook,
	tracer profiler.Tracer,
	metrics *telemetry.Metrics,
	retentionManager *retention.Manager,
	chatSessionStore chatstate.Store,
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
		Pricebook:         pricebook,
		Tracer:            tracer,
		Metrics:           metrics,
		Retention:         retentionManager,
		ChatSessions:      chatSessionStore,
		TraceBodyCapture:  cfg.Server.TraceBodyCapture,
		TraceBodyMaxBytes: cfg.Server.TraceBodyMaxBytes,
	}
}

func pruneableProviderHistory(store providers.HealthHistoryStore) retention.Pruner {
	pruner, _ := store.(retention.Pruner)
	return pruner
}

// approvalRetentionPruner exposes the AgentChatApprovalPruner surface
// when the configured approval store implements it. Memory and SQLite
// both do; tests that swap in a stub may not — returning nil is
// harmless because the retention worker skips subsystems with a nil
// pruner.
func approvalRetentionPruner(store agentadapters.ApprovalStore) retention.AgentChatApprovalPruner {
	pruner, _ := store.(retention.AgentChatApprovalPruner)
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

func buildBudgetStore(cfg config.Config, logger *slog.Logger, sqliteClient *storage.SQLiteClient) governor.BudgetStore {
	if cfg.Governor.BudgetBackend == "sqlite" {
		store, err := governor.NewSQLiteBudgetStore(context.Background(), sqliteClient)
		if err != nil {
			logger.Error("budget store init failed", slog.Any("error", err))
			os.Exit(1)
		}
		return store
	}
	return governor.NewMemoryBudgetStore()
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
	return cfg.Governor.BudgetBackend == "sqlite" ||
		cfg.Server.ControlPlaneBackend == "sqlite" ||
		cfg.Chat.SessionsBackend == "sqlite" ||
		cfg.Server.TasksBackend == "sqlite" ||
		cfg.Server.TaskQueueBackend == "sqlite" ||
		cfg.Retention.HistoryBackend == "sqlite"
}

func buildChatSessionStore(cfg config.Config, logger *slog.Logger, sqliteClient *storage.SQLiteClient) chatstate.Store {
	switch cfg.Chat.SessionsBackend {
	case "sqlite":
		store, err := chatstate.NewSQLiteStore(context.Background(), sqliteClient)
		if err != nil {
			logger.Error("chat session store init failed", slog.Any("error", err))
			os.Exit(1)
		}
		return store
	default:
		return chatstate.NewMemoryStore()
	}
}

func buildAgentChatStore(cfg config.Config, logger *slog.Logger, sqliteClient *storage.SQLiteClient) agentchat.Store {
	switch cfg.Chat.SessionsBackend {
	case "sqlite":
		store, err := agentchat.NewSQLiteStore(context.Background(), sqliteClient)
		if err != nil {
			logger.Error("agent chat store init failed", slog.Any("error", err))
			os.Exit(1)
		}
		return store
	default:
		return agentchat.NewMemoryStore()
	}
}

// buildApprovalStore picks memory or sqlite for the agent-chat
// approval coordinator. Keyed off the same env var as the agent-chat
// session/message stores (GATEWAY_CHAT_SESSIONS_BACKEND) so the whole
// agent-chat state bundle moves together.
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

func retentionHistoryKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		key = "control-plane"
	}
	return key + ":retention-history"
}

// resolveBootstrapPath returns the location the gateway should read/write
// the bootstrap secret file from. An explicit GATEWAY_BOOTSTRAP_FILE
// (carried in `bootstrapFile`) wins; otherwise the file lives at
// `<dataDir>/hecate.bootstrap.json`, which keeps it under the same
// volume mount in docker and the same `.data/` directory in local dev.
func resolveBootstrapPath(bootstrapFile, dataDir string) string {
	if bootstrapFile != "" {
		return bootstrapFile
	}
	return filepath.Join(dataDir, "hecate.bootstrap.json")
}
