package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/telemetry"
)

func renderAgentChatActivities(items []chat.Activity) []ChatActivityItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]ChatActivityItem, 0, len(items))
	for _, item := range items {
		out = append(out, ChatActivityItem{
			ID:                item.ID,
			Type:              item.Type,
			Status:            item.Status,
			Kind:              item.Kind,
			Title:             item.Title,
			Detail:            item.Detail,
			CreatedAt:         formatOptionalTime(item.CreatedAt),
			ArtifactID:        item.ArtifactID,
			ArtifactSizeBytes: item.ArtifactSizeBytes,
			ArtifactPreview:   item.ArtifactPreview,
			ApprovalID:        item.ApprovalID,
			NeedsAction:       item.NeedsAction,
			MCPApp:            renderChatMCPApp(item.MCPApp),
		})
	}
	return out
}

func renderChatMCPApp(app *chat.MCPApp) *ChatMCPAppItem {
	if app == nil {
		return nil
	}
	return &ChatMCPAppItem{
		ResourceURI:   app.ResourceURI,
		MIMEType:      app.MIMEType,
		HTML:          app.HTML,
		HTMLTruncated: app.HTMLTruncated,
		ToolName:      app.ToolName,
		ToolInput:     cloneRawJSON(app.ToolInput),
		ToolResult:    cloneRawJSON(app.ToolResult),
		ResourceMeta:  cloneRawJSON(app.ResourceMeta),
		ToolMeta:      cloneRawJSON(app.ToolMeta),
		Error:         app.Error,
	}
}

func newChatActivity(kind, status, title, detail string) chat.Activity {
	return chat.Activity{
		Type:      kind,
		Status:    status,
		Title:     title,
		Detail:    strings.TrimSpace(detail),
		CreatedAt: time.Now().UTC(),
	}
}

func externalAgentStopReasonActivity(reason string) (chat.Activity, bool) {
	reason = strings.TrimSpace(reason)
	switch reason {
	case "", "end_turn", "cancelled":
		return chat.Activity{}, false
	case "max_tokens":
		return newChatActivity("stop_reason", "completed", "Agent stop reason", "The external agent stopped because it reached its token limit."), true
	case "max_turn_requests":
		return newChatActivity("stop_reason", "completed", "Agent stop reason", "The external agent stopped because it reached its turn limit."), true
	case "refusal":
		return newChatActivity("stop_reason", "completed", "Agent stop reason", "The external agent refused to continue this turn."), true
	default:
		return newChatActivity("stop_reason", "completed", "Agent stop reason", "ACP stop reason: "+reason), true
	}
}

func agentChatActivityFromAdapter(activity agentadapters.Activity) chat.Activity {
	return chat.Activity{
		ID:              strings.TrimSpace(activity.ID),
		Type:            strings.TrimSpace(activity.Type),
		Status:          strings.TrimSpace(activity.Status),
		Kind:            strings.TrimSpace(activity.Kind),
		Title:           strings.TrimSpace(activity.Title),
		Detail:          strings.TrimSpace(activity.Detail),
		CreatedAt:       time.Now().UTC(),
		ArtifactPreview: strings.TrimRight(activity.ArtifactPreview, "\r\n"),
	}
}

func agentChatActivitiesFromTaskActivity(items []TaskActivityItem) []chat.Activity {
	if len(items) == 0 {
		return nil
	}
	out := make([]chat.Activity, 0, len(items))
	for _, item := range items {
		activity := agentChatActivityFromTaskActivity(item)
		if activity.Type == "" || activity.Title == "" {
			continue
		}
		out = append(out, activity)
	}
	return out
}

func agentChatActivityFromTaskActivity(item TaskActivityItem) chat.Activity {
	title := strings.TrimSpace(firstNonEmpty(item.Title, item.ToolName, item.Path, item.Kind, item.Type))
	return chat.Activity{
		ID:                strings.TrimSpace("task:" + item.ID),
		Type:              strings.TrimSpace(item.Type),
		Status:            strings.TrimSpace(item.Status),
		Kind:              strings.TrimSpace(firstNonEmpty(item.Kind, item.ToolName)),
		Title:             title,
		Detail:            agentChatTaskActivityDetail(item),
		CreatedAt:         parseChatActivityTime(item.OccurredAt),
		ArtifactID:        strings.TrimSpace(item.ArtifactID),
		ArtifactSizeBytes: agentChatTaskArtifactSize(item),
		ArtifactPreview:   agentChatTaskArtifactPreview(item),
		ApprovalID:        strings.TrimSpace(item.ApprovalID),
		NeedsAction:       item.NeedsAction,
		MCPApp:            agentChatTaskMCPApp(item),
	}
}

