package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
)

type blockingTerminalTaskStore struct {
	taskstate.Store
	started chan struct{}
	release chan struct{}
	err     error
	once    sync.Once
}

type blockingOriginRunMutationStore struct {
	taskstate.Store
	createStarted chan struct{}
	createRelease chan struct{}
	cancelStarted chan struct{}
	cancelRelease chan struct{}
	createOnce    sync.Once
	cancelOnce    sync.Once
}

type failingOriginValidationChatStore struct {
	chat.Store
	err error
}

type nonClaimingRunQueue struct {
	mu       sync.Mutex
	enqueued []orchestrator.QueueJob
}

type drainingOriginExecutor struct {
	started   chan struct{}
	cancelled chan struct{}
	release   chan struct{}
}

func (e drainingOriginExecutor) Execute(ctx context.Context, _ orchestrator.ExecutionSpec) (*orchestrator.ExecutionResult, error) {
	close(e.started)
	<-ctx.Done()
	close(e.cancelled)
	<-e.release
	return nil, ctx.Err()
}

func (q *nonClaimingRunQueue) Backend() string { return "test" }
func (q *nonClaimingRunQueue) Enqueue(_ context.Context, job orchestrator.QueueJob) error {
	q.mu.Lock()
	q.enqueued = append(q.enqueued, job)
	q.mu.Unlock()
	return nil
}
func (*nonClaimingRunQueue) Claim(ctx context.Context, _ string, _ time.Duration) (orchestrator.QueueClaim, bool, error) {
	<-ctx.Done()
	return orchestrator.QueueClaim{}, false, ctx.Err()
}
func (*nonClaimingRunQueue) Ack(context.Context, string) error                        { return nil }
func (*nonClaimingRunQueue) Nack(context.Context, string, string) error               { return nil }
func (*nonClaimingRunQueue) ExtendLease(context.Context, string, time.Duration) error { return nil }
func (q *nonClaimingRunQueue) Depth(context.Context) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.enqueued), nil
}
func (*nonClaimingRunQueue) Capacity() int { return 0 }

func (s failingOriginValidationChatStore) Get(context.Context, string) (chat.Session, bool, error) {
	return chat.Session{}, false, s.err
}

// Block the authoritative run-start commit after the runner has acquired its
// origin lease. This lets the test prove deletion waits for the admitted start,
// then discovers and cancels the run committed immediately before lease release.
func (s *blockingOriginRunMutationStore) ApplyRunStartTransition(ctx context.Context, transition taskstate.RunStartTransition) (taskstate.RunStartTransitionResult, error) {
	s.createOnce.Do(func() { close(s.createStarted) })
	select {
	case <-s.createRelease:
	case <-ctx.Done():
		return taskstate.RunStartTransitionResult{}, ctx.Err()
	}
	return s.Store.ApplyRunStartTransition(ctx, transition)
}

func (s *blockingOriginRunMutationStore) ApplyRunTerminalTransition(ctx context.Context, transition taskstate.TerminalRunTransition) (taskstate.TerminalRunTransitionResult, error) {
	s.cancelOnce.Do(func() { close(s.cancelStarted) })
	select {
	case <-s.cancelRelease:
	case <-ctx.Done():
		return taskstate.TerminalRunTransitionResult{}, ctx.Err()
	}
	return s.Store.ApplyRunTerminalTransition(ctx, transition)
}

func (s *blockingTerminalTaskStore) ApplyRunTerminalTransition(ctx context.Context, transition taskstate.TerminalRunTransition) (taskstate.TerminalRunTransitionResult, error) {
	s.once.Do(func() { close(s.started) })
	select {
	case <-s.release:
	case <-ctx.Done():
		return taskstate.TerminalRunTransitionResult{}, ctx.Err()
	}
	if s.err != nil {
		return taskstate.TerminalRunTransitionResult{}, s.err
	}
	return s.Store.ApplyRunTerminalTransition(ctx, transition)
}

