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

	// Sandbox / tool execution
	AttrHecateSandboxWrapperKind      = "hecate.sandbox.wrapper.kind"
	AttrHecateSandboxRTKEnabled       = "hecate.sandbox.rtk.enabled"
	AttrHecateSandboxRTKCommandBefore = "hecate.sandbox.rtk.command.before"
	AttrHecateSandboxRTKCommandAfter  = "hecate.sandbox.rtk.command.after"
	AttrHecateSandboxNetworkEnabled   = "hecate.sandbox.network.enabled"
	AttrHecateSandboxReadOnly         = "hecate.sandbox.read_only"
	AttrHecateSandboxOutputLimit      = "hecate.sandbox.output_limit.bytes"
	AttrHecateToolExitCode            = "hecate.tool.exit_code"
	AttrHecateToolStdoutBytes         = "hecate.tool.stdout.bytes"
	AttrHecateToolStderrBytes         = "hecate.tool.stderr.bytes"
	AttrHecateToolTimedOut            = "hecate.tool.timed_out"
	AttrHecateToolCancelled           = "hecate.tool.cancelled"
	AttrHecateToolOutputTruncated     = "hecate.tool.output_truncated"
	AttrHecateToolWorkingDirectory    = "hecate.tool.working_directory"
	AttrHecateToolTimeoutMS           = "hecate.tool.timeout_ms"
	AttrHecateToolFileOperation       = "hecate.tool.file.operation"
	AttrHecateToolFileBytesWritten    = "hecate.tool.file.bytes_written"
	AttrHecateToolFileBeforeExisted   = "hecate.tool.file.before_existed"
	AttrHecateToolFileDiffBytes       = "hecate.tool.file.diff_bytes"
	AttrHecateToolFileArtifactStatus  = "hecate.tool.file.artifact_status"

	// Retention
	AttrRetentionTrigger   = "retention.trigger"
	AttrRetentionSubsystem = "retention.subsystem"
	AttrRetentionDeleted   = "retention.deleted"
	AttrRetentionResults   = "retention.results"

	// External agent chats
	AttrHecateChatSessionID              = "hecate.chat.session.id"
	AttrHecateChatMessageID              = "hecate.chat.message.id"
	AttrHecateChatTimingBucket           = "hecate.chat.timing.bucket"
	AttrHecateChatTimingTotalMS          = "hecate.chat.timing.total_ms"
	AttrHecateChatTimingQueueMS          = "hecate.chat.timing.queue_ms"
	AttrHecateChatTimingModelMS          = "hecate.chat.timing.model_ms"
	AttrHecateChatTimingToolMS           = "hecate.chat.timing.tool_ms"
	AttrHecateChatTimingApprovalMS       = "hecate.chat.timing.approval_wait_ms"
	AttrHecateChatTimingOverheadMS       = "hecate.chat.timing.overhead_ms"
	AttrHecateChatTimingBottleneck       = "hecate.chat.timing.bottleneck"
	AttrHecateChatTimingBottleneckMS     = "hecate.chat.timing.bottleneck_ms"
	AttrHecateAgentAdapterID             = "hecate.agent_adapter.id"
	AttrHecateAgentAdapterName           = "hecate.agent_adapter.name"
	AttrHecateAgentAdapterCommand        = "hecate.agent_adapter.command"
	AttrHecateAgentDriverKind            = "hecate.agent_adapter.driver.kind"
	AttrHecateAgentNativeSessionID       = "hecate.agent_adapter.native_session.id"
	AttrHecateAgentNativeSessionReplaced = "hecate.agent_adapter.native_session.replaced"
	AttrHecateAgentOutputBytes           = "hecate.agent_adapter.output.bytes"
	AttrHecateAgentRawOutputBytes        = "hecate.agent_adapter.raw_output.bytes"
	AttrHecateAgentDiffCaptured          = "hecate.agent_adapter.diff.captured"
	AttrHecateWorkspacePath              = "hecate.workspace.path"

	// External-adapter approval attributes — see docs/design/external-adapter-approvals-v1.md.
	// `decision` is approve|deny, `scope` is once|session|workspace_tool|adapter_tool,
	// `path` is operator|grant|default_mode|timeout, `mode` is the configured
	// HECATE_AGENT_ADAPTER_APPROVAL_MODE.
	AttrHecateAgentApprovalID       = "hecate.agent_adapter.approval.id"
	AttrHecateAgentApprovalToolKind = "hecate.agent_adapter.approval.tool_kind"
	AttrHecateAgentApprovalDecision = "hecate.agent_adapter.approval.decision"
	AttrHecateAgentApprovalScope    = "hecate.agent_adapter.approval.scope"
	AttrHecateAgentApprovalStatus   = "hecate.agent_adapter.approval.status"
	AttrHecateAgentApprovalMode     = "hecate.agent_adapter.approval.mode"
	AttrHecateAgentApprovalPath     = "hecate.agent_adapter.approval.path"

	// External-adapter runtime attributes — paired with the
	// adapter probe / chat-cancelled counters.
	// `status` is one of probe.Probe's classifications
	// (ready|not_installed|auth_required|error); `cancel.reason` is
	// one of operator|request_cancelled|shutdown.
	AttrHecateAgentProbeStatus = "hecate.agent_adapter.probe.status"
	AttrHecateChatCancelReason = "hecate.chat.cancel.reason"
)
