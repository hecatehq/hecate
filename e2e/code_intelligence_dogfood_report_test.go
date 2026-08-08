//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	dogfoodScorecardSchemaVersion    = "hecate.code-intelligence-dogfood.v1"
	dogfoodProcessCleanupNotMeasured = "not_measured"
)

type dogfoodScorecard struct {
	SchemaVersion   string                  `json:"schema_version"`
	GeneratedAt     string                  `json:"generated_at"`
	SourceRevision  string                  `json:"source_revision"`
	HarnessRevision string                  `json:"harness_revision"`
	Environment     dogfoodEnvironment      `json:"environment"`
	Scenarios       []dogfoodScenarioResult `json:"scenarios"`
	Summary         dogfoodSummary          `json:"summary"`
}

type dogfoodEnvironment struct {
	Version        string                      `json:"version"`
	OS             string                      `json:"os"`
	Arch           string                      `json:"arch"`
	SandboxWrapper string                      `json:"sandbox_wrapper"`
	SourceDirty    bool                        `json:"source_dirty"`
	ModelProvider  string                      `json:"model_provider"`
	Model          string                      `json:"model"`
	Providers      []dogfoodProviderCapability `json:"providers"`
}

type dogfoodProviderCapability struct {
	Language   string   `json:"language"`
	Provider   string   `json:"provider"`
	Version    string   `json:"version,omitempty"`
	Status     string   `json:"status"`
	Available  bool     `json:"available"`
	Operations []string `json:"operations"`
}

type dogfoodScenarioPosture struct {
	ToolsEnabled   bool `json:"tools_enabled"`
	WritesAllowed  bool `json:"writes_allowed"`
	NetworkAllowed bool `json:"network_allowed"`
}

type dogfoodScenarioExpectation struct {
	ID                  string
	Language            string
	Intent              string
	ExpectedRoute       string
	Posture             dogfoodScenarioPosture
	PreferredOperations []string
	Provider            string
	ProviderVersion     string
	ProviderAvailable   bool
	ForcedUnavailable   bool
	PolicyRepresentable bool
	SemanticPermitted   bool
}

type dogfoodRunRef struct {
	TaskID  string `json:"task_id"`
	RunID   string `json:"run_id"`
	TraceID string `json:"trace_id,omitempty"`
}

type dogfoodScenarioObservation struct {
	RunRef                        dogfoodRunRef `json:"run_ref"`
	RunStatus                     string        `json:"run_status"`
	ToolRoute                     []string      `json:"tool_route"`
	ToolRouteModelCalls           []int         `json:"tool_route_model_calls"`
	FirstInspectionTool           string        `json:"first_inspection_tool"`
	ModelCalls                    int           `json:"model_calls"`
	CodeIntelligenceCalls         int           `json:"code_intelligence_calls"`
	SemanticCalls                 int           `json:"semantic_calls"`
	StructuralCalls               int           `json:"structural_calls"`
	GrepCalls                     int           `json:"grep_calls"`
	GrepSuccessfulCalls           int           `json:"grep_successful_calls"`
	Provider                      string        `json:"provider,omitempty"`
	ProviderVersion               string        `json:"observed_provider_version,omitempty"`
	ResultCount                   int           `json:"result_count"`
	SemanticResultCount           int           `json:"semantic_result_count"`
	StructuralResultCount         int           `json:"structural_result_count"`
	GrepResultCount               int           `json:"grep_result_count"`
	SemanticProviderFailureCall   int           `json:"semantic_provider_failure_model_call"`
	StructuralProviderFailureCall int           `json:"structural_provider_failure_model_call"`
	GrepResultModelCall           int           `json:"grep_result_model_call"`
	StructuralResultModelCall     int           `json:"structural_result_model_call"`
	QueryLatencyMillis            int64         `json:"query_latency_ms"`
	RunLatencyMillis              int64         `json:"run_latency_ms"`
	CostMicrosUSD                 int64         `json:"cost_micros_usd"`
	ProcessCleanup                string        `json:"process_cleanup"`
	WorkspaceChangeCount          int           `json:"workspace_change_count"`
	CapabilitiesBeforeQuery       bool          `json:"capabilities_before_query"`
	ProviderVersionObserved       bool          `json:"provider_version_observed"`
	ProviderUnavailableObserved   bool          `json:"provider_unavailable_observed"`
	SemanticPolicyBlocked         bool          `json:"semantic_policy_blocked"`
	Completed                     bool          `json:"completed"`
	UsefulResult                  bool          `json:"useful_result"`
	UnexpectedToolCalls           int           `json:"unexpected_tool_calls"`
	ErrorKinds                    []string      `json:"error_kinds,omitempty"`
}

type dogfoodScenarioChecks struct {
	CapabilitiesBeforeQuery     bool   `json:"capabilities_before_query"`
	FirstInspectionPreferred    bool   `json:"first_inspection_preferred"`
	PreferredRouteFirst         bool   `json:"preferred_route_first"`
	FallbackOnlyAfterPreferred  bool   `json:"fallback_only_after_preferred"`
	ProviderMatched             bool   `json:"provider_matched"`
	ProviderVersionObserved     bool   `json:"provider_version_observed"`
	ProviderUnavailableObserved bool   `json:"provider_unavailable_observed"`
	PreferredToolSelected       bool   `json:"preferred_tool_selected"`
	CorrectFallback             bool   `json:"correct_fallback"`
	UsefulResult                bool   `json:"useful_result"`
	Completed                   bool   `json:"completed"`
	NoWorkspaceWrites           bool   `json:"no_workspace_writes"`
	NoUnexpectedTools           bool   `json:"no_unexpected_tools"`
	PolicyBlockObserved         bool   `json:"policy_block_observed"`
	Cleanup                     string `json:"cleanup"`
}

