package telemetry

const (
	AttrServiceName                   = "service.name"
	AttrRequestID                     = "request.id"
	AttrTraceID                       = "trace.id"
	AttrSpanID                        = "span.id"
	AttrErrorType                     = "error.type"
	AttrErrorMessage                  = "error.message"
	AttrGenAIProviderName             = "gen_ai.provider.name"
	AttrGenAIRequestModel             = "gen_ai.request.model"
	AttrGenAIResponseModel            = "gen_ai.response.model"
	AttrGenAIUsageInputTokens         = "gen_ai.usage.input_tokens"
	AttrGenAIUsageOutputTokens        = "gen_ai.usage.output_tokens"
	AttrGenAIUsageTotalTokens         = "gen_ai.usage.total_tokens"
	AttrHecatePhase                   = "hecate.phase"
	AttrHecateResult                  = "hecate.result"
	AttrHecateErrorKind               = "hecate.error.kind"
	AttrHecateProviderKind            = "hecate.provider.kind"
	AttrHecateProviderIndex           = "hecate.provider.index"
	AttrHecateProviderLatencyMS       = "hecate.provider.latency_ms"
	AttrHecateProviderHealthStatus    = "hecate.provider.health_status"
	AttrHecateRouteReason             = "hecate.route.reason"
	AttrHecateRouteOutcome            = "hecate.route.outcome"
	AttrHecateRouteSkipReason         = "hecate.route.skip_reason"
	AttrHecateGovernorResult          = "hecate.governor.result"
	AttrHecateGovernorRouteResult     = "hecate.governor.route_result"
	AttrHecatePolicyRuleID            = "hecate.policy.rule_id"
	AttrHecatePolicyAction            = "hecate.policy.action"
	AttrHecatePolicyReason            = "hecate.policy.reason"
	AttrHecateCostTotalMicrosUSD      = "hecate.cost.total_micros_usd"
	AttrHecateCostEstimatedMicrosUSD  = "hecate.cost.estimated_micros_usd"
	AttrHecateCostInputMicrosUSD      = "hecate.cost.input_micros_usd"
	AttrHecateCostOutputMicrosUSD     = "hecate.cost.output_micros_usd"
	AttrHecateCostCachedMicrosUSD     = "hecate.cost.cached_micros_usd"
	AttrHecateModelRequestedCanonical = "hecate.model.requested_canonical"
	AttrHecateModelResolvedCanonical  = "hecate.model.resolved_canonical"
	AttrHecateModelCanonical          = "hecate.model.canonical"
	AttrHecateRequestMessageCount     = "hecate.request.message_count"
	AttrHecateHTTPDurationMS          = "hecate.http.duration_ms"
	AttrHecateTraceRequestID          = "hecate.trace.request_id"
	AttrHecateRetryAttemptCount       = "hecate.retry.attempt_count"
	AttrHecateRetryAttempt            = "hecate.retry.attempt"
	AttrHecateRetryCount              = "hecate.retry.retry_count"
	AttrHecateRetryNextAttempt        = "hecate.retry.next_attempt"
	AttrHecateRetryMaxAttempts        = "hecate.retry.max_attempts"
	AttrHecateRetryBackoffMS          = "hecate.retry.backoff_ms"
	AttrHecateRetryRetryable          = "hecate.retry.retryable"
	AttrHecateFailoverFromProvider    = "hecate.failover.from_provider"
	AttrHecateFailoverFromModel       = "hecate.failover.from_model"
	AttrHecateFailoverToProvider      = "hecate.failover.to_provider"
	AttrHecateFailoverToModel         = "hecate.failover.to_model"
	AttrHecateFailoverActive          = "hecate.failover.active"
	AttrHecateFailoverReason          = "hecate.failover.reason"
	ResultSuccess                     = "success"
	ResultDenied                      = "denied"
	ResultError                       = "error"

	// Orchestrator — task and run identity
	AttrHecateTaskID        = "hecate.task.id"
	AttrHecateRunID         = "hecate.run.id"
	AttrHecateRunNumber     = "hecate.run.number"
	AttrHecateRunStatus     = "hecate.run.status"
	AttrHecateRunDurationMS = "hecate.run.duration_ms"
	AttrHecateExecutionKind = "hecate.execution.kind"

	// Orchestrator — step
	AttrHecateStepID         = "hecate.step.id"
	AttrHecateStepKind       = "hecate.step.kind"
	AttrHecateStepIndex      = "hecate.step.index"
	AttrHecateStepToolName   = "hecate.step.tool_name"
	AttrHecateStepDurationMS = "hecate.step.duration_ms"

	// Orchestrator — artifact
	AttrHecateArtifactID        = "hecate.artifact.id"
	AttrHecateArtifactKind      = "hecate.artifact.kind"
	AttrHecateArtifactSizeBytes = "hecate.artifact.size_bytes"

	// Orchestrator — approval
	AttrHecateApprovalID       = "hecate.approval.id"
	AttrHecateApprovalKind     = "hecate.approval.kind"
	AttrHecateApprovalStatus   = "hecate.approval.status"
	AttrHecateApprovalDecision = "hecate.approval.decision"
	AttrHecateApprovalWaitMS   = "hecate.approval.wait_ms"

	// MCP — external tool call dispatch and cache observability.
	// `result` takes one of the MCPCallResult* constants in
	// contract.go (dispatched / tool_error / failed / blocked); the
	// cache `event` attribute takes MCPCacheEvent* (hit / miss /
	// evicted). `server` is the operator-chosen alias from the task's
	// mcp_servers config; `tool` is the un-namespaced upstream tool
	// name so charts can group across aliases.
	AttrHecateMCPServer     = "hecate.mcp.server"
	AttrHecateMCPTool       = "hecate.mcp.tool"
	AttrHecateMCPCallResult = "hecate.mcp.call.result"
	AttrHecateMCPCacheEvent = "hecate.mcp.cache.event"

	// Queue lifecycle
	AttrHecateQueueBackend = "hecate.queue.backend"
	AttrHecateQueueClaimID = "hecate.queue.claim_id"
	AttrHecateQueueWaitMS  = "hecate.queue.wait_ms"
	AttrHecateWorkerID     = "hecate.worker.id"

	// Task identity (non-run fields emitted on orchestrator.task.* events)
	AttrHecateTaskStatus     = "hecate.task.status"
	AttrHecateTaskRepo       = "hecate.task.repo"
	AttrHecateTaskBaseBranch = "hecate.task.base_branch"

	// Shell execution
	AttrHecateShellCommand = "hecate.shell.command"

	// Retention
	AttrRetentionTrigger   = "retention.trigger"
	AttrRetentionSubsystem = "retention.subsystem"
	AttrRetentionDeleted   = "retention.deleted"
	AttrRetentionResults   = "retention.results"

	// External agent chats
	AttrHecateAgentChatSessionID   = "hecate.agent_chat.session.id"
	AttrHecateAgentChatMessageID   = "hecate.agent_chat.message.id"
	AttrHecateAgentAdapterID       = "hecate.agent_adapter.id"
	AttrHecateAgentAdapterName     = "hecate.agent_adapter.name"
	AttrHecateAgentAdapterCommand  = "hecate.agent_adapter.command"
	AttrHecateAgentDriverKind      = "hecate.agent_adapter.driver.kind"
	AttrHecateAgentNativeSessionID = "hecate.agent_adapter.native_session.id"
	AttrHecateAgentOutputBytes     = "hecate.agent_adapter.output.bytes"
	AttrHecateAgentRawOutputBytes  = "hecate.agent_adapter.raw_output.bytes"
	AttrHecateAgentDiffCaptured    = "hecate.agent_adapter.diff.captured"
	AttrHecateWorkspacePath        = "hecate.workspace.path"
)