func TestHecateChatDeleteWaitsForLateUnlinkedOriginRunCancellation(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	baseStore := taskstate.NewMemoryStore()
	store := &blockingTerminalTaskStore{
		Store:   baseStore,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	deletionReleased := false
	defer func() {
		if !deletionReleased {
			close(store.release)
		}
	}()
	apiHandler := NewHandler(config.Config{}, logger, nil, controlplane.NewMemoryStore(), store, nil)
	server := NewServer(logger, apiHandler)
	ctx := context.Background()
	session, err := apiHandler.agentChat.Create(ctx, chat.Session{
		ID:      "chat_late_origin_run",
		Title:   "Late origin run",
		AgentID: chat.DefaultAgentID,
	})
	if err != nil {
		t.Fatalf("Create chat session: %v", err)
	}
	if session.TaskID != "" || session.LatestRunID != "" {
		t.Fatalf("session task link = %q/%q, want empty before late task", session.TaskID, session.LatestRunID)
	}

	lateCreated := make(chan error, 1)
	if got := apiHandler.agentChatLive.registerRun(apiHandler.agentChatLive.snapshotLifecycle(session.ID), func() {
		now := time.Now().UTC()
		task := types.Task{
			ID:            "task_late_origin_run",
			Title:         "Late origin run",
			OriginKind:    "chat",
			OriginID:      session.ID,
			Status:        "running",
			LatestRunID:   "run_late_origin_run",
			CreatedAt:     now,
			UpdatedAt:     now,
			StartedAt:     now,
			ExecutionKind: "agent_loop",
		}
		if _, createErr := store.CreateTask(context.Background(), task); createErr != nil {
			lateCreated <- createErr
			apiHandler.agentChatLive.clearRun(session.ID)
			return
		}
		_, createErr := store.CreateRun(context.Background(), types.TaskRun{
			ID:        task.LatestRunID,
			TaskID:    task.ID,
			Number:    1,
			Status:    "running",
			StartedAt: now,
		})
		lateCreated <- createErr
		apiHandler.agentChatLive.clearRun(session.ID)
	}); got != agentChatRunAccepted {
		t.Fatalf("registerRun() = %v, want accepted", got)
	}

	deleteDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/hecate/v1/chat/sessions/"+session.ID, nil))
		deleteDone <- recorder
	}()

	select {
	case createErr := <-lateCreated:
		if createErr != nil {
			t.Fatalf("create late origin task/run: %v", createErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("delete did not cancel the live run and create its late origin task")
	}
	select {
	case <-store.started:
	case <-time.After(3 * time.Second):
		t.Fatal("delete did not begin durable cancellation of the late origin run")
	}
	select {
	case recorder := <-deleteDone:
		t.Fatalf("delete returned before task cancellation persisted: status=%d body=%s", recorder.Code, recorder.Body.String())
	case <-time.After(100 * time.Millisecond):
	}
	if _, found, getErr := apiHandler.agentChat.Get(ctx, session.ID); getErr != nil || !found {
		t.Fatalf("chat during blocked task cancellation: found=%t err=%v, want preserved", found, getErr)
	}
	if run, found, getErr := store.GetRun(ctx, "task_late_origin_run", "run_late_origin_run"); getErr != nil || !found || run.Status != "running" {
		t.Fatalf("run during blocked cancellation = %+v found=%t err=%v, want running", run, found, getErr)
	}

	close(store.release)
	deletionReleased = true
	var recorder *httptest.ResponseRecorder
	select {
	case recorder = <-deleteDone:
	case <-time.After(3 * time.Second):
		t.Fatal("delete did not finish after task cancellation was released")
	}
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204, body=%s", recorder.Code, recorder.Body.String())
	}
	run, found, err := store.GetRun(ctx, "task_late_origin_run", "run_late_origin_run")
	if err != nil || !found || run.Status != "cancelled" {
		t.Fatalf("late origin run after delete = %+v found=%t err=%v, want retained cancelled history", run, found, err)
	}
	if !strings.Contains(run.LastError, "operator") {
		t.Fatalf("late origin run last error = %q, want operator cancellation", run.LastError)
	}
	if _, found, err := store.GetTask(ctx, "task_late_origin_run"); err != nil || !found {
		t.Fatalf("late origin task history after delete: found=%t err=%v, want retained", found, err)
	}
	if _, found, err := apiHandler.agentChat.Get(ctx, session.ID); err != nil || found {
		t.Fatalf("chat after delete: found=%t err=%v, want removed", found, err)
	}
}

func TestHecateChatDeleteWaitsForOriginExecutorExit(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := taskstate.NewMemoryStore()
	apiHandler := NewHandler(config.Config{}, logger, nil, controlplane.NewMemoryStore(), store, nil)
	executor := drainingOriginExecutor{
		started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{}),
	}
	apiHandler.taskRunner.SetExecutor(executor)
	ctx := t.Context()
	now := time.Now().UTC()
	session, err := apiHandler.agentChat.Create(ctx, chat.Session{
		ID: "chat_executor_drain", Title: "Executor drain", Status: "running", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("Create chat: %v", err)
	}
	task, err := store.CreateTask(ctx, types.Task{
		ID: "task_executor_drain", OriginKind: "chat", OriginID: session.ID,
		Status: "queued", LatestRunID: "run_executor_drain", ExecutionKind: "stub", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("Create task: %v", err)
	}
	if _, err := store.CreateRun(ctx, types.TaskRun{
		ID: task.LatestRunID, TaskID: task.ID, Status: "queued", StartedAt: now,
	}); err != nil {
		t.Fatalf("Create run: %v", err)
	}
	server := NewServer(logger, apiHandler)
	t.Cleanup(func() {
		select {
		case <-executor.release:
		default:
			close(executor.release)
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = apiHandler.Shutdown(shutdownCtx)
	})
	select {
	case <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("origin executor did not start")
	}

	responseDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/hecate/v1/chat/sessions/"+session.ID, nil))
		responseDone <- recorder
	}()
	select {
	case <-executor.cancelled:
	case <-time.After(time.Second):
		t.Fatal("origin executor did not observe cancellation")
	}
	select {
	case recorder := <-responseDone:
		t.Fatalf("delete returned before executor exit: status=%d body=%s", recorder.Code, recorder.Body.String())
	default:
	}
	if _, found, err := apiHandler.agentChat.Get(ctx, session.ID); err != nil || !found {
		t.Fatalf("chat during executor drain found=%t err=%v, want preserved", found, err)
	}
	close(executor.release)
	recorder := <-responseDone
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204, body=%s", recorder.Code, recorder.Body.String())
	}
	if _, found, err := apiHandler.agentChat.Get(ctx, session.ID); err != nil || found {
		t.Fatalf("chat after executor drain found=%t err=%v, want deleted", found, err)
	}
}

func TestHecateChatDeleteFencesConcurrentOriginRetryThroughCancellation(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	baseStore := taskstate.NewMemoryStore()
	store := &blockingOriginRunMutationStore{
		Store:         baseStore,
		createStarted: make(chan struct{}),
		createRelease: make(chan struct{}),
		cancelStarted: make(chan struct{}),
		cancelRelease: make(chan struct{}),
	}
	var releaseCreateOnce sync.Once
	var releaseCancelOnce sync.Once
	releaseCreate := func() { releaseCreateOnce.Do(func() { close(store.createRelease) }) }
	releaseCancel := func() { releaseCancelOnce.Do(func() { close(store.cancelRelease) }) }
	t.Cleanup(releaseCreate)
	t.Cleanup(releaseCancel)

	cfg := config.Config{Server: config.ServerConfig{TaskApprovalPolicies: []string{"shell_exec"}}}
	apiHandler := NewHandler(cfg, logger, nil, controlplane.NewMemoryStore(), store, nil)
	server := NewServer(logger, apiHandler)
	ctx := context.Background()
	session, err := apiHandler.agentChat.Create(ctx, chat.Session{
		ID:      "chat_origin_retry_fence",
		Title:   "Origin retry fence",
		AgentID: chat.DefaultAgentID,
	})
	if err != nil {
		t.Fatalf("Create chat session: %v", err)
	}
	now := time.Now().UTC()
	task, err := baseStore.CreateTask(ctx, types.Task{
		ID:            "task_origin_retry_fence",
		Title:         "Origin retry fence",
		OriginKind:    "chat",
		OriginID:      session.ID,
		ExecutionKind: "shell",
		ShellCommand:  "true",
		Status:        "failed",
		LatestRunID:   "run_origin_retry_source",
		CreatedAt:     now,
		UpdatedAt:     now,
	})
	if err != nil {
		t.Fatalf("Create task: %v", err)
	}
	if _, err := baseStore.CreateRun(ctx, types.TaskRun{
		ID:         task.LatestRunID,
		TaskID:     task.ID,
		Number:     1,
		Status:     "failed",
		StartedAt:  now,
		FinishedAt: now,
	}); err != nil {
		t.Fatalf("Create source run: %v", err)
	}

	retryDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		path := "/hecate/v1/tasks/" + task.ID + "/runs/" + task.LatestRunID + "/retry"
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
		retryDone <- recorder
	}()
	select {
	case <-store.createStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("retry did not reach the guarded run creation")
	}

	deleteDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/hecate/v1/chat/sessions/"+session.ID, nil))
		deleteDone <- recorder
	}()
	select {
	case recorder := <-deleteDone:
		t.Fatalf("delete returned while an admitted origin run was still being created: status=%d body=%s", recorder.Code, recorder.Body.String())
	case <-time.After(100 * time.Millisecond):
	}

	releaseCreate()
	select {
	case <-store.cancelStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("delete did not cancel the concurrently created origin run")
	}
	select {
	case recorder := <-deleteDone:
		t.Fatalf("delete returned before concurrent origin-run cancellation persisted: status=%d body=%s", recorder.Code, recorder.Body.String())
	case <-time.After(100 * time.Millisecond):
	}
	if _, found, getErr := apiHandler.agentChat.Get(ctx, session.ID); getErr != nil || !found {
		t.Fatalf("chat during blocked origin cancellation: found=%t err=%v, want preserved", found, getErr)
	}

	releaseCancel()
	var retryRecorder *httptest.ResponseRecorder
	select {
	case retryRecorder = <-retryDone:
	case <-time.After(3 * time.Second):
		t.Fatal("retry did not return after run creation was released")
	}
	if retryRecorder.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200, body=%s", retryRecorder.Code, retryRecorder.Body.String())
	}
	var deleteRecorder *httptest.ResponseRecorder
	select {
	case deleteRecorder = <-deleteDone:
	case <-time.After(3 * time.Second):
		t.Fatal("delete did not finish after origin cancellation was released")
	}
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204, body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}

	runs, err := baseStore.ListRuns(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("run count after delete = %d, want source plus concurrent retry", len(runs))
	}
	var created types.TaskRun
	for _, run := range runs {
		if run.ID != task.LatestRunID {
			created = run
		}
	}
	if created.ID == "" || created.Status != "cancelled" {
		t.Fatalf("concurrent run after delete = %+v, want retained cancelled history", created)
	}
	if _, found, getErr := baseStore.GetTask(ctx, task.ID); getErr != nil || !found {
		t.Fatalf("task history after delete: found=%t err=%v, want retained", found, getErr)
	}

	blockedRetry := httptest.NewRecorder()
	blockedPath := "/hecate/v1/tasks/" + task.ID + "/runs/" + task.LatestRunID + "/retry"
	server.ServeHTTP(blockedRetry, httptest.NewRequest(http.MethodPost, blockedPath, nil))
	if blockedRetry.Code != http.StatusConflict {
		t.Fatalf("retry after origin deletion status = %d, want 409, body=%s", blockedRetry.Code, blockedRetry.Body.String())
	}
	runsAfterBlockedRetry, err := baseStore.ListRuns(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListRuns after blocked retry: %v", err)
	}
	if len(runsAfterBlockedRetry) != len(runs) {
		t.Fatalf("run count after blocked retry = %d, want %d", len(runsAfterBlockedRetry), len(runs))
	}
}