type dogfoodScenarioResult struct {
	ID                      string                     `json:"id"`
	Language                string                     `json:"language"`
	Intent                  string                     `json:"intent"`
	ExpectedRoute           string                     `json:"expected_route"`
	Posture                 dogfoodScenarioPosture     `json:"posture"`
	PreferredOperations     []string                   `json:"preferred_operations"`
	ExpectedProvider        string                     `json:"expected_provider"`
	ExpectedVersion         string                     `json:"expected_version,omitempty"`
	PreferredRouteAvailable bool                       `json:"preferred_route_available"`
	FallbackApplicable      bool                       `json:"fallback_applicable"`
	PolicyRepresentable     bool                       `json:"policy_representable"`
	ProviderQueryFailed     bool                       `json:"provider_query_failed"`
	Observed                dogfoodScenarioObservation `json:"observed"`
	Checks                  dogfoodScenarioChecks      `json:"checks"`
	Verdict                 string                     `json:"verdict"`
	ReasonCodes             []string                   `json:"reason_codes"`
}

type dogfoodSummary struct {
	ScenarioCount           int     `json:"scenario_count"`
	Pass                    int     `json:"pass"`
	Fail                    int     `json:"fail"`
	Inconclusive            int     `json:"inconclusive"`
	Skipped                 int     `json:"skipped"`
	CapabilityAwarenessRate float64 `json:"capability_awareness_rate"`
	PreferredToolRate       float64 `json:"preferred_tool_rate"`
	PreferredToolMeasured   int     `json:"preferred_tool_measured"`
	FallbackCorrectnessRate float64 `json:"fallback_correctness_rate"`
	FallbackMeasured        int     `json:"fallback_measured"`
	UsefulResultRate        float64 `json:"useful_result_rate"`
	TaskCompletionRate      float64 `json:"task_completion_rate"`
	CleanupMeasured         int     `json:"cleanup_measured"`
	CleanupRate             float64 `json:"cleanup_rate"`
	QueryLatencyMedianMS    int64   `json:"query_latency_median_ms"`
	QueryLatencyMaxMS       int64   `json:"query_latency_max_ms"`
}

func buildDogfoodScenarioResult(expected dogfoodScenarioExpectation, observed dogfoodScenarioObservation) dogfoodScenarioResult {
	providerPrerequisiteMissing := !expected.ProviderAvailable && !expected.ForcedUnavailable &&
		(expected.ExpectedRoute == "semantic" || expected.ExpectedRoute == "structural")
	semanticPolicyUnavailable := expected.ExpectedRoute == "semantic" && !expected.SemanticPermitted
	providerFailureModelCall := 0
	switch expected.ExpectedRoute {
	case "semantic":
		if observed.SemanticResultCount == 0 {
			providerFailureModelCall = observed.SemanticProviderFailureCall
		}
	case "structural":
		if observed.StructuralResultCount == 0 {
			providerFailureModelCall = observed.StructuralProviderFailureCall
		}
	}
	providerQueryFailed := expected.ProviderAvailable && providerFailureModelCall > 0
	preferredRouteAdvertised := (expected.ExpectedRoute == "semantic" && expected.ProviderAvailable && expected.SemanticPermitted) ||
		(expected.ExpectedRoute == "structural" && expected.ProviderAvailable)
	preferredRouteAvailable := preferredRouteAdvertised && !providerQueryFailed
	fallbackApplicable := expected.ExpectedRoute == "fallback" ||
		(expected.ExpectedRoute == "policy" && expected.PolicyRepresentable) || providerPrerequisiteMissing || semanticPolicyUnavailable || providerQueryFailed
	requiresPolicyBlock := semanticPolicyUnavailable || (expected.ExpectedRoute == "policy" && expected.PolicyRepresentable)
	checks := dogfoodScenarioChecks{
		CapabilitiesBeforeQuery:     observed.CapabilitiesBeforeQuery,
		FirstInspectionPreferred:    observed.FirstInspectionTool == "code_intelligence:capabilities",
		PreferredRouteFirst:         !preferredRouteAdvertised || dogfoodPreferredRouteFirst(expected.ExpectedRoute, observed.ToolRoute),
		FallbackOnlyAfterPreferred:  !preferredRouteAdvertised || dogfoodFallbackOnlyAfterPreferred(expected.ExpectedRoute, observed, providerFailureModelCall),
		ProviderMatched:             !preferredRouteAvailable || (expected.Provider != "" && observed.Provider == expected.Provider),
		ProviderVersionObserved:     expected.ProviderVersion == "" || observed.ProviderVersionObserved,
		ProviderUnavailableObserved: !expected.ForcedUnavailable || observed.ProviderUnavailableObserved,
		UsefulResult:                observed.UsefulResult,
		Completed:                   observed.Completed,
		NoWorkspaceWrites:           observed.WorkspaceChangeCount == 0,
		NoUnexpectedTools:           observed.UnexpectedToolCalls == 0,
		PolicyBlockObserved:         !requiresPolicyBlock || observed.SemanticPolicyBlocked,
		Cleanup:                     observed.ProcessCleanup,
		CorrectFallback:             true,
	}

	switch expected.ExpectedRoute {
	case "semantic":
		if providerQueryFailed {
			checks.CorrectFallback = observed.SemanticCalls > 0 && dogfoodFallbackProducedResultsAfter(observed, providerFailureModelCall)
			checks.PreferredToolSelected = checks.CorrectFallback
		} else if providerPrerequisiteMissing || semanticPolicyUnavailable {
			checks.CorrectFallback = observed.SemanticCalls == 0 && dogfoodFallbackProducedResults(observed)
			checks.PreferredToolSelected = checks.CorrectFallback
		} else {
			checks.PreferredToolSelected = observed.SemanticCalls > 0 && observed.SemanticResultCount > 0
		}
	case "structural":
		if providerQueryFailed {
			checks.CorrectFallback = observed.StructuralCalls > 0 && observed.GrepResultCount > 0 && observed.GrepResultModelCall > providerFailureModelCall
			checks.PreferredToolSelected = checks.CorrectFallback
		} else if providerPrerequisiteMissing {
			checks.CorrectFallback = observed.StructuralCalls == 0 && observed.GrepResultCount > 0
			checks.PreferredToolSelected = checks.CorrectFallback
		} else {
			checks.PreferredToolSelected = observed.StructuralCalls > 0 && observed.StructuralResultCount > 0
		}
	case "fallback", "policy":
		if expected.ExpectedRoute == "policy" && !expected.PolicyRepresentable {
			checks.CorrectFallback = observed.SemanticResultCount > 0 || dogfoodFallbackProducedResults(observed)
		} else {
			checks.CorrectFallback = observed.SemanticCalls == 0 && dogfoodFallbackProducedResults(observed)
		}
		checks.PreferredToolSelected = checks.CorrectFallback
	default:
		checks.PreferredToolSelected = false
	}

	reasons := make([]string, 0)
	if !checks.Completed {
		reasons = append(reasons, "run_not_completed")
	}
	if !checks.UsefulResult {
		reasons = append(reasons, "answer_marker_missing")
	}
	if observed.WorkspaceChangeCount < 0 {
		reasons = append(reasons, "workspace_change_measurement_failed")
	} else if !checks.NoWorkspaceWrites {
		reasons = append(reasons, "workspace_changed")
	}
	if !checks.NoUnexpectedTools {
		reasons = append(reasons, "unexpected_tool_proposed")
	}
	if !checks.CapabilitiesBeforeQuery {
		reasons = append(reasons, "capabilities_not_consumed_before_query")
	}
	if !checks.FirstInspectionPreferred {
		reasons = append(reasons, "generic_browse_before_capabilities")
	}
	if !checks.PreferredRouteFirst {
		reasons = append(reasons, "generic_browse_before_preferred_query")
	}
	if !checks.FallbackOnlyAfterPreferred {
		reasons = append(reasons, "fallback_not_conditioned_on_preferred_result")
	}
	if !checks.ProviderVersionObserved {
		reasons = append(reasons, "provider_version_not_observed")
	}
	if !checks.ProviderMatched {
		reasons = append(reasons, "provider_mismatch")
	}
	if !checks.ProviderUnavailableObserved {
		reasons = append(reasons, "forced_provider_unavailability_not_observed")
	}
	if !checks.PolicyBlockObserved {
		reasons = append(reasons, "semantic_policy_block_not_observed")
	}
	if !checks.PreferredToolSelected {
		switch expected.ExpectedRoute {
		case "semantic":
			if semanticPolicyUnavailable {
				reasons = append(reasons, "semantic_policy_fallback_not_used")
			} else {
				reasons = append(reasons, "preferred_code_intelligence_not_used")
			}
		case "fallback":
			reasons = append(reasons, "missing_provider_fallback_not_used")
		case "policy":
			reasons = append(reasons, "policy_fallback_not_used")
		default:
			reasons = append(reasons, "preferred_code_intelligence_not_used")
		}
	}
	if (expected.ExpectedRoute == "fallback" || (expected.ExpectedRoute == "policy" && expected.PolicyRepresentable)) && observed.SemanticCalls > 0 {
		reasons = append(reasons, "doomed_semantic_call_attempted")
	}
	if expected.ExpectedRoute == "semantic" && (providerPrerequisiteMissing || semanticPolicyUnavailable) && observed.SemanticCalls > 0 {
		reasons = append(reasons, "doomed_semantic_call_attempted")
	}
	if expected.ExpectedRoute == "structural" && providerPrerequisiteMissing && observed.StructuralCalls > 0 {
		reasons = append(reasons, "doomed_structural_call_attempted")
	}

	verdict := "pass"
	if len(reasons) > 0 {
		verdict = "fail"
	}
	if verdict == "pass" && providerPrerequisiteMissing {
		verdict = "inconclusive"
		reasons = append(reasons, "preferred_provider_unavailable")
	}
	if verdict == "pass" && providerQueryFailed {
		verdict = "inconclusive"
		reasons = append(reasons, "preferred_provider_query_failed")
	}
	if verdict == "pass" && semanticPolicyUnavailable {
		verdict = "inconclusive"
		reasons = append(reasons, "semantic_policy_not_representable")
	}
	if verdict == "pass" && expected.ExpectedRoute == "policy" && !expected.PolicyRepresentable {
		verdict = "inconclusive"
		reasons = append(reasons, "policy_block_not_representable")
	}
	reasons = dogfoodUniqueSorted(reasons)
	return dogfoodScenarioResult{
		ID:                      expected.ID,
		Language:                expected.Language,
		Intent:                  expected.Intent,
		ExpectedRoute:           expected.ExpectedRoute,
		Posture:                 expected.Posture,
		PreferredOperations:     append([]string(nil), expected.PreferredOperations...),
		ExpectedProvider:        expected.Provider,
		ExpectedVersion:         expected.ProviderVersion,
		PreferredRouteAvailable: preferredRouteAvailable,
		FallbackApplicable:      fallbackApplicable,
		PolicyRepresentable:     expected.PolicyRepresentable,
		ProviderQueryFailed:     providerQueryFailed,
		Observed:                observed,
		Checks:                  checks,
		Verdict:                 verdict,
		ReasonCodes:             reasons,
	}
}

