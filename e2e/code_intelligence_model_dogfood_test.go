//go:build e2e

package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/codeintel"
	"github.com/hecatehq/hecate/internal/gitrunner"
	"github.com/hecatehq/hecate/pkg/types"
)

const (
	dogfoodGateEnv                 = "HECATE_CODEINTEL_AGENT_DOGFOOD"
	dogfoodProviderEnv             = "HECATE_CODEINTEL_DOGFOOD_PROVIDER"
	dogfoodModelEnv                = "HECATE_CODEINTEL_DOGFOOD_MODEL"
	dogfoodTimeoutEnv              = "HECATE_CODEINTEL_DOGFOOD_TIMEOUT_SECONDS"
	dogfoodReportDirEnv            = "HECATE_CODEINTEL_DOGFOOD_REPORT_DIR"
	dogfoodStrictEnv               = "HECATE_CODEINTEL_DOGFOOD_STRICT"
	dogfoodRepeatsEnv              = "HECATE_CODEINTEL_DOGFOOD_REPEATS"
	dogfoodMaxModelCalls           = 12
	dogfoodIgnoredSnapshotMaxBytes = 64 * 1024 * 1024
)

type codeIntelligenceDogfoodConfig struct {
	Provider  string
	Model     string
	Timeout   time.Duration
	ReportDir string
	Strict    bool
	Repeats   int
}

type codeIntelligenceDogfoodHealth struct {
	Version string `json:"version"`
	Sandbox struct {
		OSIsolation struct {
			Kind string `json:"kind"`
		} `json:"os_isolation"`
	} `json:"sandbox"`
}

type codeIntelligenceDogfoodProject struct {
	BaseURL          string
	ProjectID        string
	PermissiveRoleID string
	RestrictedRoleID string
	Provider         string
	Model            string
	SourceRevision   string
	SourcePath       string
}

type codeIntelligenceDogfoodSourceSnapshot struct {
	Revision           string
	Dirty              bool
	IgnoredPathsDigest string
}

type codeIntelligenceDogfoodScenario struct {
	ID            string
	Language      string
	Intent        string
	ExpectedRoute string
	Prompt        string
	AnswerMarker  string
	Restricted    bool
	ForcedMissing bool
}

type codeIntelligenceDogfoodCapabilityProbe struct {
	Language      string
	Provider      string
	ProviderToken string
	Version       string
	Available     bool
	Status        string
	Operations    []string
}

// codeIntelligenceDogfoodCapture is deliberately narrower than the Task API.
// It is the only live-runtime shape allowed to feed a persisted scorecard.
type codeIntelligenceDogfoodCapture struct {
	TaskID                        string
	RunID                         string
	TraceID                       string
	RunStatus                     string
	ToolRoute                     []string
	ToolRouteModelCalls           []int
	FirstInspectionTool           string
	ModelCalls                    int
	CodeIntelligenceCalls         int
	SemanticCalls                 int
	StructuralCalls               int
	GrepCalls                     int
	GrepSuccessfulCalls           int
	Provider                      string
	ResultCount                   int
	SemanticResultCount           int
	StructuralResultCount         int
	GrepResultCount               int
	SemanticProviderFailureCall   int
	StructuralProviderFailureCall int
	GrepResultModelCall           int
	StructuralResultModelCall     int
	QueryLatencyMillis            int64
	RunLatencyMillis              int64
	CostMicrosUSD                 int64
	WorkspaceChangeCount          int
	CapabilitiesBeforeQuery       bool
	ProviderVersionObserved       bool
	ProviderUnavailableObserved   bool
	SemanticPolicyBlocked         bool
	Completed                     bool
	UsefulResult                  bool
	ProcessCleanup                string
	UnexpectedToolCalls           int
	CodeIntelligenceErrorKind     []string
}

type codeIntelligenceDogfoodArtifactsResponse struct {
	Data []codeIntelligenceDogfoodArtifact `json:"data"`
}

type codeIntelligenceDogfoodArtifact struct {
	Kind        string `json:"kind"`
	ContentText string `json:"content_text"`
}

type codeIntelligenceDogfoodReadinessResponse struct {
	Data struct {
		Ready          bool     `json:"ready"`
		Status         string   `json:"status"`
		Blockers       []string `json:"blockers"`
		Provider       string   `json:"provider"`
		Model          string   `json:"model"`
		ProfilePosture struct {
			ToolsEnabled          bool     `json:"tools_enabled"`
			WritesAllowed         bool     `json:"writes_allowed"`
			NetworkAllowed        bool     `json:"network_allowed"`
			BrowserEvidenceStatus string   `json:"browser_evidence_status"`
			BrowserAllowed        bool     `json:"browser_allowed"`
			BrowserAllowedOrigins []string `json:"browser_allowed_origins"`
			ApprovalPolicy        string   `json:"approval_policy"`
			ProjectMemoryPolicy   string   `json:"project_memory_policy"`
			ContextSourcePolicy   string   `json:"context_source_policy"`
		} `json:"profile_posture"`
	} `json:"data"`
}

func TestCodeIntelligenceModelDogfood(t *testing.T) {
	if strings.TrimSpace(os.Getenv(dogfoodGateEnv)) != "1" {
		t.Skip("set HECATE_CODEINTEL_AGENT_DOGFOOD=1 to run the real-model Project scorecard")
	}
	cfg := readCodeIntelligenceDogfoodConfig(t)
	// Build before capturing source state so the recipe's repository-local Go
	// cache cannot make the scorecard look like it modified its own checkout.
	_ = hecateBinary(t)
	repositoryRoot := moduleRootDir()
	sourceSnapshot := codeIntelligenceDogfoodSourceState(t, repositoryRoot)
	if sourceSnapshot.Dirty {
		t.Fatalf("%s requires a clean committed worktree so managed Project workspaces match the tested source revision", dogfoodGateEnv)
	}
	sourceRevision := sourceSnapshot.Revision
	capabilities := probeCodeIntelligenceDogfoodCapabilities(t, repositoryRoot)

	baseURL := gatewayServer(t, codeIntelligenceDogfoodGatewayEnv(t, cfg)...)
	health := getJSON[codeIntelligenceDogfoodHealth](t, baseURL+"/healthz")
	sandboxKind := codeIntelligenceDogfoodSandboxKind(health.Sandbox.OSIsolation.Kind)
	if sandboxKind == "unknown" {
		t.Fatalf("health endpoint reported unsupported sandbox kind")
	}
	generatedAt := time.Now().UTC()
	card := dogfoodScorecard{
		SchemaVersion:   dogfoodScorecardSchemaVersion,
		GeneratedAt:     generatedAt.Format(time.RFC3339),
		SourceRevision:  sourceRevision,
		HarnessRevision: "project-agent-loop-v1",
		Environment: dogfoodEnvironment{
			Version:        codeIntelligenceDogfoodBuildVersion(health.Version),
			OS:             runtime.GOOS,
			Arch:           runtime.GOARCH,
			SandboxWrapper: sandboxKind,
			SourceDirty:    sourceSnapshot.Dirty,
			ModelProvider:  cfg.Provider,
			Model:          cfg.Model,
			Providers:      codeIntelligenceDogfoodReportCapabilities(capabilities),
		},
	}

	project := createCodeIntelligenceDogfoodProject(t, baseURL, cfg, generatedAt.Format("20060102T150405"), sourceRevision)
	scenarios := codeIntelligenceDogfoodScenarios()
	for repeat := 1; repeat <= cfg.Repeats; repeat++ {
		for _, scenario := range scenarios {
			if scenario.ForcedMissing {
				continue
			}
			capability := codeIntelligenceDogfoodCapabilityForLanguage(capabilities, scenario.Language)
			capture := launchCodeIntelligenceDogfoodScenario(t, project, scenario, repeat, cfg.Timeout, capability.Language, capability.ProviderToken, capability.Provider, capability.Version)
			card.Scenarios = append(card.Scenarios, buildDogfoodScenarioResult(
				codeIntelligenceDogfoodExpectation(scenario, repeat, capability, sandboxKind),
				codeIntelligenceDogfoodObservation(capture, capability.Version),
			))
		}
	}

	missingProviderPath := filepath.Join(t.TempDir(), "missing-gopls")
	missingBaseURL := gatewayServer(t, codeIntelligenceDogfoodGatewayEnv(t, cfg, "HECATE_CODEINTEL_GOPLS_PATH="+missingProviderPath)...)
	missingProject := createCodeIntelligenceDogfoodProject(t, missingBaseURL, cfg, generatedAt.Format("20060102T150405")+" missing", sourceRevision)
	for repeat := 1; repeat <= cfg.Repeats; repeat++ {
		for _, scenario := range scenarios {
			if !scenario.ForcedMissing {
				continue
			}
			capability := codeIntelligenceDogfoodCapabilityForLanguage(capabilities, scenario.Language)
			capture := launchCodeIntelligenceDogfoodScenario(t, missingProject, scenario, repeat, cfg.Timeout, capability.Language, capability.Provider, capability.Provider, "")
			card.Scenarios = append(card.Scenarios, buildDogfoodScenarioResult(
				codeIntelligenceDogfoodExpectation(scenario, repeat, capability, sandboxKind),
				codeIntelligenceDogfoodObservation(capture, ""),
			))
		}
	}

	finalSourceSnapshot := codeIntelligenceDogfoodSourceState(t, repositoryRoot)
	if finalSourceSnapshot.Dirty || finalSourceSnapshot.Revision != sourceRevision || finalSourceSnapshot.IgnoredPathsDigest != sourceSnapshot.IgnoredPathsDigest {
		t.Fatal("source tracked state or ignored-path snapshot changed while the dogfood scorecard was running")
	}
	card = finalizeDogfoodScorecard(card)
	reportDir, err := dogfoodReportDirectory(repositoryRoot, cfg.ReportDir, generatedAt)
	if err != nil {
		t.Fatalf("create code intelligence dogfood report directory: %v", err)
	}
	jsonPath, markdownPath, err := writeDogfoodScorecard(reportDir, card)
	if err != nil {
		t.Fatalf("write code intelligence dogfood scorecard: %v", err)
	}
	t.Logf("code intelligence dogfood scorecard: pass=%d fail=%d inconclusive=%d skipped=%d", card.Summary.Pass, card.Summary.Fail, card.Summary.Inconclusive, card.Summary.Skipped)
	t.Logf("scorecard JSON: %s", jsonPath)
	t.Logf("scorecard Markdown: %s", markdownPath)
	if cfg.Strict && card.Summary.Fail > 0 {
		t.Fatalf("strict code intelligence dogfood scorecard has %d failed scenario(s)", card.Summary.Fail)
	}
}

