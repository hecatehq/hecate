package telemetry

// Event name constants — the closed set of event names passed to trace.Record.
// Use these constants instead of string literals so that event names are
// typo-safe, statically searchable, and form part of the frozen signal contract.

// Gateway request lifecycle
const (
	EventRequestReceived = "request.received"
	EventRequestInvalid  = "request.invalid"
)

// Governor
const (
	EventGovernorAllowed              = "governor.allowed"
	EventGovernorDenied               = "governor.denied"
	EventGovernorModelRewrite         = "governor.model_rewrite"
	EventGovernorBudgetEstimateFailed = "governor.budget_estimate_failed"
	EventGovernorRouteDenied          = "governor.route_denied"
	EventGovernorRouteAllowed         = "governor.route_allowed"
	EventGovernorUsageRecordFailed    = "governor.usage_record_failed"
)

// Router
const (
	EventRouterFailed              = "router.failed"
	EventRouterSelected            = "router.selected"
	EventRouterCandidateConsidered = "router.candidate.considered"
	EventRouterCandidateSkipped    = "router.candidate.skipped"
	EventRouterCandidateDenied     = "router.candidate.denied"
	EventRouterCandidateSelected   = "router.candidate.selected"
)

// Provider execution
const (
	EventProviderCallStarted        = "provider.call.started"
	EventProviderCallFinished       = "provider.call.finished"
	EventProviderCallFailed         = "provider.call.failed"
	EventProviderRetryScheduled     = "provider.retry.scheduled"
	EventProviderRetryBackoffFailed = "provider.retry.backoff_failed"
	EventProviderFailoverSelected   = "provider.failover.selected"
	EventProviderFailoverSkipped    = "provider.failover.skipped"
	EventProviderHealthDegraded     = "provider.health.degraded"
)

// Response pipeline
const (
	EventUsageNormalized      = "usage.normalized"
	EventCostCalculated       = "cost.calculated"
	EventCostEstimateUnpriced = "cost.estimate_unpriced"
	EventResponseReturned     = "response.returned"
)

// Body capture (opt-in via GATEWAY_TRACE_BODIES)
const (
	EventRequestBodyCaptured  = "request.body.captured"
	EventResponseBodyCaptured = "response.body.captured"
)

// Queue lifecycle — recorded in the runner when jobs move through the queue.
const (
	EventQueueEnqueued          = "queue.enqueued"
	EventQueueClaimed           = "queue.claimed"
	EventQueueAcked             = "queue.acked"
	EventQueueNacked            = "queue.nacked"
	EventQueueLeaseExtended     = "queue.lease_extended"
	EventQueueLeaseExtendFailed = "queue.lease_extend_failed"
)

// Orchestrator
const (
	EventOrchestratorTaskStarted       = "orchestrator.task.started"
	EventOrchestratorTaskFinished      = "orchestrator.task.finished"
	EventOrchestratorRunStarted        = "orchestrator.run.started"
	EventOrchestratorRunFailed         = "orchestrator.run.failed"
	EventOrchestratorRunFinished       = "orchestrator.run.finished"
	EventOrchestratorStepCompleted     = "orchestrator.step.completed"
	EventOrchestratorStepFailed        = "orchestrator.step.failed"
	EventOrchestratorArtifactCreated   = "orchestrator.artifact.created"
	EventOrchestratorArtifactFailed    = "orchestrator.artifact.failed"
	EventOrchestratorApprovalRequested = "orchestrator.approval.requested"
	EventOrchestratorApprovalResolved  = "orchestrator.approval.resolved"
	EventOrchestratorApprovalFailed    = "orchestrator.approval.failed"

	// MCP-tool-call events — emitted by dispatchMCPToolCall on every
	// dispatch outcome using the generic event-protocol taxonomy.
	// MCP details stay in the payload so frontends can render all
	// tool kinds uniformly while operators can still filter by
	// server/tool.
	EventMCPToolCompleted = "tool.completed"
	EventMCPToolFailed    = "tool.failed"
	EventMCPToolBlocked   = "policy.tool_blocked"
)

// MCP-tool-call result values for telemetry attributes / event
// payloads. Distinct from RunStatus / step Result because we need
// finer granularity: a call that returned `is_error=true` from the
// upstream is functionally a tool-level failure but a
// protocol-level success, and operators want to chart those
// separately.
const (
	MCPCallResultDispatched = "dispatched" // upstream returned cleanly, is_error=false
	MCPCallResultToolError  = "tool_error" // upstream returned is_error=true
	MCPCallResultFailed     = "failed"     // protocol/transport error before a result
	MCPCallResultBlocked    = "blocked"    // approval policy short-circuited the call
)