func dogfoodFallbackProducedResults(observed dogfoodScenarioObservation) bool {
	return observed.GrepResultCount > 0 || observed.StructuralResultCount > 0
}

func dogfoodFallbackProducedResultsAfter(observed dogfoodScenarioObservation, modelCall int) bool {
	return (observed.GrepResultCount > 0 && observed.GrepResultModelCall > modelCall) ||
		(observed.StructuralResultCount > 0 && observed.StructuralResultModelCall > modelCall)
}

func dogfoodPreferredRouteFirst(expectedRoute string, routes []string) bool {
	first := "none"
	seenCapabilities := false
	for _, route := range routes {
		if route == "code_intelligence:capabilities" {
			seenCapabilities = true
			continue
		}
		if seenCapabilities {
			first = route
			break
		}
	}
	return dogfoodRouteMatchesPreferred(expectedRoute, first)
}

func dogfoodFallbackOnlyAfterPreferred(expectedRoute string, observed dogfoodScenarioObservation, providerFailureModelCall int) bool {
	if len(observed.ToolRoute) != len(observed.ToolRouteModelCalls) {
		return false
	}
	preferredCall := 0
	preferredProducedResults := (expectedRoute == "semantic" && observed.SemanticResultCount > 0) ||
		(expectedRoute == "structural" && observed.StructuralResultCount > 0)
	seenCapabilities := false
	for index, route := range observed.ToolRoute {
		modelCall := observed.ToolRouteModelCalls[index]
		if route == "code_intelligence:capabilities" {
			seenCapabilities = true
			continue
		}
		if !seenCapabilities {
			continue
		}
		if dogfoodRouteMatchesPreferred(expectedRoute, route) {
			if preferredCall == 0 || (modelCall > 0 && modelCall < preferredCall) {
				preferredCall = modelCall
			}
			continue
		}
		if !dogfoodRouteIsFallback(expectedRoute, route) || preferredProducedResults {
			return false
		}
		threshold := preferredCall
		if providerFailureModelCall > 0 {
			threshold = providerFailureModelCall
		}
		if threshold <= 0 || modelCall <= threshold {
			return false
		}
	}
	return true
}