func TestTaskRetryRejectsMissingDurableChatOrigin(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := taskstate.NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()
	task, err := store.CreateTask(ctx, types.Task{
		ID:            "task_missing_chat_origin",
		Title:         "Missing chat origin",
		OriginKind:    "chat",
		OriginID:      "chat_already_deleted",
		ExecutionKind: "shell",
		ShellCommand:  "true",
		Status:        "failed",
		LatestRunID:   "run_missing_chat_origin",
		CreatedAt:     now,
		UpdatedAt:     now,
	})
	if err != nil {
		t.Fatalf("Create task: %v", err)
	}
	if _, err := store.CreateRun(ctx, types.TaskRun{
		ID:         task.LatestRunID,
		TaskID:     task.ID,
		Number:     1,
		Status:     "failed",
		StartedAt:  now,
		FinishedAt: now,
	}); err != nil {
		t.Fatalf("Create source run: %v", err)
	}

	cfg := config.Config{Server: config.ServerConfig{TaskApprovalPolicies: []string{"shell_exec"}}}
	apiHandler := NewHandler(cfg, logger, nil, controlplane.NewMemoryStore(), store, nil)
	server := NewServer(logger, apiHandler)
	recorder := httptest.NewRecorder()
	path := "/hecate/v1/tasks/" + task.ID + "/runs/" + task.LatestRunID + "/retry"
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("retry status = %d, want 409, body=%s", recorder.Code, recorder.Body.String())
	}
	runs, err := store.ListRuns(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("run count after missing-origin retry = %d, want unchanged source history", len(runs))
	}
}