// MCP-cache events for the cache-events counter. Hit/miss are recorded
// at Acquire time; evicted is recorded both for reactive eviction
// (Pool.Call transport-closed error) and TTL/LRU eviction (cache reaper
// or over-cap insert).
const (
	MCPCacheEventHit     = "hit"
	MCPCacheEventMiss    = "miss"
	MCPCacheEventEvicted = "evicted"
)

// Retention
const (
	EventRetentionRunStarted        = "retention.run.started"
	EventRetentionRunFinished       = "retention.run.finished"
	EventRetentionSubsystemFailed   = "retention.subsystem.failed"
	EventRetentionSubsystemFinished = "retention.subsystem.finished"
	EventRetentionHistoryFailed     = "retention.history.failed"
	EventRetentionHistoryPersisted  = "retention.history.persisted"
)

// External agent chats
const (
	EventAgentChatRunStarted    = "agent_chat.run.started"
	EventAgentChatOutputStarted = "agent_chat.output.started"
	EventAgentChatFilesChanged  = "agent_chat.files_changed"
	EventAgentChatRunFinished   = "agent_chat.run.finished"
	EventAgentChatRunFailed     = "agent_chat.run.failed"
	EventAgentChatRunCancelled  = "agent_chat.run.cancelled"
)

var allEventNames = []string{
	EventRequestReceived,
	EventRequestInvalid,
	EventGovernorAllowed,
	EventGovernorDenied,
	EventGovernorModelRewrite,
	EventGovernorBudgetEstimateFailed,
	EventGovernorRouteDenied,
	EventGovernorRouteAllowed,
	EventGovernorUsageRecordFailed,
	EventRouterFailed,
	EventRouterSelected,
	EventRouterCandidateConsidered,
	EventRouterCandidateSkipped,
	EventRouterCandidateDenied,
	EventRouterCandidateSelected,
	EventProviderCallStarted,
	EventProviderCallFinished,
	EventProviderCallFailed,
	EventProviderRetryScheduled,
	EventProviderRetryBackoffFailed,
	EventProviderFailoverSelected,
	EventProviderFailoverSkipped,
	EventProviderHealthDegraded,
	EventUsageNormalized,
	EventCostCalculated,
	EventCostEstimateUnpriced,
	EventResponseReturned,
	EventRequestBodyCaptured,
	EventResponseBodyCaptured,
	EventQueueEnqueued,
	EventQueueClaimed,
	EventQueueAcked,
	EventQueueNacked,
	EventQueueLeaseExtended,
	EventQueueLeaseExtendFailed,
	EventOrchestratorTaskStarted,
	EventOrchestratorTaskFinished,
	EventOrchestratorRunStarted,
	EventOrchestratorRunFailed,
	EventOrchestratorRunFinished,
	EventOrchestratorStepCompleted,
	EventOrchestratorStepFailed,
	EventOrchestratorArtifactCreated,
	EventOrchestratorArtifactFailed,
	EventOrchestratorApprovalRequested,
	EventOrchestratorApprovalResolved,
	EventOrchestratorApprovalFailed,
	EventMCPToolCompleted,
	EventMCPToolFailed,
	EventMCPToolBlocked,
	EventRetentionRunStarted,
	EventRetentionRunFinished,
	EventRetentionSubsystemFailed,
	EventRetentionSubsystemFinished,
	EventRetentionHistoryFailed,
	EventRetentionHistoryPersisted,
	EventAgentChatRunStarted,
	EventAgentChatOutputStarted,
	EventAgentChatFilesChanged,
	EventAgentChatRunFinished,
	EventAgentChatRunFailed,
	EventAgentChatRunCancelled,
}

func AllEventNames() []string {
	out := make([]string, len(allEventNames))
	copy(out, allEventNames)
	return out
}

// ---------------------------------------------------------------------------
// Span name constants — the parent spans that events are grouped into.
// These match the mapping in profiler.spanSpecForEvent.
// ---------------------------------------------------------------------------

const (
	SpanGatewayRequest      = "gateway.request"
	SpanGatewayRequestParse = "gateway.request.parse"
	SpanGatewayGovernor     = "gateway.governor"
	SpanGatewayRouter       = "gateway.router"
	SpanGatewayProvider     = "gateway.provider"
	SpanGatewayCache        = "gateway.cache"
	SpanGatewayUsage        = "gateway.usage"
	SpanGatewayCost         = "gateway.cost"
	SpanGatewayResponse     = "gateway.response"
	SpanGatewayRuntime      = "gateway.runtime"

	SpanOrchestratorTask     = "orchestrator.task"
	SpanOrchestratorRun      = "orchestrator.run"
	SpanOrchestratorStep     = "orchestrator.step"
	SpanOrchestratorArtifact = "orchestrator.artifact"
	SpanOrchestratorApproval = "orchestrator.approval"
	SpanOrchestratorQueue    = "orchestrator.queue"

	SpanRetentionRun = "retention.run"

	SpanAgentChatRun = "agent_chat.run"
)