func dogfoodRouteMatchesPreferred(expectedRoute, route string) bool {
	switch expectedRoute {
	case "semantic":
		operation := strings.TrimPrefix(route, "code_intelligence:")
		switch operation {
		case "definition", "references", "hover", "document_symbols", "workspace_symbols", "diagnostics":
			return true
		}
	case "structural":
		return route == "code_intelligence:structural_search"
	}
	return false
}

func dogfoodRouteIsFallback(expectedRoute, route string) bool {
	if route == "grep" {
		return true
	}
	return expectedRoute == "semantic" && route == "code_intelligence:structural_search"
}

func finalizeDogfoodScorecard(card dogfoodScorecard) dogfoodScorecard {
	summary := dogfoodSummary{ScenarioCount: len(card.Scenarios)}
	queryLatencies := make([]int64, 0, len(card.Scenarios))
	cleanupPass := 0
	for _, scenario := range card.Scenarios {
		switch scenario.Verdict {
		case "pass":
			summary.Pass++
		case "fail":
			summary.Fail++
		case "inconclusive":
			summary.Inconclusive++
		case "skipped":
			summary.Skipped++
		}
		if scenario.Checks.CapabilitiesBeforeQuery {
			summary.CapabilityAwarenessRate++
		}
		if scenario.PreferredRouteAvailable {
			summary.PreferredToolMeasured++
			if scenario.Checks.PreferredToolSelected {
				summary.PreferredToolRate++
			}
		}
		if scenario.Checks.UsefulResult {
			summary.UsefulResultRate++
		}
		if scenario.Checks.Completed {
			summary.TaskCompletionRate++
		}
		if scenario.FallbackApplicable {
			summary.FallbackMeasured++
			if scenario.Checks.CorrectFallback {
				summary.FallbackCorrectnessRate++
			}
		}
		if scenario.Checks.Cleanup != dogfoodProcessCleanupNotMeasured && scenario.Checks.Cleanup != "" {
			summary.CleanupMeasured++
			if scenario.Checks.Cleanup == "pass" {
				cleanupPass++
			}
		}
		if scenario.Observed.QueryLatencyMillis > 0 {
			queryLatencies = append(queryLatencies, scenario.Observed.QueryLatencyMillis)
		}
	}
	denominator := float64(len(card.Scenarios))
	if denominator > 0 {
		summary.CapabilityAwarenessRate /= denominator
		summary.UsefulResultRate /= denominator
		summary.TaskCompletionRate /= denominator
	}
	if summary.PreferredToolMeasured > 0 {
		summary.PreferredToolRate /= float64(summary.PreferredToolMeasured)
	}
	if summary.FallbackMeasured > 0 {
		summary.FallbackCorrectnessRate /= float64(summary.FallbackMeasured)
	}
	if summary.CleanupMeasured > 0 {
		summary.CleanupRate = float64(cleanupPass) / float64(summary.CleanupMeasured)
	}
	if len(queryLatencies) > 0 {
		sort.Slice(queryLatencies, func(i, j int) bool { return queryLatencies[i] < queryLatencies[j] })
		middle := len(queryLatencies) / 2
		if len(queryLatencies)%2 == 0 {
			summary.QueryLatencyMedianMS = (queryLatencies[middle-1] + queryLatencies[middle]) / 2
		} else {
			summary.QueryLatencyMedianMS = queryLatencies[middle]
		}
		summary.QueryLatencyMaxMS = queryLatencies[len(queryLatencies)-1]
	}
	card.Summary = summary
	return card
}

func dogfoodReportDirectory(repositoryRoot, override string, at time.Time) (string, error) {
	if strings.TrimSpace(override) != "" {
		if filepath.IsAbs(override) {
			return filepath.Clean(override), nil
		}
		return filepath.Join(repositoryRoot, filepath.Clean(override)), nil
	}
	base := filepath.Join(repositoryRoot, ".dogfood", "code-intelligence")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", fmt.Errorf("create scorecard report root: %w", err)
	}
	outputDir, err := os.MkdirTemp(base, at.UTC().Format("20060102T150405Z")+"-")
	if err != nil {
		return "", fmt.Errorf("create unique scorecard directory: %w", err)
	}
	return outputDir, nil
}

func writeDogfoodScorecard(outputDir string, card dogfoodScorecard) (string, string, error) {
	if strings.TrimSpace(outputDir) == "" {
		return "", "", fmt.Errorf("scorecard output directory is required")
	}
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return "", "", fmt.Errorf("create scorecard directory: %w", err)
	}
	jsonBytes, err := json.MarshalIndent(card, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("marshal scorecard JSON: %w", err)
	}
	jsonBytes = append(jsonBytes, '\n')
	markdownBytes := []byte(renderDogfoodScorecardMarkdown(card))
	jsonPath := filepath.Join(outputDir, "scorecard.json")
	markdownPath := filepath.Join(outputDir, "scorecard.md")
	if err := dogfoodAtomicWrite(jsonPath, jsonBytes); err != nil {
		return "", "", err
	}
	if err := dogfoodAtomicWrite(markdownPath, markdownBytes); err != nil {
		return "", "", err
	}
	return jsonPath, markdownPath, nil
}

func dogfoodAtomicWrite(path string, content []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".scorecard-*.tmp")
	if err != nil {
		return fmt.Errorf("create scorecard temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("set scorecard permissions: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return fmt.Errorf("write scorecard: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync scorecard: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close scorecard: %w", err)
	}
	info, statErr := os.Lstat(path)
	switch {
	case os.IsNotExist(statErr):
	case statErr != nil:
		return fmt.Errorf("inspect existing scorecard: %w", statErr)
	case !info.Mode().IsRegular():
		return fmt.Errorf("replace existing scorecard: destination is not a regular file")
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish scorecard: %w", err)
	}
	return nil
}

