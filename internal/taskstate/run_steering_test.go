package taskstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/runtimeevents"
	"github.com/hecatehq/hecate/pkg/types"
)

func runSteeringConformance(t *testing.T, factory StoreFactory) {
	setup := func(t *testing.T) (Store, RunSteeringStore, types.Task, types.TaskRun) {
		t.Helper()
		store := factory(t)
		task, err := store.CreateTask(context.Background(), types.Task{ID: "steering-task", ExecutionKind: "agent_loop", Status: "running"})
		if err != nil {
			t.Fatal(err)
		}
		run, err := store.CreateRun(context.Background(), types.TaskRun{ID: "steering-run", TaskID: task.ID, Status: "running"})
		if err != nil {
			t.Fatal(err)
		}
		return store, store.(RunSteeringStore), task, run
	}
	t.Run("idempotency and limits", func(t *testing.T) {
		store, guidance, task, run := setup(t)
		in := RunSteeringSubmission{TaskID: task.ID, RunID: run.ID, ClientRequestID: "request-1", Content: "Inspect before editing"}
		first, err := guidance.SubmitRunSteering(context.Background(), in)
		if err != nil || first.Replay || first.Status != "pending" {
			t.Fatalf("first=%+v err=%v", first, err)
		}
		second, err := guidance.SubmitRunSteering(context.Background(), in)
		if err != nil || !second.Replay || second.ID != first.ID {
			t.Fatalf("replay=%+v err=%v", second, err)
		}
		in.Content = "Different instruction"
		if _, err := guidance.SubmitRunSteering(context.Background(), in); !errors.Is(err, ErrSteeringRequestConflict) {
			t.Fatalf("conflict=%v", err)
		}
		for i := 1; i < maxPendingRunSteeringItems; i++ {
			in.ClientRequestID = fmt.Sprint(i)
			if _, err := guidance.SubmitRunSteering(context.Background(), in); err != nil {
				t.Fatal(err)
			}
		}
		in.ClientRequestID = "overflow"
		if _, err := guidance.SubmitRunSteering(context.Background(), in); !errors.Is(err, ErrSteeringFull) {
			t.Fatalf("limit=%v", err)
		}
		events, err := store.ListRunEvents(context.Background(), task.ID, run.ID, 0, 100)
		if err != nil || len(events) != maxPendingRunSteeringItems {
			t.Fatalf("events=%d err=%v", len(events), err)
		}
		for _, event := range events {
			data, _ := json.Marshal(event.Data)
			if strings.Contains(string(data), "instruction") && strings.Contains(string(data), in.Content) {
				t.Fatal("instruction text leaked into event")
			}
		}
	})
	t.Run("atomic checkpoint and applied replay", func(t *testing.T) {
		store, guidance, task, run := setup(t)
		in := RunSteeringSubmission{TaskID: task.ID, RunID: run.ID, ClientRequestID: "request", Content: "Use the existing helper"}
		first, err := guidance.SubmitRunSteering(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		checkpoint := types.TaskArtifact{ID: "convo-" + run.ID, TaskID: task.ID, RunID: run.ID, Kind: "agent_conversation", Status: "ready", ContentText: `[{"Role":"user","Content":"Use the existing helper"}]`, CreatedAt: time.Now().UTC()}
		apply := RunSteeringApplication{TaskID: task.ID, RunID: run.ID, IDs: []string{first.ID}, Conversation: checkpoint, AppliedAt: time.Now().UTC(), ModelCallIndex: 2}
		apply.ExpectedConversationDigest = "wrong"
		if _, err := guidance.ApplyRunSteering(context.Background(), apply); !errors.Is(err, ErrSteeringCheckpointConflict) {
			t.Fatalf("checkpoint conflict=%v", err)
		}
		if _, found, _ := store.GetArtifact(context.Background(), task.ID, checkpoint.ID); found {
			t.Fatal("failed apply wrote checkpoint")
		}
		apply.ExpectedConversationDigest = ""
		if applied, err := guidance.ApplyRunSteering(context.Background(), apply); err != nil || !applied {
			t.Fatalf("apply=%v %v", applied, err)
		}
		replay, err := guidance.SubmitRunSteering(context.Background(), in)
		if err != nil || replay.Status != "applied" || !replay.Replay || replay.AppliedAt == nil {
			t.Fatalf("replay=%+v err=%v", replay, err)
		}
		if _, err := guidance.ApplyRunSteering(context.Background(), apply); !errors.Is(err, ErrSteeringCheckpointConflict) {
			t.Fatalf("duplicate apply=%v", err)
		}
		events, _ := store.ListRunEvents(context.Background(), task.ID, run.ID, 0, 100)
		if len(events) != 2 || events[1].EventType != runtimeevents.EventRunSteeringApplied.String() {
			t.Fatalf("events=%+v", events)
		}
	})
	for _, status := range []string{"completed", "failed", "cancelled", "awaiting_approval"} {
		t.Run("settle "+status, func(t *testing.T) {
			store, guidance, task, run := setup(t)
			in := RunSteeringSubmission{TaskID: task.ID, RunID: run.ID, ClientRequestID: "request", Content: "Please stop this direction"}
			if _, err := guidance.SubmitRunSteering(context.Background(), in); err != nil {
				t.Fatal(err)
			}
			task.Status, run.Status = status, status
			if _, err := store.ApplyRunTerminalTransition(context.Background(), TerminalRunTransition{Task: task, Run: run, FinishedAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			replay, err := guidance.SubmitRunSteering(context.Background(), in)
			if err != nil || replay.Status != "not_applied" || replay.Reason != status {
				t.Fatalf("replay=%+v err=%v", replay, err)
			}
			in.ClientRequestID = "new"
			if _, err := guidance.SubmitRunSteering(context.Background(), in); !errors.Is(err, ErrSteeringUnavailable) {
				t.Fatalf("terminal admission=%v", err)
			}
		})
	}
	t.Run("concurrent admission and terminal settlement", func(t *testing.T) {
		store, guidance, task, run := setup(t)
		in := RunSteeringSubmission{TaskID: task.ID, RunID: run.ID, ClientRequestID: "raced", Content: "New direction"}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := guidance.SubmitRunSteering(context.Background(), in)
			if err != nil && !errors.Is(err, ErrSteeringUnavailable) {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			task.Status, run.Status = "cancelled", "cancelled"
			_, err := store.ApplyRunTerminalTransition(context.Background(), TerminalRunTransition{Task: task, Run: run, FinishedAt: time.Now().UTC()})
			if err != nil {
				t.Error(err)
			}
		}()
		wg.Wait()
		artifacts, err := store.ListArtifacts(context.Background(), ArtifactFilter{TaskID: task.ID, RunID: run.ID, Kind: RunSteeringArtifactKind})
		if err != nil {
			t.Fatal(err)
		}
		for _, artifact := range artifacts {
			if artifact.Status == "pending" {
				t.Fatal("terminal run retained pending instruction")
			}
		}
	})
	t.Run("application racing terminal settlement is atomic", func(t *testing.T) {
		store, guidance, task, run := setup(t)
		first, err := guidance.SubmitRunSteering(t.Context(), RunSteeringSubmission{TaskID: task.ID, RunID: run.ID, ClientRequestID: "race-apply", Content: "Inspect first"})
		if err != nil {
			t.Fatal(err)
		}
		checkpoint := types.TaskArtifact{ID: "convo-" + run.ID, TaskID: task.ID, RunID: run.ID, Kind: "agent_conversation", Status: "ready", ContentText: `[{"role":"user","content":"Inspect first"}]`}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := guidance.ApplyRunSteering(t.Context(), RunSteeringApplication{TaskID: task.ID, RunID: run.ID, IDs: []string{first.ID}, Conversation: checkpoint, ModelCallIndex: 1, AppliedAt: time.Now().UTC()})
			if err != nil && !errors.Is(err, ErrSteeringUnavailable) {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			task.Status, run.Status = "cancelled", "cancelled"
			_, err := store.ApplyRunTerminalTransition(t.Context(), TerminalRunTransition{Task: task, Run: run, FinishedAt: time.Now().UTC()})
			if err != nil {
				t.Error(err)
			}
		}()
		wg.Wait()
		artifact, found, err := store.GetArtifact(t.Context(), task.ID, first.ID)
		if err != nil || !found || artifact.Status == "pending" {
			t.Fatalf("guidance=%+v err=%v", artifact, err)
		}
		_, exists, err := store.GetArtifact(t.Context(), task.ID, checkpoint.ID)
		if err != nil || exists != (artifact.Status == "applied") {
			t.Fatalf("checkpoint exists=%t status=%s err=%v", exists, artifact.Status, err)
		}
	})
	t.Run("malformed settlement leaves the run unchanged", func(t *testing.T) {
		store, _, task, run := setup(t)
		if _, err := store.CreateArtifact(t.Context(), types.TaskArtifact{ID: "broken-guidance", TaskID: task.ID, RunID: run.ID, Kind: RunSteeringArtifactKind, Status: "pending", ContentText: "{"}); err != nil {
			t.Fatal(err)
		}
		task.Status, run.Status = "cancelled", "cancelled"
		if _, err := store.ApplyRunTerminalTransition(t.Context(), TerminalRunTransition{Task: task, Run: run, FinishedAt: time.Now().UTC()}); err == nil {
			t.Fatal("malformed guidance settlement accepted")
		}
		stored, found, err := store.GetRun(t.Context(), task.ID, run.ID)
		if err != nil || !found || stored.Status != "running" {
			t.Fatalf("partially settled run=%+v err=%v", stored, err)
		}
	})
	t.Run("applied instructions retain the per-run bound", func(t *testing.T) {
		_, guidance, task, run := setup(t)
		checkpoint := types.TaskArtifact{ID: "convo-" + run.ID, TaskID: task.ID, RunID: run.ID, Kind: "agent_conversation", Status: "ready", ContentText: "[]"}
		expected := ""
		for i := 0; i < maxRunSteeringItems; i++ {
			item, err := guidance.SubmitRunSteering(t.Context(), RunSteeringSubmission{TaskID: task.ID, RunID: run.ID, ClientRequestID: fmt.Sprint(i), Content: "Guidance"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := guidance.ApplyRunSteering(t.Context(), RunSteeringApplication{TaskID: task.ID, RunID: run.ID, IDs: []string{item.ID}, ExpectedConversationDigest: expected, Conversation: checkpoint, ModelCallIndex: i + 1, AppliedAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			expected = ConversationDigest(checkpoint.ContentText)
		}
		if _, err := guidance.SubmitRunSteering(t.Context(), RunSteeringSubmission{TaskID: task.ID, RunID: run.ID, ClientRequestID: "overflow", Content: "Guidance"}); !errors.Is(err, ErrSteeringFull) {
			t.Fatalf("per-run bound=%v", err)
		}
	})
}

func TestRunSteeringValidation(t *testing.T) {
	for _, in := range []RunSteeringSubmission{
		{TaskID: "t", RunID: "r", Content: "text"},
		{TaskID: "t", RunID: "r", ClientRequestID: "bad\nkey", Content: "text"},
		{TaskID: "t", RunID: "r", ClientRequestID: "key", Content: " "},
		{TaskID: "t", RunID: "r", ClientRequestID: "key", Content: strings.Repeat("a", MaxRunSteeringContentBytes+1)},
	} {
		if err := ValidateRunSteeringSubmission(in); err == nil {
			t.Fatalf("accepted invalid input %+v", in)
		}
	}
}
