package eventprotocol

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/pkg/types"
)

const SchemaVersion = "1"

const crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Envelope is the API-facing event shape for agent-runtime event protocol v1.
// Storage can keep its compact taskstate row; clients should consume this shape.
type Envelope struct {
	SchemaVersion string         `json:"schema_version"`
	EventID       string         `json:"event_id"`
	RunID         string         `json:"run_id"`
	TaskID        string         `json:"task_id,omitempty"`
	SessionID     string         `json:"session_id,omitempty"`
	Sequence      int64          `json:"sequence"`
	OccurredAt    string         `json:"occurred_at"`
	Type          string         `json:"type"`
	Data          map[string]any `json:"data"`
}

// FromTaskRunEvent converts a persisted task run event into the v1 envelope.
func FromTaskRunEvent(event types.TaskRunEvent) Envelope {
	return Envelope{
		SchemaVersion: SchemaVersion,
		EventID:       eventIDFor(event),
		RunID:         event.RunID,
		TaskID:        event.TaskID,
		Sequence:      event.Sequence,
		OccurredAt:    occurredAt(event.CreatedAt),
		Type:          event.EventType,
		Data:          normalizeData(event),
	}
}

// FromTaskRunEvents converts a batch of persisted task run events.
func FromTaskRunEvents(events []types.TaskRunEvent) []Envelope {
	items := make([]Envelope, 0, len(events))
	for _, event := range events {
		items = append(items, FromTaskRunEvent(event))
	}
	return items
}