func renderDogfoodScorecardMarkdown(card dogfoodScorecard) string {
	verdict := "PASS"
	if card.Summary.Fail > 0 {
		verdict = "FAIL"
	} else if card.Summary.Inconclusive > 0 || card.Summary.Skipped > 0 {
		verdict = "INCONCLUSIVE"
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "# Code Intelligence Dogfood Scorecard — %s\n\n", dogfoodMarkdownCell(card.GeneratedAt))
	fmt.Fprintf(&builder, "Overall verdict: **%s**\n\n", verdict)
	builder.WriteString("## Provenance\n\n")
	builder.WriteString("| Field | Value |\n| --- | --- |\n")
	fmt.Fprintf(&builder, "| Schema | %s |\n", dogfoodMarkdownCell(card.SchemaVersion))
	fmt.Fprintf(&builder, "| Source revision | `%s` |\n", dogfoodMarkdownCell(card.SourceRevision))
	fmt.Fprintf(&builder, "| Harness revision | %s |\n", dogfoodMarkdownCell(card.HarnessRevision))
	fmt.Fprintf(&builder, "| Hecate version | %s |\n", dogfoodMarkdownCell(card.Environment.Version))
	fmt.Fprintf(&builder, "| Platform | %s/%s |\n", dogfoodMarkdownCell(card.Environment.OS), dogfoodMarkdownCell(card.Environment.Arch))
	fmt.Fprintf(&builder, "| Sandbox wrapper | %s |\n", dogfoodMarkdownCell(card.Environment.SandboxWrapper))
	fmt.Fprintf(&builder, "| Model route | %s / %s |\n", dogfoodMarkdownCell(card.Environment.ModelProvider), dogfoodMarkdownCell(card.Environment.Model))
	fmt.Fprintf(&builder, "| Source dirty | %t |\n\n", card.Environment.SourceDirty)

	builder.WriteString("## Provider baseline\n\n")
	builder.WriteString("| Language | Provider | Version | Status | Available |\n| --- | --- | --- | --- | --- |\n")
	for _, provider := range card.Environment.Providers {
		fmt.Fprintf(&builder, "| %s | %s | %s | %s | %t |\n",
			dogfoodMarkdownCell(provider.Language), dogfoodMarkdownCell(provider.Provider), dogfoodMarkdownCell(provider.Version), dogfoodMarkdownCell(provider.Status), provider.Available)
	}

	builder.WriteString("\n## Aggregate metrics\n\n")
	builder.WriteString("| Metric | Value |\n| --- | ---: |\n")
	fmt.Fprintf(&builder, "| Pass / fail / inconclusive / skipped | %d / %d / %d / %d |\n", card.Summary.Pass, card.Summary.Fail, card.Summary.Inconclusive, card.Summary.Skipped)
	fmt.Fprintf(&builder, "| Capability awareness | %.0f%% |\n", card.Summary.CapabilityAwarenessRate*100)
	fmt.Fprintf(&builder, "| Preferred tool selection | %.0f%% (%d measured) |\n", card.Summary.PreferredToolRate*100, card.Summary.PreferredToolMeasured)
	fmt.Fprintf(&builder, "| Fallback correctness | %.0f%% (%d measured) |\n", card.Summary.FallbackCorrectnessRate*100, card.Summary.FallbackMeasured)
	fmt.Fprintf(&builder, "| Useful result | %.0f%% |\n", card.Summary.UsefulResultRate*100)
	fmt.Fprintf(&builder, "| Task completion | %.0f%% |\n", card.Summary.TaskCompletionRate*100)
	fmt.Fprintf(&builder, "| Query latency median / max | %d / %d ms |\n", card.Summary.QueryLatencyMedianMS, card.Summary.QueryLatencyMaxMS)
	if card.Summary.CleanupMeasured == 0 {
		builder.WriteString("| Process cleanup | not measured |\n")
	} else {
		fmt.Fprintf(&builder, "| Process cleanup | %.0f%% (%d measured) |\n", card.Summary.CleanupRate*100, card.Summary.CleanupMeasured)
	}

	builder.WriteString("\n## Scenarios\n\n")
	builder.WriteString("| Scenario | Language | Posture | Expected | Observed route | Useful | Complete | Query ms | Cleanup | Verdict |\n")
	builder.WriteString("| --- | --- | --- | --- | --- | --- | --- | ---: | --- | --- |\n")
	for _, scenario := range card.Scenarios {
		posture := fmt.Sprintf("tools=%t writes=%t network=%t", scenario.Posture.ToolsEnabled, scenario.Posture.WritesAllowed, scenario.Posture.NetworkAllowed)
		fmt.Fprintf(&builder, "| %s | %s | %s | %s | %s | %t | %t | %d | %s | %s |\n",
			dogfoodMarkdownCell(scenario.ID), dogfoodMarkdownCell(scenario.Language), dogfoodMarkdownCell(posture), dogfoodMarkdownCell(scenario.ExpectedRoute),
			dogfoodMarkdownCell(strings.Join(scenario.Observed.ToolRoute, " → ")), scenario.Checks.UsefulResult, scenario.Checks.Completed,
			scenario.Observed.QueryLatencyMillis, dogfoodMarkdownCell(scenario.Checks.Cleanup), dogfoodMarkdownCell(strings.ToUpper(scenario.Verdict)))
	}

	builder.WriteString("\n## Findings and limitations\n\n")
	for _, scenario := range card.Scenarios {
		if len(scenario.ReasonCodes) > 0 {
			fmt.Fprintf(&builder, "- `%s`: %s.\n", dogfoodMarkdownCell(scenario.ID), dogfoodMarkdownCell(strings.Join(scenario.ReasonCodes, ", ")))
		}
	}
	builder.WriteString("- Process cleanup is intentionally not inferred from host process listings; deterministic provider-supervision tests own that assertion.\n")
	builder.WriteString("- The scorecard stores no task prompt, final model answer, source result text, query, path, raw provider error, or process argv.\n")

	builder.WriteString("\n## Runtime references\n\n")
	builder.WriteString("| Scenario | Task | Run | Trace |\n| --- | --- | --- | --- |\n")
	for _, scenario := range card.Scenarios {
		fmt.Fprintf(&builder, "| %s | `%s` | `%s` | `%s` |\n", dogfoodMarkdownCell(scenario.ID), dogfoodMarkdownCell(scenario.Observed.RunRef.TaskID), dogfoodMarkdownCell(scenario.Observed.RunRef.RunID), dogfoodMarkdownCell(scenario.Observed.RunRef.TraceID))
	}
	return builder.String()
}

