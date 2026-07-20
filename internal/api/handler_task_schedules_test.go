package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestTaskScheduleAPIUpsertGetListOccurrencesAndDelete(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	if _, err := store.CreateTask(t.Context(), types.Task{ID: "task_1", Status: types.TaskStatusNotStarted}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	handler := newTaskScheduleTestHandler(store)

	putBody := `{"kind":"once","timezone":"Europe/Madrid","run_at":"2099-07-20T10:30:00+02:00","enabled":true}`
	put := taskScheduleRequest(t, handler, http.MethodPut, "/hecate/v1/tasks/task_1/schedule", putBody)
	if put.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200, body=%s", put.Code, put.Body.String())
	}
	created := decodeTaskScheduleResponse(t, put)
	if created.Object != "task_schedule" || created.Data.ID == "" || created.Data.TaskID != "task_1" {
		t.Fatalf("created response = %+v", created)
	}
	if created.Data.RunAt != "2099-07-20T08:30:00Z" || created.Data.NextRunAt != created.Data.RunAt {
		t.Fatalf("created times = run_at %q next_run_at %q, want UTC occurrence", created.Data.RunAt, created.Data.NextRunAt)
	}
	if created.Data.CreatedAt == "" || created.Data.UpdatedAt == "" {
		t.Fatalf("created timestamps = %q/%q, want populated", created.Data.CreatedAt, created.Data.UpdatedAt)
	}

	get := taskScheduleRequest(t, handler, http.MethodGet, "/hecate/v1/tasks/task_1/schedule", "")
	if get.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200, body=%s", get.Code, get.Body.String())
	}
	fetched := decodeTaskScheduleResponse(t, get)
	if fetched.Data != created.Data {
		t.Fatalf("fetched schedule = %+v, want %+v", fetched.Data, created.Data)
	}

	list := taskScheduleRequest(t, handler, http.MethodGet, "/hecate/v1/task-schedules?enabled=true&limit=10", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200, body=%s", list.Code, list.Body.String())
	}
	var listed TaskSchedulesResponse
	decodeTaskScheduleJSON(t, list, &listed)
	if listed.Object != "task_schedules" || len(listed.Data) != 1 || listed.Data[0] != created.Data {
		t.Fatalf("listed schedules = %+v, want created schedule", listed)
	}

	scheduledFor, err := time.Parse(time.RFC3339Nano, created.Data.NextRunAt)
	if err != nil {
		t.Fatalf("parse next_run_at: %v", err)
	}
	claimedAt := time.Date(2099, time.July, 20, 8, 30, 0, 123, time.UTC)
	if _, claimed, err := store.ClaimTaskScheduleOccurrence(t.Context(), taskstate.TaskScheduleOccurrenceClaim{
		OccurrenceID:             "occ_1",
		ScheduleID:               created.Data.ID,
		ExpectedScheduleRevision: 1,
		ScheduledFor:             scheduledFor,
		ClaimOwner:               "worker_1",
		ClaimedAt:                claimedAt,
	}); err != nil || !claimed {
		t.Fatalf("ClaimTaskScheduleOccurrence() = claimed %v, error %v", claimed, err)
	}
	completedAt := claimedAt.Add(time.Second)
	if _, completed, err := store.CompleteTaskScheduleOccurrence(t.Context(), taskstate.TaskScheduleOccurrenceCompletion{
		ScheduleID:   created.Data.ID,
		ScheduledFor: scheduledFor,
		ClaimOwner:   "worker_1",
		Status:       taskstate.TaskScheduleOccurrenceStarted,
		RunID:        "run_1",
		CompletedAt:  completedAt,
	}); err != nil || !completed {
		t.Fatalf("CompleteTaskScheduleOccurrence() = completed %v, error %v", completed, err)
	}

	occurrences := taskScheduleRequest(t, handler, http.MethodGet, "/hecate/v1/tasks/task_1/schedule/occurrences?limit=10", "")
	if occurrences.Code != http.StatusOK {
		t.Fatalf("occurrences status = %d, want 200, body=%s", occurrences.Code, occurrences.Body.String())
	}
	var history TaskScheduleOccurrencesResponse
	decodeTaskScheduleJSON(t, occurrences, &history)
	if history.Object != "task_schedule_occurrences" || len(history.Data) != 1 {
		t.Fatalf("occurrence response = %+v", history)
	}
	gotOccurrence := history.Data[0]
	if gotOccurrence.ID != "occ_1" || gotOccurrence.RunID != "run_1" || gotOccurrence.Status != taskstate.TaskScheduleOccurrenceStarted {
		t.Fatalf("occurrence = %+v, want started run_1", gotOccurrence)
	}
	if gotOccurrence.ScheduledFor != created.Data.NextRunAt || gotOccurrence.ClaimedAt != claimedAt.Format(time.RFC3339Nano) || gotOccurrence.CompletedAt != completedAt.Format(time.RFC3339Nano) {
		t.Fatalf("occurrence timestamps = %+v, want UTC RFC3339Nano", gotOccurrence)
	}

	update := taskScheduleRequest(t, handler, http.MethodPut, "/hecate/v1/tasks/task_1/schedule", `{"kind":"cron","cron_expression":"0 9 * * 1-5","timezone":"Europe/Madrid","enabled":false}`)
	if update.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200, body=%s", update.Code, update.Body.String())
	}
	updated := decodeTaskScheduleResponse(t, update)
	if updated.Data.ID != created.Data.ID || updated.Data.Enabled || updated.Data.NextRunAt != "" || updated.Data.Kind != taskstate.TaskScheduleKindCron {
		t.Fatalf("updated schedule = %+v, want stable disabled cron", updated.Data)
	}

	deleted := taskScheduleRequest(t, handler, http.MethodDelete, "/hecate/v1/tasks/task_1/schedule", "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204, body=%s", deleted.Code, deleted.Body.String())
	}
	missing := taskScheduleRequest(t, handler, http.MethodGet, "/hecate/v1/tasks/task_1/schedule", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("GET after delete status = %d, want 404, body=%s", missing.Code, missing.Body.String())
	}
}