func eventIDFor(event types.TaskRunEvent) string {
	if strings.HasPrefix(event.ID, "evt_") {
		return event.ID
	}
	seed := strings.Join([]string{
		event.TaskID,
		event.RunID,
		strconv.FormatInt(event.Sequence, 10),
		event.EventType,
		event.CreatedAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "evt_" + crockfordEncodeTime(event.CreatedAt) + crockfordEncodeBytes(sum[:10])
}

func crockfordEncodeTime(t time.Time) string {
	if t.IsZero() {
		t = time.Unix(0, 0)
	}
	millis := uint64(t.UTC().UnixMilli())
	var out [10]byte
	for i := range out {
		shift := uint((9 - i) * 5)
		out[i] = crockfordAlphabet[(millis>>shift)&0x1F]
	}
	return string(out[:])
}

func crockfordEncodeBytes(raw []byte) string {
	if len(raw) < 10 {
		panic(fmt.Sprintf("crockfordEncodeBytes requires 10 bytes, got %d", len(raw)))
	}
	var out [16]byte
	bit := 0
	for i := range out {
		var value byte
		for j := 0; j < 5; j++ {
			byteIndex := bit / 8
			bitIndex := 7 - (bit % 8)
			value <<= 1
			value |= (raw[byteIndex] >> bitIndex) & 1
			bit++
		}
		out[i] = crockfordAlphabet[value]
	}
	return string(out[:])
}

func occurredAt(t time.Time) string {
	if t.IsZero() {
		return time.Unix(0, 0).UTC().Format(time.RFC3339Nano)
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func cloneData(data map[string]any) map[string]any {
	if data == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(data))
	for key, value := range data {
		out[key] = value
	}
	return out
}

func normalizeData(event types.TaskRunEvent) map[string]any {
	data := compactData(event.Data)
	run := runSnapshot(event.Data)

	switch event.EventType {
	case "run.created":
		return compactMap(map[string]any{
			"status":         stringValue(run, "Status", "status"),
			"kind":           stringValue(run, "Orchestrator", "orchestrator"),
			"model":          stringValue(run, "Model", "model"),
			"provider":       stringValue(run, "Provider", "provider"),
			"provider_kind":  stringValue(run, "ProviderKind", "provider_kind"),
			"workspace_path": stringValue(run, "WorkspacePath", "workspace_path"),
		})
	case "run.queued":
		out := compactMap(map[string]any{
			"kind":           stringValue(run, "Orchestrator", "orchestrator"),
			"model":          stringValue(run, "Model", "model"),
			"provider":       stringValue(run, "Provider", "provider"),
			"provider_kind":  stringValue(run, "ProviderKind", "provider_kind"),
			"workspace_path": stringValue(run, "WorkspacePath", "workspace_path"),
		})
		copyKnown(out, data, "resume")
		return out
	case "run.awaiting_approval":
		return compactMap(map[string]any{
			"status": stringValue(run, "Status", "status"),
			"kind":   stringValue(run, "Orchestrator", "orchestrator"),
		})
	case "run.started":
		out := compactMap(map[string]any{
			"kind":           stringValue(run, "Orchestrator", "orchestrator"),
			"model":          stringValue(run, "Model", "model"),
			"provider":       stringValue(run, "Provider", "provider"),
			"provider_kind":  stringValue(run, "ProviderKind", "provider_kind"),
			"workspace_path": stringValue(run, "WorkspacePath", "workspace_path"),
		})
		copyKnown(out, data, "worker_id", "lease_until", "resume_from_run_id", "resume_from_step_id", "resume_from_event_sequence", "queue_wait_ms")
		return out
	case "run.finished":
		return compactMap(map[string]any{
			"final_status":     "completed",
			"turns":            numberValue(run, "StepCount", "step_count"),
			"cost_micros_usd":  firstPresent(data["cost_micros_usd"], numberValue(run, "TotalCostMicrosUSD", "total_cost_micros_usd")),
			"duration_ms":      durationMSFromRun(run),
			"error":            firstPresent(data["error"]),
			"otel_status_code": stringValue(run, "OtelStatusCode", "otel_status_code"),
		})
	case "run.failed":
		message := firstString(data, "error")
		if message == "" {
			message = stringValue(run, "LastError", "last_error")
		}
		return compactMap(map[string]any{
			"code":        firstNonEmptyString(stringValue(run, "OtelStatusMessage", "otel_status_message"), "run_failed"),
			"message":     message,
			"retriable":   false,
			"turns":       numberValue(run, "StepCount", "step_count"),
			"duration_ms": durationMSFromRun(run),
		})
	case "run.cancelled":
		reason := firstString(data, "reason")
		if reason == "" {
			reason = stringValue(run, "LastError", "last_error")
		}
		return compactMap(map[string]any{
			"by":     "operator",
			"reason": reason,
		})
	case "run.resumed_from_event":
		out := compactMap(map[string]any{
			"from_run_id":           firstPresent(data["resumed_from_run_id"], data["from_run_id"]),
			"from_sequence":         firstPresent(data["resume_from_event_sequence"], data["from_sequence"]),
			"reason":                data["reason"],
			"retry_from_turn":       data["retry_from_turn"],
			"prior_cost_micros_usd": numberValue(run, "PriorCostMicrosUSD", "prior_cost_micros_usd"),
		})
		return out
	case "gap.run_disconnected":
		out := compactMap(map[string]any{
			"reason":  data["reason"],
			"action":  data["action"],
			"message": firstPresent(data["message"], data["error"]),
		})
		copyKnown(out, data, "prior_status", "recovered_status", "recovery_strategy", "stale_threshold_ms")
		return out
	case "approval.requested", "approval.resolved", "turn.completed":
		return data
	default:
		if strings.HasPrefix(event.EventType, "tool.") || strings.HasPrefix(event.EventType, "policy.") {
			return data
		}
		return data
	}
}

func compactData(data map[string]any) map[string]any {
	out := cloneData(data)
	delete(out, "run")
	delete(out, "steps")
	delete(out, "artifacts")
	delete(out, "snapshot")
	return out
}

func runSnapshot(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	switch run := data["run"].(type) {
	case types.TaskRun:
		return map[string]any{
			"Status":                run.Status,
			"Orchestrator":          run.Orchestrator,
			"Model":                 run.Model,
			"Provider":              run.Provider,
			"ProviderKind":          run.ProviderKind,
			"WorkspacePath":         run.WorkspacePath,
			"StepCount":             run.StepCount,
			"TotalCostMicrosUSD":    run.TotalCostMicrosUSD,
			"PriorCostMicrosUSD":    run.PriorCostMicrosUSD,
			"LastError":             run.LastError,
			"StartedAt":             run.StartedAt,
			"FinishedAt":            run.FinishedAt,
			"OtelStatusCode":        run.OtelStatusCode,
			"OtelStatusMessage":     run.OtelStatusMessage,
			"status":                run.Status,
			"orchestrator":          run.Orchestrator,
			"model":                 run.Model,
			"provider":              run.Provider,
			"provider_kind":         run.ProviderKind,
			"workspace_path":        run.WorkspacePath,
			"step_count":            run.StepCount,
			"total_cost_micros_usd": run.TotalCostMicrosUSD,
			"prior_cost_micros_usd": run.PriorCostMicrosUSD,
			"last_error":            run.LastError,
			"started_at":            run.StartedAt,
			"finished_at":           run.FinishedAt,
			"otel_status_code":      run.OtelStatusCode,
			"otel_status_message":   run.OtelStatusMessage,
		}
	case map[string]any:
		return run
	default:
		return nil
	}
}

func compactMap(data map[string]any) map[string]any {
	out := make(map[string]any, len(data))
	for key, value := range data {
		if isEmptyValue(value) {
			continue
		}
		out[key] = value
	}
	return out
}

func copyKnown(dst map[string]any, src map[string]any, keys ...string) {
	for _, key := range keys {
		if value, ok := src[key]; ok && !isEmptyValue(value) {
			dst[key] = value
		}
	}
}

func firstPresent(values ...any) any {
	for _, value := range values {
		if !isEmptyValue(value) {
			return value
		}
	}
	return nil
}

func firstString(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringFromAny(data[key]); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func stringValue(data map[string]any, keys ...string) string {
	if data == nil {
		return ""
	}
	for _, key := range keys {
		if value := stringFromAny(data[key]); value != "" {
			return value
		}
	}
	return ""
}

func numberValue(data map[string]any, keys ...string) any {
	if data == nil {
		return nil
	}
	for _, key := range keys {
		if value, ok := data[key]; ok && !isEmptyValue(value) {
			return value
		}
	}
	return nil
}

func durationMSFromRun(run map[string]any) any {
	started := timeValue(run, "StartedAt", "started_at")
	finished := timeValue(run, "FinishedAt", "finished_at")
	if started.IsZero() || finished.IsZero() || finished.Before(started) {
		return nil
	}
	return finished.Sub(started).Milliseconds()
}

func timeValue(data map[string]any, keys ...string) time.Time {
	if data == nil {
		return time.Time{}
	}
	for _, key := range keys {
		switch value := data[key].(type) {
		case time.Time:
			return value.UTC()
		case string:
			if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
				return parsed.UTC()
			}
		}
	}
	return time.Time{}
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}

func isEmptyValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return typed == ""
	case time.Time:
		return typed.IsZero()
	default:
		return false
	}
}