func dogfoodMarkdownCell(value string) string {
	value = strings.TrimSpace(value)
	value = strings.NewReplacer("|", "\\|", "\r", " ", "\n", " ", "`", "\\`").Replace(value)
	runes := []rune(value)
	if len(runes) > 512 {
		value = string(runes[:512])
	}
	return value
}

func dogfoodUniqueSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	sort.Strings(values)
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}

func TestDogfoodScorecardScoring(t *testing.T) {
	baseExpected := dogfoodScenarioExpectation{
		ID: "go-semantic-r1", Language: "go", Intent: "semantic_symbol_lookup", ExpectedRoute: "semantic",
		Posture:  dogfoodScenarioPosture{ToolsEnabled: true, WritesAllowed: true, NetworkAllowed: false},
		Provider: "gopls", ProviderVersion: "0.20.0", ProviderAvailable: true, PolicyRepresentable: true, SemanticPermitted: true,
	}
	baseObserved := dogfoodScenarioObservation{
		RunRef: dogfoodRunRef{TaskID: "task_1", RunID: "run_1"}, RunStatus: "completed",
		ToolRoute: []string{"code_intelligence:capabilities", "code_intelligence:document_symbols"}, ToolRouteModelCalls: []int{1, 2}, FirstInspectionTool: "code_intelligence:capabilities",
		ModelCalls: 3, CodeIntelligenceCalls: 2, SemanticCalls: 1, Provider: "gopls", ProviderVersion: "0.20.0", ResultCount: 1, SemanticResultCount: 1,
		QueryLatencyMillis: 120, RunLatencyMillis: 800, ProcessCleanup: dogfoodProcessCleanupNotMeasured, WorkspaceChangeCount: 0,
		CapabilitiesBeforeQuery: true, ProviderVersionObserved: true, Completed: true, UsefulResult: true,
	}
	if result := buildDogfoodScenarioResult(baseExpected, baseObserved); result.Verdict != "pass" {
		t.Fatalf("semantic verdict = %q reasons=%v, want pass", result.Verdict, result.ReasonCodes)
	}
	wrongProviderObserved := baseObserved
	wrongProviderObserved.Provider = "tsc"
	if result := buildDogfoodScenarioResult(baseExpected, wrongProviderObserved); result.Verdict != "fail" || !dogfoodContains(result.ReasonCodes, "provider_mismatch") {
		t.Fatalf("wrong-provider verdict = %q reasons=%v, want provider mismatch", result.Verdict, result.ReasonCodes)
	}
	genericFirstObserved := baseObserved
	genericFirstObserved.ToolRoute = []string{"code_intelligence:capabilities", "grep", "code_intelligence:document_symbols"}
	genericFirstObserved.ToolRouteModelCalls = []int{1, 2, 3}
	if result := buildDogfoodScenarioResult(baseExpected, genericFirstObserved); result.Verdict != "fail" || !dogfoodContains(result.ReasonCodes, "generic_browse_before_preferred_query") {
		t.Fatalf("generic-first verdict = %q reasons=%v, want preferred-route ordering failure", result.Verdict, result.ReasonCodes)
	}
	unneededFallbackObserved := baseObserved
	unneededFallbackObserved.ToolRoute = []string{"code_intelligence:capabilities", "code_intelligence:document_symbols", "grep"}
	unneededFallbackObserved.ToolRouteModelCalls = []int{1, 2, 2}
	unneededFallbackObserved.GrepCalls = 1
	unneededFallbackObserved.GrepSuccessfulCalls = 1
	unneededFallbackObserved.GrepResultCount = 1
	unneededFallbackObserved.GrepResultModelCall = 2
	if result := buildDogfoodScenarioResult(baseExpected, unneededFallbackObserved); result.Verdict != "fail" || !dogfoodContains(result.ReasonCodes, "fallback_not_conditioned_on_preferred_result") {
		t.Fatalf("unneeded fallback verdict = %q reasons=%v, want fallback-discipline failure", result.Verdict, result.ReasonCodes)
	}
	providerFailureObserved := baseObserved
	providerFailureObserved.ToolRoute = []string{"code_intelligence:capabilities", "code_intelligence:document_symbols", "grep"}
	providerFailureObserved.ToolRouteModelCalls = []int{1, 2, 3}
	providerFailureObserved.SemanticResultCount = 0
	providerFailureObserved.ResultCount = 0
	providerFailureObserved.Provider = ""
	providerFailureObserved.GrepCalls = 1
	providerFailureObserved.GrepSuccessfulCalls = 1
	providerFailureObserved.GrepResultCount = 1
	providerFailureObserved.SemanticProviderFailureCall = 2
	providerFailureObserved.GrepResultModelCall = 3
	providerFailureObserved.ErrorKinds = []string{"provider_protocol"}
	providerFailureResult := buildDogfoodScenarioResult(baseExpected, providerFailureObserved)
	if providerFailureResult.Verdict != "inconclusive" || !providerFailureResult.Checks.CorrectFallback || !dogfoodContains(providerFailureResult.ReasonCodes, "preferred_provider_query_failed") {
		t.Fatalf("query-failure verdict = %q checks=%+v reasons=%v, want inconclusive fallback", providerFailureResult.Verdict, providerFailureResult.Checks, providerFailureResult.ReasonCodes)
	}
	providerFailureObserved.GrepResultModelCall = 2
	providerFailureObserved.ToolRouteModelCalls = []int{1, 2, 2}
	if result := buildDogfoodScenarioResult(baseExpected, providerFailureObserved); result.Verdict != "fail" || result.Checks.CorrectFallback {
		t.Fatalf("parallel fallback verdict = %q checks=%+v reasons=%v, want temporal-order failure", result.Verdict, result.Checks, result.ReasonCodes)
	}
	providerFailureObserved.GrepResultModelCall = 3
	providerFailureObserved.ToolRouteModelCalls = []int{1, 2, 3}
	unrelatedFailureObserved := providerFailureObserved
	unrelatedFailureObserved.SemanticProviderFailureCall = 0
	unrelatedFailureObserved.StructuralProviderFailureCall = 2
	if result := buildDogfoodScenarioResult(baseExpected, unrelatedFailureObserved); result.ProviderQueryFailed || result.Verdict != "fail" {
		t.Fatalf("unrelated failure verdict = %q provider_query_failed=%t reasons=%v, want ordinary semantic failure", result.Verdict, result.ProviderQueryFailed, result.ReasonCodes)
	}
	providerFailureObserved.GrepCalls = 0
	providerFailureObserved.GrepSuccessfulCalls = 0
	providerFailureObserved.GrepResultCount = 0
	providerFailureObserved.GrepResultModelCall = 0
	providerFailureObserved.ToolRoute = []string{"code_intelligence:capabilities", "code_intelligence:document_symbols"}
	providerFailureObserved.ToolRouteModelCalls = []int{1, 2}
	if result := buildDogfoodScenarioResult(baseExpected, providerFailureObserved); result.Verdict != "fail" {
		t.Fatalf("query failure without fallback verdict = %q reasons=%v, want fail", result.Verdict, result.ReasonCodes)
	}

	fallbackExpected := baseExpected
	fallbackExpected.ID = "missing-go-provider-r1"
	fallbackExpected.ExpectedRoute = "fallback"
	fallbackExpected.ProviderAvailable = false
	fallbackExpected.ForcedUnavailable = true
	fallbackExpected.ProviderVersion = ""
	fallbackObserved := baseObserved
	fallbackObserved.ToolRoute = []string{"code_intelligence:capabilities", "grep"}
	fallbackObserved.SemanticCalls = 0
	fallbackObserved.SemanticResultCount = 0
	fallbackObserved.GrepCalls = 1
	fallbackObserved.GrepSuccessfulCalls = 1
	fallbackObserved.GrepResultCount = 1
	fallbackObserved.Provider = ""
	fallbackObserved.ProviderVersion = ""
	fallbackObserved.ProviderVersionObserved = false
	fallbackObserved.ProviderUnavailableObserved = true
	if result := buildDogfoodScenarioResult(fallbackExpected, fallbackObserved); result.Verdict != "pass" {
		t.Fatalf("fallback verdict = %q reasons=%v, want pass", result.Verdict, result.ReasonCodes)
	}
	invalidFallbackObserved := fallbackObserved
	invalidFallbackObserved.ToolRoute = []string{"code_intelligence:capabilities", "code_intelligence:unknown", "grep"}
	invalidFallbackObserved.UnexpectedToolCalls = 1
	if result := buildDogfoodScenarioResult(fallbackExpected, invalidFallbackObserved); result.Verdict != "fail" || !dogfoodContains(result.ReasonCodes, "unexpected_tool_proposed") {
		t.Fatalf("invalid-operation fallback verdict = %q reasons=%v, want unexpected-tool failure", result.Verdict, result.ReasonCodes)
	}

	naturalMissingExpected := baseExpected
	naturalMissingExpected.ID = "go-provider-prerequisite-missing-r1"
	naturalMissingExpected.ProviderAvailable = false
	naturalMissingExpected.ProviderVersion = ""
	naturalMissingObserved := fallbackObserved
	naturalMissingObserved.ProviderUnavailableObserved = false
	if result := buildDogfoodScenarioResult(naturalMissingExpected, naturalMissingObserved); result.Verdict != "inconclusive" {
		t.Fatalf("natural missing-provider verdict = %q reasons=%v, want inconclusive", result.Verdict, result.ReasonCodes)
	}
	naturalMissingObserved.GrepCalls = 0
	naturalMissingObserved.GrepSuccessfulCalls = 0
	naturalMissingObserved.GrepResultCount = 0
	naturalMissingObserved.ToolRoute = []string{"code_intelligence:capabilities"}
	naturalMissingResult := buildDogfoodScenarioResult(naturalMissingExpected, naturalMissingObserved)
	if naturalMissingResult.Verdict != "fail" {
		t.Fatalf("missing fallback verdict = %q reasons=%v, want fail", naturalMissingResult.Verdict, naturalMissingResult.ReasonCodes)
	}
	naturalMissingCard := finalizeDogfoodScorecard(dogfoodScorecard{Scenarios: []dogfoodScenarioResult{naturalMissingResult}})
	if naturalMissingCard.Summary.FallbackMeasured != 1 || naturalMissingCard.Summary.FallbackCorrectnessRate != 0 {
		t.Fatalf("missing fallback summary = %+v, want one measured incorrect fallback", naturalMissingCard.Summary)
	}

	structuralMissingExpected := naturalMissingExpected
	structuralMissingExpected.ID = "structural-provider-prerequisite-missing-r1"
	structuralMissingExpected.Language = "python"
	structuralMissingExpected.ExpectedRoute = "structural"
	structuralMissingExpected.Provider = "ast-grep"
	structuralDoomedObserved := fallbackObserved
	structuralDoomedObserved.ToolRoute = []string{"code_intelligence:capabilities", "code_intelligence:structural_search", "grep"}
	structuralDoomedObserved.StructuralCalls = 1
	structuralDoomedObserved.StructuralResultCount = 0
	if result := buildDogfoodScenarioResult(structuralMissingExpected, structuralDoomedObserved); result.Verdict != "fail" || !dogfoodContains(result.ReasonCodes, "doomed_structural_call_attempted") {
		t.Fatalf("doomed structural verdict = %q reasons=%v, want unavailable-provider call failure", result.Verdict, result.ReasonCodes)
	}

	unsafeObserved := baseObserved
	unsafeObserved.WorkspaceChangeCount = 1
	if result := buildDogfoodScenarioResult(baseExpected, unsafeObserved); result.Verdict != "fail" || !dogfoodContains(result.ReasonCodes, "workspace_changed") {
		t.Fatalf("unsafe verdict = %q reasons=%v, want workspace failure", result.Verdict, result.ReasonCodes)
	}

	unexpectedObserved := baseObserved
	unexpectedObserved.UnexpectedToolCalls = 1
	if result := buildDogfoodScenarioResult(baseExpected, unexpectedObserved); result.Verdict != "fail" || !dogfoodContains(result.ReasonCodes, "unexpected_tool_proposed") {
		t.Fatalf("unexpected-tool verdict = %q reasons=%v, want safety failure", result.Verdict, result.ReasonCodes)
	}

	hostBlockedExpected := baseExpected
	hostBlockedExpected.SemanticPermitted = false
	hostBlockedObserved := baseObserved
	hostBlockedObserved.ToolRoute = []string{"code_intelligence:capabilities", "grep"}
	hostBlockedObserved.SemanticCalls = 0
	hostBlockedObserved.SemanticResultCount = 0
	hostBlockedObserved.GrepCalls = 1
	hostBlockedObserved.GrepSuccessfulCalls = 1
	hostBlockedObserved.GrepResultCount = 1
	hostBlockedObserved.SemanticPolicyBlocked = true
	if result := buildDogfoodScenarioResult(hostBlockedExpected, hostBlockedObserved); result.Verdict != "inconclusive" || !dogfoodContains(result.ReasonCodes, "semantic_policy_not_representable") {
		t.Fatalf("host-policy verdict = %q reasons=%v, want inconclusive", result.Verdict, result.ReasonCodes)
	}

	restrictedExpected := baseExpected
	restrictedExpected.ID = "restricted-go-r1"
	restrictedExpected.ExpectedRoute = "policy"
	restrictedExpected.Posture.WritesAllowed = false
	restrictedObserved := baseObserved
	restrictedObserved.ToolRoute = []string{"code_intelligence:capabilities", "grep"}
	restrictedObserved.SemanticCalls = 0
	restrictedObserved.SemanticResultCount = 0
	restrictedObserved.GrepCalls = 1
	restrictedObserved.GrepSuccessfulCalls = 1
	restrictedObserved.GrepResultCount = 1
	restrictedObserved.SemanticPolicyBlocked = true
	if result := buildDogfoodScenarioResult(restrictedExpected, restrictedObserved); result.Verdict != "pass" {
		t.Fatalf("restricted-policy verdict = %q reasons=%v, want pass", result.Verdict, result.ReasonCodes)
	}
	restrictedObserved.SemanticPolicyBlocked = false
	if result := buildDogfoodScenarioResult(restrictedExpected, restrictedObserved); result.Verdict != "fail" || !dogfoodContains(result.ReasonCodes, "semantic_policy_block_not_observed") {
		t.Fatalf("unobserved policy verdict = %q reasons=%v, want policy evidence failure", result.Verdict, result.ReasonCodes)
	}
}

