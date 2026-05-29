package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/eventprotocol"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
	"github.com/hecatehq/hecate/pkg/types"
)

// runStreamSubscriber is the optional store capability that lets the
// run stream wake on mutations instead of polling. Both production
// stores (memory, sqlite) implement it; type-asserting here keeps the
// taskstate.Store interface free of this streaming concern and lets a
// bare test fake fall back to the short poll below.
type runStreamSubscriber interface {
	SubscribeRun(runID string) (<-chan struct{}, func())
}

func (h *Handler) HandleTaskRunStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.taskStore == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task store is not configured")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "streaming not supported by server")
		return
	}
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}
	run, ok := h.loadAuthorizedTaskRun(ctx, w, r, task)
	if !ok {
		return
	}
	runID := run.ID

	writeSSEHeaders(w)

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	// Wake on store mutations rather than busy-polling. Subscribing
	// before the first read closes the lost-wakeup gap: any change
	// between a read and the select below leaves a pending signal on
	// the buffered channel, so the next select returns immediately. The
	// store stays the source of truth — every wake triggers a full
	// re-read, so a coalesced or duplicate signal is harmless. The 15s
	// heartbeat doubles as a safety re-poll. A store without the
	// capability (a bare test fake) falls back to the original 50ms
	// poll so its behavior is unchanged.
	var wake <-chan struct{}
	var pollC <-chan time.Time
	if sub, ok := h.taskStore.(runStreamSubscriber); ok {
		ch, unsubscribe := sub.SubscribeRun(runID)
		defer unsubscribe()
		wake = ch
	} else {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		pollC = ticker.C
	}

	afterSequence := parseAfterSequence(r)
	lastStateJSON := ""
	for {
		events, err := h.taskStore.ListRunEvents(ctx, task.ID, runID, afterSequence, 200)
		if err != nil {
			fmt.Fprintf(w, "event: error\ndata: {\"error\":{\"message\":%q}}\n\n", err.Error())
			flusher.Flush()
			return
		}
		for _, event := range events {
			state, ok, err := h.decodeTaskRunEventData(event)
			if err != nil {
				fmt.Fprintf(w, "event: error\ndata: {\"error\":{\"message\":%q}}\n\n", err.Error())
				flusher.Flush()
				return
			}
			if !ok {
				// Some events carry an overlay (e.g. turn.completed
				// passes a Turn cost block) but no full snapshot. Save
				// the overlay across the rebuild so it survives.
				overlayTurn := state.Turn
				state, err = h.buildTaskRunStreamState(ctx, task.ID, runID)
				if err != nil {
					fmt.Fprintf(w, "event: error\ndata: {\"error\":{\"message\":%q}}\n\n", err.Error())
					flusher.Flush()
					return
				}
				state.Turn = overlayTurn
			} else if state.Approvals == nil {
				// Historical snapshots saved before approvals were
				// included in the SSE payload have a nil Approvals
				// slice. Top up from the live store so the UI doesn't
				// see "no approvals" and clear its banner. Without
				// this, replaying an old run would briefly show then
				// hide the approval card on every reconnect.
				if live, liveErr := h.buildTaskRunStreamState(ctx, task.ID, runID); liveErr == nil {
					state.Approvals = live.Approvals
				}
			}
			state.Sequence = int(event.Sequence)
			state.EventType = event.EventType
			state.Terminal = types.IsTerminalTaskRunStatus(state.Run.Status)

			payload, err := json.Marshal(TaskRunStreamEventResponse{
				Object: "task_run_stream_event",
				Data:   state,
			})
			if err != nil {
				fmt.Fprintf(w, "event: error\ndata: {\"error\":{\"message\":%q}}\n\n", err.Error())
				flusher.Flush()
				return
			}

			fmt.Fprintf(w, "id: %d\nevent: snapshot\ndata: %s\n\n", event.Sequence, payload)
			if state.Terminal {
				finalState, err := h.buildTaskRunStreamState(ctx, task.ID, runID)
				if err == nil {
					finalState.Sequence = int(event.Sequence)
					finalState.EventType = state.EventType
					finalState.Terminal = true
					if finalPayload, marshalErr := json.Marshal(TaskRunStreamEventResponse{Object: "task_run_stream_event", Data: finalState}); marshalErr == nil {
						payload = finalPayload
						fmt.Fprintf(w, "id: %d\nevent: snapshot\ndata: %s\n\n", event.Sequence, payload)
					}
				}
				fmt.Fprintf(w, "id: %d\nevent: done\ndata: %s\n\n", event.Sequence, payload)
			}
			flusher.Flush()
			afterSequence = event.Sequence
			if stateJSON, marshalErr := json.Marshal(state); marshalErr == nil {
				lastStateJSON = string(stateJSON)
			}
			if state.Terminal {
				return
			}
		}

		state, err := h.buildTaskRunStreamState(ctx, task.ID, runID)
		if err != nil {
			fmt.Fprintf(w, "event: error\ndata: {\"error\":{\"message\":%q}}\n\n", err.Error())
			flusher.Flush()
			return
		}
		stateJSON, err := json.Marshal(state)
		if err != nil {
			fmt.Fprintf(w, "event: error\ndata: {\"error\":{\"message\":%q}}\n\n", err.Error())
			flusher.Flush()
			return
		}
		if string(stateJSON) != lastStateJSON {
			var snapshot map[string]any
			_ = json.Unmarshal(stateJSON, &snapshot)
			event, appendErr := h.taskStore.AppendRunEvent(ctx, types.TaskRunEvent{
				TaskID:    task.ID,
				RunID:     runID,
				EventType: "snapshot",
				Data:      map[string]any{"snapshot": snapshot},
				RequestID: state.Run.RequestID,
				TraceID:   state.Run.TraceID,
				CreatedAt: time.Now().UTC(),
			})
			if appendErr == nil {
				afterSequence = event.Sequence
				state.Sequence = int(event.Sequence)
			}
			state.EventType = "snapshot"
			state.Terminal = types.IsTerminalTaskRunStatus(state.Run.Status)
			payload, marshalErr := json.Marshal(TaskRunStreamEventResponse{Object: "task_run_stream_event", Data: state})
			if marshalErr != nil {
				fmt.Fprintf(w, "event: error\ndata: {\"error\":{\"message\":%q}}\n\n", marshalErr.Error())
				flusher.Flush()
				return
			}
			fmt.Fprintf(w, "id: %d\nevent: snapshot\ndata: %s\n\n", state.Sequence, payload)
			if state.Terminal {
				fmt.Fprintf(w, "id: %d\nevent: done\ndata: %s\n\n", state.Sequence, payload)
			}
			flusher.Flush()
			lastStateJSON = string(stateJSON)
			if state.Terminal {
				return
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-pollC:
		case <-heartbeat.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}

func (h *Handler) HandleTaskRunEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}
	run, ok := h.loadAuthorizedTaskRun(ctx, w, r, task)
	if !ok {
		return
	}
	afterSequence := parseAfterSequence(r)
	events, err := h.taskStore.ListRunEvents(ctx, task.ID, run.ID, afterSequence, 500)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, TaskRunEventsResponse{
		Object: "task_run_events",
		Data:   eventprotocol.FromTaskRunEvents(events),
	})
}

func (h *Handler) HandleAppendTaskRunEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	task, ok := h.loadAuthorizedTask(ctx, w, r)
	if !ok {
		return
	}
	run, ok := h.loadAuthorizedTaskRun(ctx, w, r, task)
	if !ok {
		return
	}
	var req AppendTaskRunEventRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	eventType := strings.TrimSpace(req.Type)
	if eventType == "" {
		eventType = "external.event"
	}
	extra := map[string]any{}
	for key, value := range req.Data {
		extra[key] = value
	}
	if req.StepID != "" {
		extra["step_id"] = req.StepID
	}
	if req.Status != "" {
		extra["status"] = req.Status
	}
	if req.Note != "" {
		extra["note"] = req.Note
	}
	state, err := h.buildTaskRunStreamState(ctx, task.ID, run.ID)
	if err == nil {
		stateJSON, _ := json.Marshal(state)
		var snapshot map[string]any
		_ = json.Unmarshal(stateJSON, &snapshot)
		extra["snapshot"] = snapshot
	}
	event, err := h.taskStore.AppendRunEvent(ctx, types.TaskRunEvent{
		TaskID:    task.ID,
		RunID:     run.ID,
		EventType: eventType,
		Data:      extra,
		RequestID: RequestIDFromContext(ctx),
		TraceID:   telemetry.TraceIDsFromContext(ctx).TraceID,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "task_run_event",
		"data":   eventprotocol.FromTaskRunEvent(event),
	})
}

