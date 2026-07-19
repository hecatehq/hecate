package api

import (
	"testing"

	"github.com/hecatehq/hecate/pkg/types"
)

// TestDecodeModelCallCostFromEventData_LiftsAllFields confirms the helper
// pulls every documented key out of the model.call.completed event
// payload, including JSON-roundtrip floats (numerics arrive as
// float64 after json.Unmarshal into map[string]any).
func TestDecodeModelCallCostFromEventData_LiftsAllFields(t *testing.T) {
	t.Parallel()
	got := decodeModelCallCostFromEventData(map[string]any{
		"model_call_index":                float64(2),
		"step_id":                         "step-xyz",
		"cost_micros_usd":                 float64(1500),
		"run_cumulative_cost_micros_usd":  float64(3000),
		"task_cumulative_cost_micros_usd": float64(8000),
		"tool_calls":                      float64(3),
	})
	if got == nil {
		t.Fatal("decodeModelCallCostFromEventData returned nil")
	}
	if got.ModelCall != 2 {
		t.Errorf("ModelCall = %d, want 2", got.ModelCall)
	}
	if got.StepID != "step-xyz" {
		t.Errorf("StepID = %q, want step-xyz", got.StepID)
	}
	if got.CostMicrosUSD != 1500 {
		t.Errorf("CostMicrosUSD = %d, want 1500", got.CostMicrosUSD)
	}
	if got.RunCumulativeMicrosUSD != 3000 {
		t.Errorf("RunCumulativeMicrosUSD = %d, want 3000", got.RunCumulativeMicrosUSD)
	}
	if got.TaskCumulativeMicrosUSD != 8000 {
		t.Errorf("TaskCumulativeMicrosUSD = %d, want 8000", got.TaskCumulativeMicrosUSD)
	}
	if got.ToolCallCount != 3 {
		t.Errorf("ToolCallCount = %d, want 3", got.ToolCallCount)
	}
}

// TestDecodeModelCallCostFromEventData_NilDataReturnsNil — defensive: the
// caller may hand us an event with no payload (legacy rows, future
// schema drift). We should not panic.
func TestDecodeModelCallCostFromEventData_NilDataReturnsNil(t *testing.T) {
	t.Parallel()
	if got := decodeModelCallCostFromEventData(nil); got != nil {
		t.Fatalf("decodeModelCallCostFromEventData(nil) = %+v, want nil", got)
	}
}

// TestDecodeModelCallCostFromEventData_TolerantToInts — when numerics
// arrive as native ints (in-process callers, not after a JSON
// round-trip), the helper should still extract them.
func TestDecodeModelCallCostFromEventData_TolerantToInts(t *testing.T) {
	t.Parallel()
	got := decodeModelCallCostFromEventData(map[string]any{
		"model_call_index": int(1),
		"cost_micros_usd":  int64(500),
		"tool_calls":       int(0),
	})
	if got == nil {
		t.Fatal("decodeModelCallCostFromEventData returned nil")
	}
	if got.ModelCall != 1 {
		t.Errorf("ModelCall = %d, want 1", got.ModelCall)
	}
	if got.CostMicrosUSD != 500 {
		t.Errorf("CostMicrosUSD = %d, want 500", got.CostMicrosUSD)
	}
}

// TestTaskRunStreamProjector_DecodeModelCallCompletedReturnsModelCallOverlay
// verifies the projector decoder treats model.call.completed as a ModelCall-only
// overlay (ok=false so projection rebuilds full state) while still
// populating ModelCall so the overlay can be merged after.
func TestTaskRunStreamProjector_DecodeModelCallCompletedReturnsModelCallOverlay(t *testing.T) {
	t.Parallel()
	projector := newTaskRunStreamProjector(nil)
	event := types.TaskRunEvent{
		EventType: "model.call.completed",
		Data: map[string]any{
			"model_call_index":                float64(1),
			"cost_micros_usd":                 float64(1234),
			"run_cumulative_cost_micros_usd":  float64(1234),
			"task_cumulative_cost_micros_usd": float64(5678),
			"tool_calls":                      float64(2),
			"step_id":                         "step-1",
		},
	}
	state, ok, err := projector.decodeEventData(event)
	if err != nil {
		t.Fatalf("decodeEventData error = %v", err)
	}
	if ok {
		// `ok=false` is intentional — model.call.completed payloads
		// don't carry a full snapshot; the stream projector treats
		// false as "rebuild from store, then merge overlay".
		t.Errorf("decodeEventData(model.call.completed) ok = true, want false (overlay-only)")
	}
	if state.ModelCall == nil {
		t.Fatal("state.ModelCall is nil — overlay was not populated")
	}
	if state.ModelCall.CostMicrosUSD != 1234 {
		t.Errorf("ModelCall.CostMicrosUSD = %d, want 1234", state.ModelCall.CostMicrosUSD)
	}
	if state.ModelCall.TaskCumulativeMicrosUSD != 5678 {
		t.Errorf("ModelCall.TaskCumulativeMicrosUSD = %d, want 5678", state.ModelCall.TaskCumulativeMicrosUSD)
	}
	if state.ModelCall.StepID != "step-1" {
		t.Errorf("ModelCall.StepID = %q, want step-1", state.ModelCall.StepID)
	}
	// All other event fields should be zero — ModelCall is the only thing
	// we populated; the streaming handler fills the rest from the
	// live store.
	if state.Run.ID != "" {
		t.Errorf("state.Run.ID = %q, want empty (overlay shouldn't touch run)", state.Run.ID)
	}
}

// TestTaskRunStreamProjector_DecodeOtherEventsUnaffected confirms the new
// model.call.completed branch doesn't accidentally short-circuit
// other event types — the existing snapshot-decode path stays.
func TestTaskRunStreamProjector_DecodeOtherEventsUnaffected(t *testing.T) {
	t.Parallel()
	projector := newTaskRunStreamProjector(nil)
	// Snapshot-shaped event (the legacy path).
	event := types.TaskRunEvent{
		EventType: "run.started",
		Data: map[string]any{
			"snapshot": map[string]any{
				"run": map[string]any{"id": "run-A", "task_id": "task-A"},
			},
		},
	}
	state, ok, err := projector.decodeEventData(event)
	if err != nil {
		t.Fatalf("decodeEventData error = %v", err)
	}
	if !ok {
		t.Fatal("decodeEventData(run.started) ok = false, want true")
	}
	if state.Run.ID != "run-A" {
		t.Errorf("state.Run.ID = %q, want run-A", state.Run.ID)
	}
	if state.ModelCall != nil {
		t.Errorf("state.ModelCall = %+v, want nil for non-model-call events", state.ModelCall)
	}
}