func codeIntelligenceDogfoodReportCapabilities(probes []codeIntelligenceDogfoodCapabilityProbe) []dogfoodProviderCapability {
	out := make([]dogfoodProviderCapability, 0, len(probes))
	for _, probe := range probes {
		out = append(out, dogfoodProviderCapability{
			Language:   probe.Language,
			Provider:   probe.Provider,
			Version:    probe.Version,
			Status:     probe.Status,
			Available:  probe.Available,
			Operations: append([]string(nil), probe.Operations...),
		})
	}
	return out
}

func codeIntelligenceDogfoodExpectation(scenario codeIntelligenceDogfoodScenario, repeat int, capability codeIntelligenceDogfoodCapabilityProbe, sandboxKind string) dogfoodScenarioExpectation {
	expected := dogfoodScenarioExpectation{
		ID:                  fmt.Sprintf("%s-r%d", scenario.ID, repeat),
		Language:            scenario.Language,
		Intent:              scenario.Intent,
		ExpectedRoute:       scenario.ExpectedRoute,
		Posture:             dogfoodScenarioPosture{ToolsEnabled: true, WritesAllowed: !scenario.Restricted, NetworkAllowed: false},
		Provider:            capability.Provider,
		ProviderVersion:     capability.Version,
		ProviderAvailable:   capability.Available,
		ForcedUnavailable:   scenario.ForcedMissing,
		PolicyRepresentable: !scenario.Restricted || sandboxKind != "bwrap",
		SemanticPermitted:   sandboxKind != "none",
	}
	if scenario.ForcedMissing {
		expected.ProviderAvailable = false
		expected.ProviderVersion = ""
	}
	switch scenario.ExpectedRoute {
	case "semantic":
		expected.PreferredOperations = []string{"definition", "references", "hover", "document_symbols", "workspace_symbols", "diagnostics"}
	case "structural":
		expected.PreferredOperations = []string{"structural_search"}
	case "fallback", "policy":
		expected.PreferredOperations = []string{"grep", "structural_search"}
	}
	return expected
}

func codeIntelligenceDogfoodObservation(capture codeIntelligenceDogfoodCapture, providerVersion string) dogfoodScenarioObservation {
	observedProviderVersion := ""
	if capture.ProviderVersionObserved {
		observedProviderVersion = providerVersion
	}
	return dogfoodScenarioObservation{
		RunRef: dogfoodRunRef{
			TaskID:  capture.TaskID,
			RunID:   capture.RunID,
			TraceID: capture.TraceID,
		},
		RunStatus:                     capture.RunStatus,
		ToolRoute:                     append([]string(nil), capture.ToolRoute...),
		ToolRouteModelCalls:           append([]int(nil), capture.ToolRouteModelCalls...),
		FirstInspectionTool:           capture.FirstInspectionTool,
		ModelCalls:                    capture.ModelCalls,
		CodeIntelligenceCalls:         capture.CodeIntelligenceCalls,
		SemanticCalls:                 capture.SemanticCalls,
		StructuralCalls:               capture.StructuralCalls,
		GrepCalls:                     capture.GrepCalls,
		GrepSuccessfulCalls:           capture.GrepSuccessfulCalls,
		Provider:                      capture.Provider,
		ProviderVersion:               observedProviderVersion,
		ResultCount:                   capture.ResultCount,
		SemanticResultCount:           capture.SemanticResultCount,
		StructuralResultCount:         capture.StructuralResultCount,
		GrepResultCount:               capture.GrepResultCount,
		SemanticProviderFailureCall:   capture.SemanticProviderFailureCall,
		StructuralProviderFailureCall: capture.StructuralProviderFailureCall,
		GrepResultModelCall:           capture.GrepResultModelCall,
		StructuralResultModelCall:     capture.StructuralResultModelCall,
		QueryLatencyMillis:            capture.QueryLatencyMillis,
		RunLatencyMillis:              capture.RunLatencyMillis,
		CostMicrosUSD:                 capture.CostMicrosUSD,
		ProcessCleanup:                capture.ProcessCleanup,
		WorkspaceChangeCount:          capture.WorkspaceChangeCount,
		CapabilitiesBeforeQuery:       capture.CapabilitiesBeforeQuery,
		ProviderVersionObserved:       capture.ProviderVersionObserved,
		ProviderUnavailableObserved:   capture.ProviderUnavailableObserved,
		SemanticPolicyBlocked:         capture.SemanticPolicyBlocked,
		Completed:                     capture.Completed,
		UsefulResult:                  capture.UsefulResult,
		UnexpectedToolCalls:           capture.UnexpectedToolCalls,
		ErrorKinds:                    append([]string(nil), capture.CodeIntelligenceErrorKind...),
	}
}

func codeIntelligenceDogfoodSandboxKind(value string) string {
	switch strings.TrimSpace(value) {
	case "none", "bwrap", "sandbox-exec":
		return strings.TrimSpace(value)
	default:
		return "unknown"
	}
}

func codeIntelligenceDogfoodBuildVersion(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return "unknown"
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '.' || char == '_' || char == '+' || char == '-' {
			continue
		}
		return "unknown"
	}
	return value
}

func readCodeIntelligenceDogfoodConfig(t *testing.T) codeIntelligenceDogfoodConfig {
	t.Helper()
	provider := strings.TrimSpace(os.Getenv(dogfoodProviderEnv))
	model := strings.TrimSpace(os.Getenv(dogfoodModelEnv))
	if provider == "" || model == "" {
		t.Fatalf("%s and %s are required when %s=1", dogfoodProviderEnv, dogfoodModelEnv, dogfoodGateEnv)
	}
	if strings.ContainsAny(model, ",\r\n") {
		t.Fatalf("%s must be one model id without commas or newlines", dogfoodModelEnv)
	}
	if len(model) > 128 {
		t.Fatalf("%s must not exceed 128 bytes", dogfoodModelEnv)
	}
	for _, char := range model {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("._:/+@-", char) {
			continue
		}
		t.Fatalf("%s contains an unsupported character", dogfoodModelEnv)
	}
	if provider != strings.ToLower(provider) {
		t.Fatalf("%s must use the provider's lowercase id", dogfoodProviderEnv)
	}
	_ = codeIntelligenceDogfoodProviderEnvPrefix(t, provider)
	return codeIntelligenceDogfoodConfig{
		Provider:  provider,
		Model:     model,
		Timeout:   codeIntelligenceDogfoodPositiveDuration(t, dogfoodTimeoutEnv, 180*time.Second),
		ReportDir: strings.TrimSpace(os.Getenv(dogfoodReportDirEnv)),
		Strict:    strings.TrimSpace(os.Getenv(dogfoodStrictEnv)) == "1",
		Repeats:   codeIntelligenceDogfoodPositiveInt(t, dogfoodRepeatsEnv, 1, 5),
	}
}