func parseAfterSequence(r *http.Request) int64 {
	raw := strings.TrimSpace(r.URL.Query().Get("after_sequence"))
	if raw == "" {
		raw = strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	}
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func (h *Handler) buildTaskRunStreamState(ctx context.Context, taskID, runID string) (TaskRunStreamEventData, error) {
	run, found, err := h.taskStore.GetRun(ctx, taskID, runID)
	if err != nil {
		return TaskRunStreamEventData{}, err
	}
	if !found {
		return TaskRunStreamEventData{}, fmt.Errorf("task run not found")
	}
	steps, err := h.taskStore.ListSteps(ctx, runID)
	if err != nil {
		return TaskRunStreamEventData{}, err
	}
	artifacts, err := h.taskStore.ListArtifacts(ctx, taskstate.ArtifactFilter{TaskID: taskID, RunID: runID})
	if err != nil {
		return TaskRunStreamEventData{}, err
	}
	// Approvals are listed per-task by the store; we filter to the
	// current run because the streamed state is run-scoped. A failure
	// here is non-fatal — emit the snapshot without approvals rather
	// than dropping the whole stream, so the run/steps view still
	// updates. The UI's separate /hecate/v1/tasks/{id}/approvals fetch acts
	// as a fallback for that edge case.
	taskApprovals, err := h.taskStore.ListApprovals(ctx, taskID)
	if err != nil {
		taskApprovals = nil
	}

	stepItems := make([]TaskStepItem, 0, len(steps))
	for _, step := range steps {
		stepItems = append(stepItems, renderTaskStep(step))
	}
	artifactItems := make([]TaskArtifactItem, 0, len(artifacts))
	for _, artifact := range artifacts {
		artifactItems = append(artifactItems, renderTaskArtifact(artifact))
	}
	approvalItems := make([]TaskApprovalItem, 0, len(taskApprovals))
	for _, approval := range taskApprovals {
		if approval.RunID != runID {
			continue
		}
		approvalItems = append(approvalItems, renderTaskApproval(approval))
	}
	activityItems := buildTaskActivityItems(stepItems, artifactItems, approvalItems, run)
	return TaskRunStreamEventData{
		Run:       renderTaskRun(run),
		Steps:     stepItems,
		Artifacts: artifactItems,
		Activity:  activityItems,
		Approvals: approvalItems,
	}, nil
}

func (h *Handler) decodeTaskRunEventData(event types.TaskRunEvent) (TaskRunStreamEventData, bool, error) {
	if event.Data == nil {
		return TaskRunStreamEventData{}, false, nil
	}
	// `turn.completed` is the per-turn cost telemetry the runner
	// emits; its payload is a flat map (no `snapshot` envelope). We
	// don't have enough state to fabricate a full snapshot here — the
	// caller falls through to buildTaskRunStreamState — but we DO want
	// to attach the per-turn breakdown so the UI can render a live
	// cost-per-turn summary without subscribing to /hecate/v1/events.
	if event.EventType == "turn.completed" {
		turn := decodeTurnCostFromEventData(event.Data)
		return TaskRunStreamEventData{Turn: turn}, false, nil
	}
	snapshot, ok := event.Data["snapshot"]
	if ok {
		raw, err := json.Marshal(snapshot)
		if err != nil {
			return TaskRunStreamEventData{}, false, err
		}
		var decoded TaskRunStreamEventData
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return TaskRunStreamEventData{}, false, err
		}
		return decoded, true, nil
	}
	raw, err := json.Marshal(event.Data)
	if err != nil {
		return TaskRunStreamEventData{}, false, err
	}
	var typed struct {
		Run       types.TaskRun        `json:"run"`
		Steps     []types.TaskStep     `json:"steps"`
		Artifacts []types.TaskArtifact `json:"artifacts"`
	}
	if err := json.Unmarshal(raw, &typed); err == nil && typed.Run.ID != "" {
		stepItems := make([]TaskStepItem, 0, len(typed.Steps))
		for _, step := range typed.Steps {
			stepItems = append(stepItems, renderTaskStep(step))
		}
		artifactItems := make([]TaskArtifactItem, 0, len(typed.Artifacts))
		for _, artifact := range typed.Artifacts {
			artifactItems = append(artifactItems, renderTaskArtifact(artifact))
		}
		return TaskRunStreamEventData{
			Run:       renderTaskRun(typed.Run),
			Steps:     stepItems,
			Artifacts: artifactItems,
		}, true, nil
	}
	var decoded TaskRunStreamEventData
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return TaskRunStreamEventData{}, false, nil
	}
	if decoded.Run.ID == "" {
		return TaskRunStreamEventData{}, false, nil
	}
	return decoded, true, nil
}