// ---------------------------------------------------------------------------
// Metric name constants — the authoritative instrument names.
// The instrument definitions in metrics.go MUST match these exactly.
// Tests in contract_test.go enforce this.
// ---------------------------------------------------------------------------

const (
	MetricGatewayRequests        = "hecate.gateway.requests"
	MetricGatewayRequestDuration = "hecate.gateway.request.duration"
	MetricChatRequestsTotal      = "gen_ai.gateway.chat.requests"
	MetricCostMicrosTotal        = "gen_ai.gateway.cost"
	MetricInputTokensTotal       = "gen_ai.client.tokens.input"
	MetricOutputTokensTotal      = "gen_ai.client.tokens.output"
	MetricTotalTokensTotal       = "gen_ai.client.tokens.total"
	MetricRetriesTotal           = "hecate.gateway.retries"
	MetricFailoversTotal         = "hecate.gateway.failovers"

	// External agent chat metrics
	MetricAgentChatRunsTotal   = "hecate.agent_chat.runs"
	MetricAgentChatRunDuration = "hecate.agent_chat.run.duration"

	// Orchestrator metrics
	MetricOrchestratorRunsTotal            = "hecate.orchestrator.runs"
	MetricOrchestratorRunDuration          = "hecate.orchestrator.run.duration"
	MetricOrchestratorQueueWaitDuration    = "hecate.orchestrator.queue.wait_duration"
	MetricOrchestratorStepsTotal           = "hecate.orchestrator.steps"
	MetricOrchestratorStepDuration         = "hecate.orchestrator.step.duration"
	MetricOrchestratorApprovalsTotal       = "hecate.orchestrator.approvals"
	MetricOrchestratorApprovalWaitDuration = "hecate.orchestrator.approval.wait_duration"
	MetricOrchestratorLeaseExtendFailures  = "hecate.orchestrator.queue.lease_extend_failures"

	// MCP-client metrics — track the volume and latency of tool calls
	// dispatched to external MCP servers, plus the cache's
	// hit/miss/evict counts. Operators use these to answer "is the
	// github MCP server slow today?" and "is the cache doing useful
	// work?". The result attribute on calls splits failures from
	// blocked-by-policy from successful tool errors so the same
	// histogram is meaningful across all four outcomes.
	MetricOrchestratorMCPToolCallsTotal   = "hecate.orchestrator.mcp.tool_calls"
	MetricOrchestratorMCPToolCallDuration = "hecate.orchestrator.mcp.tool_call.duration"
	MetricOrchestratorMCPCacheEventsTotal = "hecate.orchestrator.mcp.cache_events"
)

// ---------------------------------------------------------------------------
// Error kind constants — the closed set of allowed hecate.error.kind values.
// All callers should use NormalizeErrorKind before recording this attribute.
// ---------------------------------------------------------------------------

const (
	ErrorKindInvalidRequest     = "invalid_request"
	ErrorKindRequestDenied      = "request_denied"
	ErrorKindRouterFailed       = "router_failed"
	ErrorKindBudgetEstimate     = "budget_estimate_failed"
	ErrorKindRouteDenied        = "route_denied"
	ErrorKindProviderCallFailed = "provider_call_failed"
	ErrorKindRetryBackoff       = "retry_backoff_failed"
	ErrorKindProviderHealth     = "provider_health_degraded"
	ErrorKindUsageRecord        = "usage_record_failed"
	// ErrorKindOther is the fallback for any value not in the known set.
	ErrorKindOther = "other"
)

var knownErrorKinds = map[string]struct{}{
	ErrorKindInvalidRequest:     {},
	ErrorKindRequestDenied:      {},
	ErrorKindRouterFailed:       {},
	ErrorKindBudgetEstimate:     {},
	ErrorKindRouteDenied:        {},
	ErrorKindProviderCallFailed: {},
	ErrorKindRetryBackoff:       {},
	ErrorKindProviderHealth:     {},
	ErrorKindUsageRecord:        {},
	ErrorKindOther:              {},
}

var knownResults = map[string]struct{}{
	ResultSuccess: {},
	ResultDenied:  {},
	ResultError:   {},
}

// NormalizeErrorKind returns kind unchanged if it belongs to the contract's
// closed error-kind set, otherwise returns ErrorKindOther. Always pass
// hecate.error.kind values through this function before recording them as
// span attributes or metric labels to prevent high-cardinality explosions.
func NormalizeErrorKind(kind string) string {
	if _, ok := knownErrorKinds[kind]; ok {
		return kind
	}
	return ErrorKindOther
}