func TestTaskScheduleAPIListAcceptsRepeatedExactTaskIDs(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	base := time.Date(2099, time.July, 20, 8, 30, 0, 0, time.UTC)
	for index, taskID := range []string{"task_a", "task_a_child", "task_c"} {
		if _, err := store.CreateTask(t.Context(), types.Task{ID: taskID, Status: types.TaskStatusNotStarted}); err != nil {
			t.Fatalf("CreateTask(%s) error = %v", taskID, err)
		}
		mustCreateAPITaskSchedule(t, store, taskstate.TaskSchedule{
			ID: "schedule_" + taskID, TaskID: taskID, Kind: taskstate.TaskScheduleKindCron,
			CronExpression: "0 9 * * *", Timezone: "UTC", Enabled: true,
			NextRunAt: base.Add(time.Duration(index) * time.Minute),
			CreatedAt: base.Add(time.Duration(index) * time.Minute),
			UpdatedAt: base.Add(time.Duration(index) * time.Minute),
		})
	}

	handler := newTaskScheduleTestHandler(store)
	list := taskScheduleRequest(t, handler, http.MethodGet, "/hecate/v1/task-schedules?task_id=%20task_a%20&task_id=task_a&task_id=%20%20&task_id=task_c", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200, body=%s", list.Code, list.Body.String())
	}
	var response TaskSchedulesResponse
	decodeTaskScheduleJSON(t, list, &response)
	if len(response.Data) != 2 || response.Data[0].TaskID != "task_c" || response.Data[1].TaskID != "task_a" {
		t.Fatalf("listed schedules = %+v, want exact task_a/task_c set", response.Data)
	}
}