// decodeTurnCostFromEventData lifts the per-turn cost figures out of
// the turn.completed event payload. The runner writes them as
// a flat map; we pull the keys defensively (event.Data is map[string]any
// after a JSON round-trip, so numerics arrive as float64).
func decodeTurnCostFromEventData(data map[string]any) *TaskRunStreamTurnCost {
	if data == nil {
		return nil
	}
	asInt := func(v any) int {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case int64:
			return int(n)
		}
		return 0
	}
	asInt64 := func(v any) int64 {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int:
			return int64(n)
		case int64:
			return n
		}
		return 0
	}
	asString := func(v any) string {
		if s, ok := v.(string); ok {
			return s
		}
		return ""
	}
	return &TaskRunStreamTurnCost{
		Turn:                    asInt(data["turn_index"]),
		StepID:                  asString(data["step_id"]),
		CostMicrosUSD:           asInt64(data["cost_micros_usd"]),
		RunCumulativeMicrosUSD:  asInt64(data["run_cumulative_cost_micros_usd"]),
		TaskCumulativeMicrosUSD: asInt64(data["task_cumulative_cost_micros_usd"]),
		ToolCallCount:           asInt(data["tool_calls"]),
	}
}

func (h *Handler) loadAuthorizedTask(ctx context.Context, w http.ResponseWriter, r *http.Request) (types.Task, bool) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task id is required")
		return types.Task{}, false
	}

	task, found, err := h.taskStore.GetTask(ctx, id)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.tasks.load.failed",
			slog.String("event.name", "gateway.tasks.load.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return types.Task{}, false
	}
	if !found {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "task not found")
		return types.Task{}, false
	}
	return task, true
}

func (h *Handler) loadAuthorizedTaskRun(ctx context.Context, w http.ResponseWriter, r *http.Request, task types.Task) (types.TaskRun, bool) {
	runID := strings.TrimSpace(r.PathValue("run_id"))
	if runID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "run id is required")
		return types.TaskRun{}, false
	}

	run, found, err := h.taskStore.GetRun(ctx, task.ID, runID)
	if err != nil {
		telemetry.Error(h.logger, ctx, "gateway.tasks.runs.load.failed",
			slog.String("event.name", "gateway.tasks.runs.load.failed"),
			slog.Any("error", err),
		)
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return types.TaskRun{}, false
	}
	if !found {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "task run not found")
		return types.TaskRun{}, false
	}
	return run, true
}

func writeSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
}
