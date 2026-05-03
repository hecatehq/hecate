package eventprotocol

import (
	"regexp"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/pkg/types"
)

func TestFromTaskRunEventMapsV1Envelope(t *testing.T) {
	createdAt := time.Date(2026, 5, 3, 10, 30, 0, 123456789, time.FixedZone("CEST", 2*60*60))
	event := types.TaskRunEvent{
		ID:        "42",
		TaskID:    "task_01HX0000000000000000000001",
		RunID:     "run_01HX0000000000000000000001",
		Sequence:  7,
		EventType: "run.started",
		Data: map[string]any{
			"worker_id": "worker-local-1",
		},
		CreatedAt: createdAt,
		RequestID: "req_ignored",
		TraceID:   "trace_ignored",
	}

	envelope := FromTaskRunEvent(event)

	if envelope.SchemaVersion != "1" {
		t.Fatalf("SchemaVersion = %q, want 1", envelope.SchemaVersion)
	}
	if !regexp.MustCompile(`^evt_[0-9A-HJKMNP-TV-Z]{26}$`).MatchString(envelope.EventID) {
		t.Fatalf("EventID = %q, want stable evt_ id", envelope.EventID)
	}
	if envelope.TaskID != event.TaskID {
		t.Fatalf("TaskID = %q, want %q", envelope.TaskID, event.TaskID)
	}
	if envelope.RunID != event.RunID {
		t.Fatalf("RunID = %q, want %q", envelope.RunID, event.RunID)
	}
	if envelope.Sequence != event.Sequence {
		t.Fatalf("Sequence = %d, want %d", envelope.Sequence, event.Sequence)
	}
	if envelope.Type != event.EventType {
		t.Fatalf("Type = %q, want %q", envelope.Type, event.EventType)
	}
	if envelope.OccurredAt != "2026-05-03T08:30:00.123456789Z" {
		t.Fatalf("OccurredAt = %q", envelope.OccurredAt)
	}
	if got := envelope.Data["worker_id"]; got != "worker-local-1" {
		t.Fatalf("Data.worker_id = %v", got)
	}
	if _, ok := envelope.Data["request_id"]; ok {
		t.Fatalf("Data unexpectedly contains request_id")
	}
	if _, ok := envelope.Data["trace_id"]; ok {
		t.Fatalf("Data unexpectedly contains trace_id")
	}
	if _, ok := envelope.Data["run"]; ok {
		t.Fatalf("Data unexpectedly contains runtime snapshot")
	}
}

func TestFromTaskRunEventKeepsExplicitEventID(t *testing.T) {
	event := types.TaskRunEvent{
		ID:        "evt_01HX0000000000000000000001",
		RunID:     "run_01HX0000000000000000000001",
		Sequence:  1,
		EventType: "run.finished",
		CreatedAt: time.Date(2026, 5, 3, 10, 30, 0, 0, time.UTC),
	}

	envelope := FromTaskRunEvent(event)

	if envelope.EventID != event.ID {
		t.Fatalf("EventID = %q, want explicit %q", envelope.EventID, event.ID)
	}
	if envelope.Data == nil {
		t.Fatalf("Data must be an empty object, not nil")
	}
}

func TestFromTaskRunEventEventIDIsStable(t *testing.T) {
	event := types.TaskRunEvent{
		TaskID:    "task_01HX0000000000000000000001",
		RunID:     "run_01HX0000000000000000000001",
		Sequence:  7,
		EventType: "run.started",
		CreatedAt: time.Date(2026, 5, 3, 10, 30, 0, 0, time.UTC),
	}

	first := FromTaskRunEvent(event)
	second := FromTaskRunEvent(event)

	if first.EventID != second.EventID {
		t.Fatalf("EventID changed between mappings: %q != %q", first.EventID, second.EventID)
	}
}