func agentChatTaskMCPApp(item TaskActivityItem) *chat.MCPApp {
	if item.Summary == nil {
		return nil
	}
	rawApp, ok := item.Summary["mcp_app"]
	if !ok {
		return nil
	}
	appMap, ok := mapStringAny(rawApp)
	if !ok {
		return nil
	}
	app := &chat.MCPApp{
		ResourceURI:   strings.TrimSpace(stringFromMap(appMap, "resource_uri")),
		MIMEType:      strings.TrimSpace(stringFromMap(appMap, "mime_type")),
		HTML:          stringFromMap(appMap, "html"),
		HTMLTruncated: boolFromMap(appMap, "html_truncated"),
		ToolName:      strings.TrimSpace(stringFromMap(appMap, "tool_name")),
		ToolInput:     rawJSONFromMap(appMap, "tool_input"),
		ToolResult:    rawJSONFromMap(appMap, "tool_result"),
		ResourceMeta:  rawJSONFromMap(appMap, "resource_meta"),
		ToolMeta:      rawJSONFromMap(appMap, "tool_meta"),
		Error:         strings.TrimSpace(stringFromMap(appMap, "error")),
	}
	if app.ResourceURI == "" && app.HTML == "" && app.Error == "" {
		return nil
	}
	return app
}

func mapStringAny(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case json.RawMessage:
		var out map[string]any
		if err := json.Unmarshal(typed, &out); err == nil {
			return out, true
		}
	case []byte:
		var out map[string]any
		if err := json.Unmarshal(typed, &out); err == nil {
			return out, true
		}
	}
	return nil, false
}

func stringFromMap(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.RawMessage:
		var out string
		if err := json.Unmarshal(typed, &out); err == nil {
			return out
		}
	}
	return ""
}

func boolFromMap(values map[string]any, key string) bool {
	value, ok := values[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return typed == "true"
	case json.RawMessage:
		var out bool
		if err := json.Unmarshal(typed, &out); err == nil {
			return out
		}
	}
	return false
}

func rawJSONFromMap(values map[string]any, key string) json.RawMessage {
	value, ok := values[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case json.RawMessage:
		return cloneRawJSON(typed)
	case []byte:
		return cloneRawJSON(typed)
	}
	data, err := json.Marshal(value)
	if err != nil || len(data) == 0 || string(data) == "null" {
		return nil
	}
	return data
}

func cloneRawJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func agentChatTaskArtifactSize(item TaskActivityItem) int64 {
	if item.Summary == nil {
		return 0
	}
	switch value := item.Summary["size_bytes"].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case json.Number:
		n, _ := value.Int64()
		return n
	default:
		return 0
	}
}

func agentChatTaskArtifactPreview(item TaskActivityItem) string {
	if item.Summary == nil {
		return ""
	}
	value, _ := item.Summary["content_preview"].(string)
	return strings.TrimRight(value, "\r\n")
}

func agentChatTaskActivityDetail(item TaskActivityItem) string {
	if item.Type == orchestrator.ProjectAssistantProposalArtifactKind || item.Kind == orchestrator.ProjectAssistantProposalArtifactKind {
		return agentChatProjectAssistantProposalDetail(item)
	}
	parts := make([]string, 0, 3)
	if item.Path != "" && item.Path != item.Title {
		parts = append(parts, item.Path)
	}
	if item.ToolName != "" && item.ToolName != item.Title && item.ToolName != item.Kind {
		parts = append(parts, item.ToolName)
	}
	if rtkEnabled, _ := item.Summary[telemetry.AttrHecateSandboxRTKEnabled].(bool); rtkEnabled {
		parts = append(parts, "via RTK")
	}
	rtkCommandDetail := false
	if rtkBefore, _ := item.Summary[telemetry.AttrHecateSandboxRTKCommandBefore].(string); rtkBefore != "" {
		if rtkAfter, _ := item.Summary[telemetry.AttrHecateSandboxRTKCommandAfter].(string); rtkAfter != "" {
			parts = append(parts, "RTK: "+rtkBefore+" -> "+rtkAfter)
			rtkCommandDetail = true
		}
	}
	if argv := compactActivityArgv(item.Summary["argv"]); argv != "" && !rtkCommandDetail {
		parts = append(parts, argv)
	}
	if reason, ok := item.Summary["reason"].(string); ok && strings.TrimSpace(reason) != "" {
		parts = append(parts, strings.TrimSpace(reason))
	}
	if item.Status != "" {
		parts = append(parts, item.Status)
	}
	return strings.Join(parts, " - ")
}

