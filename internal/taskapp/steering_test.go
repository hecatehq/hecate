package taskapp

import (
	"context"
	"errors"
	"github.com/hecatehq/hecate/internal/taskruncoord"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
	"testing"
)

func TestSteerRunRespectsOriginClosureAndCancelledAdmission(t *testing.T) {
	store := taskstate.NewMemoryStore()
	gate := taskruncoord.NewOriginGate()
	task, err := store.CreateTask(t.Context(), types.Task{ID: "task", ExecutionKind: "agent_loop", OriginKind: "chat_session", OriginID: "chat", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRun(t.Context(), types.TaskRun{ID: "run", TaskID: task.ID, Status: "running"}); err != nil {
		t.Fatal(err)
	}
	app := New(Options{Store: store, OriginRunGate: gate})
	cmd := SteerRunCommand{RunID: "run", ClientRequestID: "key", Content: "Inspect first"}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := app.SteerRun(ctx, task, cmd); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled admission=%v", err)
	}
	closure, err := gate.Close(t.Context(), taskruncoord.Origin{Kind: task.OriginKind, ID: task.OriginID})
	if err != nil {
		t.Fatal(err)
	}
	closure.Commit()
	if _, err := app.SteerRun(t.Context(), task, cmd); !errors.Is(err, taskruncoord.ErrOriginRunAdmissionClosed) {
		t.Fatalf("closed origin=%v", err)
	}
	artifacts, _ := store.ListArtifacts(t.Context(), taskstate.ArtifactFilter{TaskID: task.ID})
	if len(artifacts) != 0 {
		t.Fatal("rejected admission persisted guidance")
	}
}
