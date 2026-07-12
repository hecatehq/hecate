package orchestrator

import (
	"context"
	"encoding/json"

	"github.com/hecatehq/hecate/internal/telemetry"
)

func (d *agentLoopToolDispatcher) recordMCPCallTelemetry(
	ctx context.Context,
	spec ExecutionSpec,
	toolCallID, toolName, server, tool, result string,
	durationMS int64,
	errMsg string,
) {
	if d.metrics != nil {
		d.metrics.RecordMCPToolCall(ctx, telemetry.MCPToolCallRecord{
			Server:     server,
			Tool:       tool,
			Result:     result,
			DurationMS: durationMS,
		})
	}
	if spec.EmitRunEvent == nil {
		return
	}
	// Map the four call-result values to event names. blocked +
	// failed get distinct protocol events because operators tend to
	// alert on failed but treat blocked as an audit signal; conflating
	// them would mask one or trigger pages on the other.
	var eventType string
	switch result {
	case telemetry.MCPCallResultBlocked:
		eventType = telemetry.EventMCPToolBlocked
	case telemetry.MCPCallResultFailed:
		eventType = telemetry.EventMCPToolFailed
	default:
		// Both Dispatched and ToolError land on tool.completed — the
		// payload's `result` distinguishes clean completions from
		// upstream tool errors without spawning a provider-specific
		// event name.
		eventType = telemetry.EventMCPToolCompleted
	}
	data := map[string]any{
		"tool_call_id": toolCallID,
		"tool_name":    toolName,
		"kind":         "mcp",
		"mcp_server":   server,
		"mcp_tool":     tool,
		"result":       result,
		"duration_ms":  durationMS,
	}
	if eventType == telemetry.EventMCPToolBlocked {
		data["policy"] = "mcp_approval_policy"
		data["reason"] = errMsg
	}
	if errMsg != "" {
		data["error"] = errMsg
	}
	spec.EmitRunEvent(eventType, data)
}

// mcpToolInputForLog captures the call inputs for the step's Input
// field. Args may be arbitrarily large (file contents, etc.) — we
// truncate so the step row stays a reasonable size in the store. The
// full args remain in the conversation snapshot if operators need
// them.
func mcpToolInputForLog(name string, args json.RawMessage) map[string]any {
	const cap = 4 * 1024
	out := map[string]any{"tool": name}
	if len(args) <= cap {
		out["arguments"] = string(args)
	} else {
		out["arguments"] = string(args[:cap]) + "...(truncated)"
		out["arguments_truncated_bytes"] = len(args) - cap
	}
	return out
}
