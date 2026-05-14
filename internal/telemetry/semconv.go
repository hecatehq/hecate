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
	AttrHecateAgentChatSessionID          = "hecate.agent_chat.session.id"
	AttrHecateAgentChatMessageID          = "hecate.agent_chat.message.id"
	AttrHecateAgentChatTimingBucket       = "hecate.agent_chat.timing.bucket"
	AttrHecateAgentChatTimingTotalMS      = "hecate.agent_chat.timing.total_ms"
	AttrHecateAgentChatTimingQueueMS      = "hecate.agent_chat.timing.queue_ms"
	AttrHecateAgentChatTimingModelMS      = "hecate.agent_chat.timing.model_ms"
	AttrHecateAgentChatTimingToolMS       = "hecate.agent_chat.timing.tool_ms"
	AttrHecateAgentChatTimingApprovalMS   = "hecate.agent_chat.timing.approval_wait_ms"
	AttrHecateAgentChatTimingOverheadMS   = "hecate.agent_chat.timing.overhead_ms"
	AttrHecateAgentChatTimingBottleneck   = "hecate.agent_chat.timing.bottleneck"
	AttrHecateAgentChatTimingBottleneckMS = "hecate.agent_chat.timing.bottleneck_ms"
	AttrHecateAgentAdapterID              = "hecate.agent_adapter.id"
	AttrHecateAgentAdapterName            = "hecate.agent_adapter.name"
	AttrHecateAgentAdapterCommand         = "hecate.agent_adapter.command"
	AttrHecateAgentDriverKind             = "hecate.agent_adapter.driver.kind"
	AttrHecateAgentNativeSessionID        = "hecate.agent_adapter.native_session.id"
	AttrHecateAgentOutputBytes            = "hecate.agent_adapter.output.bytes"
	AttrHecateAgentRawOutputBytes         = "hecate.agent_adapter.raw_output.bytes"
	AttrHecateAgentDiffCaptured           = "hecate.agent_adapter.diff.captured"
	AttrHecateWorkspacePath               = "hecate.workspace.path"

	// External-adapter approval attributes — see docs/rfcs/external-adapter-approvals-v1.md.
	// `decision` is approve|deny, `scope` is once|session|workspace_tool|adapter_tool,
	// `path` is operator|grant|default_mode|timeout, `mode` is the configured
	// GATEWAY_AGENT_ADAPTER_APPROVAL_MODE.
	AttrHecateAgentApprovalID       = "hecate.agent_adapter.approval.id"
	AttrHecateAgentApprovalToolKind = "hecate.agent_adapter.approval.tool_kind"
	AttrHecateAgentApprovalDecision = "hecate.agent_adapter.approval.decision"
	AttrHecateAgentApprovalScope    = "hecate.agent_adapter.approval.scope"
	AttrHecateAgentApprovalStatus   = "hecate.agent_adapter.approval.status"
	AttrHecateAgentApprovalMode     = "hecate.agent_adapter.approval.mode"
	AttrHecateAgentApprovalPath     = "hecate.agent_adapter.approval.path"

	// External-adapter runtime attributes — paired with the
	// adapter probe / terminal-RPC / chat-cancelled counters.
	// `status` is one of probe.Probe's classifications
	// (ready|not_installed|auth_required|error); `terminal.method`
	// is one of create|kill|output|release|wait; `cancel.reason`
	// is one of operator|request_cancelled|shutdown.
	AttrHecateAgentProbeStatus      = "hecate.agent_adapter.probe.status"
	AttrHecateAgentTerminalMethod   = "hecate.agent_adapter.terminal.method"
	AttrHecateAgentChatCancelReason = "hecate.agent_chat.cancel.reason"

	// Local-models attributes — paired with the
	// local_model.install.* / local_model.runtime.* / local_model.proxy.*
	// events. `local_model.id` is the slug used everywhere
	// (registry, /v1/models, chat composer). `engine` is "llamacpp" in
	// v1 — reserved so a future MLX engine can be distinguished.
	// `install.error_kind` is one of the closed set defined in
	// internal/llamacpp (network|sha_mismatch|cancelled|disk|gated|
	// invalid_url|unknown). `runtime.reason` distinguishes
	// operator|switch|crash on the stopped event so dashboards can
	// alert on crash rate.
	AttrHecateLocalModelID                    = "hecate.local_model.id"
	AttrHecateLocalModelEngine                = "hecate.local_model.engine"
	AttrHecateLocalModelDisplayName           = "hecate.local_model.display_name"
	AttrHecateLocalModelRuntimePort           = "hecate.local_model.runtime.port"
	AttrHecateLocalModelRuntimePID            = "hecate.local_model.runtime.pid"
	AttrHecateLocalModelRuntimeContextSize    = "hecate.local_model.runtime.params.context_size"
	AttrHecateLocalModelRuntimeReason         = "hecate.local_model.runtime.reason"
	AttrHecateLocalModelRuntimeUptimeMS       = "hecate.local_model.runtime.uptime_ms"
	AttrHecateLocalModelRuntimeTTFHMS         = "hecate.local_model.runtime.ttfh_ms"
	AttrHecateLocalModelRuntimeExitCode       = "hecate.local_model.runtime.exit_code"
	AttrHecateLocalModelInstallID             = "hecate.local_model.install.id"
	AttrHecateLocalModelInstallSourceURL      = "hecate.local_model.install.source_url"
	AttrHecateLocalModelInstallBytesTotal     = "hecate.local_model.install.bytes_total"
	AttrHecateLocalModelInstallBytesDone      = "hecate.local_model.install.bytes_downloaded"
	AttrHecateLocalModelInstallDurationMS     = "hecate.local_model.install.duration_ms"
	AttrHecateLocalModelInstallErrorKind      = "hecate.local_model.install.error_kind"
	AttrHecateLocalModelInstallExpectedSHA256 = "hecate.local_model.install.expected_sha256"
	AttrHecateLocalModelInstallActualSHA256   = "hecate.local_model.install.actual_sha256"
)
