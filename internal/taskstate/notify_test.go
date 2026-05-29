package taskstate

import (
	"context"
	"testing"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

func expectWake(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected a wake signal, got none")
	}
}

func expectNoWake(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("expected no wake signal, but received one")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRunEventBusSignalsSubscriber(t *testing.T) {
	var bus runEventBus
	ch, unsubscribe := bus.SubscribeRun("run-1")
	defer unsubscribe()

	bus.signalRun("run-1")
	expectWake(t, ch)
}

func TestRunEventBusCoalescesAndRecovers(t *testing.T) {
	var bus runEventBus
	ch, unsubscribe := bus.SubscribeRun("run-1")
	defer unsubscribe()

	// Two signals before a read collapse to one buffered wake — the
	// reader re-reads the store either way, so the duplicate is noise.
	bus.signalRun("run-1")
	bus.signalRun("run-1")
	expectWake(t, ch)
	expectNoWake(t, ch)

	// The channel is reusable after draining.
	bus.signalRun("run-1")
	expectWake(t, ch)
}

func TestRunEventBusScopesByRun(t *testing.T) {
	var bus runEventBus
	ch, unsubscribe := bus.SubscribeRun("run-1")
	defer unsubscribe()

	bus.signalRun("run-2")
	expectNoWake(t, ch)

	bus.signalRun("") // empty run id is a no-op, must not panic
	expectNoWake(t, ch)
}

func TestRunEventBusUnsubscribeStopsDelivery(t *testing.T) {
	var bus runEventBus
	ch, unsubscribe := bus.SubscribeRun("run-1")
	unsubscribe()

	// Signalling a removed subscriber must neither deliver nor panic.
	bus.signalRun("run-1")
	expectNoWake(t, ch)
}

func TestRunEventBusFansOutToMultipleSubscribers(t *testing.T) {
	var bus runEventBus
	a, unsubA := bus.SubscribeRun("run-1")
	defer unsubA()
	b, unsubB := bus.SubscribeRun("run-1")
	defer unsubB()

	bus.signalRun("run-1")
	expectWake(t, a)
	expectWake(t, b)
}

// TestMemoryStoreWakesOnMutations is the contract the SSE handler
// relies on: every run-scoped mutation reaches a subscriber, not just
// appended events. Steps, artifacts, run-status changes, and approvals
// all stream live, so the stream can drop its poll loop.
func TestMemoryStoreWakesOnMutations(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	const taskID, runID = "task-1", "run-1"

	ch, unsubscribe := store.SubscribeRun(runID)
	defer unsubscribe()

	mutations := []struct {
		name string
		run  func() error
	}{
		{"CreateRun", func() error {
			_, err := store.CreateRun(ctx, types.TaskRun{ID: runID, TaskID: taskID, Status: "running"})
			return err
		}},
		{"UpdateRun", func() error {
			_, err := store.UpdateRun(ctx, types.TaskRun{ID: runID, TaskID: taskID, Status: "succeeded"})
			return err
		}},
		{"AppendStep", func() error {
			_, err := store.AppendStep(ctx, types.TaskStep{ID: "step-1", TaskID: taskID, RunID: runID, Status: "running"})
			return err
		}},
		{"UpdateStep", func() error {
			_, err := store.UpdateStep(ctx, types.TaskStep{ID: "step-1", TaskID: taskID, RunID: runID, Status: "completed"})
			return err
		}},
		{"CreateArtifact", func() error {
			_, err := store.CreateArtifact(ctx, types.TaskArtifact{ID: "art-1", TaskID: taskID, RunID: runID, Kind: "git_summary"})
			return err
		}},
		{"CreateApproval", func() error {
			_, err := store.CreateApproval(ctx, types.TaskApproval{ID: "ap-1", TaskID: taskID, RunID: runID, Status: "pending"})
			return err
		}},
		{"AppendRunEvent", func() error {
			_, err := store.AppendRunEvent(ctx, types.TaskRunEvent{TaskID: taskID, RunID: runID, EventType: "turn.completed"})
			return err
		}},
	}

	for _, m := range mutations {
		// Drain so each mutation is observed on its own merits rather
		// than reading a wake left over from a prior step.
		select {
		case <-ch:
		default:
		}
		if err := m.run(); err != nil {
			t.Fatalf("%s: %v", m.name, err)
		}
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("%s: expected a wake signal, got none", m.name)
		}
	}
}
