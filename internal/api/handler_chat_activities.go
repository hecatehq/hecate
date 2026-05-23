package api

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/chat"
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
		})
	}
	return out
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

func agentChatActivityFromAdapter(activity agentadapters.Activity) chat.Activity {
	return chat.Activity{
		ID:        strings.TrimSpace(activity.ID),
		Type:      strings.TrimSpace(activity.Type),
		Status:    strings.TrimSpace(activity.Status),
		Kind:      strings.TrimSpace(activity.Kind),
		Title:     strings.TrimSpace(activity.Title),
		Detail:    strings.TrimSpace(activity.Detail),
		CreatedAt: time.Now().UTC(),
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
	}
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