func agentChatProjectAssistantProposalDetail(item TaskActivityItem) string {
	parts := make([]string, 0, 3)
	if title, _ := item.Summary["proposal_title"].(string); strings.TrimSpace(title) != "" && title != item.Title {
		parts = append(parts, strings.TrimSpace(title))
	}
	if actionCount := intFromActivitySummary(item.Summary["proposal_action_count"]); actionCount > 0 {
		parts = append(parts, fmt.Sprintf("%d action%s", actionCount, activityPluralSuffix(actionCount)))
	}
	parts = append(parts, "ready for review")
	return strings.Join(parts, " - ")
}

func intFromActivitySummary(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	default:
		return 0
	}
}

func activityPluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func compactActivityArgv(value any) string {
	switch argv := value.(type) {
	case []string:
		return strings.Join(argv, " ")
	case []any:
		parts := make([]string, 0, len(argv))
		for _, part := range argv {
			text, ok := part.(string)
			if !ok {
				return ""
			}
			parts = append(parts, text)
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}

func parseChatActivityTime(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed
	}
	return time.Time{}
}

func agentChatActivitySignature(items []chat.Activity) string {
	if len(items) == 0 {
		return "[]"
	}
	payload, err := json.Marshal(items)
	if err != nil {
		return ""
	}
	return string(payload)
}

func mergeChatActivity(items []chat.Activity, next chat.Activity) []chat.Activity {
	if next.Type == "" || (next.ID == "" && next.Title == "") {
		return items
	}
	if next.ID != "" {
		for i := range items {
			if items[i].ID == next.ID {
				if next.Status != "" {
					items[i].Status = next.Status
				}
				if next.Kind != "" {
					items[i].Kind = next.Kind
				}
				if next.Title != "" {
					items[i].Title = next.Title
				}
				if next.Detail != "" {
					items[i].Detail = next.Detail
				}
				if next.ApprovalID != "" {
					items[i].ApprovalID = next.ApprovalID
				}
				if next.ArtifactID != "" {
					items[i].ArtifactID = next.ArtifactID
				}
				if next.ArtifactSizeBytes != 0 {
					items[i].ArtifactSizeBytes = next.ArtifactSizeBytes
				}
				if next.ArtifactPreview != "" {
					items[i].ArtifactPreview = next.ArtifactPreview
				}
				if next.MCPApp != nil {
					items[i].MCPApp = cloneChatMCPApp(next.MCPApp)
				}
				items[i].NeedsAction = next.NeedsAction
				items[i].CreatedAt = next.CreatedAt
				return items
			}
		}
	}
	if next.Title == "" {
		return items
	}
	return append(items, next)
}

func cloneChatMCPApp(app *chat.MCPApp) *chat.MCPApp {
	if app == nil {
		return nil
	}
	clone := *app
	clone.ToolInput = cloneRawJSON(app.ToolInput)
	clone.ToolResult = cloneRawJSON(app.ToolResult)
	clone.ResourceMeta = cloneRawJSON(app.ResourceMeta)
	clone.ToolMeta = cloneRawJSON(app.ToolMeta)
	return &clone
}

func finalChatActivityTitle(status string) string {
	switch status {
	case "completed":
		return "Final answer"
	case "failed":
		return "Failed"
	case "cancelled":
		return "Cancelled"
	default:
		return status
	}
}

// agentChatTerminalEvent maps an assistant message's terminal status
// to the matching telemetry event name. Lives next to the activity
// helpers because both translate the same terminal status enum into
// either UI activities or trace events.
func agentChatTerminalEvent(status string) string {
	switch status {
	case "cancelled":
		return telemetry.EventAgentChatRunCancelled
	case "failed":
		return telemetry.EventAgentChatRunFailed
	default:
		return telemetry.EventAgentChatRunFinished
	}
}
