package telemetry

import (
	"strings"
	"unicode"
)

const (
	MetricLabelOther     = "other"
	metricLabelMaxLength = 96
)

var knownProviderKinds = map[string]struct{}{
	"cloud": {},
	"local": {},
}

var knownProviderHealthStatuses = map[string]struct{}{
	"healthy":   {},
	"degraded":  {},
	"open":      {},
	"half_open": {},
}

var knownRunStatuses = map[string]struct{}{
	"queued":            {},
	"running":           {},
	"awaiting_approval": {},
	"completed":         {},
	"failed":            {},
	"cancelled":         {},
}

var knownExecutionKinds = map[string]struct{}{
	"chat":       {},
	"agent_loop": {},
	"file":       {},
	"git":        {},
	"shell":      {},
}

var knownStepKinds = map[string]struct{}{
	"approval": {},
	"file":     {},
	"git":      {},
	"model":    {},
	"shell":    {},
	"summary":  {},
	"tool":     {},
}

var knownApprovalKinds = map[string]struct{}{
	"agent_loop_tool_call": {},
	"file_write":           {},
	"git_exec":             {},
	"network_egress":       {},
	"shell_command":        {},
}

var knownApprovalDecisions = map[string]struct{}{
	"approved":  {},
	"cancelled": {},
	"rejected":  {},
}

var knownQueueBackends = map[string]struct{}{
	"memory":   {},
	"sqlite":   {},
	"postgres": {},
}

var knownAgentDriverKinds = map[string]struct{}{
	"acp":    {},
	"hecate": {},
}

var knownMCPCallResults = map[string]struct{}{
	MCPCallResultDispatched: {},
	MCPCallResultToolError:  {},
	MCPCallResultFailed:     {},
	MCPCallResultBlocked:    {},
}

var knownMCPCacheEvents = map[string]struct{}{
	MCPCacheEventHit:     {},
	MCPCacheEventMiss:    {},
	MCPCacheEventEvicted: {},
}

// knownAgentAdapterProbeStatuses mirrors agentadapters.ProbeStatus*.
// Duplicated here because the telemetry package can't import
// agentadapters without a cycle; the contract test asserts every
// known status passes through unchanged.
var knownAgentAdapterProbeStatuses = map[string]struct{}{
	"ready":         {},
	"not_installed": {},
	"auth_required": {},
	"error":         {},
}

// knownAgentAdapterTerminalMethods covers the five ACP terminal
// methods. Adapter calls into any other method don't reach
// RecordTerminalRPCUnsupported, so the closed set here is exact.
var knownAgentAdapterTerminalMethods = map[string]struct{}{
	"create":  {},
	"kill":    {},
	"output":  {},
	"release": {},
	"wait":    {},
}

// knownAgentChatCancelReasons covers the three cancellation paths
// the handler/runtime distinguishes. New paths require a label here
// so unknown reasons collapse to "other" instead of polluting
// dashboards.
var knownAgentChatCancelReasons = map[string]struct{}{
	"operator":          {},
	"request_cancelled": {},
	"shutdown":          {},
}

// NormalizeMetricLabel applies a generic safety guard for labels that are
// useful but not closed-set enums, such as provider IDs, model names, and MCP
// server aliases. Closed-set dimensions should use the specific normalizers
// below so unexpected values collapse to "other".
func NormalizeMetricLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) > metricLabelMaxLength {
		return MetricLabelOther
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return MetricLabelOther
		}
	}
	return value
}

func NormalizeProviderKind(value string) string {
	return normalizeKnownLabel(value, knownProviderKinds)
}

func NormalizeProviderHealthStatus(value string) string {
	return normalizeKnownLabel(value, knownProviderHealthStatuses)
}

func NormalizeRunStatus(value string) string {
	return normalizeKnownLabel(value, knownRunStatuses)
}

func NormalizeExecutionKind(value string) string {
	return normalizeKnownLabel(value, knownExecutionKinds)
}

func NormalizeStepKind(value string) string {
	return normalizeKnownLabel(value, knownStepKinds)
}

func NormalizeApprovalKind(value string) string {
	return normalizeKnownLabel(value, knownApprovalKinds)
}

func NormalizeApprovalDecision(value string) string {
	return normalizeKnownLabel(value, knownApprovalDecisions)
}

func NormalizeQueueBackend(value string) string {
	return normalizeKnownLabel(value, knownQueueBackends)
}

func NormalizeAgentDriverKind(value string) string {
	return normalizeKnownLabel(value, knownAgentDriverKinds)
}

func NormalizeMCPCallResult(value string) string {
	return normalizeKnownLabel(value, knownMCPCallResults)
}

func NormalizeMCPCacheEvent(value string) string {
	return normalizeKnownLabel(value, knownMCPCacheEvents)
}

func NormalizeAgentAdapterProbeStatus(value string) string {
	return normalizeKnownLabel(value, knownAgentAdapterProbeStatuses)
}

func NormalizeAgentAdapterTerminalMethod(value string) string {
	return normalizeKnownLabel(value, knownAgentAdapterTerminalMethods)
}

func NormalizeAgentChatCancelReason(value string) string {
	return normalizeKnownLabel(value, knownAgentChatCancelReasons)
}

func normalizeKnownLabel(value string, known map[string]struct{}) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if _, ok := known[value]; ok {
		return value
	}
	return MetricLabelOther
}