func probeCodeIntelligenceDogfoodCapabilities(t *testing.T, repositoryRoot string) []codeIntelligenceDogfoodCapabilityProbe {
	t.Helper()
	result, err := codeintel.NewService().Query(context.Background(), repositoryRoot, codeintel.Request{Operation: codeintel.OpCapabilities})
	if err != nil {
		t.Fatalf("probe code intelligence capabilities: %v", err)
	}
	probes := make([]codeIntelligenceDogfoodCapabilityProbe, 0, len(result.Capabilities))
	for _, capability := range result.Capabilities {
		provider := codeIntelligenceDogfoodProviderForLanguage(capability.Language)
		providerToken := strings.TrimSpace(capability.Provider)
		if providerToken == "" {
			providerToken = provider
		}
		status := strings.TrimSpace(capability.Status)
		if status != "installed_unverified" && status != "unavailable" {
			status = "unknown"
		}
		probes = append(probes, codeIntelligenceDogfoodCapabilityProbe{
			Language:      capability.Language,
			Provider:      provider,
			ProviderToken: providerToken,
			Version:       strings.TrimSpace(capability.Version),
			Available:     capability.Available,
			Status:        status,
			Operations:    codeIntelligenceDogfoodOperations(capability.Operations),
		})
	}
	return probes
}

func codeIntelligenceDogfoodOperations(operations []codeintel.Operation) []string {
	out := make([]string, 0, len(operations))
	for _, operation := range operations {
		if normalized := codeIntelligenceDogfoodOperation(string(operation)); normalized != "" {
			out = append(out, normalized)
		}
	}
	return out
}

func codeIntelligenceDogfoodCapabilityForLanguage(probes []codeIntelligenceDogfoodCapabilityProbe, language string) codeIntelligenceDogfoodCapabilityProbe {
	target := language
	if language == "python" || language == "rust" {
		target = "structural"
	}
	for _, probe := range probes {
		if probe.Language == target {
			return probe
		}
	}
	return codeIntelligenceDogfoodCapabilityProbe{Language: target, Provider: "unknown", Status: "unknown"}
}

func codeIntelligenceDogfoodSourceState(t *testing.T, repositoryRoot string) codeIntelligenceDogfoodSourceSnapshot {
	t.Helper()
	revision := codeIntelligenceDogfoodRevisionAt(t, repositoryRoot)
	runner := gitrunner.NewLocalRunner()
	status, err := runner.StatusPorcelain(context.Background(), repositoryRoot, 256*1024)
	if err != nil {
		t.Fatalf("inspect source status: %v", err)
	}
	view, err := runner.NewReadOnlyView(context.Background(), repositoryRoot)
	if err != nil {
		t.Fatalf("create passive ignored-path source view: %v", err)
	}
	defer view.Close()
	ignored, err := view.RunLimited(context.Background(), dogfoodIgnoredSnapshotMaxBytes, "--no-pager", "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--", ".")
	if err != nil {
		t.Fatalf("inspect source ignored paths: %v", err)
	}
	if ignored.StdoutTruncated {
		t.Fatal("source ignored-path snapshot exceeded its bound")
	}
	digest := sha256.Sum256([]byte(ignored.Stdout))
	return codeIntelligenceDogfoodSourceSnapshot{
		Revision:           revision,
		Dirty:              status != "",
		IgnoredPathsDigest: fmt.Sprintf("sha256:%x", digest[:]),
	}
}

func codeIntelligenceDogfoodRevisionAt(t *testing.T, repositoryRoot string) string {
	t.Helper()
	runner := gitrunner.NewLocalRunner()
	view, err := runner.NewReadOnlyView(context.Background(), repositoryRoot)
	if err != nil {
		t.Fatalf("create passive source view: %v", err)
	}
	defer view.Close()
	result, err := view.RunLimited(context.Background(), 4*1024, "--no-pager", "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("resolve source revision: %v", err)
	}
	revision := strings.TrimSpace(result.Stdout)
	if !codeIntelligenceDogfoodValidRevision(revision) {
		t.Fatalf("source revision has unexpected shape")
	}
	return revision
}

func codeIntelligenceDogfoodValidRevision(revision string) bool {
	if len(revision) != 40 && len(revision) != 64 {
		return false
	}
	for _, char := range revision {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F') {
			continue
		}
		return false
	}
	return true
}

func codeIntelligenceDogfoodPositiveDuration(t *testing.T, name string, fallback time.Duration) time.Duration {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 || seconds > 300 {
		t.Fatalf("%s must be an integer from 1 through 300", name)
	}
	return time.Duration(seconds) * time.Second
}

func codeIntelligenceDogfoodPositiveInt(t *testing.T, name string, fallback, maximum int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 || value > maximum {
		t.Fatalf("%s must be an integer from 1 through %d", name, maximum)
	}
	return value
}

func codeIntelligenceDogfoodProviderEnvPrefix(t *testing.T, provider string) string {
	t.Helper()
	provider = strings.TrimSpace(provider)
	if provider == "" {
		t.Fatal("dogfood provider is empty")
	}
	for _, char := range provider {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_' || char == '-' {
			continue
		}
		t.Fatalf("%s must contain only ASCII letters, digits, underscores, or hyphens", dogfoodProviderEnv)
	}
	return strings.ToUpper(strings.ReplaceAll(provider, "-", "_"))
}

func codeIntelligenceDogfoodGatewayEnv(t *testing.T, cfg codeIntelligenceDogfoodConfig, extra ...string) []string {
	t.Helper()
	prefix := codeIntelligenceDogfoodProviderEnvPrefix(t, cfg.Provider)
	// gatewayServer's generic provider scanner splits names at the first
	// underscore. Put the model catalog in the inherited environment so a
	// provider such as together_ai cannot accidentally enable together.
	t.Setenv("PROVIDER_"+prefix+"_MODELS", cfg.Model)
	env := []string{
		"HECATE_BACKEND=sqlite",
		"HECATE_TASK_APPROVAL_POLICIES=shell_exec,git_exec,file_write",
		"GOCACHE=" + t.TempDir(),
		fmt.Sprintf("HECATE_TASK_AGENT_LOOP_MAX_MODEL_CALLS=%d", dogfoodMaxModelCalls),
		"PROVIDER_" + prefix + "_PRECONFIGURED=1",
	}
	return append(env, extra...)
}

