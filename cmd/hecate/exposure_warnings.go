package main

import (
	"log/slog"
	"strings"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/providers"
)

type exposureProtectionWarning struct {
	envName string
	routes  string
	message string
}

func logNetworkExposureProtectionWarnings(logger *slog.Logger, cfg config.Config, registry providers.Registry) {
	riskProviderCount, warnings := networkExposureProtectionWarnings(cfg, registry)
	for _, warning := range warnings {
		logger.Warn(warning.message,
			slog.String("event.name", "runtime.exposed_without_token"),
			slog.String("listen_addr", cfg.Server.Address),
			slog.String("missing_env", warning.envName),
			slog.String("routes", warning.routes),
			slog.Int("credential_risk_provider_count", riskProviderCount),
		)
	}
}

func networkExposureProtectionWarnings(cfg config.Config, registry providers.Registry) (int, []exposureProtectionWarning) {
	if config.ListenAddressIsLoopback(cfg.Server.Address) || cfg.Server.RemoteRuntimeMode {
		return 0, nil
	}

	riskProviderCount := credentialRiskProviderCount(registry)
	if riskProviderCount == 0 {
		return 0, nil
	}

	var warnings []exposureProtectionWarning
	if strings.TrimSpace(cfg.Server.InferenceToken) == "" {
		warnings = append(warnings, exposureProtectionWarning{
			envName: "HECATE_INFERENCE_TOKEN",
			routes:  "/v1/models, /v1/chat/completions, /v1/messages",
			message: "provider-compatible inference routes are exposed without an inference token",
		})
	}
	if strings.TrimSpace(cfg.Server.RuntimeToken) == "" {
		warnings = append(warnings, exposureProtectionWarning{
			envName: "HECATE_RUNTIME_TOKEN",
			routes:  "/hecate/v1/*",
			message: "Hecate-native routes are exposed without a runtime token",
		})
	}
	return riskProviderCount, warnings
}

func credentialRiskProviderCount(registry providers.Registry) int {
	if registry == nil {
		return 0
	}

	var count int
	for _, provider := range registry.All() {
		if enabler, ok := provider.(providers.Enabler); ok && !enabler.Enabled() {
			continue
		}
		if providerCredentialMayBeConfigured(provider) {
			count++
		}
	}
	return count
}

func providerCredentialMayBeConfigured(provider providers.Provider) bool {
	reporter, ok := provider.(providers.CredentialReporter)
	if !ok {
		return provider.Kind() == providers.KindCloud
	}
	switch reporter.CredentialState() {
	case providers.CredentialStateConfigured, providers.CredentialStateUnknown:
		return true
	default:
		return false
	}
}
