package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestTaskRunSteeringHTTP(t *testing.T) {
	store := taskstate.NewMemoryStore()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := newTestAPIHandlerWithSettingsAndTaskStore(logger, nil, config.Config{}, nil, store)
	client := newTaskTestClient(t, NewServer(logger, handler))
	task, err := store.CreateTask(t.Context(), types.Task{ID: "guidance-task", ExecutionKind: "agent_loop", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(t.Context(), types.TaskRun{ID: "guidance-run", TaskID: task.ID, Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/hecate/v1/tasks/" + task.ID + "/runs/" + run.ID + "/steer"
	body := `{"client_request_id":"first","content":"Check tests first"}`
	response := client.mustRequestStatus(http.StatusOK, http.MethodPost, path, body)
	var first struct {
		Data taskstate.RunSteeringResult `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Data.Status != "pending" || first.Data.Replay {
		t.Fatalf("first=%+v", first)
	}
	replay := client.mustRequestStatus(http.StatusOK, http.MethodPost, path, body)
	if !strings.Contains(replay.Body.String(), `"replay":true`) {
		t.Fatal(replay.Body.String())
	}
	conflict := client.mustRequestStatus(http.StatusConflict, http.MethodPost, path, `{"client_request_id":"first","content":"Different"}`)
	if !strings.Contains(conflict.Body.String(), "task.steering_request_conflict") {
		t.Fatal(conflict.Body.String())
	}
	for _, invalid := range []string{`{}`, `null`, `{"content":"text","client_request_id":"key","files":[]}`, body + body, `{"client_request_id":"bad key","content":"text"}`, `{"client_request_id":"key","content":" "}`} {
		client.mustRequestStatus(http.StatusBadRequest, http.MethodPost, path, invalid)
	}
	artifacts := mustSteeringArtifacts(t, store, task.ID, run.ID)
	rendered := make([]TaskArtifactItem, 0, len(artifacts))
	for _, artifact := range artifacts {
		rendered = append(rendered, renderTaskArtifact(artifact))
	}
	items := buildTaskActivityItems(nil, rendered, nil, run)
	found := false
	for _, item := range items {
		if item.Type == "run_steering" {
			found = true
			if item.Terminal || item.Title != "Guidance waiting" || item.Summary["content_preview"] != "Check tests first" {
				t.Fatalf("item=%+v", item)
			}
		}
	}
	if !found {
		t.Fatal("guidance missing from durable activity")
	}
	task.Status, run.Status = "awaiting_approval", "awaiting_approval"
	if _, err := store.ApplyRunTerminalTransition(t.Context(), taskstate.TerminalRunTransition{Task: task, Run: run}); err != nil {
		t.Fatal(err)
	}
	settled := client.mustRequestStatus(http.StatusOK, http.MethodPost, path, body)
	if !strings.Contains(settled.Body.String(), `"status":"not_applied"`) {
		t.Fatal(settled.Body.String())
	}
	client.mustRequestStatus(http.StatusConflict, http.MethodPost, path, `{"client_request_id":"next","content":"Please continue"}`)
	client.mustRequestStatus(http.StatusNotFound, http.MethodPost, "/hecate/v1/tasks/"+task.ID+"/runs/wrong-run/steer", body)
}

func mustSteeringArtifacts(t *testing.T, store taskstate.Store, taskID, runID string) []types.TaskArtifact {
	t.Helper()
	artifacts, err := store.ListArtifacts(t.Context(), taskstate.ArtifactFilter{TaskID: taskID, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	return artifacts
}