func TestFromTaskRunEventNormalizesTerminalRunPayload(t *testing.T) {
	startedAt := time.Date(2026, 5, 3, 10, 30, 0, 0, time.UTC)
	finishedAt := startedAt.Add(1500 * time.Millisecond)
	event := types.TaskRunEvent{
		RunID:     "run_01HX0000000000000000000001",
		Sequence:  3,
		EventType: "run.finished",
		Data: map[string]any{
			"run": types.TaskRun{
				Status:             "completed",
				StepCount:          2,
				TotalCostMicrosUSD: 0,
				StartedAt:          startedAt,
				FinishedAt:         finishedAt,
				OtelStatusCode:     "ok",
				OtelStatusMessage:  "",
				PriorCostMicrosUSD: 0,
				LastError:          "",
			},
			"steps":     []types.TaskStep{{ID: "step_1"}},
			"artifacts": []types.TaskArtifact{{ID: "artifact_1"}},
			"status":    "completed",
			"error":     "",
		},
		CreatedAt: finishedAt,
	}

	envelope := FromTaskRunEvent(event)

	if envelope.Data["final_status"] != "completed" {
		t.Fatalf("final_status = %v", envelope.Data["final_status"])
	}
	if envelope.Data["turns"] != 2 {
		t.Fatalf("turns = %v, want 2", envelope.Data["turns"])
	}
	if envelope.Data["cost_micros_usd"] != int64(0) {
		t.Fatalf("cost_micros_usd = %v, want 0", envelope.Data["cost_micros_usd"])
	}
	if envelope.Data["duration_ms"] != int64(1500) {
		t.Fatalf("duration_ms = %v, want 1500", envelope.Data["duration_ms"])
	}
	for _, key := range []string{"run", "steps", "artifacts", "status"} {
		if _, ok := envelope.Data[key]; ok {
			t.Fatalf("Data unexpectedly contains %s", key)
		}
	}
}

func TestFromTaskRunEventNormalizesResumePayload(t *testing.T) {
	event := types.TaskRunEvent{
		RunID:     "run_01HX0000000000000000000002",
		Sequence:  1,
		EventType: "run.resumed_from_event",
		Data: map[string]any{
			"run": types.TaskRun{
				PriorCostMicrosUSD: 1234,
			},
			"resumed_from_run_id": "run_01HX0000000000000000000001",
			"reason":              "continue after cancellation",
			"retry_from_turn":     3,
		},
	}

	envelope := FromTaskRunEvent(event)

	if envelope.Data["from_run_id"] != "run_01HX0000000000000000000001" {
		t.Fatalf("from_run_id = %v", envelope.Data["from_run_id"])
	}
	if envelope.Data["prior_cost_micros_usd"] != int64(1234) {
		t.Fatalf("prior_cost_micros_usd = %v", envelope.Data["prior_cost_micros_usd"])
	}
	if _, ok := envelope.Data["resumed_from_run_id"]; ok {
		t.Fatalf("Data unexpectedly contains resumed_from_run_id")
	}
}

func TestFromTaskRunEventStripsSnapshotsFromNonRunPayloads(t *testing.T) {
	event := types.TaskRunEvent{
		RunID:     "run_01HX0000000000000000000001",
		Sequence:  4,
		EventType: "turn.completed",
		Data: map[string]any{
			"run":                            types.TaskRun{ID: "run_1"},
			"steps":                          []types.TaskStep{{ID: "step_1"}},
			"artifacts":                      []types.TaskArtifact{{ID: "artifact_1"}},
			"turn_index":                     1,
			"cost_micros_usd":                int64(0),
			"run_cumulative_cost_micros_usd": int64(0),
		},
	}

	envelope := FromTaskRunEvent(event)

	if envelope.Data["turn_index"] != 1 {
		t.Fatalf("turn_index = %v, want 1", envelope.Data["turn_index"])
	}
	if envelope.Data["cost_micros_usd"] != int64(0) {
		t.Fatalf("cost_micros_usd = %v, want 0", envelope.Data["cost_micros_usd"])
	}
	for _, key := range []string{"run", "steps", "artifacts"} {
		if _, ok := envelope.Data[key]; ok {
			t.Fatalf("Data unexpectedly contains %s", key)
		}
	}
}
