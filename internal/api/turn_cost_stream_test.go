package api

import (
	"testing"

	"github.com/hecate/agent-runtime/pkg/types"
)

// TestDecodeTurnCostFromEventData_LiftsAllFields confirms the helper
// pulls every documented key out of the agent.turn.completed event
// payload, including JSON-roundtrip floats (numerics arrive as
// float64 after json.Unmarshal into map[string]any).
func TestDecodeTurnCostFromEventData_LiftsAllFields(t *testing.T) {
	t.Parallel()
	got := decodeTurnCostFromEventData(map[string]any{
		"turn":                            float64(2),
		"step_id":                         "step-xyz",
		"cost_micros_usd":                 float64(1500),
		"run_cumulative_cost_micros_usd":  float64(3000),
		"task_cumulative_cost_micros_usd": float64(8000),
		"tool_call_count":                 float64(3),
	})
	if got == nil {
		t.Fatal("decodeTurnCostFromEventData returned nil")
	}
	if got.Turn != 2 {
		t.Errorf("Turn = %d, want 2", got.Turn)
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

// TestDecodeTurnCostFromEventData_NilDataReturnsNil — defensive: the
// caller may hand us an event with no payload (legacy rows, future
// schema drift). We should not panic.
func TestDecodeTurnCostFromEventData_NilDataReturnsNil(t *testing.T) {
	t.Parallel()
	if got := decodeTurnCostFromEventData(nil); got != nil {
		t.Fatalf("decodeTurnCostFromEventData(nil) = %+v, want nil", got)
	}
}

// TestDecodeTurnCostFromEventData_TolerantToInts — when numerics
// arrive as native ints (in-process callers, not after a JSON
// round-trip), the helper should still extract them.
func TestDecodeTurnCostFromEventData_TolerantToInts(t *testing.T) {
	t.Parallel()
	got := decodeTurnCostFromEventData(map[string]any{
		"turn":            int(1),
		"cost_micros_usd": int64(500),
		"tool_call_count": int(0),
	})
	if got == nil {
		t.Fatal("decodeTurnCostFromEventData returned nil")
	}
	if got.Turn != 1 {
		t.Errorf("Turn = %d, want 1", got.Turn)
	}
	if got.CostMicrosUSD != 500 {
		t.Errorf("CostMicrosUSD = %d, want 500", got.CostMicrosUSD)
	}
}

// TestDecodeTaskRunEventData_AgentTurnCompletedReturnsTurnOverlay
// verifies the decoder treats agent.turn.completed as a Turn-only
// overlay (ok=false so the streaming handler rebuilds full state)
// while still populating Turn so the overlay can be merged after.
func TestDecodeTaskRunEventData_AgentTurnCompletedReturnsTurnOverlay(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	event := types.TaskRunEvent{
		EventType: "agent.turn.completed",
		Data: map[string]any{
			"turn":                            float64(1),
			"cost_micros_usd":                 float64(1234),
			"run_cumulative_cost_micros_usd":  float64(1234),
			"task_cumulative_cost_micros_usd": float64(5678),
			"tool_call_count":                 float64(2),
			"step_id":                         "step-1",
		},
	}
	state, ok, err := h.decodeTaskRunEventData(event)
	if err != nil {
		t.Fatalf("decodeTaskRunEventData error = %v", err)
	}
	if ok {
		// `ok=false` is intentional — agent.turn.completed payloads
		// don't carry a full snapshot; the streaming handler treats
		// false as "rebuild from store, then merge overlay".
		t.Errorf("decodeTaskRunEventData(agent.turn.completed) ok = true, want false (overlay-only)")
	}
	if state.Turn == nil {
		t.Fatal("state.Turn is nil — overlay was not populated")
	}
	if state.Turn.CostMicrosUSD != 1234 {
		t.Errorf("Turn.CostMicrosUSD = %d, want 1234", state.Turn.CostMicrosUSD)
	}
	if state.Turn.TaskCumulativeMicrosUSD != 5678 {
		t.Errorf("Turn.TaskCumulativeMicrosUSD = %d, want 5678", state.Turn.TaskCumulativeMicrosUSD)
	}
	if state.Turn.StepID != "step-1" {
		t.Errorf("Turn.StepID = %q, want step-1", state.Turn.StepID)
	}
	// All other event fields should be zero — Turn is the only thing
	// we populated; the streaming handler fills the rest from the
	// live store.
	if state.Run.ID != "" {
		t.Errorf("state.Run.ID = %q, want empty (overlay shouldn't touch run)", state.Run.ID)
	}
}

// TestDecodeTaskRunEventData_OtherEventsUnaffected confirms the new
// agent.turn.completed branch doesn't accidentally short-circuit
// other event types — the existing snapshot-decode path stays.
func TestDecodeTaskRunEventData_OtherEventsUnaffected(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	// Snapshot-shaped event (the legacy path).
	event := types.TaskRunEvent{
		EventType: "run.started",
		Data: map[string]any{
			"snapshot": map[string]any{
				"run": map[string]any{"id": "run-A", "task_id": "task-A"},
			},
		},
	}
	state, ok, err := h.decodeTaskRunEventData(event)
	if err != nil {
		t.Fatalf("decodeTaskRunEventData error = %v", err)
	}
	if !ok {
		t.Fatal("decodeTaskRunEventData(run.started) ok = false, want true")
	}
	if state.Run.ID != "run-A" {
		t.Errorf("state.Run.ID = %q, want run-A", state.Run.ID)
	}
	if state.Turn != nil {
		t.Errorf("state.Turn = %+v, want nil for non-turn events", state.Turn)
	}
}