func TestNewServerReconcilesOnlyAfterDurableChatOriginStoreIsWired(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := taskstate.NewMemoryStore()
	queue := &nonClaimingRunQueue{}
	ctx := t.Context()
	now := time.Now().UTC().Add(-time.Minute)
	task, err := store.CreateTask(ctx, types.Task{
		ID: "task_startup_origin", OriginKind: "chat", OriginID: "chat_startup_origin",
		Status: "running", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run, err := store.CreateRun(ctx, types.TaskRun{
		ID: "run_startup_origin", TaskID: task.ID, Status: "running", StartedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	apiHandler := NewHandler(config.Config{}, logger, nil, controlplane.NewMemoryStore(), store, queue)
	before, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found || before.Status != "running" {
		t.Fatalf("run before server composition = %+v found=%t err=%v", before, found, err)
	}
	if depth, _ := queue.Depth(ctx); depth != 0 {
		t.Fatalf("queue depth before server composition = %d, want 0", depth)
	}
	durableChats := chat.NewMemoryStore()
	if _, err := durableChats.Create(ctx, chat.Session{ID: task.OriginID, Title: "Durable owner", Status: "idle"}); err != nil {
		t.Fatalf("Create chat owner: %v", err)
	}
	apiHandler.SetAgentChatStore(durableChats)
	_ = NewServer(logger, apiHandler)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = apiHandler.Shutdown(shutdownCtx)
	})

	after, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found || after.Status != "queued" {
		t.Fatalf("run after server composition = %+v found=%t err=%v", after, found, err)
	}
	if depth, _ := queue.Depth(ctx); depth != 1 {
		t.Fatalf("queue depth after server composition = %d, want 1", depth)
	}
}

func TestTaskRetrySanitizesDurableChatOriginValidationFailure(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	store := taskstate.NewMemoryStore()
	ctx := t.Context()
	now := time.Now().UTC()
	task, err := store.CreateTask(ctx, types.Task{
		ID: "task_chat_origin_store_failure", Title: "Origin store failure",
		OriginKind: "chat", OriginID: "chat_origin_store_failure", ExecutionKind: "shell", ShellCommand: "true",
		Status: "failed", LatestRunID: "run_chat_origin_store_failure", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, types.TaskRun{
		ID: task.LatestRunID, TaskID: task.ID, Number: 1, Status: "failed", StartedAt: now, FinishedAt: now,
	}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	apiHandler := NewHandler(config.Config{}, logger, nil, controlplane.NewMemoryStore(), store, nil)
	storeDetail := errors.New("postgres connection contains secret detail")
	apiHandler.SetAgentChatStore(failingOriginValidationChatStore{Store: apiHandler.agentChat, err: storeDetail})
	server := NewServer(logger, apiHandler)
	recorder := httptest.NewRecorder()
	path := "/hecate/v1/tasks/" + task.ID + "/runs/" + task.LatestRunID + "/retry"
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("retry status = %d, want 500, body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "task origin validation failed") || strings.Contains(recorder.Body.String(), storeDetail.Error()) {
		t.Fatalf("retry body leaked validator detail: %s", recorder.Body.String())
	}
	runs, err := store.ListRuns(ctx, task.ID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs after validator failure = %+v err=%v", runs, err)
	}
}

func TestHecateChatDeleteConflictsWhenOriginRunCancellationFails(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cancelErr := errors.New("terminal transition unavailable")
	store := &blockingTerminalTaskStore{
		Store:   taskstate.NewMemoryStore(),
		started: make(chan struct{}),
		release: make(chan struct{}),
		err:     cancelErr,
	}
	close(store.release)
	apiHandler := NewHandler(config.Config{}, logger, nil, controlplane.NewMemoryStore(), store, nil)
	server := NewServer(logger, apiHandler)
	ctx := context.Background()
	session, task, run := seedUnlinkedChatOriginRun(t, ctx, apiHandler, store, "failure")

	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/hecate/v1/chat/sessions/"+session.ID, nil))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("delete status = %d, want 409, body=%s", recorder.Code, recorder.Body.String())
	}
	if _, found, err := apiHandler.agentChat.Get(ctx, session.ID); err != nil || !found {
		t.Fatalf("chat after cancellation conflict: found=%t err=%v, want preserved", found, err)
	}
	gotRun, found, err := store.GetRun(ctx, task.ID, run.ID)
	if err != nil || !found || gotRun.Status != "running" {
		t.Fatalf("run after cancellation conflict = %+v found=%t err=%v, want running for retry", gotRun, found, err)
	}
}

func TestHecateChatOriginRunCancellationIsSharedByProjectAndQuiescedCleanup(t *testing.T) {
	for _, test := range []struct {
		name   string
		delete func(context.Context, *Handler, chat.Session) error
	}{
		{
			name: "project delete",
			delete: func(ctx context.Context, handler *Handler, session chat.Session) error {
				stopping, err := handler.deleteProjectChatSession(ctx, session)
				if stopping {
					return errors.New("project chat remained stopping")
				}
				return err
			},
		},
		{
			name: "quiesced reset cleanup",
			delete: func(ctx context.Context, handler *Handler, _ chat.Session) error {
				deleted, err := handler.resetChatSessions(ctx)
				if err == nil && deleted != 1 {
					return errors.New("quiesced reset cleanup did not delete exactly one chat")
				}
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
			store := taskstate.NewMemoryStore()
			apiHandler := NewHandler(config.Config{}, logger, nil, controlplane.NewMemoryStore(), store, nil)
			ctx := context.Background()
			session, task, run := seedUnlinkedChatOriginRun(t, ctx, apiHandler, store, strings.ReplaceAll(test.name, " ", "_"))

			if err := test.delete(ctx, apiHandler, session); err != nil {
				t.Fatalf("delete: %v", err)
			}
			gotRun, found, err := store.GetRun(ctx, task.ID, run.ID)
			if err != nil || !found || gotRun.Status != "cancelled" {
				t.Fatalf("origin run after shared delete = %+v found=%t err=%v, want cancelled", gotRun, found, err)
			}
			if _, found, err := store.GetTask(ctx, task.ID); err != nil || !found {
				t.Fatalf("task history after shared delete: found=%t err=%v, want retained", found, err)
			}
			if _, found, err := apiHandler.agentChat.Get(ctx, session.ID); err != nil || found {
				t.Fatalf("chat after shared delete: found=%t err=%v, want removed", found, err)
			}
		})
	}
}

func seedUnlinkedChatOriginRun(t *testing.T, ctx context.Context, handler *Handler, store taskstate.Store, suffix string) (chat.Session, types.Task, types.TaskRun) {
	t.Helper()
	now := time.Now().UTC()
	session, err := handler.agentChat.Create(ctx, chat.Session{
		ID:      "chat_origin_" + suffix,
		Title:   "Origin cleanup",
		AgentID: chat.DefaultAgentID,
	})
	if err != nil {
		t.Fatalf("Create chat session: %v", err)
	}
	task, err := store.CreateTask(ctx, types.Task{
		ID:            "task_origin_" + suffix,
		Title:         "Origin cleanup",
		OriginKind:    "chat",
		OriginID:      session.ID,
		ExecutionKind: "agent_loop",
		Status:        "running",
		LatestRunID:   "run_origin_" + suffix,
		CreatedAt:     now,
		UpdatedAt:     now,
		StartedAt:     now,
	})
	if err != nil {
		t.Fatalf("Create origin task: %v", err)
	}
	run, err := store.CreateRun(ctx, types.TaskRun{
		ID:        task.LatestRunID,
		TaskID:    task.ID,
		Number:    1,
		Status:    "running",
		StartedAt: now,
	})
	if err != nil {
		t.Fatalf("Create origin run: %v", err)
	}
	return session, task, run
}

var _ taskstate.Store = (*blockingTerminalTaskStore)(nil)
