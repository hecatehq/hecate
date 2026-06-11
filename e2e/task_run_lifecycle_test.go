//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestTaskRunQueuedLifecycleE2E(t *testing.T) {
	workDir := t.TempDir()
	baseURL := gatewayServer(t,
		"HECATE_BACKEND=sqlite",
		"HECATE_TASK_APPROVAL_POLICIES=",
	)

	body := fmt.Sprintf(`{
		"title": "queued lifecycle e2e",
		"prompt": "complete normally",
		"execution_kind": "shell",
		"shell_command": "printf 'ok\n'",
		"working_directory": %q,
		"sandbox_allowed_root": %q,
		"workspace_mode": "in_place",
		"timeout_ms": 10000
	}`, workDir, workDir)
	created := postJSONDecode[e2eTaskResponse](t, baseURL+"/hecate/v1/tasks", body)
	started := postJSONDecode[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/start", `{}`)

	run := waitForE2ETaskRunTerminal(t, baseURL, created.Data.ID, started.Data.ID, 10*time.Second)
	if run.Status != "completed" {
		t.Fatalf("run status = %q last_error=%q, want completed", run.Status, run.LastError)
	}

	events := getJSON[e2eTaskEventsResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events")
	assertE2EEventOrder(t, events.Data, []string{"run.created", "run.queued", "run.started", "run.finished"})
	assertE2EEventData(t, events.Data, "run.finished", "final_status", "completed")
}

func TestTaskApplicationLayerDirectShellLifecycleE2E(t *testing.T) {
	workDir := t.TempDir()
	baseURL := gatewayServer(t,
		"HECATE_BACKEND=sqlite",
		"HECATE_TASK_APPROVAL_POLICIES=",
	)

	body := fmt.Sprintf(`{
		"title": "application layer shell e2e",
		"execution_kind": "shell",
		"shell_command": "printf 'app-layer\n'",
		"working_directory": %q,
		"sandbox_allowed_root": %q,
		"workspace_mode": "in_place",
		"timeout_ms": 10000
	}`, workDir, workDir)
	created := postJSONDecode[e2eTaskResponse](t, baseURL+"/hecate/v1/tasks", body)
	if created.Data.Status != "queued" {
		t.Fatalf("created status = %q, want queued", created.Data.Status)
	}

	started := postJSONDecode[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/start", `{}`)
	run := waitForE2ETaskRunTerminal(t, baseURL, created.Data.ID, started.Data.ID, 10*time.Second)
	if run.Status != "completed" {
		t.Fatalf("run status = %q last_error=%q, want completed", run.Status, run.LastError)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, baseURL+"/hecate/v1/tasks/"+created.Data.ID, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE task: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE task status = %d, want 204; body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

func TestTaskRunClaimedStartTransitionPopulatesTraceE2E(t *testing.T) {
	workDir := t.TempDir()
	baseURL := gatewayServer(t,
		"HECATE_BACKEND=sqlite",
		"HECATE_TASK_APPROVAL_POLICIES=",
	)

	body := fmt.Sprintf(`{
		"title": "claimed start transition e2e",
		"prompt": "complete normally",
		"execution_kind": "shell",
		"shell_command": "printf 'started\n'",
		"working_directory": %q,
		"sandbox_allowed_root": %q,
		"workspace_mode": "in_place",
		"timeout_ms": 10000
	}`, workDir, workDir)
	created := postJSONDecode[e2eTaskResponse](t, baseURL+"/hecate/v1/tasks", body)
	started := postJSONDecode[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/start", `{}`)

	run := waitForE2ETaskRunTerminal(t, baseURL, created.Data.ID, started.Data.ID, 10*time.Second)
	if run.Status != "completed" {
		t.Fatalf("run status = %q last_error=%q, want completed", run.Status, run.LastError)
	}
	if run.RequestID == "" || run.TraceID == "" || run.RootSpanID == "" {
		t.Fatalf("run ids = request:%q trace:%q span:%q, want populated", run.RequestID, run.TraceID, run.RootSpanID)
	}

	events := getJSON[e2eTaskEventsResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events")
	assertE2EEventOrder(t, events.Data, []string{"run.created", "run.queued", "run.started", "run.finished"})
}

func TestTaskRunExecutionResultPersisterCountsE2E(t *testing.T) {
	workDir := t.TempDir()
	baseURL := gatewayServer(t,
		"HECATE_BACKEND=sqlite",
		"HECATE_TASK_APPROVAL_POLICIES=",
	)

	body := fmt.Sprintf(`{
		"title": "execution result persister e2e",
		"prompt": "persist result counts",
		"execution_kind": "shell",
		"shell_command": "printf 'persisted\n'",
		"working_directory": %q,
		"sandbox_allowed_root": %q,
		"workspace_mode": "in_place",
		"timeout_ms": 10000
	}`, workDir, workDir)
	created := postJSONDecode[e2eTaskResponse](t, baseURL+"/hecate/v1/tasks", body)
	started := postJSONDecode[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/start", `{}`)

	run := waitForE2ETaskRunTerminal(t, baseURL, created.Data.ID, started.Data.ID, 10*time.Second)
	if run.Status != "completed" {
		t.Fatalf("run status = %q last_error=%q, want completed", run.Status, run.LastError)
	}
	if run.StepCount != 1 {
		t.Fatalf("step_count = %d, want 1", run.StepCount)
	}
	if run.ArtifactCount != 2 {
		t.Fatalf("artifact_count = %d, want 2", run.ArtifactCount)
	}

	events := getJSON[e2eTaskEventsResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events")
	assertE2EEventOrder(t, events.Data, []string{"run.created", "run.queued", "run.started", "run.finished"})
}

func TestTaskRunQueueCoordinatorDrainsMultipleTasksE2E(t *testing.T) {
	workDir := t.TempDir()
	baseURL := gatewayServer(t,
		"HECATE_BACKEND=sqlite",
		"HECATE_TASK_APPROVAL_POLICIES=",
		"HECATE_TASK_QUEUE_WORKERS=1",
	)

	type startedTask struct {
		taskID string
		runID  string
	}
	startedTasks := make([]startedTask, 0, 2)
	for i := 0; i < 2; i++ {
		body := fmt.Sprintf(`{
			"title": "queue coordinator e2e %d",
			"prompt": "complete normally",
			"execution_kind": "shell",
			"shell_command": "printf 'task-%d\n'",
			"working_directory": %q,
			"sandbox_allowed_root": %q,
			"workspace_mode": "in_place",
			"timeout_ms": 10000
		}`, i, i, workDir, workDir)
		created := postJSONDecode[e2eTaskResponse](t, baseURL+"/hecate/v1/tasks", body)
		started := postJSONDecode[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/start", `{}`)
		startedTasks = append(startedTasks, startedTask{taskID: created.Data.ID, runID: started.Data.ID})
	}

	for _, task := range startedTasks {
		run := waitForE2ETaskRunTerminal(t, baseURL, task.taskID, task.runID, 10*time.Second)
		if run.Status != "completed" {
			t.Fatalf("run %s status = %q last_error=%q, want completed", task.runID, run.Status, run.LastError)
		}
		events := getJSON[e2eTaskEventsResponse](t, baseURL+"/hecate/v1/tasks/"+task.taskID+"/runs/"+task.runID+"/events")
		assertE2EEventOrder(t, events.Data, []string{"run.created", "run.queued", "run.started", "run.finished"})
	}
}

func TestTaskRunClaimedProcessorFinalizesFailureE2E(t *testing.T) {
	workDir := t.TempDir()
	baseURL := gatewayServer(t,
		"HECATE_BACKEND=sqlite",
		"HECATE_TASK_APPROVAL_POLICIES=",
	)

	body := fmt.Sprintf(`{
		"title": "claimed processor failure e2e",
		"prompt": "fail normally",
		"execution_kind": "shell",
		"shell_command": "exit 7",
		"working_directory": %q,
		"sandbox_allowed_root": %q,
		"workspace_mode": "in_place",
		"timeout_ms": 10000
	}`, workDir, workDir)
	created := postJSONDecode[e2eTaskResponse](t, baseURL+"/hecate/v1/tasks", body)
	started := postJSONDecode[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/start", `{}`)

	run := waitForE2ETaskRunTerminal(t, baseURL, created.Data.ID, started.Data.ID, 10*time.Second)
	if run.Status != "failed" {
		t.Fatalf("run status = %q last_error=%q, want failed", run.Status, run.LastError)
	}

	events := getJSON[e2eTaskEventsResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events")
	assertE2EEventOrder(t, events.Data, []string{"run.created", "run.queued", "run.started", "run.failed"})
	assertE2EEventData(t, events.Data, "run.failed", "message", "exit status 7")
}

func TestTaskRunClaimedExecutionFinalizesOperatorCancelE2E(t *testing.T) {
	workDir := t.TempDir()
	baseURL := gatewayServer(t,
		"HECATE_BACKEND=sqlite",
		"HECATE_TASK_APPROVAL_POLICIES=",
	)

	body := fmt.Sprintf(`{
		"title": "claimed execution cancel e2e",
		"prompt": "cancel while running",
		"execution_kind": "shell",
		"shell_command": "while true; do sleep 1; done",
		"working_directory": %q,
		"sandbox_allowed_root": %q,
		"workspace_mode": "in_place",
		"timeout_ms": 30000
	}`, workDir, workDir)
	created := postJSONDecode[e2eTaskResponse](t, baseURL+"/hecate/v1/tasks", body)
	started := postJSONDecode[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/start", `{}`)

	waitForE2ETaskRunStatus(t, baseURL, created.Data.ID, started.Data.ID, "running", 10*time.Second)
	cancelled := postJSONDecode[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/cancel", `{"reason":"operator stop"}`)
	if cancelled.Data.Status != "cancelled" {
		t.Fatalf("cancel response status = %q last_error=%q, want cancelled", cancelled.Data.Status, cancelled.Data.LastError)
	}

	run := waitForE2ETaskRunTerminal(t, baseURL, created.Data.ID, started.Data.ID, 10*time.Second)
	if run.Status != "cancelled" || run.LastError != "run cancelled: operator stop" {
		t.Fatalf("run status=%q last_error=%q, want cancelled with operator stop", run.Status, run.LastError)
	}

	events := getJSON[e2eTaskEventsResponse](t, baseURL+"/hecate/v1/tasks/"+created.Data.ID+"/runs/"+started.Data.ID+"/events")
	assertE2EEventOrder(t, events.Data, []string{"run.created", "run.queued", "run.started", "run.cancelled"})
	assertE2EEventData(t, events.Data, "run.cancelled", "reason", "run cancelled: operator stop")
	for _, event := range events.Data {
		if event.Type == "run.failed" {
			t.Fatalf("unexpected run.failed after operator cancellation: %+v", event)
		}
	}
}

func waitForE2ETaskRunStatus(t *testing.T, baseURL, taskID, runID, status string, timeout time.Duration) e2eTaskRun {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last e2eTaskRun
	for time.Now().Before(deadline) {
		resp := getJSON[e2eTaskRunResponse](t, baseURL+"/hecate/v1/tasks/"+taskID+"/runs/"+runID)
		last = resp.Data
		if resp.Data.Status == status {
			return resp.Data
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach status %q within %s; last=%+v", runID, status, timeout, last)
	return e2eTaskRun{}
}
