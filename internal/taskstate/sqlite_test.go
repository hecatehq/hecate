package taskstate

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hecate/agent-runtime/internal/storage"
	"github.com/hecate/agent-runtime/pkg/types"
)

func newSQLiteTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        filepath.Join(dir, "taskstate.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	store, err := NewSQLiteStore(context.Background(), client)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	return store
}

func TestSQLiteStore_RejectsNilClient(t *testing.T) {
	_, err := NewSQLiteStore(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestSQLiteStore_BackendName(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	if got := store.Backend(); got != "sqlite" {
		t.Fatalf("Backend() = %q, want %q", got, "sqlite")
	}
}

func TestSQLiteStore_TaskRunStepRoundTrip(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	task := types.Task{
		ID:     "task-1",
		Title:  "demo",
		Status: "queued",
	}
	saved, err := store.CreateTask(ctx, task)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if saved.CreatedAt.IsZero() {
		t.Fatal("CreateTask did not stamp CreatedAt")
	}

	got, ok, err := store.GetTask(ctx, "task-1")
	if err != nil || !ok {
		t.Fatalf("GetTask: ok=%v err=%v", ok, err)
	}
	if got.Title != "demo" {
		t.Fatalf("GetTask round-trip mismatch: %+v", got)
	}

	run := types.TaskRun{
		ID:        "run-1",
		TaskID:    "task-1",
		Number:    1,
		Status:    "running",
		StartedAt: time.Now().UTC(),
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	gotRun, ok, err := store.GetRun(ctx, "task-1", "run-1")
	if err != nil || !ok {
		t.Fatalf("GetRun: ok=%v err=%v", ok, err)
	}
	if gotRun.Status != "running" || gotRun.Number != 1 {
		t.Fatalf("GetRun round-trip mismatch: %+v", gotRun)
	}

	for i, status := range []string{"running", "completed"} {
		step := types.TaskStep{
			ID:        "step-" + status,
			TaskID:    "task-1",
			RunID:     "run-1",
			Index:     i,
			Status:    status,
			StartedAt: time.Now().UTC(),
		}
		if _, err := store.AppendStep(ctx, step); err != nil {
			t.Fatalf("AppendStep(%s): %v", status, err)
		}
	}
	steps, err := store.ListSteps(ctx, "run-1")
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("ListSteps len = %d, want 2", len(steps))
	}
	// step_index ASC ordering: index 0 first.
	if steps[0].Index != 0 || steps[1].Index != 1 {
		t.Fatalf("ListSteps ordering: %+v", steps)
	}
}

func TestSQLiteStore_ListTasksFilterAndLimit(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	// Three tasks with staggered updated_at so ordering is
	// deterministic.
	now := time.Now().UTC()
	for i, spec := range []struct {
		id     string
		status string
		ts     time.Time
	}{
		{"t-a1", "queued", now.Add(-3 * time.Minute)},
		{"t-a2", "running", now.Add(-2 * time.Minute)},
		{"t-b1", "queued", now.Add(-1 * time.Minute)},
	} {
		_, err := store.CreateTask(ctx, types.Task{
			ID:        spec.id,
			Status:    spec.status,
			CreatedAt: spec.ts,
			UpdatedAt: spec.ts,
		})
		if err != nil {
			t.Fatalf("CreateTask[%d]: %v", i, err)
		}
	}

	all, err := store.ListTasks(ctx, TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(all): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListTasks all len = %d, want 3", len(all))
	}
	// updated_at DESC: t-b1 first.
	if all[0].ID != "t-b1" {
		t.Fatalf("ListTasks ordering: got first %q, want t-b1", all[0].ID)
	}

	limited, err := store.ListTasks(ctx, TaskFilter{Limit: 2})
	if err != nil {
		t.Fatalf("ListTasks(limit): %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("ListTasks limit len = %d, want 2", len(limited))
	}

	statused, err := store.ListTasks(ctx, TaskFilter{Status: "queued"})
	if err != nil {
		t.Fatalf("ListTasks(status): %v", err)
	}
	if len(statused) != 2 {
		t.Fatalf("ListTasks status len = %d, want 2", len(statused))
	}
}

func TestSQLiteStore_ApprovalRoundTrip(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	if _, err := store.CreateTask(ctx, types.Task{ID: "task-ap", Status: "running"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	approval := types.TaskApproval{
		ID:          "ap-1",
		TaskID:      "task-ap",
		RunID:       "run-ap",
		Kind:        "shell",
		Status:      "pending",
		RequestedBy: "agent",
		CreatedAt:   time.Now().UTC(),
	}
	if _, err := store.CreateApproval(ctx, approval); err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}

	got, ok, err := store.GetApproval(ctx, "task-ap", "ap-1")
	if err != nil || !ok {
		t.Fatalf("GetApproval: ok=%v err=%v", ok, err)
	}
	if got.Status != "pending" || got.Kind != "shell" {
		t.Fatalf("GetApproval round-trip mismatch: %+v", got)
	}

	// Resolve.
	got.Status = "approved"
	got.ResolvedBy = "operator"
	got.ResolvedAt = time.Now().UTC()
	got.ResolutionNote = "looks fine"
	if _, err := store.UpdateApproval(ctx, got); err != nil {
		t.Fatalf("UpdateApproval: %v", err)
	}

	resolved, ok, err := store.GetApproval(ctx, "task-ap", "ap-1")
	if err != nil || !ok {
		t.Fatalf("GetApproval after resolve: ok=%v err=%v", ok, err)
	}
	if resolved.Status != "approved" || resolved.ResolvedBy != "operator" || resolved.ResolutionNote != "looks fine" {
		t.Fatalf("resolution not persisted: %+v", resolved)
	}

	approvals, err := store.ListApprovals(ctx, "task-ap")
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	if len(approvals) != 1 || approvals[0].Status != "approved" {
		t.Fatalf("ListApprovals: %+v", approvals)
	}
}

func TestSQLiteStore_ArtifactRoundTrip(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	artifact := types.TaskArtifact{
		ID:          "art-1",
		TaskID:      "task-art",
		RunID:       "run-art",
		StepID:      "step-art",
		Kind:        "log",
		Name:        "build.log",
		MimeType:    "text/plain",
		StorageKind: "inline",
		ContentText: "hello world",
		SizeBytes:   11,
		Status:      "ready",
		CreatedAt:   time.Now().UTC(),
	}
	if _, err := store.CreateArtifact(ctx, artifact); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	got, ok, err := store.GetArtifact(ctx, "task-art", "art-1")
	if err != nil || !ok {
		t.Fatalf("GetArtifact: ok=%v err=%v", ok, err)
	}
	if got.ContentText != "hello world" || got.MimeType != "text/plain" {
		t.Fatalf("GetArtifact round-trip mismatch: %+v", got)
	}

	listed, err := store.ListArtifacts(ctx, ArtifactFilter{TaskID: "task-art"})
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(listed) != 1 || listed[0].ContentText != "hello world" {
		t.Fatalf("ListArtifacts: %+v", listed)
	}

	// Filter by kind that doesn't match — should be empty.
	missing, err := store.ListArtifacts(ctx, ArtifactFilter{TaskID: "task-art", Kind: "trace"})
	if err != nil {
		t.Fatalf("ListArtifacts(kind=trace): %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("ListArtifacts(kind=trace) len = %d, want 0", len(missing))
	}
}

func TestSQLiteStore_RunEventsAppendAndList(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
			TaskID:    "task-evt",
			RunID:     "run-evt",
			EventType: "step.completed",
			Data:      map[string]any{"i": i},
			RequestID: "req-evt",
		})
		if err != nil {
			t.Fatalf("AppendRunEvent[%d]: %v", i, err)
		}
	}

	events, err := store.ListRunEvents(ctx, "task-evt", "run-evt", 0, 100)
	if err != nil {
		t.Fatalf("ListRunEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("ListRunEvents len = %d, want 3", len(events))
	}
	// sequence ASC, so the first event has the smallest sequence.
	if events[0].Sequence >= events[2].Sequence {
		t.Fatalf("sequence ordering: %+v", events)
	}
	// Cursor: afterSequence skips earlier rows.
	tail, err := store.ListRunEvents(ctx, "task-evt", "run-evt", events[0].Sequence, 100)
	if err != nil {
		t.Fatalf("ListRunEvents(cursor): %v", err)
	}
	if len(tail) != 2 {
		t.Fatalf("ListRunEvents(cursor) len = %d, want 2", len(tail))
	}
}

func TestSQLiteStore_ListEventsCrossRunFilters(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	// Three tasks/runs producing four events of varying types.
	// We want to confirm: (1) cross-run listing returns everything,
	// (2) event_type filter narrows correctly, (3) task_ids filter
	// narrows correctly, (4) afterSequence cursor works the same as
	// the per-run lister.
	mustAppend := func(taskID, runID, eventType string) types.TaskRunEvent {
		evt, err := store.AppendRunEvent(ctx, types.TaskRunEvent{
			TaskID: taskID, RunID: runID, EventType: eventType,
		})
		if err != nil {
			t.Fatalf("AppendRunEvent: %v", err)
		}
		return evt
	}
	e1 := mustAppend("t-A", "r-A", "agent.turn.completed")
	e2 := mustAppend("t-A", "r-A", "run.finished")
	e3 := mustAppend("t-B", "r-B", "agent.turn.completed")
	e4 := mustAppend("t-C", "r-C", "approval.requested")
	_ = e1
	_ = e2
	_ = e3
	_ = e4

	t.Run("no filter returns all events globally ordered", func(t *testing.T) {
		events, err := store.ListEvents(ctx, EventFilter{})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 4 {
			t.Fatalf("len = %d, want 4", len(events))
		}
		for i := 1; i < len(events); i++ {
			if events[i].Sequence <= events[i-1].Sequence {
				t.Errorf("not sequence-ascending at %d: %+v", i, events)
			}
		}
	})

	t.Run("event_type filter matches OR semantics", func(t *testing.T) {
		events, err := store.ListEvents(ctx, EventFilter{EventTypes: []string{"agent.turn.completed"}})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 2 {
			t.Fatalf("len = %d, want 2 (two agent.turn.completed)", len(events))
		}
		for _, e := range events {
			if e.EventType != "agent.turn.completed" {
				t.Errorf("unexpected type %q", e.EventType)
			}
		}
	})

	t.Run("task_ids filter restricts to listed tasks", func(t *testing.T) {
		events, err := store.ListEvents(ctx, EventFilter{TaskIDs: []string{"t-A", "t-C"}})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 3 {
			t.Fatalf("len = %d, want 3 (t-A: 2, t-C: 1)", len(events))
		}
		for _, e := range events {
			if e.TaskID != "t-A" && e.TaskID != "t-C" {
				t.Errorf("unexpected task %q in result", e.TaskID)
			}
		}
	})

	t.Run("after_sequence cursor skips older rows", func(t *testing.T) {
		events, err := store.ListEvents(ctx, EventFilter{AfterSequence: e2.Sequence})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 2 {
			t.Fatalf("len = %d, want 2 (e3, e4)", len(events))
		}
		if events[0].Sequence != e3.Sequence {
			t.Errorf("first sequence = %d, want %d", events[0].Sequence, e3.Sequence)
		}
	})

	t.Run("combined filters AND together", func(t *testing.T) {
		events, err := store.ListEvents(ctx, EventFilter{
			EventTypes: []string{"agent.turn.completed"},
			TaskIDs:    []string{"t-B"},
		})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 1 || events[0].TaskID != "t-B" {
			t.Errorf("expected one event from t-B, got %+v", events)
		}
	})

	t.Run("limit caps the response size", func(t *testing.T) {
		events, err := store.ListEvents(ctx, EventFilter{Limit: 2})
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if len(events) != 2 {
			t.Fatalf("len = %d, want 2 (limit honored)", len(events))
		}
	})
}

func TestSQLiteStore_ListRunsByFilterStatusSet(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	for i, status := range []string{"queued", "running", "completed", "failed"} {
		_, err := store.CreateRun(ctx, types.TaskRun{
			ID:        "run-" + status,
			TaskID:    "task-rfilter",
			Number:    i + 1,
			Status:    status,
			StartedAt: now.Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("CreateRun(%s): %v", status, err)
		}
	}

	got, err := store.ListRunsByFilter(ctx, RunFilter{
		TaskID:   "task-rfilter",
		Statuses: []string{"running", "completed"},
	})
	if err != nil {
		t.Fatalf("ListRunsByFilter: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListRunsByFilter len = %d, want 2", len(got))
	}
	for _, run := range got {
		if run.Status != "running" && run.Status != "completed" {
			t.Fatalf("unexpected status in filtered set: %q", run.Status)
		}
	}

	// Limit clamps the result.
	limited, err := store.ListRunsByFilter(ctx, RunFilter{TaskID: "task-rfilter", Limit: 2})
	if err != nil {
		t.Fatalf("ListRunsByFilter(limit): %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("ListRunsByFilter(limit) len = %d, want 2", len(limited))
	}
}

func TestSQLiteStore_DeleteTaskCascades(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	if _, err := store.CreateTask(ctx, types.Task{ID: "task-del", Status: "queued"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CreateRun(ctx, types.TaskRun{ID: "run-del", TaskID: "task-del", Status: "running", StartedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := store.AppendStep(ctx, types.TaskStep{ID: "step-del", TaskID: "task-del", RunID: "run-del", Status: "running"}); err != nil {
		t.Fatalf("AppendStep: %v", err)
	}

	if err := store.DeleteTask(ctx, "task-del"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	if _, ok, _ := store.GetTask(ctx, "task-del"); ok {
		t.Fatal("task still present after delete")
	}
	if _, ok, _ := store.GetRun(ctx, "task-del", "run-del"); ok {
		t.Fatal("run still present after delete")
	}
	steps, err := store.ListSteps(ctx, "run-del")
	if err != nil {
		t.Fatalf("ListSteps: %v", err)
	}
	if len(steps) != 0 {
		t.Fatalf("steps still present after delete: %d", len(steps))
	}
}

// TestSQLiteStore_TaskMCPServersRoundTrip pins that a Task with a
// fully-populated MCPServers slice survives a round-trip through
// the sqlite backend's JSON-blob storage path. The pkg/types/task
// JSON-round-trip test pins the marshaling contract on the type
// itself; this test pins the actual storage layer (write JSON,
// read JSON, deep-equal). Postgres uses the identical
// json.Marshal/json.Unmarshal pair on its blob column, so this
// test covers that backend's contract by construction — same code
// path, no per-backend variance.
//
// Catches: a regression where someone changes a Task field tag,
// adds an unmarshal hook that mishandles a default value, or
// makes a column type incompatible with the existing JSON shape
// would silently corrupt every persisted MCP config; this test
// fails first.
func TestSQLiteStore_TaskMCPServersRoundTrip(t *testing.T) {
	t.Parallel()
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	task := types.Task{
		ID:            "task-mcp",
		Title:         "MCP store round-trip",
		Status:        "queued",
		ExecutionKind: "agent_loop",
		MCPServers: []types.MCPServerConfig{
			{
				Name:    "fs",
				Command: "bunx",
				Args:    []string{"--bun", "@modelcontextprotocol/server-filesystem", "/workspace"},
				Env: map[string]string{
					"DEBUG_TOKEN": "$DEBUG_TOKEN",
					"AUTH":        "enc:abc123base64=",
					"NODE_ENV":    "production",
				},
				ApprovalPolicy: types.MCPApprovalAuto,
			},
			{
				Name: "github",
				URL:  "https://api.example.com/mcp",
				Headers: map[string]string{
					"Authorization": "Bearer $GITHUB_TOKEN",
					"X-Trace":       "on",
				},
				ApprovalPolicy: types.MCPApprovalRequireApproval,
			},
			{
				Name:           "blocked",
				Command:        "npx",
				Args:           []string{"@vendor/dangerous"},
				ApprovalPolicy: types.MCPApprovalBlock,
			},
		},
	}

	if _, err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	got, ok, err := store.GetTask(ctx, task.ID)
	if err != nil || !ok {
		t.Fatalf("GetTask: ok=%v err=%v", ok, err)
	}

	// Field-by-field check on the MCP slice rather than reflect.DeepEqual
	// on the whole Task: the store stamps CreatedAt / UpdatedAt /
	// other timestamps, so a whole-Task DeepEqual would fail on
	// fields the test doesn't care about.
	if len(got.MCPServers) != len(task.MCPServers) {
		t.Fatalf("MCPServers count: got %d, want %d", len(got.MCPServers), len(task.MCPServers))
	}
	for i, want := range task.MCPServers {
		gotEntry := got.MCPServers[i]
		if gotEntry.Name != want.Name {
			t.Errorf("[%d] Name = %q, want %q", i, gotEntry.Name, want.Name)
		}
		if gotEntry.Command != want.Command {
			t.Errorf("[%d] Command = %q, want %q", i, gotEntry.Command, want.Command)
		}
		if gotEntry.URL != want.URL {
			t.Errorf("[%d] URL = %q, want %q", i, gotEntry.URL, want.URL)
		}
		if gotEntry.ApprovalPolicy != want.ApprovalPolicy {
			t.Errorf("[%d] ApprovalPolicy = %q, want %q", i, gotEntry.ApprovalPolicy, want.ApprovalPolicy)
		}
		if !equalStringSlice(gotEntry.Args, want.Args) {
			t.Errorf("[%d] Args = %+v, want %+v", i, gotEntry.Args, want.Args)
		}
		if !equalStringMap(gotEntry.Env, want.Env) {
			t.Errorf("[%d] Env = %+v, want %+v", i, gotEntry.Env, want.Env)
		}
		if !equalStringMap(gotEntry.Headers, want.Headers) {
			t.Errorf("[%d] Headers = %+v, want %+v", i, gotEntry.Headers, want.Headers)
		}
	}
}

// equalStringSlice / equalStringMap are tiny helpers because
// reflect.DeepEqual treats nil and empty slice/map as different —
// for round-trip tests we want to consider them equivalent (empty
// JSON arrays and missing keys end up as nil after unmarshal).
func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