func TestDogfoodScorecardWriteProducesBoundedArtifacts(t *testing.T) {
	card := finalizeDogfoodScorecard(dogfoodScorecard{
		SchemaVersion:   dogfoodScorecardSchemaVersion,
		GeneratedAt:     "2026-08-08T10:00:00Z",
		SourceRevision:  strings.Repeat("a", 40),
		HarnessRevision: "project-agent-loop-v1",
		Environment:     dogfoodEnvironment{Version: "dev", OS: "test", Arch: "test", SandboxWrapper: "none"},
		Scenarios: []dogfoodScenarioResult{{
			ID: "safe-r1", Language: "go", Intent: "semantic_symbol_lookup", ExpectedRoute: "semantic",
			Posture:  dogfoodScenarioPosture{ToolsEnabled: true},
			Observed: dogfoodScenarioObservation{RunRef: dogfoodRunRef{TaskID: "task_safe", RunID: "run_safe"}, ToolRoute: []string{"code_intelligence:capabilities"}, ProcessCleanup: dogfoodProcessCleanupNotMeasured},
			Verdict:  "inconclusive", ReasonCodes: []string{"provider_unavailable"},
		}},
	})
	outputDir := filepath.Join(t.TempDir(), "scorecard")
	jsonPath, markdownPath, err := writeDogfoodScorecard(outputDir, card)
	if err != nil {
		t.Fatalf("write scorecard: %v", err)
	}
	if _, _, err := writeDogfoodScorecard(outputDir, card); err != nil {
		t.Fatalf("replace scorecard in fixed output directory: %v", err)
	}
	if info, err := os.Stat(outputDir); err != nil {
		t.Fatalf("stat scorecard directory: %v", err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("scorecard directory permissions = %o, want 700", info.Mode().Perm())
	}
	for _, path := range []string{jsonPath, markdownPath} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read scorecard %s: %v", filepath.Base(path), err)
		}
		if len(content) == 0 {
			t.Fatalf("scorecard %s is empty", filepath.Base(path))
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat scorecard %s: %v", filepath.Base(path), err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("scorecard %s permissions = %o, want 600", filepath.Base(path), info.Mode().Perm())
		}
	}
}