// NormalizeResult returns result unchanged when it is one of the three defined
// values (ResultSuccess, ResultDenied, ResultError). Any other value is mapped
// to ResultError.
func NormalizeResult(result string) string {
	if _, ok := knownResults[result]; ok {
		return result
	}
	return ResultError
}

// ---------------------------------------------------------------------------
// Required attribute schema — the minimum set of attributes each event MUST
// carry. Validated by tests; use ValidateEventAttrs in test helpers.
// ---------------------------------------------------------------------------

// requiredEventAttrs maps event name → the attribute keys that must be present
// in attrs when that event is recorded. Events not listed here have no
// contract-enforced required attributes (but may still carry useful attrs).
var requiredEventAttrs = map[string][]string{
	EventRequestReceived: {
		AttrHecateRequestMessageCount,
		AttrGenAIRequestModel,
	},
	EventGovernorDenied: {
		AttrHecateGovernorResult,
		AttrHecateErrorKind,
	},
	EventGovernorAllowed: {
		AttrHecateGovernorResult,
	},
	EventRouterSelected: {
		AttrGenAIProviderName,
		AttrGenAIRequestModel,
		AttrHecateRouteReason,
	},
	EventGovernorRouteDenied: {
		AttrGenAIProviderName,
		AttrHecateErrorKind,
	},
	EventGovernorRouteAllowed: {
		AttrGenAIProviderName,
		AttrHecateCostEstimatedMicrosUSD,
	},
	EventProviderCallStarted: {
		AttrGenAIProviderName,
		AttrGenAIRequestModel,
		AttrHecateRetryAttempt,
	},
	EventProviderCallFinished: {
		AttrGenAIProviderName,
		AttrGenAIRequestModel,
		AttrHecateProviderLatencyMS,
	},
	EventProviderCallFailed: {
		AttrGenAIProviderName,
		AttrGenAIRequestModel,
		AttrHecateErrorKind,
	},
	EventUsageNormalized: {
		AttrGenAIUsageInputTokens,
		AttrGenAIUsageOutputTokens,
		AttrGenAIUsageTotalTokens,
	},
	EventCostCalculated: {
		AttrHecateCostTotalMicrosUSD,
	},
	EventResponseReturned: {
		AttrGenAIProviderName,
		AttrGenAIResponseModel,
		AttrGenAIRequestModel,
	},
	EventAgentChatRunStarted: {
		AttrHecateAgentChatSessionID,
		AttrHecateRunID,
		AttrHecateAgentAdapterID,
	},
	EventAgentChatOutputStarted: {
		AttrHecateAgentChatSessionID,
		AttrHecateRunID,
		AttrHecateAgentAdapterID,
		AttrHecateAgentOutputBytes,
	},
	EventAgentChatFilesChanged: {
		AttrHecateAgentChatSessionID,
		AttrHecateRunID,
		AttrHecateAgentAdapterID,
		AttrHecateAgentDiffCaptured,
	},
	EventAgentChatRunFinished: {
		AttrHecateAgentChatSessionID,
		AttrHecateRunID,
		AttrHecateAgentAdapterID,
		AttrHecateRunDurationMS,
	},
	EventAgentChatRunFailed: {
		AttrHecateAgentChatSessionID,
		AttrHecateRunID,
		AttrHecateAgentAdapterID,
		AttrHecateRunDurationMS,
		AttrHecateErrorKind,
		AttrErrorType,
	},
	EventAgentChatRunCancelled: {
		AttrHecateAgentChatSessionID,
		AttrHecateRunID,
		AttrHecateAgentAdapterID,
		AttrHecateRunDurationMS,
	},
}

// RequiredAttrsForEvent returns the required attribute keys for the given event
// name, or nil for event names not listed in the schema (unconstrained events).
// Use this in test helpers that verify trace output completeness.
func RequiredAttrsForEvent(eventName string) []string {
	required := requiredEventAttrs[eventName]
	if len(required) == 0 {
		return nil
	}
	out := make([]string, len(required))
	copy(out, required)
	return out
}

// ValidateEventAttrs returns the attribute keys required for eventName that are
// absent from attrs. An empty (or nil) return means the event passes the
// contract. Unknown event names always pass (nil return).
func ValidateEventAttrs(eventName string, attrs map[string]any) []string {
	required, ok := requiredEventAttrs[eventName]
	if !ok {
		return nil
	}
	var missing []string
	for _, k := range required {
		if _, present := attrs[k]; !present {
			missing = append(missing, k)
		}
	}
	return missing
}
