package api

import (
	"log/slog"
	"strings"

	"github.com/hecatehq/hecate/internal/browserrunner"
	"github.com/hecatehq/hecate/internal/config"
)

// browserInspectorFromConfig creates the native browser evidence runtime only
// when an operator explicitly configured a local executable. It also returns
// a path-free readiness record for the operator console; callers must never
// expose constructor errors because they can include a local filesystem path.
// The remote-runtime guard is repeated here so direct Handler construction in
// tests or embedders remains fail-closed even if Config.Validate was skipped.
func browserInspectorFromConfig(cfg config.Config, logger *slog.Logger) (browserrunner.Inspector, BrowserEvidenceRuntimeReadinessResponse) {
	if cfg.Server.RemoteRuntimeMode {
		return nil, BrowserEvidenceRuntimeReadinessResponse{
			Status:         "local_only",
			Message:        "Native browser evidence is unavailable in remote runtime.",
			OperatorAction: "Run the task on a local Hecate runtime with browser evidence configured.",
		}
	}
	if strings.TrimSpace(cfg.Server.TaskBrowserExecutable) == "" {
		return nil, BrowserEvidenceRuntimeReadinessResponse{
			Status:         "not_configured",
			Message:        "Native browser evidence is not configured on this runtime.",
			OperatorAction: "Set HECATE_TASK_BROWSER_EXECUTABLE to an absolute path to a Chromium-compatible executable, then restart Hecate.",
		}
	}
	inspector, err := browserrunner.New(browserrunner.Config{
		ExecutablePath:  cfg.Server.TaskBrowserExecutable,
		Timeout:         cfg.Server.TaskBrowserTimeout,
		AllowPrivateIPs: cfg.Server.TaskBrowserAllowPrivateIPs,
	})
	if err != nil {
		if logger != nil {
			// Do not attach err: it may contain a local filesystem path.
			logger.Warn("native browser evidence is unavailable; the tool will be omitted")
		}
		return nil, BrowserEvidenceRuntimeReadinessResponse{
			Status:         "unavailable",
			Message:        "Native browser evidence is unavailable from the current runtime configuration.",
			OperatorAction: "Check HECATE_TASK_BROWSER_EXECUTABLE and its executable permissions, then restart Hecate.",
		}
	}
	return inspector, BrowserEvidenceRuntimeReadinessResponse{
		Available: true,
		Status:    "ready",
		Message:   "Native browser evidence is ready on this local runtime.",
	}
}