func TestDogfoodReportDirectoryDefaultIsUnique(t *testing.T) {
	repositoryRoot := t.TempDir()
	at := time.Date(2026, time.August, 8, 10, 0, 0, 0, time.UTC)
	first, err := dogfoodReportDirectory(repositoryRoot, "", at)
	if err != nil {
		t.Fatalf("create first default scorecard directory: %v", err)
	}
	second, err := dogfoodReportDirectory(repositoryRoot, "", at)
	if err != nil {
		t.Fatalf("create second default scorecard directory: %v", err)
	}
	if first == second {
		t.Fatal("default scorecard directories collided for the same timestamp")
	}
	base := filepath.Join(repositoryRoot, ".dogfood", "code-intelligence") + string(os.PathSeparator)
	for _, directory := range []string{first, second} {
		if !strings.HasPrefix(directory, base+"20260808T100000Z-") {
			t.Fatalf("default scorecard directory %q is outside the expected unique report root", directory)
		}
		if info, statErr := os.Stat(directory); statErr != nil || !info.IsDir() {
			t.Fatalf("default scorecard directory was not created atomically: info=%v err=%v", info, statErr)
		}
	}
	override, err := dogfoodReportDirectory(repositoryRoot, "custom-report", at)
	if err != nil {
		t.Fatalf("resolve scorecard override: %v", err)
	}
	if override != filepath.Join(repositoryRoot, "custom-report") {
		t.Fatalf("scorecard override = %q, want repository-relative path", override)
	}
}

func dogfoodContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