func TestTaskScheduleAPIListLimitsUniqueTaskIDs(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	if _, err := store.CreateTask(t.Context(), types.Task{ID: "task_a", Status: types.TaskStatusNotStarted}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	mustCreateAPITaskSchedule(t, store, taskstate.TaskSchedule{
		ID: "schedule_a", TaskID: "task_a", Kind: taskstate.TaskScheduleKindCron,
		CronExpression: "0 9 * * *", Timezone: "UTC", Enabled: true,
		NextRunAt: time.Date(2099, time.July, 20, 8, 30, 0, 0, time.UTC),
	})
	handler := newTaskScheduleTestHandler(store)

	duplicates := url.Values{}
	for range maxTaskScheduleTaskIDFilters + 1 {
		duplicates.Add("task_id", " task_a ")
	}
	duplicateList := taskScheduleRequest(t, handler, http.MethodGet, "/hecate/v1/task-schedules?"+duplicates.Encode(), "")
	if duplicateList.Code != http.StatusOK {
		t.Fatalf("duplicate list status = %d, want 200, body=%s", duplicateList.Code, duplicateList.Body.String())
	}
	var duplicateResponse TaskSchedulesResponse
	decodeTaskScheduleJSON(t, duplicateList, &duplicateResponse)
	if len(duplicateResponse.Data) != 1 || duplicateResponse.Data[0].TaskID != "task_a" {
		t.Fatalf("duplicate list response = %+v, want one task_a schedule", duplicateResponse.Data)
	}

	unique := url.Values{}
	for index := range maxTaskScheduleTaskIDFilters + 1 {
		unique.Add("task_id", fmt.Sprintf("task_%03d", index))
	}
	overLimit := taskScheduleRequest(t, handler, http.MethodGet, "/hecate/v1/task-schedules?"+unique.Encode(), "")
	if overLimit.Code != http.StatusBadRequest {
		t.Fatalf("over-limit status = %d, want 400, body=%s", overLimit.Code, overLimit.Body.String())
	}
	var errorResponse struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeTaskScheduleJSON(t, overLimit, &errorResponse)
	if errorResponse.Error.Type != errCodeInvalidRequest || !strings.Contains(errorResponse.Error.Message, "at most 200 unique values") {
		t.Fatalf("over-limit error = %+v, want invalid_request with unique-value limit", errorResponse.Error)
	}
}

func TestTaskScheduleAPIRejectsCronWithoutFutureOccurrenceAsInvalidRequest(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	if _, err := store.CreateTask(t.Context(), types.Task{ID: "task_1", Status: types.TaskStatusNotStarted}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	handler := newTaskScheduleTestHandler(store)

	for _, enabled := range []bool{false, true} {
		body := fmt.Sprintf(`{"kind":"cron","cron_expression":"0 0 31 2 *","timezone":"UTC","enabled":%t}`, enabled)
		response := taskScheduleRequest(t, handler, http.MethodPut, "/hecate/v1/tasks/task_1/schedule", body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("enabled=%v status = %d, want 400, body=%s", enabled, response.Code, response.Body.String())
		}
		var errorResponse struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		decodeTaskScheduleJSON(t, response, &errorResponse)
		if errorResponse.Error.Type != errCodeInvalidRequest || !strings.Contains(errorResponse.Error.Message, "no future occurrence") {
			t.Fatalf("enabled=%v error = %+v, want invalid_request for no future occurrence", enabled, errorResponse.Error)
		}
	}
}

type taskScheduleTaskDeletionRaceStore struct {
	*taskstate.MemoryStore
}

func (s *taskScheduleTaskDeletionRaceStore) CompareAndSwapTaskSchedule(ctx context.Context, mutation taskstate.TaskScheduleCompareAndSwap) (taskstate.TaskSchedule, bool, error) {
	if err := s.MemoryStore.DeleteTask(ctx, mutation.Schedule.TaskID); err != nil {
		return taskstate.TaskSchedule{}, false, err
	}
	return taskstate.TaskSchedule{}, false, fmt.Errorf("insert task schedule: foreign key constraint failed")
}

func TestTaskScheduleAPIUpsertRacingTaskDeletionReturnsNotFound(t *testing.T) {
	t.Parallel()

	store := &taskScheduleTaskDeletionRaceStore{MemoryStore: taskstate.NewMemoryStore()}
	if _, err := store.CreateTask(t.Context(), types.Task{
		ID: "task_schedule_delete_race", Status: types.TaskStatusNotStarted,
	}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	handler := newTaskScheduleTestHandler(store)

	response := taskScheduleRequest(
		t,
		handler,
		http.MethodPut,
		"/hecate/v1/tasks/task_schedule_delete_race/schedule",
		`{"kind":"cron","cron_expression":"0 9 * * *","timezone":"UTC","enabled":true}`,
	)
	if response.Code != http.StatusNotFound {
		t.Fatalf("PUT status = %d, want 404, body=%s", response.Code, response.Body.String())
	}
	var errorResponse struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeTaskScheduleJSON(t, response, &errorResponse)
	if errorResponse.Error.Type != errCodeNotFound || errorResponse.Error.Message != "task not found" {
		t.Fatalf("error = %+v, want stable task-not-found response", errorResponse.Error)
	}
	if strings.Contains(response.Body.String(), "foreign key") {
		t.Fatalf("response exposed storage error: %s", response.Body.String())
	}
}

func TestTaskScheduleAPIRejectsChatOwnedTask(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	if _, err := store.CreateTask(t.Context(), types.Task{
		ID: "task_chat_owned", OriginKind: "chat", OriginID: "chat_1", Status: types.TaskStatusNotStarted,
	}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	handler := newTaskScheduleTestHandler(store)

	response := taskScheduleRequest(
		t,
		handler,
		http.MethodPut,
		"/hecate/v1/tasks/task_chat_owned/schedule",
		`{"kind":"cron","cron_expression":"0 9 * * *","timezone":"UTC","enabled":true}`,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("PUT status = %d, want 400, body=%s", response.Code, response.Body.String())
	}
	var errorResponse struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeTaskScheduleJSON(t, response, &errorResponse)
	if errorResponse.Error.Type != errCodeInvalidRequest || errorResponse.Error.Message != "chat-owned tasks cannot be scheduled" {
		t.Fatalf("error = %+v, want stable invalid_request chat-owned Task response", errorResponse.Error)
	}
	if _, found, err := store.GetTaskScheduleByTask(t.Context(), "task_chat_owned"); err != nil || found {
		t.Fatalf("GetTaskScheduleByTask() = found %v error %v, want no persisted Schedule", found, err)
	}
}

func TestTaskScheduleAPIRejectsInvalidContracts(t *testing.T) {
	t.Parallel()

	store := taskstate.NewMemoryStore()
	if _, err := store.CreateTask(t.Context(), types.Task{ID: "task_1", Status: types.TaskStatusNotStarted}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	handler := newTaskScheduleTestHandler(store)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{name: "enabled omitted", method: http.MethodPut, path: "/hecate/v1/tasks/task_1/schedule", body: `{"kind":"cron","cron_expression":"0 9 * * *","timezone":"UTC"}`, want: http.StatusBadRequest},
		{name: "enabled null", method: http.MethodPut, path: "/hecate/v1/tasks/task_1/schedule", body: `{"kind":"cron","cron_expression":"0 9 * * *","timezone":"UTC","enabled":null}`, want: http.StatusBadRequest},
		{name: "malformed run at", method: http.MethodPut, path: "/hecate/v1/tasks/task_1/schedule", body: `{"kind":"once","run_at":"tomorrow","timezone":"UTC","enabled":true}`, want: http.StatusBadRequest},
		{name: "invalid cron", method: http.MethodPut, path: "/hecate/v1/tasks/task_1/schedule", body: `{"kind":"cron","cron_expression":"every morning","timezone":"UTC","enabled":true}`, want: http.StatusBadRequest},
		{name: "unknown task", method: http.MethodPut, path: "/hecate/v1/tasks/missing/schedule", body: `{"kind":"cron","cron_expression":"0 9 * * *","timezone":"UTC","enabled":true}`, want: http.StatusNotFound},
		{name: "missing schedule", method: http.MethodGet, path: "/hecate/v1/tasks/task_1/schedule", want: http.StatusNotFound},
		{name: "invalid enabled filter", method: http.MethodGet, path: "/hecate/v1/task-schedules?enabled=sometimes", want: http.StatusBadRequest},
		{name: "invalid limit", method: http.MethodGet, path: "/hecate/v1/task-schedules?limit=-1", want: http.StatusBadRequest},
		{name: "zero limit", method: http.MethodGet, path: "/hecate/v1/task-schedules?limit=0", want: http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := taskScheduleRequest(t, handler, tc.method, tc.path, tc.body)
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d, body=%s", recorder.Code, tc.want, recorder.Body.String())
			}
		})
	}
}

func newTaskScheduleTestHandler(store taskstate.Store) http.Handler {
	mux := http.NewServeMux()
	registerHecateTaskRoutes(mux, &Handler{taskStore: store, logger: quietLogger()})
	return mux
}

func mustCreateAPITaskSchedule(t *testing.T, store taskstate.ScheduleStore, schedule taskstate.TaskSchedule) taskstate.TaskSchedule {
	t.Helper()
	stored, applied, err := store.CompareAndSwapTaskSchedule(t.Context(), taskstate.TaskScheduleCompareAndSwap{Schedule: schedule})
	if err != nil || !applied {
		t.Fatalf("CompareAndSwapTaskSchedule(create) = (%+v, %v, %v)", stored, applied, err)
	}
	return stored
}

func taskScheduleRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func decodeTaskScheduleResponse(t *testing.T, recorder *httptest.ResponseRecorder) TaskScheduleResponse {
	t.Helper()
	var response TaskScheduleResponse
	decodeTaskScheduleJSON(t, recorder, &response)
	return response
}

func decodeTaskScheduleJSON(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(recorder.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, recorder.Body.String())
	}
}

func TestRenderTaskScheduleUsesUTCRFC3339Nano(t *testing.T) {
	t.Parallel()

	local := time.FixedZone("UTC+2", 2*60*60)
	item := renderTaskSchedule(taskstate.TaskSchedule{
		ID:        "schedule_1",
		TaskID:    "task_1",
		Kind:      taskstate.TaskScheduleKindOnce,
		Timezone:  "Europe/Madrid",
		RunAt:     time.Date(2026, time.July, 20, 10, 30, 0, 123, local),
		Enabled:   true,
		NextRunAt: time.Date(2026, time.July, 20, 10, 30, 0, 123, local),
		CreatedAt: time.Date(2026, time.July, 20, 10, 0, 0, 0, local),
		UpdatedAt: time.Date(2026, time.July, 20, 10, 5, 0, 0, local),
	})
	if item.RunAt != "2026-07-20T08:30:00.000000123Z" || item.NextRunAt != item.RunAt {
		t.Fatalf("rendered occurrence times = %q/%q", item.RunAt, item.NextRunAt)
	}
	if item.CreatedAt != "2026-07-20T08:00:00Z" || item.UpdatedAt != "2026-07-20T08:05:00Z" {
		t.Fatalf("rendered metadata times = %q/%q", item.CreatedAt, item.UpdatedAt)
	}
}