func createCodeIntelligenceDogfoodProject(t *testing.T, baseURL string, cfg codeIntelligenceDogfoodConfig, suffix, sourceRevision string) codeIntelligenceDogfoodProject {
	t.Helper()
	repositoryRoot, err := filepath.EvalSymlinks(moduleRootDir())
	if err != nil {
		t.Fatalf("canonicalize module root: %v", err)
	}
	project := postJSONDecodeStatus[e2eProjectResponse](t, baseURL+"/hecate/v1/projects", e2eProjectLaunchJSON(t, map[string]any{
		"name":                   "Code intelligence dogfood " + suffix,
		"workspace_path":         repositoryRoot,
		"workspace_kind":         "git",
		"default_provider":       cfg.Provider,
		"default_model":          cfg.Model,
		"default_workspace_mode": "persistent",
	}), http.StatusCreated)
	if project.Data.ID == "" {
		t.Fatal("dogfood project id is empty")
	}

	const presetInstructions = "Inspect only the requested code. Do not modify files or run commands. Finish with the exact identifier requested and a one-sentence explanation."
	for _, preset := range []struct {
		ID             string
		Name           string
		WritesAllowed  bool
		NetworkAllowed bool
	}{
		{ID: "preset_codeintel_permissive", Name: "Code intelligence permissive dogfood", WritesAllowed: true, NetworkAllowed: false},
		{ID: "preset_codeintel_restricted", Name: "Code intelligence restricted dogfood", WritesAllowed: false, NetworkAllowed: false},
	} {
		created := postJSONDecodeStatus[e2eAgentPresetResponse](t, baseURL+"/hecate/v1/agent-presets", e2eProjectLaunchJSON(t, map[string]any{
			"id":                    preset.ID,
			"name":                  preset.Name,
			"instructions":          presetInstructions,
			"surface":               "hecate_task",
			"provider_hint":         cfg.Provider,
			"model_hint":            cfg.Model,
			"tools_enabled":         true,
			"writes_allowed":        preset.WritesAllowed,
			"network_allowed":       preset.NetworkAllowed,
			"browser_allowed":       false,
			"approval_policy":       "allow",
			"project_memory_policy": "exclude",
			"context_source_policy": "exclude",
		}), http.StatusCreated)
		if created.Data.ID != preset.ID || !created.Data.ToolsEnabled {
			t.Fatalf("created dogfood preset = %+v, want tools-enabled %q", created.Data, preset.ID)
		}
	}

	roles := []struct {
		ID       string
		Name     string
		PresetID string
	}{
		{ID: "role_codeintel_permissive", Name: "Code intelligence permissive reviewer", PresetID: "preset_codeintel_permissive"},
		{ID: "role_codeintel_restricted", Name: "Code intelligence restricted reviewer", PresetID: "preset_codeintel_restricted"},
	}
	for _, role := range roles {
		created := postJSONDecodeStatus[e2eProjectWorkRoleResponse](t, baseURL+"/hecate/v1/projects/"+project.Data.ID+"/roles", e2eProjectLaunchJSON(t, map[string]any{
			"id":                    role.ID,
			"name":                  role.Name,
			"default_driver_kind":   "hecate_task",
			"default_agent_profile": role.PresetID,
		}), http.StatusCreated)
		if created.Data.ID != role.ID {
			t.Fatalf("created dogfood role id = %q, want %q", created.Data.ID, role.ID)
		}
	}

	return codeIntelligenceDogfoodProject{
		BaseURL:          baseURL,
		ProjectID:        project.Data.ID,
		PermissiveRoleID: roles[0].ID,
		RestrictedRoleID: roles[1].ID,
		Provider:         cfg.Provider,
		Model:            cfg.Model,
		SourceRevision:   sourceRevision,
		SourcePath:       repositoryRoot,
	}
}

func codeIntelligenceDogfoodScenarios() []codeIntelligenceDogfoodScenario {
	const instruction = "Begin by checking which precise code-navigation capabilities are available. Prefer the most precise permitted inspection and use bounded text search only as a fallback. Do not modify files, run commands, or inspect unrelated paths. "
	return []codeIntelligenceDogfoodScenario{
		{
			ID:            "go-semantic",
			Language:      "go",
			Intent:        "semantic_symbol_lookup",
			ExpectedRoute: "semantic",
			Prompt:        instruction + "In internal/orchestrator/executor_agent_loop_code_intelligence.go, which function decides whether task posture blocks semantic code intelligence? Return its exact identifier.",
			AnswerMarker:  "semanticCodeIntelligencePolicyBlock",
		},
		{
			ID:            "typescript-semantic",
			Language:      "typescript",
			Intent:        "semantic_symbol_lookup",
			ExpectedRoute: "semantic",
			Prompt:        instruction + "In ui/src/lib/agent-terminal-tools.ts, which exported function maps a terminal tool name to its activity title? Return its exact identifier.",
			AnswerMarker:  "agentTerminalToolActivityTitle",
		},
		{
			ID:            "python-structural",
			Language:      "python",
			Intent:        "structural_function_lookup",
			ExpectedRoute: "structural",
			Prompt:        instruction + "In e2e/testdata/code-intelligence-dogfood/python_target.py, which function returns the CODEINTEL_PYTHON_READY marker? Return its exact identifier.",
			AnswerMarker:  "dogfood_python_target",
		},
		{
			ID:            "rust-structural",
			Language:      "rust",
			Intent:        "structural_function_lookup",
			ExpectedRoute: "structural",
			Prompt:        instruction + "In e2e/testdata/code-intelligence-dogfood/rust_target.rs, which function returns the CODEINTEL_RUST_READY marker? Return its exact identifier.",
			AnswerMarker:  "dogfood_rust_target",
		},
		{
			ID:            "restricted-posture",
			Language:      "go",
			Intent:        "policy_aware_lookup",
			ExpectedRoute: "policy",
			Prompt:        instruction + "In internal/orchestrator/executor_agent_loop_code_intelligence.go, which function decides whether task posture blocks semantic code intelligence? Return its exact identifier.",
			AnswerMarker:  "semanticCodeIntelligencePolicyBlock",
			Restricted:    true,
		},
		{
			ID:            "missing-go-provider",
			Language:      "go",
			Intent:        "missing_provider_fallback",
			ExpectedRoute: "fallback",
			Prompt:        instruction + "In internal/orchestrator/executor_agent_loop_code_intelligence.go, which function decides whether task posture blocks semantic code intelligence? Return its exact identifier.",
			AnswerMarker:  "semanticCodeIntelligencePolicyBlock",
			ForcedMissing: true,
		},
	}
}

func launchCodeIntelligenceDogfoodScenario(t *testing.T, project codeIntelligenceDogfoodProject, scenario codeIntelligenceDogfoodScenario, repeat int, timeout time.Duration, providerLanguage, providerToken, provider, providerVersion string) codeIntelligenceDogfoodCapture {
	t.Helper()
	token := strings.ReplaceAll(scenario.ID, "-", "_") + "_r" + strconv.Itoa(repeat)
	workItemID := "work_" + token
	assignmentID := "asgn_" + token
	roleID := project.PermissiveRoleID
	if scenario.Restricted {
		roleID = project.RestrictedRoleID
	}

	postJSONDecodeStatus[e2eProjectWorkItemResponse](t, project.BaseURL+"/hecate/v1/projects/"+project.ProjectID+"/work-items", e2eProjectLaunchJSON(t, map[string]any{
		"id":       workItemID,
		"title":    "Code intelligence dogfood " + scenario.ID,
		"brief":    scenario.Prompt,
		"priority": "normal",
	}), http.StatusCreated)
	postJSONDecodeStatus[e2eProjectWorkAssignmentResponse](t, project.BaseURL+"/hecate/v1/projects/"+project.ProjectID+"/work-items/"+workItemID+"/assignments", e2eProjectLaunchJSON(t, map[string]any{
		"id":          assignmentID,
		"role_id":     roleID,
		"driver_kind": "hecate_task",
	}), http.StatusCreated)

	assignmentURL := project.BaseURL + "/hecate/v1/projects/" + project.ProjectID + "/work-items/" + workItemID + "/assignments/" + assignmentID
	readiness := getJSON[codeIntelligenceDogfoodReadinessResponse](t, assignmentURL+"/launch-readiness")
	if !readiness.Data.Ready || readiness.Data.Status != "ready" {
		t.Fatalf("dogfood launch readiness status=%q blockers=%v", readiness.Data.Status, readiness.Data.Blockers)
	}
	if readiness.Data.Provider != project.Provider || readiness.Data.Model != project.Model {
		t.Fatalf("dogfood readiness route = provider %q model %q, want configured route", readiness.Data.Provider, readiness.Data.Model)
	}
	if !readiness.Data.ProfilePosture.ToolsEnabled || readiness.Data.ProfilePosture.WritesAllowed != !scenario.Restricted || readiness.Data.ProfilePosture.NetworkAllowed {
		t.Fatalf("dogfood launch posture = %+v, restricted=%t", readiness.Data.ProfilePosture, scenario.Restricted)
	}
	if readiness.Data.ProfilePosture.BrowserEvidenceStatus != "disabled" || readiness.Data.ProfilePosture.BrowserAllowed ||
		len(readiness.Data.ProfilePosture.BrowserAllowedOrigins) != 0 || readiness.Data.ProfilePosture.ApprovalPolicy != "allow" ||
		readiness.Data.ProfilePosture.ProjectMemoryPolicy != "exclude" || readiness.Data.ProfilePosture.ContextSourcePolicy != "exclude" {
		t.Fatalf("dogfood launch context posture = %+v, want browser disabled and project context excluded", readiness.Data.ProfilePosture)
	}
	preflight := getJSON[e2eProjectLaunchContextResponse](t, assignmentURL+"/preflight")
	if preflight.Data.ExecutionMode != "hecate_task" || preflight.Data.Provider != project.Provider || preflight.Data.Model != project.Model {
		t.Fatalf("dogfood preflight = %+v, want resolved native task route", preflight.Data)
	}
	started := postJSONDecode[e2eProjectWorkAssignmentLaunchResponse](t, assignmentURL+"/start", `{}`)
	ref := started.Data.ExecutionRef
	if ref.Kind != "task_run" || ref.TaskID == "" || ref.RunID == "" || ref.ContextSnapshotID == "" {
		t.Fatalf("dogfood execution ref = %+v, want task_run", ref)
	}
	run := waitForCodeIntelligenceDogfoodRun(t, project.BaseURL, ref.TaskID, ref.RunID, timeout)
	if run.Provider != project.Provider || run.Model != project.Model {
		t.Fatalf("dogfood terminal run route = provider %q model %q, want configured route", run.Provider, run.Model)
	}
	steps := getJSON[e2eTaskStepsResponse](t, project.BaseURL+"/hecate/v1/tasks/"+ref.TaskID+"/runs/"+ref.RunID+"/steps")
	events := getJSON[e2eTaskEventsResponse](t, project.BaseURL+"/hecate/v1/tasks/"+ref.TaskID+"/runs/"+ref.RunID+"/events")
	artifacts := getJSON[codeIntelligenceDogfoodArtifactsResponse](t, project.BaseURL+"/hecate/v1/tasks/"+ref.TaskID+"/runs/"+ref.RunID+"/artifacts")
	codeIntelligenceDogfoodAssertOrderedEvidence(t, steps.Data, events.Data)

	if run.WorkspacePath == "" {
		t.Fatal("dogfood terminal run omitted its managed workspace path")
	}
	workspacePath, err := filepath.EvalSymlinks(run.WorkspacePath)
	if err != nil {
		t.Fatalf("canonicalize dogfood managed workspace: %v", err)
	}
	sourceInfo, err := os.Stat(project.SourcePath)
	if err != nil {
		t.Fatalf("stat dogfood project source: %v", err)
	}
	workspaceInfo, err := os.Stat(workspacePath)
	if err != nil {
		t.Fatalf("stat dogfood managed workspace: %v", err)
	}
	if filepath.Clean(workspacePath) == filepath.Clean(project.SourcePath) || os.SameFile(sourceInfo, workspaceInfo) {
		t.Fatal("dogfood assignment resolved to the source checkout instead of an isolated managed workspace")
	}
	if codeIntelligenceDogfoodRevisionAt(t, run.WorkspacePath) != project.SourceRevision {
		t.Fatal("dogfood managed workspace revision does not match the recorded source revision")
	}
	workspaceChanges := codeIntelligenceDogfoodWorkspaceChangeCount(t, run.WorkspacePath)
	return captureCodeIntelligenceDogfoodRun(run, ref.TaskID, steps.Data, events.Data, artifacts.Data, scenario.AnswerMarker, providerLanguage, providerToken, provider, providerVersion, workspaceChanges)
}

