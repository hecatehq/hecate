package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hecatehq/hecate/internal/taskschedule"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/internal/telemetry"
)

const maxTaskScheduleTaskIDFilters = 200

func (h *Handler) HandlePutTaskSchedule(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(r.PathValue("id"))
	if taskID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task id is required")
		return
	}

	var req PutTaskScheduleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "enabled is required")
		return
	}

	var runAt time.Time
	if raw := strings.TrimSpace(req.RunAt); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "run_at must be an RFC3339 timestamp")
			return
		}
		runAt = parsed
	}

	schedule, err := h.taskscheduleApplication().Upsert(r.Context(), taskschedule.UpsertCommand{
		TaskID:         taskID,
		Kind:           req.Kind,
		CronExpression: req.CronExpression,
		Timezone:       req.Timezone,
		RunAt:          runAt,
		Enabled:        *req.Enabled,
	})
	if err != nil {
		h.writeTaskScheduleError(w, r, "gateway.task_schedules.put.failed", err)
		return
	}

	WriteJSON(w, http.StatusOK, TaskScheduleResponse{
		Object: "task_schedule",
		Data:   renderTaskSchedule(schedule),
	})
}

func (h *Handler) HandleTaskSchedule(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(r.PathValue("id"))
	if taskID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task id is required")
		return
	}

	schedule, err := h.taskscheduleApplication().GetForTask(r.Context(), taskID)
	if err != nil {
		h.writeTaskScheduleError(w, r, "gateway.task_schedules.get.failed", err)
		return
	}
	WriteJSON(w, http.StatusOK, TaskScheduleResponse{
		Object: "task_schedule",
		Data:   renderTaskSchedule(schedule),
	})
}

func (h *Handler) HandleDeleteTaskSchedule(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(r.PathValue("id"))
	if taskID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task id is required")
		return
	}

	if err := h.taskscheduleApplication().DeleteForTask(r.Context(), taskID); err != nil {
		h.writeTaskScheduleError(w, r, "gateway.task_schedules.delete.failed", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) HandleTaskSchedules(w http.ResponseWriter, r *http.Request) {
	filter := taskstate.TaskScheduleFilter{
		Limit: 100,
	}
	seenTaskIDs := make(map[string]struct{})
	for _, rawTaskID := range r.URL.Query()["task_id"] {
		taskID := strings.TrimSpace(rawTaskID)
		if taskID == "" {
			continue
		}
		if _, seen := seenTaskIDs[taskID]; seen {
			continue
		}
		if len(seenTaskIDs) == maxTaskScheduleTaskIDFilters {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task_id query parameter supports at most 200 unique values")
			return
		}
		seenTaskIDs[taskID] = struct{}{}
		filter.TaskIDs = append(filter.TaskIDs, taskID)
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, ok := parseTaskScheduleLimit(w, raw)
		if !ok {
			return
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("enabled")); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "enabled query parameter must be true or false")
			return
		}
		filter.Enabled = &enabled
	}

	schedules, err := h.taskscheduleApplication().List(r.Context(), filter)
	if err != nil {
		h.writeTaskScheduleError(w, r, "gateway.task_schedules.list.failed", err)
		return
	}
	items := make([]TaskScheduleItem, 0, len(schedules))
	for _, schedule := range schedules {
		items = append(items, renderTaskSchedule(schedule))
	}
	WriteJSON(w, http.StatusOK, TaskSchedulesResponse{
		Object: "task_schedules",
		Data:   items,
	})
}

func (h *Handler) HandleTaskScheduleOccurrences(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(r.PathValue("id"))
	if taskID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "task id is required")
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, ok := parseTaskScheduleLimit(w, raw)
		if !ok {
			return
		}
		limit = parsed
	}

	occurrences, err := h.taskscheduleApplication().ListOccurrencesForTask(r.Context(), taskID, limit)
	if err != nil {
		h.writeTaskScheduleError(w, r, "gateway.task_schedule_occurrences.list.failed", err)
		return
	}
	items := make([]TaskScheduleOccurrenceItem, 0, len(occurrences))
	for _, occurrence := range occurrences {
		items = append(items, renderTaskScheduleOccurrence(occurrence))
	}
	WriteJSON(w, http.StatusOK, TaskScheduleOccurrencesResponse{
		Object: "task_schedule_occurrences",
		Data:   items,
	})
}

func (h *Handler) writeTaskScheduleError(w http.ResponseWriter, r *http.Request, eventName string, err error) {
	if writeTaskScheduleAppError(w, err) {
		return
	}
	telemetry.Error(h.logger, r.Context(), eventName,
		slog.String("event.name", eventName),
		slog.Any("error", err),
	)
	WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
}

func parseTaskScheduleLimit(w http.ResponseWriter, raw string) (int, bool) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "limit query parameter must be a positive integer")
		return 0, false
	}
	if value > 200 {
		value = 200
	}
	return value, true
}

func renderTaskSchedule(schedule taskstate.TaskSchedule) TaskScheduleItem {
	return TaskScheduleItem{
		ID:             schedule.ID,
		TaskID:         schedule.TaskID,
		Kind:           schedule.Kind,
		CronExpression: schedule.CronExpression,
		Timezone:       schedule.Timezone,
		RunAt:          renderTaskScheduleTime(schedule.RunAt),
		Enabled:        schedule.Enabled,
		NextRunAt:      renderTaskScheduleTime(schedule.NextRunAt),
		CreatedAt:      renderTaskScheduleTime(schedule.CreatedAt),
		UpdatedAt:      renderTaskScheduleTime(schedule.UpdatedAt),
	}
}

func renderTaskScheduleOccurrence(occurrence taskstate.TaskScheduleOccurrence) TaskScheduleOccurrenceItem {
	return TaskScheduleOccurrenceItem{
		ID:           occurrence.ID,
		TaskID:       occurrence.TaskID,
		ScheduleID:   occurrence.ScheduleID,
		ScheduledFor: renderTaskScheduleTime(occurrence.ScheduledFor),
		Status:       occurrence.Status,
		ClaimedAt:    renderTaskScheduleTime(occurrence.ClaimedAt),
		RunID:        occurrence.RunID,
		Error:        occurrence.Error,
		CompletedAt:  renderTaskScheduleTime(occurrence.CompletedAt),
	}
}

func renderTaskScheduleTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
