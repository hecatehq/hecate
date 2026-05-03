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