func waitForCodeIntelligenceDogfoodRun(t *testing.T, baseURL, taskID, runID string, timeout time.Duration) e2eTaskRun {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last e2eTaskRun
	for time.Now().Before(deadline) {
		run := getJSON[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/runs/"+runID).Data
		last = run
		switch run.Status {
		case "completed", "failed", "cancelled":
			return run
		case "awaiting_approval":
			approvals := getJSON[e2eTaskApprovalsResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/approvals")
			for _, approval := range approvals.Data {
				if approval.Status != "pending" {
					continue
				}
				resolved := postJSONDecode[e2eTaskApprovalResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/approvals/"+approval.ID+"/resolve", `{"decision":"reject","note":"dogfood harness denies effectful tool requests"}`)
				if resolved.Data.Status != "rejected" {
					t.Fatalf("dogfood approval status = %q, want rejected", resolved.Data.Status)
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("dogfood run did not reach terminal state within %s; last_status=%q", timeout, last.Status)
	return e2eTaskRun{}
}

func captureCodeIntelligenceDogfoodRun(run e2eTaskRun, taskID string, steps []e2eTaskStep, events []e2eEventEnvelope, artifacts []codeIntelligenceDogfoodArtifact, answerMarker, providerLanguage, providerToken, provider, providerVersion string, workspaceChanges int) codeIntelligenceDogfoodCapture {
	routes, routeModelCalls := codeIntelligenceDogfoodToolRoutes(events)
	capture := codeIntelligenceDogfoodCapture{
		TaskID:               taskID,
		RunID:                run.ID,
		TraceID:              run.TraceID,
		RunStatus:            codeIntelligenceDogfoodSafeRunStatus(run.Status),
		ToolRoute:            routes,
		ToolRouteModelCalls:  routeModelCalls,
		FirstInspectionTool:  codeIntelligenceDogfoodFirstInspectionTool(routes),
		ModelCalls:           run.ModelCallCount,
		RunLatencyMillis:     codeIntelligenceDogfoodDurationMillis(run.StartedAt, run.FinishedAt),
		CostMicrosUSD:        run.TotalCostMicrosUSD,
		WorkspaceChangeCount: workspaceChanges,
		Completed:            run.Status == "completed",
		ProcessCleanup:       "not_measured",
	}
	for _, route := range routes {
		switch {
		case route == "code_intelligence:capabilities":
			capture.CodeIntelligenceCalls++
		case route == "code_intelligence:unknown":
			capture.CodeIntelligenceCalls++
			capture.UnexpectedToolCalls++
		case strings.HasPrefix(route, "code_intelligence:"):
			capture.CodeIntelligenceCalls++
			operation := strings.TrimPrefix(route, "code_intelligence:")
			if operation == string(codeintel.OpStructuralSearch) {
				capture.StructuralCalls++
			} else if codeIntelligenceDogfoodSemanticOperation(operation) {
				capture.SemanticCalls++
			}
		case route == "grep":
			capture.GrepCalls++
		case route == "other", route == "git_status", route == "git_diff":
			capture.UnexpectedToolCalls++
		}
	}
	capture.CapabilitiesBeforeQuery = codeIntelligenceDogfoodCapabilitiesBeforeQuery(events)

	for _, step := range steps {
		if step.ToolName == "grep" && step.Status == "completed" {
			capture.GrepSuccessfulCalls++
			matches := codeIntelligenceDogfoodNumber(step.Input["matches"])
			capture.GrepResultCount += matches
			if matches > 0 {
				capture.GrepResultModelCall = max(capture.GrepResultModelCall, codeIntelligenceDogfoodModelCall(step.Input["model_call_index"]))
			}
		}
		if step.ToolName != "code_intelligence" {
			continue
		}
		if codeIntelligenceDogfoodBool(step.OutputSummary["semantic_policy_blocked"]) || step.OutputSummary["policy"] == "sandbox_code_intelligence" {
			capture.SemanticPolicyBlocked = true
		}
		operation := codeIntelligenceDogfoodOperation(fmt.Sprint(step.Input["operation"]))
		if category := codeIntelligenceDogfoodErrorCategory(step); category != "" {
			capture.CodeIntelligenceErrorKind = append(capture.CodeIntelligenceErrorKind, category)
			if codeIntelligenceDogfoodProviderQueryFailure(category) {
				modelCall := codeIntelligenceDogfoodModelCall(step.Input["model_call_index"])
				switch {
				case codeIntelligenceDogfoodSemanticOperation(operation):
					capture.SemanticProviderFailureCall = codeIntelligenceDogfoodFirstPositive(capture.SemanticProviderFailureCall, modelCall)
				case operation == string(codeintel.OpStructuralSearch):
					capture.StructuralProviderFailureCall = codeIntelligenceDogfoodFirstPositive(capture.StructuralProviderFailureCall, modelCall)
				}
			}
		}
		if operation == "" || operation == string(codeintel.OpCapabilities) {
			continue
		}
		if capture.QueryLatencyMillis == 0 {
			capture.QueryLatencyMillis = codeIntelligenceDogfoodDurationMillis(step.StartedAt, step.FinishedAt)
		}
		items := codeIntelligenceDogfoodNumber(step.OutputSummary["items"])
		capture.ResultCount += items
		if operation == string(codeintel.OpStructuralSearch) {
			capture.StructuralResultCount += items
			if items > 0 {
				capture.StructuralResultModelCall = max(capture.StructuralResultModelCall, codeIntelligenceDogfoodModelCall(step.Input["model_call_index"]))
			}
		} else if codeIntelligenceDogfoodSemanticOperation(operation) {
			capture.SemanticResultCount += items
		}
		observedProvider := strings.TrimSpace(fmt.Sprint(step.OutputSummary["provider"]))
		if observedProvider != "" {
			if providerToken != "" && strings.EqualFold(observedProvider, providerToken) {
				capture.Provider = provider
			} else if normalized := codeIntelligenceDogfoodProvider(observedProvider); normalized != "" && normalized != provider {
				capture.Provider = normalized
			}
		}
	}
	sort.Strings(capture.CodeIntelligenceErrorKind)
	capture.CodeIntelligenceErrorKind = codeIntelligenceDogfoodUniqueStrings(capture.CodeIntelligenceErrorKind)
	capture.UsefulResult = codeIntelligenceDogfoodFinalContains(events, answerMarker)
	capture.ProviderVersionObserved = codeIntelligenceDogfoodConversationObservedVersion(artifacts, providerLanguage, providerToken, providerVersion)
	capture.ProviderUnavailableObserved = codeIntelligenceDogfoodConversationObservedUnavailable(artifacts, providerLanguage, providerToken)
	return capture
}

func codeIntelligenceDogfoodToolRoutes(events []e2eEventEnvelope) ([]string, []int) {
	routes := make([]string, 0)
	modelCalls := make([]int, 0)
	for _, event := range events {
		if event.Type != "assistant.tool_call_proposed" {
			continue
		}
		toolName := strings.TrimSpace(fmt.Sprint(event.Data["tool_name"]))
		if toolName == "code_intelligence" {
			input, _ := event.Data["input"].(map[string]any)
			operation := codeIntelligenceDogfoodOperation(fmt.Sprint(input["operation"]))
			if operation == "" {
				operation = "unknown"
			}
			routes = append(routes, "code_intelligence:"+operation)
			modelCalls = append(modelCalls, codeIntelligenceDogfoodModelCall(event.Data["model_call_index"]))
			continue
		}
		switch toolName {
		case "grep", "glob", "list_dir", "read_file", "git_status", "git_diff":
			routes = append(routes, toolName)
		default:
			routes = append(routes, "other")
		}
		modelCalls = append(modelCalls, codeIntelligenceDogfoodModelCall(event.Data["model_call_index"]))
	}
	return routes, modelCalls
}

func codeIntelligenceDogfoodCapabilitiesBeforeQuery(events []e2eEventEnvelope) bool {
	capabilityModelCall := 0
	decisionModelCall := 0
	for _, event := range events {
		if event.Type != "assistant.tool_call_proposed" {
			continue
		}
		toolName := strings.TrimSpace(fmt.Sprint(event.Data["tool_name"]))
		input, _ := event.Data["input"].(map[string]any)
		operation := codeIntelligenceDogfoodOperation(fmt.Sprint(input["operation"]))
		modelCall := codeIntelligenceDogfoodModelCall(event.Data["model_call_index"])
		if toolName == "code_intelligence" && operation == string(codeintel.OpCapabilities) {
			if capabilityModelCall == 0 {
				capabilityModelCall = modelCall
			}
			continue
		}
		if decisionModelCall == 0 {
			decisionModelCall = modelCall
		}
	}
	return capabilityModelCall > 0 && decisionModelCall > capabilityModelCall
}

func codeIntelligenceDogfoodOperation(value string) string {
	value = strings.TrimSpace(value)
	for _, operation := range []codeintel.Operation{
		codeintel.OpCapabilities,
		codeintel.OpDefinition,
		codeintel.OpReferences,
		codeintel.OpHover,
		codeintel.OpDocumentSymbols,
		codeintel.OpWorkspaceSymbols,
		codeintel.OpDiagnostics,
		codeintel.OpStructuralSearch,
	} {
		if value == string(operation) {
			return value
		}
	}
	return ""
}

func codeIntelligenceDogfoodSemanticOperation(operation string) bool {
	switch codeintel.Operation(operation) {
	case codeintel.OpDefinition, codeintel.OpReferences, codeintel.OpHover,
		codeintel.OpDocumentSymbols, codeintel.OpWorkspaceSymbols, codeintel.OpDiagnostics:
		return true
	default:
		return false
	}
}

func codeIntelligenceDogfoodInspectionRoute(route string) bool {
	return strings.HasPrefix(route, "code_intelligence:") || route == "grep" || route == "glob" || route == "list_dir" || route == "read_file" || route == "git_status" || route == "git_diff"
}

func codeIntelligenceDogfoodFirstInspectionTool(routes []string) string {
	for _, route := range routes {
		if codeIntelligenceDogfoodInspectionRoute(route) {
			return route
		}
	}
	return "none"
}

func codeIntelligenceDogfoodFinalContains(events []e2eEventEnvelope, marker string) bool {
	for _, event := range events {
		if event.Type == "assistant.final_answer" && strings.Contains(fmt.Sprint(event.Data["summary"]), marker) {
			return true
		}
	}
	return false
}

func codeIntelligenceDogfoodConversationObservedVersion(artifacts []codeIntelligenceDogfoodArtifact, language, provider, version string) bool {
	language = strings.TrimSpace(language)
	provider = strings.TrimSpace(provider)
	version = strings.TrimSpace(version)
	if language == "" || provider == "" || version == "" {
		return false
	}
	target := language + ": available via " + provider + " version=" + version + " ["
	return codeIntelligenceDogfoodConsumedCapabilityMatches(artifacts, func(content string) bool {
		return strings.Contains(content, target)
	})
}

func codeIntelligenceDogfoodConversationObservedUnavailable(artifacts []codeIntelligenceDogfoodArtifact, language, provider string) bool {
	target := strings.TrimSpace(language) + ": unavailable via " + strings.TrimSpace(provider) + " [unavailable]"
	return codeIntelligenceDogfoodConsumedCapabilityMatches(artifacts, func(content string) bool {
		return strings.Contains(content, target)
	})
}

func codeIntelligenceDogfoodConsumedCapabilityMatches(artifacts []codeIntelligenceDogfoodArtifact, matches func(string) bool) bool {
	if matches == nil {
		return false
	}
	for _, artifact := range artifacts {
		if artifact.Kind != "agent_conversation" || strings.TrimSpace(artifact.ContentText) == "" {
			continue
		}
		var messages []types.Message
		if err := json.Unmarshal([]byte(artifact.ContentText), &messages); err != nil {
			continue
		}
		capabilityCalls := make(map[string]struct{})
		for _, message := range messages {
			if message.Role != "assistant" {
				continue
			}
			for _, call := range message.ToolCalls {
				if call.Function.Name != "code_intelligence" {
					continue
				}
				var input struct {
					Operation string `json:"operation"`
				}
				if json.Unmarshal([]byte(call.Function.Arguments), &input) == nil && input.Operation == string(codeintel.OpCapabilities) {
					capabilityCalls[call.ID] = struct{}{}
				}
			}
		}
		for index, message := range messages {
			if message.Role != "tool" {
				continue
			}
			if _, ok := capabilityCalls[message.ToolCallID]; !ok || !matches(message.Content) {
				continue
			}
			for _, later := range messages[index+1:] {
				if later.Role == "assistant" {
					return true
				}
			}
		}
	}
	return false
}

func codeIntelligenceDogfoodAssertOrderedEvidence(t *testing.T, steps []e2eTaskStep, events []e2eEventEnvelope) {
	t.Helper()
	lastStepIndex := -1
	for _, step := range steps {
		if step.Index <= lastStepIndex {
			t.Fatalf("task step indexes are not strictly increasing: previous=%d current=%d", lastStepIndex, step.Index)
		}
		lastStepIndex = step.Index
	}
	var lastSequence int64
	for _, event := range events {
		if event.Sequence <= lastSequence {
			t.Fatalf("run event sequences are not strictly increasing: previous=%d current=%d", lastSequence, event.Sequence)
		}
		lastSequence = event.Sequence
	}
}

func codeIntelligenceDogfoodDurationMillis(started, finished string) int64 {
	start, startErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(started))
	end, endErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(finished))
	if startErr != nil || endErr != nil || end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

func codeIntelligenceDogfoodStatusCount(status string) int {
	if status == "" {
		return 0
	}
	return strings.Count(status, "\x00")
}

func TestCodeIntelligenceDogfoodWorkspaceChangeCountIncludesIgnoredFiles(t *testing.T) {
	workspace := codeIntelligenceDogfoodGitFixture(t)
	if got := codeIntelligenceDogfoodWorkspaceChangeCount(t, workspace); got != 0 {
		t.Fatalf("clean dogfood workspace change count = %d, want 0", got)
	}
	ignoredDir := filepath.Join(workspace, ".dogfood")
	if err := os.MkdirAll(ignoredDir, 0o700); err != nil {
		t.Fatalf("create ignored fixture directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ignoredDir, "evidence.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write ignored fixture file: %v", err)
	}
	if got := codeIntelligenceDogfoodWorkspaceChangeCount(t, workspace); got != 1 {
		t.Fatalf("ignored dogfood workspace change count = %d, want 1", got)
	}
}

func TestCodeIntelligenceDogfoodSourceStateIncludesIgnoredPaths(t *testing.T) {
	workspace := codeIntelligenceDogfoodGitFixture(t)
	before := codeIntelligenceDogfoodSourceState(t, workspace)
	ignoredDir := filepath.Join(workspace, ".dogfood")
	if err := os.MkdirAll(ignoredDir, 0o700); err != nil {
		t.Fatalf("create ignored source fixture directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ignoredDir, "evidence.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write ignored source fixture file: %v", err)
	}
	after := codeIntelligenceDogfoodSourceState(t, workspace)
	if before.Revision != after.Revision || before.Dirty || after.Dirty {
		t.Fatal("ignored source fixture unexpectedly changed tracked Git state")
	}
	if before.IgnoredPathsDigest == after.IgnoredPathsDigest {
		t.Fatal("ignored source path creation did not change the bounded source snapshot")
	}
}

func codeIntelligenceDogfoodGitFixture(t *testing.T) string {
	t.Helper()
	workspace := t.TempDir()
	ctx := context.Background()
	runner := gitrunner.NewLocalRunner()
	if _, err := runner.Run(ctx, workspace, "init", "--quiet"); err != nil {
		t.Fatalf("initialize dogfood Git fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".gitignore"), []byte(".dogfood/\n"), 0o600); err != nil {
		t.Fatalf("write fixture ignore file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "tracked.txt"), []byte("clean\n"), 0o600); err != nil {
		t.Fatalf("write tracked fixture: %v", err)
	}
	if _, err := runner.Run(ctx, workspace, "add", "--", ".gitignore", "tracked.txt"); err != nil {
		t.Fatalf("stage dogfood Git fixture: %v", err)
	}
	if _, err := runner.Run(ctx, workspace, "-c", "user.name=Dogfood Test", "-c", "user.email=dogfood@example.invalid", "commit", "--quiet", "-m", "fixture"); err != nil {
		t.Fatalf("commit dogfood Git fixture: %v", err)
	}
	return workspace
}

func codeIntelligenceDogfoodWorkspaceChangeCount(t *testing.T, workspace string) int {
	t.Helper()
	ctx := context.Background()
	runner := gitrunner.NewLocalRunner()
	status, err := runner.StatusPorcelain(ctx, workspace, 64*1024)
	if err != nil {
		t.Fatalf("measure dogfood workspace changes: %v", err)
	}
	view, err := runner.NewReadOnlyView(ctx, workspace)
	if err != nil {
		t.Fatalf("create dogfood ignored-file view: %v", err)
	}
	defer view.Close()
	ignored, err := view.RunLimited(ctx, 64*1024, "--no-pager", "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--", ".")
	if err != nil {
		t.Fatalf("measure dogfood ignored workspace changes: %v", err)
	}
	if ignored.StdoutTruncated {
		t.Fatal("dogfood ignored workspace change snapshot exceeded its bound")
	}
	return codeIntelligenceDogfoodStatusCount(status) + codeIntelligenceDogfoodStatusCount(ignored.Stdout)
}

func codeIntelligenceDogfoodNumber(value any) int {
	switch number := value.(type) {
	case float64:
		return int(number)
	case int:
		return number
	case int64:
		return int(number)
	case json.Number:
		parsed, _ := number.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func codeIntelligenceDogfoodModelCall(value any) int {
	modelCall := codeIntelligenceDogfoodNumber(value)
	if modelCall <= 0 || modelCall > dogfoodMaxModelCalls {
		return 0
	}
	return modelCall
}

func codeIntelligenceDogfoodBool(value any) bool {
	result, _ := value.(bool)
	return result
}

func codeIntelligenceDogfoodProvider(value string) string {
	value = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".exe")
	switch value {
	case "gopls":
		return "gopls"
	case "tsc", "tsgo":
		return "tsc"
	case "ast-grep", "sg":
		return "ast-grep"
	default:
		return ""
	}
}

func codeIntelligenceDogfoodProviderForLanguage(language string) string {
	switch strings.TrimSpace(language) {
	case "go":
		return "gopls"
	case "typescript":
		return "tsc"
	case "structural":
		return "ast-grep"
	default:
		return "unknown"
	}
}

func codeIntelligenceDogfoodErrorCategory(step e2eTaskStep) string {
	category := strings.TrimSpace(step.ErrorKind)
	if step.OutputSummary["policy"] == "sandbox_code_intelligence" {
		return "sandbox_policy_denied"
	}
	switch category {
	case "cancelled", "timeout", "diagnostics_incomplete", "invalid_workspace", "invalid_request", "provider_protocol", "provider_unavailable", "provider_error", "not_configured", "sandbox_policy_denied":
		return category
	default:
		return ""
	}
}

func codeIntelligenceDogfoodProviderQueryFailure(category string) bool {
	switch category {
	case "diagnostics_incomplete", "not_configured", "provider_error", "provider_protocol", "provider_unavailable", "timeout":
		return true
	default:
		return false
	}
}

func codeIntelligenceDogfoodFirstPositive(current, candidate int) int {
	if candidate <= 0 || (current > 0 && current <= candidate) {
		return current
	}
	return candidate
}

func codeIntelligenceDogfoodSafeRunStatus(value string) string {
	switch strings.TrimSpace(value) {
	case "completed", "failed", "cancelled":
		return strings.TrimSpace(value)
	default:
		return "unknown"
	}
}

func codeIntelligenceDogfoodUniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}

func TestCodeIntelligenceDogfoodCapabilitiesRequireLaterModelCall(t *testing.T) {
	capabilities := e2eEventEnvelope{Type: "assistant.tool_call_proposed", Data: map[string]any{
		"model_call_index": float64(1),
		"tool_name":        "code_intelligence",
		"input":            map[string]any{"operation": "capabilities"},
	}}
	semantic := e2eEventEnvelope{Type: "assistant.tool_call_proposed", Data: map[string]any{
		"model_call_index": float64(1),
		"tool_name":        "code_intelligence",
		"input":            map[string]any{"operation": "document_symbols"},
	}}
	if codeIntelligenceDogfoodCapabilitiesBeforeQuery([]e2eEventEnvelope{capabilities, semantic}) {
		t.Fatal("parallel capabilities and semantic proposals counted as capability-informed")
	}
	readFile := e2eEventEnvelope{Type: "assistant.tool_call_proposed", Data: map[string]any{
		"model_call_index": float64(1),
		"tool_name":        "read_file",
		"input":            map[string]any{"path": "safe.go"},
	}}
	if codeIntelligenceDogfoodCapabilitiesBeforeQuery([]e2eEventEnvelope{capabilities, readFile}) {
		t.Fatal("parallel capabilities and generic browsing counted as capability-informed")
	}
	semantic.Data["model_call_index"] = float64(2)
	if !codeIntelligenceDogfoodCapabilitiesBeforeQuery([]e2eEventEnvelope{capabilities, semantic}) {
		t.Fatal("later semantic proposal did not count as capability-informed")
	}
}

func TestCodeIntelligenceDogfoodConversationVersionRequiresConsumption(t *testing.T) {
	conversation := []types.Message{
		{Role: "assistant", ToolCalls: []types.ToolCall{{
			ID:   "call-capabilities",
			Type: "function",
			Function: types.ToolCallFunction{
				Name:      "code_intelligence",
				Arguments: `{"operation":"capabilities"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call-capabilities", Content: "go: available via gopls.exe version=0.20.0 [installed_unverified]"},
	}
	raw, err := json.Marshal(conversation)
	if err != nil {
		t.Fatalf("marshal conversation: %v", err)
	}
	artifacts := []codeIntelligenceDogfoodArtifact{{Kind: "agent_conversation", ContentText: string(raw)}}
	if codeIntelligenceDogfoodConversationObservedVersion(artifacts, "go", "gopls.exe", "0.20.0") {
		t.Fatal("unconsumed final tool result counted as observed")
	}
	conversation = append(conversation, types.Message{Role: "assistant", Content: "done"})
	raw, err = json.Marshal(conversation)
	if err != nil {
		t.Fatalf("marshal consumed conversation: %v", err)
	}
	artifacts[0].ContentText = string(raw)
	if !codeIntelligenceDogfoodConversationObservedVersion(artifacts, "go", "gopls.exe", "0.20.0") {
		t.Fatal("consumed capability version was not observed")
	}
	if codeIntelligenceDogfoodConversationObservedVersion(artifacts, "typescript", "tsc.exe", "0.20.0") ||
		codeIntelligenceDogfoodConversationObservedVersion(artifacts, "go", "gopls", "0.20.0") ||
		codeIntelligenceDogfoodConversationObservedVersion(artifacts, "go", "gopls.exe", "0.20") {
		t.Fatal("capability version matched the wrong provider line or a version prefix")
	}
}

func TestCodeIntelligenceDogfoodFallbackRequiresLaterModelCall(t *testing.T) {
	run := e2eTaskRun{
		ID: "run_order", Status: "completed", ModelCallCount: 3,
		StartedAt: "2026-08-08T10:00:00Z", FinishedAt: "2026-08-08T10:00:01Z",
	}
	steps := []e2eTaskStep{
		{
			Index: 2, ToolName: "code_intelligence", Status: "failed", ErrorKind: "provider_protocol",
			Input:         map[string]any{"operation": "document_symbols", "model_call_index": float64(2)},
			OutputSummary: map[string]any{"error_category": "provider_protocol"},
			StartedAt:     "2026-08-08T10:00:00Z", FinishedAt: "2026-08-08T10:00:00.100Z",
		},
		{
			Index: 3, ToolName: "grep", Status: "completed",
			Input:      map[string]any{"matches": float64(1), "model_call_index": float64(2)},
			StartedAt:  "2026-08-08T10:00:00.100Z",
			FinishedAt: "2026-08-08T10:00:00.200Z",
		},
	}
	events := []e2eEventEnvelope{
		{Sequence: 1, Type: "assistant.tool_call_proposed", Data: map[string]any{"model_call_index": float64(1), "tool_name": "code_intelligence", "input": map[string]any{"operation": "capabilities"}}},
		{Sequence: 2, Type: "assistant.tool_call_proposed", Data: map[string]any{"model_call_index": float64(2), "tool_name": "code_intelligence", "input": map[string]any{"operation": "document_symbols"}}},
		{Sequence: 3, Type: "assistant.tool_call_proposed", Data: map[string]any{"model_call_index": float64(2), "tool_name": "grep", "input": map[string]any{}}},
		{Sequence: 4, Type: "assistant.final_answer", Data: map[string]any{"summary": "expected_marker"}},
	}
	expected := dogfoodScenarioExpectation{
		ID: "provider-failure-r1", Language: "go", Intent: "semantic_symbol_lookup", ExpectedRoute: "semantic",
		Posture: dogfoodScenarioPosture{ToolsEnabled: true, WritesAllowed: true}, Provider: "gopls", ProviderAvailable: true, SemanticPermitted: true,
	}
	parallel := captureCodeIntelligenceDogfoodRun(run, "task_order", steps, events, nil, "expected_marker", "go", "gopls", "gopls", "", 0)
	parallelResult := buildDogfoodScenarioResult(expected, codeIntelligenceDogfoodObservation(parallel, ""))
	if parallelResult.Verdict != "fail" || parallelResult.Checks.CorrectFallback {
		t.Fatalf("parallel fallback verdict = %q checks=%+v reasons=%v, want temporal-order failure", parallelResult.Verdict, parallelResult.Checks, parallelResult.ReasonCodes)
	}

	steps[1].Input["model_call_index"] = float64(3)
	events[2].Data["model_call_index"] = float64(3)
	later := captureCodeIntelligenceDogfoodRun(run, "task_order", steps, events, nil, "expected_marker", "go", "gopls", "gopls", "", 0)
	laterResult := buildDogfoodScenarioResult(expected, codeIntelligenceDogfoodObservation(later, ""))
	if laterResult.Verdict != "inconclusive" || !laterResult.Checks.CorrectFallback {
		t.Fatalf("later fallback verdict = %q checks=%+v reasons=%v, want inconclusive recovery", laterResult.Verdict, laterResult.Checks, laterResult.ReasonCodes)
	}
}

func TestCodeIntelligenceDogfoodCaptureDoesNotRetainRawEvidence(t *testing.T) {
	const secret = "DOGFOOD_PRIVATE_SENTINEL"
	run := e2eTaskRun{
		ID:                 "run_safe",
		Status:             "completed",
		TraceID:            "trace_safe",
		ModelCallCount:     2,
		TotalCostMicrosUSD: 7,
		StartedAt:          "2026-08-08T10:00:00Z",
		FinishedAt:         "2026-08-08T10:00:01Z",
	}
	steps := []e2eTaskStep{{
		Index:         2,
		ToolName:      "code_intelligence",
		Status:        "completed",
		Input:         map[string]any{"operation": "document_symbols", "path": secret, "query": secret},
		OutputSummary: map[string]any{"provider": "gopls.exe", "items": float64(1)},
		Error:         secret,
		StartedAt:     "2026-08-08T10:00:00Z",
		FinishedAt:    "2026-08-08T10:00:00.100Z",
	}, {
		Index:      3,
		ToolName:   "grep",
		Status:     "completed",
		Input:      map[string]any{"pattern": secret, "path": secret, "matches": float64(1)},
		StartedAt:  "2026-08-08T10:00:00.100Z",
		FinishedAt: "2026-08-08T10:00:00.200Z",
	}}
	events := []e2eEventEnvelope{
		{Sequence: 1, Type: "assistant.tool_call_proposed", Data: map[string]any{"model_call_index": float64(1), "tool_name": "code_intelligence", "input": map[string]any{"operation": "capabilities", "query": secret}}},
		{Sequence: 2, Type: "assistant.tool_call_proposed", Data: map[string]any{"model_call_index": float64(2), "tool_name": "code_intelligence", "input": map[string]any{"operation": "document_symbols", "path": secret}}},
		{Sequence: 3, Type: "assistant.tool_call_proposed", Data: map[string]any{"model_call_index": float64(2), "tool_name": "grep", "input": map[string]any{"pattern": secret}}},
		{Sequence: 4, Type: "assistant.tool_call_proposed", Data: map[string]any{"model_call_index": float64(2), "tool_name": "shell_exec", "input": map[string]any{"command": secret}}},
		{Sequence: 5, Type: "assistant.final_answer", Data: map[string]any{"summary": secret + " expected_marker"}},
	}
	artifacts := []codeIntelligenceDogfoodArtifact{{Kind: "agent_conversation", ContentText: secret}}
	capture := captureCodeIntelligenceDogfoodRun(run, "task_safe", steps, events, artifacts, "expected_marker", "go", "gopls.exe", "gopls", "", 0)
	raw, err := json.Marshal(capture)
	if err != nil {
		t.Fatalf("marshal safe capture: %v", err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("safe capture retained raw evidence: %s", raw)
	}
	if !capture.UsefulResult || capture.Provider != "gopls" || capture.ResultCount != 1 || capture.GrepResultCount != 1 || capture.UnexpectedToolCalls != 1 {
		t.Fatalf("safe capture = %+v, want useful gopls result", capture)
	}

	card := finalizeDogfoodScorecard(dogfoodScorecard{
		SchemaVersion:   dogfoodScorecardSchemaVersion,
		GeneratedAt:     "2026-08-08T10:00:00Z",
		SourceRevision:  strings.Repeat("a", 40),
		HarnessRevision: "project-agent-loop-v1",
		Environment: dogfoodEnvironment{
			Version: "dev", OS: "test", Arch: "test", SandboxWrapper: "none",
			ModelProvider: "test", Model: "test-model",
		},
		Scenarios: []dogfoodScenarioResult{buildDogfoodScenarioResult(
			dogfoodScenarioExpectation{
				ID: "safe-r1", Language: "go", Intent: "semantic_symbol_lookup", ExpectedRoute: "semantic",
				Posture: dogfoodScenarioPosture{ToolsEnabled: true}, Provider: "gopls",
			},
			codeIntelligenceDogfoodObservation(capture, ""),
		)},
	})
	reportJSON, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal final scorecard: %v", err)
	}
	for name, serialized := range map[string]string{
		"JSON":     string(reportJSON),
		"Markdown": renderDogfoodScorecardMarkdown(card),
	} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("%s scorecard retained raw evidence", name)
		}
	}
}

func TestCodeIntelligenceDogfoodRevisionValidation(t *testing.T) {
	for _, revision := range []string{strings.Repeat("a", 40), strings.Repeat("F", 64)} {
		if !codeIntelligenceDogfoodValidRevision(revision) {
			t.Fatalf("valid revision rejected: length=%d", len(revision))
		}
	}
	for _, revision := range []string{"", strings.Repeat("a", 39), strings.Repeat("a", 65), strings.Repeat("g", 40)} {
		if codeIntelligenceDogfoodValidRevision(revision) {
			t.Fatalf("invalid revision accepted: length=%d", len(revision))
		}
	}
}
